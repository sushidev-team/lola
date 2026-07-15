package tui

import (
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// The project editor writes its list/env fields back into the project and
// persists them, splitting one-entry-per-line.
func TestProjectFormSave(t *testing.T) {
	m := newTestRoot(t) // config has a project named "nori-app"
	f, ok := newProjectForm(m.cfgPath, m.cfg, "nori-app")
	if !ok {
		t.Fatal("newProjectForm: project not found")
	}
	f.fields[3].buf = ".env\n storage \n" // symlinks (trims, drops blank)
	f.fields[4].buf = "composer install\nnpm ci"
	f.fields[5].buf = "APP_ENV=local\nDEBUG = 1\nnope"

	if ev := f.save(); ev != projFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var p *config.Project
	for i := range reloaded.Projects {
		if reloaded.Projects[i].Name == "nori-app" {
			p = &reloaded.Projects[i]
		}
	}
	if p == nil {
		t.Fatal("project gone after save")
	}
	if !reflect.DeepEqual(p.Symlinks, []string{".env", "storage"}) {
		t.Errorf("symlinks = %v", p.Symlinks)
	}
	if !reflect.DeepEqual(p.PostCreate, []string{"composer install", "npm ci"}) {
		t.Errorf("post_create = %v", p.PostCreate)
	}
	if p.Env["APP_ENV"] != "local" || p.Env["DEBUG"] != "1" {
		t.Errorf("env = %v", p.Env)
	}
	if _, bad := p.Env["nope"]; bad {
		t.Error("a line without '=' must be ignored")
	}
}

func TestSplitLinesTrimAndParseEnv(t *testing.T) {
	if got := splitLinesTrim("  a \n\n b \n"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("splitLinesTrim = %v", got)
	}
	if got := splitLinesTrim("   \n  "); got != nil {
		t.Errorf("all-blank must be nil, got %v", got)
	}
	env := parseEnvLines("K=v\n X = y \nbad\n=z")
	if env["K"] != "v" || env["X"] != "y" {
		t.Errorf("parseEnvLines = %v", env)
	}
	if len(env) != 2 {
		t.Errorf("bad/keyless lines must be dropped: %v", env)
	}
}
