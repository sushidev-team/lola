package main

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/doctor"
)

const hubProjectsConfig = minimalConfig +
	"[[project]]\nname = \"web\"\npath = \"/tmp/web\"\nrepo = \"acme/web\"\nteam_id = \"t\"\ncycle_mode = \"none\"\nassignee_mode = \"anyone\"\n" +
	"[[project]]\nname = \"api\"\npath = \"/tmp/api\"\nteam_id = \"t\"\ncycle_mode = \"none\"\nassignee_mode = \"anyone\"\n"

func stubInspectRepo(t *testing.T, repos map[string]string) {
	t.Helper()
	prev := inspectRepo
	inspectRepo = func(_ context.Context, dir string) string { return repos[dir] }
	t.Cleanup(func() { inspectRepo = prev })
}

func TestHubProjectsListsReposAndSuggestions(t *testing.T) {
	writeTestConfig(t, hubProjectsConfig)
	stubInspectRepo(t, map[string]string{"/tmp/web": "acme/web", "/tmp/api": "acme/api"})
	got, err := (&ConfigService{}).HubProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "web" || got[0].Repo != "acme/web" || got[0].SuggestedRepo != "" {
		t.Fatalf("web: %+v", got)
	}
	if got[1].Repo != "" || got[1].SuggestedRepo != "acme/api" || got[1].HubChores || got[1].Sandboxed {
		t.Fatalf("api: %+v", got[1])
	}
}

func TestSaveHubProject(t *testing.T) {
	path := writeTestConfig(t, hubProjectsConfig)
	stubInspectRepo(t, nil)
	s := &ConfigService{}
	on, off := true, false
	// Chores need a repo first; nothing is written.
	if err := s.SaveHubProject(HubProjectPatchDTO{Name: "api", HubChores: &on}); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("no repo: %v", err)
	}
	bad := "not a slug"
	if err := s.SaveHubProject(HubProjectPatchDTO{Name: "api", Repo: &bad}); err == nil {
		t.Fatal("accepted a bad repo")
	}
	repo := " acme/api "
	if err := s.SaveHubProject(HubProjectPatchDTO{Name: "api", Repo: &repo, HubChores: &on, Sandboxed: &on}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.ProjectByName("api")
	if p.Repo != "acme/api" || !p.HubChores || !p.Sandboxed() {
		t.Fatalf("saved: repo=%q chores=%v sandbox=%q", p.Repo, p.HubChores, p.Sandbox)
	}
	if err := s.SaveHubProject(HubProjectPatchDTO{Name: "api", Sandboxed: &off}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(path)
	if cfg.ProjectByName("api").Sandboxed() || !cfg.ProjectByName("api").HubChores {
		t.Fatal("sandbox off changed more than the sandbox")
	}
	if err := s.SaveHubProject(HubProjectPatchDTO{Name: "nope", HubChores: &on}); err == nil {
		t.Fatal("unknown project accepted")
	}
}

func TestHubReadinessRows(t *testing.T) {
	writeTestConfig(t, hubProjectsConfig)
	prev := hubReadinessProbe
	t.Cleanup(func() { hubReadinessProbe = prev })
	hubReadinessProbe = func(_ context.Context, cfg *config.Config) []doctor.Result {
		return []doctor.Result{
			{Name: "hub chores", Detail: "no project takes chores", Fix: doctor.FixHubChores},
			{Name: "hub github access", OK: true, Detail: "gh is logged in"},
		}
	}
	got, err := (&ConfigService{}).HubReadiness()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Fix != "hub_chores" || got[0].OK || !got[1].OK || got[1].Fix != "" {
		t.Fatalf("rows = %+v", got)
	}
}

const testPolicyPub = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestPinPolicyKey(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	if fp := s.PolicyKeyFingerprint(testPolicyPub); len(fp) != 19 {
		t.Fatalf("fingerprint %q", fp)
	}
	if s.PolicyKeyFingerprint("short") != "" {
		t.Fatal("fingerprint of a malformed key")
	}
	if _, err := s.PinPolicyKey("bad id!", testPolicyPub); err == nil {
		t.Fatal("pinned a malformed key id")
	}
	added, err := s.PinPolicyKey("kadmin1", testPolicyPub)
	if err != nil || !added {
		t.Fatalf("pin: %v %v", added, err)
	}
	if added, err := s.PinPolicyKey("kadmin1", testPolicyPub); err != nil || added {
		t.Fatalf("re-pin: %v %v", added, err)
	}
	// A different key under the same id is never replaced.
	other := "B" + testPolicyPub[1:]
	if _, err := s.PinPolicyKey("kadmin1", other); err == nil {
		t.Fatal("replaced a pinned key")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hub.TrustedPolicyKeys["kadmin1"] != testPolicyPub {
		t.Fatalf("keys = %v", cfg.Hub.TrustedPolicyKeys)
	}
}

func TestSaveHubRulesLeavesNilFields(t *testing.T) {
	path := writeTestConfig(t, minimalConfig+"[hub]\nmax_claims_per_day = 2\npoll_seconds = 600\n")
	s := &ConfigService{}
	off, claims := false, 7
	if err := s.SaveHubRules(HubRulesDTO{RequireSandbox: &off, MaxClaimsPerDay: &claims,
		TrustedKeys: []HubTrustedKeyDTO{{KeyID: "kadmin1", PublicKey: testPolicyPub}}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hub
	if *h.MaxClaimsPerDay != 7 || h.SandboxRequired() || h.PollSeconds != 600 || h.TrustedPolicyKeys["kadmin1"] != testPolicyPub {
		t.Fatalf("hub = %+v", h)
	}
	tooSlow := 5000
	if err := s.SaveHubRules(HubRulesDTO{PollSeconds: &tooSlow}); err == nil {
		t.Fatal("accepted a poll past the maximum")
	}
}
