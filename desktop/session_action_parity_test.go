package main

// Anti-drift pin for the Session menu. main.go names each accelerator's action and the frontend
// applies it to the session in view, with no compiler in common. A chord whose action the
// frontend does not know is a menu item that silently does nothing, inside a terminal too.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	appSvelte = "frontend/src/App.svelte"
	sshStore  = "frontend/src/lib/ssh.svelte.ts"
)

func TestEverySessionActionIsHandled(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile(appSvelte)
	if err != nil {
		t.Fatal(err)
	}
	store, err := os.ReadFile(sshStore)
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`\baction[A-Z]\w*\s*=\s*"([a-z-]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(m) < 8 {
		t.Fatalf("found %d actions in main.go, so this test stopped guarding anything", len(m))
	}
	actions := map[string]bool{}
	for _, a := range m {
		actions[a[1]] = true
	}
	// The three the menu names by a literal, ask("commands") and its neighbours.
	for _, a := range regexp.MustCompile(`ask\("([a-z-]+)"\)`).FindAllStringSubmatch(string(src), -1) {
		actions[a[1]] = true
	}

	for action := range actions {
		quoted := `"` + action + `"`
		if strings.HasPrefix(action, "ssh-") {
			if !strings.Contains(string(store), quoted) {
				t.Errorf("%s: action %s reaches ssh.menuAction, which never names it", sshStore, action)
			}
			continue
		}
		if !strings.Contains(string(app), `case `+quoted) && !strings.Contains(string(app), `action === `+quoted) {
			t.Errorf("%s: sessionAction does not handle %s", appSvelte, action)
		}
	}
}
