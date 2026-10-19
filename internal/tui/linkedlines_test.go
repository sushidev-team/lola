package tui

import (
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

func TestLinkedLinesRoundTrip(t *testing.T) {
	repos := []config.LinkedRepo{
		{Name: "website", Path: "/src/web", Repo: "acme/web", Labels: []string{"apps/website", "Needs QA"}, DefaultBranch: "develop"},
		{Name: "api", Path: "/src/api"},
		{Name: "docs", Path: "/src/docs", Labels: []string{"docs"}},
	}
	lines := linkedLines(repos)
	if lines[1] != "api | /src/api" || lines[2] != "docs | /src/docs |  | docs" {
		t.Fatalf("lines = %q", lines)
	}
	got, err := parseLinkedLines(append(lines, "  "))
	if err != nil || !reflect.DeepEqual(got, repos) {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	for _, bad := range []string{"website", " | /src/x", "a | b | c | d | e | f"} {
		if _, err := parseLinkedLines([]string{bad}); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

// The project form edits [[project.linked_repo]] (labels included) and saves it.
func TestFormSavesLinkedRepos(t *testing.T) {
	f, path := newFormOn(t, []config.Project{{
		Name: "nori", Path: "/tmp/nori", Repo: "acme/nori",
		LinkedRepos: []config.LinkedRepo{{Name: "api", Path: "/tmp/api"}},
	}}, "nori")
	if !reflect.DeepEqual(f.linkedRepos, []string{"api | /tmp/api"}) {
		t.Fatalf("loaded lines = %q", f.linkedRepos)
	}
	f.linkedRepos = []string{"website | /tmp/web | acme/web | apps/website", "api | /tmp/api"}
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []config.LinkedRepo{{Name: "website", Path: "/tmp/web", Repo: "acme/web", Labels: []string{"apps/website"}}, {Name: "api", Path: "/tmp/api"}}
	if p := got.ProjectByName("nori"); !reflect.DeepEqual(p.LinkedRepos, want) {
		t.Fatalf("saved linked repos = %+v", p.LinkedRepos)
	}

	f.linkedRepos = []string{"website"}
	if _, ev := f.save(); ev == formSaved {
		t.Fatal("a line without a path must not save")
	}
}
