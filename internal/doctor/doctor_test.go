package doctor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// fakeBin writes an executable shell script named tool into dir that prints
// body to stdout and exits 0.
func fakeBin(t *testing.T, dir, tool, body string) {
	t.Helper()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// pathWith builds a temp dir, installs each named tool as a trivial fake, sets
// PATH to ONLY that dir (so nothing leaks from the host), and returns the dir.
// claude gets a --version-aware script; the rest just exit 0.
func pathWith(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		if tool == "claude" {
			fakeBin(t, dir, "claude", `echo "1.2.3 (Claude Code)"`)
			continue
		}
		fakeBin(t, dir, tool, "exit 0")
	}
	t.Setenv("PATH", dir)
	return dir
}

// result fetches the named result from a report.
func result(t *testing.T, r Report, name string) Result {
	t.Helper()
	for _, res := range r.Results {
		if res.Name == name {
			return res
		}
	}
	t.Fatalf("no result named %q in %+v", name, r.Results)
	return Result{}
}

func TestCheckAllToolsMissing(t *testing.T) {
	pathWith(t) // empty PATH: no tools resolve
	t.Setenv("LOLA_HOME", t.TempDir())

	r := Check(context.Background(), nil)

	for _, name := range []string{checkTmux, checkGit, checkClaude} {
		res := result(t, r, name)
		if res.OK {
			t.Errorf("%s: OK=true, want false (not on PATH)", name)
		}
		if !res.Critical {
			t.Errorf("%s: Critical=false, want true", name)
		}
		if res.Detail != "not found on PATH" {
			t.Errorf("%s: Detail=%q, want %q", name, res.Detail, "not found on PATH")
		}
	}

	gh := result(t, r, checkGh)
	if gh.OK || gh.Critical {
		t.Errorf("gh: OK=%v Critical=%v, want false/false", gh.OK, gh.Critical)
	}

	if r.OK() {
		t.Error("Report.OK()=true with critical tools missing, want false")
	}
}

func TestCheckToolsPresent(t *testing.T) {
	dir := pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())

	r := Check(context.Background(), nil)

	for _, name := range []string{checkTmux, checkGit} {
		res := result(t, r, name)
		if !res.OK {
			t.Errorf("%s: OK=false, want true", name)
		}
		if res.Detail != filepath.Join(dir, name) {
			t.Errorf("%s: Detail=%q, want resolved path %q", name, res.Detail, filepath.Join(dir, name))
		}
	}

	claude := result(t, r, checkClaude)
	if !claude.OK {
		t.Fatalf("claude: OK=false, want true")
	}
	if !strings.Contains(claude.Detail, "1.2.3 (Claude Code)") {
		t.Errorf("claude Detail=%q, want it to include the --version first line", claude.Detail)
	}

	gh := result(t, r, checkGh)
	if !gh.OK {
		t.Errorf("gh: OK=false, want true")
	}
}

// nil cfg: config-dependent checks are skipped with one note, and no
// linear/project results are emitted.
func TestCheckNilConfig(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())

	r := Check(context.Background(), nil)

	cfg := result(t, r, checkConfig)
	if cfg.OK || cfg.Critical {
		t.Errorf("config note: OK=%v Critical=%v, want false/false (skipped, non-critical)", cfg.OK, cfg.Critical)
	}
	if !strings.Contains(cfg.Detail, "config not loaded") {
		t.Errorf("config note Detail=%q, want it to explain config not loaded", cfg.Detail)
	}
	for _, res := range r.Results {
		if res.Name == checkLinear {
			t.Error("linear check ran with nil cfg, want it skipped")
		}
		if strings.HasPrefix(res.Name, "project ") {
			t.Error("project check ran with nil cfg, want it skipped")
		}
	}
	// All present tools + skipped-but-non-critical config note ⇒ report OK.
	if !r.OK() {
		t.Error("Report.OK()=false with all tools present and cfg nil, want true")
	}
}

func TestLinearKeyFoundInEnvNeverPrinted(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())
	const secret = "lin_api_SUPERSECRET_should_never_render"
	t.Setenv("LINEAR_API_KEY", secret)

	cfg := &config.Config{
		Defaults: config.Defaults{GlobalCap: 1},
		Linear:   config.LinearConfig{APIKeyEnv: "LINEAR_API_KEY"},
	}
	r := Check(context.Background(), cfg)

	lin := result(t, r, checkLinear)
	if !lin.OK {
		t.Fatalf("linear: OK=false, want true (key in env)")
	}
	if !lin.Critical {
		t.Error("linear: Critical=false, want true (a key source is configured)")
	}
	if lin.Detail != "found in env LINEAR_API_KEY" {
		t.Errorf("linear Detail=%q, want %q", lin.Detail, "found in env LINEAR_API_KEY")
	}
	// The key value must not appear anywhere in the rendered report.
	assertNoSecret(t, r, secret)
}

func TestLinearKeyNotFound(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())
	// Ensure the configured env var is unset.
	os.Unsetenv("LINEAR_API_KEY")

	cfg := &config.Config{
		Defaults: config.Defaults{GlobalCap: 1},
		Linear:   config.LinearConfig{APIKeyEnv: "LINEAR_API_KEY"},
	}
	r := Check(context.Background(), cfg)

	lin := result(t, r, checkLinear)
	if lin.OK {
		t.Error("linear: OK=true, want false (no key)")
	}
	if !lin.Critical {
		t.Error("linear: Critical=false, want true (a key source is configured)")
	}
	if !strings.Contains(lin.Detail, "not found") {
		t.Errorf("linear Detail=%q, want it to report not found", lin.Detail)
	}
}

// No key source configured at all: not critical (nothing to resolve).
func TestLinearNoSourceNotCritical(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())

	cfg := &config.Config{Defaults: config.Defaults{GlobalCap: 1}}
	r := Check(context.Background(), cfg)

	lin := result(t, r, checkLinear)
	if lin.OK {
		t.Error("linear: OK=true, want false (no source)")
	}
	if lin.Critical {
		t.Error("linear: Critical=true, want false (no key source configured)")
	}
}

func TestConfigValidAndInvalid(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())

	valid := &config.Config{Defaults: config.Defaults{GlobalCap: 1}}
	r := Check(context.Background(), valid)
	if cfg := result(t, r, checkConfig); !cfg.OK || cfg.Detail != "valid" {
		t.Errorf("valid config: %+v, want OK/valid", cfg)
	}

	invalid := &config.Config{} // GlobalCap=0 fails Validate
	r = Check(context.Background(), invalid)
	cfg := result(t, r, checkConfig)
	if cfg.OK {
		t.Error("invalid config: OK=true, want false")
	}
	if !cfg.Critical {
		t.Error("invalid config: Critical=false, want true")
	}
	if cfg.Detail == "" || cfg.Detail == "valid" {
		t.Errorf("invalid config Detail=%q, want first validation error", cfg.Detail)
	}
	if r.OK() {
		t.Error("Report.OK()=true with invalid config, want false")
	}
}

func TestProjectResults(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())

	// A real git repo (path exists + .git present).
	good := t.TempDir()
	if err := os.Mkdir(filepath.Join(good, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A dir without .git.
	notRepo := t.TempDir()

	cfg := &config.Config{
		Defaults: config.Defaults{GlobalCap: 1},
		Projects: []config.Project{
			{Name: "ok", Path: good},
			{Name: "norepo", Path: notRepo},
			{Name: "missing", Path: filepath.Join(good, "nope")},
		},
	}
	r := Check(context.Background(), cfg)

	ok := result(t, r, "project ok")
	if !ok.OK {
		t.Errorf("project ok: OK=false (%s), want true", ok.Detail)
	}
	if ok.Critical {
		t.Error("project ok: Critical=true, want false (per-project checks are warnings)")
	}

	nr := result(t, r, "project norepo")
	if nr.OK || !strings.Contains(nr.Detail, "not a git repo") {
		t.Errorf("project norepo: %+v, want failure mentioning git repo", nr)
	}

	miss := result(t, r, "project missing")
	if miss.OK || !strings.Contains(miss.Detail, "does not exist") {
		t.Errorf("project missing: %+v, want path-does-not-exist", miss)
	}

	// Per-project failures are warnings: with a valid config and tools present,
	// the report is still OK() (no critical failure).
	if !r.OK() {
		t.Error("Report.OK()=false on per-project warnings only, want true")
	}
}

func TestDaemonSocketDown(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())

	r := Check(context.Background(), &config.Config{Defaults: config.Defaults{GlobalCap: 1}})
	d := result(t, r, checkDaemon)
	if d.OK {
		t.Error("daemon: OK=true with no socket, want false")
	}
	if d.Critical {
		t.Error("daemon: Critical=true, want false (doctor must work with daemon down)")
	}
	if !strings.Contains(d.Detail, "not running") {
		t.Errorf("daemon Detail=%q, want 'not running' hint", d.Detail)
	}
}

func TestDaemonSocketUp(t *testing.T) {
	home := t.TempDir()
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", home)

	ln, err := net.Listen("unix", filepath.Join(home, "lola.sock"))
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox forbids unix socket bind")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	r := Check(context.Background(), &config.Config{Defaults: config.Defaults{GlobalCap: 1}})
	d := result(t, r, checkDaemon)
	if !d.OK {
		t.Errorf("daemon: OK=false with socket up (%s), want true", d.Detail)
	}
}

func TestSummaryCounts(t *testing.T) {
	r := Report{Results: []Result{
		{Name: "a", OK: true},
		{Name: "b", OK: true},
		{Name: "c", OK: false, Critical: false}, // warning
		{Name: "d", OK: false, Critical: true},  // critical
		{Name: "e", OK: false, Critical: true},  // critical
	}}
	if got := r.Summary(); got != "2 ok, 1 warning, 2 critical" {
		t.Errorf("Summary()=%q, want %q", got, "2 ok, 1 warning, 2 critical")
	}
	if r.OK() {
		t.Error("OK()=true with critical failures, want false")
	}
}

func TestRuntimeResultsSubset(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())

	r := Check(context.Background(), nil)
	sub := RuntimeResults(r)
	if len(sub) != 3 {
		t.Fatalf("RuntimeResults returned %d results, want 3 (tmux/git/claude)", len(sub))
	}
	want := map[string]bool{checkTmux: true, checkGit: true, checkClaude: true}
	for _, res := range sub {
		if !want[res.Name] {
			t.Errorf("RuntimeResults included %q, not a runtime tool", res.Name)
		}
		if !res.Critical {
			t.Errorf("%s in RuntimeResults not Critical, want true", res.Name)
		}
	}
}

// The migration check is a NON-critical warning when lola sessions linger on
// the user's default tmux server, and OK otherwise. The default-server scan is
// seamed so no real tmux is touched.
func TestMigrationCheckFlagsOrphans(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())

	prev := defaultServerSessions
	t.Cleanup(func() { defaultServerSessions = prev })

	// Orphans present → warning (not critical), naming the sessions + cleanup hint.
	defaultServerSessions = func(context.Context, string, string) ([]string, error) {
		return []string{"lola-web-eng-1", "lola-api-eng-2"}, nil
	}
	r := Check(context.Background(), nil)
	mig := result(t, r, checkMigration)
	if mig.OK {
		t.Error("migration: OK=true with orphans present, want false (warning)")
	}
	if mig.Critical {
		t.Error("migration: Critical=true, want false (a non-critical warning)")
	}
	for _, want := range []string{"lola-web-eng-1", "lola-api-eng-2", "tmux kill-session"} {
		if !strings.Contains(mig.Detail, want) {
			t.Errorf("migration Detail=%q, want it to include %q", mig.Detail, want)
		}
	}
	// A warning must not make the whole report fail.
	if !r.OK() {
		t.Error("Report.OK()=false on a migration warning only, want true")
	}

	// No orphans → OK.
	defaultServerSessions = func(context.Context, string, string) ([]string, error) {
		return nil, nil
	}
	r = Check(context.Background(), nil)
	if mig := result(t, r, checkMigration); !mig.OK {
		t.Errorf("migration: OK=false with no orphans, want true (%s)", mig.Detail)
	}

	// A tmux error (no default server) is the healthy case → OK, best-effort.
	defaultServerSessions = func(context.Context, string, string) ([]string, error) {
		return nil, errors.New("tmux list-sessions: exit 1")
	}
	r = Check(context.Background(), nil)
	if mig := result(t, r, checkMigration); !mig.OK {
		t.Errorf("migration: OK=false on a best-effort tmux error, want true (%s)", mig.Detail)
	}
}

// assertNoSecret fails if secret appears in any rendered field of the report.
func assertNoSecret(t *testing.T, r Report, secret string) {
	t.Helper()
	for _, res := range r.Results {
		if strings.Contains(res.Detail, secret) {
			t.Fatalf("secret leaked into %s Detail: %q", res.Name, res.Detail)
		}
		if strings.Contains(res.Name, secret) {
			t.Fatalf("secret leaked into a Result.Name")
		}
	}
	if strings.Contains(r.Summary(), secret) {
		t.Fatal("secret leaked into Summary()")
	}
}

// A repaired config is reported as a WARNING, not a critical failure: it works,
// but it no longer says what the user wrote.
func TestRepairsReportedAsWarning(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[defaults]\nglobal_cap = 4\nconcurrency_cap = 2\npriority_sort = [\"urgent\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	rep := Check(context.Background(), cfg)
	var found *Result
	for i := range rep.Results {
		if rep.Results[i].Name == checkRepaired {
			found = &rep.Results[i]
		}
	}
	if found == nil {
		t.Fatal("a repaired config must be reported")
	}
	if found.Critical {
		t.Error("a repair is a warning, not critical — the config works")
	}
	if !strings.Contains(found.Detail, "urgent") {
		t.Errorf("the detail must name what was dropped, got %q", found.Detail)
	}
}

// A clean config adds no repair result at all.
func TestNoRepairResultWhenClean(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	cfg := &config.Config{Defaults: config.Defaults{GlobalCap: 4, ConcurrencyCap: 1}}
	for _, res := range Check(context.Background(), cfg).Results {
		if res.Name == checkRepaired {
			t.Fatalf("clean config must add no repair result, got %+v", res)
		}
	}
}
