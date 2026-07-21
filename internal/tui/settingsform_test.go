package tui

// Tests for the global settings editor (settingsform.go): the tab strip and the
// field filtering it drives, field pre-fill, bool toggles + text/int editing,
// the list/env sub-editor, the workspace-label pickers and their raw-UUID
// fallback, a persisted save across all five tables, the invalid-input guards
// (non-numeric, global_cap <= 0) that abort WITHOUT mutating config, and the
// [defaults] project-fallback keys round-tripping to config.toml.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

// loadFakeLabels drives one workspace-label load through the same seam the
// tea.Cmd feeds — api.WorkspaceLabels then applyWorkspaceLabels — so the tests
// exercise the real fold-in path rather than assigning f.wsLabels directly.
func loadFakeLabels(t *testing.T, f *settingsForm, fake *linear.Fake) {
	t.Helper()
	labels, err := fake.WorkspaceLabels(context.Background())
	f.applyWorkspaceLabels(workspaceLabelsMsg{labels: labels, err: err})
}

// fakeWorkspace is the label fixture: a flat label plus a grouped one, so the
// "parent / child" rendering is covered too.
func fakeWorkspace() *linear.Fake {
	return &linear.Fake{WorkspaceLabelSet: []linear.Label{
		{ID: "ws-ready", Name: "agent-ready"},
		{ID: "ws-blocked", Name: "blocked"},
		{ID: "ws-child", Name: "urgent", Parent: &linear.Label{ID: "ws-parent", Name: "triage"}},
	}}
}

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
	// The watch provider's author pre-fills the effective default when unset.
	if got := f.field("pv_watch_author").text; got != config.DefaultCodeRabbitAuthor {
		t.Errorf("pv_watch_author prefill = %q, want %q", got, config.DefaultCodeRabbitAuthor)
	}

	// Turn the watch on, set a custom author, and enable notify + send.
	f.field("pv_watch_enabled").b = true
	f.field("pv_watch_notify").b = true
	f.field("pv_watch_send").b = true
	f.field("pv_watch_author").text = "sonarcloud"
	// Also enable the coderabbit-cli provider and bump the global cap.
	f.field("pv_cli_enabled").b = true
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
	// The two enabled providers round-trip into the catalog; the legacy tables
	// stay empty (catalog-only).
	if reloaded.Review != (config.ReviewConfig{}) || reloaded.CodeRabbit != (config.CodeRabbitConfig{}) {
		t.Errorf("legacy tables must stay empty in catalog mode: %+v / %+v", reloaded.Review, reloaded.CodeRabbit)
	}
	byKind := map[string]config.ReviewProvider{}
	for _, p := range reloaded.ReviewProviders {
		byKind[p.KindString()] = p
	}
	watch, ok := byKind["coderabbit-watch"]
	if !ok || !watch.Enabled || watch.Author != "sonarcloud" || !watch.Notify || !watch.SendToAgent {
		t.Errorf("watch provider not persisted: %+v (ok=%v)", watch, ok)
	}
	if cli, ok := byKind["coderabbit-cli"]; !ok || !cli.Enabled {
		t.Errorf("coderabbit-cli provider must persist enabled: %+v (ok=%v)", cli, ok)
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
	_, _ = f.update(keyMsg("tab"))
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
	_, _ = f.update(shiftTabKey())
	if f.tab != stDefaults {
		t.Fatalf("shift+tab must go back to Defaults, got %v", f.tab)
	}
	_, _ = f.update(shiftTabKey())
	if f.tab != stCodeRabbit {
		t.Fatalf("shift+tab must wrap onto the last tab, got %v", f.tab)
	}
	// right/left are aliases, and wrap forwards off the last tab.
	_, _ = f.update(keyMsg("right"))
	if f.tab != stDefaults {
		t.Fatalf("right must wrap onto the first tab, got %v", f.tab)
	}
	_, _ = f.update(keyMsg("left"))
	if f.tab != stCodeRabbit {
		t.Fatalf("left must wrap back onto the last tab, got %v", f.tab)
	}

	// Switching tabs resets the cursor, so it can never point past a shorter
	// field list, and ↓ stops at the end of the ACTIVE tab.
	f.tab, f.cursor = stCodeRabbit, 9
	_, _ = f.update(keyMsg("tab"))
	if f.cursor != 0 {
		t.Errorf("switching tabs must reset the cursor, got %d", f.cursor)
	}
	for range 20 {
		_, _ = f.update(keyMsg("down"))
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
				_, _ = f.update(keyMsg(" "))
				if fld.text != want {
					t.Fatalf("%s cycled to %q, want %q", tc.key, fld.text, want)
				}
			}
			// Typing must not corrupt a cycle field.
			_, _ = f.update(keyMsg("z"))
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

	if _, ev := f.update(keyMsg("enter")); ev != settingsFormNone || !f.editing {
		t.Fatalf("enter must open the list for editing (ev=%v editing=%v)", ev, f.editing)
	}
	for _, r := range ".env" {
		_, _ = f.update(keyMsg(string(r)))
	}
	_, _ = f.update(keyMsg("enter")) // second line
	for _, r := range "vendor" {
		_, _ = f.update(keyMsg(string(r)))
	}
	if got := f.field("def_symlinks").lines; len(got) != 2 || got[0] != ".env" || got[1] != "vendor" {
		t.Fatalf("list lines = %q, want [.env vendor]", got)
	}
	// Backspace on an empty line removes it rather than editing the one above.
	_, _ = f.update(keyMsg("enter"))
	_, _ = f.update(keyMsg("backspace"))
	if got := f.field("def_symlinks").lines; len(got) != 2 {
		t.Errorf("backspace on a blank line must drop it, got %q", got)
	}

	// esc closes the sub-editor; it must NOT cancel the settings form.
	if _, ev := f.update(keyMsg("esc")); ev != settingsFormNone || f.editing {
		t.Fatalf("esc must close the list, not cancel the form (ev=%v editing=%v)", ev, f.editing)
	}
	// esc again, now in field navigation, does cancel.
	if _, ev := f.update(keyMsg("esc")); ev != settingsFormCancel {
		t.Errorf("esc in field navigation must cancel, got %v", ev)
	}
}

func TestSettingsFormBoolToggleViaKeys(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	focusField(t, f, "pv_watch_enabled")
	before := f.cur().b
	_, _ = f.update(keyMsg(" "))
	if f.cur().b == before {
		t.Error("space must toggle a bool field")
	}
}

func TestSettingsFormRejectsBadInt(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.field("pv_cli_timeout").text = "abc"

	if ev := f.save(); ev != settingsFormNone || f.err == "" {
		t.Fatalf("bad int must abort save with an error, got ev=%v err=%q", ev, f.err)
	}
	// Config on disk is untouched (no providers written from newTestRoot).
	reloaded, _ := config.Load(m.cfgPath)
	if len(reloaded.ReviewProviders) != 0 {
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
	_, _ = f.update(keyMsg("5"))
	_, _ = f.update(keyMsg("x")) // non-digit ignored in an int field
	_, _ = f.update(keyMsg("2"))
	if got := f.cur().text; got != "52" {
		t.Errorf("int field must accept only digits, got %q", got)
	}
}

// The three review provider KINDS share ONE tab as distinct, indented
// subsections — so they read as one integration but are never mistaken for each
// other. No top-level section header: the tab title already says Review, and
// each subsection names its own provider kind.
func TestSettingsFormDistinguishesReviewProviders(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	cli := f.field("pv_cli_enabled")
	watch := f.field("pv_watch_enabled")
	claude := f.field("pv_claude_enabled")

	for _, fld := range []*setField{cli, watch, claude} {
		if fld.tab != stCodeRabbit {
			t.Errorf("every provider belongs on the Review tab, got %v for %q", fld.tab, fld.key)
		}
		if fld.section != "" {
			t.Errorf("the tab title replaces the section header, got %q for %q", fld.section, fld.key)
		}
		if !fld.indent {
			t.Errorf("every provider subsection must be indented, got %q not indented", fld.key)
		}
	}
	// Distinct subsections, each naming its own provider kind.
	if cli.subsection == watch.subsection || cli.subsection == claude.subsection || watch.subsection == claude.subsection {
		t.Errorf("each provider needs a distinct subsection, got %q / %q / %q", cli.subsection, watch.subsection, claude.subsection)
	}
	if !strings.Contains(cli.subsection, "coderabbit-cli") ||
		!strings.Contains(watch.subsection, "coderabbit-watch") ||
		!strings.Contains(claude.subsection, "claude-session") {
		t.Errorf("each subsection must name its kind, got %q / %q / %q", cli.subsection, watch.subsection, claude.subsection)
	}

	f.tab = stCodeRabbit
	out := f.view()
	if !strings.Contains(out, cli.subsection) || !strings.Contains(out, watch.subsection) || !strings.Contains(out, claude.subsection) {
		t.Errorf("all three subsections must render:\n%s", out)
	}
}

// The watch provider forbids the github transport (its feedback is already on
// the PR) and offers no fallback (it cannot classify quota); the pass providers
// offer both. The editor reflects those constraints in its choice sets.
func TestSettingsFormWatchOmitsGithubAndFallback(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	tokens := func(opts []setPickOpt) []string {
		out := make([]string, len(opts))
		for i, o := range opts {
			out[i] = o.id
		}
		return out
	}
	if got := tokens(f.field("pv_cli_transports").choices); !slices.Contains(got, "github") {
		t.Errorf("coderabbit-cli transports must offer github, got %v", got)
	}
	if got := tokens(f.field("pv_watch_transports").choices); slices.Contains(got, "github") {
		t.Errorf("coderabbit-watch transports must NOT offer github, got %v", got)
	}
	if f.field("pv_watch_transports").choices == nil {
		t.Error("watch must still offer a transports picker (lola/linear)")
	}
	// The watch has no fallback field at all.
	if f.field("pv_watch_fallback") != nil {
		t.Error("coderabbit-watch must not have a fallback field")
	}
	// A pass provider's fallback offers the OTHER pass kind, never itself or the watch.
	if got := tokens(f.field("pv_cli_fallback").choices); !slices.Equal(got, []string{"claude-session"}) {
		t.Errorf("coderabbit-cli fallback must offer [claude-session], got %v", got)
	}
	if got := tokens(f.field("pv_claude_fallback").choices); !slices.Equal(got, []string{"coderabbit-cli"}) {
		t.Errorf("claude-session fallback must offer [coderabbit-cli], got %v", got)
	}
}

// A legacy [review]/[coderabbit] config renders the Review tab read-only and
// offers an in-place migration into the editable provider catalog. After
// migrating, the same providers are present, editable, and the legacy tables
// are cleared on disk.
func TestSettingsFormLegacyReviewMigrates(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Review = config.ReviewConfig{Enabled: true, Command: "coderabbit review", OnPROpen: true, SendToAgent: true, TimeoutSeconds: 300}
	m.cfg.CodeRabbit = config.CodeRabbitConfig{Enabled: true, Author: "coderabbitai", Notify: true, SendToAgent: true}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	f := newSettingsForm(m.cfgPath, m.cfg)
	if !f.reviewLegacy {
		t.Fatal("a legacy config must open the Review tab read-only")
	}
	// Edits are suppressed while read-only.
	f.tab = stCodeRabbit
	focusField(t, f, "pv_cli_enabled")
	before := f.field("pv_cli_enabled").b
	_, _ = f.update(keyMsg(" "))
	if f.field("pv_cli_enabled").b != before {
		t.Error("read-only Review tab must not toggle a field")
	}
	// m migrates.
	if _, ev := f.update(keyMsg("m")); ev != settingsFormNone {
		t.Fatalf("m must run the migration in place, got ev=%v err=%q", ev, f.err)
	}
	if f.reviewLegacy {
		t.Fatalf("migration must clear read-only mode, err=%q", f.err)
	}
	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Review != (config.ReviewConfig{}) || reloaded.CodeRabbit != (config.CodeRabbitConfig{}) {
		t.Errorf("migration must clear the legacy tables, got %+v / %+v", reloaded.Review, reloaded.CodeRabbit)
	}
	if len(reloaded.ReviewProviders) != 2 {
		t.Errorf("migration must synthesize both providers, got %d", len(reloaded.ReviewProviders))
	}
	// The editor now reflects the catalog and is editable.
	if !f.field("pv_cli_enabled").b || !f.field("pv_watch_enabled").b {
		t.Error("migrated providers must show enabled in the editor")
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
	_, _ = f.update(keyMsg(" "))
	if af.text != "codex" {
		t.Fatalf("space must cycle to codex, got %q", af.text)
	}
	_, _ = f.update(keyMsg("enter"))
	if af.text != "opencode" {
		t.Fatalf("enter must cycle to opencode, got %q", af.text)
	}
	_, _ = f.update(keyMsg(" "))
	if af.text != "claude" {
		t.Fatalf("cycle must wrap back to claude, got %q", af.text)
	}
	// A stray keystroke must not corrupt the selection.
	_, _ = f.update(keyMsg("z"))
	_, _ = f.update(keyMsg("backspace"))
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
		{"pv_cli_enabled", []string{"pv_cli_onpropen", "pv_cli_notify", "pv_cli_send"}},
		{"pv_watch_enabled", []string{"pv_watch_notify", "pv_watch_send"}},
		{"pv_claude_enabled", []string{"pv_claude_onpropen", "pv_claude_notify", "pv_claude_send"}},
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

// Paste arrives as its own tea.PasteMsg in bubbletea v2, so the settings editor
// has to be routed it explicitly or pasting silently does nothing.
func TestSettingsFormPaste(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	// Single-line field: first non-blank line, trailing newline dropped.
	focusField(t, f, "def_branch_prefix")
	f.paste("feat/\n")
	if got := f.field("def_branch_prefix").text; got != "feat/" {
		t.Errorf("branch prefix = %q, want feat/", got)
	}

	// Open list editor: a multi-line paste becomes multiple entries.
	focusField(t, f, "def_symlinks")
	f.openList(f.cur())
	f.paste(".env\nstorage/app\n")
	if got := f.field("def_symlinks").lines; len(got) != 2 || got[0] != ".env" || got[1] != "storage/app" {
		t.Errorf("symlinks = %v, want [.env storage/app]", got)
	}
	f.editing = false

	// Int field: digits only.
	focusField(t, f, "global_cap")
	f.field("global_cap").text = ""
	f.paste("cap 8")
	if got := f.field("global_cap").text; got != "8" {
		t.Errorf("global_cap = %q, want 8", got)
	}
}

// The three [defaults] label fields pick from WORKSPACE labels — organisation
// labels with no team, which exist across every team. A [defaults] value is
// inherited by projects on any team, so a team label there could never match.
// The help must say so and must NOT carry the old "rejected on save if projects
// span teams" claim, which config.Validate no longer enforces.
func TestSettingsFormLabelFieldsAreWorkspaceScoped(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	for _, key := range []string{"def_match_labels", "def_on_sent_set_label", "def_blocked_label_id"} {
		fld := f.field(key)
		if fld == nil {
			t.Fatalf("field %q missing", key)
		}
		if !fld.wsPick {
			t.Errorf("%s must offer the workspace-label picker", key)
		}
		if !strings.Contains(fld.help, "Workspace label") {
			t.Errorf("%s help must say these are workspace labels, got %q", key, fld.help)
		}
		for _, stale := range []string{"team-scoped", "Rejected on save", "several teams"} {
			if strings.Contains(fld.help, stale) {
				t.Errorf("%s help still carries the withdrawn %q claim: %q", key, stale, fld.help)
			}
		}
	}
}

// Opening the form must never touch Linear: it has to work with no API key and
// offline. The load is lazy — nothing is fetched until a picker is opened.
func TestSettingsFormDoesNotLoadLabelsOnOpen(t *testing.T) {
	m := newTestRoot(t)
	fake := fakeWorkspace()
	f := newSettingsForm(m.cfgPath, m.cfg)

	if len(fake.CallLog()) != 0 {
		t.Errorf("opening the settings form must not call Linear, got %v", fake.CallNames())
	}
	if f.wsTried || f.wsLoading || len(f.wsLabels) != 0 {
		t.Errorf("workspace labels must start unloaded (tried=%v loading=%v n=%d)",
			f.wsTried, f.wsLoading, len(f.wsLabels))
	}
	// Every field is still editable with no labels loaded.
	focusField(t, f, "def_on_sent_set_label")
	f.paste("manual-uuid")
	if got := f.field("def_on_sent_set_label").text; got != "manual-uuid" {
		t.Errorf("field must be editable offline, got %q", got)
	}
}

// A loaded workspace-label set populates the picker: multi-select for
// def_match_labels, space toggles entries, and confirming writes the chosen IDs
// back in OPTION order (not toggle order) ready for save.
func TestSettingsFormWorkspaceLabelPickerMultiSelect(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	loadFakeLabels(t, f, fakeWorkspace())

	focusField(t, f, "def_match_labels")
	if _, ev := f.update(keyMsg("enter")); ev != settingsFormNone || f.picker == nil {
		t.Fatalf("enter must open the picker (ev=%v picker=%v)", ev, f.picker)
	}
	p := f.picker
	if !p.multi {
		t.Error("def_match_labels must be a multi-select")
	}
	// Options come straight from the fake, grouped labels as "parent / child".
	if len(p.opts) != 3 {
		t.Fatalf("picker opts = %d, want 3 workspace labels", len(p.opts))
	}
	if p.opts[0].label != "agent-ready" || p.opts[2].label != "triage / urgent" {
		t.Errorf("picker must render label names, got %q / %q", p.opts[0].label, p.opts[2].label)
	}
	if out := f.view(); !strings.Contains(out, "agent-ready") || !strings.Contains(out, "triage / urgent") {
		t.Errorf("picker view must list the labels:\n%s", out)
	}

	// Toggle the third then the first, so option order (not toggle order) is
	// what lands.
	f.picker.cursor = 2
	_, _ = f.update(keyMsg(" "))
	f.picker.cursor = 0
	_, _ = f.update(keyMsg(" "))
	if _, ev := f.update(keyMsg("enter")); ev != settingsFormNone || f.picker != nil {
		t.Fatalf("enter must confirm and close the picker (ev=%v)", ev)
	}
	if got := f.field("def_match_labels").lines; len(got) != 2 || got[0] != "ws-ready" || got[1] != "ws-child" {
		t.Fatalf("picked IDs = %q, want [ws-ready ws-child] in option order", got)
	}

	// The picked IDs round-trip through save into config.Defaults.
	if ev := f.save(); ev != settingsFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := strings.Join(reloaded.Defaults.MatchLabels, ","); got != "ws-ready,ws-child" {
		t.Errorf("defaults.match_labels = %q, want ws-ready,ws-child", got)
	}
}

// The two single-value label fields lead with "(none)" so a set label can be
// cleared, and pre-select whatever the field already holds.
func TestSettingsFormWorkspaceLabelPickerSingleSelect(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	loadFakeLabels(t, f, fakeWorkspace())

	focusField(t, f, "def_blocked_label_id")
	_, _ = f.update(keyMsg("enter"))
	p := f.picker
	if p == nil || p.multi {
		t.Fatalf("def_blocked_label_id must open a single-select picker, got %v", p)
	}
	if p.opts[0].id != "" || p.opts[0].label != "(none)" {
		t.Errorf("single-select must lead with (none), got %q/%q", p.opts[0].id, p.opts[0].label)
	}
	// Choose "blocked" (option index 2: (none), agent-ready, blocked).
	p.cursor = 2
	_, _ = f.update(keyMsg("enter"))
	if got := f.field("def_blocked_label_id").text; got != "ws-blocked" {
		t.Fatalf("picked label = %q, want ws-blocked", got)
	}

	// Reopening pre-selects the current value and starts the cursor on it, so
	// confirming without moving is a no-op.
	_, _ = f.update(keyMsg("enter"))
	if f.picker.cursor != 2 {
		t.Errorf("picker must open on the current value, got cursor %d", f.picker.cursor)
	}
	_, _ = f.update(keyMsg("enter"))
	if got := f.field("def_blocked_label_id").text; got != "ws-blocked" {
		t.Errorf("confirming without moving must not change the value, got %q", got)
	}

	// (none) clears it.
	_, _ = f.update(keyMsg("enter"))
	f.picker.cursor = 0
	_, _ = f.update(keyMsg("enter"))
	if got := f.field("def_blocked_label_id").text; got != "" {
		t.Errorf("(none) must clear the label, got %q", got)
	}
}

// esc abandons the picker without writing anything back.
func TestSettingsFormWorkspaceLabelPickerEscapes(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	loadFakeLabels(t, f, fakeWorkspace())
	f.field("def_blocked_label_id").text = "keep-me"

	focusField(t, f, "def_blocked_label_id")
	_, _ = f.update(keyMsg("enter"))
	f.picker.cursor = 1
	if _, ev := f.update(keyMsg("esc")); ev != settingsFormNone || f.picker != nil {
		t.Fatalf("esc must close the picker, not cancel the form (ev=%v)", ev)
	}
	if got := f.field("def_blocked_label_id").text; got != "keep-me" {
		t.Errorf("esc must not write a selection, got %q", got)
	}
}

// When the fetch fails the field falls back to raw UUID entry and the footer
// says why — the user must never be unable to set the field.
func TestSettingsFormLabelFallbackOnFetchError(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	fake := fakeWorkspace()
	fake.Errs = map[string]error{"WorkspaceLabels": errors.New("no linear api key configured")}
	loadFakeLabels(t, f, fake)

	if len(f.wsLabels) != 0 || !f.wsTried {
		t.Fatalf("a failed load must leave no labels but mark the attempt (n=%d tried=%v)", len(f.wsLabels), f.wsTried)
	}
	if !strings.Contains(f.wsErr, "no linear api key") {
		t.Errorf("the reason must name the failure, got %q", f.wsErr)
	}

	// A single-value field edits inline, so it is already usable; the footer
	// explains why no picker appeared.
	focusField(t, f, "def_on_sent_set_label")
	if _, ev := f.update(keyMsg("enter")); ev != settingsFormNone || f.picker != nil {
		t.Fatalf("a failed load must not open an empty picker (ev=%v)", ev)
	}
	foot := strings.Join(f.footerLines(), "\n")
	if !strings.Contains(foot, "no linear api key") || !strings.Contains(foot, "type UUIDs manually") {
		t.Errorf("footer must explain the fallback:\n%s", foot)
	}
	f.paste("raw-uuid-1")
	if got := f.field("def_on_sent_set_label").text; got != "raw-uuid-1" {
		t.Errorf("raw UUID entry must still work, got %q", got)
	}

	// A multi-value field falls back to its one-UUID-per-line sub-editor.
	focusField(t, f, "def_match_labels")
	if _, ev := f.update(keyMsg("enter")); ev != settingsFormNone || !f.editing {
		t.Fatalf("enter must fall back to the line editor (ev=%v editing=%v)", ev, f.editing)
	}
	for _, r := range "raw-uuid-2" {
		_, _ = f.update(keyMsg(string(r)))
	}
	if got := f.field("def_match_labels").lines; len(got) != 1 || got[0] != "raw-uuid-2" {
		t.Errorf("raw list entry must still work, got %q", got)
	}
}

// An organisation with no workspace labels at all is a success with an empty
// set, not an error — it must still fall back rather than open an empty picker.
func TestSettingsFormLabelFallbackOnEmptyWorkspace(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	loadFakeLabels(t, f, &linear.Fake{}) // no WorkspaceLabelSet

	if !f.wsTried || f.wsErr == "" {
		t.Fatalf("an empty workspace must be recorded as tried with a reason (tried=%v err=%q)", f.wsTried, f.wsErr)
	}
	focusField(t, f, "def_blocked_label_id")
	if _, ev := f.update(keyMsg("enter")); ev != settingsFormNone || f.picker != nil {
		t.Fatalf("an empty workspace must not open a picker (ev=%v)", ev)
	}
	if !strings.Contains(f.wsErr, "no workspace labels") {
		t.Errorf("reason must name the empty workspace, got %q", f.wsErr)
	}
}

// A cold picker dispatches the async load exactly once and does not block: the
// first enter returns a tea.Cmd, and a second enter while it is in flight does
// not dispatch a duplicate.
func TestSettingsFormLabelLoadIsLazyAndDispatchedOnce(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	focusField(t, f, "def_match_labels")
	cmd, ev := f.update(keyMsg("enter"))
	if ev != settingsFormNone || cmd == nil {
		t.Fatalf("a cold picker must dispatch the load command (ev=%v cmd=%v)", ev, cmd)
	}
	if !f.wsLoading || f.picker != nil {
		t.Errorf("the load must be in flight with no picker yet (loading=%v)", f.wsLoading)
	}
	if again, _ := f.update(keyMsg("enter")); again != nil {
		t.Error("a second enter while loading must not dispatch a duplicate fetch")
	}

	// The result arrives as a plain tea.Msg through update, not a key press.
	if _, ev := f.update(workspaceLabelsMsg{labels: fakeWorkspace().WorkspaceLabelSet}); ev != settingsFormNone {
		t.Fatalf("the load result must fold in quietly, got ev=%v", ev)
	}
	if f.wsLoading || len(f.wsLabels) != 3 {
		t.Fatalf("labels must land (loading=%v n=%d)", f.wsLoading, len(f.wsLabels))
	}
	// The enter that started the load opens the picker itself once the labels
	// arrive — the user must not have to press it a second time.
	if f.picker == nil {
		t.Fatal("the completed load must open the picker the enter asked for")
	}
	if f.wsPendingKey != "" {
		t.Errorf("the pending request must be consumed, got %q", f.wsPendingKey)
	}
}

// The auto-open is tied to the field that asked. If focus moved while the load
// was in flight, the labels still land but no picker steals the new position.
func TestSettingsFormLabelAutoOpenOnlyForTheAskingField(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	focusField(t, f, "def_match_labels")
	if _, _ = f.update(keyMsg("enter")); f.wsPendingKey != "def_match_labels" {
		t.Fatalf("the asking field must be recorded, got %q", f.wsPendingKey)
	}
	// User moves on while the fetch is in flight.
	focusField(t, f, "def_branch_prefix")
	_, _ = f.update(workspaceLabelsMsg{labels: fakeWorkspace().WorkspaceLabelSet})

	if f.picker != nil {
		t.Error("a load that lands after focus moved must not open a picker")
	}
	if len(f.wsLabels) != 3 {
		t.Errorf("the labels must still be kept for the next open, got %d", len(f.wsLabels))
	}
	if f.wsPendingKey != "" {
		t.Errorf("the pending request must be cleared either way, got %q", f.wsPendingKey)
	}
	// Going back and pressing enter now opens instantly off the loaded set.
	focusField(t, f, "def_match_labels")
	if cmd, _ := f.update(keyMsg("enter")); cmd != nil || f.picker == nil {
		t.Errorf("the loaded set must open with no refetch (cmd=%v picker=%v)", cmd, f.picker)
	}
}

// A failed load that a field is still waiting on routes that field into its raw
// fallback, rather than leaving the enter with nothing to show for it.
func TestSettingsFormLabelAutoFallbackOnFailedLoad(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	focusField(t, f, "def_match_labels")
	_, _ = f.update(keyMsg("enter")) // dispatches, records the pending field
	_, _ = f.update(workspaceLabelsMsg{err: errors.New("offline")})

	if f.picker != nil {
		t.Error("a failed load must not open an empty picker")
	}
	if !f.editing {
		t.Error("an sfList field waiting on a failed load must open its line editor")
	}
	for _, r := range "raw-uuid" {
		_, _ = f.update(keyMsg(string(r)))
	}
	if got := f.field("def_match_labels").lines; len(got) != 1 || got[0] != "raw-uuid" {
		t.Errorf("the fallback editor must accept typing, got %q", got)
	}
}

// ctrl+r forces a live fetch past both the in-memory set and the disk cache, so
// a label added in Linear moments ago can be picked without waiting out the
// cache max-age.
func TestSettingsFormLabelRefreshBypassesCache(t *testing.T) {
	m := newTestRoot(t)
	if err := saveWorkspaceLabelCache(fakeWorkspace().WorkspaceLabelSet); err != nil {
		t.Fatalf("save cache: %v", err)
	}
	f := newSettingsForm(m.cfgPath, m.cfg)

	// A warm cache opens with no fetch...
	focusField(t, f, "def_match_labels")
	if cmd, _ := f.update(keyMsg("enter")); cmd != nil {
		t.Fatalf("a warm cache must not fetch, got cmd=%v", cmd)
	}
	if len(f.picker.opts) != 3 {
		t.Fatalf("cache must populate the picker, got %d opts", len(f.picker.opts))
	}
	// ...but ctrl+r from inside the picker forces one anyway.
	cmd, ev := f.update(keyMsg("ctrl+r"))
	if ev != settingsFormNone || cmd == nil {
		t.Fatalf("ctrl+r must dispatch a live fetch (ev=%v cmd=%v)", ev, cmd)
	}
	if f.picker != nil || len(f.wsLabels) != 0 || !f.wsLoading {
		t.Errorf("a refresh must drop the stale set while it reloads (picker=%v n=%d loading=%v)",
			f.picker, len(f.wsLabels), f.wsLoading)
	}
	// The fresh set reopens the picker on the field that asked.
	fresh := append(fakeWorkspace().WorkspaceLabelSet, linear.Label{ID: "ws-new", Name: "just-added"})
	_, _ = f.update(workspaceLabelsMsg{labels: fresh})
	if f.picker == nil || len(f.picker.opts) != 4 {
		t.Fatalf("the refreshed set must reopen the picker, got %v", f.picker)
	}
	if f.picker.opts[3].label != "just-added" {
		t.Errorf("the new label must be offered, got %q", f.picker.opts[3].label)
	}
}

// A cache older than wsLabelCacheMaxAge is ignored, so a stale file cannot mask
// a workspace whose labels have changed.
func TestSettingsFormWorkspaceLabelCacheExpires(t *testing.T) {
	m := newTestRoot(t)
	path, err := workspaceLabelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := wsLabelCache{
		FetchedAt: time.Now().Add(-wsLabelCacheMaxAge - time.Hour),
		Labels:    fakeWorkspace().WorkspaceLabelSet,
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadWorkspaceLabelCache(); err == nil {
		t.Error("a cache past the max age must be rejected")
	}
	// So opening the picker refetches rather than serving the stale set.
	f := newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, "def_match_labels")
	if cmd, _ := f.update(keyMsg("enter")); cmd == nil {
		t.Error("a stale cache must fall through to a live fetch")
	}
}

// The disk cache is the synchronous fast path: a warm cache populates the picker
// with no fetch at all, so reopening the editor never waits on Linear.
func TestSettingsFormWorkspaceLabelCacheWarmsPicker(t *testing.T) {
	m := newTestRoot(t) // sets LOLA_HOME to a temp dir, isolating the cache
	if err := saveWorkspaceLabelCache(fakeWorkspace().WorkspaceLabelSet); err != nil {
		t.Fatalf("save cache: %v", err)
	}

	f := newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, "def_match_labels")
	cmd, ev := f.update(keyMsg("enter"))
	if ev != settingsFormNone {
		t.Fatalf("enter = %v", ev)
	}
	if cmd != nil {
		t.Error("a warm cache must not dispatch a fetch")
	}
	if f.picker == nil || len(f.picker.opts) != 3 {
		t.Fatalf("the cache must populate the picker, got %v", f.picker)
	}
}

// A configured label renders as its NAME, not the bare UUID that is actually
// stored — a screen full of "13ad06f0-…" tells the user nothing.
func TestSettingsFormRendersLabelNames(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.MatchLabels = []string{"ws-ready"}
	m.cfg.Defaults.OnSentSetLabel = "ws-blocked"
	f := newSettingsForm(m.cfgPath, m.cfg)
	loadFakeLabels(t, f, fakeWorkspace())
	f.tab = stProjectDefaults

	view := stripANSI(f.view())
	if !strings.Contains(view, "agent-ready") {
		t.Errorf("the match label must render as its name:\n%s", view)
	}
	if !strings.Contains(view, "blocked") {
		t.Errorf("the on-sent label must render as its name:\n%s", view)
	}
	if strings.Contains(view, "ws-ready") {
		t.Errorf("the raw UUID must not be shown once the name is known:\n%s", view)
	}
}

// An unknown id — a hand-typed UUID, or one whose label was deleted — still
// shows verbatim rather than vanishing.
func TestSettingsFormRendersUnknownLabelVerbatim(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.MatchLabels = []string{"13ad06f0-a662-4ba4-a290-56e93f7d3c5d"}
	f := newSettingsForm(m.cfgPath, m.cfg)
	loadFakeLabels(t, f, fakeWorkspace())
	f.tab = stProjectDefaults

	if view := stripANSI(f.view()); !strings.Contains(view, "13ad06f0-a662-4ba4-a290-56e93f7d3c5d") {
		t.Errorf("an unresolvable id must still be shown:\n%s", view)
	}
}

// While a line is being typed it shows the RAW text: backspace acts on the
// stored value, so showing a resolved name there would be a lie.
func TestSettingsFormShowsRawWhileTypingLabel(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.MatchLabels = []string{"ws-ready"}
	f := newSettingsForm(m.cfgPath, m.cfg)
	loadFakeLabels(t, f, fakeWorkspace())
	focusField(t, f, "def_match_labels")
	f.openList(f.field("def_match_labels"))

	view := stripANSI(f.view())
	if !strings.Contains(view, "ws-ready") {
		t.Errorf("the line being edited must show its raw value:\n%s", view)
	}
}

// Landing on the project-defaults tab starts the label load, so stored IDs
// resolve to names without the user opening a picker first. Opening the FORM
// still loads nothing.
func TestSettingsFormLoadsLabelNamesOnTabSwitch(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	if f.wsLoading {
		t.Fatal("opening the form must not start a load")
	}

	// stDefaults -> stProjectDefaults
	cmd, _ := f.update(keyMsg("tab"))
	if f.tab != stProjectDefaults {
		t.Fatalf("tab = %v, want the project-defaults tab", f.tab)
	}
	if cmd == nil || !f.wsLoading {
		t.Fatalf("landing on the tab must start the label load (cmd=%v loading=%v)", cmd, f.wsLoading)
	}

	// Names land without popping a picker open — nothing asked for one.
	f.update(workspaceLabelsMsg{labels: fakeWorkspace().WorkspaceLabelSet})
	if f.picker != nil {
		t.Error("a name-only load must not open a picker")
	}
	if f.wsNames["ws-ready"] != "agent-ready" {
		t.Errorf("names must be recorded, got %v", f.wsNames)
	}

	// And moving away and back does not refetch.
	if again, _ := f.update(shiftTabKey()); again != nil {
		t.Error("leaving the tab must not fetch")
	}
	if again, _ := f.update(keyMsg("tab")); again != nil {
		t.Error("returning with labels already loaded must not refetch")
	}
}

// priority_sort is a tie-break CHAIN over lola's own sort keys — not Linear
// priorities, and nothing is fetched. Selection ORDER is the value: "priority
// then createdAt" and the reverse are different sorts.
func TestSettingsFormPrioritySortPickerKeepsOrder(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, "def_priority_sort")

	cmd, _ := f.update(keyMsg("enter"))
	if cmd != nil {
		t.Error("the sort picker must not fetch anything — the keys are lola's own")
	}
	if f.picker == nil || !f.picker.ordered {
		t.Fatalf("enter must open an ordered picker, got %+v", f.picker)
	}
	if len(f.picker.opts) != len(config.PrioritySortKeys) {
		t.Fatalf("picker offers %d keys, want %d", len(f.picker.opts), len(config.PrioritySortKeys))
	}

	// Select createdAt FIRST, then priority — the reverse of the default.
	f.picker.cursor = slices.Index(config.PrioritySortKeys, "createdAt")
	f.update(keyMsg("space"))
	f.picker.cursor = slices.Index(config.PrioritySortKeys, "priority")
	f.update(keyMsg("space"))
	f.update(keyMsg("enter"))

	got := f.field("def_priority_sort").lines
	if !slices.Equal(got, []string{"createdAt", "priority"}) {
		t.Errorf("lines = %v, want toggle order [createdAt priority]", got)
	}
}

// Deselecting removes a key from the chain without disturbing the rest.
func TestSettingsFormPrioritySortPickerDeselect(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.PrioritySort = []string{"priority", "createdAt"}
	f := newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, "def_priority_sort")
	f.update(keyMsg("enter"))

	f.picker.cursor = slices.Index(config.PrioritySortKeys, "priority")
	f.update(keyMsg("space")) // drop it
	f.update(keyMsg("enter"))

	if got := f.field("def_priority_sort").lines; !slices.Equal(got, []string{"createdAt"}) {
		t.Errorf("lines = %v, want [createdAt]", got)
	}
}

// The picker seeds from the configured chain, so confirming without touching
// anything is a no-op rather than a silent reset.
func TestSettingsFormPrioritySortPickerSeeds(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.PrioritySort = []string{"createdAt", "priority"}
	f := newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, "def_priority_sort")
	f.update(keyMsg("enter"))
	f.update(keyMsg("enter"))

	if got := f.field("def_priority_sort").lines; !slices.Equal(got, []string{"createdAt", "priority"}) {
		t.Errorf("lines = %v, want the configured chain unchanged", got)
	}
}

// The chosen chain round-trips to config through save.
func TestSettingsFormPrioritySortSaves(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, "def_priority_sort")
	f.update(keyMsg("enter"))
	f.picker.cursor = slices.Index(config.PrioritySortKeys, "createdAt")
	f.update(keyMsg("space"))
	f.update(keyMsg("enter"))

	if ev := f.save(); ev != settingsFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Defaults.PrioritySort; !slices.Equal(got, []string{"createdAt"}) {
		t.Errorf("persisted priority_sort = %v, want [createdAt]", got)
	}
}

// An EMPTY priority_sort is not "nothing" — SortIssues falls back to the
// built-in chain — so the field must show that fallback rather than a bare
// "(none)", which reads as "unsorted".
func TestSettingsFormEmptyPrioritySortShowsTheDefault(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.PrioritySort = nil
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.tab = stProjectDefaults

	view := stripANSI(f.view())
	want := strings.Join(config.DefaultPrioritySort, " → ")
	if !strings.Contains(view, want) {
		t.Errorf("an empty chain must show the default %q:\n%s", want, view)
	}
	if !strings.Contains(view, "(default)") {
		t.Errorf("the fallback must be marked as the default:\n%s", view)
	}
	// Scoped to the sort line: the other empty list fields on this tab (symlinks,
	// post-create, env) DO open the line editor and correctly say "add".
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Priority sort") && strings.Contains(line, "enter to add") {
			t.Errorf("the sort field opens a picker, not the line editor: %q", line)
		}
	}
}

// A configured chain renders in ORDER, since the order is the meaning.
func TestSettingsFormPrioritySortShowsChainInOrder(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.PrioritySort = []string{"createdAt", "priority"}
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.tab = stProjectDefaults

	view := stripANSI(f.view())
	if !strings.Contains(view, "createdAt → priority") {
		t.Errorf("the chain must render in order:\n%s", view)
	}
	if strings.Contains(view, "(default)") {
		t.Errorf("a configured chain is not the default:\n%s", view)
	}
}

// An empty label list says "pick", not "add": enter opens the picker.
func TestSettingsFormEmptyLabelListSaysPick(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Defaults.MatchLabels = nil
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.tab = stProjectDefaults

	if view := stripANSI(f.view()); !strings.Contains(view, "enter to pick") {
		t.Errorf("a label list opens a picker on enter:\n%s", view)
	}
}
