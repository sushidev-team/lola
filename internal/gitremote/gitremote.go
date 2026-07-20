// Package gitremote resolves the GitHub "owner/name" of a local checkout from
// its git remotes, so the project form can prefill [[project]].repo instead of
// making the user copy it by hand.
//
// It reads git remotes only — no network, no gh, no auth. It deliberately does
// NOT live in internal/scm (which is gh-only, one gh invocation per call) or in
// internal/config (static validation, never execs).
//
// FAIL CLOSED. Every failure path returns "" rather than a guess, because an
// empty repo is safe while a WRONG one is not: config.PollRepo("") makes the
// open-PR check unavailable and the reconcile orphan-revert skips (see the
// fail-closed invariant in CLAUDE.md), whereas a wrong owner/name would have
// `gh pr list --repo` answer confidently about someone else's repository. That
// is why a non-GitHub or unrecognised host yields "" and the user types it in.
package gitremote

import (
	"context"
	"os/exec"
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
