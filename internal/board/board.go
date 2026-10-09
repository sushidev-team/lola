// Package board is the agent's OWN progress report: the todo list, phase,
// progress bar, blocker, note and self-run checks a coding agent publishes about
// itself through `lola report …` (see Apply for the verbs).
//
// Everything here is a CLAIM, not a fact. The agent's context includes the PR
// diff, CI logs and issue text — all attacker-influenceable — so what it says
// about itself can be steered. The board is therefore DISPLAY-ONLY, exactly
// like the [statusagent] overlay: it reaches SessionInfo.board and nothing else.
// The agent/delivery axes, slot counting, reactions, write-back, the send-keys
// gates and dispatch must never read it, and a report is not even evidence of
// activity (hooks and the pane own that).
//
// A pure stdlib leaf so the session store can hold a Board and the daemon can
// apply a report without either importing the other's concerns. Every string
// that enters is sanitized (control characters stripped — an ANSI sequence in a
// todo would otherwise reach a terminal UI verbatim), collapsed to one line and
// clipped, and every list is capped, because the socket is open to any process
// running as the user and a report must not be able to bloat the store.
package board

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Caps on everything a report can store. Generous for a real plan, small enough
// that the session snapshot cannot be grown by a looping agent.
const (
	MaxTodos       = 25
	MaxChecks      = 8
	MaxTextRunes   = 160 // one todo, the progress label, a check summary
	MaxNoteRunes   = 280 // the note and the blocker
	MaxCheckName   = 32
	maxArgvEntries = 64
)

// Phase is the agent's coarse stage of work. A closed vocabulary on purpose: a
// phase is rendered as a coloured chip, and a chip must only ever say a word the
// UIs have a colour for.
type Phase string

const (
	PhasePlanning      Phase = "planning"
	PhaseInvestigating Phase = "investigating"
	PhaseImplementing  Phase = "implementing"
	PhaseTesting       Phase = "testing"
	PhaseReviewing     Phase = "reviewing"
	PhasePolishing     Phase = "polishing"
	PhaseDone          Phase = "done"
)

// Phases lists the vocabulary in workflow order (the order help text and the
// briefing name them in).
var Phases = []Phase{PhasePlanning, PhaseInvestigating, PhaseImplementing, PhaseTesting, PhaseReviewing, PhasePolishing, PhaseDone}

// TodoState is one plan item's state.
type TodoState string

const (
	TodoPending TodoState = "pending"
	TodoActive  TodoState = "active"
	TodoDone    TodoState = "done"
)

// CheckState is a self-run check's result. "running" exists so an agent can say
// the test suite is going before it knows the answer.
type CheckState string

const (
	CheckPass    CheckState = "pass"
	CheckFail    CheckState = "fail"
	CheckRunning CheckState = "running"
)

// Todo is one plan item.
type Todo struct {
	Text  string    `json:"text"`
	State TodoState `json:"state"`
}

// Check is one self-reported check result ("tests", "lint", …). Distinct from
// the PR's CI on purpose: CI is a gh FACT on the delivery axis, this is what the
// agent says it saw locally before pushing.
type Check struct {
	Name    string     `json:"name"`
	State   CheckState `json:"state"`
	Summary string     `json:"summary,omitempty"`
}

// Board is a session's self-report. The zero value is "nothing reported".
type Board struct {
	Phase Phase  `json:"phase,omitempty"`
	Todos []Todo `json:"todos,omitempty"`
	// Progress is an explicit percentage (0–100). nil means "not set", in which
	// case Percent derives one from the todos.
	Progress      *int      `json:"progress,omitempty"`
	ProgressLabel string    `json:"progress_label,omitempty"`
	Blocked       string    `json:"blocked,omitempty"`
	BlockedAt     time.Time `json:"blocked_at,omitzero"`
	Note          string    `json:"note,omitempty"`
	Checks        []Check   `json:"checks,omitempty"`
	// UpdatedAt is when the agent last reported anything. A client fades a
	// board whose report is old — agents forget to report, and a confident
	// stale "testing" is worse than none.
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Empty reports whether nothing has been reported (or the board was cleared).
func (b Board) Empty() bool {
	return b.Phase == "" && len(b.Todos) == 0 && b.Progress == nil && b.ProgressLabel == "" &&
		b.Blocked == "" && b.Note == "" && len(b.Checks) == 0
}

// Done and Total count the plan.
func (b Board) Done() int {
	n := 0
	for _, t := range b.Todos {
		if t.State == TodoDone {
			n++
		}
	}
	return n
}

func (b Board) Total() int { return len(b.Todos) }

// Percent is the progress to draw: the explicit value when one was reported,
// else done/total of the plan. derived says which; ok is false when there is
// nothing to draw at all.
func (b Board) Percent() (pct int, derived, ok bool) {
	if b.Progress != nil {
		return *b.Progress, false, true
	}
	if len(b.Todos) == 0 {
		return 0, false, false
	}
	return b.Done() * 100 / len(b.Todos), true, true
}

// Current is the active plan item's text, "" when none is active.
func (b Board) Current() string {
	for _, t := range b.Todos {
		if t.State == TodoActive {
			return t.Text
		}
	}
	return ""
}

// Clone deep-copies b, so a mutation never aliases a slice the session store
// (or a snapshot reader) still holds.
func (b Board) Clone() Board {
	b.Todos = slices.Clone(b.Todos)
	b.Checks = slices.Clone(b.Checks)
	if b.Progress != nil {
		p := *b.Progress
		b.Progress = &p
	}
	return b
}

// ErrUsage is wrapped by every rejection of a malformed report, so the CLI can
// tell "you called it wrong" (worth showing the agent) from a transport error.
var ErrUsage = errors.New("usage")

func usage(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUsage, fmt.Sprintf(format, a...))
}

// Usage is the one-screen reference printed by `lola report --help` and quoted
// (abridged) in the agent briefing.
const Usage = `lola report <verb> [args]

  phase <planning|investigating|implementing|testing|reviewing|polishing|done>
  todo set <item> [item...]     replace the plan (first item becomes active)
  todo add <item> [item...]     append items
  todo start <n>                mark item n (1-based) as the one in progress
  todo done <n> [n...]          mark items done (advances to the next pending one)
  todo undo <n>                 mark item n pending again
  todo remove <n>               drop item n
  todo clear                    drop the plan
  progress <0-100|done/total> [label...]   explicit progress (else derived from todos)
  progress clear
  blocked <reason...>           say what you need from a human
  unblocked
  note <text...>                one-line status note ("note clear" removes it)
  check <name> <pass|fail|running> [summary...]   a check you ran locally
  check clear [name]
  clear                         reset the whole board`

// Apply applies one report (argv as typed after `lola report`) to a copy of b
// and returns the new board. It never mutates b. now stamps UpdatedAt (and
// BlockedAt on a new blocker). A malformed report returns an ErrUsage-wrapped
// error and b unchanged.
func Apply(b Board, argv []string, now time.Time) (Board, error) {
	if len(argv) == 0 {
		return b, usage("missing verb")
	}
	if len(argv) > maxArgvEntries {
		return b, usage("too many arguments (max %d)", maxArgvEntries)
	}
	nb := b.Clone()
	verb, args := strings.ToLower(strings.TrimSpace(argv[0])), argv[1:]
	var err error
	switch verb {
	case "phase":
		err = nb.applyPhase(args)
	case "todo", "todos":
		err = nb.applyTodo(args)
	case "progress":
		err = nb.applyProgress(args)
	case "blocked", "block":
		reason := clip(joinArgs(args), MaxNoteRunes)
		if reason == "" {
			return b, usage("blocked needs a reason")
		}
		if nb.Blocked == "" {
			nb.BlockedAt = now
		}
		nb.Blocked = reason
	case "unblocked", "unblock":
		nb.Blocked, nb.BlockedAt = "", time.Time{}
	case "note":
		if len(args) == 1 && strings.EqualFold(strings.TrimSpace(args[0]), "clear") {
			nb.Note = ""
		} else if nb.Note = clip(joinArgs(args), MaxNoteRunes); nb.Note == "" {
			return b, usage("note needs text")
		}
	case "check":
		err = nb.applyCheck(args)
	case "clear", "reset":
		nb = Board{}
	default:
		return b, usage("unknown verb %q", clip(verb, 32))
	}
	if err != nil {
		return b, err
	}
	nb.UpdatedAt = now
	return nb, nil
}

func (b *Board) applyPhase(args []string) error {
	if len(args) != 1 {
		return usage("phase takes one of: %s", phaseList())
	}
	p := Phase(strings.ToLower(strings.TrimSpace(args[0])))
	if p == "clear" {
		b.Phase = ""
		return nil
	}
	if !slices.Contains(Phases, p) {
		return usage("unknown phase %q (one of: %s)", clip(string(p), 32), phaseList())
	}
	b.Phase = p
	return nil
}

func phaseList() string {
	s := make([]string, len(Phases))
	for i, p := range Phases {
		s[i] = string(p)
	}
	return strings.Join(s, ", ")
}

func (b *Board) applyTodo(args []string) error {
	if len(args) == 0 {
		return usage("todo needs a sub-verb (set|add|start|done|undo|remove|clear)")
	}
	sub, rest := strings.ToLower(strings.TrimSpace(args[0])), args[1:]
	switch sub {
	case "set":
		items := cleanItems(rest)
		if len(items) == 0 {
			return usage("todo set needs at least one item")
		}
		if len(items) > MaxTodos {
			return usage("at most %d todos", MaxTodos)
		}
		b.Todos = make([]Todo, len(items))
		for i, t := range items {
			b.Todos[i] = Todo{Text: t, State: TodoPending}
		}
		b.Todos[0].State = TodoActive
	case "add":
		items := cleanItems(rest)
		if len(items) == 0 {
			return usage("todo add needs at least one item")
		}
		if len(b.Todos)+len(items) > MaxTodos {
			return usage("at most %d todos", MaxTodos)
		}
		for _, t := range items {
			b.Todos = append(b.Todos, Todo{Text: t, State: TodoPending})
		}
		if b.Current() == "" {
			b.activateNextPending(0)
		}
	case "start":
		i, err := b.index(rest, sub)
		if err != nil {
			return err
		}
		for j := range b.Todos {
			if b.Todos[j].State == TodoActive {
				b.Todos[j].State = TodoPending
			}
		}
		b.Todos[i].State = TodoActive
	case "done":
		if len(rest) == 0 {
			return usage("todo done needs an item number")
		}
		var idx []int
		for _, a := range rest {
			i, err := b.index([]string{a}, sub)
			if err != nil {
				return err
			}
			idx = append(idx, i)
		}
		for _, i := range idx {
			b.Todos[i].State = TodoDone
		}
		if b.Current() == "" {
			b.activateNextPending(slices.Max(idx) + 1)
		}
	case "undo":
		i, err := b.index(rest, sub)
		if err != nil {
			return err
		}
		b.Todos[i].State = TodoPending
	case "remove", "rm":
		i, err := b.index(rest, sub)
		if err != nil {
			return err
		}
		b.Todos = slices.Delete(b.Todos, i, i+1)
	case "clear":
		b.Todos = nil
	default:
		return usage("unknown todo sub-verb %q", clip(sub, 32))
	}
	return nil
}

// activateNextPending makes the first pending item at or after from (wrapping
// to the start) the active one — `todo done` advancing the plan by itself, so an
// agent that only ever reports completions still shows what it is on now.
func (b *Board) activateNextPending(from int) {
	n := len(b.Todos)
	for k := range n {
		j := (from + k) % n
		if b.Todos[j].State == TodoPending {
			b.Todos[j].State = TodoActive
			return
		}
	}
}

func (b *Board) index(args []string, sub string) (int, error) {
	if len(args) != 1 {
		return 0, usage("todo %s takes one item number", sub)
	}
	n, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || n < 1 || n > len(b.Todos) {
		return 0, usage("todo %s: no item %q (have %d)", sub, clip(args[0], 16), len(b.Todos))
	}
	return n - 1, nil
}

func (b *Board) applyProgress(args []string) error {
	if len(args) == 0 {
		return usage("progress needs a value (0-100 or done/total)")
	}
	v := strings.TrimSpace(args[0])
	if strings.EqualFold(v, "clear") {
		b.Progress, b.ProgressLabel = nil, ""
		return nil
	}
	pct, err := parsePercent(v)
	if err != nil {
		return err
	}
	b.Progress = &pct
	b.ProgressLabel = clip(joinArgs(args[1:]), MaxTextRunes)
	return nil
}

// parsePercent accepts "60", "60%", "0.6" and "3/5", clamped to 0–100.
func parsePercent(v string) (int, error) {
	bad := usage("progress %q is not 0-100, a fraction, or done/total", clip(v, 16))
	if num, den, ok := strings.Cut(v, "/"); ok {
		n, err1 := strconv.Atoi(strings.TrimSpace(num))
		d, err2 := strconv.Atoi(strings.TrimSpace(den))
		if err1 != nil || err2 != nil || d <= 0 || n < 0 {
			return 0, bad
		}
		return clampPct(n * 100 / d), nil
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
	if err != nil {
		return 0, bad
	}
	if f > 0 && f < 1 && !strings.HasSuffix(v, "%") && strings.Contains(v, ".") {
		f *= 100
	}
	return clampPct(int(f + 0.5)), nil
}

func clampPct(n int) int { return max(0, min(100, n)) }

func (b *Board) applyCheck(args []string) error {
	if len(args) >= 1 && strings.EqualFold(strings.TrimSpace(args[0]), "clear") {
		if len(args) == 1 {
			b.Checks = nil
			return nil
		}
		name := clip(args[1], MaxCheckName)
		b.Checks = slices.DeleteFunc(b.Checks, func(c Check) bool { return strings.EqualFold(c.Name, name) })
		return nil
	}
	if len(args) < 2 {
		return usage("check needs <name> <pass|fail|running> [summary]")
	}
	name := clip(args[0], MaxCheckName)
	st := CheckState(strings.ToLower(strings.TrimSpace(args[1])))
	switch st {
	case CheckPass, CheckFail, CheckRunning:
	case "ok", "passed", "green":
		st = CheckPass
	case "failed", "red", "error":
		st = CheckFail
	default:
		return usage("check state %q is not pass, fail or running", clip(string(st), 16))
	}
	if name == "" {
		return usage("check needs a name")
	}
	c := Check{Name: name, State: st, Summary: clip(joinArgs(args[2:]), MaxTextRunes)}
	if i := slices.IndexFunc(b.Checks, func(x Check) bool { return strings.EqualFold(x.Name, name) }); i >= 0 {
		b.Checks[i] = c
		return nil
	}
	if len(b.Checks) >= MaxChecks {
		return usage("at most %d checks", MaxChecks)
	}
	b.Checks = append(b.Checks, c)
	return nil
}

func cleanItems(args []string) []string {
	var out []string
	for _, a := range args {
		if t := clip(a, MaxTextRunes); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func joinArgs(args []string) string { return strings.Join(args, " ") }

// clip sanitizes s to one display line of at most n runes: invalid UTF-8 and
// every control/format character dropped (an ESC is the head of an ANSI
// sequence a terminal UI would interpret), any whitespace run collapsed to a
// single space, trimmed, and an over-long result ended with "…".
func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > n {
		r := []rune(out)
		out = strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return out
}
