package hub

import (
	"testing"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

func advisoryItem(repo string) hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
		Repo: repo, Ecosystem: "npm", Package: "lodash", Vulnerable: "< 4.17.21", Fixed: "4.17.21",
		Advisory: "GHSA-jf85-cpcp-j695", Severity: hubproto.SeverityHigh,
	}}
}

func bumpItem(repo string) hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
		Repo: repo, PR: 42, Branch: "dependabot/npm_and_yarn/lodash-4.17.21", Package: "lodash", Author: hubproto.AuthorDependabot,
	}}
}

func TestAccept(t *testing.T) {
	f := false
	zero := 0
	sandboxed := config.Project{Name: "web", Repo: "Acme/Web", HubChores: true, Sandbox: config.SandboxDocker}
	cases := []struct {
		name   string
		item   hubproto.Item
		p      *config.Project
		h      config.HubConfig
		claims int
		want   string
	}{
		{"ok", advisoryItem("acme/web"), &sandboxed, config.HubConfig{Enabled: true}, 0, ""},
		{"ok bump", bumpItem("acme/web"), &sandboxed, config.HubConfig{Enabled: true}, 3, ""},
		{"invalid item", hubproto.Item{Kind: "shell"}, &sandboxed, config.HubConfig{Enabled: true}, 0, ReasonInvalidItem},
		{"disabled", advisoryItem("acme/web"), &sandboxed, config.HubConfig{}, 0, ReasonHubDisabled},
		{"no project", advisoryItem("acme/web"), nil, config.HubConfig{Enabled: true}, 0, ReasonNoProject},
		{"not opted in", advisoryItem("acme/web"), &config.Project{Repo: "acme/web", Sandbox: "docker"}, config.HubConfig{Enabled: true}, 0, ReasonNotOptedIn},
		{"stack", advisoryItem("acme/web"), &config.Project{Repo: "acme/web", HubChores: true, Kind: config.ProjectKindStack}, config.HubConfig{Enabled: true}, 0, ReasonStack},
		{"repo mismatch", advisoryItem("acme/api"), &sandboxed, config.HubConfig{Enabled: true}, 0, ReasonRepoMismatch},
		{"kind rejected", bumpItem("acme/web"), &sandboxed, config.HubConfig{Enabled: true, AcceptKinds: []string{"dep_advisory"}}, 0, ReasonKindRejected},
		{"cap", advisoryItem("acme/web"), &sandboxed, config.HubConfig{Enabled: true}, 4, ReasonDailyCap},
		{"cap zero", advisoryItem("acme/web"), &sandboxed, config.HubConfig{Enabled: true, MaxClaimsPerDay: &zero}, 0, ReasonDailyCap},
		{"needs sandbox", advisoryItem("acme/web"), &config.Project{Repo: "acme/web", HubChores: true}, config.HubConfig{Enabled: true}, 0, ReasonNeedsSandbox},
		{"sandbox opt-out", advisoryItem("acme/web"), &config.Project{Repo: "acme/web", HubChores: true}, config.HubConfig{Enabled: true, RequireSandbox: &f}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := Accept(tc.item, tc.p, tc.h, tc.claims)
			if ok != (tc.want == "") || reason != tc.want {
				t.Fatalf("Accept = %v %q, want %q", ok, reason, tc.want)
			}
		})
	}
}

func TestProjectForAndOptedInRepos(t *testing.T) {
	cfg := &config.Config{Projects: []config.Project{
		{Name: "a", Repo: "acme/web"},
		{Name: "b", Repo: "ACME/web", HubChores: true},
		{Name: "c", Repo: "acme/web", HubChores: true},
		{Name: "d", Repo: "acme/api", HubChores: true},
		{Name: "e", Repo: "", HubChores: true},
		{Name: "s", Repo: "acme/stack", HubChores: true, Kind: config.ProjectKindStack},
	}}
	if p := ProjectFor(cfg, advisoryItem("acme/web")); p == nil || p.Name != "b" {
		t.Fatalf("ProjectFor = %+v, want b", p)
	}
	if p := ProjectFor(cfg, advisoryItem("acme/stack")); p != nil {
		t.Fatalf("a stack must never match: %+v", p)
	}
	if p := ProjectFor(cfg, advisoryItem("acme/none")); p != nil {
		t.Fatal("unmatched repo must give nil")
	}
	got := OptedInRepos(cfg)
	if len(got) != 2 || got[0] != "ACME/web" || got[1] != "acme/api" {
		t.Fatalf("OptedInRepos = %v", got)
	}
}
