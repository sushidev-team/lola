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
	LabelIDs   []string
}
