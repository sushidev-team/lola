package runtime

// context.go holds the two cross-session pieces of SUSHI-624 that live in the
// launcher: the SHARED CONTEXT FOLDER every agent session gets at
// .lola/context, and ForkAgent, which starts a new session from a turn
// checkpoint of another one.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sushidev-team/lola/internal/checkpoint"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// contextLink is the folder's name inside .lola/. The folder itself lives
// OUTSIDE the worktree, under <Home>/context/<project>/<key>/, and is reached
// through a symlink: a worktree is deleted on teardown (and a re-spawn gets a
// new one), the notes must not be. .lola/ is excluded via info/exclude, so the
// link is never committed, never makes a worktree dirty, and `git worktree
// remove` deletes the link, not what it points at.
const contextLink = "context"

// contextListMax bounds how many existing entries the briefing names.
const contextListMax = 20

// ContextKey is the key of s's shared context folder: the recorded one, else
// the lowercased Linear issue (so every re-spawn of an issue — "-r2", "-r3" —
// finds the same notes), else the session ID (a manual or PR session reopened
// on the same branch derives the same ID, and with it the same folder).
func ContextKey(s session.Session) string {
	if s.ContextKey != "" {
		return s.ContextKey
	}
	return contextKeyFor(s.Issue, s.ID)
}

func contextKeyFor(issue, id string) string {
	if issue != "" {
		return strings.ToLower(issue)
	}
	return id
}

// ContextDir is where project's context folder for key lives.
func ContextDir(home, project, key string) string {
	return filepath.Join(home, "context", project, key)
}

// setupContext creates the context folder for key and links it into the
// worktree at dir. It is BEST-EFFORT: notes are a convenience, so a failure is
// logged and the launch goes on without them — the returned path is then ""
// and the briefing says nothing about a folder that is not there.
func (n *Native) setupContext(dir, project, key string) string {
	if n.Home == "" || key == "" {
		return ""
	}
	if err := validContextSegment(project); err != nil {
		n.logContext(dir, err)
		return ""
	}
	if err := validContextSegment(key); err != nil {
		n.logContext(dir, err)
		return ""
	}
	target := ContextDir(n.Home, project, key)
	if err := os.MkdirAll(target, 0o700); err != nil {
		n.logContext(dir, err)
		return ""
	}
	link := filepath.Join(dir, lolaDir, contextLink)
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			// Something real is already there (a revived worktree from before the
			// link existed, or an agent that made the folder itself). Never
			// clobber it: the briefing still points at it.
			return link
		}
		if err := os.Remove(link); err != nil {
			n.logContext(dir, err)
			return ""
		}
	}
	if err := os.Symlink(target, link); err != nil {
		n.logContext(dir, err)
		return ""
	}
	return target
}

func (n *Native) logContext(dir string, err error) {
	if n.Logf != nil {
		n.Logf("context folder for %s skipped: %v", dir, err)
	}
}

func validContextSegment(s string) error {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, `/\`) || strings.ContainsRune(s, 0) {
		return fmt.Errorf("invalid context folder segment %q", s)
	}
	return nil
}

// withContextBriefing appends the context-folder section to a prompt.md body,
// naming what a previous session left there. ctxDir "" (no folder) returns the
// prompt unchanged.
func withContextBriefing(prompt []byte, ctxDir string) []byte {
	if ctxDir == "" {
		return prompt
	}
	var b strings.Builder
	b.Write(prompt)
	if len(prompt) > 0 && prompt[len(prompt)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("\n## Shared context folder\n\n")
	b.WriteString("`" + lolaDir + "/" + contextLink + "/` is a notes folder that OUTLIVES this session: it is kept when the worktree is torn down and handed to the next session that works on the same task (a re-spawn of the issue, or a fork of this session). It is git-ignored — nothing in it is ever committed.\n\n")
	if names := contextEntries(ctxDir); len(names) > 0 {
		b.WriteString("A previous session left these — read them before you start:\n\n")
		for _, name := range names {
			b.WriteString("- `" + lolaDir + "/" + contextLink + "/" + name + "`\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("It is empty: no earlier session has worked on this yet.\n\n")
	}
	b.WriteString("Keep `" + lolaDir + "/" + contextLink + "/notes.md` current as you work: decisions and why, dead ends, what is left, anything the next session would otherwise have to rediscover. Update it before you end a turn that changed your understanding. Never put secrets in it.\n")
	return []byte(b.String())
}

// contextEntries lists the folder's top-level entries for the briefing, capped.
// The names are the agent's own earlier output; they are only listed, never
// executed, and a name carrying a control byte or a backtick is skipped so it
// cannot break out of its code span in the prompt.
func contextEntries(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '`' }) {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > contextListMax {
		names = append(names[:contextListMax], fmt.Sprintf("… and %d more", len(names)-contextListMax))
	}
	return names
}

// Checkpointer is the slice of internal/checkpoint the launcher needs: laying
// a checkpoint into a fork's fresh worktree, and dropping a session's refs.
// checkpoint.Git satisfies it.
type Checkpointer interface {
	Apply(ctx context.Context, dir, tree string) error
	Prune(ctx context.Context, dir, session string) error
}

var _ Checkpointer = checkpoint.Git{}

// ForkSpec describes a fork: the new session's ID and lola-owned branch, the
// checkpoint it starts from (Head, the commit to branch at; Tree, the files to
// lay over it), the parent's context key, the briefing and an optional agent
// override.
type ForkSpec struct {
	SessionID  string
	Branch     string
	Head       string
	Tree       string
	ContextKey string
	Title      string
	Prompt     string
	Agent      string
}

// ForkAgent starts a NEW agent session from a turn checkpoint: a fresh worktree
// on a new branch cut at the checkpoint's HEAD, the checkpoint's files laid over
// it as uncommitted work (exactly as the parent had them), then the usual agent
// launch. The parent is not touched. Session Kind=manual: lola owns the branch.
func (n *Native) ForkAgent(ctx context.Context, p config.Project, f ForkSpec) (session.Session, error) {
	if f.SessionID == "" || f.Branch == "" || f.Head == "" || f.Tree == "" {
		return session.Session{}, errors.New("runtime: fork: session id, branch and checkpoint required")
	}
	dir, err := n.WT.CreateAt(ctx, p, f.SessionID, f.Branch, f.Head)
	if err != nil {
		return session.Session{}, fmt.Errorf("runtime: fork %s: %w", f.SessionID, err)
	}
	if n.Checkpoints == nil {
		return session.Session{}, errors.New("runtime: fork: checkpoints unavailable")
	}
	if err := n.Checkpoints.Apply(ctx, dir, f.Tree); err != nil {
		if rmErr := n.WT.Remove(ctx, p, dir, f.Branch, true); rmErr != nil {
			return session.Session{}, fmt.Errorf("runtime: fork %s: lay down checkpoint: %w (rollback failed: %v; worktree kept at %s)", f.SessionID, err, rmErr, dir)
		}
		return session.Session{}, fmt.Errorf("runtime: fork %s: lay down checkpoint: %w (worktree rolled back)", f.SessionID, err)
	}
	kind := n.resolveKind(p.Name, f.Agent)
	key := f.ContextKey
	if key == "" {
		key = f.SessionID
	}
	// The fork's worktree is dirty by construction (it carries the checkpoint's
	// uncommitted work), so a failed launch keeps it for inspection rather than
	// discarding the copy — finishAgentLaunch's rollback is force=false.
	if err := n.finishAgentLaunch(ctx, p, f.SessionID, dir, f.Branch, kind, true, key, f.Prompt); err != nil {
		return session.Session{}, err
	}
	title := f.Title
	if title == "" {
		title = "fork: " + f.Branch
	}
	return withAgentState(session.Session{
		ID:         f.SessionID,
		Source:     "native",
		Kind:       session.KindManual,
		Project:    p.Name,
		Title:      title,
		Branch:     f.Branch,
		Repo:       p.Repo,
		Worktree:   dir,
		TmuxName:   f.SessionID,
		Agent:      string(kind),
		ContextKey: key,
	}, state.AgentStarting), nil
}

// pruneCheckpoints drops every checkpoint ref recorded under id. Called when a
// session ID is (re)used for a fresh worktree — refs live in the shared ref
// store and outlive a worktree deleted behind lola's back, and a new session
// must not inherit a dead one's history — and on teardown. Best-effort.
func (n *Native) pruneCheckpoints(ctx context.Context, dir, id string) {
	if n.Checkpoints == nil {
		return
	}
	if err := n.Checkpoints.Prune(ctx, dir, id); err != nil && n.Logf != nil {
		n.Logf("session %s: prune checkpoints: %v", id, err)
	}
}
