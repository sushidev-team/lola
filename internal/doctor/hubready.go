package doctor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
)

// The HUB READINESS list answers one question in plain words: can this
// machine take the team's chores right now? `lola hub join` prints it right
// after enrolling, `lola hub status` prints it every time, and `lola doctor`
// shows the same rows while the hub is enabled. None is critical: the hub
// only offers chores, and the daemon runs without it.
//
// Every row reports facts and sources only: never a token, never a key's
// value (a pinned policy key is named by its id).

// Readiness row names (stable, like every check name).
const (
	checkGhAuth       = "gh auth"
	checkProjects     = "projects"
	checkPolling      = "polling"
	checkHubJoined    = "hub joined"
	checkHubChores    = "hub chores"
	checkHubDocker    = "hub docker"
	checkHubImage     = "hub sandbox image"
	checkHubLogin     = "hub sandbox login"
	checkHubGh        = "hub github access"
	checkHubPolicyKey = "hub policy key"
)

// ghAuthTimeout bounds `gh auth status`: it may reach the network.
const ghAuthTimeout = 10 * time.Second

// ghAuthStatus runs `gh auth status`, bounded. Its output is discarded: only
// the exit status is read, so nothing it prints (a masked token, a login
// name) can reach a Result. Tests replace it.
var ghAuthStatus = func(ctx context.Context) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errGhMissing
	}
	ctx, cancel := context.WithTimeout(ctx, ghAuthTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "gh", "auth", "status").Run()
}

var errGhMissing = errors.New("gh not found on PATH")

// ghAuthResult renders a gh auth probe as the doctor row.
func ghAuthResult(err error) Result {
	switch {
	case err == nil:
		return Result{Name: checkGhAuth, OK: true, Detail: "logged in"}
	case errors.Is(err, errGhMissing):
		return Result{Name: checkGhAuth, Detail: "gh is not installed, so lola cannot read pull requests or CI", Fix: FixInstallGh}
	case errors.Is(err, context.DeadlineExceeded):
		return Result{Name: checkGhAuth, Detail: "`gh auth status` did not answer in time", Fix: FixGhLogin}
	}
	return Result{Name: checkGhAuth, Detail: "gh is not logged in: run `gh auth login`", Fix: FixGhLogin}
}

// projectsResult is the "no projects" row (nil when there are some).
func projectsResult(cfg *config.Config) (Result, bool) {
	if len(cfg.Projects) > 0 {
		return Result{}, false
	}
	return Result{Name: checkProjects, Detail: "no projects yet: add one (a folder with a git checkout) to give lola work", Fix: FixAddProject}, true
}

// pollingResult reports whether any project polls Linear: enabled and with a
// team. Nil when there are no projects (projectsResult already said so).
func pollingResult(cfg *config.Config) (Result, bool) {
	if len(cfg.Projects) == 0 {
		return Result{}, false
	}
	var polls []string
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.Enabled && p.Polls() {
			polls = append(polls, p.DisplayName())
		}
	}
	if len(polls) == 0 {
		return Result{Name: checkPolling, Detail: "no project polls Linear: give one a team and enable it (hub chores and manual sessions still work)", Fix: FixEnablePoll}, true
	}
	return Result{Name: checkPolling, OK: true, Detail: "polling: " + strings.Join(polls, ", ")}, true
}

// HubReadiness is the readiness list for cfg's hub, probing docker, the
// sandbox credentials and gh as needed (each bounded). The hub itself is not
// contacted: "joined" is what this machine's identity says.
func HubReadiness(ctx context.Context, cfg *config.Config) []Result {
	dir, err := hub.StateDir()
	if err != nil {
		return []Result{{Name: checkHubJoined, Detail: "cannot find lola's state folder: " + err.Error()}}
	}
	var sb *SandboxStatus
	sandbox := func() SandboxStatus {
		if sb == nil {
			st := CheckSandbox(ctx, cfg)
			sb = &st
		}
		return *sb
	}
	return hubReadiness(cfg, dir, sandbox, func() error { return ghAuthStatus(ctx) })
}

// hubReadiness builds the rows; sandbox and gh are probed lazily, so a list
// that needs neither runs nothing.
func hubReadiness(cfg *config.Config, dir string, sandbox func() SandboxStatus, gh func() error) []Result {
	var out []Result
	st := hub.LocalStatus(cfg, dir)
	switch st.Membership() {
	case "joined":
		r := Result{Name: checkHubJoined, OK: true, Detail: fmt.Sprintf("joined team %s as %q", st.Identity.Org, st.Identity.DeviceName)}
		if st.Identity.Member != "" {
			r.Detail += fmt.Sprintf(" (member %s)", st.Identity.Member)
		}
		if st.KeyMismatch || !st.KeyPresent {
			r = Result{Name: checkHubJoined, Detail: "joined, but this machine's device key is not the one that enrolled: leave and join again", Fix: FixRejoinHub}
		}
		out = append(out, r)
	case "revoked":
		out = append(out, Result{Name: checkHubJoined, Detail: fmt.Sprintf("revoked: the hub at %s stopped accepting this machine; ask an admin for a new invite and join again", st.Revoked.URL), Fix: FixRejoinHub})
	default:
		out = append(out, Result{Name: checkHubJoined, Detail: "not joined: ask an admin for an invite, then run `lola hub join`", Fix: FixJoinHub})
	}

	h := cfg.Hub
	var takes, problems []string
	needSandbox := false
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if !p.HubChores {
			continue
		}
		name := p.DisplayName()
		switch {
		case p.IsStack():
			problems = append(problems, name+" is a stack (turn chores on for its member projects)")
		case hubproto.ValidateRepo(p.Repo) != nil:
			problems = append(problems, name+" has no GitHub repo set")
		case h.SandboxRequired() && !p.Sandboxed():
			problems = append(problems, name+" does not run its agent in the sandbox, which this machine requires for chores")
		default:
			takes = append(takes, name)
		}
		if p.Sandboxed() || h.SandboxRequired() {
			needSandbox = true
		}
	}
	sort.Strings(takes)
	chores := Result{Name: checkHubChores, OK: len(takes) > 0 && len(problems) == 0}
	switch {
	case len(takes) == 0 && len(problems) == 0:
		chores.Detail = "no project takes chores: turn on team chores (hub_chores) for a project"
		chores.Fix = FixHubChores
	case len(takes) == 0:
		chores.Detail = "no project can take chores: " + strings.Join(problems, "; ")
	default:
		chores.Detail = "projects that take chores: " + strings.Join(takes, ", ")
		if len(problems) > 0 {
			chores.Detail += "; " + strings.Join(problems, "; ")
		}
	}
	if chores.Fix == FixNone && len(problems) > 0 {
		if strings.Contains(strings.Join(problems, " "), "sandbox") {
			chores.Fix = FixSandboxProject
		} else {
			chores.Fix = FixHubChores
		}
	}
	out = append(out, chores)

	if needSandbox {
		sb := sandbox()
		if sb.DockerOK {
			out = append(out, Result{Name: checkHubDocker, OK: true, Detail: "docker answers (" + sb.Docker + ")"})
			if len(sb.Missing) > 0 {
				out = append(out, Result{Name: checkHubImage, OK: true, Detail: "not pulled yet (pulled on the first chore): " + strings.Join(sb.Missing, ", ")})
			} else {
				out = append(out, Result{Name: checkHubImage, OK: true, Detail: "present: " + strings.Join(sb.Images, ", ")})
			}
		} else {
			out = append(out, Result{Name: checkHubDocker, Detail: "docker does not answer: install and start Docker, OrbStack or Colima; sandboxed chores cannot start", Fix: FixStartDocker})
			out = append(out, Result{Name: checkHubImage, Detail: "unknown until docker runs"})
		}
		if sb.ClaudeSource != "" {
			out = append(out, Result{Name: checkHubLogin, OK: true, Detail: "the sandboxed agent can sign in (token found in " + sb.ClaudeSource + ")"})
		} else {
			out = append(out, Result{Name: checkHubLogin, Detail: "the sandboxed agent cannot sign in: run `claude setup-token`, then `lola sandbox token`", Fix: FixSandboxToken})
		}
	}

	gr := ghAuthResult(gh())
	gr.Name = checkHubGh
	if gr.OK {
		gr.Detail = "gh is logged in: lola can read advisories and open pull requests"
	}
	out = append(out, gr)

	if n := len(h.TrustedPolicyKeys); n > 0 {
		ids := make([]string, 0, n)
		for id := range h.TrustedPolicyKeys {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		out = append(out, Result{Name: checkHubPolicyKey, OK: true, Detail: "team policy key pinned: " + strings.Join(ids, ", ")})
	} else {
		out = append(out, Result{Name: checkHubPolicyKey, Detail: "no team policy key pinned: the team's settings cannot be verified, so none apply here", Fix: FixPinPolicyKey})
	}
	return out
}
