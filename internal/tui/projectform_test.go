package tui

import (
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// setList fills a list/env field the way the key handler does: the value plus
// the promotion from inherited to a project-level override.
func setList(fld *projField, lines []string) {
	fld.lines, fld.inherit = lines, false
}

// The project editor writes its list/env fields back into the project and
// persists them, splitting one-entry-per-line.
func TestProjectFormSave(t *testing.T) {
	m := newTestRoot(t) // config has a project named "nori-app"
	f, ok := newProjectForm(m.cfgPath, m.cfg, "nori-app")
	if !ok {
		t.Fatal("newProjectForm: project not found")
	}
	// Editing a field promotes it from inherited to a project-level override;
	// the key handler does that, so a direct poke must clear the bit too.
	setList(&f.fields[3], []string{".env", " storage ", ""}) // symlinks (trims, drops blank)
	setList(&f.fields[4], []string{"composer install", "npm ci"})
	setList(&f.fields[5], []string{"APP_ENV=local", "DEBUG = 1", "nope"})

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

func TestTrimDropEmptyAndParseEnv(t *testing.T) {
	if got := trimDropEmpty([]string{"  a ", "", " b ", "  "}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("trimDropEmpty = %v", got)
	}
	if got := trimDropEmpty([]string{"   ", ""}); got != nil {
		t.Errorf("all-blank must be nil, got %v", got)
	}
	env := parseEnvLines([]string{"K=v", " X = y ", "bad", "=z"})
	if env["K"] != "v" || env["X"] != "y" {
		t.Errorf("parseEnvLines = %v", env)
	}
	if len(env) != 2 {
		t.Errorf("bad/keyless lines must be dropped: %v", env)
	}
}

// The per-project agent override is a cycle field: it pre-fills the project's
// stored value (empty = inherit), steps inherit→claude→codex→opencode on enter
// AND space, and persists a pinned selection.
func TestProjectFormAgentOverride(t *testing.T) {
	m := newTestRoot(t)
	f, ok := newProjectForm(m.cfgPath, m.cfg, "nori-app")
	if !ok {
		t.Fatal("newProjectForm: project not found")
	}
	af := f.agentField()
	if af == nil {
		t.Fatal("agent override field missing")
	}
	if af.kind != pfAgent {
		t.Errorf("agent must be a cycle field, got kind %v", af.kind)
	}
	if af.text != "" {
		t.Errorf("agent prefill = %q, want empty (inherit) for an unset project", af.text)
	}

	// Focus the appended field, then cycle: inherit → claude (enter) → codex (space).
	f.cursor = len(f.fields) - 1
	f.update(keyMsg("enter"))
	if af.text != "claude" {
		t.Fatalf("enter must cycle inherit → claude, got %q", af.text)
	}
	f.update(keyMsg(" "))
	if af.text != "codex" {
		t.Fatalf("space must cycle claude → codex, got %q", af.text)
	}
	// A stray keystroke must not open a list or corrupt the value.
	f.update(keyMsg("x"))
	if f.editing || af.text != "codex" {
		t.Errorf("typing on a cycle field must be inert (editing=%v val=%q)", f.editing, af.text)
	}

	if ev := f.save(); ev != projFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if p := reloaded.ProjectByName("nori-app"); p == nil || p.Agent != "codex" {
		t.Fatalf("project agent not persisted: %+v", p)
	}
	if got := reloaded.AgentForProject("nori-app"); got != "codex" {
		t.Errorf("AgentForProject = %q, want codex", got)
	}
}

// Empty means inherit: cycling back to the inherit slot clears the override so
// the project resolves the global default again.
func TestProjectFormAgentInheritClears(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Projects[0].Agent = "opencode" // start pinned
	f, _ := newProjectForm(m.cfgPath, m.cfg, "nori-app")
	af := f.agentField()
	if af.text != "opencode" {
		t.Fatalf("prefill = %q, want opencode", af.text)
	}
	af.text = "" // inherit
	if ev := f.save(); ev != projFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	reloaded, _ := config.Load(m.cfgPath)
	if got := reloaded.ProjectByName("nori-app").Agent; got != "" {
		t.Errorf("inherit must clear the override, got %q", got)
	}
	// With the override cleared the project inherits the (unset ⇒ claude) default.
	if got := reloaded.AgentForProject("nori-app"); got != "claude" {
		t.Errorf("cleared override must inherit claude, got %q", got)
	}
}

// A bad override value (only reachable by injection) is rejected by Validate on
// save and rolled back, leaving both the in-memory and on-disk config untouched.
func TestProjectFormRejectsBadAgent(t *testing.T) {
	m := newTestRoot(t)
	f, _ := newProjectForm(m.cfgPath, m.cfg, "nori-app")
	f.agentField().text = "gpt5"

	if ev := f.save(); ev != projFormNone || f.err == "" {
		t.Fatalf("a bad agent must abort save with an error, got ev=%v err=%q", ev, f.err)
	}
	if m.cfg.Projects[0].Agent == "gpt5" {
		t.Error("a rejected save must roll back the in-memory agent value")
	}
	reloaded, _ := config.Load(m.cfgPath)
	if got := reloaded.ProjectByName("nori-app").Agent; got != "" {
		t.Errorf("a rejected save must not persist the bad value, got %q", got)
	}
}

// enter opens a list field into line-editing (arrows then move lines); esc
// closes it back to field navigation.
func TestProjectFormListEditMode(t *testing.T) {
	m := newTestRoot(t)
	f, _ := newProjectForm(m.cfgPath, m.cfg, "nori-app")
	f.cursor = 4 // Post-create (a list field)
	// up/down navigate FIELDS while not editing
	f.update(keyMsg("up"))
	if f.editing || f.cursor != 3 {
		t.Fatalf("up in nav mode must move fields (cursor=%d editing=%v)", f.cursor, f.editing)
	}
	f.cursor = 4
	f.update(keyMsg("enter")) // open the list
	if !f.editing {
		t.Fatal("enter on a list field must open it for editing")
	}
	// now enter adds a line; typing edits it
	f.update(keyMsg("enter"))
	for _, r := range "make build" {
		f.update(keyMsg(string(r)))
	}
	if f.fields[4].lines[f.lineCur] != "make build" {
		t.Errorf("typed line = %q", f.fields[4].lines[f.lineCur])
	}
	f.update(keyMsg("esc")) // close
	if f.editing {
		t.Error("esc must close the list back to field navigation")
	}
}
