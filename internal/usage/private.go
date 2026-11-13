package usage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Observation is reported telemetry, not an invoice or an exact spending cap.
// Private transcripts are mutable and may be delayed or incomplete. Missing
// telemetry must never be interpreted as a known zero.
type Observation struct {
	Agent      string            `json:"agent"`
	Days       map[string]Totals `json:"days,omitempty"`
	Status     string            `json:"status"`      // missing or partial
	CostStatus string            `json:"cost_status"` // unavailable or estimate
	Reason     string            `json:"reason"`
	At         time.Time         `json:"at"`
}

// Private reads only an attempt's captured agent logs. launch must be the
// daemon-owned launch directory, not a worker-supplied transcript path. A
// confined, bounded copy keeps existing scanners away from mutable symlinks and
// prevents SQLite from opening the worker's live database or adjacent files.
func Private(ctx context.Context, launch, agent string, now time.Time) Observation {
	out := Observation{Agent: agent, Status: "missing", CostStatus: "unavailable", Reason: "No private agent usage reported", At: now}
	root, err := os.OpenRoot(launch)
	if err != nil {
		return out
	}
	defer root.Close()
	logs, err := root.OpenRoot("workspace/.agent")
	if err != nil {
		out.Reason = "Private agent logs unavailable"
		return out
	}
	defer logs.Close()
	temp, err := os.MkdirTemp("", "lola-usage-")
	if err != nil {
		out.Reason = "Usage snapshot unavailable"
		return out
	}
	defer os.RemoveAll(temp)
	prefix := ""
	switch agent {
	case "claude":
		prefix = "claude/projects"
	case "codex":
		prefix = "codex/sessions"
	case "opencode":
		prefix = "data/opencode"
	default:
		out.Reason = "Agent usage format unavailable"
		return out
	}
	source, err := logs.OpenRoot(prefix)
	if err != nil {
		return out
	}
	defer source.Close()
	budget := copyBudget{bytes: 64 << 20, entries: 4096}
	if err := copyUsage(ctx, source, temp, agent, 0, &budget); err != nil {
		out.Reason = "Private usage snapshot incomplete or exceeds scan limits"
		return out
	}
	workspace := filepath.Join(launch, "workspace")
	switch agent {
	case "claude":
		entries, err := os.ReadDir(temp)
		if err != nil {
			return out
		}
		scanner := NewScanner()
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			days, err := scanner.ScanDir(filepath.Join(temp, entry.Name()))
			if err != nil {
				out.Reason = "Private transcript scan incomplete"
				return out
			}
			out.Days = MergeDays(out.Days, days)
		}
		out.CostStatus = "estimate"
	case "codex":
		scanner := NewCodexScanner(workspace)
		scanner.Scan(temp)
		out.Days = scanner.For(workspace)
	case "opencode":
		scanner := NewOpencodeScanner(workspace)
		scanner.RunSQL = privateSQL
		if err := scanner.Scan(ctx, filepath.Join(temp, "opencode.db"), now); err != nil {
			out.Reason = "Private OpenCode usage query unavailable"
			return out
		}
		out.Days = scanner.For(workspace)
		out.CostStatus = "estimate"
	}
	// Every returned total is bounded before it can enter a persistent budget.
	for day, t := range out.Days {
		if _, err := time.Parse(DayFormat, day); err != nil || !ValidTotals(t) {
			out.Days = nil
			out.Reason = "Invalid private usage counters"
			out.CostStatus = "unavailable"
			return out
		}
	}
	if Sum(out.Days).CostUSD == 0 {
		out.CostStatus = "unavailable"
	}
	if Sum(out.Days).Tokens() > 0 {
		out.Status = "partial"
		out.Reason = "Agent-reported usage; logs may be delayed, incomplete or modified"
	} else {
		out.CostStatus = "unavailable"
	}
	return out
}

// Bound arithmetic as well as input bytes. These limits are deliberately far
// above a plausible single run, while keeping malicious counters from wrapping.
func ValidTotals(t Totals) bool {
	return t.Slots >= 0 && t.Slots <= 1e7 && t.Input >= 0 && t.Output >= 0 && t.CacheRead >= 0 && t.CacheWrite >= 0 && t.Input <= 1e12 && t.Output <= 1e12 && t.CacheRead <= 1e12 && t.CacheWrite <= 1e12 && t.CostUSD >= 0 && t.CostUSD <= 1e9 && !math.IsNaN(t.CostUSD) && !math.IsInf(t.CostUSD, 0)
}

type copyBudget struct {
	bytes   int64
	entries int
}

func copyUsage(ctx context.Context, root *os.Root, destination, agent string, depth int, b *copyBudget) error {
	if depth > 8 || ctx.Err() != nil {
		return errors.New("usage scan bounded")
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(b.entries + 1)
	dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	b.entries -= len(entries)
	if b.entries < 0 {
		return errors.New("too many usage files")
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := entry.Name()
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("usage symlink refused")
		}
		target := filepath.Join(destination, name)
		if info.IsDir() {
			child, err := root.OpenRoot(name)
			if err != nil {
				return err
			}
			if err = os.Mkdir(target, 0700); err == nil {
				err = copyUsage(ctx, child, target, agent, depth+1, b)
			}
			child.Close()
			if err != nil {
				return err
			}
			continue
		}
		selected := strings.HasSuffix(name, ".jsonl")
		if agent == "opencode" {
			selected = depth == 0 && (name == "opencode.db" || name == "opencode.db-wal")
		}
		if !selected {
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > b.bytes {
			return errors.New("usage file exceeds bounds")
		}
		f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		actual, err := f.Stat()
		if err != nil || !actual.Mode().IsRegular() {
			f.Close()
			return errors.New("usage file changed")
		}
		body, err := io.ReadAll(io.LimitReader(f, b.bytes+1))
		f.Close()
		if err != nil {
			return err
		}
		b.bytes -= int64(len(body))
		if b.bytes < 0 {
			return errors.New("usage byte limit")
		}
		if agent != "opencode" {
			if err := validatePrivateJSONL(ctx, body, agent); err != nil {
				return err
			}
		}
		if err := os.WriteFile(target, body, 0600); err != nil {
			return err
		}
	}
	return nil
}

func privateSQL(ctx context.Context, db, query string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-safe", "-readonly", "-json", db)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Dir(db)}
	cmd.Stdin = strings.NewReader("PRAGMA trusted_schema=OFF;\n" + query)
	out := &privateOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, errors.New("usage query failed")
	}
	if out.overflow {
		return nil, errors.New("usage query exceeded output bound")
	}
	var rows []map[string]json.RawMessage
	if len(bytes.TrimSpace(out.Bytes())) > 0 {
		if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
			return nil, errors.New("invalid usage query result")
		}
	}
	for _, row := range rows {
		for _, key := range []string{"i", "o", "r", "cr", "cw"} {
			var value int64
			if err := json.Unmarshal(row[key], &value); err != nil || value < 0 || value > 1e12 {
				return nil, errors.New("invalid usage query counter")
			}
		}
		var cost float64
		if err := json.Unmarshal(row["cost"], &cost); err != nil || cost < 0 || cost > 1e9 || math.IsNaN(cost) || math.IsInf(cost, 0) {
			return nil, errors.New("invalid usage query cost")
		}
	}
	return out.Bytes(), nil
}

// Validate individual records before the legacy scanners sum them. Checking
// only the final total would allow negative or overflowing counters to cancel
// valid usage. An unfinished trailing line is left for the next observation.
func validatePrivateJSONL(ctx context.Context, body []byte, agent string) error {
	if end := bytes.LastIndexByte(body, '\n'); end >= 0 {
		body = body[:end+1]
	} else {
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), maxCodexLine)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		if agent == "claude" {
			var r record
			if json.Unmarshal(line, &r) != nil || r.Message == nil || r.Message.Usage == nil {
				continue
			}
			u := r.Message.Usage
			if !ValidTotals(Totals{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}) {
				return errors.New("invalid private usage record")
			}
			if u.CacheCreation != nil && (u.CacheCreation.Hour < 0 || u.CacheCreation.Hour > u.CacheWrite) {
				return errors.New("invalid private cache counters")
			}
		} else {
			var r struct {
				Payload struct {
					Info *struct {
						Total *codexUsage `json:"total_token_usage"`
					} `json:"info"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &r) != nil || r.Payload.Info == nil || r.Payload.Info.Total == nil {
				continue
			}
			u := r.Payload.Info.Total
			if !ValidTotals(Totals{Input: u.Input, Output: u.Output, CacheRead: u.Cached, CacheWrite: u.Write}) || u.Cached+u.Write > u.Input {
				return errors.New("invalid private cumulative counters")
			}
		}
	}
	return scanner.Err()
}

type privateOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *privateOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := (8 << 20) - b.Len()
	if len(p) > left {
		p = p[:max(0, left)]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
