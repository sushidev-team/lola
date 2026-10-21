package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// Optional, offline render fixture. It exercises the actual modal renderer and
// keyboard paths; captures do not demonstrate a running daemon or live review.
func TestCaptureSpecialistSettings(t *testing.T) {
	dir := os.Getenv("LOLA_TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set LOLA_TUI_CAPTURE_DIR to render offline fixtures")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 40}, {"compact", 80, 24}} {
		m := newTestRoot(t)
		m.width = size.w
		m.height = size.h
		f := newSettingsForm(m.cfgPath, m.cfg)
		m.settings = f
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "specialists-tui-"+size.name+"-"+state+".ansi"), []byte(m.settingsFormModal()), 0600); err != nil {
				t.Fatal(err)
			}
		}
		focusField(t, f, specialistAddKey)
		capture("empty")
		f.update(keyMsg("enter"))
		capture("picker")
		f.update(keyMsg("enter"))
		capture("profile")
		focusField(t, f, specialistKey(0, "enabled"))
		f.update(keyMsg("space"))
		f.save()
		capture("error")
		f.err = ""
		focusField(t, f, specialistKey(0, "remove"))
		capture("remove")
	}
}
