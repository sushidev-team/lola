package claimaudit

import (
	"fmt"
	"time"

	"github.com/sushidev-team/lola/internal/board"
)

// Verdict is the closed vocabulary an audited check carries to the wire.
type Verdict string

const (
	// Verified: the latest matching run before the claim exited zero.
	Verified Verdict = "verified"
	// Ran: a matching run happened, but its exit status was not its own.
	Ran Verdict = "ran"
	// Unverified: no matching command anywhere in the transcript before the
	// claim — the acceptance case ("tests pass" with no test run).
	Unverified Verdict = "unverified"
	// Contradicted: the latest matching run before the claim FAILED.
	Contradicted Verdict = "contradicted"
)

// Mismatch reports whether a verdict is worth a warning.
func (v Verdict) Mismatch() bool { return v == Unverified || v == Contradicted }

// claimSlack absorbs the gap between a tool result being written and the
// report that follows it (both stamped on this machine, so it is ordering, not
// clock skew).
const claimSlack = 5 * time.Second

// Finding is one audited check.
type Finding struct {
	Check   string
	Verdict Verdict
	Note    string // lola's own words, safe to render
}

// Result is a board's audit: one Finding per audited check, plus the
// mismatches worth a warning, as lola-authored one-liners.
type Result struct {
	Checks   map[string]Finding // keyed by check name as reported
	Warnings []string
}

// Facts are the observed facts the board is compared with. Ledger is nil when
// the transcript cannot be read (not claude, no path yet, scan never ran), in
// which case only the CI comparison runs.
type Facts struct {
	Ledger   *Ledger
	CIFailed bool // the PR's checks rollup is failing (a gh fact)
}

// Audit compares a board's claims with the facts. Only PASS claims are
// audited — a "fail" or "running" check claims nothing that could be false in
// the agent's favour — and only for checks whose name names a category.
func Audit(b board.Board, f Facts) Result {
	var r Result
	testClaimed := false
	for _, c := range b.Checks {
		if c.State != board.CheckPass {
			continue
		}
		cat := CategoryForCheck(c.Name)
		if cat == "" {
			continue
		}
		if cat == Test {
			testClaimed = true
		}
		if f.Ledger == nil || !f.Ledger.Complete {
			continue
		}
		fd := judge(c, cat, f.Ledger.Runs[cat])
		if r.Checks == nil {
			r.Checks = map[string]Finding{}
		}
		r.Checks[c.Name] = fd
		if fd.Verdict.Mismatch() {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%q claimed pass, but %s", c.Name, fd.Note))
		}
	}
	if f.CIFailed {
		switch {
		case testClaimed:
			r.Warnings = append(r.Warnings, "tests claimed passing, but the PR's CI is failing")
		case b.Phase == board.PhaseDone:
			r.Warnings = append(r.Warnings, "phase claimed done, but the PR's CI is failing")
		}
	}
	return r
}

func judge(c board.Check, cat Category, runs []Run) Finding {
	fd := Finding{Check: c.Name}
	var last *Run
	for i := range runs {
		if c.At.IsZero() || !runs[i].At.After(c.At.Add(claimSlack)) {
			last = &runs[i]
		}
	}
	switch {
	case last == nil:
		fd.Verdict = Unverified
		fd.Note = fmt.Sprintf("no %s command ran before the claim", cat)
	case last.Outcome == Failed:
		fd.Verdict = Contradicted
		fd.Note = fmt.Sprintf("the last %s run before the claim exited non-zero", cat)
	case last.Outcome == Passed:
		fd.Verdict = Verified
		fd.Note = fmt.Sprintf("a %s run exited zero before the claim", cat)
	default:
		fd.Verdict = Ran
		fd.Note = fmt.Sprintf("a %s command ran, but its exit status was masked (pipe, ;, background)", cat)
	}
	return fd
}
