package tui

// Tests for the global settings editor (settingsform.go): the tab strip and the
// field filtering it drives, field pre-fill, bool toggles + text/int editing,
// the list/env sub-editor, a persisted save across all five tables, the
// invalid-input guards (non-numeric, global_cap <= 0) that abort WITHOUT
// mutating config, and the [defaults] project-fallback keys round-tripping to
// config.toml.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
)

// shiftTabKey is the back-tab press. The shared keyMsg helper only knows plain
// named keys, and shift+tab is a modifier combination.
func shiftTabKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
}

// focusField moves the cursor onto the named field, switching to whichever tab
// owns it. f.cursor is TAB-RELATIVE, so a bare index into f.fields is not a
// valid cursor.
func focusField(t *testing.T, f *settingsForm, key string) {
	t.Helper()
	for i := range f.fields {
		if f.fields[i].key != key {
			continue
		}
		f.tab = f.fields[i].tab
		for vi, gi := range f.visible() {
			if gi == i {
				f.cursor = vi
				return
			}
		}
	}
	t.Fatalf("field %q not found", key)
}

func TestSettingsFormPrefillAndSave(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	// Pre-filled from the live config ([defaults] came from newTestRoot).
	if got := f.field("global_cap").text; got != "4" {
		t.Errorf("global_cap prefill = %q, want 4", got)
	}
	if got := f.field("poll_interval").text; got != "1m0s" {
		t.Errorf("poll_interval prefill = %q, want 1m0s", got)
	}
	// coderabbit author pre-fills the effective default when unset.
	if got := f.field("cr_author").text; got != config.DefaultCodeRabbitAuthor {
		t.Errorf("cr_author prefill = %q, want %q", got, config.DefaultCodeRabbitAuthor)
	}

	// Turn the watch on, set a custom author, and enable notify + send.
	f.field("cr_enabled").b = true
	f.field("cr_notify").b = true
	f.field("cr_send").b = true
	f.field("cr_author").text = "sonarcloud"
	// Also flip a [review] toggle and bump the global cap.
	f.field("review_enabled").b = true
	f.field("global_cap").text = "8"

	if ev := f.save(); ev != settingsFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}

	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Defaults.GlobalCap != 8 {
		t.Errorf("global_cap = %d, want 8", reloaded.Defaults.GlobalCap)
	}
	cr := reloaded.CodeRabbit
	if !cr.Enabled || cr.Author != "sonarcloud" || !cr.Notify || !cr.SendToAgent {
		t.Errorf("coderabbit not persisted: %+v", cr)
	}
	if !reloaded.Review.Enabled {
		t.Error("review.enabled must persist")
	}
}

// The tab strip filters the field list: only the active tab's fields are
// navigable and rendered, and tab / shift+tab (and right/left) move between
// tabs, wrapping at both ends.
func TestSettingsFormTabsFilterFields(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	if f.tab != stDefaults {
		t.Fatalf("editor must open on the Defaults tab, got %v", f.tab)
	}
	// Assertions use field LABELS, never tab titles — the strip renders every
	// tab title on every tab, so a title match proves nothing.
	out := f.view()
	if !strings.Contains(out, "Global cap") {
		t.Errorf("Defaults tab must show its own fields:\n%s", out)
	}
	for _, other := range []string{"Branch prefix", "Desktop banners", "Summarize approved", "Comment on Linear"} {
		if strings.Contains(out, other) {
			t.Errorf("Defaults tab must not show %q:\n%s", other, out)
		}
	}

	// tab → Project defaults.
	f.update(keyMsg("tab"))
	if f.tab != stProjectDefaults {
		t.Fatalf("tab must advance to Project defaults, got %v", f.tab)
	}
	out = f.view()
	if !strings.Contains(out, "Branch prefix") || !strings.Contains(out, "Match mode") {
		t.Errorf("Project defaults tab must show its fields:\n%s", out)
	}
	if strings.Contains(out, "Global cap") {
		t.Errorf("Project defaults tab must not show the Defaults fields:\n%s", out)
	}

	// shift+tab back, then wrap backwards past the first tab onto the last.
	f.update(shiftTabKey())
	if f.tab != stDefaults {
		t.Fatalf("shift+tab must go back to Defaults, got %v", f.tab)
	}
	f.update(shiftTabKey())
	if f.tab != stCodeRabbit {
		t.Fatalf("shift+tab must wrap onto the last tab, got %v", f.tab)
	}
	// right/left are aliases, and wrap forwards off the last tab.
	f.update(keyMsg("right"))
	if f.tab != stDefaults {
		t.Fatalf("right must wrap onto the first tab, got %v", f.tab)
	}
	f.update(keyMsg("left"))
	if f.tab != stCodeRabbit {
		t.Fatalf("left must wrap back onto the last tab, got %v", f.tab)
	}

	// Switching tabs resets the cursor, so it can never point past a shorter
	// field list, and ↓ stops at the end of the ACTIVE tab.
	f.tab, f.cursor = stCodeRabbit, 9
	f.update(keyMsg("tab"))
	if f.cursor != 0 {
		t.Errorf("switching tabs must reset the cursor, got %d", f.cursor)
	}
	for range 20 {
		f.update(keyMsg("down"))
	}
	if f.cursor != len(f.visible())-1 {
		t.Errorf("↓ must stop at the last field of the active tab, got %d of %d", f.cursor, len(f.visible()))
	}
}

// Every [defaults] project-fallback key round-trips through save into
// config.Defaults — the list/env fields via one entry per line, the two enums
// via their cycled value, the label UUIDs as plain text.
func TestSettingsFormProjectDefaultsRoundTrip(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	// Every one of these fields must live on the Project defaults tab.
	keys := []string{
		"def_branch_prefix", "def_symlinks", "def_post_create", "def_env",
		"def_match_labels", "def_match_mode", "def_on_sent_set_label",
		"def_blocked_label_id", "def_dedup_mode", "def_priority_sort",
	}
	for _, k := range keys {
		fld := f.field(k)
		if fld == nil {
			t.Fatalf("field %q missing", k)
		}
		if fld.tab != stProjectDefaults {
			t.Errorf("%s must sit on the Project defaults tab, got %v", k, fld.tab)
		}
	}

	f.field("def_branch_prefix").text = "feat/"
	// Blank entries are dropped on save (shared trimDropEmpty).
	f.field("def_symlinks").lines = []string{".env", "  ", "storage/app"}
	f.field("def_post_create").lines = []string{"composer install"}
	f.field("def_env").lines = []string{"FOO=bar", "BAZ=qux"}
	f.field("def_match_labels").lines = []string{"lbl-uuid-1", "lbl-uuid-2"}
	f.field("def_match_mode").text = "all"
	f.field("def_on_sent_set_label").text = "lbl-sent"
	f.field("def_blocked_label_id").text = "lbl-blocked"
	f.field("def_dedup_mode").text = "label"
	f.field("def_priority_sort").lines = []string{"priority", "createdAt"}

	if ev := f.save(); ev != settingsFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	d := reloaded.Defaults
	if d.BranchPrefix != "feat/" {
		t.Errorf("branch_prefix = %q, want feat/", d.BranchPrefix)
	}
	if got := strings.Join(d.Symlinks, ","); got != ".env,storage/app" {
		t.Errorf("symlinks = %q, want .env,storage/app (blank entry dropped)", got)
	}
	if got := strings.Join(d.PostCreate, ","); got != "composer install" {
		t.Errorf("post_create = %q", got)
	}
	if d.Env["FOO"] != "bar" || d.Env["BAZ"] != "qux" || len(d.Env) != 2 {
		t.Errorf("env = %v, want FOO=bar BAZ=qux", d.Env)
	}
	if got := strings.Join(d.MatchLabels, ","); got != "lbl-uuid-1,lbl-uuid-2" {
		t.Errorf("match_labels = %q", got)
	}
	if d.MatchMode != "all" || d.DedupMode != "label" {
		t.Errorf("match_mode/dedup_mode = %q/%q, want all/label", d.MatchMode, d.DedupMode)
	}
	if d.OnSentSetLabel != "lbl-sent" || d.BlockedLabelID != "lbl-blocked" {
		t.Errorf("label UUIDs = %q/%q", d.OnSentSetLabel, d.BlockedLabelID)
	}
	if got := strings.Join(d.PrioritySort, ","); got != "priority,createdAt" {
		t.Errorf("priority_sort = %q", got)
	}
}

// The two mode fields cycle a fixed set rather than accepting free text, and
// lead with an unset value so [defaults] can stay silent and let the built-in
// fallback apply.
func TestSettingsFormModeFieldsCycle(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	for _, tc := range []struct {
		key  string
		want []string
	}{
		{"def_match_mode", []string{"", "any", "all"}},
		{"def_dedup_mode", []string{"", "label", "seen", "state"}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			fld := f.field(tc.key)
			if fld.kind != sfEnum {
				t.Fatalf("%s must be a cycle field, got kind %v", tc.key, fld.kind)
			}
			if fld.text != "" {
				t.Errorf("%s must pre-fill unset, got %q", tc.key, fld.text)
			}
			focusField(t, f, tc.key)
			// space steps through every option and wraps back to unset.
			for _, want := range append(tc.want[1:], "") {
				f.update(keyMsg(" "))
				if fld.text != want {
					t.Fatalf("%s cycled to %q, want %q", tc.key, fld.text, want)
				}
			}
			// Typing must not corrupt a cycle field.
			f.update(keyMsg("z"))
			if fld.text != "" {
				t.Errorf("typing must not edit %s, got %q", tc.key, fld.text)
			}
		})
	}
}

// A list field opens into a one-entry-per-line sub-editor: enter opens it and
// adds lines, typing edits the focused line, and esc closes the editor WITHOUT
// cancelling the whole form.
func TestSettingsFormListSubEditor(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, "def_symlinks")

	if ev := f.update(keyMsg("enter")); ev != settingsFormNone || !f.editing {
		t.Fatalf("enter must open the list for editing (ev=%v editing=%v)", ev, f.editing)
	}
	for _, r := range ".env" {
		f.update(keyMsg(string(r)))
	}
	f.update(keyMsg("enter")) // second line
	for _, r := range "vendor" {
		f.update(keyMsg(string(r)))
	}
	if got := f.field("def_symlinks").lines; len(got) != 2 || got[0] != ".env" || got[1] != "vendor" {
		t.Fatalf("list lines = %q, want [.env vendor]", got)
	}
	// Backspace on an empty line removes it rather than editing the one above.
	f.update(keyMsg("enter"))
	f.update(keyMsg("backspace"))
	if got := f.field("def_symlinks").lines; len(got) != 2 {
		t.Errorf("backspace on a blank line must drop it, got %q", got)
	}

	// esc closes the sub-editor; it must NOT cancel the settings form.
	if ev := f.update(keyMsg("esc")); ev != settingsFormNone || f.editing {
		t.Fatalf("esc must close the list, not cancel the form (ev=%v editing=%v)", ev, f.editing)
	}
	// esc again, now in field navigation, does cancel.
	if ev := f.update(keyMsg("esc")); ev != settingsFormCancel {
		t.Errorf("esc in field navigation must cancel, got %v", ev)
	}
}

func TestSettingsFormBoolToggleViaKeys(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	focusField(t, f, "cr_enabled")
	before := f.cur().b
	f.update(keyMsg(" "))
	if f.cur().b == before {
		t.Error("space must toggle a bool field")
	}
}

func TestSettingsFormRejectsBadInt(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.field("review_timeout").text = "abc"

	if ev := f.save(); ev != settingsFormNone || f.err == "" {
		t.Fatalf("bad int must abort save with an error, got ev=%v err=%q", ev, f.err)
	}
	// Config on disk is untouched (review still disabled from newTestRoot).
	reloaded, _ := config.Load(m.cfgPath)
	if reloaded.Review.Enabled {
		t.Error("a rejected save must not have written anything")
	}
}

func TestSettingsFormRejectsZeroGlobalCap(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.field("global_cap").text = "0"

	if ev := f.save(); ev != settingsFormNone || f.err == "" {
		t.Fatalf("global_cap <= 0 must abort save, got ev=%v err=%q", ev, f.err)
	}
	// The in-memory config must be rolled back to the valid prior value.
	if m.cfg.Defaults.GlobalCap != 4 {
		t.Errorf("rejected save must restore global_cap, got %d", m.cfg.Defaults.GlobalCap)
	}
}

// A save rejected by Validate rolls back the project-fallback keys too — the
// slice and map members of [defaults] included, which the value-copy snapshot
// only covers because save REPLACES them rather than mutating in place.
func TestSettingsFormRejectedSaveRollsBackProjectDefaults(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.Symlinks = []string{"original.env"}
	m.cfg.Defaults.Env = map[string]string{"KEEP": "me"}
	m.cfg.Defaults.BranchPrefix = "keep/"

	f := newSettingsForm(m.cfgPath, m.cfg)
	f.field("def_symlinks").lines = []string{"clobbered"}
	f.field("def_env").lines = []string{"NEW=value"}
	f.field("def_branch_prefix").text = "clobbered/"
	// Only reachable by injection — the picker cannot produce it — so Validate
	// rejects the save after every field above has already been applied.
	f.field("def_dedup_mode").text = "sometimes"

	if ev := f.save(); ev != settingsFormNone || f.err == "" {
		t.Fatalf("a bad dedup_mode must abort save with an error, got ev=%v err=%q", ev, f.err)
	}
	d := m.cfg.Defaults
	if got := strings.Join(d.Symlinks, ","); got != "original.env" {
		t.Errorf("rejected save must restore defaults.symlinks, got %q", got)
	}
	if d.Env["KEEP"] != "me" || len(d.Env) != 1 {
		t.Errorf("rejected save must restore defaults.env, got %v", d.Env)
	}
	if d.BranchPrefix != "keep/" {
		t.Errorf("rejected save must restore defaults.branch_prefix, got %q", d.BranchPrefix)
	}
	if d.DedupMode != "" {
		t.Errorf("rejected save must restore defaults.dedup_mode, got %q", d.DedupMode)
	}
	reloaded, _ := config.Load(m.cfgPath)
	if reloaded.Defaults.BranchPrefix == "clobbered/" {
		t.Error("a rejected save must not persist anything")
	}
}

func TestSettingsFormOnlyDigitsInIntField(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, "global_cap")
	f.cur().text = ""
	f.update(keyMsg("5"))
	f.update(keyMsg("x")) // non-digit ignored in an int field
	f.update(keyMsg("2"))
	if got := f.cur().text; got != "52" {
		t.Errorf("int field must accept only digits, got %q", got)
	}
}

// The two CodeRabbit features share ONE tab as two distinct, indented
// subsections — so they read as one integration but are never mistaken for each
// other. No top-level section header: the tab title already says CodeRabbit,
// and each subsection names its own config table.
func TestSettingsFormDistinguishesReviewAndCoderabbit(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	rv := f.field("review_enabled")
	cr := f.field("cr_enabled")

	if rv.tab != stCodeRabbit || cr.tab != stCodeRabbit {
		t.Errorf("both features belong on the CodeRabbit tab, got %v / %v", rv.tab, cr.tab)
	}
	if rv.section != "" || cr.section != "" {
		t.Errorf("the tab title replaces the section header, got %q / %q", rv.section, cr.section)
	}
	// Both are indented under distinct subsections naming their own table.
	if rv.subsection == "" || cr.subsection == "" || rv.subsection == cr.subsection {
		t.Errorf("each feature needs a distinct subsection, got %q / %q", rv.subsection, cr.subsection)
	}
	if !strings.Contains(rv.subsection, "[review]") || !strings.Contains(cr.subsection, "[coderabbit]") {
		t.Errorf("each subsection must name its config table, got %q / %q", rv.subsection, cr.subsection)
	}
	if !rv.indent || !cr.indent {
		t.Error("both CodeRabbit subsections must be indented under the tab")
	}

	f.tab = stCodeRabbit
	out := f.view()
	if !strings.Contains(out, rv.subsection) || !strings.Contains(out, cr.subsection) {
		t.Errorf("both subsections must render:\n%s", out)
	}
}

// The [defaults].agent picker is a cycle field: it pre-fills "claude" for an
// unset value, steps claude→codex→opencode (wrapping) on space AND enter, and
// round-trips a non-default selection to config.toml.
func TestSettingsFormAgentPickerCyclesAndSaves(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	af := f.field("agent")
	if af == nil {
		t.Fatal("agent field missing from [defaults]")
	}
	if af.kind != sfEnum {
		t.Errorf("agent must be a cycle field, got kind %v", af.kind)
	}
	if af.text != "claude" {
		t.Errorf("agent prefill = %q, want claude (effective default when unset)", af.text)
	}

	// space cycles claude → codex; enter cycles codex → opencode; space wraps.
	focusField(t, f, "agent")
	f.update(keyMsg(" "))
	if af.text != "codex" {
		t.Fatalf("space must cycle to codex, got %q", af.text)
	}
	f.update(keyMsg("enter"))
	if af.text != "opencode" {
		t.Fatalf("enter must cycle to opencode, got %q", af.text)
	}
	f.update(keyMsg(" "))
	if af.text != "claude" {
		t.Fatalf("cycle must wrap back to claude, got %q", af.text)
	}
	// A stray keystroke must not corrupt the selection.
	f.update(keyMsg("z"))
	f.update(keyMsg("backspace"))
	if af.text != "claude" {
		t.Errorf("typing/backspace must not edit a cycle field, got %q", af.text)
	}

	// Select codex and save: it persists to disk.
	af.text = "codex"
	if ev := f.save(); ev != settingsFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Defaults.Agent != "codex" {
		t.Errorf("defaults.agent = %q, want codex", reloaded.Defaults.Agent)
	}

	// It renders as a cycle affordance, not a typed value.
	focusField(t, f, "agent")
	if out := f.view(); !strings.Contains(out, "Coding agent") || !strings.Contains(out, "codex") {
		t.Errorf("agent picker must render its label and value:\n%s", out)
	}
}

// Selecting the effective default (claude) persists as an empty value so the
// on-disk config stays unpinned — empty resolves back to claude at read time.
func TestSettingsFormAgentDefaultStaysUnpinned(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.Agent = "codex" // start pinned so we can watch it clear
	f := newSettingsForm(m.cfgPath, m.cfg)
	if got := f.field("agent").text; got != "codex" {
		t.Fatalf("agent prefill = %q, want codex", got)
	}
	f.field("agent").text = "claude" // back to the default
	if ev := f.save(); ev != settingsFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	reloaded, _ := config.Load(m.cfgPath)
	if reloaded.Defaults.Agent != "" {
		t.Errorf("choosing the default must persist empty, got %q", reloaded.Defaults.Agent)
	}
	if got := reloaded.AgentForProject("nori-app"); got != "claude" {
		t.Errorf("an empty default must resolve to claude, got %q", got)
	}
}

// A value outside claude|codex|opencode (only reachable by injection — the
// picker can't produce it) is rejected by c.Validate() on save and leaves both
// the in-memory and on-disk config untouched.
func TestSettingsFormRejectsBadAgent(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.field("agent").text = "gpt5"

	if ev := f.save(); ev != settingsFormNone || f.err == "" {
		t.Fatalf("a bad agent must abort save with an error, got ev=%v err=%q", ev, f.err)
	}
	if m.cfg.Defaults.Agent == "gpt5" {
		t.Error("a rejected save must roll back the in-memory agent value")
	}
	reloaded, _ := config.Load(m.cfgPath)
	if reloaded.Defaults.Agent != "" {
		t.Errorf("a rejected save must not persist the bad value, got %q", reloaded.Defaults.Agent)
	}
}

// In a viewport too short for the active tab's fields, the modal scrolls to keep
// the focused field visible and ALWAYS pins the tab strip and the footer (help +
// key hint) — so neither the tab hint nor ctrl-s/esc is ever clipped on a short
// terminal. Project defaults is the tallest tab (four expanded list fields).
func TestSettingsFormScrollsToCursor(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	const hint = "ctrl-s save"
	const budget = 14 // fewer rows than the ~16-row field region
	f.tab = stProjectDefaults

	// Cursor at the very top: the first field shows, a "↓ more" marker hints at the
	// rest, and both the strip and the hint are present.
	focusField(t, f, "def_branch_prefix")
	top := f.scrolledView(budget)
	if !strings.Contains(top, "Branch prefix") || !strings.Contains(top, "↓ more") || !strings.Contains(top, hint) {
		t.Errorf("top view must show the first field, a down-marker, and the hint:\n%s", top)
	}
	if !strings.Contains(top, "Project defaults") {
		t.Errorf("the tab strip must stay pinned:\n%s", top)
	}

	// Cursor at the bottom of the tab: it must be visible, an "↑ more" marker
	// present, and the hint still pinned.
	focusField(t, f, "def_priority_sort")
	bot := f.scrolledView(budget)
	if !strings.Contains(bot, "Priority sort") {
		t.Errorf("scrolled view must reveal the focused field:\n%s", bot)
	}
	if !strings.Contains(bot, "↑ more") {
		t.Errorf("scrolling past the top must show an up-marker:\n%s", bot)
	}
	if !strings.Contains(bot, hint) || !strings.Contains(bot, "Project defaults") {
		t.Errorf("the tab strip and key hint must stay pinned regardless of scroll:\n%s", bot)
	}

	// The rendered body never exceeds the budget (so box never clips it).
	nLines := len(strings.Split(strings.TrimRight(bot, "\n"), "\n"))
	if nLines > budget+2 { // +2 for the liftable title + blank
		t.Errorf("scrolled body = %d lines, must fit budget %d (+2 title)", nLines, budget)
	}
}

// Toggling a feature's `enabled` master switch ON also flips its dependent sinks
// ON, mirroring the config resolution (`enabled = true` alone defaults them on).
// This prevents the silent trap where enabling [coderabbit] in the TUI left
// send_to_agent = false, so the watch surfaced nothing to the agent.
func TestSettingsFormEnablingFlipsDependentSinks(t *testing.T) {
	m := newTestRoot(t) // config has no [coderabbit]/[review]/[brain] → all off
	cases := []struct {
		master string
		deps   []string
	}{
		{"cr_enabled", []string{"cr_notify", "cr_send"}},
		{"review_enabled", []string{"review_onpropen", "review_send"}},
		{"brain_enabled", []string{"brain_esc", "brain_appr"}},
	}
	for _, tc := range cases {
		t.Run(tc.master, func(t *testing.T) {
			f := newSettingsForm(m.cfgPath, m.cfg)
			mf := f.field(tc.master)
			if mf.b {
				t.Fatalf("%s should start off", tc.master)
			}
			for _, d := range tc.deps {
				if f.field(d).b {
					t.Fatalf("%s should start off", d)
				}
			}
			f.toggleBool(mf) // OFF → ON
			for _, d := range tc.deps {
				if !f.field(d).b {
					t.Errorf("enabling %s must flip %s ON", tc.master, d)
				}
			}
			// Turning the master back OFF leaves the sinks as-is (not force-reset).
			f.toggleBool(mf)
			for _, d := range tc.deps {
				if !f.field(d).b {
					t.Errorf("disabling %s must NOT reset %s", tc.master, d)
				}
			}
		})
	}
}
