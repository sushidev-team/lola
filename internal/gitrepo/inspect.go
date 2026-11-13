package gitrepo

import (
	"context"
	"slices"
	"strings"
)

// Info is everything the project forms can learn about a directory from LOCAL
// git alone — enough to fill a whole [[project]] from one folder pick.
//
// Every field fails closed the same way the package does: a directory that is
// not a checkout, or a git that cannot answer, yields the zero value, and the
// caller's response to a zero value is always "leave the field for the user".
type Info struct {
	// Root is the checkout's top level. Picking a SUBDIRECTORY of a repository
	// resolves here, so a project's Path is the repo root even when the user
	// browsed into src/ before choosing.
	Root string
	// IsRepo reports whether dir is inside a git checkout at all.
	IsRepo bool
	// Repo is the GitHub "owner/name" (upstream, else origin), or "" — see
	// ParseRemoteURL for why an unrecognised host is never guessed at.
	Repo string
	// DefaultBranch is the branch worktrees should fork from: origin/HEAD when
	// git knows it, else a conventional name that actually exists, else the
	// checked-out branch. "" when nothing can be resolved.
	DefaultBranch string
	// Branches is the fork-from candidates, DefaultBranch first.
	Branches []string
}

// conventionalDefaults are the names tried, in order, when the repository has no
// origin/HEAD to point at its default branch. Only a name that actually exists
// in the checkout is used — this narrows a guess, it never invents one.
var conventionalDefaults = []string{"main", "master", "develop", "trunk"}

// Inspector reads a checkout. GitBin is the binary to invoke; empty resolves
// "git" via LookPath, mirroring Detector and BranchLister.
type Inspector struct {
	GitBin string

	// run is the exec seam; nil uses runGitArgs. Tests inject it.
	run func(ctx context.Context, bin, dir string, args ...string) (string, error)
}

// Inspect gathers a directory's git facts in ONE pass. It exists because the
// project forms need all of them together the moment a folder is picked, and
// running Detect and Branches separately meant two independent probes, two
// timeouts and two chances for the answers to disagree about which checkout they
// describe.
//
// Never returns an error: an unusable directory is simply Info{}.
func (i Inspector) Inspect(ctx context.Context, dir string) Info {
	var info Info
	if strings.TrimSpace(dir) == "" {
		return info
	}
	bin := i.GitBin
	if bin == "" {
		bin = "git"
	}
	run := i.run
	if run == nil {
		run = runGitArgs
	}

	out, err := run(ctx, bin, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return info
	}
	root := strings.TrimSpace(firstLine(out))
	if root == "" {
		return info
	}
	info.Root, info.IsRepo = root, true

	// Everything below reads the ROOT rather than the directory handed in: a
	// subdirectory has no remotes of its own, and answering about the checkout
	// is the whole point of resolving the top level first.
	for _, remote := range remotePreference {
		o, err := run(ctx, bin, root, "remote", "get-url", remote)
		if err != nil {
			continue
		}
		if repo := ParseRemoteURL(o); repo != "" {
			info.Repo = repo
			break
		}
	}

	if refs, err := run(ctx, bin, root, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes"); err == nil {
		info.Branches = parseBranches(refs)
	}
	info.DefaultBranch = defaultBranchOf(ctx, run, bin, root, info.Branches)
	if info.DefaultBranch != "" {
		if n := slices.Index(info.Branches, info.DefaultBranch); n > 0 {
			info.Branches = slices.Insert(slices.Delete(info.Branches, n, n+1), 0, info.DefaultBranch)
		}
	}
	return info
}

// Inspect is the package-level convenience for the default inspector.
func Inspect(ctx context.Context, dir string) Info { return Inspector{}.Inspect(ctx, dir) }

// defaultBranchOf resolves the branch a project should fork from, narrowing from
// authoritative to conventional: origin/HEAD is what the remote itself calls
// default; a conventional name is only accepted when the checkout HAS it; and
// the currently checked-out branch is the last resort (in a fresh repo with a
// single branch it is exactly right, and on a feature branch it is at least a
// branch that exists).
func defaultBranchOf(ctx context.Context, run func(context.Context, string, string, ...string) (string, error), bin, root string, branches []string) string {
	if out, err := run(ctx, bin, root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if _, b, ok := strings.Cut(strings.TrimSpace(firstLine(out)), "/"); ok && b != "" {
			return b
		}
	}
	for _, c := range conventionalDefaults {
		if slices.Contains(branches, c) {
			return c
		}
	}
	if out, err := run(ctx, bin, root, "symbolic-ref", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(firstLine(out)); b != "" {
			return b
		}
	}
	return ""
}

// firstLine keeps a multi-line git answer from becoming a path with a newline in
// it — every caller here wants one value.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
