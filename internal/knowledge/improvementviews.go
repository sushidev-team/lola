package knowledge

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// ImprovementRecord keeps decided proposals discoverable after their sources
// no longer support an active proposal. Available controls triage/export; a
// retained decision does not authorize applying an obsolete source revision.
type ImprovementRecord struct {
	ID            string `json:"id"`
	Revision      string `json:"revision"`
	Scope         Scope  `json:"scope"`
	Topic         string `json:"topic"`
	State         string `json:"state"`
	Available     bool   `json:"available"`
	DecisionCount int    `json:"decision_count"`
}
type ImprovementCatalog struct {
	Records   []ImprovementRecord `json:"records"`
	Total     int                 `json:"total"`
	NextAfter string              `json:"next_after,omitempty"`
}
type ImprovementSources struct {
	ID         string      `json:"id"`
	Revision   string      `json:"revision"`
	Scope      Scope       `json:"scope"`
	References []Reference `json:"references"`
	Total      int         `json:"total"`
	NextAfter  string      `json:"next_after,omitempty"`
}

// ImprovementTriage is a reviewed decision, never an execution permission.
type ImprovementTriage struct {
	Scope    Scope  `json:"scope"`
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Decision string `json:"decision"`
	Evidence string `json:"evidence"`
}

func sameImprovementScope(a, b Scope) bool { return a.Project == b.Project && a.GoalID == b.GoalID }
func snapshotLessonVersion(data snapshot, ref Reference) (Lesson, bool) {
	if l, ok := data.Lessons[ref.ID]; ok && l.Version == ref.Version {
		return l, true
	}
	for _, l := range data.History[ref.ID] {
		if l.Version == ref.Version {
			return l, true
		}
	}
	return Lesson{}, false
}

// ImprovementCatalog is an on-demand local read. Each page is bounded and sorted
// by stable ID; new evidence can change later pages, so clients refresh before a
// mutation and pin its explicit proposal revision.
func (s *Store) ImprovementCatalog(scope Scope, after string) ImprovementCatalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.copy()
	records := map[string]ImprovementRecord{}
	for _, p := range improvements(data, scope, time.Now()) {
		records[p.ID] = ImprovementRecord{ID: p.ID, Revision: p.Revision, Scope: p.Scope, Topic: p.Topic, State: p.State, Available: true, DecisionCount: len(p.Decisions)}
	}
	for id, history := range data.ImprovementDecisions {
		if _, ok := records[id]; ok {
			continue
		}
		var latest *ImprovementDecision
		count := 0
		for i := range history {
			if sameImprovementScope(scope, history[i].Scope) {
				latest = &history[i]
				count++
			}
		}
		if latest == nil {
			continue
		}
		// The topic comes from the immutable evidence retained by this decision.
		topic := "Retained improvement decision"
		if len(latest.SourceVersions) > 0 {
			if l, ok := snapshotLessonVersion(data, latest.SourceVersions[0]); ok && sameImprovementScope(l.Scope, scope) {
				topic = l.Topic
			}
		}
		records[id] = ImprovementRecord{ID: id, Revision: latest.Revision, Scope: latest.Scope, Topic: topic, State: improvementState(latest.Decision), DecisionCount: count}
	}
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := ImprovementCatalog{Records: []ImprovementRecord{}, Total: len(ids)}
	for _, id := range ids {
		if id <= after {
			continue
		}
		if len(out.Records) == 50 {
			out.NextAfter = out.Records[len(out.Records)-1].ID
			break
		}
		out.Records = append(out.Records, records[id])
	}
	return out
}

// ImprovementSourcePage exposes every exact-version reference, not just the
// bounded proposal preview. Old revisions are inspectable only when retained by
// a durable decision. A revision that merely appeared in an earlier read is not
// silently reconstructed from different sources.
func (s *Store) ImprovementSourcePage(scope Scope, id, revision, after string) (ImprovementSources, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ImprovementSources{ID: id, Revision: revision, Scope: scope, References: []Reference{}}
	if scope.Project == "" || revision == "" {
		return out, fmt.Errorf("project and reviewed improvement revision required")
	}
	data := s.copy()
	var refs []Reference
	for _, p := range improvements(data, scope, time.Now()) {
		if p.ID == id && p.Revision == revision {
			refs = p.sourceVersions
			out.Scope = p.Scope
			break
		}
	}
	if refs == nil {
		for _, d := range data.ImprovementDecisions[id] {
			if sameImprovementScope(d.Scope, scope) && d.Revision == revision {
				refs = d.SourceVersions
				out.Scope = d.Scope
				break
			}
		}
	}
	if refs == nil {
		return out, fmt.Errorf("improvement source revision is unavailable in this scope; reload proposals")
	}
	out.Total = len(refs)
	for _, ref := range refs {
		if strings.Compare(ref.ID, after) <= 0 {
			continue
		}
		if len(out.References) == 50 {
			out.NextAfter = out.References[len(out.References)-1].ID
			break
		}
		out.References = append(out.References, ref)
	}
	return out, nil
}

// ImprovementDecisionHistory is the bounded presentation of the existing
// append-only receipts. Full source references are paged separately; the CLI's
// original ImprovementHistory still returns the complete receipt unchanged.
func (s *Store) ImprovementDecisionHistory(scope Scope, id string) []ImprovementDecision {
	out := s.ImprovementHistory(scope, id)
	for i := range out {
		out[i].SourceVersions = nil
	}
	return out
}
