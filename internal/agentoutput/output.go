// Package agentoutput separates CLI telemetry from untrusted assistant text.
// It accepts bounded JSONL events, never a model's claimed usage in its answer.
package agentoutput

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/sushidev-team/lola/internal/usage"
)

const MaxBytes = 8 << 20
const maxResult = 64 << 10

// Decode preserves observed counters even when the run lacks a successful
// completion. The caller must persist the observation before trusting text.
func Decode(kind string, body []byte, now time.Time) ([]byte, usage.Observation, error) {
	o := usage.Observation{Agent: kind, Status: "missing", CostStatus: "unavailable", Reason: "CLI emitted no supported usage telemetry", At: now}
	if len(body) > MaxBytes {
		return nil, o, errors.New("agent event output exceeds limit")
	}
	s := state{kind: kind, items: map[string]usage.Totals{}, textParts: map[string]string{}}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	var parseErr error
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		if s.events++; s.events > 10000 {
			parseErr = errors.New("too many agent events")
			break
		}
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.Type == "" {
			parseErr = errors.New("invalid agent event envelope")
			break
		}
		if err := s.consume(e); err != nil {
			parseErr = err
			break
		}
	}
	if scanner.Err() != nil {
		parseErr = errors.New("agent event exceeds line limit")
	}
	total := s.total()
	if s.observed {
		o.Status = "partial"
		o.Reason = "CLI-reported usage; interrupted or unsupported events may omit consumption"
		if s.costKnown {
			o.CostStatus = "estimate"
		}
		o.Days = map[string]usage.Totals{now.Local().Format(usage.DayFormat): total}
	}
	if parseErr != nil {
		return nil, o, parseErr
	}
	if s.failed || !s.complete || len(s.text) == 0 {
		return nil, o, errors.New("agent stream has no successful final result")
	}
	return []byte(s.text), o, nil
}

type counters struct {
	Input  *int64 `json:"input_tokens"`
	Output *int64 `json:"output_tokens"`
	Cached int64  `json:"cached_input_tokens"`
	Read   int64  `json:"cache_read_input_tokens"`
	Write  int64  `json:"cache_creation_input_tokens"`
}
type event struct {
	Type    string    `json:"type"`
	Subtype string    `json:"subtype"`
	Result  string    `json:"result"`
	IsError *bool     `json:"is_error"`
	Usage   *counters `json:"usage"`
	Cost    *float64  `json:"total_cost_usd"`
	Message *struct {
		ID    string    `json:"id"`
		Usage *counters `json:"usage"`
	} `json:"message"`
	Item *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Part *struct {
		ID        string   `json:"id"`
		Type      string   `json:"type"`
		MessageID string   `json:"messageID"`
		Text      string   `json:"text"`
		Reason    string   `json:"reason"`
		Cost      *float64 `json:"cost"`
		Tokens    *struct {
			Input     *int64 `json:"input"`
			Output    *int64 `json:"output"`
			Reasoning int64  `json:"reasoning"`
			Cache     struct {
				Read  int64 `json:"read"`
				Write int64 `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
	} `json:"part"`
}
type state struct {
	kind, text, message                   string
	events                                int
	complete, failed, observed, costKnown bool
	items                                 map[string]usage.Totals
	final                                 usage.Totals
	textParts                             map[string]string
	textOrder                             []string
	turn                                  int
	turnOpen                              bool
}

func (s *state) consume(e event) error {
	switch s.kind {
	case "claude":
		if e.Type == "assistant" && e.Message != nil && e.Message.Usage != nil {
			if e.Message.ID == "" {
				return errors.New("usage message identity missing")
			}
			t, ok, err := count(e.Message.Usage, false)
			if err != nil {
				return err
			}
			if ok {
				s.observe(e.Message.ID, t)
			}
		}
		if e.Type == "result" {
			t, ok, err := count(e.Usage, false)
			if err != nil {
				return err
			}
			if ok {
				s.final = maximum(s.final, t)
				s.observed = true
			}
			if e.Cost != nil {
				if !validCost(*e.Cost) {
					return errors.New("invalid reported cost")
				}
				s.final.CostUSD = max(s.final.CostUSD, *e.Cost)
				s.costKnown = true
				s.observed = true
			}
			if s.complete && s.text != e.Result {
				return errors.New("conflicting final results")
			}
			if err := s.setText(e.Result); err != nil {
				return err
			}
			s.complete = true
			s.failed = s.failed || e.IsError == nil || *e.IsError || e.Subtype != "success"
		}
	case "codex":
		if e.Type == "turn.started" {
			if s.turnOpen {
				return errors.New("overlapping Codex turns")
			}
			s.turn++
			s.turnOpen = true
			s.complete = false
			s.text = ""
		}
		if e.Type == "item.completed" && e.Item != nil && e.Item.Type == "agent_message" {
			if err := s.setText(e.Item.Text); err != nil {
				return err
			}
		}
		if e.Type == "turn.completed" {
			t, ok, err := count(e.Usage, true)
			if err != nil {
				return err
			}
			if ok {
				s.observe("turn:"+strconv.Itoa(s.turn), t)
			}
			s.turnOpen = false
			s.complete = true
		}
		if e.Type == "error" || e.Type == "turn.failed" {
			s.failed = true
		}
	case "opencode":
		if e.Type == "step_start" {
			s.complete = false
		}
		p := e.Part
		if e.Type == "text" && p != nil && p.Type == "text" {
			if p.MessageID == "" || p.ID == "" {
				return errors.New("text message identity missing")
			}
			if s.message != p.MessageID {
				s.complete = false
				s.text = ""
				s.message = p.MessageID
				s.textParts = map[string]string{}
				s.textOrder = nil
			}
			if _, exists := s.textParts[p.ID]; !exists {
				s.textOrder = append(s.textOrder, p.ID)
			}
			s.textParts[p.ID] = p.Text
			text := ""
			for _, id := range s.textOrder {
				text += s.textParts[id]
				if len(text) > maxResult {
					return errors.New("agent result exceeds limit")
				}
			}
			if err := s.setText(text); err != nil {
				return err
			}
		}
		if e.Type == "step_finish" && p != nil && p.Type == "step-finish" {
			if p.ID == "" {
				return errors.New("step identity missing")
			}
			t := usage.Totals{}
			observed := false
			if u := p.Tokens; u != nil && u.Input != nil && u.Output != nil {
				if u.Reasoning < 0 || u.Reasoning > 1e12 {
					return errors.New("invalid reasoning counters")
				}
				t = usage.Totals{Input: *u.Input, Output: *u.Output + u.Reasoning, CacheRead: u.Cache.Read, CacheWrite: u.Cache.Write}
				if !usage.ValidTotals(t) {
					return errors.New("invalid step counters")
				}
				observed = true
			}
			if p.Cost != nil {
				if !validCost(*p.Cost) {
					return errors.New("invalid reported cost")
				}
				t.CostUSD = *p.Cost
				s.costKnown = true
				observed = true
			}
			if observed {
				s.observe(p.ID, t)
			}
			if p.Reason == "stop" {
				if p.MessageID == "" || p.MessageID != s.message {
					return errors.New("final step has no matching assistant result")
				}
				s.complete = true
			}
		}
		if e.Type == "error" {
			s.failed = true
		}
	default:
		return errors.New("unknown agent event format")
	}
	return nil
}
func (s *state) observe(id string, t usage.Totals) {
	s.items[id] = maximum(s.items[id], t)
	s.observed = true
}
func (s *state) setText(text string) error {
	if len(text) > maxResult {
		return errors.New("agent result exceeds limit")
	}
	s.text = text
	return nil
}
func (s state) total() (out usage.Totals) {
	keys := make([]string, 0, len(s.items))
	for k := range s.items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.Add(s.items[k])
	}
	return maximum(out, s.final)
}
func maximum(a, b usage.Totals) usage.Totals {
	return usage.Totals{Input: max(a.Input, b.Input), Output: max(a.Output, b.Output), CacheRead: max(a.CacheRead, b.CacheRead), CacheWrite: max(a.CacheWrite, b.CacheWrite), CostUSD: max(a.CostUSD, b.CostUSD)}
}
func count(c *counters, codex bool) (usage.Totals, bool, error) {
	if c == nil || c.Input == nil || c.Output == nil {
		return usage.Totals{}, false, nil
	}
	t := usage.Totals{Input: *c.Input, Output: *c.Output, CacheRead: c.Read, CacheWrite: c.Write}
	if codex {
		if c.Cached < 0 || c.Cached > *c.Input {
			return t, false, errors.New("invalid cached counters")
		}
		t.Input -= c.Cached
		t.CacheRead = c.Cached
	}
	if !usage.ValidTotals(t) {
		return t, false, errors.New("invalid agent usage counters")
	}
	return t, true, nil
}
func validCost(v float64) bool { return v >= 0 && v <= 1e9 && !math.IsNaN(v) && !math.IsInf(v, 0) }
