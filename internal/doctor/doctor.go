// Package doctor runs lola's structured health checks and returns them as
// plain data so the CLI (`lola doctor`) and the TUI can render them however
// they like — this package never prints. It probes the native runtime's
// external tools (tmux, git, the configured agents, gh), the Linear API key, the daemon
// socket, and the config (validity + per-project repos) — plus the one fact a
// checking process cannot see for itself, the daemon's tripped status
// interpreter (statusagent.go).
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
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/secrets"
	"github.com/sushidev-team/lola/internal/tmux"
)

// Check names. Stable strings so renderers (and RuntimeResults) can key off
// them; per-project checks use projectCheckName.
const (
	checkTmux      = "tmux"
	checkGit       = "git"
	checkClaude    = "claude"
	checkGh        = "gh"
	checkLolaCLI   = "lola cli"
	checkLinear    = "linear api key"
	checkDaemon    = "daemon"
	checkConfig    = "config"
	checkRepaired  = "config repairs"
	checkMigration = "migration"
	checkFallback  = "agent fallback"
	// checkStatusAgent is reported ONLY when the daemon's status interpreter
	// has tripped its circuit breaker (statusagent.go) — there is no healthy
	// row for it.
	checkStatusAgent = "status interpreter"
)

// defaultServerSessions is the seam over tmux.DefaultServerSessions so the
// migration check is testable without a real default tmux server.
var defaultServerSessions = tmux.DefaultServerSessions

// execTimeout bounds each agent version probe; LookPath probes never exec.
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
// mandatory tools (tmux, git, configured coding agents), matching the tools
// daemon.checkRuntimeHealth gates spawning on. Renderers (and, later, the daemon) can reuse this to show
// "why can't lola spawn" without re-probing.
func RuntimeResults(r Report) []Result {
	var out []Result
	for _, res := range r.Results {
		switch res.Name {
		case checkTmux, checkGit, checkClaude, "codex", "opencode":
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

	// Native runtime tools. Each configured coding agent gets a bounded
	// version probe. Optional helpers have separate non-critical checks.
	add(lookPathResult(checkTmux, true))
	add(lookPathResult(checkGit, true))
	for _, res := range agentResults(ctx, cfg) {
		add(res)
	}
	for _, res := range helperResults(cfg) {
		add(res)
	}
	add(ghResult())
	add(lolaCLIResult())
	add(migrationResult(ctx))
	// Daemon-process runtime facts the checking process cannot see for itself,
	// read from the breadcrumb the daemon leaves (statusagent.go). Independent
	// of cfg, so it sits above the cfg == nil early return.
	if res, ok := statusAgentResult(); ok {
		add(res)
	}

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
	if res, ok := repairResult(cfg); ok {
		add(res)
	}
	for _, res := range fallbackResults(cfg) {
		add(res)
	}
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

// lolaCLIResult reports whether the `lola` CLI is on PATH. NOT critical, and
// deliberately so: the daemon running this check IS lola, and the desktop app
// falls back to the copy bundled in its own .app — so a miss costs the shell
// (`lola tui`, every subcommand), not the runtime.
//
// It exists because that miss used to be invisible. A DMG-only install ships no
// CLI, and the app's first-run wizard then failed to start a daemon with an
// error naming a lookup rather than a fix, while the doctor said nothing at all.
func lolaCLIResult() Result {
	path, err := exec.LookPath("lola")
	if err != nil {
		return Result{
			Name:     checkLolaCLI,
			OK:       false,
			Critical: false,
			Detail:   "not found on PATH — `lola` and `lola tui` are unavailable in a terminal (the desktop app uses its bundled copy)",
		}
	}
	return Result{Name: checkLolaCLI, OK: true, Critical: false, Detail: path}
}

// agentResults checks the default worker and each project's effective worker,
// once per binary. A config that has not loaded retains the legacy default.
func agentResults(ctx context.Context, cfg *config.Config) []Result {
	kinds := []agent.Kind{agent.Claude}
	if cfg != nil {
		kinds[0] = agent.Parse(cfg.Defaults.Agent)
		for _, p := range cfg.Projects {
			k := agent.Parse(cfg.AgentForProject(p.Name))
			if !slices.Contains(kinds, k) {
				kinds = append(kinds, k)
			}
		}
	}
	out := make([]Result, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, agentResult(ctx, k))
	}
	return out
}

// agentResult appends a bounded --version probe. A version-exec failure does
// not fail the check: dispatch requires presence on PATH.
func agentResult(ctx context.Context, k agent.Kind) Result {
	bin := k.Binary()
	res := lookPathResult(bin, true)
	if !res.OK {
		return res
	}
	if k == agent.Codex {
		if err := CheckCodexAutoApproval(ctx, bin); err != nil {
			res.OK = false
			res.Detail = err.Error()
			return res
		}
	}
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "--version").Output()
	if err != nil {
		res.Detail += " (version unavailable)"
		return res
	}
	if line := firstLine(out); line != "" {
		res.Detail += " (" + line + ")"
	}
	return res
}

// CheckCodexAutoApproval verifies the installed CLI supports Lola's autonomous
// launch mode. A failed probe must not fall back to bypassing approvals.
func CheckCodexAutoApproval(ctx context.Context, binary string) error {
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, binary, "--help").Output()
	if err != nil {
		return fmt.Errorf("cannot verify Codex auto-approval support: %w", err)
	}
	if !strings.Contains(string(out), "--approve-for-me") {
		return fmt.Errorf("Codex does not support --approve-for-me; upgrade Codex CLI before starting autonomous sessions")
	}
	return nil
}

// helperResults keeps optional Claude helpers and independently configured
// review agents visible without making them prerequisites for worker dispatch.
func helperResults(cfg *config.Config) []Result {
	if cfg == nil {
		return nil
	}
	var out []Result
	add := func(name, binary string) {
		res := lookPathResult(binary, false)
		res.Name = name
		res.Detail = binary + ": " + res.Detail
		out = append(out, res)
	}
	if cfg.Brain.Enabled {
		add("brain agent", "claude")
	}
	if cfg.StatusAgent.Enabled {
		binary := cfg.StatusAgent.Bin
		if binary == "" {
			binary = agent.Parse(cfg.StatusAgent.Agent).Binary()
		}
		add("status agent", binary)
	}
	for _, p := range cfg.EffectiveReviewProviders() {
		if k, ok := config.ReviewAgentFor(string(p.Provider)); p.Enabled && ok {
			add("review agent ("+string(p.Provider)+")", agent.Parse(k).Binary())
		}
	}
	return out
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

// migrationResult flags lola sessions still running on the user's DEFAULT tmux
// server from before the "-L lola" isolation change: they are orphaned,
// invisible to the daemon, and `lola kill` cannot reach them. A NON-critical
// warning (lola runs fine; these are just leftovers to clean up manually).
// Best-effort — a tmux error (no default server, tmux missing) is the common
// healthy case and reports OK, not a warning.
func migrationResult(ctx context.Context) Result {
	names, err := defaultServerSessions(ctx, "tmux", tmux.OrphanSessionPrefix)
	if err != nil || len(names) == 0 {
		return Result{Name: checkMigration, OK: true, Critical: false, Detail: "no pre-migration sessions on the default tmux server"}
	}
	return Result{
		Name:     checkMigration,
		OK:       false,
		Critical: false,
		Detail: fmt.Sprintf("%d lola session(s) on the DEFAULT tmux server (invisible to lola): %s — stop with: tmux kill-session -t <name>",
			len(names), strings.Join(names, ", ")),
	}
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

// repairResult reports the non-fatal repairs Load made to config.toml — values
// that were already inert and would otherwise have hard-blocked the daemon (see
// Config.Notices). NOT critical: the config works, but the user should know it
// no longer says what they wrote. Nothing to report on a clean config.
func repairResult(cfg *config.Config) (Result, bool) {
	n := cfg.Notices()
	if len(n) == 0 {
		return Result{}, false
	}
	return Result{
		Name:     checkRepaired,
		OK:       false,
		Critical: false,
		Detail:   strings.Join(n, "; ") + " — save from the settings editor to write the cleaned value back",
	}, true
}

// fallbackResults checks that binaries for configured agent fallback chains
// resolve on PATH. Missing binaries are reported as non-critical warnings so
// the operator is alerted before a quota limit triggers a failed fallback.
// When no fallback chains are configured (or all resolve), no warning results
// are added.
func fallbackResults(cfg *config.Config) []Result {
	if cfg == nil {
		return nil
	}

	var out []Result
	checkChain := func(name string, chain []string) {
		var seen []string
		for _, entry := range chain {
			k := agent.Parse(entry)
			bin := k.Binary()
			if slices.Contains(seen, bin) {
				continue
			}
			seen = append(seen, bin)
			if _, err := exec.LookPath(bin); err != nil {
				out = append(out, Result{
					Name:     name,
					OK:       false,
					Critical: false,
					Detail:   fmt.Sprintf("binary %q not found on PATH", bin),
				})
			}
		}
	}

	// 1. Check [defaults].agent_fallback when set.
	if len(cfg.Defaults.AgentFallback) > 0 {
		checkChain(checkFallback, cfg.Defaults.AgentFallback)
	}

	// 2. Check per-project overrides.
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.AgentFallback != nil && !p.Inherits.AgentFallback {
			name := fallbackCheckName(p.Name)
			checkChain(name, p.AgentFallback)
		}
	}
	return out
}

func fallbackCheckName(name string) string {
	if name == "" {
		return checkFallback + " (unnamed project)"
	}
	return fmt.Sprintf("%s (%s)", checkFallback, name)
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
