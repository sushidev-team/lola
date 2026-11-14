// Package projectgit powers the human-operated project repository workspace.
// It uses the installed Git and its normal identity, signing and hook settings.
package projectgit

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
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/gitguard"
)

type Commit struct {
	SHA     string   `json:"sha"`
	Parents []string `json:"parents"`
	Subject string   `json:"subject"`
	Author  string   `json:"author"`
	Email   string   `json:"email"`
	Date    string   `json:"date"`
	Refs    []string `json:"refs"`
}
type Ref struct {
	Name     string `json:"name"`
	FullName string `json:"fullName"`
	SHA      string `json:"sha"`
	Remote   bool   `json:"remote"`
	Current  bool   `json:"current"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
}
type Worktree struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Head     string `json:"head"`
	Locked   bool   `json:"locked"`
	Prunable bool   `json:"prunable"`
	Blocked  bool   `json:"blocked"`
	// Changes counts the uncommitted files in a checkout OTHER than the one the
	// snapshot was taken in (that one lists its files in Snapshot.Files). Zero
	// also means "not asked" or "could not be read": a count is a cue to go and
	// look, never a claim that a checkout is clean.
	Changes   int `json:"changes,omitempty"`
	Added     int `json:"added,omitempty"`
	Modified  int `json:"modified,omitempty"`
	Deleted   int `json:"deleted,omitempty"`
	Conflicts int `json:"conflicts,omitempty"`
}

// ChangeCounts splits a checkout's uncommitted files the way the history's
// working row does: a conflict first, then added (untracked or staged new),
// then deleted, and everything else modified.
type ChangeCounts struct{ Total, Added, Modified, Deleted, Conflicts int }
type File struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath"`
	Index     string `json:"index"`
	Worktree  string `json:"worktree"`
	Untracked bool   `json:"untracked"`
	Conflict  bool   `json:"conflict"`
}
type Signing struct {
	Enabled bool   `json:"enabled"`
	Format  string `json:"format"`
	Key     string `json:"key"`
}
type Snapshot struct {
	Operation    string     `json:"operation"`
	MergeMessage string     `json:"mergeMessage,omitempty"`
	Remotes      []Remote   `json:"remotes"`
	Path         string     `json:"path"`
	Branch       string     `json:"branch"`
	Head         string     `json:"head"`
	Upstream     string     `json:"upstream"`
	Commits      []Commit   `json:"commits"`
	Refs         []Ref      `json:"refs"`
	Worktrees    []Worktree `json:"worktrees"`
	Files        []File     `json:"files"`
	Signing      Signing    `json:"signing"`
	HasMore      bool       `json:"hasMore"`
}

// Client's seam is deliberately private: bound methods never accept an executable.
type Client struct {
	run func(context.Context, string, string, ...string) ([]byte, error)
}

const outputLimit = 8 << 20

var errOutputLimit = errors.New("Git output is too large; choose a narrower history or file")

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > outputLimit {
		return 0, errOutputLimit
	}
	return b.Buffer.Write(p)
}
func runGit(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
	return runGitEnvironment(ctx, dir, input, nil, args...)
}
func runGitEnvironment(ctx context.Context, dir, input string, env []string, args ...string) ([]byte, error) {
	if gitguard.Pinned(dir) {
		return nil, errors.New("sandbox worktrees must be managed through their session")
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"--literal-pathspecs", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = strings.NewReader(input)
	cmd.WaitDelay = 2 * time.Second
	var out, stderr boundedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("Git operation timed out: %w", ctx.Err())
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 4000 {
			message = message[len(message)-4000:]
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, message)
	}
	return out.Bytes(), nil
}
func (c Client) git(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
	run := c.run
	if run == nil {
		run = runGit
	}
	return run(ctx, dir, input, args...)
}
func (c Client) text(ctx context.Context, dir string, args ...string) (string, error) {
	b, e := c.git(ctx, dir, "", args...)
	if e != nil {
		return "", e
	}
	return strings.TrimSpace(string(b)), nil
}

func (c Client) Worktrees(ctx context.Context, dir string) ([]Worktree, error) {
	b, err := c.git(ctx, dir, "", "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	trees := []Worktree{}
	var current *Worktree
	for _, line := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(line, "worktree ") {
			trees = append(trees, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			current = &trees[len(trees)-1]
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case strings.HasPrefix(line, "locked"):
			current.Locked = true
		case strings.HasPrefix(line, "prunable"):
			current.Prunable = true
		}
	}
	return trees, nil
}

// ChangeCounts counts the uncommitted files in dir, as status lists them.
func (c Client) ChangeCounts(ctx context.Context, dir string) (ChangeCounts, error) {
	files, err := c.status(ctx, dir)
	n := ChangeCounts{Total: len(files)}
	for _, f := range files {
		switch {
		case f.Conflict:
			n.Conflicts++
		case f.Untracked || f.Index == "A":
			n.Added++
		case f.Index == "D" || f.Worktree == "D":
			n.Deleted++
		default:
			n.Modified++
		}
	}
	return n, err
}
func (c Client) status(ctx context.Context, dir string) ([]File, error) {
	b, err := c.git(ctx, dir, "", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	records := strings.Split(string(b), "\x00")
	files := []File{}
	for i := 0; i < len(records); i++ {
		r := records[i]
		if len(r) < 4 {
			continue
		}
		f := File{Path: r[3:], Index: r[:1], Worktree: r[1:2], Untracked: r[:2] == "??"}
		f.Conflict = strings.Contains(r[:2], "U") || r[:2] == "AA" || r[:2] == "DD"
		if strings.ContainsAny(r[:2], "RC") {
			i++
			if i >= len(records) {
				return nil, errors.New("incomplete rename from Git")
			}
			f.OldPath = records[i]
		}
		files = append(files, f)
	}
	return files, nil
}
func (c Client) Snapshot(ctx context.Context, dir, ref string, limit int) (Snapshot, error) {
	s := Snapshot{Remotes: c.Remotes(ctx, dir), Path: dir, Commits: []Commit{}, Refs: []Ref{}, Worktrees: []Worktree{}, Files: []File{}}
	var err error
	s.Worktrees, err = c.Worktrees(ctx, dir)
	if err != nil {
		return s, err
	}
	s.Files, err = c.status(ctx, dir)
	if err != nil {
		return s, err
	}
	s.Operation, err = c.Operation(ctx, dir)
	if err != nil {
		return s, err
	}
	if s.Operation == "merge" {
		s.MergeMessage, err = c.MergeMessage(ctx, dir)
		if err != nil {
			return s, err
		}
	}
	s.Branch, _ = c.text(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	s.Head, _ = c.text(ctx, dir, "rev-parse", "--verify", "HEAD")
	s.Upstream, _ = c.text(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	b, err := c.git(ctx, dir, "", "for-each-ref", "--format=%(refname)%00%(objectname)%00%(HEAD)%00%(upstream)%00%(upstream:track,nobracket)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return s, err
	}
	bySHA := map[string][]string{}
	validRef := ref == ""
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		p := strings.Split(line, "\x00")
		if len(p) != 5 {
			continue
		}
		remote := strings.HasPrefix(p[0], "refs/remotes/")
		name := strings.TrimPrefix(strings.TrimPrefix(p[0], "refs/heads/"), "refs/remotes/")
		r := Ref{Name: name, FullName: p[0], SHA: p[1], Remote: remote, Current: p[2] == "*", Upstream: p[3]}
		for _, tracking := range strings.Split(p[4], ", ") {
			parts := strings.Fields(tracking)
			if len(parts) != 2 {
				continue
			}
			n, err := strconv.Atoi(parts[1])
			if err != nil || n < 0 {
				continue
			}
			switch parts[0] {
			case "ahead":
				r.Ahead = n
			case "behind":
				r.Behind = n
			}
		}
		s.Refs = append(s.Refs, r)
		bySHA[r.SHA] = append(bySHA[r.SHA], name)
		if ref == r.FullName {
			validRef = true
		}
	}
	if !validRef {
		return s, errors.New("the selected branch no longer exists; refresh the repository")
	}
	// Tags decorate commits but are not branches or checkout targets. Peel
	// annotated tags so their labels attach to the commit, not the tag object.
	b, err = c.git(ctx, dir, "", "for-each-ref", "--format=%(refname:strip=2)%00%(objecttype)%00%(objectname)%00%(*objecttype)%00%(*objectname)", "refs/tags/")
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		p := strings.Split(line, "\x00")
		if len(p) != 5 {
			continue
		}
		sha := p[2]
		if p[1] == "tag" {
			if p[3] == "tag" {
				sha, err = c.text(ctx, dir, "rev-parse", "--verify", "refs/tags/"+p[0]+"^{commit}")
				if err != nil {
					continue
				}
			} else if p[3] == "commit" {
				sha = p[4]
			} else {
				continue
			}
		} else if p[1] != "commit" {
			continue
		}
		bySHA[sha] = append(bySHA[sha], "tag: "+p[0])
	}
	enabled, _ := c.text(ctx, dir, "config", "--bool", "--get", "commit.gpgsign")
	s.Signing.Enabled = enabled == "true"
	s.Signing.Format, _ = c.text(ctx, dir, "config", "--get", "gpg.format")
	if s.Signing.Format == "" {
		s.Signing.Format = "openpgp"
	}
	s.Signing.Key, _ = c.text(ctx, dir, "config", "--get", "user.signingkey")
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	if s.Head == "" && len(s.Refs) == 0 {
		return s, nil
	}
	args := []string{"log", "--topo-order", "-z", "--format=%H%x00%P%x00%s%x00%an%x00%ae%x00%aI", "-n", strconv.Itoa(limit + 1)}
	if ref == "" {
		args = append(args, "--branches", "--remotes", "--tags")
		if s.Head != "" {
			args = append(args, "HEAD")
		}
	} else {
		args = append(args, ref)
	}
	args = append(args, "--")
	b, err = c.git(ctx, dir, "", args...)
	if err != nil {
		return s, err
	}
	fields := strings.Split(string(b), "\x00")
	for i := 0; i+5 < len(fields); i += 6 {
		sha := fields[i]
		if sha == "" {
			continue
		}
		refs := bySHA[sha]
		if refs == nil {
			refs = []string{}
		}
		s.Commits = append(s.Commits, Commit{SHA: sha, Parents: strings.Fields(fields[i+1]), Subject: fields[i+2], Author: fields[i+3], Email: fields[i+4], Date: fields[i+5], Refs: refs})
	}
	if len(s.Commits) > limit {
		s.HasMore = true
		s.Commits = s.Commits[:limit]
	}
	return s, nil
}

var shaPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func (c Client) commitSHA(ctx context.Context, dir, sha string) (string, error) {
	if !shaPattern.MatchString(sha) {
		return "", errors.New("select a full commit ID")
	}
	return c.text(ctx, dir, "rev-parse", "--verify", sha+"^{commit}")
}
func validPath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != ".." && !strings.HasPrefix(path, "../") && !strings.ContainsRune(path, 0)
}

func (c Client) Diff(ctx context.Context, dir, from, to, area, path string) (gitdiff.Result, error) {
	result := gitdiff.Result{Files: []gitdiff.File{}}
	if path != "" && !validPath(path) {
		return result, errors.New("invalid file path")
	}
	if area == "untracked" {
		return c.untracked(ctx, dir, path)
	}
	args := []string{"-c", "core.quotepath=off", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M", "--unified=3"}
	switch area {
	case "working":
		var err error
		from, err = c.commitSHA(ctx, dir, from)
		if err != nil {
			return result, err
		}
		var cleanup func()
		c, cleanup, err = c.workingIndex(ctx, dir)
		if err != nil {
			return result, err
		}
		defer cleanup()
		args = append(args, "--cached", from)
	case "commit":
		var err error
		to, err = c.commitSHA(ctx, dir, to)
		if err != nil {
			return result, err
		}
		if from != "" {
			from, err = c.commitSHA(ctx, dir, from)
			if err != nil {
				return result, err
			}
			args = append(args, from, to)
		} else {
			args = []string{"-c", "core.quotepath=off", "show", "--format=", "--first-parent", "--root", "--no-color", "--no-ext-diff", "--no-textconv", "-M", "--unified=3", to}
		}
	case "staged":
		args = append(args, "--cached")
	case "unstaged":
	default:
		return result, errors.New("unknown diff selection")
	}
	args = append(args, "--")
	if path != "" {
		args = append(args, path)
	}
	b, err := c.git(ctx, dir, "", args...)
	if err != nil {
		return result, err
	}
	result.Base = from
	result.MergeBase = from
	budget := gitdiff.MaxPatchBytes
	for _, f := range gitdiff.SplitPatch(string(b)) {
		if len(f.Patch) > gitdiff.MaxFilePatchBytes {
			f.Patch = ""
			f.TooLarge = true
		}
		if len(f.Patch) > budget {
			result.Truncated = true
			f.Patch = ""
			f.TooLarge = true
		}
		budget -= len(f.Patch)
		result.Files = append(result.Files, f)
	}
	return result, nil
}
func (c Client) untracked(ctx context.Context, dir, path string) (gitdiff.Result, error) {
	r := gitdiff.Result{Files: []gitdiff.File{}}
	if !validPath(path) {
		return r, errors.New("select an untracked file")
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		return r, err
	}
	found := false
	for _, f := range files {
		if f.Path == path && f.Untracked {
			found = true
		}
	}
	if !found {
		return r, errors.New("file is no longer untracked; refresh the repository")
	}
	full := filepath.Join(dir, path)
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return r, err
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return r, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !validPath(rel) {
		return r, errors.New("file points outside the repository")
	}
	f := gitdiff.File{Path: path, Status: "added", Untracked: true}
	info, err := os.Lstat(full)
	if err != nil {
		return r, err
	}
	if !info.Mode().IsRegular() {
		f.Binary = true
		r.Files = append(r.Files, f)
		return r, nil
	}
	if info.Size() > gitdiff.MaxFilePatchBytes {
		f.TooLarge = true
		r.Files = append(r.Files, f)
		return r, nil
	}
	file, err := os.Open(full)
	if err != nil {
		return r, err
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, gitdiff.MaxFilePatchBytes+1))
	if err != nil {
		return r, err
	}
	if len(b) > gitdiff.MaxFilePatchBytes {
		f.TooLarge = true
	} else if bytes.ContainsRune(b, 0) {
		f.Binary = true
	} else if len(b) > 0 {
		lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		f.Additions = len(lines)
		var patch strings.Builder
		patch.Grow(len(b) + len(lines) + 64)
		fmt.Fprintf(&patch, "@@ -0,0 +1,%d @@\n", len(lines))
		for _, line := range lines {
			patch.WriteByte('+')
			patch.WriteString(line)
			patch.WriteByte('\n')
		}
		if b[len(b)-1] != '\n' {
			patch.WriteString("\\ No newline at end of file\n")
		}
		f.Patch = patch.String()
	}
	r.Files = append(r.Files, f)
	return r, nil
}

// ChangeIndex only accepts paths currently reported by Git and passes literal
// pathspecs. Rename endpoints belong to the area being changed: an index
// rename's old path is absent from the index, so staging later edits must only
// name its destination. Unstaging the rename restores both index endpoints.
func (c Client) ChangeIndex(ctx context.Context, dir string, paths []string, stage bool) error {
	if len(paths) == 0 || len(paths) > 2000 {
		return errors.New("select between 1 and 2000 files")
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		return err
	}
	known := map[string]File{}
	for _, f := range files {
		known[f.Path] = f
	}
	selected := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		f, ok := known[path]
		if !ok || !validPath(path) {
			return errors.New("a selected file changed; refresh the repository")
		}
		if f.Conflict {
			return errors.New("resolve conflicts before changing the index")
		}
		if stage && f.Worktree == " " && !f.Untracked {
			continue
		}
		if !stage && (f.Index == " " || f.Untracked) {
			continue
		}
		endpoints := []string{f.Path}
		if (stage && f.Worktree == "R") || (!stage && f.Index == "R") {
			endpoints = append(endpoints, f.OldPath)
		}
		for _, p := range endpoints {
			if p != "" && !seen[p] {
				selected = append(selected, p)
				seen[p] = true
			}
		}
	}
	if len(selected) == 0 {
		return nil
	}
	args := []string{"add", "--"}
	if !stage {
		if _, err := c.text(ctx, dir, "rev-parse", "--verify", "HEAD"); err != nil {
			// With no HEAD there is no tree to restore. --cached keeps working
			// files intact; -f permits dropping a staged version that differs
			// from subsequent working edits, matching ordinary unstage behavior.
			args = []string{"rm", "--cached", "-f", "--"}
		} else {
			args = []string{"restore", "--staged", "--"}
		}
	}
	_, err = c.git(ctx, dir, "", append(args, selected...)...)
	return err
}

// ChangeIndexAndStatus returns the authoritative file list without rereading
// history, refs, worktrees or configuration after a file-only mutation.
func (c Client) ChangeIndexAndStatus(ctx context.Context, dir string, paths []string, stage bool) ([]File, error) {
	if err := c.ChangeIndex(ctx, dir, paths, stage); err != nil {
		return nil, err
	}
	return c.status(ctx, dir)
}

func (c Client) Commit(ctx context.Context, dir, message string) error {
	return c.commit(ctx, dir, message, "")
}

// Amend is an explicit human action. expectedHead is the commit the user saw
// when choosing amend; a stale workspace must not rewrite a newer commit.
func (c Client) Amend(ctx context.Context, dir, message, expectedHead string) error {
	if !shaPattern.MatchString(expectedHead) {
		return errors.New("refresh the repository and choose a commit to amend")
	}
	return c.commit(ctx, dir, message, expectedHead)
}

// AmendNeedsConfirmation checks all locally known remote histories, including
// descendants of HEAD and remotes other than the branch's configured upstream.
// It never fetches or treats a failed query as proof that a commit is local.
func (c Client) AmendNeedsConfirmation(ctx context.Context, dir, expectedHead string) (bool, error) {
	if !shaPattern.MatchString(expectedHead) {
		return false, errors.New("refresh the repository and choose a commit to amend")
	}
	if err := c.requireHead(ctx, dir, expectedHead); err != nil {
		return false, err
	}
	refs, err := c.text(ctx, dir, "for-each-ref", "--format=%(refname)", "--contains="+expectedHead, "refs/remotes/")
	if err != nil {
		return false, err
	}
	return refs != "", nil
}

func (c Client) LastCommitMessage(ctx context.Context, dir, expectedHead string) (string, error) {
	if !shaPattern.MatchString(expectedHead) {
		return "", errors.New("refresh the repository and choose a commit to amend")
	}
	if err := c.requireHead(ctx, dir, expectedHead); err != nil {
		return "", err
	}
	b, err := c.git(ctx, dir, "", "show", "--no-patch", "--format=%B", expectedHead, "--")
	if err != nil {
		return "", err
	}
	message := strings.TrimRight(string(b), "\n")
	if len(message) > 65536 || strings.ContainsRune(message, 0) {
		return "", errors.New("the existing commit message cannot be edited here; use Git directly")
	}
	return message, nil
}

func (c Client) requireHead(ctx context.Context, dir, expectedHead string) error {
	head, err := c.text(ctx, dir, "rev-parse", "--verify", "HEAD")
	if err != nil || head != expectedHead {
		return errors.New("HEAD changed since amend was selected; refresh and choose amend again")
	}
	return nil
}

func (c Client) commit(ctx context.Context, dir, message, expectedHead string) error {
	if strings.TrimSpace(message) == "" || len(message) > 65536 || strings.ContainsRune(message, 0) {
		return errors.New("enter a commit message of at most 64 KB")
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		return err
	}
	staged := false
	for _, f := range files {
		if f.Conflict {
			return errors.New("resolve conflicts before committing")
		}
		if !f.Untracked && f.Index != " " {
			staged = true
		}
	}
	if !staged {
		return errors.New("stage changes before committing")
	}
	args := []string{"commit", "--file=-"}
	if expectedHead != "" {
		if err := c.requireHead(ctx, dir, expectedHead); err != nil {
			return err
		}
		args = append(args, "--amend")
	}
	// Do not disable signing or hooks. Git's repository/global settings apply.
	_, err = c.git(ctx, dir, message, args...)
	return err
}
func (c Client) Sync(ctx context.Context, dir, action string) error {
	var args []string
	switch action {
	case "fetch":
		args = []string{"fetch", "--all", "--prune"}
	case "pull":
		ref, err := c.text(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
		if err != nil {
			return err
		}
		head, err := c.text(ctx, dir, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return err
		}
		return c.BranchAction(ctx, dir, BranchRequest{Action: "pull", Source: ref, SourceSHA: head})
	case "push":
		args = []string{"push"}
	default:
		return errors.New("unknown Git action")
	}
	_, err := c.git(ctx, dir, "", args...)
	return err
}

// Context bounds all calls, including hooks and credential/signing helpers.
func Context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}
