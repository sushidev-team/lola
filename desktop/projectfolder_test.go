package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

func TestProjectFolderImport(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"api", "web", "existing", "skipped"} {
		if out, err := exec.Command("git", "init", "-b", "trunk", filepath.Join(parent, name)).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	web := filepath.Join(parent, "web")
	api := filepath.Join(parent, "api")
	existing := filepath.Join(parent, "existing")
	if err := os.Symlink(api, filepath.Join(parent, "alias")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", api, "remote", "add", "origin", "git@github.com:acme/api.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v %s", err, out)
	}
	configFile := writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[group]]\nname = 'clients'\ncollapsed = true\n[[project]]\nname = 'web'\npath = %q\n", existing))
	svc := &ConfigService{}
	scan, err := svc.ScanProjectFolder(parent)
	if err != nil || len(scan.Apps) != 4 {
		t.Fatalf("scan = %+v, err = %v", scan, err)
	}
	for _, app := range scan.Apps {
		if (app.Blocked != "") != (app.Path == canonicalPath(existing)) {
			t.Fatalf("unexpected eligibility: %+v", app)
		}
	}
	missing := filepath.Join(parent, "missing")
	if _, err := svc.ScanProjectFolder(missing); err == nil {
		t.Fatal("missing parent scanned as empty")
	}
	if _, err := svc.AddProjectsFromFolder("clients", []string{web, missing}); err == nil {
		t.Fatal("missing checkout accepted")
	}
	if _, err := svc.AddProjectsFromFolder("removed-group", []string{web}); err == nil {
		t.Fatal("missing group accepted")
	}
	paths := []string{api, web, existing}
	if n, err := svc.AddProjectsFromFolder("clients", paths); n != 2 || err != nil {
		t.Fatalf("import = %d, %v", n, err)
	}
	if n, err := svc.AddProjectsFromFolder("", paths); n != 0 || err != nil {
		t.Fatalf("repeated import = %d, %v", n, err)
	}
	cfg, err := config.Load(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 3 || cfg.GroupByName("clients").Collapsed || cfg.ProjectByName("web").Group != "" {
		t.Fatalf("unexpected configuration: groups=%+v projects=%+v", cfg.Groups, cfg.Projects)
	}
	for _, id := range []string{"api", "web-2"} {
		p := cfg.ProjectByName(id)
		if p == nil || p.Group != "clients" || p.Stack != "" || p.Enabled || p.DefaultBranch != "trunk" || !p.Inherits.MatchLabels {
			t.Fatalf("project %s = %+v", id, p)
		}
	}
	if p := cfg.ProjectByName("api"); p.Path != canonicalPath(api) || p.Repo != "acme/api" {
		t.Fatalf("imported repository = %+v", p)
	}
}
