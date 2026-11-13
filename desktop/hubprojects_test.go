package main

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
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
