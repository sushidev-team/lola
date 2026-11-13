package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
)

// tree builds a browsable directory: two plain folders, one git checkout, one
// worktree-style checkout (.git is a FILE), a hidden folder and a regular file.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"apps", "web", "worktree", ".cache"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "web", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "worktree", ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// load runs the picker's pending listing synchronously and feeds it back the way
// the tea loop would.
func load(t *testing.T, p *dirPicker, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a directory listing command")
	}
	msg := cmd()
	if _, ok := msg.(dirLoadedMsg); !ok {
		t.Fatalf("got %T, want dirLoadedMsg", msg)
	}
	p.update(msg)
}

func names(p *dirPicker) []string {
	var out []string
	for _, e := range p.visible() {
		out = append(out, e.name)
	}
	return out
}

// The listing is directories only, and marks the ones that are checkouts —
// including a worktree, whose .git is a file rather than a directory.
func TestDirPickerListsDirectoriesAndMarksCheckouts(t *testing.T) {
	root := tree(t)
	p, cmd := newDirPicker(root)
	load(t, p, cmd)

	repos := map[string]bool{}
	for _, e := range p.entries {
		repos[e.name] = e.isRepo
	}
	if _, ok := repos["notes.txt"]; ok {
		t.Error("a regular file must not be listed")
	}
	if !repos["web"] {
		t.Error("a .git directory must mark a checkout")
	}
	if !repos["worktree"] {
		t.Error("a .git FILE (a git worktree) must mark a checkout too")
	}
	if repos["apps"] {
		t.Error("a plain directory must not be marked as a checkout")
	}
}

// Dot-directories stay out of the way until ctrl+t asks for them.
func TestDirPickerHidesDotDirsUntilToggled(t *testing.T) {
	root := tree(t)
	p, cmd := newDirPicker(root)
	load(t, p, cmd)

	for _, n := range names(p) {
		if n == ".cache" {
			t.Fatal(".cache must be hidden by default")
		}
	}
	p.update(keyMsg("ctrl+t"))
	var found bool
	for _, n := range names(p) {
		found = found || n == ".cache"
	}
	if !found {
		t.Error("ctrl+t must reveal dot-directories")
	}
}

// Typing narrows the list; esc clears the filter before it closes anything, so
// a mistyped filter is not an accidental cancel.
func TestDirPickerFilterAndEscape(t *testing.T) {
	root := tree(t)
	p, cmd := newDirPicker(root)
	load(t, p, cmd)

	p.update(keyMsg("w"))
	for _, n := range names(p) {
		if n == "apps" {
			t.Fatalf("filter %q did not narrow the list: %v", p.filter, names(p))
		}
	}
	if _, ev := p.update(keyMsg("esc")); ev != dirPickerNone {
		t.Errorf("esc with a filter must clear it, got %v", ev)
	}
	if p.filter != "" {
		t.Errorf("filter = %q, want it cleared", p.filter)
	}
	if _, ev := p.update(keyMsg("esc")); ev != dirPickerCancel {
		t.Errorf("esc on an unfiltered list must cancel, got %v", ev)
	}
}

// enter TAKES a checkout (the thing the browser is for) and WALKS INTO anything
// else.
func TestDirPickerEnterTakesRepoAndDescendsFolders(t *testing.T) {
	root := tree(t)
	p, cmd := newDirPicker(root)
	load(t, p, cmd)

	// "apps" sorts first — a plain directory.
	if got := names(p)[p.cursor]; got != "apps" {
		t.Fatalf("cursor on %q, want apps", got)
	}
	c, ev := p.update(keyMsg("enter"))
	if ev != dirPickerNone {
		t.Fatalf("enter on a plain folder must walk into it, got %v", ev)
	}
	if p.dir != filepath.Join(root, "apps") {
		t.Errorf("dir = %q, want the child", p.dir)
	}
	load(t, p, c)

	// Back up, put the cursor on the checkout, take it.
	load(t, p, p.ascend())
	p.filter = "web"
	p.cursor = 0
	_, ev = p.update(keyMsg("enter"))
	if ev != dirPickerChosen {
		t.Fatalf("enter on a checkout must choose it, got %v", ev)
	}
	if p.chosen != filepath.Join(root, "web") {
		t.Errorf("chosen = %q, want the checkout", p.chosen)
	}
}

// Going up puts the cursor back on the directory just left, so walking the tree
// is symmetrical rather than resetting to the top every time.
func TestDirPickerAscendRestoresCursor(t *testing.T) {
	root := tree(t)
	p, cmd := newDirPicker(filepath.Join(root, "web"))
	load(t, p, cmd)

	load(t, p, p.ascend())
	if p.dir != root {
		t.Fatalf("dir = %q, want the parent %q", p.dir, root)
	}
	if got := names(p)[p.cursor]; got != "web" {
		t.Errorf("cursor on %q, want the directory we came from", got)
	}
}

// ctrl+s takes the directory being browsed — a checkout already walked into, or
// a folder about to be git init'd.
func TestDirPickerCtrlSTakesCurrentDirectory(t *testing.T) {
	root := tree(t)
	p, cmd := newDirPicker(root)
	load(t, p, cmd)

	if _, ev := p.update(keyMsg("ctrl+s")); ev != dirPickerChosen {
		t.Fatalf("ctrl+s must choose, got %v", ev)
	}
	if p.chosen != root {
		t.Errorf("chosen = %q, want the browsed directory %q", p.chosen, root)
	}
}

// A listing that lands after the user has moved on is dropped: it describes a
// directory the browser already left.
func TestDirPickerDropsStaleListing(t *testing.T) {
	root := tree(t)
	p, cmd := newDirPicker(root)
	load(t, p, cmd)

	p.update(dirLoadedMsg{dir: filepath.Join(root, "elsewhere"), entries: []dirEntry{{name: "ghost"}}})
	for _, n := range names(p) {
		if n == "ghost" {
			t.Fatal("a listing for another directory must be ignored")
		}
	}
}

// The browser opens where the next repo most likely is: the project's own path,
// else the folder the configured checkouts already share, else $HOME.
func TestDirPickerStart(t *testing.T) {
	root := tree(t)
	cfg := &config.Config{Projects: []config.Project{
		{Name: "web", Path: filepath.Join(root, "web")},
		{Name: "worktree", Path: filepath.Join(root, "worktree")},
	}}

	if got := dirPickerStart(cfg, filepath.Join(root, "apps")); got != filepath.Join(root, "apps") {
		t.Errorf("start = %q, want the current path itself", got)
	}
	if got := dirPickerStart(cfg, filepath.Join(root, "web", "gone")); got != filepath.Join(root, "web") {
		t.Errorf("start = %q, want the parent of a path that no longer exists", got)
	}
	if got := dirPickerStart(cfg, ""); got != root {
		t.Errorf("start = %q, want the directory the configured projects share (%q)", got, root)
	}
	home, _ := os.UserHomeDir()
	if got := dirPickerStart(&config.Config{}, ""); got != home {
		t.Errorf("start = %q, want $HOME with nothing configured", got)
	}
}
