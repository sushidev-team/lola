// Package goal owns durable execution contracts. Linear is a projection of
// these records; a Linear workflow state never proves task verification.
package goal

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

const MarkerPrefix = "<!-- lola-goal:"

func Marked(description string) bool { return strings.Contains(description, MarkerPrefix) }

func Marker(goalID, taskID string) string {
	return MarkerPrefix + goalID + ":" + taskID + " -->"
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failure is unrecoverable, like the stdlib UUID users.
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type Draft struct {
	Project       string      `json:"project"`
	Title         string      `json:"title"`
	Outcome       string      `json:"outcome"`
	Acceptance    []string    `json:"acceptance"`
	NonGoals      []string    `json:"non_goals,omitempty"`
	MilestoneID   string      `json:"milestone_id,omitempty"`
	MilestoneName string      `json:"milestone_name,omitempty"`
	Tasks         []TaskDraft `json:"tasks"`
}

type TaskDraft struct {
	ExistingIssueID string              `json:"existing_issue_id,omitempty"` // explicit human adoption only
	Title           string              `json:"title"`
	Description     string              `json:"description"`
	Acceptance      []string            `json:"acceptance"`
	Apps            []string            `json:"apps,omitempty"`
	LabelIDs        []string            `json:"label_ids,omitempty"`
	BlockedBy       []int               `json:"blocked_by,omitempty"` // 1-based, earlier tasks only
	Paths           map[string][]string `json:"paths,omitempty"`      // absent = policy scope; explicit empty member = read-only
}

type Binding struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	Confirmed  bool   `json:"confirmed"`
}

type Task struct {
	TaskDraft
	ID             string    `json:"id"`
	Issue          Binding   `json:"issue"`
	Relations      []Binding `json:"relations,omitempty"` // parallel to BlockedBy
	DecomposedFrom string    `json:"decomposed_from,omitempty"`
	Adoption       *Adoption `json:"adoption,omitempty"`
}

type Goal struct {
	Workflow        []WorkflowTransition `json:"workflow,omitempty"`
	WorkflowError   string               `json:"workflow_error,omitempty"`
	ProgressUpdates []ProgressUpdate     `json:"progress_updates,omitempty"`
	ProgressError   string               `json:"progress_error,omitempty"`
	BriefSources    []BriefSource        `json:"brief_sources,omitempty"`
	BriefRevisions  []BriefRevision      `json:"brief_revisions,omitempty"`
	PendingBrief    string               `json:"pending_brief,omitempty"`
	ID              string               `json:"id"`
	PlanningID      string               `json:"planning_id,omitempty"` // accepted durable proposal; never an execution authorization
	Project         string               `json:"project"`
	Title           string               `json:"title"`
	Outcome         string               `json:"outcome"`
	Acceptance      []string             `json:"acceptance"`
	NonGoals        []string             `json:"non_goals,omitempty"`
	TeamID          string               `json:"team_id"`
	LinearProjectID string               `json:"linear_project_id"`
	Parent          Binding              `json:"parent"`
	Milestone       Binding              `json:"milestone"`
	MilestoneName   string               `json:"milestone_name,omitempty"`
	Tasks           []Task               `json:"tasks"`
	State           string               `json:"state"` // draft, provisioning, planned; no implicit execution
	SyncError       string               `json:"sync_error,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	Revision        uint64               `json:"revision"`
	Execution       *Execution           `json:"execution,omitempty"`
}

func ValidateDraft(d Draft) error {
	if strings.TrimSpace(d.Project) == "" || strings.TrimSpace(d.Title) == "" || strings.TrimSpace(d.Outcome) == "" {
		return fmt.Errorf("goal requires project, title, and outcome")
	}
	if len(d.Title) > 240 || len(d.Outcome) > 16000 || len(d.Tasks) < 1 || len(d.Tasks) > 50 {
		return fmt.Errorf("goal limits: title 240 bytes, outcome 16000 bytes, 1–50 tasks")
	}
	if d.MilestoneID != "" && d.MilestoneName != "" {
		return fmt.Errorf("choose an existing milestone_id or a new milestone_name")
	}
	if len(d.MilestoneName) > 240 || Marked(d.Outcome) {
		return fmt.Errorf("invalid milestone name or reserved goal marker in outcome")
	}
	if err := criteria(d.Acceptance); err != nil {
		return err
	}
	for i, t := range d.Tasks {
		if t.ExistingIssueID != "" && !issueUUID.MatchString(t.ExistingIssueID) {
			return fmt.Errorf("task %d existing_issue_id must be a Linear UUID", i+1)
		}
		if strings.TrimSpace(t.Title) == "" || len(t.Title) > 240 || len(t.Description) > 16000 || Marked(t.Description) {
			return fmt.Errorf("task %d has invalid title or description", i+1)
		}
		if err := criteria(t.Acceptance); err != nil {
			return fmt.Errorf("task %d: %w", i+1, err)
		}
		if err := validateTaskPaths(d.Project, t); err != nil {
			return fmt.Errorf("task %d: %w", i+1, err)
		}
		seen := map[int]bool{}
		for _, dep := range t.BlockedBy {
			if dep < 1 || dep > i || seen[dep] {
				return fmt.Errorf("task %d dependencies must be distinct earlier task numbers", i+1)
			}
			seen[dep] = true
		}
	}
	return nil
}

func criteria(a []string) error {
	if len(a) == 0 || len(a) > 50 {
		return fmt.Errorf("1–50 observable acceptance criteria required")
	}
	for _, s := range a {
		if strings.TrimSpace(s) == "" || len(s) > 2000 || Marked(s) {
			return fmt.Errorf("acceptance criteria must be nonempty, at most 2000 bytes, without reserved markers")
		}
	}
	return nil
}

// New reserves every external create identity before any network call.
func New(d Draft, teamID, linearProjectID string) (Goal, error) {
	if err := ValidateDraft(d); err != nil {
		return Goal{}, err
	}
	if teamID == "" || linearProjectID == "" {
		return Goal{}, fmt.Errorf("goal requires an existing Linear team and project")
	}
	g := Goal{ID: NewID(), Project: d.Project, Title: d.Title, Outcome: d.Outcome,
		Acceptance: d.Acceptance, NonGoals: d.NonGoals, TeamID: teamID, LinearProjectID: linearProjectID,
		Parent: Binding{ID: NewID()}, MilestoneName: d.MilestoneName,
		State: "draft", CreatedAt: time.Now().UTC()}
	if d.MilestoneID != "" {
		g.Milestone = Binding{ID: d.MilestoneID, Confirmed: true}
	} else if d.MilestoneName != "" {
		g.Milestone.ID = NewID()
	}
	for _, t := range d.Tasks {
		task := Task{TaskDraft: t, ID: NewID(), Issue: Binding{ID: NewID()}}
		if t.ExistingIssueID != "" {
			task.Issue.ID = t.ExistingIssueID
			task.Adoption = &Adoption{Membership: Binding{ID: NewID()}}
		}
		for range t.BlockedBy {
			task.Relations = append(task.Relations, Binding{ID: NewID()})
		}
		g.Tasks = append(g.Tasks, task)
	}
	return g, nil
}

func (g Goal) ParentDescription() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%s\n\n## Acceptance\n\n", Marker(g.ID, "parent"), g.Outcome)
	for _, s := range g.Acceptance {
		fmt.Fprintf(&b, "* %s\n", s)
	}
	if len(g.NonGoals) > 0 {
		b.WriteString("\n## Non-goals\n\n")
		for _, s := range g.NonGoals {
			fmt.Fprintf(&b, "* %s\n", s)
		}
	}
	for _, task := range g.Tasks {
		if task.Adoption == nil {
			continue
		}
		fmt.Fprintf(&b, "\n## Adopted task: %s\n\n[Existing issue](https://linear.app/issue/%s). Its existing parent and release milestone are preserved.\n\nAccepted criteria for this goal:\n\n", task.Title, task.Issue.ID)
		for _, criterion := range task.Acceptance {
			fmt.Fprintf(&b, "* %s\n", criterion)
		}
	}
	b.WriteString("\nManaged by Lola. Publishing this plan does not authorize execution. Task completion is tracked by verified goal integration; final delivery requires human merge.\n")
	return strings.TrimSuffix(b.String(), "\n")
}

func (g Goal) TaskDescription(t Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%s\n\n## Acceptance\n\n", Marker(g.ID, t.ID), t.Description)
	for _, s := range t.Acceptance {
		fmt.Fprintf(&b, "* %s\n", s)
	}
	if t.DecomposedFrom != "" {
		for _, parent := range g.Tasks {
			if parent.ID == t.DecomposedFrom {
				fmt.Fprintf(&b, "\nReplacement work for [original task](https://linear.app/issue/%s). Its original acceptance remains required.\n", parent.Issue.ID)
				break
			}
		}
	}
	if len(t.Paths) > 0 {
		b.WriteString("\n## Repository ownership\n\n")
		var members []string
		for member := range t.Paths {
			members = append(members, member)
		}
		sort.Strings(members)
		for _, member := range members {
			paths := t.Paths[member]
			if len(paths) == 0 {
				fmt.Fprintf(&b, "* %s: read-only context\n", member)
			} else {
				quoted := make([]string, len(paths))
				for i, path := range paths {
					quoted[i] = markdownCode(path)
				}
				fmt.Fprintf(&b, "* %s: %s\n", member, strings.Join(quoted, ", "))
			}
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func validateTaskPaths(project string, t TaskDraft) error {
	if len(t.Paths) == 0 {
		return nil
	}
	members := t.Apps
	if len(members) == 0 {
		members = []string{project}
	}
	if len(t.Paths) != len(members) {
		return fmt.Errorf("explicit ownership must cover every routed repository")
	}
	for _, id := range members {
		paths, ok := t.Paths[id]
		if !ok {
			return fmt.Errorf("ownership missing for repository %s", id)
		}
		if err := goalpolicy.ValidatePaths(paths); err != nil {
			return err
		}
	}
	return nil
}

// Protect repository path punctuation from Linear Markdown normalization.
func markdownCode(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}
