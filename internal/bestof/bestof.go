// Package bestof is the pure half of best-of-N: run several coding agents (or
// one agent on several models) on ONE issue, compare what they produced, and
// keep one. It owns two things and execs nothing:
//
//   - the MIX vocabulary — which attempts to spawn, written
//     `<agent>[@<model>][:<count>]` (`claude:2`, `codex`, `claude@opus`,
//     `opencode@anthropic/claude-sonnet-4`), parsed and validated here so the
//     config, the CLI and the socket command all accept exactly one shape;
//   - Suggest, the display-only ranking that marks one attempt as the
//     suggested winner from facts the daemon already has (PR state, CI rollup,
//     review tallies, diff size). It is a HINT for the human doing the pick,
//     never an input to anything that acts: the pick is always a human's.
//
// A stdlib leaf (plus internal/agent for the kind and model checks) so config,
// the daemon and both UIs can share it without a cycle.
package bestof

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
)

// MaxAttempts bounds one best-of group. Every attempt is a worktree, a coding
// agent and (eventually) a PR, all counted against the concurrency cap and the
// budget, so a typo like `claude:20` must be an error rather than twenty agents.
const MaxAttempts = 6

// Attempt is one entry of an expanded mix: which agent, on which model ("" =
// the agent's own default).
type Attempt struct {
	Agent string `json:"agent"`
	Model string `json:"model,omitempty"`
}

// Label is the attempt's short display form: "claude", "claude@opus".
func (a Attempt) Label() string {
	if a.Model == "" {
		return a.Agent
	}
	return a.Agent + "@" + a.Model
}

// ParseEntry parses one mix entry `<agent>[@<model>][:<count>]` into count
// copies of its attempt. The count is the text after the LAST colon, and only
// when it is all digits — so a model that itself carries a colon (a Bedrock
// `…-v1:0`) is written with an explicit count: `claude@us.anthropic…-v1:0:1`.
func ParseEntry(entry string) ([]Attempt, error) {
	e := strings.TrimSpace(entry)
	if e == "" {
		return nil, errors.New("empty best-of entry")
	}
	count := 1
	if i := strings.LastIndexByte(e, ':'); i >= 0 && isDigits(e[i+1:]) {
		n, err := strconv.Atoi(e[i+1:])
		if err != nil || n < 1 || n > MaxAttempts {
			return nil, fmt.Errorf("best-of entry %q: count must be 1..%d", entry, MaxAttempts)
		}
		count, e = n, e[:i]
	}
	kind, model, _ := strings.Cut(e, "@")
	kind = strings.ToLower(strings.TrimSpace(kind))
	model = strings.TrimSpace(model)
	if !agent.Valid(kind) {
		return nil, fmt.Errorf("best-of entry %q: unknown agent %q (want claude, codex or opencode)", entry, kind)
	}
	if strings.Contains(e, "@") && !agent.ValidModel(model) {
		return nil, fmt.Errorf("best-of entry %q: invalid model %q", entry, model)
	}
	out := make([]Attempt, count)
	for i := range out {
		out[i] = Attempt{Agent: kind, Model: model}
	}
	return out, nil
}

// ParseMix expands a list of entries (the config's `agents` array) into the
// attempts to spawn, in order. A best-of group needs at least two attempts —
// one is just a normal dispatch — and at most MaxAttempts.
func ParseMix(entries []string) ([]Attempt, error) {
	var out []Attempt
	for _, e := range entries {
		as, err := ParseEntry(e)
		if err != nil {
			return nil, err
		}
		out = append(out, as...)
		if len(out) > MaxAttempts {
			return nil, fmt.Errorf("best-of mix has more than %d attempts", MaxAttempts)
		}
	}
	if len(out) < 2 {
		return nil, errors.New("best-of mix needs at least 2 attempts")
	}
	return out, nil
}

// ParseMixString is ParseMix over the CLI's comma-separated form
// ("claude:2,codex").
func ParseMixString(s string) ([]Attempt, error) {
	return ParseMix(SplitMix(s))
}

// SplitMix splits the comma-separated form into entries, dropping empties.
func SplitMix(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Facts is what Suggest ranks one attempt by — all of it already known to the
// daemon (gh PR facts, the recorded review tallies, local git), none of it
// fetched for the ranking.
type Facts struct {
	// OpenPR is true while the attempt has an OPEN pull request. An attempt
	// without one has nothing to keep yet and is never suggested.
	OpenPR bool
	// Checks is the PR's CI rollup: pass | fail | pending | none | "".
	Checks string
	// Reviewed is true when at least one review pass answered for the current
	// PR; the counts are its findings by severity.
	Reviewed              bool
	Blocker, Major, Minor int
	// Lines is added + deleted against the merge-base (smaller wins a tie:
	// the same result in less code is the better one to review).
	Lines int
}

// checksRank orders CI outcomes best first. "none" (a repo that runs no CI)
// sits between a pass and a run still in flight.
func checksRank(c string) int {
	switch c {
	case "pass":
		return 0
	case "none", "":
		return 1
	case "pending":
		return 2
	default: // fail, or anything unknown
		return 3
	}
}

// Suggest returns the index of the attempt to suggest as the winner, or -1
// when none has an open PR. Lexicographic, best first: CI outcome, then fewer
// blocker findings, then fewer major ones, then reviewed over not-yet-reviewed,
// then fewer minor ones, then the smaller diff; a full tie keeps the earlier
// attempt. Display-only — see the package comment.
func Suggest(fs []Facts) int {
	idx := make([]int, 0, len(fs))
	for i, f := range fs {
		if f.OpenPR {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return -1
	}
	key := func(f Facts) [6]int {
		unreviewed := 0
		if !f.Reviewed {
			unreviewed = 1
		}
		return [6]int{checksRank(f.Checks), f.Blocker, f.Major, unreviewed, f.Minor, f.Lines}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ka, kb := key(fs[idx[a]]), key(fs[idx[b]])
		for i := range ka {
			if ka[i] != kb[i] {
				return ka[i] < kb[i]
			}
		}
		return false
	})
	return idx[0]
}
