package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

// newNativeTestForm builds a form on a saved config that defines the given
// [[project]] entries (the native runtime's registry).
func newNativeTestForm(t *testing.T, projects []config.Project) (*formModel, string) {
	t.Helper()
	t.Setenv("LOLA_HOME", t.TempDir())
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Defaults: config.Defaults{PollInterval: time.Minute, ConcurrencyCap: 1, GlobalCap: 4},
		Projects: projects,
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := newFormModel(loaded, nil) // returned cmd (teams fetch) is never run
	return f, path
}

// newFormOn is newNativeTestForm preloaded with one of the saved projects —
// how every edit entry point reaches the form now that a project IS the unit.
func newFormOn(t *testing.T, projects []config.Project, name string) (*formModel, string) {
	t.Helper()
	f, path := newNativeTestForm(t, projects)
	pr := f.cfg.ProjectByName(name)
	if pr == nil {
		t.Fatalf("project %q not in test config", name)
	}
	g, _ := newFormModel(f.cfg, pr)
	return g, path
}

// A form opened on an existing project persists onto that project — there is
// no project picker to disambiguate.
func TestFormSavesPollingOntoItsProject(t *testing.T) {
	f, path := newFormOn(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web"},
	}, "web")

	// Linear-backed fields are set directly; the form only persists them.
	f.poll.TeamID = "team-1"

	if f.isNew {
		t.Fatal("form on an existing project must not be isNew")
	}
	if f.origName != "web" {
		t.Fatalf("origName = %q, want web", f.origName)
	}

	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.PollByName("web") // the polling config lives on project "web"
	if p == nil || !p.Polls() {
		t.Fatalf("project web not configured to poll: %+v", p)
	}
	if p.TeamID != "team-1" {
		t.Errorf("persisted team = %q, want team-1", p.TeamID)
	}
}

// The write-back fields are edited through the same Linear state/label pickers
// as the trigger fields: On PR → In Review via the state picker, and the
// pr_requires_checks gate toggled in place — then persisted to config.
func TestFormWriteBackPickersAndToggles(t *testing.T) {
	f, path := newFormOn(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web"},
	}, "web")
	f.poll.TeamID = "team-1"
	f.meta = &teamMeta{
		States: []linear.State{
			{ID: "st-prog", Name: "In Progress", Type: "started"},
			{ID: "st-review", Name: "In Review", Type: "started"},
			{ID: "st-done", Name: "Done", Type: "completed"},
		},
		Labels: []linear.Label{{ID: "lbl-blocked", Name: "blocked"}},
	}

	// Write-back lives on its own tab, visible once a team is set.
	f.tab = tabWriteback
	for _, fd := range []fieldID{fOnSpawnState, fOnPRState, fCommentOnPR, fOnMergedState, fBlockedLabel} {
		if !slices.Contains(f.fields(), fd) {
			t.Fatalf("write-back field %d must be visible", fd)
		}
	}
	// The green-checks gate stays hidden until the PR transition is configured.
	if slices.Contains(f.fields(), fPRRequiresChecks) {
		t.Fatal("pr_requires_checks must be hidden before an on_pr transition is set")
	}

	// Pick "On PR → In Review" through the state picker (opts: (none), In
	// Progress, In Review, Done → index 2 is In Review).
	f.openPicker(fOnPRState)
	if f.picker == nil || f.picker.field != fOnPRState {
		t.Fatalf("PR-state picker did not open: %+v", f.picker)
	}
	f.picker.cursor = 2
	f.pickerKey(keyMsg("enter"))
	if f.poll.OnPRStateID != "st-review" {
		t.Fatalf("OnPRStateID = %q, want st-review", f.poll.OnPRStateID)
	}

	// Now the gate appears; enter toggles it on.
	if !slices.Contains(f.fields(), fPRRequiresChecks) {
		t.Fatal("pr_requires_checks must appear once on_pr state is set")
	}
	if _, ev := f.interact(fPRRequiresChecks); ev != formNone || !f.poll.PRRequiresChecks {
		t.Fatalf("interact must toggle PRRequiresChecks on, got %v", f.poll.PRRequiresChecks)
	}

	// Spawn → In Progress via the picker too.
	f.openPicker(fOnSpawnState)
	f.picker.cursor = 1
	f.pickerKey(keyMsg("enter"))
	if f.poll.OnSpawnStateID != "st-prog" {
		t.Fatalf("OnSpawnStateID = %q, want st-prog", f.poll.OnSpawnStateID)
	}

	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.PollByName("web")
	if p == nil {
		t.Fatal("polling config on web not persisted")
	}
	if p.OnSpawnStateID != "st-prog" || p.OnPRStateID != "st-review" || !p.PRRequiresChecks {
		t.Errorf("persisted write-back = spawn %q / pr %q / requires-checks %v, want st-prog / st-review / true",
			p.OnSpawnStateID, p.OnPRStateID, p.PRRequiresChecks)
	}
}

// A "(none)" pick clears a configured write-back state transition.
func TestFormWriteBackStateClearToNone(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web", Repo: "acme/web"}}, "web")
	f.poll.TeamID = "team-1"
	f.poll.OnMergedStateID = "st-done"
	f.meta = &teamMeta{States: []linear.State{{ID: "st-done", Name: "Done", Type: "completed"}}}

	f.openPicker(fOnMergedState) // opts: (none) at index 0, Done at 1
	f.picker.cursor = 0
	f.pickerKey(keyMsg("enter"))
	if f.poll.OnMergedStateID != "" {
		t.Errorf("OnMergedStateID = %q, want cleared", f.poll.OnMergedStateID)
	}
}

// Saving without a project surfaces a friendly error (nc.Validate also enforces
// the [[project]] reference).
func TestFormSaveRequiresProject(t *testing.T) {
	f, _ := newNativeTestForm(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web"},
	})
	f.poll.TeamID = "team-1"
	// Name deliberately left unset.

	if _, ev := f.save(); ev != formNone {
		t.Fatal("save must fail without a name")
	}
	if !slices.Contains(f.errs, "name is required") {
		t.Errorf("errs = %v, want name requirement", f.errs)
	}
}

// An unset repo renders the daemon-owned [[project]] fallback hint rather than
// reading as a hard requirement.
func TestFormRepoHintShowsProjectFallback(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{
		{Name: "web", Path: "/tmp/web"}, // no repo
	}, "web")

	if hint := f.display(fRepo); !strings.Contains(hint, "[[project]] repo") {
		t.Errorf("repo hint = %q, want [[project]] fallback wording", hint)
	}
}

// The name is the [[project]] config key: save() targets origName, so typing
// over it on an existing project must be inert rather than silently no-op.
func TestFormNameReadOnlyOnExistingProject(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web"},
	}, "web")

	f.cursor = 0 // fName
	if f.fields()[f.cursor] != fName {
		t.Fatalf("cursor 0 = %v, want fName", f.fields()[f.cursor])
	}
	f.key(keyMsg("x"))
	if f.poll.Name != "web" {
		t.Errorf("Name = %q after typing, want web (read-only)", f.poll.Name)
	}
}

// The form CREATES a project end to end: on an empty config it takes a name and
// a path and writes a new [[project]]. This is the case that was impossible
// before the merge — the form could only attach polling to an entry that
// already existed, and the only way to create one lived on another screen.
func TestFormCreatesProjectFromEmptyConfig(t *testing.T) {
	f, path := newNativeTestForm(t, nil) // no projects at all

	if !f.isNew {
		t.Fatal("a form opened on nothing must be new")
	}
	f.poll.Name = "web"
	f.poll.Path = "/tmp/web"

	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.ProjectByName("web")
	if p == nil {
		t.Fatal("project was not created")
	}
	if p.Path != "/tmp/web" {
		t.Errorf("path = %q, want /tmp/web", p.Path)
	}
	// Not polling: no team was picked, so it is a plain worktree project.
	if p.Polls() {
		t.Error("a project with no team must not be marked as polling")
	}
}

// A path is required to create a project — Validate rejects one without it, so
// the form must say so rather than writing an entry that can never spawn.
func TestFormCreateRequiresPath(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.poll.Name = "web" // path deliberately left unset

	if _, ev := f.save(); ev != formNone {
		t.Fatal("save must fail without a path")
	}
	if !slices.ContainsFunc(f.errs, func(e string) bool { return strings.Contains(e, "path is required") }) {
		t.Errorf("errs = %v, want a path requirement", f.errs)
	}
}

// A new project starts out inheriting the shared [defaults] setup, so a first
// project picks up whatever is already configured globally rather than starting
// blank and silently diverging.
func TestFormNewProjectInheritsDefaults(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Defaults: config.Defaults{
			PollInterval: time.Minute, ConcurrencyCap: 1, GlobalCap: 4,
			Symlinks:    []string{".env"},
			PostCreate:  []string{"composer install"},
			MatchLabels: []string{"label-agent"},
			MatchMode:   "all",
		},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := newFormModel(loaded, nil)

	if !f.inherits(fSymlinks) || !f.inherits(fLabels) || !f.inherits(fMatchMode) {
		t.Errorf("a new project must start inheriting, got %+v", f.poll.Inherits)
	}
	if !slices.Equal(f.symlinks, []string{".env"}) {
		t.Errorf("symlinks ghost = %v, want the [defaults] value", f.symlinks)
	}
	if !slices.Equal(f.poll.MatchLabels, []string{"label-agent"}) || f.poll.MatchMode != "all" {
		t.Errorf("label ghosts = %v / %q, want the [defaults] values", f.poll.MatchLabels, f.poll.MatchMode)
	}

	// Saving keeps them inherited: the keys must not be frozen onto the project.
	f.poll.Name, f.poll.Path = "web", "/tmp/web"
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	again, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := again.ProjectByName("web")
	if p == nil {
		t.Fatal("project not created")
	}
	if !p.Inherits.Symlinks || !p.Inherits.MatchLabels {
		t.Errorf("inherited keys must stay inherited after save, got %+v", p.Inherits)
	}
	// And a later change to [defaults] still reaches it.
	again.Defaults.Symlinks = []string{".env", ".env.local"}
	if err := again.Save(path); err != nil {
		t.Fatal(err)
	}
	final, _ := config.Load(path)
	if got := final.ProjectByName("web").Symlinks; !slices.Equal(got, []string{".env", ".env.local"}) {
		t.Errorf("symlinks = %v, want to track the changed default", got)
	}
}

// ctrl+o promotes an inherited field to a project-level override, and toggling
// back refills it from [defaults] so the ghost shows what will apply.
func TestFormInheritToggle(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	path, _ := config.DefaultPath()
	cfg := &config.Config{
		Defaults: config.Defaults{
			PollInterval: time.Minute, ConcurrencyCap: 1, GlobalCap: 4,
			Symlinks: []string{".env"},
		},
		Projects: []config.Project{{Name: "web", Path: "/tmp/web"}},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, _ := config.Load(path)
	f, _ := newFormModel(loaded, loaded.ProjectByName("web"))

	if !f.inherits(fSymlinks) {
		t.Fatal("symlinks must start inherited (the key is absent from the project)")
	}
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fSymlinks)
	f.key(keyMsg("ctrl+o"))
	if f.inherits(fSymlinks) {
		t.Error("ctrl+o must promote an inherited field to an override")
	}
	f.symlinks = []string{"vendor-cache"}
	f.key(keyMsg("ctrl+o"))
	if !f.inherits(fSymlinks) {
		t.Error("ctrl+o must revert an override back to inherit")
	}
	if !slices.Equal(f.symlinks, []string{".env"}) {
		t.Errorf("reverting must refill from [defaults], got %v", f.symlinks)
	}
}

// Opening a list field for editing IS overriding it — an inherited value the
// user starts typing into is no longer inherited.
func TestFormEditingListPromotesToOverride(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fPostCreate)
	if !f.inherits(fPostCreate) {
		t.Fatal("post_create must start inherited")
	}
	f.interact(fPostCreate)
	if f.inherits(fPostCreate) {
		t.Error("opening the field for editing must promote it to an override")
	}
	if !f.editing {
		t.Error("the list field should be open for line editing")
	}
}

// bubbletea v2 delivers a bracketed paste as its OWN tea.PasteMsg, which the
// key encoder never sees — so pasting a project path silently did nothing until
// the forms were routed it explicitly.
func TestFormPasteIntoTextField(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fPath)

	// Copying a path out of a terminal carries a trailing newline.
	f.paste("/Volumes/Git/acme/web\n")
	if f.poll.Path != "/Volumes/Git/acme/web" {
		t.Errorf("path = %q, want the pasted path without the trailing newline", f.poll.Path)
	}
}

// Control characters never reach a field: the value ends up in config.toml and,
// for env, in a shell-sourced file.
func TestFormPasteStripsControlChars(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fPath)

	f.paste("/tmp/\x1b[31mweb\x00")
	if strings.ContainsAny(f.poll.Path, "\x1b\x00") {
		t.Errorf("path = %q, want control characters stripped", f.poll.Path)
	}
}

// The concurrency cap is digits-only, on paste as well as on typing.
func TestFormPasteDigitsOnlyIntoCap(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.poll.TeamID = "team-1"
	f.capBuf = ""
	f.tab = tabFilter // set the tab BEFORE fields(), which is tab-scoped
	f.cursor = slices.Index(f.fields(), fCap)

	f.paste("cap 12\n")
	if f.capBuf != "12" {
		t.Errorf("capBuf = %q, want 12", f.capBuf)
	}
}

// A MULTI-line paste into an open list editor becomes multiple entries —
// pasting several symlinks at once is the point of the sub-editor.
func TestFormPasteMultilineIntoList(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fSymlinks)
	f.interact(fSymlinks) // opens the sub-editor (and promotes to an override)

	f.paste(".env\nstorage/app\nnode_modules\n")
	want := []string{".env", "storage/app", "node_modules"}
	if !slices.Equal(f.symlinks, want) {
		t.Errorf("symlinks = %v, want %v", f.symlinks, want)
	}
	if f.lineCur != len(want)-1 {
		t.Errorf("lineCur = %d, want the cursor on the last pasted entry (%d)", f.lineCur, len(want)-1)
	}
}

// The name is the config key on an existing project; paste must respect that
// read-only rule exactly as typing does.
func TestFormPasteRespectsReadOnlyName(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fName)

	f.paste("renamed")
	if f.poll.Name != "web" {
		t.Errorf("Name = %q, want web (read-only)", f.poll.Name)
	}
}

// A paste while a picker overlay is open must not leak into the field behind it.
func TestFormPasteIgnoredWhilePickerOpen(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fPath)
	f.picker = &picker{title: "Team", field: fTeam}

	f.paste("/etc/passwd")
	if f.poll.Path != "/tmp/web" {
		t.Errorf("path = %q, want unchanged while a picker is open", f.poll.Path)
	}
}

// Space toggles an option in a MULTI-select picker (workflow states, trigger
// labels). bubbletea v2 renders the space key as "space", never " ", so the
// original `case " "` never fired and these lists could not be built at all.
func TestPickerSpaceTogglesMultiSelect(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.poll.TeamID = "team-1"
	f.meta = &teamMeta{States: []linear.State{
		{ID: "st-todo", Name: "Todo", Type: "unstarted"},
		{ID: "st-prog", Name: "In Progress", Type: "started"},
	}}
	f.tab = tabFilter
	f.openPicker(fStates)
	if f.picker == nil || !f.picker.multi {
		t.Fatal("states picker must open as multi-select")
	}

	f.pickerKey(keyMsg("space")) // select Todo (cursor 0)
	f.pickerKey(keyMsg("down"))
	f.pickerKey(keyMsg("space")) // select In Progress
	f.pickerKey(keyMsg("enter"))

	if !slices.Equal(f.poll.StateIDs, []string{"st-todo", "st-prog"}) {
		t.Errorf("StateIDs = %v, want both states selected", f.poll.StateIDs)
	}
}

// Space is also a DEselect — toggling an already-selected option removes it.
func TestPickerSpaceDeselects(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.poll.TeamID = "team-1"
	f.poll.StateIDs = []string{"st-todo"}
	f.meta = &teamMeta{States: []linear.State{{ID: "st-todo", Name: "Todo", Type: "unstarted"}}}
	f.tab = tabFilter
	f.openPicker(fStates)

	f.pickerKey(keyMsg("space")) // cursor is on the pre-selected Todo
	f.pickerKey(keyMsg("enter"))
	if len(f.poll.StateIDs) != 0 {
		t.Errorf("StateIDs = %v, want the selection cleared", f.poll.StateIDs)
	}
}

// Repo auto-detection: leaving the Path field asks for the checkout's GitHub
// remote so the user does not have to copy owner/name by hand.
func TestFormDetectsRepoOnLeavingPath(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fPath)
	f.poll.Path = "/tmp/web"

	cmd := f.leftField(fPath)
	if cmd == nil {
		t.Fatal("leaving Path with an empty repo must request detection")
	}
	// Feed the result back the way the tea loop would.
	f.update(repoDetectedMsg{path: "/tmp/web", repo: "acme/web"})
	if f.poll.Repo != "acme/web" {
		t.Errorf("Repo = %q, want the detected value", f.poll.Repo)
	}
	if !f.repoAuto {
		t.Error("a detected repo must be marked as such for the view")
	}
	if !strings.Contains(stripANSI(f.display(fRepo)), "detected") {
		t.Errorf("display = %q, want it to say the value was detected", stripANSI(f.display(fRepo)))
	}
}

// Detection never overwrites what the user set: it only fills an empty field.
func TestFormDetectionNeverOverwritesTypedRepo(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.poll.Path, f.poll.Repo = "/tmp/web", "mine/web"

	if cmd := f.maybeDetectRepo(); cmd != nil {
		t.Error("a repo the user already set must not trigger detection")
	}
	// Even a stray in-flight result from before must not clobber it.
	f.update(repoDetectedMsg{path: "/tmp/web", repo: "acme/web"})
	if f.poll.Repo != "mine/web" {
		t.Errorf("Repo = %q, want the user's value preserved", f.poll.Repo)
	}
}

// A result for a path the user has since changed is dropped, so a slow
// detection cannot land the wrong repo on a different checkout.
func TestFormDetectionIgnoresStaleResult(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.poll.Path = "/tmp/second"

	f.update(repoDetectedMsg{path: "/tmp/first", repo: "acme/first"})
	if f.poll.Repo != "" {
		t.Errorf("Repo = %q, want a stale result dropped", f.poll.Repo)
	}
}

// A checkout with no usable GitHub remote leaves the field empty — the
// fail-closed value, which disables PR checks rather than guessing.
func TestFormDetectionFailsClosed(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.poll.Path = "/tmp/web"

	f.update(repoDetectedMsg{path: "/tmp/web", repo: ""})
	if f.poll.Repo != "" || f.repoAuto {
		t.Errorf("no remote must leave the field empty, got %q auto=%v", f.poll.Repo, f.repoAuto)
	}
}

// The same path is resolved once, however much the cursor moves over it.
func TestFormDetectionRunsOncePerPath(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.poll.Path = "/tmp/web"

	if f.maybeDetectRepo() == nil {
		t.Fatal("first detection must run")
	}
	if f.maybeDetectRepo() != nil {
		t.Error("the same path must not be resolved twice")
	}
	f.poll.Path = "/tmp/other"
	if f.maybeDetectRepo() == nil {
		t.Error("a changed path must be resolved again")
	}
}

// Typing over a detected value drops the "detected" marker — it is the user's
// value now.
func TestFormTypingClearsDetectedMarker(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.poll.Path = "/tmp/web"
	f.update(repoDetectedMsg{path: "/tmp/web", repo: "acme/web"})

	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fRepo)
	f.key(keyMsg("x"))
	if f.repoAuto {
		t.Error("typing into Repo must clear the detected marker")
	}
}

// The default branch is pickable from the checkout's branches, and still
// typable — a path that is not a checkout must never be a dead end.
func TestFormDefaultBranchPicker(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.branches = []string{"main", "develop", "release-2"}
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fDefaultBranch)

	f.interact(fDefaultBranch)
	if f.picker == nil || f.picker.field != fDefaultBranch {
		t.Fatalf("enter must open the branch picker, got %+v", f.picker)
	}
	if f.picker.multi {
		t.Error("the default branch is a single choice")
	}
	f.picker.cursor = 1 // develop
	f.pickerKey(keyMsg("enter"))
	if f.poll.DefaultBranch != "develop" {
		t.Errorf("DefaultBranch = %q, want develop", f.poll.DefaultBranch)
	}
}

// The picker preselects the current value so enter is a no-op rather than a
// silent change to whatever sits at the top.
func TestFormDefaultBranchPickerPreselects(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.branches = []string{"main", "develop"}
	f.poll.DefaultBranch = "develop"

	f.interact(fDefaultBranch)
	if f.picker == nil {
		t.Fatal("picker did not open")
	}
	if got := f.picker.opts[f.picker.cursor].id; got != "develop" {
		t.Errorf("cursor on %q, want the current value develop", got)
	}
}

// With no branches (path unset, or not a checkout) the picker refuses to open
// and says why — the field stays free text.
func TestFormDefaultBranchPickerRefusesWithoutBranches(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.branches = nil

	f.interact(fDefaultBranch)
	if f.picker != nil {
		t.Fatal("the picker must not open with nothing to pick")
	}
	if !strings.Contains(f.loadErr, "no branches found") {
		t.Errorf("loadErr = %q, want an explanation", f.loadErr)
	}
	// Still typable.
	f.tab = tabRepo
	f.cursor = slices.Index(f.fields(), fDefaultBranch)
	f.poll.DefaultBranch = ""
	f.key(keyMsg("m"))
	if f.poll.DefaultBranch != "m" {
		t.Errorf("DefaultBranch = %q, want typing to still work", f.poll.DefaultBranch)
	}
}

// Leaving Path refreshes the branch list, and a result for a path the user has
// since changed is dropped.
func TestFormBranchListLoadsOnPathChange(t *testing.T) {
	f, _ := newNativeTestForm(t, nil)
	f.poll.Path = "/tmp/web"

	if f.maybeLoadBranches() == nil {
		t.Fatal("a new path must load its branches")
	}
	if f.maybeLoadBranches() != nil {
		t.Error("the same path must not reload")
	}
	f.update(branchesMsg{path: "/tmp/web", branches: []string{"main"}})
	if !slices.Equal(f.branches, []string{"main"}) {
		t.Errorf("branches = %v, want [main]", f.branches)
	}

	f.poll.Path = "/tmp/other"
	f.update(branchesMsg{path: "/tmp/web", branches: []string{"stale"}})
	if slices.Contains(f.branches, "stale") {
		t.Error("a result for a superseded path must be dropped")
	}
}
