// Package sshelement is the vocabulary of what an agent PUBLISHES about an ssh
// investigation: `lola ssh post <kind> …` becomes one timeline element a human
// can read at a glance.
//
// Everything here is a CLAIM, not a fact. The agent has read remote output, and
// remote output is attacker-influenceable, so what it says can be steered.
// Elements are therefore DISPLAY-ONLY, like internal/board: they reach the
// timeline wire and nothing else. No element ever feeds a connection state, an
// approval, a wheel, a grant or the agent axis, and a `proposal` never runs by
// itself. The one daemon-verified part of an element is its Evidence: a link
// that the daemon resolved against a block it recorded itself.
//
// A pure stdlib leaf. The argv is parsed DAEMON-side (Parse), so the trust
// boundary is the daemon and not whichever lola binary the agent's pane runs,
// and agents of every kind get the same behaviour from a plain shell command.
// Every string is sanitized and clipped and every list capped, because the
// socket is open to any process running as the user. Text is text: a UI renders
// it verbatim and never as markup or a link.
package sshelement

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Caps on everything a post can store.
const (
	MaxElements     = 500 // per session; the daemon prunes the oldest closed ones
	MaxTitleRunes   = 200
	MaxBodyRunes    = 2000
	MaxRefRunes     = 40
	MaxSteps        = 20
	MaxStepRunes    = 160
	MaxMetrics      = 16
	MaxMetricName   = 48
	MaxMetricValue  = 80
	MaxEvidence     = 12
	MaxChoices      = 6
	MaxChoiceRunes  = 80
	MaxAnswerRunes  = 2000
	MaxCommandBytes = 8 * 1024
	MaxCwdBytes     = 1024
	MaxBlobRunes    = 280 // an unbroken answer longer than this is refused, see blobError
	MaxWidgetHTML   = 128 * 1024
	MaxWidgetData   = 64 * 1024
	MaxWidgetHeight = 480
	MaxWidgets      = 20 // per session
	maxArgvEntries  = 128
)

// Kind is the closed vocabulary of elements. A kind is a designed card in every
// UI, and a card must only ever be one the UIs know how to draw.
type Kind string

const (
	KindSummary        Kind = "summary"        // where the investigation stands now
	KindNarration      Kind = "narration"      // one line on what the agent is doing
	KindHypothesis     Kind = "hypothesis"     // a theory: open, confirmed or refuted
	KindFinding        Kind = "finding"        // something learned, with a severity
	KindPlan           Kind = "plan"           // ordered steps with states
	KindMetric         Kind = "metric"         // key-values read from a system
	KindEvidence       Kind = "evidence"       // a claim tied to specific blocks
	KindRecommendation Kind = "recommendation" // advice, in prose
	KindProposal       Kind = "proposal"       // a command a human may choose to run
	KindQuestion       Kind = "question"       // the agent asks the human
	KindWidget         Kind = "widget"         // agent-written HTML in a sandboxed frame
)

// Kinds lists the vocabulary in the order help text names it.
var Kinds = []Kind{KindSummary, KindNarration, KindHypothesis, KindFinding, KindPlan, KindMetric, KindEvidence, KindRecommendation, KindProposal, KindQuestion, KindWidget}

// Hypothesis states.
const (
	HypothesisOpen      = "open"
	HypothesisConfirmed = "confirmed"
	HypothesisRefuted   = "refuted"
)

// Proposal states. Only a human moves a proposal off StateProposed.
const (
	ProposalProposed = "proposed"
	ProposalAccepted = "accepted"
	ProposalDeclined = "declined"
)

// Question states. Only a human moves a question off QuestionOpen.
const (
	QuestionOpen     = "open"
	QuestionAnswered = "answered"
)

// Severities, least to most severe.
const (
	SeverityInfo     = "info"
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// Severities lists the finding severities, least to most severe.
var Severities = []string{SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical}

// Plan step states.
const (
	StepPending = "pending"
	StepActive  = "active"
	StepDone    = "done"
	StepSkipped = "skipped"
	StepFailed  = "failed"
)

// StepStates lists the plan step states.
var StepStates = []string{StepPending, StepActive, StepDone, StepSkipped, StepFailed}

// Step is one plan item.
type Step struct {
	Text  string `json:"text"`
	State string `json:"state"`
}

// Metric is one key-value the agent read. Unit is free text ("%", "ms").
type Metric struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
}

// Evidence links an element to a block. Block comes from the agent; Verified,
// Status and ExitCode are written ONLY by Resolve from the daemon's own records.
// Verified means "this block exists in this session and these are its recorded
// facts". It does NOT mean the agent's claim about that block is correct.
type Evidence struct {
	Block    string `json:"block"`
	Verified bool   `json:"verified"`
	Status   string `json:"status,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// Outcome records a HUMAN's answer to a question or decision on a proposal.
type Outcome struct {
	Text  string    `json:"text,omitempty"`  // the answer, or the decline reason
	Block string    `json:"block,omitempty"` // the block an accepted proposal became
	At    time.Time `json:"at,omitzero"`
}

// Element is one agent-published timeline element.
type Element struct {
	ID        string     `json:"id"`            // daemon-assigned, e.g. "e12"
	Ref       string     `json:"ref,omitempty"` // the agent's handle for updating it
	Kind      Kind       `json:"kind"`
	Conn      string     `json:"conn,omitempty"` // daemon-resolved from --host
	Title     string     `json:"title,omitempty"`
	Body      string     `json:"body,omitempty"`
	State     string     `json:"state,omitempty"`
	Severity  string     `json:"severity,omitempty"`
	Steps     []Step     `json:"steps,omitempty"`
	Metrics   []Metric   `json:"metrics,omitempty"`
	Evidence  []Evidence `json:"evidence,omitempty"`
	Command   string     `json:"command,omitempty"` // proposal only, validated by ValidateCommand
	Cwd       string     `json:"cwd,omitempty"`     // proposal only
	Choices   []string   `json:"choices,omitempty"` // question only
	HTML      string     `json:"html,omitempty"`    // widget only: never rendered outside its sandbox
	Data      string     `json:"data,omitempty"`    // widget only: valid JSON
	Height    int        `json:"height,omitempty"`  // widget only: unused since widgets fill a panel, kept so old posts still parse
	Outcome   *Outcome   `json:"outcome,omitempty"`
	Revision  int        `json:"revision"`
	CreatedAt time.Time  `json:"createdAt,omitzero"`
	UpdatedAt time.Time  `json:"updatedAt,omitzero"`
}

// Post is one parsed `lola ssh post`. Host is the --host flag, naming a
// configured host; the daemon resolves it to Element.Conn.
type Post struct {
	Host    string
	Element Element
	// set records which optional fields the argv named, so re-posting a Ref
	// updates exactly those.
	set map[string]bool
}

// Usage is `lola ssh post --help`, and the daemon's answer to a malformed post.
const Usage = `lola ssh post <kind> [text] [flags]

Publish a timeline element for the humans watching. Everything you post is shown
as your claim. Only --evidence links are verified, against blocks lola recorded.

Kinds:
  summary <text>                        where the investigation stands now
  narration <text>                      one line: what you are doing, or your
                                        answer when the human talks to you
  hypothesis <text> [--state open|confirmed|refuted]
  finding <text> --severity info|low|medium|high|critical
  plan [title] --step "text" --step "done: text"
                                        states: pending active done skipped failed
  metric [title] --kv name=value --kv "name=value unit"
  evidence <claim> --evidence b3,b4     a claim tied to specific blocks (required)
  recommendation <text>                 advice in prose
  proposal <title> --command "cmd" [--cwd dir]
                                        a human may run it; it never runs by itself
  question <text> [--choice A --choice B]
  widget <title> --file page.html [--data JSON | --data-file F]
                                        a small page in a sandbox; update with --ref.
                                        Start from: lola ssh guide widget-template

Answers can use lists, tables, callouts and folds. See: lola ssh guide answers

Common flags:
  --body <text>       longer detail, shown when the human expands the card
  --evidence b3,b4    link blocks from lola ssh run; unknown ids are rejected
  --ref <name>        name this element; posting the same kind and ref updates it
  --host <name>       the configured host this is about, when it is not obvious
`

// blobError refuses a long unbroken answer, and says how to write it. Teaching this in
// the briefing alone did not work: agents wrote paragraphs anyway, and a human reading a
// timeline skips them. Refusing costs one re-post, and the message is all the agent needs,
// so it works in a session started before any guide existed. A recommendation is advice in
// prose and keeps its shape.
func blobError(e *Element) error {
	if e.Kind == KindRecommendation || strings.Contains(e.Title, "\n") || utf8.RuneCountInString(e.Title) <= MaxBlobRunes {
		return nil
	}
	return usage(`this is a wall of text, and the human will not read it. Post it again with its structure: a verdict on line one, then short lines. Nothing is lost by cutting what they can see by opening the command. For example:

GitLab is healthy
| Uptime | 8 weeks |
|---|---|
| Memory | 5.26 of 8 GiB |
- [ok] all 16 services run
- [warn] nothing listens on port 80
> [!NOTE] Log scan timed out, so recent errors are unknown
+++ Details
anything long goes in a fold
+++

Use real line breaks. More: lola ssh guide answers`)
}

func usage(format string, a ...any) error {
	return fmt.Errorf("lola ssh post: "+format+" (see lola ssh post --help)", a...)
}

var (
	blockIDRe = regexp.MustCompile(`^b[0-9]{1,9}$`)
	refRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// flagInfo says whether a flag takes a value and may repeat.
var flagRepeats = map[string]bool{
	"--step": true, "--kv": true, "--choice": true, "--evidence": true,
}

var knownFlags = map[string]bool{
	"--step": true, "--kv": true, "--choice": true, "--evidence": true,
	"--state": true, "--severity": true, "--body": true, "--ref": true,
	"--host": true, "--command": true, "--cwd": true,
	"--html": true, "--data": true, "--height": true,
}

// flagsByKind names the kind-specific flags. The common ones are always allowed.
var flagsByKind = map[Kind][]string{
	KindHypothesis: {"--state"},
	KindFinding:    {"--severity"},
	KindPlan:       {"--step"},
	KindMetric:     {"--kv"},
	KindProposal:   {"--command", "--cwd"},
	KindQuestion:   {"--choice"},
	KindWidget:     {"--html", "--data", "--height"},
}

func flagAllowed(k Kind, f string) bool {
	switch f {
	case "--body", "--evidence", "--ref", "--host":
		return true
	}
	return slices.Contains(flagsByKind[k], f)
}

// Parse turns the argv typed after `lola ssh post` into a validated Post. It
// reads no clock and no daemon state: IDs, times and evidence facts are the
// daemon's to add.
func Parse(argv []string) (Post, error) {
	if len(argv) == 0 {
		return Post{}, usage("missing kind")
	}
	if len(argv) > maxArgvEntries {
		return Post{}, usage("too many arguments (max %d)", maxArgvEntries)
	}
	k := Kind(strings.ToLower(strings.TrimSpace(argv[0])))
	if !slices.Contains(Kinds, k) {
		return Post{}, usage("unknown kind %q", Line(argv[0], 32))
	}

	var words []string
	flags := map[string][]string{}
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		name, inline, hasInline := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "--") {
			words = append(words, a)
			continue
		}
		if a == "--" {
			words = append(words, argv[i+1:]...)
			break
		}
		if !knownFlags[name] {
			return Post{}, usage("unknown flag %q", Line(name, 32))
		}
		if !flagAllowed(k, name) {
			return Post{}, usage("%s does not take %s", k, name)
		}
		val := inline
		if !hasInline {
			if i+1 >= len(argv) {
				return Post{}, usage("%s needs a value", name)
			}
			i++
			val = argv[i]
		}
		if !flagRepeats[name] && len(flags[name]) > 0 {
			return Post{}, usage("%s given twice", name)
		}
		flags[name] = append(flags[name], val)
	}

	one := func(name string) string {
		if v := flags[name]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	e := Element{Kind: k}
	e.Title = Line(strings.Join(words, " "), MaxTitleRunes)
	// These three ARE the agent's prose, usually its answer to a human. Flattening one
	// to a title's single clipped line ate its lists and cut it off mid-word.
	if k == KindSummary || k == KindNarration || k == KindRecommendation {
		e.Title = Para(strings.Join(words, " "), MaxBodyRunes)
	}
	e.Body = Para(one("--body"), MaxBodyRunes)
	set := map[string]bool{}
	if e.Title != "" {
		set["title"] = true
	}
	if _, ok := flags["--body"]; ok {
		set["body"] = true
	}

	if v := one("--ref"); v != "" {
		if len(v) > MaxRefRunes || !refRe.MatchString(v) {
			return Post{}, usage("--ref must be letters, digits, dots, dashes (max %d)", MaxRefRunes)
		}
		e.Ref = v
	}
	host := one("--host")
	if host != "" && !hostRe.MatchString(host) {
		return Post{}, usage("--host is not a host name")
	}

	ev, err := parseEvidence(flags["--evidence"])
	if err != nil {
		return Post{}, err
	}
	e.Evidence = ev
	if len(ev) > 0 {
		set["evidence"] = true
	}

	if err := parseKind(&e, flags, set); err != nil {
		return Post{}, err
	}
	return Post{Host: host, Element: e, set: set}, nil
}

func parseEvidence(vals []string) ([]Evidence, error) {
	var out []Evidence
	seen := map[string]bool{}
	for _, v := range vals {
		for _, id := range strings.Split(v, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if !blockIDRe.MatchString(id) {
				return nil, usage("--evidence wants block ids like b3, got %q", Line(id, 32))
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, Evidence{Block: id})
		}
	}
	if len(out) > MaxEvidence {
		return nil, usage("too many --evidence links (max %d)", MaxEvidence)
	}
	return out, nil
}

// parseKind applies the rules particular to one kind. A required field missing
// from a post that names a Ref is left to Merge, because an update may name only
// what changed.
func parseKind(e *Element, flags map[string][]string, set map[string]bool) error {
	one := func(name string) string {
		if v := flags[name]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	isUpdate := e.Ref != ""
	needTitle := func() error {
		if e.Title == "" && !isUpdate {
			return usage("%s needs text", e.Kind)
		}
		return nil
	}
	switch e.Kind {
	case KindSummary, KindNarration, KindRecommendation:
		if err := needTitle(); err != nil {
			return err
		}
		return blobError(e)
	case KindWidget:
		if err := needTitle(); err != nil {
			return err
		}
		if v, ok := flags["--html"]; ok {
			if len(v[0]) > MaxWidgetHTML {
				return usage("--html is %d bytes, the limit is %d", len(v[0]), MaxWidgetHTML)
			}
			e.HTML = strings.ToValidUTF8(strings.ReplaceAll(v[0], "\x00", ""), "\uFFFD")
			set["html"] = true
		} else if !isUpdate {
			return usage("widget needs --file or --html")
		}
		if v, ok := flags["--data"]; ok {
			if len(v[0]) > MaxWidgetData {
				return usage("--data is %d bytes, the limit is %d", len(v[0]), MaxWidgetData)
			}
			if !json.Valid([]byte(v[0])) {
				return usage("--data is not valid JSON")
			}
			e.Data = v[0]
			set["data"] = true
		}
		if v, ok := flags["--height"]; ok {
			h, err := strconv.Atoi(v[0])
			if err != nil || h < 1 {
				return usage("--height must be a positive number")
			}
			e.Height = min(h, MaxWidgetHeight)
			set["height"] = true
		}
		return nil
	case KindHypothesis:
		if err := needTitle(); err != nil {
			return err
		}
		if s := strings.ToLower(one("--state")); s != "" {
			if s != HypothesisOpen && s != HypothesisConfirmed && s != HypothesisRefuted {
				return usage("--state must be open, confirmed or refuted")
			}
			e.State = s
			set["state"] = true
		} else if !isUpdate {
			e.State = HypothesisOpen
		}
	case KindFinding:
		if err := needTitle(); err != nil {
			return err
		}
		s := strings.ToLower(one("--severity"))
		switch {
		case s == "" && isUpdate:
		case slices.Contains(Severities, s):
			e.Severity = s
			set["severity"] = true
		default:
			return usage("--severity must be one of %s", strings.Join(Severities, ", "))
		}
	case KindPlan:
		vals := flags["--step"]
		if len(vals) == 0 && !isUpdate {
			return usage("plan needs at least one --step")
		}
		if len(vals) > MaxSteps {
			return usage("too many --step entries (max %d)", MaxSteps)
		}
		for _, v := range vals {
			st := parseStep(v)
			if st.Text == "" {
				return usage("--step needs text")
			}
			e.Steps = append(e.Steps, st)
		}
		if len(vals) > 0 {
			set["steps"] = true
		}
	case KindMetric:
		vals := flags["--kv"]
		if len(vals) == 0 && !isUpdate {
			return usage("metric needs at least one --kv name=value")
		}
		if len(vals) > MaxMetrics {
			return usage("too many --kv entries (max %d)", MaxMetrics)
		}
		for _, v := range vals {
			m, err := parseMetric(v)
			if err != nil {
				return err
			}
			e.Metrics = append(e.Metrics, m)
		}
		if len(vals) > 0 {
			set["metrics"] = true
		}
	case KindEvidence:
		if err := needTitle(); err != nil {
			return err
		}
		if len(e.Evidence) == 0 && !isUpdate {
			return usage("evidence needs --evidence with at least one block id")
		}
	case KindProposal:
		if err := needTitle(); err != nil {
			return err
		}
		cmd, hasCmd := flags["--command"]
		if !hasCmd && !isUpdate {
			return usage("proposal needs --command")
		}
		if hasCmd {
			if err := ValidateCommand(cmd[0]); err != nil {
				return usage("--command: %v", err)
			}
			e.Command = cmd[0]
			set["command"] = true
		}
		if cwd, ok := flags["--cwd"]; ok {
			if err := ValidateCwd(cwd[0]); err != nil {
				return usage("--cwd: %v", err)
			}
			e.Cwd = cwd[0]
			set["cwd"] = true
		}
		if !isUpdate {
			e.State = ProposalProposed
		}
	case KindQuestion:
		if err := needTitle(); err != nil {
			return err
		}
		vals := flags["--choice"]
		if len(vals) > MaxChoices {
			return usage("too many --choice entries (max %d)", MaxChoices)
		}
		for _, v := range vals {
			if c := Line(v, MaxChoiceRunes); c != "" {
				e.Choices = append(e.Choices, c)
			}
		}
		if len(vals) > 0 {
			set["choices"] = true
		}
		if !isUpdate {
			e.State = QuestionOpen
		}
	}
	return nil
}

// parseStep reads "done: text" or plain "text" (pending). Only a known state
// word is a prefix, so "note: check disk" stays the whole text.
func parseStep(v string) Step {
	if head, rest, ok := strings.Cut(v, ":"); ok {
		if s := strings.ToLower(strings.TrimSpace(head)); slices.Contains(StepStates, s) {
			return Step{Text: Line(rest, MaxStepRunes), State: s}
		}
	}
	return Step{Text: Line(v, MaxStepRunes), State: StepPending}
}

// parseMetric reads "name=value" or "name=value unit". A value's unit is its
// last space-separated word only when the value has exactly two words.
func parseMetric(v string) (Metric, error) {
	name, val, ok := strings.Cut(v, "=")
	name, val = Line(name, MaxMetricName), Line(val, MaxMetricValue)
	if !ok || name == "" || val == "" {
		return Metric{}, usage("--kv wants name=value, got %q", Line(v, 32))
	}
	m := Metric{Name: name, Value: val}
	if f := strings.Fields(val); len(f) == 2 {
		m.Value, m.Unit = f[0], f[1]
	}
	return m, nil
}

// ErrClosed refuses an update to an element a human has already acted on: what
// the human answered or decided must not change under the record of it.
var ErrClosed = errors.New("a human has already acted on this element, post a new one")

// Merge applies an update onto prev, returning the new element. Only the fields
// the post named change; prev is not mutated. Identity, creation time and any
// Outcome are kept. A Ref names one element of one kind.
func (p Post) Merge(prev Element, now time.Time) (Element, error) {
	if prev.Kind != p.Element.Kind {
		return Element{}, usage("ref %q is a %s, not a %s", prev.Ref, prev.Kind, p.Element.Kind)
	}
	if prev.Outcome != nil {
		return Element{}, ErrClosed
	}
	n := prev.Clone()
	src := p.Element
	if p.set["title"] {
		n.Title = src.Title
	}
	if p.set["body"] {
		n.Body = src.Body
	}
	if p.set["state"] {
		n.State = src.State
	}
	if p.set["severity"] {
		n.Severity = src.Severity
	}
	if p.set["steps"] {
		n.Steps = slices.Clone(src.Steps)
	}
	if p.set["metrics"] {
		n.Metrics = slices.Clone(src.Metrics)
	}
	if p.set["evidence"] {
		n.Evidence = slices.Clone(src.Evidence)
	}
	if p.set["command"] {
		n.Command = src.Command
	}
	if p.set["cwd"] {
		n.Cwd = src.Cwd
	}
	if p.set["choices"] {
		n.Choices = slices.Clone(src.Choices)
	}
	if p.set["html"] {
		n.HTML = src.HTML
	}
	if p.set["data"] {
		n.Data = src.Data
	}
	if p.set["height"] {
		n.Height = src.Height
	}
	n.Revision = prev.Revision + 1
	n.UpdatedAt = now
	return n, nil
}

// New finishes a post that names no existing element.
func (p Post) New(id string, now time.Time) (Element, error) {
	e := p.Element.Clone()
	if err := e.complete(); err != nil {
		return Element{}, err
	}
	e.ID, e.Revision, e.CreatedAt, e.UpdatedAt = id, 1, now, now
	return e, nil
}

// complete enforces the required fields on a post creating an element: a Ref
// with no element behind it is a creation, so Parse's leniency ends here.
func (e *Element) complete() error {
	switch e.Kind {
	case KindPlan:
		if len(e.Steps) == 0 {
			return usage("plan needs at least one --step")
		}
		return nil
	case KindMetric:
		if len(e.Metrics) == 0 {
			return usage("metric needs at least one --kv name=value")
		}
		return nil
	}
	if e.Title == "" {
		return usage("%s needs text", e.Kind)
	}
	switch e.Kind {
	case KindHypothesis:
		if e.State == "" {
			e.State = HypothesisOpen
		}
	case KindFinding:
		if e.Severity == "" {
			return usage("--severity must be one of %s", strings.Join(Severities, ", "))
		}
	case KindEvidence:
		if len(e.Evidence) == 0 {
			return usage("evidence needs --evidence with at least one block id")
		}
	case KindProposal:
		if e.Command == "" {
			return usage("proposal needs --command")
		}
		e.State = ProposalProposed
	case KindQuestion:
		e.State = QuestionOpen
	}
	return nil
}

// Clone deep-copies an element.
func (e Element) Clone() Element {
	e.Steps = slices.Clone(e.Steps)
	e.Metrics = slices.Clone(e.Metrics)
	e.Evidence = slices.Clone(e.Evidence)
	e.Choices = slices.Clone(e.Choices)
	if e.Outcome != nil {
		o := *e.Outcome
		e.Outcome = &o
	}
	return e
}

// BlockFact is what the daemon's own records say about a block.
type BlockFact struct {
	Status   string
	ExitCode *int
}

// Resolve checks every evidence link against the daemon's records and copies the
// facts in. An unknown block REJECTS the post rather than recording an unverified
// link, because the agent can correct itself and the human should never have to
// wonder why a link goes nowhere. lookup must only find blocks of this session.
func Resolve(e *Element, lookup func(id string) (BlockFact, bool)) error {
	for i := range e.Evidence {
		fact, ok := lookup(e.Evidence[i].Block)
		if !ok {
			return usage("--evidence: no block %s in this session", e.Evidence[i].Block)
		}
		e.Evidence[i].Verified = true
		e.Evidence[i].Status = fact.Status
		e.Evidence[i].ExitCode = fact.ExitCode
	}
	return nil
}

// Answer returns e answered by a human. The text may be one of e.Choices or free
// text, and is sanitized here. A second answer is refused.
func Answer(e Element, text string, now time.Time) (Element, error) {
	if e.Kind != KindQuestion {
		return Element{}, errors.New("not a question")
	}
	if e.Outcome != nil {
		return Element{}, ErrClosed
	}
	t := Para(text, MaxAnswerRunes)
	if t == "" {
		return Element{}, errors.New("answer is empty")
	}
	n := e.Clone()
	n.State = QuestionAnswered
	n.Outcome = &Outcome{Text: t, At: now}
	n.Revision++
	n.UpdatedAt = now
	return n, nil
}

// Decide returns a proposal accepted (block is what it became) or declined (note
// is the optional reason) by a human. A second decision is refused.
func Decide(e Element, accepted bool, block, note string, now time.Time) (Element, error) {
	if e.Kind != KindProposal {
		return Element{}, errors.New("not a proposal")
	}
	if e.Outcome != nil {
		return Element{}, ErrClosed
	}
	n := e.Clone()
	n.Outcome = &Outcome{Text: Para(note, MaxAnswerRunes), At: now}
	if accepted {
		n.State = ProposalAccepted
		n.Outcome.Block = block
	} else {
		n.State = ProposalDeclined
	}
	n.Revision++
	n.UpdatedAt = now
	return n, nil
}

// AwaitsHuman reports whether a human could still act on e. It is a DISPLAY
// count only: an agent can post questions at will, so this must never raise a
// session's needs-human state, which is reserved for daemon facts.
func (e Element) AwaitsHuman() bool {
	return e.Outcome == nil && (e.Kind == KindQuestion || e.Kind == KindProposal)
}
