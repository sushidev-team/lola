// Package comparison validates portable, versioned evaluation records and emits
// deterministic paired reports. It performs no execution or external writes.
package comparison

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

const Version = 1
const MaxBytes = 512 << 10

var Metrics = []string{"repeat_corrections", "useful_findings", "dismissed_findings", "unassessed_findings", "regressions_caught", "human_intervention_seconds", "cost_usd"}
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)

type Bundle struct {
	Version  int    `json:"version"`
	ID       string `json:"id"`
	Question string `json:"question"`
	Pairs    []Pair `json:"pairs"`
	Runs     []Run  `json:"runs"`
}
type Repository struct {
	Name string `json:"name"`
	Base string `json:"base"`
}
type Limits struct {
	AgentCalls     int     `json:"agent_calls"`
	RuntimeSeconds int     `json:"runtime_seconds"`
	Tokens         int64   `json:"tokens"`
	SpendUSD       float64 `json:"spend_usd"`
}

// A pair is one predeclared task/repetition. Declare every planned pair before
// execution, including tasks for which one or both runs later fail to finish.
type Pair struct {
	ID           string       `json:"id"`
	Task         string       `json:"task"`
	Acceptance   []string     `json:"acceptance"`
	Repositories []Repository `json:"repositories"`
	Limits       Limits       `json:"limits"`
	Intervention string       `json:"intervention"`
}

// Measurement distinguishes an unavailable value from a measured zero. Evidence
// identifies the retained observation/assessment; the report does not verify it.
type Measurement struct {
	Value    *float64 `json:"value"`
	Status   string   `json:"status"` // observed, reported, estimated, unavailable
	Evidence []string `json:"evidence"`
	Reason   string   `json:"reason,omitempty"`
}
type Invocation struct {
	Agent          string `json:"agent"`
	RequestedModel string `json:"requested_model"`
	ResolvedModel  string `json:"resolved_model"`
	PromptSHA256   string `json:"prompt_sha256"`
	Source         string `json:"source"`
	UnknownReason  string `json:"unknown_reason,omitempty"`
}
type Lesson struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	Source  string `json:"source"`
}
type Reviewer struct {
	Profile       string     `json:"profile"`
	Provider      string     `json:"provider"`
	RubricVersion string     `json:"rubric_version"`
	Invocation    Invocation `json:"invocation"`
}
type Run struct {
	ID              string                 `json:"id"`
	PairID          string                 `json:"pair_id"`
	Arm             string                 `json:"arm"` // baseline or treatment
	PairSHA256      string                 `json:"pair_sha256"`
	Outcome         string                 `json:"outcome"` // accepted, rejected, incomplete
	OutcomeEvidence []string               `json:"outcome_evidence"`
	StartedAt       time.Time              `json:"started_at"`
	EndedAt         time.Time              `json:"ended_at"`
	Worker          Invocation             `json:"worker"`
	Lessons         []Lesson               `json:"lessons"`
	Reviewers       []Reviewer             `json:"reviewers"`
	AgentCalls      Measurement            `json:"agent_calls"`
	Tokens          Measurement            `json:"tokens"`
	Metrics         map[string]Measurement `json:"metrics"`
}
type PairResult struct {
	ID            string   `json:"id"`
	SHA256        string   `json:"sha256"`
	BaselineRun   string   `json:"baseline_run,omitempty"`
	TreatmentRun  string   `json:"treatment_run,omitempty"`
	MatchedBudget bool     `json:"matched_budget"`
	Limitations   []string `json:"limitations"`
}
type Aggregate struct {
	Known         int      `json:"known"`
	Unavailable   int      `json:"unavailable"`
	Estimated     int      `json:"estimated"`
	Reported      int      `json:"reported"`
	Observed      int      `json:"observed"`
	KnownSubtotal float64  `json:"known_subtotal"`
	Total         *float64 `json:"total"` // absent if any included measurement is unavailable
}
type MetricResult struct {
	Name       string    `json:"name"`
	Baseline   Aggregate `json:"baseline"`
	Treatment  Aggregate `json:"treatment"`
	Difference *float64  `json:"treatment_minus_baseline"`
}
type ArmResult struct {
	Runs            int      `json:"runs"`
	Accepted        int      `json:"accepted"`
	Rejected        int      `json:"rejected"`
	Incomplete      int      `json:"incomplete"`
	CostPerAccepted *float64 `json:"cost_per_accepted_task"`
}
type Report struct {
	Version            int            `json:"version"`
	BundleID           string         `json:"bundle_id"`
	BundleSHA256       string         `json:"bundle_sha256"`
	Question           string         `json:"question"`
	PlannedPairs       int            `json:"planned_pairs"`
	MatchedBudgetPairs int            `json:"matched_budget_pairs"`
	Pairs              []PairResult   `json:"pairs"`
	Baseline           ArmResult      `json:"baseline"`
	Treatment          ArmResult      `json:"treatment"`
	Metrics            []MetricResult `json:"metrics"`
	Limitations        []string       `json:"limitations"`
	Markdown           string         `json:"markdown"`
}

func digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func PairDigest(p Pair) string { return digest(p) }
func Decode(raw []byte) (Bundle, error) {
	var b Bundle
	if len(raw) > MaxBytes {
		return b, errors.New("comparison bundle exceeds 512 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&b); err != nil {
		return b, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return b, errors.New("comparison needs one JSON object")
	}
	return b, Validate(b)
}
func validText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}
func evidence(refs []string) bool {
	if len(refs) < 1 || len(refs) > 50 {
		return false
	}
	for _, s := range refs {
		if !validText(s, 2000) {
			return false
		}
	}
	return true
}
func measurement(m Measurement, count bool) error {
	if m.Status == "unavailable" {
		if m.Value != nil || !validText(m.Reason, 2000) {
			return errors.New("unavailable measurements need a null value and reason")
		}
		return nil
	}
	if m.Status != "observed" && m.Status != "reported" && m.Status != "estimated" {
		return errors.New("measurement status must be observed, reported, estimated or unavailable")
	}
	if m.Value == nil || math.IsNaN(*m.Value) || math.IsInf(*m.Value, 0) || *m.Value < 0 || *m.Value > 1e12 || !evidence(m.Evidence) {
		return errors.New("known measurements need a bounded nonnegative value and evidence")
	}
	if count && math.Trunc(*m.Value) != *m.Value {
		return errors.New("count measurements must be integers")
	}
	return nil
}
func invocation(v Invocation) error {
	if !validText(v.Agent, 100) || !validText(v.Source, 2000) {
		return errors.New("invocation agent and retained source required")
	}
	if (v.RequestedModel == "" || v.ResolvedModel == "" || v.PromptSHA256 == "") && !validText(v.UnknownReason, 2000) {
		return errors.New("unknown model/prompt provenance needs an explicit reason")
	}
	if len(v.RequestedModel) > 240 || len(v.ResolvedModel) > 240 || (v.PromptSHA256 != "" && !digestPattern.MatchString(v.PromptSHA256)) {
		return errors.New("invalid model/prompt provenance")
	}
	return nil
}
func Validate(b Bundle) error {
	if b.Version != Version {
		return fmt.Errorf("unsupported comparison version %d", b.Version)
	}
	if !validText(b.ID, 120) || !validText(b.Question, 4000) || len(b.Pairs) < 1 || len(b.Pairs) > 100 || len(b.Runs) > 200 {
		return errors.New("comparison needs an ID, question and 1–100 predeclared pairs (at most 200 runs)")
	}
	pairs := map[string]Pair{}
	ids := map[string]bool{}
	slots := map[string]bool{}
	for _, p := range b.Pairs {
		if !validText(p.ID, 120) || !validText(p.Task, 8000) || !validText(p.Intervention, 4000) || !evidence(p.Acceptance) || len(p.Repositories) < 1 || len(p.Repositories) > 20 {
			return errors.New("pair needs task, intervention, acceptance and starting repositories")
		}
		if _, ok := pairs[p.ID]; ok {
			return errors.New("duplicate pair ID")
		}
		pairs[p.ID] = p
		repos := map[string]bool{}
		for _, r := range p.Repositories {
			if !validText(r.Name, 240) || !commitPattern.MatchString(r.Base) || repos[r.Name] {
				return errors.New("starting repositories need unique names and full commit hashes")
			}
			repos[r.Name] = true
		}
		l := p.Limits
		if l.AgentCalls < 1 || l.AgentCalls > 10000 || l.RuntimeSeconds < 1 || l.RuntimeSeconds > 604800 || l.Tokens < 1 || l.Tokens > 1e12 || math.IsNaN(l.SpendUSD) || math.IsInf(l.SpendUSD, 0) || l.SpendUSD < 0 || l.SpendUSD > 1e9 {
			return errors.New("pair needs finite call, runtime and token limits; spend must be finite and nonnegative")
		}
	}
	for _, r := range b.Runs {
		p, ok := pairs[r.PairID]
		slot := r.PairID + "\x00" + r.Arm
		if !ok || !validText(r.ID, 120) || ids[r.ID] || slots[slot] || (r.Arm != "baseline" && r.Arm != "treatment") {
			return errors.New("runs need unique IDs and exactly one slot per declared pair/arm")
		}
		ids[r.ID] = true
		slots[slot] = true
		if r.PairSHA256 != PairDigest(p) {
			return fmt.Errorf("run %s does not bind the current task, bases, criteria, intervention and budget", r.ID)
		}
		if (r.Outcome != "accepted" && r.Outcome != "rejected" && r.Outcome != "incomplete") || !evidence(r.OutcomeEvidence) || r.StartedAt.IsZero() || r.EndedAt.IsZero() || r.EndedAt.Before(r.StartedAt) {
			return fmt.Errorf("run %s needs an assessed outcome, evidence and ordered start/end times", r.ID)
		}
		if err := invocation(r.Worker); err != nil {
			return fmt.Errorf("run %s: %w", r.ID, err)
		}
		if len(r.Lessons) > 100 || len(r.Reviewers) > 20 {
			return errors.New("too many lesson/reviewer records")
		}
		seen := map[string]bool{}
		for _, l := range r.Lessons {
			if !validText(l.ID, 120) || l.Version < 1 || !digestPattern.MatchString(l.SHA256) || !validText(l.Source, 2000) || seen[l.ID] {
				return errors.New("lessons need unique IDs, exact versions, content digests and sources")
			}
			seen[l.ID] = true
		}
		seen = map[string]bool{}
		for _, v := range r.Reviewers {
			if !validText(v.Profile, 120) || !validText(v.Provider, 120) || !validText(v.RubricVersion, 120) || seen[v.Profile] {
				return errors.New("reviewers need unique profiles, providers and rubric versions")
			}
			seen[v.Profile] = true
			if err := invocation(v.Invocation); err != nil {
				return err
			}
		}
		if err := measurement(r.AgentCalls, true); err != nil {
			return fmt.Errorf("run %s calls: %w", r.ID, err)
		}
		if err := measurement(r.Tokens, true); err != nil {
			return fmt.Errorf("run %s tokens: %w", r.ID, err)
		}
		if len(r.Metrics) != len(Metrics) {
			return errors.New("every outcome metric must be recorded, including unavailable values")
		}
		for _, key := range Metrics {
			m, ok := r.Metrics[key]
			if !ok {
				return fmt.Errorf("missing metric %s", key)
			}
			if err := measurement(m, key != "human_intervention_seconds" && key != "cost_usd"); err != nil {
				return fmt.Errorf("run %s metric %s: %w", r.ID, key, err)
			}
		}
	}
	return nil
}
func budgetIssues(r Run, p Pair) []string {
	var out []string
	check := func(label string, m Measurement, limit float64) {
		if m.Value == nil || m.Status == "estimated" {
			out = append(out, r.Arm+": "+label+" usage unavailable or estimated")
		} else if *m.Value > limit {
			out = append(out, r.Arm+": "+label+" exceeded shared limit")
		}
	}
	check("agent calls", r.AgentCalls, float64(p.Limits.AgentCalls))
	check("tokens", r.Tokens, float64(p.Limits.Tokens))
	if r.EndedAt.Sub(r.StartedAt).Seconds() > float64(p.Limits.RuntimeSeconds) {
		out = append(out, r.Arm+": wall time exceeded shared limit")
	}
	if p.Limits.SpendUSD > 0 {
		check("spend", r.Metrics["cost_usd"], p.Limits.SpendUSD)
	}
	return out
}
func add(a *Aggregate, m Measurement) {
	if m.Value == nil {
		a.Unavailable++
		return
	}
	a.Known++
	a.KnownSubtotal += *m.Value
	switch m.Status {
	case "estimated":
		a.Estimated++
	case "reported":
		a.Reported++
	case "observed":
		a.Observed++
	}
}
func finishAggregate(a *Aggregate) {
	if a.Unavailable == 0 && a.Known > 0 {
		x := a.KnownSubtotal
		a.Total = &x
	}
}
func countRun(a *ArmResult, r Run) {
	a.Runs++
	switch r.Outcome {
	case "accepted":
		a.Accepted++
	case "rejected":
		a.Rejected++
	case "incomplete":
		a.Incomplete++
	}
}
func conditions(r Run) string {
	// Order and source locations are not treatment differences. Exact content,
	// versions, rubrics, models and reviewer prompts are.
	lessons := append([]Lesson{}, r.Lessons...)
	for i := range lessons {
		lessons[i].Source = ""
	}
	slices.SortFunc(lessons, func(a, b Lesson) int { return strings.Compare(a.ID, b.ID) })
	reviewers := append([]Reviewer{}, r.Reviewers...)
	for i := range reviewers {
		reviewers[i].Invocation.Source = ""
		reviewers[i].Invocation.UnknownReason = ""
	}
	slices.SortFunc(reviewers, func(a, b Reviewer) int { return strings.Compare(a.Profile, b.Profile) })
	return digest(struct {
		Lessons   []Lesson
		Reviewers []Reviewer
	}{lessons, reviewers})
}
func Generate(b Bundle) (Report, error) {
	if err := Validate(b); err != nil {
		return Report{}, err
	}
	out := Report{Version: Version, BundleID: b.ID, BundleSHA256: digest(b), Question: b.Question, PlannedPairs: len(b.Pairs), Pairs: []PairResult{}, Metrics: []MetricResult{}, Limitations: []string{
		"Metrics and provenance are recorded evidence, not independently verified observations. Preserve the input bundle and cited artifacts with this report.",
		"Only pairs with both runs and recorded usage within their shared limits enter aggregates. Excluded pairs remain listed; exclusions can bias the result.",
		"Missing measurements are never zero. A total or difference is unavailable when any included run lacks that metric.",
		"Finding counts do not measure accuracy. Useful, dismissed and unassessed states require explicit assessment; regressions caught require evidence from before human review.",
		"These descriptive comparisons do not establish causation or statistical significance. Report task selection, run order and evaluator bias alongside results.",
	}}
	byPair := map[string]map[string]Run{}
	for _, r := range b.Runs {
		if byPair[r.PairID] == nil {
			byPair[r.PairID] = map[string]Run{}
		}
		byPair[r.PairID][r.Arm] = r
	}
	totals := map[string]*MetricResult{}
	for _, name := range Metrics {
		totals[name] = &MetricResult{Name: name}
	}
	for _, p := range b.Pairs {
		result := PairResult{ID: p.ID, SHA256: PairDigest(p), Limitations: []string{}}
		a, aOK := byPair[p.ID]["baseline"]
		z, zOK := byPair[p.ID]["treatment"]
		if aOK {
			result.BaselineRun = a.ID
			result.Limitations = append(result.Limitations, budgetIssues(a, p)...)
		} else {
			result.Limitations = append(result.Limitations, "baseline run missing")
		}
		if zOK {
			result.TreatmentRun = z.ID
			result.Limitations = append(result.Limitations, budgetIssues(z, p)...)
		} else {
			result.Limitations = append(result.Limitations, "treatment run missing")
		}
		result.MatchedBudget = aOK && zOK && len(result.Limitations) == 0
		if aOK && zOK {
			if a.Worker.Agent != z.Worker.Agent || a.Worker.RequestedModel != z.Worker.RequestedModel || a.Worker.ResolvedModel != z.Worker.ResolvedModel {
				result.Limitations = append(result.Limitations, "worker model/agent differs; treatment effect is confounded")
			}
			if a.Worker.PromptSHA256 != z.Worker.PromptSHA256 {
				result.Limitations = append(result.Limitations, "worker prompt differs; inspect whether differences are only the declared intervention")
			}
			if a.Worker.ResolvedModel == "" || z.Worker.ResolvedModel == "" || a.Worker.PromptSHA256 == "" || z.Worker.PromptSHA256 == "" {
				result.Limitations = append(result.Limitations, "worker model or prompt provenance is incomplete")
			}
			if conditions(a) == conditions(z) {
				result.Limitations = append(result.Limitations, "no recorded lesson/reviewer difference between arms")
			}
			for _, r := range []Run{a, z} {
				for _, v := range r.Reviewers {
					if v.Invocation.ResolvedModel == "" || v.Invocation.PromptSHA256 == "" {
						result.Limitations = append(result.Limitations, r.Arm+": reviewer "+v.Profile+" model or prompt provenance is incomplete")
					}
				}
			}
		}
		if result.MatchedBudget {
			out.MatchedBudgetPairs++
			countRun(&out.Baseline, a)
			countRun(&out.Treatment, z)
			for _, name := range Metrics {
				add(&totals[name].Baseline, a.Metrics[name])
				add(&totals[name].Treatment, z.Metrics[name])
			}
		}
		out.Pairs = append(out.Pairs, result)
	}
	for _, name := range Metrics {
		m := totals[name]
		finishAggregate(&m.Baseline)
		finishAggregate(&m.Treatment)
		if m.Baseline.Total != nil && m.Treatment.Total != nil {
			x := *m.Treatment.Total - *m.Baseline.Total
			m.Difference = &x
		}
		out.Metrics = append(out.Metrics, *m)
	}
	cost := totals["cost_usd"]
	if cost.Baseline.Total != nil && out.Baseline.Accepted > 0 {
		x := *cost.Baseline.Total / float64(out.Baseline.Accepted)
		out.Baseline.CostPerAccepted = &x
	}
	if cost.Treatment.Total != nil && out.Treatment.Accepted > 0 {
		x := *cost.Treatment.Total / float64(out.Treatment.Accepted)
		out.Treatment.CostPerAccepted = &x
	}
	out.Markdown = markdown(out)
	return out, nil
}
func cell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "`", "'").Replace(s)
}
func number(p *float64) string {
	if p == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.4g", *p)
}
func markdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Comparison: %s\n\n%s\n\nInput SHA-256: `%s`\n\n%d of %d planned pairs have matched recorded budgets.\n\n", cell(r.BundleID), cell(r.Question), r.BundleSHA256, r.MatchedBudgetPairs, r.PlannedPairs)
	fmt.Fprintf(&b, "| Outcome | Baseline | Treatment |\n| --- | ---: | ---: |\n| Runs included | %d | %d |\n| Accepted | %d | %d |\n| Rejected | %d | %d |\n| Incomplete | %d | %d |\n| Cost per accepted task (USD) | %s | %s |\n\n", r.Baseline.Runs, r.Treatment.Runs, r.Baseline.Accepted, r.Treatment.Accepted, r.Baseline.Rejected, r.Treatment.Rejected, r.Baseline.Incomplete, r.Treatment.Incomplete, number(r.Baseline.CostPerAccepted), number(r.Treatment.CostPerAccepted))
	b.WriteString("| Metric | Baseline total | Treatment total | Treatment − baseline |\n| --- | ---: | ---: | ---: |\n")
	for _, m := range r.Metrics {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", m.Name, number(m.Baseline.Total), number(m.Treatment.Total), number(m.Difference))
	}
	b.WriteString("\n## Measurement coverage\n\nKnown subtotals are descriptive only; unavailable totals above are not replaced with them.\n\n")
	for _, m := range r.Metrics {
		fmt.Fprintf(&b, "- %s: baseline %d known / %d unavailable (%d reported, %d estimated), subtotal %.4g; treatment %d known / %d unavailable (%d reported, %d estimated), subtotal %.4g.\n", m.Name, m.Baseline.Known, m.Baseline.Unavailable, m.Baseline.Reported, m.Baseline.Estimated, m.Baseline.KnownSubtotal, m.Treatment.Known, m.Treatment.Unavailable, m.Treatment.Reported, m.Treatment.Estimated, m.Treatment.KnownSubtotal)
	}
	b.WriteString("\n## Every planned pair\n\n")
	for _, p := range r.Pairs {
		fmt.Fprintf(&b, "- %s: matched budget %t; baseline %s; treatment %s.\n", cell(p.ID), p.MatchedBudget, cell(p.BaselineRun), cell(p.TreatmentRun))
		for _, s := range p.Limitations {
			fmt.Fprintf(&b, "  - %s\n", cell(s))
		}
	}
	b.WriteString("\n## Limits of this report\n\n")
	for _, s := range r.Limitations {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	return b.String()
}
