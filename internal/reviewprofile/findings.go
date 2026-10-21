package reviewprofile

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

// FindingReport retains the exact reviewer claim even after a retry replaces
// its review record. Decisions annotate that claim; they never change a verdict
// or grant integration/merge authority.
type FindingReport struct {
	ID            string         `json:"id"`
	ReviewKey     string         `json:"review_key"`
	Attempt       int            `json:"attempt"`
	Index         int            `json:"index"`
	Profile       string         `json:"profile"`
	Provider      string         `json:"provider"`
	RubricVersion string         `json:"rubric_version"`
	Inputs        Inputs         `json:"inputs"`
	Finding       Finding        `json:"finding"`
	ReportedAt    time.Time      `json:"reported_at"`
	Revision      uint64         `json:"revision"`
	State         string         `json:"state"`
	Events        []FindingEvent `json:"events"`
}

type FindingDecision struct {
	ID              string `json:"id"`
	Revision        uint64 `json:"revision"`
	State           string `json:"state"`
	Evidence        string `json:"evidence"`
	FixHead         string `json:"fix_head,omitempty"`
	VerificationKey string `json:"verification_key,omitempty"`
}

type FindingEvent struct {
	Revision     uint64    `json:"revision"`
	State        string    `json:"state"`
	Evidence     string    `json:"evidence"`
	FixHead      string    `json:"fix_head,omitempty"`
	Verification *Record   `json:"verification,omitempty"`
	At           time.Time `json:"at"`
}

func findingIdentity(key string, attempt, index int, f Finding) string {
	body, _ := json.Marshal(struct {
		Key            string
		Attempt, Index int
		Finding        Finding
	}{key, attempt, index, f})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:16])
}

func registerFindings(out map[string]FindingReport, r Record) {
	if !r.Result.Complete || (r.State != "pass" && r.State != "findings") {
		return
	}
	for index, finding := range r.Result.Findings {
		id := findingIdentity(r.Key, r.Attempt, index, finding)
		if _, exists := out[id]; exists {
			continue
		}
		out[id] = FindingReport{ID: id, ReviewKey: r.Key, Attempt: r.Attempt, Index: index, Profile: r.Profile, Provider: r.Provider,
			RubricVersion: r.Version, Inputs: r.Inputs, Finding: finding, ReportedAt: r.UpdatedAt, Revision: 1, State: "reported", Events: []FindingEvent{}}
	}
}

func cloneFindings(in map[string]FindingReport) map[string]FindingReport {
	body, _ := json.Marshal(in)
	out := map[string]FindingReport{}
	_ = json.Unmarshal(body, &out)
	if out == nil {
		out = map[string]FindingReport{}
	}
	return out
}

func (s *Store) Findings() []FindingReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []FindingReport{}
	for _, f := range cloneFindings(s.findings) {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b FindingReport) int { return strings.Compare(a.ID, b.ID) })
	return out
}

var fixCommit = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)
var feedbackSecret = regexp.MustCompile(`(?i)(-----BEGIN [A-Z ]*PRIVATE KEY-----|(?:gh[pousr]_|github_pat_|sk-(?:ant-|proj-)?|lin_api_)[A-Za-z0-9_\-]{12,}|(?:api[_-]?key|password|secret|access[_-]?token)\s*[:=]\s*["']?[A-Za-z0-9_\-+/=]{8,})`)

func validFindingTransition(from, to string) bool {
	switch from {
	case "reported":
		return to == "accepted" || to == "disputed" || to == "dismissed"
	case "accepted":
		return to == "fixed" || to == "disputed"
	case "disputed":
		return to == "accepted" || to == "dismissed"
	case "fixed":
		return to == "verified" || to == "disputed"
	case "verified", "dismissed":
		return to == "disputed"
	}
	return false
}

func validFindingEvidence(text string) bool {
	return len(strings.TrimSpace(text)) >= 8 && len(text) <= 4000 && !feedbackSecret.MatchString(text) &&
		!strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}

func verifyFindingFix(f FindingReport, r Record, head string) error {
	if r.Key == f.ReviewKey || r.State != "pass" || !r.Result.Complete || len(r.Result.Evidence) == 0 || len(r.Result.Findings) != 0 ||
		r.Profile != f.Profile || r.Provider != f.Provider || r.Version != f.RubricVersion || r.Inputs.Repo != f.Inputs.Repo ||
		r.Inputs.Contract != f.Inputs.Contract || r.Inputs.Head != head {
		return fmt.Errorf("verification requires a clean independent review of the recorded fix commit, same repository, profile, provider, rubric and contract")
	}
	return nil
}

// Decide is operator curation. A fixed claim names an exact new commit. Verified
// additionally requires a completed matching independent review, snapshotted in
// the event. Neither one modifies the original result or opens its delivery gate.
func (s *Store) Decide(a FindingDecision) (FindingReport, error) {
	if a.Revision == 0 || !validFindingEvidence(a.Evidence) {
		return FindingReport{}, fmt.Errorf("reviewed revision and bounded, non-secret decision evidence required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneFindings(s.findings)
	f, ok := next[a.ID]
	if !ok {
		return f, fmt.Errorf("unknown specialist finding")
	}
	// Lost-response retries are safe only for the identical historical decision.
	for _, event := range f.Events {
		key := ""
		if event.Verification != nil {
			key = event.Verification.Key
		}
		if event.Revision-1 == a.Revision && event.State == a.State && event.Evidence == a.Evidence && event.FixHead == a.FixHead && key == a.VerificationKey {
			return f, nil
		}
	}
	if f.Revision != a.Revision {
		return f, fmt.Errorf("finding changed; refresh before deciding")
	}
	if len(f.Events) >= 64 {
		return f, fmt.Errorf("finding decision history is full; retain evidence for human review")
	}
	if !validFindingTransition(f.State, a.State) {
		return f, fmt.Errorf("cannot change finding from %s to %s", f.State, a.State)
	}
	if a.State != "fixed" && a.State != "verified" && a.FixHead != "" || a.State != "verified" && a.VerificationKey != "" {
		return f, fmt.Errorf("fix and verification references do not apply to this decision")
	}
	event := FindingEvent{Revision: f.Revision + 1, State: a.State, Evidence: a.Evidence, FixHead: a.FixHead, At: time.Now().UTC()}
	if a.State == "fixed" {
		if !fixCommit.MatchString(a.FixHead) || a.FixHead == f.Inputs.Head {
			return f, fmt.Errorf("fixed requires an exact new fix commit")
		}
	}
	if a.State == "verified" {
		last := f.Events[len(f.Events)-1]
		if a.FixHead != last.FixHead {
			return f, fmt.Errorf("verification must name the recorded fix commit")
		}
		r, exists := s.records[a.VerificationKey]
		if !exists {
			return f, fmt.Errorf("unknown verification review")
		}
		if err := verifyFindingFix(f, r, a.FixHead); err != nil {
			return f, err
		}
		event.Verification = &r
	}
	f.Events = append(f.Events, event)
	f.State, f.Revision = event.State, event.Revision
	next[a.ID] = f
	if err := s.saveWithFindings(clone(s.records), next); err != nil {
		return FindingReport{}, err
	}
	return cloneFindings(next)[a.ID], nil
}

func validateFindingReport(f FindingReport) error {
	if f.ID != findingIdentity(f.ReviewKey, f.Attempt, f.Index, f.Finding) || f.Attempt < 1 || f.Index < 0 || f.Profile == "" || f.ReportedAt.IsZero() || f.Revision != uint64(len(f.Events))+1 || len(f.Events) > 64 {
		return fmt.Errorf("invalid specialist finding identity or history")
	}
	state := "reported"
	var previous FindingEvent
	for i, event := range f.Events {
		if event.Revision != uint64(i)+2 || !validFindingTransition(state, event.State) || !validFindingEvidence(event.Evidence) || event.At.IsZero() || event.At.Before(f.ReportedAt) || (i > 0 && event.At.Before(previous.At)) {
			return fmt.Errorf("invalid specialist finding decision history")
		}
		switch event.State {
		case "fixed":
			if !fixCommit.MatchString(event.FixHead) || event.FixHead == f.Inputs.Head || event.Verification != nil {
				return fmt.Errorf("invalid fix claim")
			}
		case "verified":
			if event.FixHead != previous.FixHead || event.Verification == nil {
				return fmt.Errorf("missing fix verification")
			}
			if err := verifyFindingFix(f, *event.Verification, event.FixHead); err != nil {
				return err
			}
		default:
			if event.FixHead != "" || event.Verification != nil {
				return fmt.Errorf("unexpected finding verification")
			}
		}
		state, previous = event.State, event
	}
	if f.State != state {
		return fmt.Errorf("finding state does not match its history")
	}
	return nil
}

// CalibrationGroup keeps versions and repositories apart. Counts describe
// explicit operator assessments, not an objective accuracy score or evidence
// that adding reviewers improves results. Unassessed findings remain visible.
type CalibrationGroup struct {
	Profile          string `json:"profile"`
	Provider         string `json:"provider"`
	RubricVersion    string `json:"rubric_version"`
	Repo             string `json:"repo"`
	Findings         int    `json:"findings"`
	Reported         int    `json:"reported"`
	Accepted         int    `json:"accepted"`
	Disputed         int    `json:"disputed"`
	Dismissed        int    `json:"dismissed"`
	Fixed            int    `json:"fixed"`
	Verified         int    `json:"verified"`
	BlockingDisputes int    `json:"blocking_disputes"`
}

func (s *Store) Calibration() []CalibrationGroup {
	groups := map[string]CalibrationGroup{}
	for _, f := range s.Findings() {
		keyBytes, _ := json.Marshal([]string{f.Profile, f.Provider, f.RubricVersion, f.Inputs.Repo})
		key := string(keyBytes)
		g := groups[key]
		g.Profile, g.Provider, g.RubricVersion, g.Repo = f.Profile, f.Provider, f.RubricVersion, f.Inputs.Repo
		g.Findings++
		switch f.State {
		case "reported":
			g.Reported++
		case "accepted":
			g.Accepted++
		case "disputed":
			g.Disputed++
			if f.Finding.Severity == "major" || f.Finding.Severity == "blocker" {
				g.BlockingDisputes++
			}
		case "dismissed":
			g.Dismissed++
		case "fixed":
			g.Fixed++
		case "verified":
			g.Verified++
		}
		groups[key] = g
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := []CalibrationGroup{}
	for _, key := range keys {
		out = append(out, groups[key])
	}
	return out
}
