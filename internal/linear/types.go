package linear

type Team struct{ ID, Key, Name string }
type Project struct{ ID, Name, State string }
type Cycle struct {
	ID     string
	Number int
	Name   string
}
type State struct {
	ID, Name, Type string
	Position       float64
}

// Ref is a label or workflow state as FilterRefs reports it: its name and the
// team that owns it ("" for a workspace label, which every team can use).
type Ref struct {
	ID, Name, TeamID string
}

type Label struct {
	ID, Name, Color string
	Parent          *Label
}
type User struct {
	ID, Name, Email string
	Active          bool
}
type Issue struct {
	ID         string // UUID -> used for issueUpdate
	Identifier string // e.g. FE-231 -> used for `ao spawn`
	Title      string
	BranchName string
	Priority   float64
	CreatedAt  string
	UpdatedAt  string
	// Workflow state as Linear reports it: StateName is the team's own label
	// ("In Progress", "Ready for QA"), StateType the stable enum behind it
	// (triage|backlog|unstarted|started|completed|canceled). Dispatch filters on
	// state IDs and never reads these; they exist for the pickers, which show a
	// human what an issue currently is.
	StateName  string
	StateType  string
	Estimate   float64
	Assignee   string
	LabelIDs   []string
	LabelNames []string // parallel to LabelIDs, display only
	// BlockedBy lists the issues Linear records as BLOCKING this one (the
	// inverse side of a "blocks" relation), each with its current state type.
	// OpenChildren counts sub-issues not yet completed or canceled. Dispatch
	// reads both: an issue whose blockers are unfinished, or whose work has
	// been decomposed into open sub-issues, is not eligible yet.
	BlockedBy    []Blocker
	OpenChildren int
}

// Blocker is one issue blocking another, as dispatch needs it: who it is and
// whether its workflow state is finished (StateType completed|canceled).
type Blocker struct {
	ID, Identifier, StateType string
}

// Finished reports whether the blocker's workflow state no longer holds the
// blocked issue back: done, or canceled (a canceled dependency will never
// land, so waiting on it would park the dependent forever).
func (b Blocker) Finished() bool {
	return StateFinished(b.StateType)
}

// StateFinished reports whether a workflow state TYPE is terminal.
func StateFinished(stateType string) bool {
	return stateType == "completed" || stateType == "canceled"
}

// IssueDetail is one issue read in full for the planning pass: its text (the
// planner's input) and the placement fields its sub-issues inherit.
type IssueDetail struct {
	ID, Identifier, Title, Description string
	TeamID, ProjectID, CycleID         string
	StateID, AssigneeID                string
	LabelIDs                           []string
	// Children counts ALL existing sub-issues, finished or not: applying a plan
	// to an issue that already has some would duplicate the decomposition.
	Children int
}

// IssueCreate is the input for CreateIssue. Empty fields are omitted, so
// Linear applies the team's defaults (e.g. the initial workflow state).
type IssueCreate struct {
	TeamID, ProjectID, CycleID, StateID, AssigneeID, ParentID string
	Title, Description                                        string
	LabelIDs                                                  []string
}
