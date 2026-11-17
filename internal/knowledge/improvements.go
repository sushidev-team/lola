package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// PreventionIdea is untrusted implementation advice, never an executable rule.
// Validation describes how a future implementation must prove its usefulness.
type PreventionIdea struct {
	Kind       string `json:"kind"` // regression_test, lint, skill
	Change     string `json:"change"`
	Validation string `json:"validation"`
}

func validatePrevention(p *PreventionIdea) error {
	if p == nil {
		return nil
	}
	if p.Kind != "regression_test" && p.Kind != "lint" && p.Kind != "skill" {
		return fmt.Errorf("prevention kind must be regression_test, lint, or skill")
	}
	for _, text := range []string{p.Change, p.Validation} {
		if len(strings.TrimSpace(text)) < 8 || len(text) > 2000 || credential.MatchString(text) || strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
			return fmt.Errorf("prevention needs bounded, non-secret change and validation descriptions")
		}
	}
	return nil
}

type ImprovementSource struct {
	Reference
	Source Source `json:"source"`
	Status Status `json:"status"`
}

type ImprovementDecision struct {
	Scope          Scope       `json:"scope"`
	SourceVersions []Reference `json:"source_versions,omitempty"`
	Revision       string      `json:"revision"`
	Decision       string      `json:"decision"` // dismissed, tracked, reopened
	Evidence       string      `json:"evidence"`
	At             time.Time   `json:"at"`
}

type Improvement struct {
	sourceVersions     []Reference           // full provenance for a durable decision; preview remains bounded
	ID                 string                `json:"id"`
	Revision           string                `json:"revision"`
	Scope              Scope                 `json:"scope"`
	Topic              string                `json:"topic"`
	Problem            string                `json:"problem"`
	Trigger            string                `json:"trigger"`     // recurring, explicit, policy
	Occurrences        int                   `json:"occurrences"` // distinct reporting sessions or attributable sources, not independent proof
	Suggestions        []PreventionIdea      `json:"suggestions"`
	Sources            []ImprovementSource   `json:"sources"`
	OmittedSources     int                   `json:"omitted_sources"`
	OmittedSuggestions int                   `json:"omitted_suggestions"`
	State              string                `json:"state"` // proposed, dismissed, tracked
	Decisions          []ImprovementDecision `json:"decisions,omitempty"`
}

type ImprovementReport struct {
	Proposals []Improvement `json:"proposals"`
	Total     int           `json:"total"`
	NextAfter string        `json:"next_after,omitempty"`
}

func digestJSON(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func normalized(text string) string { return strings.Join(strings.Fields(text), " ") }

// improvements derives proposals from the canonical records. This makes capture,
// dispute, expiry and supersession immediately affect the proposal without a
// second store or a background agent that can manufacture evidence. Exact text
// after whitespace normalization is intentional: similarity is not agreement.
func improvements(data snapshot, scope Scope, now time.Time) []Improvement {
	groups := map[string][]Lesson{}
	for _, l := range data.Lessons {
		if l.Scope.Project != scope.Project || l.Scope.GoalID != scope.GoalID || (l.Status != Candidate && l.Status != Active) || l.ReviewReason != "" || (!l.ExpiresAt.IsZero() && !l.ExpiresAt.After(now)) {
			continue
		}
		paths := slices.Clone(l.Paths)
		slices.Sort(paths)
		paths = slices.Compact(paths)
		if len(paths) == 0 {
			paths = nil
		}
		key := digestJSON(struct {
			Project, Goal, Topic, Summary string
			Paths                         []string
			Policy                        bool
		}{l.Scope.Project, l.Scope.GoalID, normalized(l.Topic), normalized(l.Summary), paths, l.Policy})[:32]
		groups[key] = append(groups[key], l)
	}
	out := []Improvement{}
	for id, lessons := range groups {
		slices.SortFunc(lessons, func(a, b Lesson) int { return strings.Compare(a.ID, b.ID) })
		first := lessons[0]
		p := Improvement{ID: id, Scope: first.Scope, Topic: normalized(first.Topic), Problem: normalized(first.Summary), State: "proposed", Suggestions: []PreventionIdea{}, Sources: []ImprovementSource{}}
		p.Scope.Paths = slices.Clone(first.Paths)
		slices.Sort(p.Scope.Paths)
		p.Scope.Paths = slices.Compact(p.Scope.Paths)
		reports := map[string]bool{}
		ideas := map[string]PreventionIdea{}
		// Revision includes ALL contributing versions, even when the source preview
		// is bounded. A new report or curation decision invalidates an old decision.
		refs := make([]Reference, 0, len(lessons))
		for _, l := range lessons {
			key := l.Source.Kind + ":" + l.Source.ID
			if l.Source.Session != "" {
				key = "session:" + l.Source.Session
			} else if l.Source.Kind == "operator" || l.Source.Kind == "correction" {
				// These IDs are minted per CLI request, not per observed failure.
				// Repeating the same human capture must not invent recurrence.
				key = "operator"
			}
			reports[key] = true
			refs = append(refs, Reference{ID: l.ID, Version: l.Version})
			if len(p.Sources) < 20 {
				p.Sources = append(p.Sources, ImprovementSource{Reference: Reference{ID: l.ID, Version: l.Version}, Source: l.Source, Status: l.Status})
			} else {
				p.OmittedSources++
			}
			if l.Prevention != nil {
				ideas[digestJSON(*l.Prevention)] = *l.Prevention
			}
		}
		p.Occurrences = len(reports)
		switch {
		case first.Policy:
			p.Trigger = "policy"
		case p.Occurrences >= 2:
			p.Trigger = "recurring"
		case len(ideas) > 0:
			p.Trigger = "explicit"
		default:
			continue
		}
		// Keep competing approaches visible, rather than arbitrarily adopting the
		// first reporter's advice. Bound duplicates before constructing the preview.
		keys := make([]string, 0, len(ideas))
		for k := range ideas {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if len(p.Suggestions) == 5 {
				break
			}
			p.Suggestions = append(p.Suggestions, ideas[k])
		}
		if len(p.Suggestions) == 0 && !first.Policy {
			p.Suggestions = append(p.Suggestions, PreventionIdea{Kind: "regression_test", Change: "Reproduce the reported failure and add a focused regression test for: " + p.Problem, Validation: "Show the test failing on the reproduced faulty behavior and passing with the fix; run the repository's required checks. If the report cannot be reproduced or mechanically checked, record that result instead of enforcing the claim."})
		}
		p.OmittedSuggestions = len(ideas) - len(p.Suggestions)
		if p.OmittedSuggestions < 0 {
			p.OmittedSuggestions = 0
		}
		p.sourceVersions = refs
		p.Revision = digestJSON(struct {
			ID   string
			Refs []Reference
		}{id, refs})
		p.Decisions = slices.Clone(data.ImprovementDecisions[id])
		for i := range p.Decisions {
			p.Decisions[i].SourceVersions = nil
		}
		for _, d := range p.Decisions {
			if d.Revision == p.Revision {
				p.State = improvementState(d.Decision)
			}
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Improvement) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (s *Store) Improvements(scope Scope, after string) ImprovementReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := improvements(s.copy(), scope, time.Now())
	report := ImprovementReport{Proposals: []Improvement{}, Total: len(all)}
	for _, p := range all {
		if p.ID <= after {
			continue
		}
		if len(report.Proposals) == 50 {
			report.NextAfter = report.Proposals[len(report.Proposals)-1].ID
			break
		}
		report.Proposals = append(report.Proposals, p)
	}
	return report
}
func (s *Store) Improvement(scope Scope, id string) (Improvement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range improvements(s.copy(), scope, time.Now()) {
		if p.ID == id {
			return p, nil
		}
	}
	return Improvement{}, fmt.Errorf("improvement is unavailable in this scope; reload proposals")
}

// Decisions record human triage, not execution authorization or verification.
// They bind the complete source revision and remain visible when new evidence
// makes a proposal pending again. No decision changes a lesson's status.
func (s *Store) DecideImprovement(scope Scope, id, revision, decision, evidence string) (Improvement, error) {
	if decision != "dismissed" && decision != "tracked" && decision != "reopened" {
		return Improvement{}, fmt.Errorf("decision must be dismissed, tracked, or reopened")
	}
	if len(strings.TrimSpace(evidence)) < 8 || len(evidence) > 2000 || credential.MatchString(evidence) || strings.ContainsFunc(evidence, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return Improvement{}, fmt.Errorf("bounded, non-secret decision evidence required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copy()
	for _, p := range improvements(next, scope, time.Now()) {
		if p.ID != id {
			continue
		}
		if revision == "" || revision != p.Revision {
			return Improvement{}, fmt.Errorf("improvement changed; review the current revision")
		}
		decisionEntry := ImprovementDecision{Scope: p.Scope, SourceVersions: slices.Clone(p.sourceVersions), Revision: revision, Decision: decision, Evidence: evidence, At: time.Now().UTC()}
		if len(p.Decisions) > 0 {
			old := p.Decisions[len(p.Decisions)-1]
			if old.Revision == revision && old.Decision == decision && old.Evidence == evidence {
				return p, nil
			}
		}
		if next.ImprovementDecisions == nil {
			next.ImprovementDecisions = map[string][]ImprovementDecision{}
		}
		if len(next.ImprovementDecisions[id]) >= 100 {
			return Improvement{}, fmt.Errorf("improvement decision history is full")
		}
		next.ImprovementDecisions[id] = append(next.ImprovementDecisions[id], decisionEntry)
		if err := s.commit(next); err != nil {
			return Improvement{}, err
		}
		p.State = improvementState(decision)
		decisionEntry.SourceVersions = nil
		p.Decisions = append(p.Decisions, decisionEntry)
		return p, nil
	}
	return Improvement{}, fmt.Errorf("improvement is unavailable in this scope; reload proposals")
}

// ExportImprovement produces a task brief for the normal coding/review workflow.
// It never installs a skill/linter or changes permissions, acceptance gates or Git.
func ExportImprovement(p Improvement) map[string]string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Improvement proposal: %s\n\nID: %s\nRevision: %s\nProject: %s\nGoal: %s\nState: %s\n\n## Reported problem\n\n%s\n\n", p.Topic, p.ID, p.Revision, p.Scope.Project, p.Scope.GoalID, p.State, p.Problem)
	fmt.Fprintf(&b, "Trigger: %s; %d distinct reporting sessions or attributable sources. Repetition is not independent proof of correctness.\n\nAffected paths: %s\n\n", p.Trigger, p.Occurrences, strings.Join(p.Scope.Paths, ", "))
	b.WriteString("## Proposed implementation\n\n")
	if p.Trigger == "policy" {
		b.WriteString("This proposes a mandatory policy change. Review the proposed rule and its scope with the project owner before implementing it; capture never authorizes changing AGENTS.md, permissions, or acceptance gates.\n\n")
	}
	for _, idea := range p.Suggestions {
		fmt.Fprintf(&b, "### %s\n\n%s\n\nValidation: %s\n\n", idea.Kind, idea.Change, idea.Validation)
	}
	b.WriteString("## Acceptance and delivery\n\n- Inspect the cited lesson versions and reproduce the reported problem before adopting their claims.\n- Keep changes within the named project, goal and paths; confirm the affected repositories for a stack.\n- Demonstrate the proposed prevention catches the failure and permits valid behavior. For a skill, evaluate representative positive and negative tasks and preserve existing permissions.\n- Implement on an ordinary task branch or an explicitly authorized goal workspace. Run required project checks and independent reviews; merge only through the normal human-controlled path.\n- If evidence contradicts the proposal, record the result and dispute the lesson instead of introducing an unsupported rule.\n\n## Source versions\n\nInspect a reference with `lola learn lesson <id> --version <version> --project <project>`; include `--goal <goal>` for goal-scoped evidence.\n\n")
	for _, s := range p.Sources {
		fmt.Fprintf(&b, "- %s v%d (%s; %s %s; session %s; commit %s)\n", s.ID, s.Version, s.Status, s.Source.Kind, s.Source.ID, s.Source.Session, s.Source.Commit)
	}
	if p.OmittedSources > 0 {
		fmt.Fprintf(&b, "\n%d additional source records omitted from this preview; inspect scoped lesson records for the full evidence. The revision covers all contributing versions.\n", p.OmittedSources)
	}
	if p.OmittedSuggestions > 0 {
		fmt.Fprintf(&b, "\n%d additional suggested approaches omitted from this preview; inspect the referenced lessons before selecting an approach.\n", p.OmittedSuggestions)
	}
	body, _ := json.MarshalIndent(p, "", "  ")
	return map[string]string{"IMPROVEMENT.md": b.String(), "proposal.json": string(body) + "\n"}
}

func improvementState(decision string) string {
	if decision == "reopened" {
		return "proposed"
	}
	return decision
}

var improvementID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validateImprovementDecisions(decisions map[string][]ImprovementDecision) error {
	if len(decisions) > 10000 {
		return fmt.Errorf("too many improvement decisions")
	}
	for id, history := range decisions {
		if !improvementID.MatchString(id) || len(history) > 100 {
			return fmt.Errorf("invalid improvement decision history")
		}
		for _, d := range history {
			if d.Scope.Project == "" || len(d.SourceVersions) == 0 || len(d.SourceVersions) > 10000 || digestJSON(struct {
				ID   string
				Refs []Reference
			}{id, d.SourceVersions}) != d.Revision {
				return fmt.Errorf("invalid improvement source receipt")
			}
			previous := ""
			for _, ref := range d.SourceVersions {
				if !improvementID.MatchString(ref.ID) || ref.Version < 1 || ref.ID <= previous {
					return fmt.Errorf("invalid improvement source versions")
				}
				previous = ref.ID
			}
			if !codeDigest.MatchString(d.Revision) || d.At.IsZero() || (d.Decision != "dismissed" && d.Decision != "tracked" && d.Decision != "reopened") || len(strings.TrimSpace(d.Evidence)) < 8 || len(d.Evidence) > 2000 || credential.MatchString(d.Evidence) || strings.ContainsFunc(d.Evidence, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
				return fmt.Errorf("invalid improvement decision")
			}
		}
	}
	return nil
}

// ImprovementHistory remains inspectable after disputes or expiry withdraw a
// proposal. Each decision retains the complete exact-version source receipt.
func (s *Store) ImprovementHistory(scope Scope, id string) []ImprovementDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ImprovementDecision{}
	for _, d := range s.copy().ImprovementDecisions[id] {
		if d.Scope.Project == scope.Project && d.Scope.GoalID == scope.GoalID {
			out = append(out, d)
		}
	}
	return out
}
