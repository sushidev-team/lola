package runtime

import (
	"crypto/sha1"
	"encoding/hex"
	"maps"
	"strconv"
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
	// Ports is the session's leased port block (internal/portlease); empty
	// when the project configures no port_range or the range is exhausted.
	Ports []int
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

	pairs := []string{
		"{{.Session}}", v.Session,
		"{{.Slug}}", v.Session,
		"{{.DBName}}", DBName(v.Session),
		"{{.Issue}}", v.Issue,
		"{{.Branch}}", v.Branch,
		"{{.Project}}", v.Project,
		"{{.Worktree}}", v.Worktree,
		"{{.Port}}", portAt(v.Ports, 0),
	}
	// {{.Port1}}..{{.PortN}}: each placeholder is a complete token including
	// its closing braces, so {{.Port1}} can never match inside {{.Port10}}.
	for i := range max(len(v.Ports), config.MaxPortsPerSession) {
		pairs = append(pairs, "{{.Port"+strconv.Itoa(i+1)+"}}", portAt(v.Ports, i))
	}
	r := strings.NewReplacer(pairs...)

	out := make(map[string]string, len(p.Env))
	for k, val := range maps.All(p.Env) {
		out[k] = r.Replace(val)
	}
	p.Env = out

	return p
}

// portAt is ports[i] as a string, "" past the end (no lease, or a smaller one).
func portAt(ports []int, i int) string {
	if i < len(ports) {
		return strconv.Itoa(ports[i])
	}
	return ""
}

// withSessionEnv is expandProjectEnv plus lola's OWN per-session variables,
// which every session gets whatever its [project.env] says:
//
//	LOLA_SESSION_SLUG  the session ID (a hostname/path-safe slug)
//	LOLA_DB_NAME       DBName(session) — a stable database name post_create can
//	                   create and pre_remove can drop
//	LOLA_PORT          the first leased port      (only with a lease)
//	LOLA_PORT_1..N     every leased port, 1-based (only with a lease)
//
// They are added to p.Env, so they travel every road [[project]].env already
// travels — post_create, the .lola/env file the agent pane, the shell tabs and
// the dev tabs source, and the pre_remove hook — with no second plumbing path
// to drift. lola's values WIN over a same-named [project.env] key: a project
// that pins LOLA_PORT would hand every worktree the same port, which is the
// collision these exist to prevent.
func withSessionEnv(p config.Project, v EnvVars) config.Project {
	p = expandProjectEnv(p, v)
	out := make(map[string]string, len(p.Env)+2+len(v.Ports))
	maps.Copy(out, p.Env)
	if v.Session != "" {
		out["LOLA_SESSION_SLUG"] = v.Session
		out["LOLA_DB_NAME"] = DBName(v.Session)
	}
	if len(v.Ports) > 0 {
		out["LOLA_PORT"] = strconv.Itoa(v.Ports[0])
		for i, port := range v.Ports {
			out["LOLA_PORT_"+strconv.Itoa(i+1)] = strconv.Itoa(port)
		}
	}
	p.Env = out
	return p
}

// dbNameMax is the longest identifier every common database accepts
// (PostgreSQL's NAMEDATALEN-1 is the tightest; MySQL allows 64).
const dbNameMax = 63

// DBName derives the session's stable database name: the session ID
// lowercased, every character outside [a-z0-9] folded to "_" — so it is a
// valid unquoted identifier in PostgreSQL, MySQL and SQLite alike — and capped
// at 63 bytes. An over-long ID keeps a recognizable prefix plus a short hash of
// the whole ID, so two long IDs sharing a prefix never collide. Deterministic:
// the same session always gets the same name, which is what lets pre_remove
// drop exactly what post_create created, even across a daemon restart.
func DBName(session string) string {
	if session == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(session) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if name[0] >= '0' && name[0] <= '9' {
		name = "lola_" + name
	}
	if len(name) > dbNameMax {
		sum := sha1.Sum([]byte(session))
		name = name[:dbNameMax-9] + "_" + hex.EncodeToString(sum[:])[:8]
	}
	return name
}
