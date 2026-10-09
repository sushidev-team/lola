// Package checkpoint records a session's worktree once per agent turn so a bad
// turn can be rolled back, inspected or forked from without a git command
// (SUSHI-624).
//
// A checkpoint is a COMMIT OBJECT that no branch points at: its tree is the
// whole working tree at that moment — committed, staged, unstaged and untracked
// (non-ignored) files alike — and its one parent is the HEAD the session was on.
// It is kept alive by a ref under refs/lola/checkpoints/<session>/<seq>, never
// by the session's branch, so the branch history and the PR are untouched and a
// push never carries one. Refs live in the repository's COMMON ref store, which
// is why every call can be pointed at any worktree of the project (or its main
// checkout) and why the session ID is part of the ref name.
//
// Why a temporary index rather than `git stash create`: stash ignores untracked
// files, and a turn's new files are exactly what a rollback has to remove. The
// temp index is SEEDED with a copy of the real one, so `git add -A` re-hashes
// only what changed — and the real index (which the agent may be using right
// now) is never written. .lola/ is excluded via info/exclude, so the scratch
// folder and the shared context folder are neither captured nor rolled back.
//
// A stdlib leaf: it shells to git only (local, no network), behind an exec
// seam, and knows nothing about sessions or config.
package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RefPrefix is the ref namespace every checkpoint lives under.
const RefPrefix = "refs/lola/checkpoints/"

// MaxPerSession bounds how many checkpoints one session keeps; recording past
// it deletes the oldest. Each ref pins a full snapshot's objects against gc, so
// an agent that runs hundreds of turns must not pin hundreds of trees forever.
const MaxPerSession = 100

// subjectPrefix opens every checkpoint commit's message; the rest is the label.
const subjectPrefix = "lola checkpoint: "

// Checkpoint is one recorded snapshot.
type Checkpoint struct {
	Seq     int       // 1-based, monotonic per session; the ref's last segment
	SHA     string    // the snapshot commit
	Tree    string    // its tree — two checkpoints with equal Tree+Head are the same state
	Head    string    // its parent: the HEAD the session was on when it was taken
	Label   string    // e.g. "turn 3", "before restore to #2"
	Created time.Time // commit time
}

// Git is the checkpoint store over one git binary. The zero value is usable.
type Git struct {
	Bin string // "" resolves "git"

	// run is the exec seam; nil uses runGit. env is appended to the process
	// environment.
	run func(ctx context.Context, bin, dir string, env []string, args ...string) ([]byte, error)
}

// ErrNotFound is returned for a sequence number the session has no checkpoint for.
var ErrNotFound = errors.New("no such checkpoint")

// ErrWouldClobber is returned when a restore would overwrite files no
// checkpoint holds (ignored files), so it could not be undone.
var ErrWouldClobber = errors.New("restore would overwrite ignored files that no checkpoint can bring back")

// identity pins the snapshot commit's author/committer: a machine without
// user.name/user.email configured would otherwise fail commit-tree, and the
// checkpoint is lola's, not the human's.
var identity = []string{
	"GIT_AUTHOR_NAME=lola", "GIT_AUTHOR_EMAIL=lola@localhost",
	"GIT_COMMITTER_NAME=lola", "GIT_COMMITTER_EMAIL=lola@localhost",
}

// sessionRe is what a session ID may look like to become a ref segment: the
// characters lola's own IDs use, never a leading dot and never a ".lock" or
// ".." that git refuses (or that would climb out of the namespace).
var sessionRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

func validSession(s string) error {
	if !sessionRe.MatchString(s) || strings.Contains(s, "..") || strings.HasSuffix(s, ".lock") {
		return fmt.Errorf("checkpoint: invalid session id %q", s)
	}
	return nil
}

// RefName is the ref holding session's checkpoint seq. Zero-padded so a plain
// sorted listing is in recording order.
func RefName(session string, seq int) string {
	return fmt.Sprintf("%s%s/%06d", RefPrefix, session, seq)
}

func (g Git) exec(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	run := g.run
	if run == nil {
		run = runGit
	}
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	out, err := run(ctx, bin, dir, env, args...)
	return strings.TrimSpace(string(out)), err
}

// Snapshot writes the working tree at dir into the object store and returns its
// tree and the HEAD it sits on, without touching the real index or any ref.
func (g Git) Snapshot(ctx context.Context, dir string) (tree, head string, err error) {
	head, err = g.exec(ctx, dir, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("checkpoint: resolve HEAD: %w", err)
	}
	idx, err := g.exec(ctx, dir, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", "", fmt.Errorf("checkpoint: locate index: %w", err)
	}
	if !filepath.IsAbs(idx) {
		idx = filepath.Join(dir, idx)
	}
	tmp, err := os.CreateTemp("", "lola-checkpoint-*.index")
	if err != nil {
		return "", "", fmt.Errorf("checkpoint: temp index: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	seeded := false
	if src, err := os.Open(idx); err == nil {
		_, cerr := io.Copy(tmp, src)
		src.Close()
		seeded = cerr == nil
	}
	tmp.Close()
	if !seeded {
		// An empty file is not a valid index; a missing one is an empty index.
		os.Remove(tmpPath)
	}
	env := []string{"GIT_INDEX_FILE=" + tmpPath}
	if _, err := g.exec(ctx, dir, env, "add", "-A", "--", "."); err != nil {
		return "", "", fmt.Errorf("checkpoint: stage snapshot: %w", err)
	}
	tree, err = g.exec(ctx, dir, env, "write-tree")
	if err != nil {
		return "", "", fmt.Errorf("checkpoint: write tree: %w", err)
	}
	return tree, head, nil
}

// Record snapshots the worktree at dir as session's next checkpoint, labelled
// label. When the state is identical to the latest checkpoint (same tree, same
// HEAD) nothing is written and that checkpoint is returned with recorded=false:
// a turn that only talked costs no ref.
func (g Git) Record(ctx context.Context, dir, session, label string) (cp Checkpoint, recorded bool, err error) {
	if err := validSession(session); err != nil {
		return Checkpoint{}, false, err
	}
	tree, head, err := g.Snapshot(ctx, dir)
	if err != nil {
		return Checkpoint{}, false, err
	}
	list, err := g.List(ctx, dir, session)
	if err != nil {
		return Checkpoint{}, false, err
	}
	seq := 1
	if n := len(list); n > 0 {
		last := list[n-1]
		if last.Tree == tree && last.Head == head {
			return last, false, nil
		}
		seq = last.Seq + 1
	}
	label = cleanLabel(label)
	sha, err := g.exec(ctx, dir, identity, "-c", "commit.gpgSign=false",
		"commit-tree", "--no-gpg-sign", tree, "-p", head, "-m", subjectPrefix+label)
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("checkpoint: commit snapshot: %w", err)
	}
	// The empty old value makes the update fail if the ref already exists, so
	// two recorders that raced to the same seq cannot overwrite each other.
	if _, err := g.exec(ctx, dir, nil, "update-ref", RefName(session, seq), sha, ""); err != nil {
		return Checkpoint{}, false, fmt.Errorf("checkpoint: write ref: %w", err)
	}
	for i := 0; i <= len(list)-MaxPerSession; i++ {
		_, _ = g.exec(ctx, dir, nil, "update-ref", "-d", RefName(session, list[i].Seq))
	}
	return Checkpoint{Seq: seq, SHA: sha, Tree: tree, Head: head, Label: label, Created: time.Now()}, true, nil
}

// List returns session's checkpoints, oldest first.
func (g Git) List(ctx context.Context, dir, session string) ([]Checkpoint, error) {
	if err := validSession(session); err != nil {
		return nil, err
	}
	prefix := RefPrefix + session + "/"
	out, err := g.exec(ctx, dir, nil, "for-each-ref",
		"--format=%(refname)%00%(objectname)%00%(tree)%00%(parent)%00%(creatordate:unix)%00%(contents:subject)",
		prefix)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: list: %w", err)
	}
	var list []Checkpoint
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 6 {
			continue
		}
		seq, err := strconv.Atoi(strings.TrimPrefix(f[0], prefix))
		if err != nil || seq < 1 {
			continue
		}
		head, _, _ := strings.Cut(f[3], " ")
		unix, _ := strconv.ParseInt(f[4], 10, 64)
		list = append(list, Checkpoint{
			Seq:     seq,
			SHA:     f[1],
			Tree:    f[2],
			Head:    head,
			Label:   strings.TrimPrefix(f[5], subjectPrefix),
			Created: time.Unix(unix, 0),
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Seq < list[j].Seq })
	return list, nil
}

// Get returns one checkpoint, and the one recorded before it (zero when seq is
// the oldest kept).
func (g Git) Get(ctx context.Context, dir, session string, seq int) (cp, prev Checkpoint, err error) {
	list, err := g.List(ctx, dir, session)
	if err != nil {
		return Checkpoint{}, Checkpoint{}, err
	}
	for i, c := range list {
		if c.Seq == seq {
			if i > 0 {
				prev = list[i-1]
			}
			return c, prev, nil
		}
	}
	return Checkpoint{}, Checkpoint{}, fmt.Errorf("%w #%d", ErrNotFound, seq)
}

// Restore makes the worktree at dir match session's checkpoint seq. It first
// records the CURRENT state (labelled "before restore to #<seq>") so the restore
// is itself undoable, and returns that safety checkpoint.
//
// Only FILES move: HEAD and the branch stay where they are, so commits made
// after the checkpoint are not rewritten — their changes show up as uncommitted
// edits that undo them, for the agent or a human to commit. Files the
// checkpoint did not have are deleted; ignored files and .lola/ are untouched.
func (g Git) Restore(ctx context.Context, dir, session string, seq int) (safety Checkpoint, err error) {
	target, _, err := g.Get(ctx, dir, session, seq)
	if err != nil {
		return Checkpoint{}, err
	}
	safety, _, err = g.Record(ctx, dir, session, fmt.Sprintf("before restore to #%d", seq))
	if err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint: save current state first: %w", err)
	}
	// A snapshot never holds IGNORED files (`add -A` skips them), so a target
	// path that exists on disk now but is not in the safety snapshot is a file
	// the reset would overwrite with no way back. Refuse before anything moves.
	if clobbered, err := g.uncaptured(ctx, dir, safety.Tree, target.Tree); err != nil {
		return safety, fmt.Errorf("checkpoint: restore #%d: %w", seq, err)
	} else if len(clobbered) > 0 {
		shown := clobbered
		if len(shown) > 5 {
			shown = append(shown[:5:5], fmt.Sprintf("… and %d more", len(clobbered)-5))
		}
		return safety, fmt.Errorf("%w: %s — move them out of the way first",
			ErrWouldClobber, strings.Join(shown, ", "))
	}
	// Point the real index at the current snapshot, so the reset below knows
	// every file that exists now — untracked ones included — and removes the
	// ones the target lacks.
	if _, err := g.exec(ctx, dir, nil, "read-tree", safety.Tree); err != nil {
		return safety, fmt.Errorf("checkpoint: restore #%d: %w", seq, err)
	}
	if err := g.Apply(ctx, dir, target.Tree); err != nil {
		return safety, fmt.Errorf("checkpoint: restore #%d (the previous state is checkpoint #%d): %w", seq, safety.Seq, err)
	}
	return safety, nil
}

// uncaptured lists the paths the target tree ADDS relative to the current
// snapshot that nonetheless exist on disk now — or whose parent exists as a
// non-directory — i.e. ignored files and directories a restore would overwrite
// or remove although no checkpoint holds them.
func (g Git) uncaptured(ctx context.Context, dir, current, target string) ([]string, error) {
	out, err := g.exec(ctx, dir, nil, "diff", "--name-only", "-z", "--no-renames", "--diff-filter=A", current, target, "--")
	if err != nil {
		return nil, err
	}
	var hit []string
	for p := range strings.SplitSeq(out, "\x00") {
		if p == "" {
			continue
		}
		if blocked(dir, p) {
			hit = append(hit, p)
		}
	}
	return hit, nil
}

// blocked reports whether writing the file p (slash-separated, relative to dir)
// would destroy something on disk: p itself exists, or one of its parents is a
// non-directory.
func blocked(dir, p string) bool {
	parts := strings.Split(p, "/")
	cur := dir
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return false
		}
		if i == len(parts)-1 || !fi.IsDir() {
			return true
		}
	}
	return false
}

// Apply rewrites the worktree at dir to tree, deleting files the current index
// has and tree does not, then resets the index to HEAD so the result reads as
// ordinary uncommitted changes (a file new since HEAD is untracked again).
func (g Git) Apply(ctx context.Context, dir, tree string) error {
	if tree == "" || strings.HasPrefix(tree, "-") {
		return fmt.Errorf("checkpoint: invalid tree %q", tree)
	}
	if _, err := g.exec(ctx, dir, nil, "read-tree", "--reset", "-u", tree); err != nil {
		return err
	}
	if _, err := g.exec(ctx, dir, nil, "reset", "-q"); err != nil {
		return err
	}
	return nil
}

// Prune deletes every checkpoint ref of session. Best-effort per ref: it
// returns the first error but keeps going.
func (g Git) Prune(ctx context.Context, dir, session string) error {
	list, err := g.List(ctx, dir, session)
	if err != nil {
		return err
	}
	var first error
	for _, c := range list {
		if _, err := g.exec(ctx, dir, nil, "update-ref", "-d", RefName(session, c.Seq)); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// cleanLabel keeps a label to one short printable line: it becomes a commit
// subject and is shown in every UI.
func cleanLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	if s == "" {
		s = "checkpoint"
	}
	return s
}

func runGit(ctx context.Context, bin, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, append([]string{"-C", dir}, args...)...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			if i := strings.IndexByte(msg, '\n'); i >= 0 {
				msg = msg[:i]
			}
			return out, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}
