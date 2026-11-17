package tui

import (
	"github.com/sushidev-team/lola/internal/config"
	"testing"
)

func TestPRCommentFormOverrideAndDefault(t *testing.T) {
	f, path := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web", Inherits: config.ProjectInherits{AutoPRComments: true}}}, "web")
	f.cfg.Defaults.AutoPRComments = true
	f.setInherit(fAutoPRComments, true)
	if !f.poll.AutoPRComments {
		t.Fatal("did not fill inherited true")
	}
	f.toggleBool(fAutoPRComments)
	if f.poll.AutoPRComments || f.poll.Inherits.AutoPRComments {
		t.Fatal("toggle did not override")
	}
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Projects[0].AutoPRComments || c.Projects[0].Inherits.AutoPRComments {
		t.Fatal("explicit off lost")
	}
	settings := newSettingsForm(path, c)
	settings.field("def_auto_pr_comments").b = true
	if ev := settings.save(); ev != settingsFormSaved {
		t.Fatalf("settings save failed: %v", settings.err)
	}
	c, err = config.Load(path)
	if err != nil || !c.Defaults.AutoPRComments || c.Projects[0].AutoPRComments {
		t.Fatal("global default or override lost")
	}
}
