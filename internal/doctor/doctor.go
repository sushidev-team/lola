// Package doctor runs lola's structured health checks and returns them as
// plain data so the CLI (`lola doctor`) and the TUI can render them however
// they like — this package never prints. It probes the native runtime's
// external tools (tmux, git, claude, gh), the Linear API key, the daemon
// socket, and the config (validity + per-project repos).
//
// Secret discipline: the Linear API key value is NEVER placed in a Result
// (or anywhere else). The key check reports only where the key was found —
// never the key itself — mirroring internal/secrets, whose errors already
// name only the sources tried.
package doctor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/secrets"
)

// Check names. Stable strings so renderers (and RuntimeResults) can key off
// them; per-project checks use projectCheckName.
const (
	checkTmux   = "tmux"
	checkGit    = "git"
	checkClaude = "claude"
	checkGh     = "gh"
	checkLinear = "linear api key"
	checkDaemon = "daemon"
	checkConfig = "config"
)

// execTimeout bounds every subprocess a check runs (only `claude --version`
// today); LookPath probes never exec.
const execTimeout = 5 * time.Second

// socketTimeout bounds the daemon-socket dial. The daemon may legitimately be
// down (doctor must still work), so this stays short.
const socketTimeout = 500 * time.Millisecond

// Result is one health check outcome. Critical=true means the daemon cannot
// function without it (a failing critical check makes Report.OK() false).
type Result struct {
	Name     string
	OK       bool
	Detail   string
	Critical bool
}

// Report is the full set of check Results, in check order.
type Report struct {
	Results []Result
}

// OK reports whether every critical check passed. Non-critical failures
// (warnings) do not make the report fail.
func (r Report) OK() bool {
	for _, res := range r.Results {
		if res.Critical && !res.OK {
			return false
		}
	}
	return true
}

// Summary is a one-line tally, e.g. "7 ok, 1 warning, 1 critical". A warning
// is a failed non-critical check; a critical is a failed critical check.
func (r Report) Summary() string {
	var ok, warn, crit int
	for _, res := range r.Results {
		switch {
		case res.OK:
			ok++
		case res.Critical:
			crit++
		default:
			warn++
		}
	}
	return fmt.Sprintf("%d ok, %d warning, %d critical", ok, warn, crit)
}

// RuntimeResults returns the subset of results covering the native runtime's
// mandatory tools (tmux, git, claude) — the same trio daemon.checkRuntimeHealth
// gates spawning on. Renderers (and, later, the daemon) can reuse this to show
// "why can't lola spawn" without re-probing.
func RuntimeResults(r Report) []Result {
	var out []Result
	for _, res := range r.Results {
		switch res.Name {
		case checkTmux, checkGit, checkClaude:
			out = append(out, res)
		}
	}
	return out
}

// Check runs every health check and returns the assembled Report. cfg may be
// nil: the config-dependent checks (Linear key, config validity, per-project
// repos) are then skipped with a single explanatory note. All execs are bounded
// by execTimeout derived from ctx.
func Check(ctx context.Context, cfg *config.Config) Report {
	var r Report
	add := func(res Result) { r.Results = append(r.Results, res) }

	// Native runtime tools. Presence is a bare PATH lookup; only claude is
	// exec'd (for its version), under the 5s bound.
	add(lookPathResult(checkTmux, true))
	add(lookPathResult(checkGit, true))
	add(claudeResult(ctx))
	add(ghResult())

	if cfg == nil {
		add(Result{
			Name:     checkConfig,
			OK:       false,
			Critical: false,
			Detail:   "config not loaded; config-dependent checks (linear key, validity, projects) skipped",
		})
		add(daemonResult())
		return r
	}

	add(linearResult(cfg))
	add(daemonResult())
	add(configResult(cfg))
	for _, res := range projectResults(cfg) {
		add(res)
	}
	return r
}

// lookPathResult resolves name on PATH. Detail is the resolved absolute path
// or "not found on PATH".
func lookPathResult(name string, critical bool) Result {
	path, err := exec.LookPath(name)
	if err != nil {
		return Result{Name: name, OK: false, Critical: critical, Detail: "not found on PATH"}
	}
	return Result{Name: name, OK: true, Critical: critical, Detail: path}
}

// claudeResult resolves claude and, when present, appends the first line of
// `claude --version` (bounded by execTimeout). A version-exec failure does not
// fail the check — presence on PATH is what matters.
func claudeResult(ctx context.Context) Result {
	res := lookPathResult(checkClaude, true)
	if !res.OK {
		return res
	}
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "claude", "--version").Output()
	if err != nil {
		res.Detail = res.Detail + " (version unavailable)"
		return res
	}
	if line := firstLine(out); line != "" {
		res.Detail = res.Detail + " (" + line + ")"
	}
	return res
}

// ghResult reports gh presence. gh is only needed to reconcile PR checks, so a
// missing gh is a warning, not critical.
func ghResult() Result {
	res := lookPathResult(checkGh, false)
	if res.OK {
		res.Detail = res.Detail + " (needed only for PR/CI reconcile)"
	} else {
		res.Detail = "not found on PATH (PR/CI reconcile disabled)"
	}
	return res
}

// linearResult reports whether the Linear API key resolves, and from where,
// without ever exposing the key. It is critical only when a key source is
// actually configured (keychain service or env var): with neither set there is
// nothing lola can do, and doctor should not hard-fail on an unconfigured key.
func linearResult(cfg *config.Config) Result {
	kc := cfg.Linear.APIKeyKeychain
	env := cfg.Linear.APIKeyEnv
	hasSource := kc != "" || env != ""

	key, err := secrets.LinearAPIKey(kc, env)
	if err != nil {
		// secrets errors name only the sources tried, never a value.
		return Result{Name: checkLinear, OK: false, Critical: hasSource, Detail: err.Error()}
	}
	// Attribute the source without printing the key. secrets tries the
	// keychain first, then the env var; comparing the resolved key to the env
	// var's value tells them apart. The comparison never leaks the key.
	detail := "found"
	switch {
	case env != "" && os.Getenv(env) == key:
		detail = "found in env " + env
	case kc != "":
		detail = fmt.Sprintf("found in keychain %q", kc)
	case env != "":
		detail = "found in env " + env
	}
	return Result{Name: checkLinear, OK: true, Critical: hasSource, Detail: detail}
}

// daemonResult dials the daemon's unix socket. A running daemon answers;
// otherwise the check is a (non-critical) note that doctor still works with the
// daemon down.
func daemonResult() Result {
	home, err := config.Home()
	if err != nil {
		return Result{Name: checkDaemon, OK: false, Critical: false, Detail: "cannot resolve LOLA_HOME: " + err.Error()}
	}
	sock := filepath.Join(home, "lola.sock")
	conn, err := net.DialTimeout("unix", sock, socketTimeout)
	if err != nil {
		return Result{Name: checkDaemon, OK: false, Critical: false, Detail: "not running (start with: lola run)"}
	}
	conn.Close()
	return Result{Name: checkDaemon, OK: true, Critical: false, Detail: "running (" + sock + ")"}
}

// configResult runs static config validation. Detail is the first validation
// error or "valid".
func configResult(cfg *config.Config) Result {
	if err := cfg.Validate(); err != nil {
		return Result{Name: checkConfig, OK: false, Critical: true, Detail: firstErr(err)}
	}
	return Result{Name: checkConfig, OK: true, Critical: true, Detail: "valid"}
}

// projectResults checks each [[project]]'s path exists and is a git repo.
// Per-project problems are warnings (not critical): a bad project only breaks
// polls that reference it. With no projects configured, no results are added.
func projectResults(cfg *config.Config) []Result {
	var out []Result
	for i := range cfg.Projects {
		p := cfg.Projects[i]
		name := projectCheckName(p.Name)
		switch {
		case p.Path == "":
			out = append(out, Result{Name: name, OK: false, Detail: "no path configured"})
		default:
			out = append(out, projectPathResult(name, p.Path))
		}
	}
	return out
}

// projectPathResult stats the project path and its .git entry (a directory for
// a normal clone, a file for a linked worktree — os.Stat accepts both).
func projectPathResult(name, path string) Result {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{Name: name, OK: false, Detail: "path does not exist: " + path}
		}
		return Result{Name: name, OK: false, Detail: "cannot stat " + path + ": " + err.Error()}
	}
	if !info.IsDir() {
		return Result{Name: name, OK: false, Detail: "not a directory: " + path}
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return Result{Name: name, OK: false, Detail: "not a git repo (no .git): " + path}
	}
	return Result{Name: name, OK: true, Detail: path}
}

func projectCheckName(name string) string {
	if name == "" {
		return "project (unnamed)"
	}
	return "project " + name
}

// firstLine returns the first non-empty line of b, trimmed.
func firstLine(b []byte) string {
	s := bufio.NewScanner(strings.NewReader(string(b)))
	for s.Scan() {
		if line := strings.TrimSpace(s.Text()); line != "" {
			return line
		}
	}
	return ""
}

// firstErr returns the first line of a (possibly errors.Join'd) error.
func firstErr(err error) string {
	if err == nil {
		return ""
	}
	return firstLine([]byte(err.Error()))
}
