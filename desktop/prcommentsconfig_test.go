package main

import (
	"github.com/sushidev-team/lola/internal/config"
	"testing"
)

func TestPRCommentSettingsAndProjectOverride(t *testing.T) {
	path := writeTestConfig(t, minimalConfig+"\nauto_pr_comments = true\n[[project]]\nname = \"web\"\npath = \"/tmp/web\"\n")
	svc := &ConfigService{}
	p, err := svc.GetProject("web")
	if err != nil {
		t.Fatal(err)
	}
	if !p.AutoPRComments || !p.Inherits.AutoPRComments {
		t.Fatal("missing inherited setting")
	}
	p.AutoPRComments, p.Inherits.AutoPRComments = false, false
	if err := svc.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Defaults.AutoPRComments || c.Projects[0].AutoPRComments || c.Projects[0].Inherits.AutoPRComments {
		t.Fatal("explicit off lost")
	}
	p.Inherits.AutoPRComments = true
	if err := svc.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	p, err = svc.GetProject("web")
	if err != nil || !p.AutoPRComments {
		t.Fatalf("inherit failed: %+v %v", p, err)
	}
	d, err := svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	d.AutoPRComments = false
	if err := svc.SaveSettings(d); err != nil {
		t.Fatal(err)
	}
	p, err = svc.GetProject("web")
	if err != nil || p.AutoPRComments || !p.Inherits.AutoPRComments {
		t.Fatalf("default failed: %+v %v", p, err)
	}
	p, err = svc.GetProject("")
	if err != nil || !p.Inherits.AutoPRComments {
		t.Fatal("new project must inherit")
	}
}
