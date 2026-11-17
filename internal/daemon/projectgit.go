package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/projectgit"
	"github.com/sushidev-team/lola/internal/protocol"
)

// The phone's Git client. A phone holds no git credentials: every operation
// runs here, with this machine's own, on a checkout the project's repository
// registers. The desktop app has its own in-process twin (desktop/
// projectgitsvc.go); the isolation rules below mirror it and must change with it.
//
//   - Repository identity comes from config, never from the request. A phone may
//     name a worktree only if `git worktree list` of the project's folder does.
//   - Sandboxed, pinned, goal and unknown managed checkouts are refused: those
//     are managed through their session.
//   - Writes are serialized; a second one is refused rather than queued.

const (
	projectGitReadTimeout = 30 * time.Second
	projectGitMaxPaths    = 2000
	projectGitMaxMessage  = 64 << 10
	projectGitMaxLimit    = 300
	// projectGitDiffBudget bounds an encoded diff reply well under
	// protocol.MaxFrameBytes (1 MiB): an oversize reply is a fatal frame error
	// that drops the whole phone connection, so it must never be sent.
	projectGitDiffBudget = 800 << 10
)

// fitDiffToFrame omits patches, last file first, until the encoded result fits
// one frame. An omitted patch is marked TooLarge (the viewer says so) and the
// result Truncated; file names and counts always survive.
func fitDiffToFrame(res gitdiff.Result) gitdiff.Result {
	size := func() int {
		b, err := json.Marshal(res)
		if err != nil {
			return projectGitDiffBudget + 1
		}
		return len(b)
	}
	for i := len(res.Files) - 1; i >= 0 && size() > projectGitDiffBudget; i-- {
		if res.Files[i].Patch == "" {
			continue
		}
		res.Files[i].Patch, res.Files[i].TooLarge, res.Truncated = "", true, true
	}
	return res
}

func gitWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func gitCanonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// projectGitBlocked reports whether path is an isolated or managed-by-session
// checkout, from the live session table and the gitguard registry.
func (d *Daemon) projectGitBlocked(path string) bool {
	path = gitCanonical(path)
	home := gitCanonical(d.home)
	if gitguard.Pinned(path) || gitWithin(path, filepath.Join(home, "goals")) {
		return true
	}
	knownHost := false
	for _, s := range d.sessions.Snapshot() {
		if s.Worktree == "" {
			continue
		}
		root := gitCanonical(s.Worktree)
		if !gitWithin(path, root) {
			continue
		}
		if s.Sandbox != "" && s.Sandbox != "none" {
			return true
		}
		knownHost = true
	}
	// A forgotten lola checkout is not proof it was never sandboxed.
	return gitWithin(path, filepath.Join(home, "worktrees")) && !knownHost
}

func (d *Daemon) projectGitTarget(ctx context.Context, project, requested string) (string, error) {
	d.mu.Lock()
	var p *config.Project
	if d.cfg != nil {
		if found := d.cfg.ProjectByName(project); found != nil {
			c := *found
			p = &c
		}
	}
	d.mu.Unlock()
	if p == nil {
		return "", errors.New("project no longer exists")
	}
	if p.IsStack() {
		return "", errors.New("choose a repository inside this stack")
	}
	if p.Path == "" {
		return "", errors.New("configure this project's repository folder first")
	}
	root := gitCanonical(p.Path)
	if d.projectGitBlocked(root) {
		return "", errors.New("sandbox worktrees must be managed through their session")
	}
	trees, err := (projectgit.Client{}).Worktrees(ctx, root)
	if err != nil {
		return "", err
	}
	wanted := root
	if requested != "" {
		if !filepath.IsAbs(requested) {
			return "", errors.New("invalid worktree path")
		}
		wanted = gitCanonical(requested)
	}
	for _, tree := range trees {
		if gitCanonical(tree.Path) != wanted {
			continue
		}
		if tree.Prunable {
			return "", errors.New("this worktree is no longer available")
		}
		if d.projectGitBlocked(wanted) {
			return "", errors.New("sandbox worktrees must be managed through their session")
		}
		return wanted, nil
	}
	return "", errors.New("worktree does not belong to this project; refresh the repository")
}

// projectGitOps names the ops each command may run.
var projectGitOps = map[string]map[string]bool{
	protocol.CmdProjectGit: {"snapshot": true, "diff": true, "stashes": true},
	protocol.CmdProjectGitAct: {"stage": true, "unstage": true, "commit": true, "fetch": true, "pull": true,
		"push": true, "createBranch": true, "stashPush": true, "stashApply": true},
	protocol.CmdProjectGitDiscard: {"prepareDiscard": true, "discard": true, "stashDrop": true},
}

func (d *Daemon) handleProjectGit(ctx context.Context, req protocol.Request) protocol.Response {
	var a protocol.ProjectGitArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return protocol.Response{Error: "malformed arguments"}
	}
	if !projectGitOps[req.Cmd][a.Op] {
		return protocol.Response{Error: fmt.Sprintf("unknown repository op %q", a.Op)}
	}
	if len(a.Paths) > projectGitMaxPaths || len(a.Message) > projectGitMaxMessage {
		return protocol.Response{Error: "request too large"}
	}
	var (
		data any
		err  error
	)
	switch req.Cmd {
	case protocol.CmdProjectGit:
		data, err = d.projectGitRead(ctx, a)
	default:
		data, err = d.projectGitWrite(a)
	}
	if err != nil {
		return protocol.Response{Error: err.Error()}
	}
	return dataResponse(data)
}

func (d *Daemon) projectGitRead(ctx context.Context, a protocol.ProjectGitArgs) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, projectGitReadTimeout)
	defer cancel()
	dir, err := d.projectGitTarget(ctx, a.Project, a.Worktree)
	if err != nil {
		return nil, err
	}
	c := projectgit.Client{}
	switch a.Op {
	case "snapshot":
		limit := min(max(a.Limit, 1), projectGitMaxLimit)
		snap, err := c.Snapshot(ctx, dir, a.Ref, limit)
		if err != nil {
			return nil, err
		}
		for i := range snap.Worktrees {
			snap.Worktrees[i].Blocked = d.projectGitBlocked(snap.Worktrees[i].Path)
		}
		return snap, nil
	case "diff":
		res, err := c.Diff(ctx, dir, a.From, a.To, a.Area, a.Path)
		if err != nil {
			return nil, err
		}
		return fitDiffToFrame(res), nil
	default:
		return c.Stashes(ctx, dir)
	}
}

// projectGitWrite runs one serialized write (act or discard). A refused lock is
// an error, not a queue: a phone's second tap must not stack commits.
func (d *Daemon) projectGitWrite(a protocol.ProjectGitArgs) (any, error) {
	lock := &d.projectGitMu
	if a.Op == "fetch" {
		lock = &d.projectGitFetchMu
	}
	if !lock.TryLock() {
		return nil, errors.New("another repository action is running; wait for it to finish")
	}
	defer lock.Unlock()
	ctx, cancel := projectgit.Context()
	defer cancel()
	dir, err := d.projectGitTarget(ctx, a.Project, a.Worktree)
	if err != nil {
		return nil, err
	}
	c := projectgit.Client{}
	fail := func(err error) (any, error) {
		if err == nil {
			return struct{}{}, nil
		}
		return nil, fmt.Errorf("repository action failed: %w", err)
	}
	switch a.Op {
	case "stage", "unstage":
		files, err := c.ChangeIndexAndStatus(ctx, dir, a.Paths, a.Op == "stage")
		if err != nil {
			return fail(err)
		}
		return files, nil
	case "commit":
		return fail(c.Commit(ctx, dir, a.Message))
	case "fetch", "pull", "push":
		return fail(c.Sync(ctx, dir, a.Op))
	case "createBranch":
		if err := c.CreateBranch(ctx, dir, a.Name, a.SHA, a.Checkout); err != nil {
			return fail(err)
		}
		return projectgit.BranchResult{Path: dir}, nil
	case "stashPush":
		return fail(c.StashPush(ctx, dir, a.Message, a.Head))
	case "stashApply":
		return fail(c.StashApply(ctx, dir, a.SHA, a.Head, a.Pop))
	case "prepareDiscard":
		plan, err := c.PrepareDiscard(ctx, dir, a.Paths...)
		if err != nil {
			return fail(err)
		}
		return plan, nil
	case "discard":
		return fail(c.DiscardUnstaged(ctx, dir, a.Token, a.Paths...))
	default: // stashDrop
		return fail(c.StashDrop(ctx, dir, a.SHA))
	}
}
