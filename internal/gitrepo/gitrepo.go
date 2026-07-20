// Package gitrepo reads local metadata about a checkout — the GitHub
// "owner/name" behind its remotes, and the branches it offers — so the project
// forms can prefill [[project]].repo and offer a branch list instead of making
// the user type either by hand.
//
// It shells to git only — no network, no gh, no auth. It deliberately does NOT
// live in internal/scm (which is gh-only, one gh invocation per call) or in
// internal/config (static validation, never execs).
//
// FAIL CLOSED. Every failure path returns "" rather than a guess, because an
// empty repo is safe while a WRONG one is not: config.PollRepo("") makes the
// open-PR check unavailable and the reconcile orphan-revert skips (see the
// fail-closed invariant in CLAUDE.md), whereas a wrong owner/name would have
// `gh pr list --repo` answer confidently about someone else's repository. That
// is why a non-GitHub or unrecognised host yields "" and the user types it in.
package gitrepo

import (
	"context"
	"os/exec"
	"slices"
	"strings"
)

// remotePreference is the order remotes are consulted. "upstream" comes first
// on purpose: in a fork, origin is your fork but upstream is the repository the
// pull requests actually land in, which is the one PR/CI observation must watch.
var remotePreference = []string{"upstream", "origin"}

// Detector reads a checkout's remotes. GitBin is the binary to invoke; empty
// resolves "git" via LookPath — mirroring internal/worktree's exec seam.
type Detector struct {
	GitBin string

	// run is the exec seam; nil uses runGit. Tests inject it.
	run func(ctx context.Context, bin, dir, remote string) (string, error)
}

// Detect returns the GitHub "owner/name" for the checkout at dir, or "" when it
// cannot be determined — not a git repo, no GitHub remote, an unparsable or
// non-GitHub URL. Never returns an error: every failure is "unknown", and the
// caller's correct response to unknown is always the same (leave the field
// empty for the user to fill).
func (d Detector) Detect(ctx context.Context, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	bin := d.GitBin
	if bin == "" {
		bin = "git"
	}
	run := d.run
	if run == nil {
		run = runGit
	}
	for _, remote := range remotePreference {
		out, err := run(ctx, bin, dir, remote)
		if err != nil {
			continue
		}
		if repo := ParseRemoteURL(out); repo != "" {
			return repo
		}
	}
	return ""
}

// Detect is the package-level convenience for the default detector.
func Detect(ctx context.Context, dir string) string { return Detector{}.Detect(ctx, dir) }

func runGit(ctx context.Context, bin, dir, remote string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "-C", dir, "remote", "get-url", remote)
	out, err := cmd.Output()
	return string(out), err
}

// ParseRemoteURL extracts "owner/name" from a git remote URL, or "" when the
// URL is not a recognisable GitHub reference. Pure — no exec, no network.
//
// Handles the forms git actually stores:
//
//	git@github.com:owner/name.git
//	ssh://git@github.com/owner/name.git
//	https://github.com/owner/name(.git)
//	https://user@github.com/owner/name.git
//	git://github.com/owner/name.git
//
// A non-GitHub host (gitlab.com, a self-hosted GHE on a bare corporate domain)
// returns "" — see the fail-closed note in the package doc. GitHub Enterprise
// on a github.* host is accepted, since owner/name is still the right pair.
func ParseRemoteURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	var host, path string
	switch {
	case strings.Contains(s, "://"):
		// scheme://[user@]host[:port]/owner/name
		rest := s[strings.Index(s, "://")+3:]
		if i := strings.Index(rest, "/"); i >= 0 {
			host, path = rest[:i], rest[i+1:]
		} else {
			return ""
		}
	case strings.Contains(s, ":"):
		// scp-like: [user@]host:owner/name
		i := strings.Index(s, ":")
		host, path = s[:i], s[i+1:]
	default:
		return ""
	}

	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if colon := strings.Index(host, ":"); colon >= 0 {
		host = host[:colon] // strip an explicit port
	}
	if !isGitHubHost(host) {
		return ""
	}

	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !ok || owner == "" || name == "" {
		return ""
	}
	// Anything deeper than owner/name is not a repository reference.
	if strings.Contains(name, "/") {
		return ""
	}
	return owner + "/" + name
}

// isGitHubHost reports whether a remote host is GitHub or a GitHub Enterprise
// deployment identifiable by name. A corporate GHE on an unrelated domain is
// not recognisable from the URL alone and is treated as unknown.
func isGitHubHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "github.com" || strings.HasPrefix(host, "github.")
}

// ---- branches --------------------------------------------------------------

// BranchLister enumerates the branches a checkout can fork worktrees from.
// GitBin is the binary to invoke; empty resolves "git" via LookPath.
type BranchLister struct {
	GitBin string

	// run is the exec seam; nil uses runGitArgs. Tests inject it.
	run func(ctx context.Context, bin, dir string, args ...string) (string, error)
}

// Branches returns the branch names available in the checkout at dir: local
// branches plus any remote-tracking branch that has no local counterpart (so a
// base branch you have never checked out is still offerable), with the
// "<remote>/" prefix stripped and duplicates collapsed.
//
// Ordering puts the repository's own default branch first — that is the answer
// in the overwhelming majority of cases — and the rest alphabetically.
//
// Returns nil when dir is not a checkout or git fails. As with Detect, an empty
// result is "unknown", never an error: the caller's response is to let the user
// type the branch instead.
func (b BranchLister) Branches(ctx context.Context, dir string) []string {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	bin := b.GitBin
	if bin == "" {
		bin = "git"
	}
	run := b.run
	if run == nil {
		run = runGitArgs
	}

	// refs/heads gives local branches, refs/remotes the tracking ones. One
	// invocation for both keeps this cheap enough to run on a field blur.
	out, err := run(ctx, bin, dir, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// refs/remotes entries are "<remote>/<branch>"; "<remote>/HEAD" is a
		// symbolic pointer, not a branch anyone forks from.
		if remote, branch, ok := strings.Cut(name, "/"); ok && isRemoteRef(line) {
			if branch == "HEAD" || branch == "" {
				continue
			}
			_ = remote
			name = branch
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	slices.Sort(names)

	// Float the repository's own default branch to the top.
	if def := (BranchLister{GitBin: bin, run: run}).defaultBranch(ctx, dir); def != "" {
		if i := slices.Index(names, def); i > 0 {
			names = slices.Insert(slices.Delete(names, i, i+1), 0, def)
		}
	}
	return names
}

// isRemoteRef reports whether a for-each-ref short name came from refs/remotes.
// A short name alone is ambiguous — a local branch may legitimately be called
// "feature/x" — so this re-checks against the remote list.
func isRemoteRef(shortName string) bool {
	remote, _, ok := strings.Cut(strings.TrimSpace(shortName), "/")
	if !ok {
		return false
	}
	// The common remotes; anything else is treated as a local branch name, which
	// is the safe direction (it stays listed under its full name).
	return remote == "origin" || remote == "upstream"
}

// defaultBranch resolves the checkout's own default branch from
// refs/remotes/origin/HEAD, or "" when the symbolic ref is not set (a fresh
// clone without it, or no origin).
func (b BranchLister) defaultBranch(ctx context.Context, dir string) string {
	run := b.run
	if run == nil {
		run = runGitArgs
	}
	out, err := run(ctx, b.GitBin, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	_, branch, ok := strings.Cut(strings.TrimSpace(out), "/")
	if !ok {
		return ""
	}
	return branch
}

// Branches is the package-level convenience for the default lister.
func Branches(ctx context.Context, dir string) []string {
	return BranchLister{}.Branches(ctx, dir)
}

func runGitArgs(ctx context.Context, bin, dir string, args ...string) (string, error) {
	if bin == "" {
		bin = "git"
	}
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, bin, full...).Output()
	return string(out), err
}
