package goal

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
)

// ProgressUpdate is a durable outbox item. Its identity and observed summary
// never change after reservation; failed sends retry the same remote UUID.
type ProgressUpdate struct {
	DismissedAt time.Time `json:"dismissed_at,omitempty"`
	Resolution  string    `json:"resolution,omitempty"`
	SentAt      time.Time `json:"sent_at,omitempty"`
	ID          string    `json:"id"`
	Summary     string    `json:"summary"`
	CreatedAt   time.Time `json:"created_at"`
	ConfirmedAt time.Time `json:"confirmed_at,omitempty"`
	AttemptedAt time.Time `json:"attempted_at,omitempty"`
}

func (u ProgressUpdate) Body(goalID string) string {
	return fmt.Sprintf("<!-- lola-goal-progress:%s:%s -->\n\n## Lola goal progress\n\nObserved %s.\n\n%s", goalID, u.ID, u.CreatedAt.UTC().Format(time.RFC3339), u.Summary)
}

// ProgressSummary deliberately excludes heartbeats, usage counters and raw
// tool errors. Only durable decisions, verification and delivery change it.
func (g Goal) ProgressSummary() string {
	e := g.Execution
	if e == nil {
		return ""
	}
	var b strings.Builder
	state := g.State
	if !slices.Contains([]string{"preparing", "running", "verifying", "ready", "accepted", "paused", "blocked", "failed", "canceled"}, state) {
		state = "unknown"
	}
	fmt.Fprintf(&b, "Execution: **%s**.\n\n", state)
	if e.Policy.RequireHumanAcceptance {
		if e.HumanAcceptance != nil {
			b.WriteString("Human acceptance recorded for the verified, merged goal delivery.\n\n")
		} else if g.Delivered() {
			b.WriteString("Delivery is merged. A separate human acceptance decision is still required in Lola.\n\n")
		} else {
			b.WriteString("This goal requires separate human acceptance after all final PR deliveries are observed merged.\n\n")
		}
	}
	integrated, superseded, blocked := 0, 0, 0
	for _, t := range g.Tasks {
		p := e.Tasks[t.ID]
		if len(p.ReplacedBy) > 0 {
			superseded++
			continue
		}
		switch p.State {
		case "integrated":
			integrated++
		case "blocked":
			blocked++
		}
	}
	fmt.Fprintf(&b, "Tasks: %d of %d current tasks integrated; %d superseded; %d blocked. Integration is verified goal-branch work, not target-branch delivery.\n\n", integrated, len(g.Tasks)-superseded, superseded, blocked)
	switch {
	case g.PendingBrief != "":
		b.WriteString("Decision needed: the Linear brief changed. Review the proposed revision in Lola before new work.\n\n")
	case g.SyncError != "":
		b.WriteString("Blocker: Linear bindings or execution authority require reconciliation in Lola.\n\n")
	case state == "blocked":
		b.WriteString("Blocker: execution is held. Inspect the retained evidence and reason in Lola.\n\n")
	}
	if !e.StopWorkersAt.IsZero() {
		b.WriteString("Stopping workers; retained workspaces and evidence remain available.\n\n")
	}
	fmt.Fprintf(&b, "Accepted brief revisions: %d. Task-plan revisions: %d.\n\n", len(g.BriefRevisions), len(e.Replans))
	if g.PendingBrief != "" {
		b.WriteString("Whole-goal verification: proposed brief changes have not been verified. Prior evidence remains in Lola.\n\n")
	} else if e.Final != nil && e.Final.Receipt != "" {
		b.WriteString("Whole-goal verification: **passed** on the current repository combination.\n\n")
	} else {
		b.WriteString("Whole-goal verification: **not yet established** for the current brief.\n\n")
	}
	if e.Delivery == nil {
		b.WriteString("Delivery: no target-branch merge has been recorded.\n")
	} else {
		merged, published, unchanged := 0, 0, 0
		for _, m := range e.Delivery.Members {
			switch m.State {
			case "merged":
				merged++
			case "published":
				published++
			case "unchanged":
				unchanged++
			}
		}
		fmt.Fprintf(&b, "Delivery: %d merged, %d awaiting human review/merge, %d unchanged across %d repositories.\n", merged, published, unchanged, len(e.Policy.Members))
		ids := []string{}
		for id := range e.Delivery.Members {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			m := e.Delivery.Members[id]
			if safeProgressURL(m.URL) {
				fmt.Fprintf(&b, "\n- [Pull request #%d](%s)\n", m.Number, m.URL)
			}
		}
	}
	return b.String()
}
func safeProgressURL(raw string) bool {
	if len(raw) > 2000 || strings.ContainsAny(raw, "\r\n\\()<> ") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
func (g *Goal) ReserveProgress(id string, now time.Time) (bool, error) {
	if g.Execution == nil || !g.Parent.Confirmed || id == "" || now.IsZero() {
		return false, errors.New("progress requires an authorized published goal and update identity")
	}
	summary := g.ProgressSummary()
	if len(g.ProgressUpdates) > 0 {
		last := g.ProgressUpdates[len(g.ProgressUpdates)-1]
		if !last.Resolved() || last.Summary == summary || now.Sub(last.CreatedAt) < 2*time.Minute {
			return false, nil
		}
	}
	if len(g.ProgressUpdates) >= 512 {
		return false, errors.New("goal progress update limit reached")
	}
	if len(summary) > 14000 {
		return false, errors.New("goal progress summary exceeds limit")
	}
	for _, u := range g.ProgressUpdates {
		if u.ID == id {
			return false, errors.New("progress identity already used")
		}
	}
	g.ProgressUpdates = append(g.ProgressUpdates, ProgressUpdate{ID: id, Summary: summary, CreatedAt: now})
	return true, nil
}
func (g Goal) validateProgress() error {
	if len(g.ProgressUpdates) > 512 || len(g.ProgressError) > 2000 {
		return errors.New("invalid bounded progress history")
	}
	seen := map[string]bool{}
	for i, u := range g.ProgressUpdates {
		if !issueUUID.MatchString(u.ID) || seen[u.ID] || u.CreatedAt.IsZero() || (!u.SentAt.IsZero() && u.SentAt.Before(u.CreatedAt)) || u.Summary == "" || len(u.Summary) > 14000 || (!u.ConfirmedAt.IsZero() && u.ConfirmedAt.Before(u.CreatedAt)) || (!u.AttemptedAt.IsZero() && u.AttemptedAt.Before(u.CreatedAt)) {
			return errors.New("invalid progress update")
		}
		if i < len(g.ProgressUpdates)-1 && !u.Resolved() {
			return errors.New("progress history has an unresolved send")
		}
		if !u.DismissedAt.IsZero() && (!u.ConfirmedAt.IsZero() || u.DismissedAt.Before(u.CreatedAt) || strings.TrimSpace(u.Resolution) == "" || len(u.Resolution) > 2000) {
			return errors.New("invalid progress resolution")
		}
		if u.DismissedAt.IsZero() && u.Resolution != "" {
			return errors.New("progress resolution lacks timestamp")
		}
		seen[u.ID] = true
	}
	return nil
}
func preserveProgress(old, g Goal) error {
	if len(g.ProgressUpdates) < len(old.ProgressUpdates) || len(g.ProgressUpdates) > len(old.ProgressUpdates)+1 {
		return errors.New("progress history must be retained; reserve one update at a time")
	}
	for i, a := range old.ProgressUpdates {
		b := g.ProgressUpdates[i]
		if a.ID != b.ID || a.Summary != b.Summary || a.CreatedAt != b.CreatedAt || (!a.ConfirmedAt.IsZero() && a.ConfirmedAt != b.ConfirmedAt) || b.AttemptedAt.Before(a.AttemptedAt) || (!a.SentAt.IsZero() && a.SentAt != b.SentAt) {
			return errors.New("progress intent changed or confirmation regressed")
		}
		if a.Resolved() && !reflect.DeepEqual(a, b) {
			return errors.New("confirmed progress history is immutable")
		}
	}
	return nil
}

// Resolved includes an explicit human dismissal; dismissal never claims that a
// remote comment was delivered, and does not retry its content under a new ID.
func (u ProgressUpdate) Resolved() bool { return !u.ConfirmedAt.IsZero() || !u.DismissedAt.IsZero() }

type ProgressResolution struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	UpdateID string `json:"update_id"`
	Reason   string `json:"reason"`
}

func (g *Goal) DismissProgress(a ProgressResolution, now time.Time) error {
	if a.ID != g.ID || a.Revision == 0 || a.Revision != g.Revision || len(g.ProgressUpdates) == 0 || strings.TrimSpace(a.Reason) == "" || len(a.Reason) > 2000 {
		return errors.New("review the exact goal revision, pending update and dismissal reason")
	}
	i := len(g.ProgressUpdates) - 1
	u := g.ProgressUpdates[i]
	if u.ID != a.UpdateID || u.Resolved() || now.Before(u.CreatedAt) {
		return errors.New("progress update is no longer pending")
	}
	u.DismissedAt = now
	u.Resolution = a.Reason
	g.ProgressUpdates[i] = u
	g.ProgressError = ""
	return nil
}
