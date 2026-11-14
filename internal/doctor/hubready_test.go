package doctor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/secrets"
)

func stubGhAuth(t *testing.T, err error) {
	t.Helper()
	old := ghAuthStatus
	t.Cleanup(func() { ghAuthStatus = old })
	ghAuthStatus = func(context.Context) error { return err }
}

func rows(rs []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

// Not joined, nothing opted in, no key: every row says what to do, with a
// fix id, and nothing probes docker (no project needs the sandbox).
func TestHubReadinessFromScratch(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	stubSandbox(t, func(...string) error { t.Fatal("docker probed with no chores project"); return nil }, func(string) (string, string) { return "", "" })
	stubGhAuth(t, errors.New("exit status 1"))
	cfg := &config.Config{Hub: config.HubConfig{Enabled: true, URL: "https://hub.example.com"}, Projects: []config.Project{{Name: "web"}}}
	got := rows(HubReadiness(context.Background(), cfg))
	want := map[string]FixID{
		checkHubJoined:    FixJoinHub,
		checkHubChores:    FixHubChores,
		checkHubGh:        FixGhLogin,
		checkHubPolicyKey: FixPinPolicyKey,
	}
	for name, fix := range want {
		r, ok := got[name]
		if !ok || r.OK || r.Critical || r.Fix != fix {
			t.Errorf("%s = %+v (present %v), want a failed row with fix %q", name, r, ok, fix)
		}
	}
	if _, ok := got[checkHubDocker]; ok {
		t.Error("no docker row without a project that needs the sandbox")
	}
}

// A joined machine with one ready project and one without a repo: the list
// names both, reports docker/login, and never prints a credential or key.
func TestHubReadinessJoined(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	stubSandbox(t, func(...string) error { return nil }, func(name string) (string, string) {
		if name == "CLAUDE_CODE_OAUTH_TOKEN" {
			return "sk-ant-oat-SECRET", secrets.SourceKeychain
		}
		return "", ""
	})
	stubGhAuth(t, nil)
	dir, _ := hub.StateDir()
	key, err := hub.LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.SaveIdentity(dir, hub.Identity{URL: "https://hub.example.com", DeviceID: key.DeviceID(), Org: "acme", DeviceName: "mac", Member: "Ana"}); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := hubproto.GenerateKey()
	cfg := &config.Config{
		Hub: config.HubConfig{Enabled: true, URL: "https://hub.example.com", TrustedPolicyKeys: config.TrustedKeyMap{"k1": hubproto.EncodePublicKey(pub)}},
		Projects: []config.Project{
			{Name: "web", Repo: "acme/web", HubChores: true, Sandbox: config.SandboxDocker},
			{Name: "api", HubChores: true, Sandbox: config.SandboxDocker},
			{Name: "cli", Repo: "acme/cli", HubChores: true},
		},
	}
	list := HubReadiness(context.Background(), cfg)
	got := rows(list)
	if r := got[checkHubJoined]; !r.OK || !strings.Contains(r.Detail, "acme") || !strings.Contains(r.Detail, "Ana") {
		t.Errorf("joined = %+v", r)
	}
	ch := got[checkHubChores]
	if ch.OK || !strings.Contains(ch.Detail, "projects that take chores: web") || !strings.Contains(ch.Detail, "api has no GitHub repo") ||
		!strings.Contains(ch.Detail, "cli does not run its agent in the sandbox") || ch.Fix != FixSandboxProject {
		t.Errorf("chores = %+v", ch)
	}
	for _, n := range []string{checkHubDocker, checkHubImage, checkHubLogin, checkHubGh, checkHubPolicyKey} {
		if !got[n].OK {
			t.Errorf("%s = %+v", n, got[n])
		}
	}
	all := ""
	for _, r := range list {
		all += r.Detail + "\n"
	}
	if strings.Contains(all, "SECRET") || strings.Contains(all, hubproto.EncodePublicKey(pub)) {
		t.Fatalf("a secret or key value reached the list:\n%s", all)
	}
	// Revoked: the list says so and offers the rejoin.
	if _, err := hub.MarkRevoked(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r := rows(HubReadiness(context.Background(), cfg))[checkHubJoined]; r.OK || r.Fix != FixRejoinHub || !strings.Contains(r.Detail, "revoked") {
		t.Errorf("revoked = %+v", r)
	}
	if r, _ := hubResult(context.Background(), cfg); r.Fix != FixRejoinHub || !strings.Contains(r.Detail, "revoked") {
		t.Errorf("doctor hub row = %+v", r)
	}
}

func TestGhAuthAndProjectRows(t *testing.T) {
	if r := ghAuthResult(nil); !r.OK {
		t.Errorf("logged in = %+v", r)
	}
	if r := ghAuthResult(errors.New("exit status 1")); r.OK || r.Fix != FixGhLogin {
		t.Errorf("logged out = %+v", r)
	}
	if r := ghAuthResult(errGhMissing); r.OK || r.Fix != FixInstallGh {
		t.Errorf("missing = %+v", r)
	}
	if r, ok := projectsResult(&config.Config{}); !ok || r.OK || r.Fix != FixAddProject {
		t.Errorf("no projects = %+v", r)
	}
	if _, ok := pollingResult(&config.Config{}); ok {
		t.Error("no polling row without projects")
	}
	cfg := &config.Config{Projects: []config.Project{{Name: "web", TeamID: "t1"}, {Name: "api", Enabled: true}}}
	if r, ok := pollingResult(cfg); !ok || r.OK || r.Fix != FixEnablePoll {
		t.Errorf("nothing polls = %+v", r)
	}
	cfg.Projects[0].Enabled = true
	if r, _ := pollingResult(cfg); !r.OK || !strings.Contains(r.Detail, "web") {
		t.Errorf("web polls = %+v", r)
	}
}

// The doctor runs the readiness list (minus "hub joined") while the hub is
// enabled, and gh auth once.
func TestCheckIncludesHubReadiness(t *testing.T) {
	pathWith(t, "tmux", "git", "claude", "gh")
	t.Setenv("LOLA_HOME", t.TempDir())
	stubSandbox(t, func(...string) error { return nil }, func(string) (string, string) { return "", "" })
	calls := 0
	old := ghAuthStatus
	t.Cleanup(func() { ghAuthStatus = old })
	ghAuthStatus = func(context.Context) error { calls++; return errors.New("exit status 1") }
	oldProbe := hubProbe
	t.Cleanup(func() { hubProbe = oldProbe })
	hubProbe = nil
	cfg := &config.Config{Hub: config.HubConfig{Enabled: true, URL: "https://hub.example.com"}}
	r := Check(context.Background(), cfg)
	if calls != 1 {
		t.Errorf("gh auth status ran %d times, want 1", calls)
	}
	names := []string{}
	for _, res := range r.Results {
		names = append(names, res.Name)
	}
	for _, must := range []string{checkGhAuth, checkProjects, checkHubChores, checkHubGh, checkHubPolicyKey} {
		if !slices.Contains(names, must) {
			t.Errorf("no %q row in %v", must, names)
		}
	}
	if slices.Contains(names, checkHubJoined) {
		t.Error("the doctor's hub row already covers joined")
	}
}

func TestFixIDsAreDistinct(t *testing.T) {
	seen := map[FixID]bool{}
	for _, f := range AllFixIDs() {
		if f == FixNone || seen[f] {
			t.Errorf("fix id %q empty or listed twice", f)
		}
		seen[f] = true
	}
}

// The two sandbox credential rows have distinct names.
func TestSandboxAPIKeyRowHasItsOwnName(t *testing.T) {
	stubSandbox(t, func(...string) error { return nil }, func(string) (string, string) { return "", "" })
	cfg := sandboxedConfig()
	cfg.Sandbox.PassEnv = []string{config.SandboxAPIKeyEnv}
	got := sandboxResults(context.Background(), cfg)
	n := 0
	for _, r := range got {
		if r.Name == checkSandboxClaude {
			n++
		}
	}
	if n != 1 || !slices.ContainsFunc(got, func(r Result) bool { return r.Name == checkSandboxAPIKey }) {
		t.Errorf("rows = %+v", got)
	}
}
