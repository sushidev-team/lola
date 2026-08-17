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
}
