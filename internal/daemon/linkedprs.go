package daemon

import (
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

// Linked-repository PRs ([[project.linked_repo]]): a session whose issue spans
// several repositories has one PR per repository it changed. The PRIMARY PR
// stays the session's PR and alone drives the delivery axis, reactions and
// write-back; the linked ones are observed beside it for two readers only —
// the UIs (SessionInfo.linkedPrs) and the merged cleanup, which waits while
// any of them is still open (Session.LinkedPRsSettled).

// linkedPRFact is one cycle's answer for one linked repository.
type linkedPRFact struct {
	name string
	pr   *scm.PR
}

// observeLinkedPRs asks gh for each linked repository's PR on its branch.
// Bounded per call like every observer exec; a failed fetch is logged and
// simply absent from the result, so the last known facts stand.
func (d *Daemon) observeLinkedPRs(ctx context.Context, s session.Session) []linkedPRFact {
	var out []linkedPRFact
	for _, l := range s.Linked {
		if l.Repo == "" || l.Branch == "" {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		pr, err := d.prForBranch(cctx, l.Repo, l.Branch)
		cancel()
		if err != nil {
			d.logf("", "observe: PR check for %s linked repo %s (branch %s in %s) failed: %v", s.ID, l.Name, l.Branch, l.Repo, err)
			continue
		}
		out = append(out, linkedPRFact{name: l.Name, pr: pr})
	}
	return out
}

// applyLinkedPRs merges a cycle's linked facts into cur (under the store
// lock), reporting whether any PR changed. The slice is replaced, never
// mutated in place: snapshots handed out earlier share its backing array.
func applyLinkedPRs(cur *session.Session, facts []linkedPRFact, now time.Time) bool {
	if len(facts) == 0 {
		return false
	}
	next := slices.Clone(cur.Linked)
	changed := false
	for _, f := range facts {
		for i := range next {
			if next[i].Name != f.name {
				continue
			}
			if !reflect.DeepEqual(next[i].PR, f.pr) {
				changed = true
			}
			next[i].PR = f.pr
			next[i].PRObservedAt = now
		}
	}
	cur.Linked = next
	return changed
}

// linkedPRInfo flattens a session's linked repositories for the wire.
func linkedPRInfo(linked []session.LinkedRepo) []protocol.LinkedPR {
	if len(linked) == 0 {
		return nil
	}
	out := make([]protocol.LinkedPR, 0, len(linked))
	for _, l := range linked {
		li := protocol.LinkedPR{Name: l.Name, Repo: l.Repo, Branch: l.Branch}
		if l.PR != nil {
			li.Number, li.URL, li.State, li.Checks, li.Draft = l.PR.Number, l.PR.URL, l.PR.State, l.PR.ChecksState, l.PR.IsDraft
		}
		out = append(out, li)
	}
	return out
}
