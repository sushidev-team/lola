package goal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"
)

// BriefSource is an exact human-authored Linear snapshot, never executable policy.
type BriefSource struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	ObservedAt  time.Time `json:"observed_at"`
}
type BriefContract struct {
	Title      string   `json:"title"`
	Outcome    string   `json:"outcome"`
	Acceptance []string `json:"acceptance"`
	NonGoals   []string `json:"non_goals,omitempty"`
}
type BriefRevision struct {
	ArchivedFinals []FinalVerification `json:"archived_finals,omitempty"`
	EvidenceURL    string              `json:"evidence_url,omitempty"`
	SourceID       string              `json:"source_id"`
	Before         BriefContract       `json:"before"`
	After          BriefContract       `json:"after"`
	Reason         string              `json:"reason"`
	AcceptedAt     time.Time           `json:"accepted_at"`
}
type BriefAcceptance struct {
	ID       string        `json:"id"`
	Revision uint64        `json:"revision"`
	SourceID string        `json:"source_id"`
	Contract BriefContract `json:"contract"`
	Reason   string        `json:"reason"`
}

func NewBriefSource(title, description string, now time.Time) (BriefSource, error) {
	if strings.TrimSpace(title) == "" || len(title) > 512 || len(description) > 128<<10 || now.IsZero() {
		return BriefSource{}, errors.New("Linear brief exceeds snapshot limits or lacks title/time")
	}
	b, _ := json.Marshal([]string{title, description})
	sum := sha256.Sum256(b)
	return BriefSource{ID: hex.EncodeToString(sum[:]), Title: title, Description: description, ObservedAt: now}, nil
}
func (g Goal) BriefContract() BriefContract {
	return BriefContract{g.Title, g.Outcome, slices.Clone(g.Acceptance), slices.Clone(g.NonGoals)}
}
func (g Goal) ParentIssueText() (string, string) {
	if len(g.BriefRevisions) > 0 {
		id := g.BriefRevisions[len(g.BriefRevisions)-1].SourceID
		for _, s := range g.BriefSources {
			if s.ID == id {
				return s.Title, s.Description
			}
		}
	}
	return "Goal — " + g.Title, g.ParentDescription()
}

// ObserveBrief retains bounded immutable proposals. Repeated reads do not grow history.
func (g *Goal) ObserveBrief(s BriefSource) error {
	checked, err := NewBriefSource(s.Title, s.Description, s.ObservedAt)
	if err != nil || checked.ID != s.ID {
		return errors.New("invalid brief snapshot")
	}
	title, body := g.ParentIssueText()
	if s.Title == title && s.Description == body {
		g.PendingBrief = ""
		return nil
	}
	for _, old := range g.BriefSources {
		if old.ID == s.ID {
			g.PendingBrief = s.ID
			return nil
		}
	}
	if len(g.BriefSources) >= 64 {
		return errors.New("brief snapshot limit reached; retain history and create a follow-up goal")
	}
	g.BriefSources = append(g.BriefSources, s)
	g.PendingBrief = s.ID
	return nil
}

// AcceptBrief reconciles an explicit contract against the existing task plan.
// Active work and verification must settle first. Policy and task contracts
// remain immutable; a changed brief cannot confer new tools or permissions.
func (g *Goal) AcceptBrief(a BriefAcceptance, now time.Time) error {
	if a.ID != g.ID || a.Revision == 0 || a.Revision != g.Revision || a.SourceID == "" || a.SourceID != g.PendingBrief || now.IsZero() || strings.TrimSpace(a.Reason) == "" || len(a.Reason) > 2000 {
		return errors.New("review the current goal revision, proposed brief and reconciliation reason")
	}
	if g.State == "accepted" || g.State == "canceled" || !g.Parent.Confirmed {
		return errors.New("goal cannot accept a brief revision")
	}
	if e := g.Execution; e != nil {
		if (g.State != "paused" && g.State != "blocked") || e.PendingIntegration != "" || e.PendingReplan != "" || e.PendingRefresh != "" || len(e.Refreshes) > 0 || e.Delivery != nil {
			return errors.New("brief revision requires paused work with no pending integration, replan, target refresh or delivery")
		}
		if g.briefFinalRounds()+len(e.FinalHistory)+boolInt(e.Final != nil) >= e.Policy.Limits.ReviewRounds {
			return errors.New("brief revision would exceed the accepted whole-goal review budget")
		}
		for _, a := range e.Attempts {
			if activeAttempt(a.State) {
				return errors.New("stop or finish active attempts before reconciling the brief")
			}
		}
	}
	var drafts []TaskDraft
	for _, t := range g.Tasks {
		drafts = append(drafts, t.TaskDraft)
	}
	c := a.Contract
	if err := ValidateDraft(Draft{Project: g.Project, Title: c.Title, Outcome: c.Outcome, Acceptance: c.Acceptance, NonGoals: c.NonGoals, Tasks: drafts}); err != nil {
		return err
	}
	if len(c.NonGoals) > 50 {
		return errors.New("at most 50 non-goals permitted")
	}
	for _, v := range c.NonGoals {
		if len(v) > 2000 || Marked(v) {
			return errors.New("invalid non-goal")
		}
	}
	revision := BriefRevision{SourceID: a.SourceID, Before: g.BriefContract(), After: c, Reason: a.Reason, AcceptedAt: now}
	if e := g.Execution; e != nil {
		revision.ArchivedFinals = append(revision.ArchivedFinals, e.FinalHistory...)
		if e.Final != nil {
			revision.ArchivedFinals = append(revision.ArchivedFinals, *e.Final)
		}
		revision.EvidenceURL = e.DeliveryEvidenceURL
		e.Final, e.FinalHistory, e.DeliveryEvidenceURL = nil, nil, ""
	}
	g.BriefRevisions = append(g.BriefRevisions, revision)
	g.Title, g.Outcome, g.Acceptance, g.NonGoals = c.Title, c.Outcome, slices.Clone(c.Acceptance), slices.Clone(c.NonGoals)
	g.PendingBrief = ""
	g.SyncError = ""
	if g.Execution != nil {
		g.State = "paused"
		g.Execution.Reason = "Brief revision accepted; review the retained task plan before resuming"
	}
	return nil
}
func (g Goal) validateBrief() error {
	if len(g.BriefSources) > 64 || len(g.BriefRevisions) > 64 {
		return errors.New("brief history exceeds limit")
	}
	sources := map[string]bool{}
	for _, s := range g.BriefSources {
		checked, err := NewBriefSource(s.Title, s.Description, s.ObservedAt)
		if err != nil || checked.ID != s.ID || sources[s.ID] {
			return errors.New("invalid brief source history")
		}
		sources[s.ID] = true
	}
	if g.PendingBrief != "" && !sources[g.PendingBrief] {
		return errors.New("pending brief source is missing")
	}
	finalIDs := map[string]bool{}
	for i, r := range g.BriefRevisions {
		for _, f := range r.ArchivedFinals {
			if g.Execution == nil || f.ID == "" || f.CreatedAt.IsZero() || finalIDs[f.ID] || (f.Receipt == "") != f.CompletedAt.IsZero() {
				return errors.New("invalid superseded brief verification")
			}
			if err := validateManifest(g.Execution.Policy, f.Manifest); err != nil {
				return err
			}
			finalIDs[f.ID] = true
		}
		if r.EvidenceURL != "" {
			if err := ValidateEvidenceURL(r.EvidenceURL); err != nil {
				return err
			}
		}
		if !sources[r.SourceID] || r.AcceptedAt.IsZero() || strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 2000 {
			return errors.New("invalid accepted brief revision")
		}
		if i > 0 && !reflect.DeepEqual(g.BriefRevisions[i-1].After, r.Before) {
			return errors.New("brief history is discontinuous")
		}
	}
	if g.Execution != nil {
		if g.briefFinalRounds()+len(g.Execution.FinalHistory)+boolInt(g.Execution.Final != nil) > g.Execution.Policy.Limits.ReviewRounds {
			return errors.New("brief revisions exceeded whole-goal review budget")
		}
		if f := g.Execution.Final; f != nil && finalIDs[f.ID] {
			return errors.New("superseded brief verification reused")
		}
		for _, f := range g.Execution.FinalHistory {
			if finalIDs[f.ID] {
				return errors.New("superseded brief history reused")
			}
		}
	}
	if len(g.BriefRevisions) > 0 && !reflect.DeepEqual(g.BriefRevisions[len(g.BriefRevisions)-1].After, g.BriefContract()) {
		return errors.New("accepted brief differs from its contract")
	}
	return nil
}
func preserveBrief(old, g Goal) error {
	if len(g.BriefSources) < len(old.BriefSources) || len(g.BriefRevisions) < len(old.BriefRevisions) {
		return errors.New("brief history must be retained")
	}
	for i, s := range old.BriefSources {
		if !reflect.DeepEqual(s, g.BriefSources[i]) {
			return errors.New("brief source history is immutable")
		}
	}
	for i, r := range old.BriefRevisions {
		if !reflect.DeepEqual(r, g.BriefRevisions[i]) {
			return errors.New("accepted brief history is immutable")
		}
	}
	if len(g.BriefRevisions) > len(old.BriefRevisions) {
		if len(g.BriefRevisions) != len(old.BriefRevisions)+1 {
			return errors.New("accept one brief revision at a time")
		}
		r := g.BriefRevisions[len(old.BriefRevisions)]
		expected := copyGoal(old)
		if err := expected.AcceptBrief(BriefAcceptance{ID: old.ID, Revision: old.Revision, SourceID: r.SourceID, Contract: r.After, Reason: r.Reason}, r.AcceptedAt); err != nil {
			return err
		}
		if !reflect.DeepEqual(expected.BriefRevisions, g.BriefRevisions) || g.PendingBrief != "" || g.State != expected.State || (g.Execution != nil && (g.Execution.Final != nil || len(g.Execution.FinalHistory) > 0 || g.Execution.DeliveryEvidenceURL != "")) {
			return errors.New("brief acceptance must preserve its reviewed transition")
		}
	} else if (old.Parent.Confirmed || old.Execution != nil) && !reflect.DeepEqual(old.BriefContract(), g.BriefContract()) {
		return errors.New("published brief changes require an accepted revision")
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func (g Goal) briefFinalRounds() int {
	n := 0
	for _, r := range g.BriefRevisions {
		n += len(r.ArchivedFinals)
	}
	return n
}
