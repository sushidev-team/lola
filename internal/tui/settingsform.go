// Global settings editor (S): edits the config.toml tables that are NOT
// poll-scoped — [defaults], [notify], [brain], [review], and [coderabbit] — from
// the TUI, so the opt-in feature toggles no longer need hand-editing. Reached
// with 'S' from the cockpit; saved back to config.toml (atomic) and the daemon
// reloaded, exactly like the project form (form.go).
//
// The fields are split across a tab strip (tab / shift+tab, or left/right);
// within a tab they are a flat, navigable list grouped by section header. Five
// kinds: bool (space/enter toggles), text (type inline), int (digits, validated
// on save), enum (space/enter cycles a fixed set), and list/env (enter opens a
// one-entry-per-line sub-editor, over the shared fieldedit.go helpers). The
// Slack webhook and Linear key are secrets and are NEVER edited here — [notify]
// exposes only the env-var NAME that holds the webhook, never its value.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

type settingsFormEvent int

const (
	settingsFormNone settingsFormEvent = iota
	settingsFormCancel
	settingsFormSaved
)

type setFieldKind int

const (
	sfBool setFieldKind = iota
	sfText
	sfInt
	sfEnum // fixed set of values, cycled with space/enter (no free typing)
	sfList // one value per line, edited in an open sub-editor
	sfEnv  // one KEY=value per line, same sub-editor
)

// settingsTab groups the fields into the config tables they write. [defaults]
// spans two tabs on purpose: the daemon-wide knobs (caps, interval, agent) and
// the per-project fallbacks are the same TOML table but answer different
// questions, and together they overflow one readable column.
type settingsTab int

const (
	stDefaults settingsTab = iota
	stProjectDefaults
	stNotify
	stBrain
	stCodeRabbit
)

var settingsTabs = []struct {
	tab   settingsTab
	title string
}{
	{stDefaults, "Defaults"},
	{stProjectDefaults, "Project defaults"},
	{stNotify, "Notify"},
	{stBrain, "Brain"},
	{stCodeRabbit, "Review"},
}

type setField struct {
	key         string      // stable identifier used by save()
	tab         settingsTab // which tab this field appears on
	section     string      // non-empty ⇒ a top-level section header is drawn ABOVE this field
	sectionNote string      // faint one-liner beside the section header (what the section is FOR)
	subsection  string      // non-empty ⇒ an indented sub-header is drawn ABOVE this field
	indent      bool        // this field sits under a subsection (rendered indented)
	label       string
	help        string
	kind        setFieldKind
	b           bool     // sfBool
	text        string   // sfText / sfInt (int held as text, parsed on save) / sfEnum (current selection)
	options     []string // sfEnum: the values cycled through, in order
	lines       []string // sfList / sfEnv (one entry per line)
	// wsPick marks a field whose value is a workspace-label UUID, so enter
	// offers the label picker instead of (sfList) the raw line editor. The
	// underlying storage is unchanged — text for single, lines for multi — so
	// raw UUID entry remains the fallback when the labels cannot be fetched.
	wsPick bool
	// sortPick marks the priority_sort field: enter offers an ORDERED picker
	// over config.PrioritySortKeys. Unlike wsPick there is nothing to fetch —
	// the valid keys are lola's own, not Linear's.
	sortPick bool
	// choices, when non-empty, makes an sfList field a FIXED multiselect: enter
	// opens a picker over these options (not the raw line editor / label fetch),
	// writing the chosen ids back to lines in option order. Used by the review
	// provider editor's transports and fallback fields — local constants, so
	// nothing is fetched and it works offline.
	choices []setPickOpt
}

type settingsForm struct {
	cfgPath string
	cfg     *config.Config
	fields  []setField
	tab     settingsTab
	cursor  int  // index into the ACTIVE tab's fields (see visible)
	scroll  int  // first visible field-region line (cursor-following viewport)
	editing bool // a list/env field is OPEN for line editing
	lineCur int  // which line, while editing
	err     string

	// Workspace-label picker state. wsLabels is the organisation-level label
	// set backing the three [defaults] label fields; wsTried records that a
	// load has been ATTEMPTED (so a genuinely empty workspace is not retried
	// forever), and wsErr is the short reason shown when the picker is
	// unavailable and the field falls back to raw UUID entry.
	wsLabels []linear.Label
	wsTried  bool
	// wsLoading guards against dispatching a duplicate fetch; wsPendingKey is
	// the field whose enter triggered the in-flight load, so its picker can be
	// opened when the labels land rather than eating that keystroke.
	wsLoading    bool
	wsPendingKey string

	// wsNames maps a workspace-label UUID to its display name, for RENDERING
	// only. It is warmed from the on-disk cache when the form opens — a local
	// file read, not a Linear call — so a stored label reads as "agent-ready"
	// rather than a bare UUID before any picker has been opened. Kept separate
	// from wsLabels so the picker's lazy-load state is unaffected.
	wsNames map[string]string
	wsErr   string
	picker  *setPicker

	// reviewLegacy is set when the config still carries the legacy
	// [review]/[coderabbit] tables and no [[review.provider]] catalog. In that
	// state the Review tab is READ-ONLY (editing would produce a mixed config,
	// which is a hard validation error) and offers a one-key migration into the
	// editable provider catalog. Cleared once migrated.
	reviewLegacy bool
}

// setPicker is the workspace-label chooser floated over the field list. It is
// deliberately separate from form.go's picker: that one is keyed by fieldID and
// carries team-scoped metadata this form has no notion of. Options are held as
// id+label pairs so the UUID never has to be shown to pick a label.
type setPicker struct {
	title  string
	key    string // setField.key this writes back to
	multi  bool
	opts   []setPickOpt
	cursor int
	sel    map[string]bool

	// ordered makes the SELECTION order meaningful and preserved. Labels are a
	// set, so their picker writes back in option order; priority_sort is a
	// tie-break CHAIN, where "priority then createdAt" and the reverse are
	// different sorts, so it must keep the order the user toggled them in.
	ordered bool
	order   []string
}

type setPickOpt struct{ id, label string }

// matchModeOptions / dedupModeOptions lead with "" — [defaults] may leave the
// key unset, in which case a project that inherits it falls back to the built-in
// default (config.DefaultMatchMode / DefaultDedupMode) rather than to a value
// frozen into the file.
var (
	matchModeOptions = []string{"", "any", "all"}
	dedupModeOptions = []string{"", "label", "seen", "state"}
)

// wsLabelHelp closes the three [defaults] label helps. A [defaults] label is
// inherited by projects on ANY team, so it has to be a workspace (organisation)
// label — one with no team, which therefore exists across every team. A team
// label put here could not match issues in the other teams. The per-project
// pickers offer that project's own team labels; this one offers workspace
// labels. Each help leads with "Workspace label" too, because the modal
// truncates a help line to its width and this sentence can fall off the end.
const wsLabelHelp = " enter opens the workspace-label picker; raw UUID entry stays available if the labels cannot be fetched."

// newSettingsForm builds the editor pre-filled from the live config. The int
// fields render the resolved values (e.g. review.timeout defaults to 300 once
// enabled), so saving without touching them is a faithful round-trip.
func newSettingsForm(cfgPath string, cfg *config.Config) *settingsForm {
	itoa := strconv.Itoa
	d, n, br := cfg.Defaults, cfg.Notify, cfg.Brain
	// Review providers: seed each kind's fields from the EFFECTIVE catalog (the
	// real [[review.provider]] entries, or the ones synthesized from the legacy
	// tables), falling back to a fresh disabled provider for a kind that is not
	// configured. A legacy-only config renders these READ-ONLY until migrated.
	rp := reviewProviderSeed(cfg)
	cli, watch, claude := rp("coderabbit-cli"), rp("coderabbit-watch"), rp("claude-session")
	reviewLegacy := legacyReviewOnly(cfg)
	// The fixed multiselect option sets. github is offered on the pass kinds
	// only; the watch forbids it (validation), so its editor omits it.
	trAll := transportOpts(true)
	trNoGitHub := transportOpts(false)
	f := &settingsForm{
		reviewLegacy: reviewLegacy,
		cfgPath: cfgPath,
		cfg:     cfg,
		fields: []setField{
			// [defaults] — daemon-wide knobs.
			{key: "global_cap", tab: stDefaults, section: "[defaults]", sectionNote: "caps, interval, agent", label: "Global cap", help: "Max concurrent sessions across all polls. Must be > 0.", kind: sfInt, text: itoa(d.GlobalCap)},
			{key: "concurrency_cap", tab: stDefaults, label: "Concurrency cap", help: "Default per-poll cap (a poll's own cap overrides). 0 = no per-poll default.", kind: sfInt, text: itoa(d.ConcurrencyCap)},
			{key: "poll_interval", tab: stDefaults, label: "Poll interval", help: "How often each poll ticks, as a Go duration (e.g. 60s, 2m). Clamped up to 30s.", kind: sfText, text: d.PollInterval.String()},
			{key: "agent", tab: stDefaults, label: "Coding agent", help: "Default coding agent each session spawns (a [[project]] can override). space/enter cycles claude|codex|opencode.", kind: sfEnum, options: agentKindStrings(), text: defaultAgentDisplay(d.Agent)},

			// [defaults] — the per-project fallbacks. Same TOML table, but every
			// key here is the value a [[project]] gets when it omits its own, so
			// shared setup is written once (see config.ProjectInherits).
			{key: "def_branch_prefix", tab: stProjectDefaults, section: "[defaults]", sectionNote: "inherited by every [[project]] that omits the key", label: "Branch prefix", help: "Prepended to a session's branch name (e.g. \"lola/\" → lola/eng-42). Empty resolves to \"lola/\".", kind: sfText, text: d.BranchPrefix},
			{key: "def_symlinks", tab: stProjectDefaults, label: "Symlinks", help: "One relative path per line, linked from the main checkout into each worktree (e.g. .env). enter opens the list. Do NOT symlink vendor/ — it breaks PHP autoload; use post_create.", kind: sfList, lines: append([]string(nil), d.Symlinks...)},
			{key: "def_post_create", tab: stProjectDefaults, label: "Post-create", help: "One command per line, run in a fresh worktree before the agent starts (e.g. composer install). enter opens the list.", kind: sfList, lines: append([]string(nil), d.PostCreate...)},
			{key: "def_env", tab: stProjectDefaults, label: "Env (KEY=value)", help: "One KEY=value per line, exported into every session and its post_create commands. Keys must be shell identifiers. enter opens the list.", kind: sfEnv, lines: envLines(d.Env)},
			{key: "def_match_labels", tab: stProjectDefaults, label: "Match labels", help: "Workspace labels an issue must carry (see match mode) to be picked up." + wsLabelHelp, kind: sfList, wsPick: true, lines: append([]string(nil), d.MatchLabels...)},
			{key: "def_match_mode", tab: stProjectDefaults, label: "Match mode", help: "How match labels combine: any = at least one, all = every one. space/enter cycles; unset falls back to \"any\".", kind: sfEnum, options: matchModeOptions, text: d.MatchMode},
			{key: "def_on_sent_set_label", tab: stProjectDefaults, label: "On-sent set label", help: "Workspace label flipped onto an issue once its session is dispatched (label dedup mode)." + wsLabelHelp, kind: sfText, wsPick: true, text: d.OnSentSetLabel},
			{key: "def_blocked_label_id", tab: stProjectDefaults, label: "Blocked label", help: "Workspace label applied when a session escalates and needs a human." + wsLabelHelp, kind: sfText, wsPick: true, text: d.BlockedLabelID},
			{key: "def_dedup_mode", tab: stProjectDefaults, label: "Dedup mode", help: "How an already-dispatched issue is remembered: label (flip a Linear label), seen (local store), state (workflow state). space/enter cycles; unset falls back to \"seen\".", kind: sfEnum, options: dedupModeOptions, text: d.DedupMode},
			{key: "def_priority_sort", tab: stProjectDefaults, label: "Priority sort", help: "Tie-break chain for ranking the issues a tick matched, applied in order (priority = highest first, createdAt = oldest first). enter picks the keys; ORDER matters. Unset sorts by priority, then createdAt.", kind: sfList, sortPick: true, lines: append([]string(nil), d.PrioritySort...)},

			// [notify]
			{key: "notify_desktop", tab: stNotify, section: "[notify]", sectionNote: "desktop / Slack alerts", label: "Desktop banners", help: "Native desktop notifications (macOS only).", kind: sfBool, b: n.Desktop},
			{key: "slack_webhook_env", tab: stNotify, label: "Slack webhook env", help: "NAME of the env var holding the Slack webhook URL (never the URL itself — that stays a secret). Empty = no Slack.", kind: sfText, text: n.SlackWebhookEnv},

			// [brain]
			{key: "brain_enabled", tab: stBrain, section: "[brain]", sectionNote: "claude notification summaries", label: "Enabled", help: "Opt-in headless-claude summarizer for escalation / approved notifications.", kind: sfBool, b: br.Enabled},
			{key: "brain_model", tab: stBrain, label: "Model", help: "claude --model override; empty = claude's default.", kind: sfText, text: br.Model},
			{key: "brain_timeout", tab: stBrain, label: "Timeout seconds", help: "Hard cap per summary call. Must be >= 0.", kind: sfInt, text: itoa(br.TimeoutSeconds)},
			{key: "brain_esc", tab: stBrain, label: "Summarize escalation", help: "Summarize WHY a session is blocked on escalation.", kind: sfBool, b: br.SummarizeEscalation},
			{key: "brain_appr", tab: stBrain, label: "Summarize approved", help: "Summarize PR risk on approved+green.", kind: sfBool, b: br.SummarizeApproved},

			// Review — the pluggable provider catalog ([[review.provider]]). One
			// indented subsection per KIND; each names its config kind. transports
			// and fallback are fixed multiselects (enter opens a local picker), the
			// notify/send toggles refine the always-on `lola` transport. A
			// legacy-only config shows these READ-ONLY with a migrate action.
			{key: "pv_cli_enabled", tab: stCodeRabbit, subsection: "coderabbit-cli — execs `coderabbit review` locally on PR-open", indent: true, label: "Enabled", help: "Opt-in CodeRabbit CLI QA pass: execs `coderabbit review` against the worktree when a session first opens a PR.", kind: sfBool, b: cli.Enabled},
			{key: "pv_cli_onpropen", tab: stCodeRabbit, indent: true, label: "On PR open", help: "Run the pass automatically when a session first opens a PR.", kind: sfBool, b: cli.OnPROpen},
			{key: "pv_cli_command", tab: stCodeRabbit, indent: true, label: "Command", help: "coderabbit argv override (space-split); empty = built-in default.", kind: sfText, text: cli.Command},
			{key: "pv_cli_timeout", tab: stCodeRabbit, indent: true, label: "Timeout seconds", help: "Hard cap per review pass. Must be >= 0.", kind: sfInt, text: itoa(cli.TimeoutSeconds)},
			{key: "pv_cli_notify", tab: stCodeRabbit, indent: true, label: "Notify", help: "lola transport: surface findings to a human (desktop/Slack).", kind: sfBool, b: cli.Notify},
			{key: "pv_cli_send", tab: stCodeRabbit, indent: true, label: "Send to agent", help: "lola transport: feed findings back to the worker via the send-keys gate.", kind: sfBool, b: cli.SendToAgent},
			{key: "pv_cli_transports", tab: stCodeRabbit, indent: true, label: "Transports", help: "Sinks findings route to: lola (always on: notify + agent), github (PR comment), linear (issue comment). enter picks.", kind: sfList, choices: trAll, lines: cli.Transports.Strings()},
			{key: "pv_cli_fallback", tab: stCodeRabbit, indent: true, label: "Fallback", help: "Ordered pass kinds tried when this provider can't answer (unavailable / over-quota). enter picks.", kind: sfList, choices: fallbackOpts("coderabbit-cli"), lines: cli.FallbackStrings()},

			{key: "pv_watch_enabled", tab: stCodeRabbit, subsection: "coderabbit-watch — polls the PR for the app's comments", indent: true, label: "Enabled", help: "Opt-in PR-comment watch: polls the GitHub PR for comments the CodeRabbit app (or another bot) leaves, and routes them. Needs no local coderabbit binary. No github transport / fallback (its feedback is already on the PR).", kind: sfBool, b: watch.Enabled},
			{key: "pv_watch_author", tab: stCodeRabbit, indent: true, label: "Author", help: "Login substring matched against comment authors. Default coderabbitai.", kind: sfText, text: watchAuthor(watch)},
			{key: "pv_watch_notify", tab: stCodeRabbit, indent: true, label: "Notify", help: "lola transport: surface each new comment to a human.", kind: sfBool, b: watch.Notify},
			{key: "pv_watch_send", tab: stCodeRabbit, indent: true, label: "Send to agent", help: "lola transport: relay each new comment to the worker via the send-keys gate.", kind: sfBool, b: watch.SendToAgent},
			{key: "pv_watch_transports", tab: stCodeRabbit, indent: true, label: "Transports", help: "Sinks: lola (always on), linear (issue comment). github is not offered — the feedback is already on the PR. enter picks.", kind: sfList, choices: trNoGitHub, lines: watch.Transports.Strings()},

			{key: "pv_claude_enabled", tab: stCodeRabbit, subsection: "claude-session — headless `claude -p` review on PR-open", indent: true, label: "Enabled", help: "Opt-in headless Claude review pass: runs `claude -p` over the PR diff when a session first opens a PR.", kind: sfBool, b: claude.Enabled},
			{key: "pv_claude_onpropen", tab: stCodeRabbit, indent: true, label: "On PR open", help: "Run the pass automatically when a session first opens a PR.", kind: sfBool, b: claude.OnPROpen},
			{key: "pv_claude_model", tab: stCodeRabbit, indent: true, label: "Model", help: "claude --model override; empty = claude's default.", kind: sfText, text: claude.Model},
			{key: "pv_claude_timeout", tab: stCodeRabbit, indent: true, label: "Timeout seconds", help: "Hard cap per review pass. Must be >= 0.", kind: sfInt, text: itoa(claude.TimeoutSeconds)},
			{key: "pv_claude_notify", tab: stCodeRabbit, indent: true, label: "Notify", help: "lola transport: surface findings to a human.", kind: sfBool, b: claude.Notify},
			{key: "pv_claude_send", tab: stCodeRabbit, indent: true, label: "Send to agent", help: "lola transport: feed findings back to the worker via the send-keys gate.", kind: sfBool, b: claude.SendToAgent},
			{key: "pv_claude_transports", tab: stCodeRabbit, indent: true, label: "Transports", help: "Sinks: lola (always on), github (PR comment), linear (issue comment). enter picks.", kind: sfList, choices: trAll, lines: claude.Transports.Strings()},
			{key: "pv_claude_fallback", tab: stCodeRabbit, indent: true, label: "Fallback", help: "Ordered pass kinds tried when this provider can't answer. enter picks.", kind: sfList, choices: fallbackOpts("claude-session"), lines: claude.FallbackStrings()},
		},
	}
	// Warm the label NAMES from the on-disk cache for rendering. This is a local
	// file read, never a Linear call, and deliberately does not touch wsLabels:
	// the picker stays lazy (see openLabelPicker).
	if ls, err := loadWorkspaceLabelCache(); err == nil {
		f.rememberLabelNames(ls)
	}
	return f
}

// watchAuthor pre-fills the watch's author field with the effective default
// when unset, so the editor shows what the watch will actually match.
func watchAuthor(p config.ReviewProvider) string {
	if p.Author == "" {
		return config.DefaultCodeRabbitAuthor
	}
	return p.Author
}

// reviewProviderSeed returns a lookup that yields the effective provider for a
// kind (from the real catalog or the legacy synthesis), or a fresh DISABLED
// provider when the kind is not configured — so every kind's fields have a
// sensible pre-fill even before it is enabled.
func reviewProviderSeed(cfg *config.Config) func(kind string) config.ReviewProvider {
	byKind := map[string]config.ReviewProvider{}
	for _, p := range cfg.EffectiveReviewProviders() {
		byKind[p.KindString()] = p
	}
	return func(kind string) config.ReviewProvider {
		if p, ok := byKind[kind]; ok {
			return p
		}
		// A kind that is not in the catalog seeds DISABLED with its sinks OFF, so
		// enabling it in the editor flips the dependent toggles on (enableDefaults)
		// exactly as saving `enabled = true` alone would resolve — rather than
		// showing them pre-checked while the provider itself is off.
		p, _ := config.NewReviewProvider(kind)
		p.Enabled, p.OnPROpen, p.Notify, p.SendToAgent = false, false, false, false
		return p
	}
}

// legacyReviewOnly reports whether the config carries the legacy
// [review]/[coderabbit] tables but no [[review.provider]] catalog — the state
// in which the Review tab is read-only pending migration.
func legacyReviewOnly(cfg *config.Config) bool {
	hasLegacy := cfg.Review != (config.ReviewConfig{}) || cfg.CodeRabbit != (config.CodeRabbitConfig{})
	return hasLegacy && len(cfg.ReviewProviders) == 0
}

// transportOpts builds the transport multiselect options; github is included
// only for the pass kinds (the watch forbids it).
func transportOpts(withGitHub bool) []setPickOpt {
	var out []setPickOpt
	for _, t := range config.TransportTokens() {
		if t == string(config.TransportGitHub) && !withGitHub {
			continue
		}
		out = append(out, setPickOpt{t, t})
	}
	return out
}

// fallbackOpts lists the pass kinds a provider may fall through to: every pass
// kind except itself (the watch is not a valid fallback — it cannot classify
// quota).
func fallbackOpts(self string) []setPickOpt {
	var out []setPickOpt
	for _, k := range config.ReviewProviderKinds() {
		if k == self || config.IsWatchKind(k) {
			continue
		}
		out = append(out, setPickOpt{k, k})
	}
	return out
}

// agentKindStrings is the [defaults].agent picker's cycle order: the concrete
// kinds (claude|codex|opencode) from agent.Kinds. The global default is the top
// of the resolution chain, so it has no "inherit" option — a project-level
// override carries that (see projAgentOptions in fieldedit.go).
func agentKindStrings() []string {
	out := make([]string, len(agent.Kinds))
	for i, k := range agent.Kinds {
		out[i] = k.String()
	}
	return out
}

// defaultAgentDisplay is the picker's pre-fill for [defaults].agent: an unset
// value shows "claude", the effective default the chain resolves to.
func defaultAgentDisplay(v string) string {
	if v == "" {
		return agent.Claude.String()
	}
	return v
}

// settingsAgentValue maps the picker selection to the value stored on disk: the
// effective default "claude" persists as "" so an unpinned config stays clean
// (empty resolves to claude at read time, AgentForProject); codex/opencode
// persist verbatim. Any other value passes through so c.Validate() rejects it.
func settingsAgentValue(sel string) string {
	if sel == agent.Claude.String() {
		return ""
	}
	return sel
}

// cycleEnum advances an sfEnum field to the next option (wrapping). A value not
// in the option set resets to the first — unreachable in practice, since the
// field is seeded from options and only ever mutated through here.
func cycleEnum(fld *setField) {
	if len(fld.options) == 0 {
		return
	}
	for i, o := range fld.options {
		if o == fld.text {
			fld.text = fld.options[(i+1)%len(fld.options)]
			return
		}
	}
	fld.text = fld.options[0]
}

func (f *settingsForm) field(key string) *setField {
	for i := range f.fields {
		if f.fields[i].key == key {
			return &f.fields[i]
		}
	}
	return nil // unreachable: keys are compile-time constants matched in save()
}

// visible returns the indices (into f.fields) of the ACTIVE tab's fields, in
// order. f.cursor indexes THIS slice, not f.fields — a tab-relative cursor is
// what lets switchTab reset to the top without pointing past a shorter list.
func (f *settingsForm) visible() []int {
	out := make([]int, 0, len(f.fields))
	for i := range f.fields {
		if f.fields[i].tab == f.tab {
			out = append(out, i)
		}
	}
	return out
}

// paste inserts clipboard text into the focused field. An open list/env
// sub-editor takes a MULTI-line paste as multiple entries; a single-line field
// takes the first non-blank line. Bool and enum fields ignore paste — they are
// cycled, not typed. See the paste helpers in fieldedit.go.
func (f *settingsForm) paste(s string) {
	if s == "" {
		return
	}
	fld := f.cur()
	if fld == nil {
		return
	}
	if f.editing {
		lines := pasteLines(s)
		if len(lines) == 0 {
			return
		}
		fld.lines[f.lineCur] += lines[0]
		if rest := lines[1:]; len(rest) > 0 {
			tail := append(rest, fld.lines[f.lineCur+1:]...)
			fld.lines = append(fld.lines[:f.lineCur+1], tail...)
			f.lineCur += len(rest)
		}
		return
	}
	switch fld.kind {
	case sfText:
		fld.text += pasteInline(s)
	case sfInt:
		fld.text += pasteDigits(s)
	}
}

// cur returns the focused field, clamping a cursor left past the end of a
// shorter tab. Every tab has at least one field by construction.
func (f *settingsForm) cur() *setField {
	vis := f.visible()
	if f.cursor >= len(vis) {
		f.cursor = len(vis) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
	return &f.fields[vis[f.cursor]]
}

// switchTab moves to the next/previous tab (wrapping), resetting the cursor,
// scroll and any open list editor so nothing carries over from a tab whose
// field list has a different length.
// switchTab moves to the next/previous tab. Landing on the project-defaults tab
// starts the workspace-label load if it has not run, so the stored label IDs
// render as NAMES rather than bare UUIDs without the user having to open a
// picker first. It stays lazy with respect to opening the form itself, and a
// failure is silent here — the fields degrade to UUID text as they already do.
func (f *settingsForm) switchTab(delta int) tea.Cmd {
	n := len(settingsTabs)
	f.tab = settingsTab((int(f.tab) + delta%n + n) % n)
	f.cursor, f.scroll, f.editing = 0, 0, false
	return f.maybeLoadLabelNames()
}

// maybeLoadLabelNames kicks off a label fetch purely so names can be rendered.
// Unlike openLabelPicker it sets no pending key, so nothing pops open when the
// result lands. Returns nil when the labels are already known, a fetch is in
// flight, or one has already been tried and failed.
func (f *settingsForm) maybeLoadLabelNames() tea.Cmd {
	if f.tab != stProjectDefaults || f.wsTried || f.wsLoading || len(f.wsLabels) > 0 {
		return nil
	}
	f.wsLoading = true
	return loadWorkspaceLabelsCmd(f.cfg)
}

// enableDefaults maps a feature's master "enabled" toggle to the dependent sink
// fields its config resolution turns ON when the feature is enabled (see
// resolveCodeRabbit / resolveReview / resolveBrain: `enabled = true` alone
// defaults these on). The editor mirrors that so enabling a feature in the TUI is
// not silently inert with every sink left off.
var enableDefaults = map[string][]string{
	"pv_cli_enabled":    {"pv_cli_onpropen", "pv_cli_notify", "pv_cli_send"},
	"pv_watch_enabled":  {"pv_watch_notify", "pv_watch_send"},
	"pv_claude_enabled": {"pv_claude_onpropen", "pv_claude_notify", "pv_claude_send"},
	"brain_enabled":     {"brain_esc", "brain_appr"},
}

// toggleBool flips a bool field. When it flips a master "enabled" switch OFF→ON,
// it also switches that feature's dependent sinks ON, matching what saving
// `enabled = true` alone would resolve to — so enabling in the editor actually
// does something. Turning a master OFF leaves the sinks as-is (they are ignored
// while disabled and preserved if re-enabled by hand).
func (f *settingsForm) toggleBool(fld *setField) {
	was := fld.b
	fld.b = !fld.b
	if !was && fld.b {
		for _, dep := range enableDefaults[fld.key] {
			if d := f.field(dep); d != nil {
				d.b = true
			}
		}
	}
}

// update takes the whole tea.Msg (not just a key) so the form can dispatch and
// receive async work — see the settings dispatch in app.go. Non-key messages
// other than the ones handled here are ignored.
func (f *settingsForm) update(msg tea.Msg) (tea.Cmd, settingsFormEvent) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return f.updateNonKey(msg)
	}
	return f.key(k)
}

// updateNonKey handles the async results the form asks for. Left as its own
// method so the key path stays readable; extend it alongside any new tea.Cmd.
func (f *settingsForm) updateNonKey(msg tea.Msg) (tea.Cmd, settingsFormEvent) {
	if v, ok := msg.(workspaceLabelsMsg); ok {
		f.applyWorkspaceLabels(v)
	}
	return nil, settingsFormNone
}

func (f *settingsForm) key(k tea.KeyPressMsg) (tea.Cmd, settingsFormEvent) {
	f.err = ""
	if f.picker != nil {
		return f.pickerKey(k)
	}
	if f.editing {
		return nil, f.editList(k)
	}
	fld := f.cur()
	switch k.String() {
	case "esc":
		return nil, settingsFormCancel
	case "ctrl+s":
		return nil, f.save()
	case "tab", "right":
		return f.switchTab(1), settingsFormNone
	case "shift+tab", "left":
		return f.switchTab(-1), settingsFormNone
	case "up":
		if f.cursor > 0 {
			f.cursor--
		}
	case "down":
		if f.cursor < len(f.visible())-1 {
			f.cursor++
		}
	case "ctrl+r":
		// Force a live refetch of the workspace labels. Only meaningful on a
		// label field; ctrl+r is free here because the settings modal owns all
		// input while open (it never reaches the cockpit's daemon restart).
		if fld.wsPick {
			return f.refreshWorkspaceLabels(fld), settingsFormNone
		}
	case "m", "M":
		// Migrate the legacy [review]/[coderabbit] tables into the editable
		// provider catalog — the only action offered while the Review tab is
		// read-only. A no-op elsewhere (an "m" is a literal in a text field,
		// handled by the default case below).
		if f.reviewReadOnly() {
			f.migrateReview()
			return nil, settingsFormNone
		}
		f.typeInto(fld, k)
	case "enter":
		if f.reviewReadOnly() {
			return nil, settingsFormNone // read-only pending migration
		}
		if fld.sortPick {
			f.openSortPicker(fld)
			return nil, settingsFormNone
		}
		if fld.wsPick {
			return f.openLabelPicker(fld), settingsFormNone
		}
		if len(fld.choices) > 0 {
			f.openChoicePicker(fld)
			return nil, settingsFormNone
		}
		switch fld.kind {
		case sfBool:
			f.toggleBool(fld)
		case sfEnum:
			cycleEnum(fld)
		case sfList, sfEnv:
			f.openList(fld)
		}
	case "space":
		if f.reviewReadOnly() {
			return nil, settingsFormNone
		}
		// Space toggles a bool and cycles an enum, but is a literal character in a
		// text field (e.g. the review command argv); int and list fields ignore it.
		switch fld.kind {
		case sfBool:
			f.toggleBool(fld)
		case sfEnum:
			cycleEnum(fld)
		case sfText:
			fld.text += " "
		}
	case "backspace":
		if f.reviewReadOnly() {
			return nil, settingsFormNone
		}
		if fld.kind == sfText || fld.kind == sfInt {
			fld.text = dropLastRune(fld.text)
		}
	default:
		if f.reviewReadOnly() {
			return nil, settingsFormNone
		}
		f.typeInto(fld, k)
	}
	return nil, settingsFormNone
}

// typeInto appends a typed character to a text/int field (digits only for int).
func (f *settingsForm) typeInto(fld *setField, k tea.KeyPressMsg) {
	switch {
	case fld.kind == sfInt && len(k.Text) == 1 && k.Text >= "0" && k.Text <= "9":
		fld.text += k.Text
	case fld.kind == sfText && k.Text != "":
		fld.text += k.Text
	}
}

// reviewReadOnly reports whether the focused tab is the (legacy, unmigrated)
// Review tab, in which every edit is suppressed and only the migrate action runs.
func (f *settingsForm) reviewReadOnly() bool {
	return f.reviewLegacy && f.tab == stCodeRabbit
}

// openChoicePicker opens a fixed multiselect over fld.choices, seeded from the
// field's current lines. applyPick writes the selection back to lines in option
// order, so save() reads it uniformly with the other list fields.
func (f *settingsForm) openChoicePicker(fld *setField) {
	p := &setPicker{title: fld.label, key: fld.key, multi: true, sel: map[string]bool{}}
	for _, id := range trimDropEmpty(fld.lines) {
		p.sel[id] = true
	}
	p.opts = append(p.opts, fld.choices...)
	for i, o := range p.opts {
		if p.sel[o.id] {
			p.cursor = i
			break
		}
	}
	f.picker = p
}

// migrateReview folds the legacy [review]/[coderabbit] tables into the catalog,
// persists, and rebuilds the Review tab as an editable provider editor. One-way
// (mirrors `lola config migrate-review`); a Validate/Save failure leaves the
// legacy config in place and surfaces the reason.
func (f *settingsForm) migrateReview() {
	config.MigrateLegacyReview(f.cfg)
	if err := f.cfg.Validate(); err != nil {
		f.err = "migrate failed: " + err.Error()
		return
	}
	if err := f.cfg.Save(f.cfgPath); err != nil {
		f.err = "migrate save failed: " + err.Error()
		return
	}
	// Reseed the provider fields from the freshly-written catalog and drop
	// read-only mode so the editor is now live.
	fresh := newSettingsForm(f.cfgPath, f.cfg)
	for i := range f.fields {
		if f.fields[i].tab != stCodeRabbit {
			continue
		}
		if nf := fresh.field(f.fields[i].key); nf != nil {
			f.fields[i] = *nf
		}
	}
	f.reviewLegacy = false
	f.cursor = 0
}

// openList opens a list/env field for line editing, seeding an empty field with
// one blank line so there is somewhere to type.
func (f *settingsForm) openList(fld *setField) {
	if len(fld.lines) == 0 {
		fld.lines = []string{""}
	}
	f.editing, f.lineCur = true, 0
}

// editList drives the OPEN list/env field: arrows move between lines, enter adds
// a line, backspace edits (or removes an empty line), esc closes back to field
// navigation. Deliberately the same shape as (*formModel).editList in form.go —
// the two forms are separate types with their own line buffers, but a list field
// must feel identical in both.
func (f *settingsForm) editList(k tea.KeyPressMsg) settingsFormEvent {
	fld := f.cur()
	switch k.String() {
	case "esc", "ctrl+s":
		f.editing = false
		if k.String() == "ctrl+s" {
			return f.save()
		}
	case "up":
		if f.lineCur > 0 {
			f.lineCur--
		}
	case "down":
		if f.lineCur < len(fld.lines)-1 {
			f.lineCur++
		}
	case "enter":
		f.lineCur++
		fld.lines = append(fld.lines[:f.lineCur], append([]string{""}, fld.lines[f.lineCur:]...)...)
	case "backspace":
		if fld.lines[f.lineCur] == "" && len(fld.lines) > 1 {
			fld.lines = append(fld.lines[:f.lineCur], fld.lines[f.lineCur+1:]...)
			if f.lineCur > 0 {
				f.lineCur--
			}
		} else {
			fld.lines[f.lineCur] = dropLastRune(fld.lines[f.lineCur])
		}
	default:
		if k.Text != "" {
			fld.lines[f.lineCur] += k.Text
		}
	}
	return settingsFormNone
}

// ---- workspace labels -----------------------------------------------------
//
// The three [defaults] label keys are picked from the WORKSPACE (organisation)
// labels — those with no team, which exist across every team. A [defaults]
// value is inherited by projects on any team, so a team-scoped label here could
// never match issues in the other teams. Loading is lazy (nothing happens until
// a picker is opened) and asynchronous, and every failure path degrades to raw
// UUID entry: the form must stay usable with no API key and offline.

type workspaceLabelsMsg struct {
	labels []linear.Label
	err    error
}

// wsLabelCache is the on-disk blob, mirroring meta.go's per-team cache. Reading
// it is a local file read, so the picker can consult it synchronously without
// ever waiting on Linear.
type wsLabelCache struct {
	FetchedAt time.Time      `json:"fetchedAt"`
	Labels    []linear.Label `json:"labels"`
}

func workspaceLabelCachePath() (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cache", "linear-workspace-labels.json"), nil
}

// wsLabelCacheMaxAge bounds how long the picker will trust the cache. Labels
// are edited in Linear, not here, so a cache with no expiry would mask a
// renamed or newly added label indefinitely; past this it refetches on its own.
// ctrl+r forces a live fetch when that wait is too long.
const wsLabelCacheMaxAge = 12 * time.Hour

// loadWorkspaceLabelCache returns the cached labels, or an error if the cache
// is missing, unreadable, or older than wsLabelCacheMaxAge.
func loadWorkspaceLabelCache() ([]linear.Label, error) {
	path, err := workspaceLabelCachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c wsLabelCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if age := time.Since(c.FetchedAt); age > wsLabelCacheMaxAge {
		return nil, fmt.Errorf("workspace label cache is %s old", age.Truncate(time.Hour))
	}
	return c.Labels, nil
}

func saveWorkspaceLabelCache(ls []linear.Label) error {
	path, err := workspaceLabelCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(wsLabelCache{FetchedAt: time.Now(), Labels: ls}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// loadWorkspaceLabelsCmd fetches the organisation-level labels off the UI
// goroutine, on a bounded context, and refreshes the cache. Mirrors
// loadMetaCmd; the cache read is NOT done here because openLabelPicker already
// consulted it synchronously before deciding to dispatch this.
func loadWorkspaceLabelsCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		api, err := newLinearAPI(cfg)
		if err != nil {
			return workspaceLabelsMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ls, err := api.WorkspaceLabels(ctx)
		if err != nil {
			return workspaceLabelsMsg{err: err}
		}
		_ = saveWorkspaceLabelCache(ls) // best-effort; a stale cache beats none
		return workspaceLabelsMsg{labels: ls}
	}
}

// sortChainText renders a priority_sort field as the ordering it actually
// produces. An EMPTY chain is not "nothing" — daemon.SortIssues falls back to
// config.DefaultPrioritySort — so it shows that fallback, marked as the default,
// rather than a bare "(none)" that reads as "no sorting".
func (f *settingsForm) sortChainText(fld *setField) string {
	keys := trimDropEmpty(fld.lines)
	if len(keys) == 0 {
		return faintText.Render(strings.Join(config.DefaultPrioritySort, " → ") + "  (default)")
	}
	return strings.Join(keys, " → ")
}

// rememberLabelNames records id -> display name for rendering. Additive: a
// later, larger fetch never drops names an earlier one supplied.
func (f *settingsForm) rememberLabelNames(ls []linear.Label) {
	if len(ls) == 0 {
		return
	}
	if f.wsNames == nil {
		f.wsNames = make(map[string]string, len(ls))
	}
	for _, l := range ls {
		f.wsNames[l.ID] = labelDisplay(l)
	}
}

// labelText renders a stored workspace-label UUID as its name when known,
// falling back to the raw value. The fallback is what makes this safe on a
// PARTIALLY TYPED id: as soon as the text stops matching a known label it shows
// verbatim again, so manual entry still reads correctly as it is typed.
func (f *settingsForm) labelText(v string) string {
	if name, ok := f.wsNames[strings.TrimSpace(v)]; ok {
		return name
	}
	return v
}

// applyWorkspaceLabels folds a completed load into the form. Both failure modes
// (error, empty workspace) leave wsLabels empty and set a short reason, which
// the footer shows and which sends the next enter to raw UUID entry.
func (f *settingsForm) applyWorkspaceLabels(msg workspaceLabelsMsg) {
	f.wsTried, f.wsLoading = true, false
	f.rememberLabelNames(msg.labels) // so stored IDs render as names
	pending := f.wsPendingKey
	f.wsPendingKey = ""
	// The instruction leads and the reason trails: the modal truncates this to
	// its width, and losing the cause is survivable where losing "what do I do
	// now" is not.
	switch {
	case msg.err != nil:
		f.wsErr = "type UUIDs manually — workspace labels unavailable: " + shortErr(msg.err)
	case len(msg.labels) == 0:
		f.wsErr = "type UUIDs manually — no workspace labels in this Linear organisation"
	default:
		f.wsLabels, f.wsErr = msg.labels, ""
	}
	if pending == "" {
		return
	}
	// The enter that triggered the load opens the picker when the labels land,
	// so that keystroke is not silently swallowed. Only if the asking field is
	// STILL focused, though — the load is async and the user may have moved on
	// or switched tabs, and stealing focus back would be worse than doing
	// nothing. On failure the field keeps its raw-entry fallback (below).
	fld := f.cur()
	if fld == nil || fld.key != pending || f.picker != nil || f.editing {
		return
	}
	if len(f.wsLabels) > 0 {
		f.openPickerFor(fld)
		return
	}
	f.fallbackToRawEntry(fld)
}

// fallbackToRawEntry makes a label field editable without a picker: a
// multi-value field opens its one-UUID-per-line editor, a single-value field
// already edits inline so there is nothing to open.
func (f *settingsForm) fallbackToRawEntry(fld *setField) {
	if fld.kind == sfList || fld.kind == sfEnv {
		f.openList(fld)
	}
}

// shortErr keeps a failure readable on the single footer line the modal allows.
func shortErr(err error) string {
	r := []rune(err.Error())
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return string(r)
}

// openLabelPicker opens the workspace-label chooser for a wsPick field. It never
// blocks: an in-memory set is used directly, a cold one is filled from the disk
// cache (a local read), and only a genuine miss dispatches the async fetch. Once
// a load has been tried and yielded nothing, enter falls back to raw UUID entry
// so the field is never uneditable.
func (f *settingsForm) openLabelPicker(fld *setField) tea.Cmd {
	if len(f.wsLabels) == 0 && !f.wsTried {
		if ls, err := loadWorkspaceLabelCache(); err == nil && len(ls) > 0 {
			f.wsLabels, f.wsTried = ls, true
		}
	}
	if len(f.wsLabels) > 0 {
		f.openPickerFor(fld)
		return nil
	}
	if !f.wsTried {
		if f.wsLoading {
			return nil // a fetch is already in flight
		}
		// Remember who asked: applyWorkspaceLabels opens this field's picker
		// when the labels land, so the enter that started the load is not lost.
		f.wsLoading, f.wsPendingKey = true, fld.key
		f.wsErr = "loading workspace labels…"
		return loadWorkspaceLabelsCmd(f.cfg)
	}
	f.fallbackToRawEntry(fld)
	return nil
}

// refreshWorkspaceLabels (ctrl+r on a label field or in the picker) forces a
// live fetch past both the in-memory set and the disk cache. The cache has a
// max age, but a label added in Linear seconds ago still needs a way to appear
// without waiting it out.
func (f *settingsForm) refreshWorkspaceLabels(fld *setField) tea.Cmd {
	if f.wsLoading {
		return nil
	}
	f.wsLabels, f.wsTried = nil, false
	f.wsLoading, f.wsPendingKey = true, fld.key
	f.wsErr = "refreshing workspace labels…"
	f.picker = nil // reopened by applyWorkspaceLabels once the fresh set lands
	return loadWorkspaceLabelsCmd(f.cfg)
}

// openSortPicker offers the sort keys daemon.SortIssues understands, seeded
// with the field's current chain so confirming without touching anything is a
// no-op. Nothing is fetched: the keys are lola's own, not a Linear concept —
// there is no "list of priorities" to read from the API.
func (f *settingsForm) openSortPicker(fld *setField) {
	cur := trimDropEmpty(fld.lines)
	p := &setPicker{
		title:   fld.label,
		key:     fld.key,
		multi:   true,
		ordered: true,
		sel:     map[string]bool{},
		order:   slices.Clone(cur),
	}
	for _, k := range config.PrioritySortKeys {
		p.opts = append(p.opts, setPickOpt{k, sortKeyLabel(k)})
	}
	for _, k := range cur {
		p.sel[k] = true
	}
	f.picker = p
}

// sortKeyLabel spells out what a sort key actually does — "priority" alone does
// not say which end sorts first.
func sortKeyLabel(k string) string {
	switch k {
	case "priority":
		return "priority — highest first (no priority last)"
	case "createdAt":
		return "createdAt — oldest first"
	}
	return k
}

// openPickerFor builds the chooser for a field, pre-selecting its current
// value(s) and starting the cursor there, so confirming without moving is a
// no-op. A single-value field leads with "(none)" to clear it.
func (f *settingsForm) openPickerFor(fld *setField) {
	p := &setPicker{title: fld.label, key: fld.key, sel: map[string]bool{}}
	if fld.kind == sfList || fld.kind == sfEnv {
		p.multi = true
		for _, id := range trimDropEmpty(fld.lines) {
			p.sel[id] = true
		}
	} else {
		p.opts = append(p.opts, setPickOpt{"", "(none)"})
		p.sel[strings.TrimSpace(fld.text)] = true // "" marks (none)
	}
	for _, l := range f.wsLabels {
		p.opts = append(p.opts, setPickOpt{l.ID, labelDisplay(l)})
	}
	for i, o := range p.opts {
		if p.sel[o.id] {
			p.cursor = i
			break
		}
	}
	f.picker = p
}

// pickerKey drives the OPEN chooser: arrows move, space toggles a multi-select,
// enter commits, esc abandons. Mirrors (*formModel).pickerKey so both pickers
// feel the same.
func (f *settingsForm) pickerKey(k tea.KeyPressMsg) (tea.Cmd, settingsFormEvent) {
	p := f.picker
	switch k.String() {
	case "esc":
		f.picker = nil
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.opts)-1 {
			p.cursor++
		}
	case "space":
		// bubbletea v2 reports the space bar as "space"; a test cannot build a
		// message that says " ", so matching that too would be untestable and
		// unreachable.
		if p.multi && len(p.opts) > 0 {
			id := p.opts[p.cursor].id
			if p.sel[id] {
				delete(p.sel, id)
				p.order = slices.DeleteFunc(p.order, func(s string) bool { return s == id })
			} else {
				p.sel[id] = true
				p.order = append(p.order, id)
			}
		}
	case "ctrl+r":
		// Refetch past the cache, keeping the pending selection's field so the
		// picker reopens on the fresh set. Only the workspace-label pickers fetch;
		// a fixed choice picker (transports/fallback) has nothing to refresh.
		if fld := f.field(p.key); fld != nil && fld.wsPick {
			return f.refreshWorkspaceLabels(fld), settingsFormNone
		}
	case "enter":
		f.applyPick(p)
		f.picker = nil
	}
	return nil, settingsFormNone
}

// applyPick writes the chosen label ID(s) back into the field's own storage —
// lines for a multi-select, text for a single — so save() needs no special case.
func (f *settingsForm) applyPick(p *setPicker) {
	fld := f.field(p.key)
	if fld == nil || len(p.opts) == 0 {
		return
	}
	if p.multi {
		if p.ordered {
			fld.lines = slices.Clone(p.order) // toggle order IS the value
			return
		}
		var ids []string
		for _, o := range p.opts { // option order, not toggle order
			if o.id != "" && p.sel[o.id] {
				ids = append(ids, o.id)
			}
		}
		fld.lines = ids
		return
	}
	fld.text = p.opts[p.cursor].id
}

// pickerView renders the chooser in place of the field list, in the same shape
// as the form body: a liftable title, a blank, the windowed options, and a
// pinned hint.
func (f *settingsForm) pickerView(bodyBudget int) string {
	p := f.picker
	hint := "↑/↓ move · enter select · ctrl-r refresh · esc cancel"
	switch {
	case p.ordered:
		// No refresh: the options are lola's own constants, not fetched.
		hint = "↑/↓ move · space add/remove · enter confirm · esc cancel — the NUMBER is the tie-break order"
	case p.multi:
		hint = "↑/↓ move · space toggle · enter confirm · ctrl-r refresh · esc cancel"
	}
	footer := []string{"", faintText.Render(hint)}
	avail := bodyBudget - len(footer)
	if avail < 1 {
		avail = 1
	}

	rows := make([]string, 0, len(p.opts))
	for i, o := range p.opts {
		marker := "  "
		if i == p.cursor {
			marker = "› "
		}
		check := ""
		switch {
		case p.ordered:
			// Show the RANK, not a tick: which key wins is the whole point.
			if rank := slices.Index(p.order, o.id); rank >= 0 {
				check = fmt.Sprintf("%d. ", rank+1)
			} else {
				check = "   "
			}
		case p.multi:
			check = "[ ] "
			if p.sel[o.id] {
				check = "[x] "
			}
		case p.sel[o.id]:
			check = "• "
		}
		line := marker + check + o.label
		if i == p.cursor {
			line = selStyle.Render(line)
		}
		rows = append(rows, line)
	}
	win := pickerWindow(rows, p.cursor, avail)

	out := make([]string, 0, bodyBudget+2)
	out = append(out, titleStyle.Render(p.title), "")
	out = append(out, win...)
	for i := len(win); i < avail; i++ { // pad so the hint sits at the bottom
		out = append(out, "")
	}
	out = append(out, footer...)
	return strings.Join(out, "\n") + "\n"
}

// pickerWindow keeps the cursor row visible in at most avail rows, replacing the
// clipped edge rows with faint markers (same affordance as the field scroller).
func pickerWindow(rows []string, cursor, avail int) []string {
	if avail < 1 {
		avail = 1
	}
	n := len(rows)
	if n <= avail {
		return rows
	}
	top := cursor - avail/2
	if top > n-avail {
		top = n - avail
	}
	if top < 0 {
		top = 0
	}
	win := append([]string(nil), rows[top:top+avail]...)
	if top > 0 {
		win[0] = faintText.Render("  ↑ more")
	}
	if top+avail < n {
		win[avail-1] = faintText.Render("  ↓ more")
	}
	return win
}

// save parses+validates every field, applies them to the config tables, runs the
// full static validation (restoring the prior tables on failure so a rejected
// edit never leaves the in-memory config dirty), then persists atomically.
func (f *settingsForm) save() settingsFormEvent {
	// Parse the numeric / duration fields FIRST, before mutating anything, so a
	// malformed entry aborts with the config untouched.
	gc, err := f.parseInt("global_cap")
	if err != nil {
		return settingsFormNone
	}
	if gc <= 0 {
		f.err = "global cap must be > 0"
		return settingsFormNone
	}
	cc, err := f.parseInt("concurrency_cap")
	if err != nil {
		return settingsFormNone
	}
	bt, err := f.parseInt("brain_timeout")
	if err != nil {
		return settingsFormNone
	}
	interval, perr := time.ParseDuration(strings.TrimSpace(f.field("poll_interval").text))
	if perr != nil {
		f.err = "poll interval: " + perr.Error()
		return settingsFormNone
	}
	// The provider catalog (skipped entirely while the Review tab is read-only —
	// the legacy tables stay as they are pending an explicit migrate).
	var provs []config.ReviewProvider
	if !f.reviewLegacy {
		var perr error
		if provs, perr = f.buildReviewProviders(); perr != nil {
			return settingsFormNone // f.err already set
		}
	}

	c := f.cfg
	// Snapshot the tables we touch so a failed Validate can be rolled back cleanly.
	// The slice/map members of Defaults (post_create, symlinks, env, …) are always
	// REPLACED below, never mutated in place, so the value copy is a complete
	// rollback (same reason NotifyConfig.Routing survives untouched).
	oldD, oldN, oldB, oldR, oldC := c.Defaults, c.Notify, c.Brain, c.Review, c.CodeRabbit
	oldP := c.ReviewProviders

	c.Defaults.GlobalCap = gc
	c.Defaults.ConcurrencyCap = cc
	c.Defaults.PollInterval = interval
	c.Defaults.Agent = settingsAgentValue(f.field("agent").text)

	// [defaults] project fallbacks — the counterpart of each inheritable
	// [[project]] key (config.ProjectInherits).
	c.Defaults.BranchPrefix = strings.TrimSpace(f.field("def_branch_prefix").text)
	c.Defaults.Symlinks = trimDropEmpty(f.field("def_symlinks").lines)
	c.Defaults.PostCreate = trimDropEmpty(f.field("def_post_create").lines)
	c.Defaults.Env = parseEnvLines(f.field("def_env").lines)
	c.Defaults.MatchLabels = trimDropEmpty(f.field("def_match_labels").lines)
	c.Defaults.MatchMode = f.field("def_match_mode").text
	c.Defaults.OnSentSetLabel = strings.TrimSpace(f.field("def_on_sent_set_label").text)
	c.Defaults.BlockedLabelID = strings.TrimSpace(f.field("def_blocked_label_id").text)
	c.Defaults.DedupMode = f.field("def_dedup_mode").text
	c.Defaults.PrioritySort = trimDropEmpty(f.field("def_priority_sort").lines)

	c.Notify.Desktop = f.field("notify_desktop").b
	c.Notify.SlackWebhookEnv = strings.TrimSpace(f.field("slack_webhook_env").text)

	c.Brain.Enabled = f.field("brain_enabled").b
	c.Brain.Model = strings.TrimSpace(f.field("brain_model").text)
	c.Brain.TimeoutSeconds = bt
	c.Brain.SummarizeEscalation = f.field("brain_esc").b
	c.Brain.SummarizeApproved = f.field("brain_appr").b

	// The review provider catalog replaces the two legacy tables. In catalog
	// mode the legacy tables MUST stay zero (a non-empty pair alongside a catalog
	// is a hard validation error); the read-only guard above means we only reach
	// here with legacy already cleared, so assigning the built catalog is safe.
	if !f.reviewLegacy {
		c.ReviewProviders = provs
	}

	rollback := func() {
		c.Defaults, c.Notify, c.Brain, c.Review, c.CodeRabbit = oldD, oldN, oldB, oldR, oldC
		c.ReviewProviders = oldP
		c.ResolveInheritance() // re-resolve projects against the restored defaults
	}
	if err := c.Validate(); err != nil {
		rollback()
		f.err = "invalid: " + err.Error()
		return settingsFormNone
	}
	if err := c.Save(f.cfgPath); err != nil {
		rollback()
		f.err = "save failed: " + err.Error()
		return settingsFormNone
	}
	return settingsFormSaved
}

// buildReviewProviders assembles the catalog from the per-kind fields, emitting
// an entry for every ENABLED kind (a disabled kind is dropped, so a config that
// enables nothing writes no [[review.provider]] table — fresh-omits). The two
// timeout fields are parsed here so a malformed one aborts save before any
// mutation; on that path f.err is set and a non-nil error is returned.
func (f *settingsForm) buildReviewProviders() ([]config.ReviewProvider, error) {
	cliTimeout, err := f.parseInt("pv_cli_timeout")
	if err != nil {
		return nil, err
	}
	claudeTimeout, err := f.parseInt("pv_claude_timeout")
	if err != nil {
		return nil, err
	}
	var out []config.ReviewProvider

	if f.field("pv_cli_enabled").b {
		p, _ := config.NewReviewProvider("coderabbit-cli")
		p.Enabled = true
		p.OnPROpen = f.field("pv_cli_onpropen").b
		p.Command = strings.TrimSpace(f.field("pv_cli_command").text)
		p.TimeoutSeconds = cliTimeout
		p.Notify = f.field("pv_cli_notify").b
		p.SendToAgent = f.field("pv_cli_send").b
		p.SetTransportTokens(trimDropEmpty(f.field("pv_cli_transports").lines))
		p.SetFallbackKinds(trimDropEmpty(f.field("pv_cli_fallback").lines))
		out = append(out, p)
	}
	if f.field("pv_watch_enabled").b {
		p, _ := config.NewReviewProvider("coderabbit-watch")
		p.Enabled = true
		p.Author = strings.TrimSpace(f.field("pv_watch_author").text)
		p.Notify = f.field("pv_watch_notify").b
		p.SendToAgent = f.field("pv_watch_send").b
		p.SetTransportTokens(trimDropEmpty(f.field("pv_watch_transports").lines))
		out = append(out, p)
	}
	if f.field("pv_claude_enabled").b {
		p, _ := config.NewReviewProvider("claude-session")
		p.Enabled = true
		p.OnPROpen = f.field("pv_claude_onpropen").b
		p.Model = strings.TrimSpace(f.field("pv_claude_model").text)
		p.TimeoutSeconds = claudeTimeout
		p.Notify = f.field("pv_claude_notify").b
		p.SendToAgent = f.field("pv_claude_send").b
		p.SetTransportTokens(trimDropEmpty(f.field("pv_claude_transports").lines))
		p.SetFallbackKinds(trimDropEmpty(f.field("pv_claude_fallback").lines))
		out = append(out, p)
	}
	return out, nil
}

// parseInt reads an int field, setting f.err (and returning the error) on a
// non-numeric value so save can abort before mutating.
func (f *settingsForm) parseInt(key string) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(f.field(key).text))
	if err != nil {
		f.err = strings.ReplaceAll(key, "_", " ") + ": not a whole number"
	}
	return v, err
}

// tabStrip renders the pinned tab header: the active tab highlighted, the rest
// faint. It sits above the (scrolling) field region so it never scrolls away.
func (f *settingsForm) tabStrip() string {
	parts := make([]string, 0, len(settingsTabs))
	for _, t := range settingsTabs {
		if t.tab == f.tab {
			parts = append(parts, boxTitleHi.Render(t.title))
			continue
		}
		parts = append(parts, faintText.Render(t.title))
	}
	return "  " + strings.Join(parts, faintText.Render(" · "))
}

// fieldRegion renders the ACTIVE tab's section/subsection headers and field
// lines, and records the line index each field's cursor should track
// (fieldLine[i], indexed tab-relative like f.cursor) so the scroller can keep it
// visible — for an open list that is the edited line, not the label row.
func (f *settingsForm) fieldRegion() (lines []string, fieldLine []int) {
	vis := f.visible()
	fieldLine = make([]int, len(vis))
	for vi, i := range vis {
		fld := &f.fields[i]
		if fld.section != "" {
			header := boxTitleHi.Render(fld.section)
			if fld.sectionNote != "" {
				header += "  " + faintText.Render("— "+fld.sectionNote)
			}
			lines = append(lines, header)
		}
		if fld.subsection != "" {
			// Indented sub-header grouping fields under a shared section.
			lines = append(lines, "  "+faintText.Render("▸ "+fld.subsection))
		}
		onField := vi == f.cursor
		open := onField && f.editing
		// Fields under a subsection are indented one level so the hierarchy reads.
		indent := ""
		if fld.indent {
			indent = "    "
		}
		marker := "  "
		lab := fmt.Sprintf("%-22s", fld.label)
		switch {
		case open:
			marker, lab = boxTitleHi.Render("▸ "), boxTitleHi.Render(lab)
		case onField:
			marker, lab = "› ", selStyle.Render(lab)
		}
		// The sort chain reads as a CHAIN, on one line, because the order is the
		// whole meaning — and because empty does not mean "nothing": it means
		// the built-in default applies, which a bare "(none)" hides.
		if fld.sortPick {
			fieldLine[vi] = len(lines)
			lines = append(lines, indent+marker+lab+f.sortChainText(fld))
			continue
		}
		if fld.kind == sfList || fld.kind == sfEnv {
			fieldLine[vi] = len(lines)
			lines = append(lines, indent+marker+lab)
			if len(fld.lines) == 0 {
				// wsPick / choice fields open a picker on enter, not the line editor.
				empty := "(none — enter to add)"
				if fld.wsPick || len(fld.choices) > 0 {
					empty = "(none — enter to pick)"
				}
				lines = append(lines, indent+"      "+faintText.Render(empty))
				continue
			}
			for j, e := range fld.lines {
				bullet, caret := faintText.Render("· "), ""
				typing := open && j == f.lineCur
				if typing {
					// Anchor the scroller on the line being typed, not the label.
					bullet, caret = warnText.Render("▸ "), "_"
					fieldLine[vi] = len(lines)
				}
				// A stored label reads as its name, not its UUID — except on the
				// line being typed, where the raw text is what backspace acts on.
				shown := e
				if fld.wsPick && !typing {
					shown = f.labelText(e)
				}
				lines = append(lines, indent+"      "+bullet+shown+caret)
			}
			continue
		}
		var val string
		switch fld.kind {
		case sfBool:
			val = boolGlyph(fld.b)
		case sfEnum:
			val = enumGlyph(fld.text)
		default:
			val = fld.text
			if fld.wsPick {
				// Resolves to the label's name when the value IS a known label;
				// a partially typed UUID matches nothing and shows verbatim, so
				// manual entry still reads correctly as it is typed.
				val = f.labelText(val)
			}
			if onField {
				val += "_"
			}
		}
		fieldLine[vi] = len(lines)
		lines = append(lines, indent+marker+lab+val)
	}
	return lines, fieldLine
}

// footerLines is the PINNED footer: the focused field's help, any error, and the
// key hint. Always shown below the (scrolling) field region.
func (f *settingsForm) footerLines() []string {
	var out []string
	if help := f.cur().help; help != "" {
		out = append(out, "", faintText.Render(help))
	}
	if f.err != "" {
		out = append(out, "", badText.Render("✗ "+f.err))
	}
	// Repairs Load made to config.toml (see config.Notices): shown while the
	// affected field is focused, so the value on screen not matching the file is
	// explained rather than mysterious. A warning, never blocking — the dropped
	// keys were already inert.
	if f.cur().sortPick {
		for _, n := range f.cfg.Notices() {
			if strings.Contains(n, "priority_sort") {
				out = append(out, "", warnText.Render("! "+n))
			}
		}
	}
	// Why the label picker is unavailable, shown only while a label field is
	// focused — it explains why enter is typing UUIDs rather than offering a list.
	if f.cur().wsPick && f.wsErr != "" {
		out = append(out, "", warnText.Render("! "+f.wsErr))
	}
	// The Review tab is read-only while the legacy tables are still present:
	// editing them alongside a catalog is a hard validation error, so the only
	// action is to migrate into the editable provider catalog.
	if f.reviewReadOnly() {
		out = append(out, "", warnText.Render("! legacy [review]/[coderabbit] tables — read-only. Press m to migrate to the editable provider catalog."))
	}
	hint := "tab/⇧tab section · ↑/↓ field · space toggle · ctrl-s save · esc cancel"
	switch {
	case f.reviewReadOnly():
		hint = "m migrate to providers · tab/⇧tab section · ↑/↓ field · ctrl-s save · esc cancel"
	case f.editing:
		hint = "editing " + f.cur().label + " — ↑/↓ line · enter new line · esc done"
	case f.cur().wsPick:
		hint = "enter labels · ctrl-r refresh · ↑/↓ field · ctrl-s save · esc cancel"
	}
	out = append(out, "", faintText.Render(hint))
	return out
}

// window returns at most `avail` lines of the field region, scrolled (updating
// f.scroll) so the focused field stays visible. When it clips, the top/bottom
// visible row is replaced with a faint "more" marker so the hidden content is
// discoverable. A margin keeps the cursor off an overwritten marker row.
func (f *settingsForm) window(region []string, fieldLine []int, avail int) []string {
	if avail < 1 {
		avail = 1
	}
	n := len(region)
	if n <= avail {
		f.scroll = 0
		return region
	}
	cur := fieldLine[f.cursor]
	top := f.scroll
	if cur < top+1 { // keep a row above the cursor for the ↑ marker
		top = cur - 1
	}
	if cur > top+avail-2 { // keep a row below for the ↓ marker
		top = cur - avail + 2
	}
	if top > n-avail {
		top = n - avail
	}
	if top < 0 {
		top = 0
	}
	f.scroll = top
	win := append([]string(nil), region[top:top+avail]...)
	if top > 0 {
		win[0] = faintText.Render("  ↑ more")
	}
	if top+avail < n {
		win[avail-1] = faintText.Render("  ↓ more")
	}
	return win
}

// view renders the FULL editor body unwindowed (no scrolling). The modal uses
// the windowed scrolledView; view is the plain full render for tests.
func (f *settingsForm) view() string {
	if f.picker != nil {
		return f.pickerView(len(f.picker.opts) + 2) // +2 for the pinned hint
	}
	region, _ := f.fieldRegion()
	all := append([]string{titleStyle.Render("settings"), "", f.tabStrip(), ""}, region...)
	all = append(all, f.footerLines()...)
	return strings.Join(all, "\n") + "\n"
}

// scrolledView renders the body to fit exactly bodyBudget lines: the pinned tab
// strip, a windowed cursor-following field region, and the footer pinned at the
// bottom. The first two returned lines are the (liftable) title + a blank,
// matching view's shape.
func (f *settingsForm) scrolledView(bodyBudget int) string {
	if f.picker != nil {
		return f.pickerView(bodyBudget)
	}
	region, fieldLine := f.fieldRegion()
	footer := f.footerLines()
	avail := bodyBudget - len(footer) - 2 // -2 for the tab strip + its blank
	if avail < 1 {
		avail = 1
	}
	win := f.window(region, fieldLine, avail)

	out := make([]string, 0, bodyBudget+2)
	out = append(out, titleStyle.Render("settings"), "", f.tabStrip(), "")
	out = append(out, win...)
	for i := len(win); i < avail; i++ { // pad so the footer sits at the bottom
		out = append(out, "")
	}
	out = append(out, footer...)
	return strings.Join(out, "\n") + "\n"
}

func boolGlyph(on bool) string {
	if on {
		return goodText.Render("✔ on")
	}
	return faintText.Render("✘ off")
}

// enumGlyph renders a cycle field's current value flanked by faint guillemets
// that signal it steps through a fixed set (space/enter) — distinct from a free
// text field, which shows a typing cursor. An empty value writes no key at all,
// so it reads as "(unset)" rather than as a blank; the field help says which
// built-in default then applies.
func enumGlyph(v string) string {
	if v == "" {
		v = "(unset)"
	}
	return faintText.Render("‹ ") + v + faintText.Render(" ›")
}

// settingsFormModal floats the settings editor over the dimmed cockpit, lifting
// its leading title into the box header (mirrors formModal). The body is
// scrolled to the box's inner height so it is never clipped on a short terminal.
func (m *rootModel) settingsFormModal() string {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	mw := W - 8
	if mw > 78 {
		mw = 78
	}
	if mw < 30 {
		mw = 30
	}
	mh := H - 4
	if mh > 34 {
		mh = 34
	}
	if mh < 8 {
		mh = 8
	}
	// box shows h-2 body rows; the title lifts into the border and the leading
	// blank costs one row, so the scroller gets mh-2-2 rows for content+footer.
	lines := strings.Split(strings.TrimRight(m.settings.scrolledView(mh-3), "\n"), "\n")
	title := "settings"
	if len(lines) > 0 {
		title = stripANSI(lines[0])
	}
	body := lines
	if len(body) >= 2 {
		body = body[2:]
	}
	for i := range body {
		body[i] = previewLine(body[i], mw-4)
	}
	modal := box(title, body, mw, mh, true)
	return strings.Join(placeModal(m.backdropLines(), modal, W), "\n")
}
