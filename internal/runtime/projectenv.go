package runtime

import (
	"maps"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
)

// EnvVars are the per-session identifiers a [[project]].env value may reference.
// Every field is known by the time a worktree exists, except Issue, which only
// a Linear spawn has.
type EnvVars struct {
	Session  string // tmux session id, == the worktree directory name
	Issue    string // Linear identifier (NOR-366); empty for PR/manual sessions
	Branch   string // the branch checked out in the worktree
	Project  string // [[project]].name
	Worktree string // absolute worktree path
}

// expandProjectEnv returns p with its Env values rendered against v.
//
// Placeholders use the same {{.Field}} spelling as reaction and write-back
// templates, and — for the same reason — the same plain simultaneous
// strings.Replacer rather than text/template: values reach a sourced shell
// file, so a template engine here would be an eval surface for anything that
// ever ends up in a substituted field. Only VALUES are rendered; names are
// validated shell identifiers and are never touched.
//
// The point is per-worktree distinctness. Several worktrees of one repo
// commonly share a .env (lola symlinks it), so they also share every backing
// service named in it — the same Redis queue, the same cache prefix. Two dev
// stacks then consume each other's jobs, which reads as work vanishing rather
// than as a collision. A value keyed on {{.Session}} gives each worktree its
// own namespace without a per-machine shell hack:
//
//	[project.env]
//	REDIS_QUEUE = "{{{.Session}}}"
//
// Sessions with no Linear issue render {{.Issue}} as empty, so prefer
// {{.Session}} when the value has to be unique for every session. p is a value
// and the returned copy owns a fresh map, so the caller's Env is untouched.
func expandProjectEnv(p config.Project, v EnvVars) config.Project {
	if len(p.Env) == 0 {
		return p
	}

	r := strings.NewReplacer(
		"{{.Session}}", v.Session,
		"{{.Issue}}", v.Issue,
		"{{.Branch}}", v.Branch,
		"{{.Project}}", v.Project,
		"{{.Worktree}}", v.Worktree,
	)

	out := make(map[string]string, len(p.Env))
	for k, val := range maps.All(p.Env) {
		out[k] = r.Replace(val)
	}
	p.Env = out

	return p
}
