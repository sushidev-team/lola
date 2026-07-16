package tui

// Tests for the global settings editor (settingsform.go): field pre-fill, bool
// toggles + text/int editing, a persisted save across all five tables, the
// invalid-input guards (non-numeric, global_cap <= 0) that abort WITHOUT
// mutating config, and the [coderabbit] fields round-tripping to config.toml.

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

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

func TestSettingsFormBoolToggleViaKeys(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)

	// Navigate to a known bool field and toggle it with space.
	idx := -1
	for i := range f.fields {
		if f.fields[i].key == "cr_enabled" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("cr_enabled field missing")
	}
	f.cursor = idx
	before := f.fields[idx].b
	f.update(keyMsg(" "))
	if f.fields[idx].b == before {
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

func TestSettingsFormOnlyDigitsInIntField(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	gc := -1
	for i := range f.fields {
		if f.fields[i].key == "global_cap" {
			gc = i
		}
	}
	f.cursor = gc
	f.fields[gc].text = ""
	f.update(keyMsg("5"))
	f.update(keyMsg("x")) // non-digit ignored in an int field
	f.update(keyMsg("2"))
	if f.fields[gc].text != "52" {
		t.Errorf("int field must accept only digits, got %q", f.fields[gc].text)
	}
}

// The two CodeRabbit features live under ONE "CodeRabbit" section as two
// distinct, indented subsections — so they read as one integration but are never
// mistaken for each other.
func TestSettingsFormDistinguishesReviewAndCoderabbit(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	rv := f.field("review_enabled")
	cr := f.field("cr_enabled")

	// A single top-level "CodeRabbit" header opens the section (only on the first
	// field); the second feature carries no new top-level header.
	if rv.section != "CodeRabbit" {
		t.Errorf("review must open the CodeRabbit section, got %q", rv.section)
	}
	if cr.section != "" {
		t.Errorf("coderabbit must NOT open a second top-level section, got %q", cr.section)
	}
	// Both are indented under distinct subsections.
	if rv.subsection == "" || cr.subsection == "" || rv.subsection == cr.subsection {
		t.Errorf("each feature needs a distinct subsection, got %q / %q", rv.subsection, cr.subsection)
	}
	if !rv.indent || !cr.indent {
		t.Error("both CodeRabbit subsections must be indented under the section")
	}

	out := f.view()
	if !strings.Contains(out, "CodeRabbit") || !strings.Contains(out, rv.subsection) || !strings.Contains(out, cr.subsection) {
		t.Errorf("the section header and both subsections must render:\n%s", out)
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
	f.cursor = indexOfField(f, "agent")
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
	f.cursor = indexOfField(f, "agent")
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

// indexOfField returns the position of the field with the given key.
func indexOfField(f *settingsForm, key string) int {
	for i := range f.fields {
		if f.fields[i].key == key {
			return i
		}
	}
	return -1
}

// In a viewport too short for every field, the modal scrolls to keep the focused
// field visible and ALWAYS pins the footer (help + key hint) — so the ctrl-s/esc
// hint is never clipped on a short terminal.
func TestSettingsFormScrollsToCursor(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	const hint = "ctrl-s save"
	const budget = 14 // fewer rows than the ~28-row field region

	// Cursor at the very top: the first field shows, a "↓ more" marker hints at the
	// rest, and the hint is present.
	f.cursor = indexOfField(f, "global_cap")
	top := f.scrolledView(budget)
	if !strings.Contains(top, "Global cap") || !strings.Contains(top, "↓ more") || !strings.Contains(top, hint) {
		t.Errorf("top view must show the first field, a down-marker, and the hint:\n%s", top)
	}

	// Cursor deep down (Author, unique to the coderabbit watch): it must be visible,
	// an "↑ more" marker present, and the hint still pinned.
	f.cursor = indexOfField(f, "cr_author")
	bot := f.scrolledView(budget)
	if !strings.Contains(bot, "Author") || !strings.Contains(bot, "coderabbitai") {
		t.Errorf("scrolled view must reveal the focused field:\n%s", bot)
	}
	if !strings.Contains(bot, "↑ more") {
		t.Errorf("scrolling past the top must show an up-marker:\n%s", bot)
	}
	if !strings.Contains(bot, hint) {
		t.Errorf("the key hint must stay pinned regardless of scroll:\n%s", bot)
	}

	// The rendered body never exceeds the budget (so box never clips it).
	nLines := len(strings.Split(strings.TrimRight(bot, "\n"), "\n"))
	if nLines > budget+2 { // +2 for the liftable title + blank
		t.Errorf("scrolled body = %d lines, must fit budget %d (+2 title)", nLines, budget)
	}
}
