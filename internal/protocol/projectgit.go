package protocol

// The phone's Git client. Repository identity is always Project (a configured
// [[project]]); Worktree may only name one of that repository's registered
// checkouts. Results are projectgit / gitdiff values, marshalled as they are.
const (
	CmdProjectGit        = "projectGit"        // read: snapshot, diff, stashes
	CmdProjectGitAct     = "projectGitAct"     // control: stage, unstage, commit, fetch, pull, push, createBranch, stashPush, stashApply
	CmdProjectGitDiscard = "projectGitDiscard" // destructive: prepareDiscard, discard, stashDrop
)

// ProjectGitArgs carries every op of the three commands; each op reads only the
// fields it names and the handler refuses an op that belongs to another command.
type ProjectGitArgs struct {
	Op       string   `json:"op"`
	Project  string   `json:"project"`
	Worktree string   `json:"worktree,omitempty"`
	Ref      string   `json:"ref,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Area     string   `json:"area,omitempty"`
	Path     string   `json:"path,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Message  string   `json:"message,omitempty"`
	Head     string   `json:"head,omitempty"` // expected HEAD for stash ops
	SHA      string   `json:"sha,omitempty"`
	Name     string   `json:"name,omitempty"`
	Checkout bool     `json:"checkout,omitempty"`
	Pop      bool     `json:"pop,omitempty"`
	Token    string   `json:"token,omitempty"`
}
