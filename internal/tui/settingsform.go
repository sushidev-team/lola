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
	{stCodeRabbit, "CodeRabbit"},
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
	d, n, br, rv, cr := cfg.Defaults, cfg.Notify, cfg.Brain, cfg.Review, cfg.CodeRabbit
	f := &settingsForm{
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

			// CodeRabbit — one tab, two subsections. [review] runs the CLI on
			// PR-open; [coderabbit] watches the PR for the app's comments. They are
			// separate config tables but the same integration, so they read as one.
			// No top-level section header: the tab title already says CodeRabbit,
			// and each subsection names its own table.
			{key: "review_enabled", tab: stCodeRabbit, subsection: "CLI review — execs `coderabbit review` locally on PR-open [review]", indent: true, label: "Enabled", help: "Opt-in CodeRabbit CLI QA pass: execs `coderabbit review` against the worktree when a session first opens a PR.", kind: sfBool, b: rv.Enabled},
			{key: "review_command", tab: stCodeRabbit, indent: true, label: "Command", help: "coderabbit argv override (space-split); empty = built-in default.", kind: sfText, text: rv.Command},
			{key: "review_onpropen", tab: stCodeRabbit, indent: true, label: "On PR open", help: "Run the pass automatically when a session first opens a PR.", kind: sfBool, b: rv.OnPROpen},
			{key: "review_send", tab: stCodeRabbit, indent: true, label: "Send to agent", help: "Feed findings back to the worker via the send-keys gate.", kind: sfBool, b: rv.SendToAgent},
			{key: "review_linear", tab: stCodeRabbit, indent: true, label: "Comment on Linear", help: "Also post findings as a Linear comment.", kind: sfBool, b: rv.CommentOnLinear},
			{key: "review_timeout", tab: stCodeRabbit, indent: true, label: "Timeout seconds", help: "Hard cap per review pass. Must be >= 0.", kind: sfInt, text: itoa(rv.TimeoutSeconds)},

			{key: "cr_enabled", tab: stCodeRabbit, subsection: "PR-comment watch — polls the PR for the app's comments [coderabbit]", indent: true, label: "Enabled", help: "Opt-in PR-comment watch: polls the GitHub PR for comments the CodeRabbit app (or another bot) leaves, and routes them. Unlike the CLI review above, this needs no local coderabbit binary.", kind: sfBool, b: cr.Enabled},
			{key: "cr_author", tab: stCodeRabbit, indent: true, label: "Author", help: "Login substring matched against comment authors. Default coderabbitai.", kind: sfText, text: crAuthor(cr)},
			{key: "cr_notify", tab: stCodeRabbit, indent: true, label: "Notify", help: "Surface each new comment to a human.", kind: sfBool, b: cr.Notify},
			{key: "cr_send", tab: stCodeRabbit, indent: true, label: "Send to agent", help: "Relay each new comment to the worker via the send-keys gate.", kind: sfBool, b: cr.SendToAgent},
			{key: "cr_linear", tab: stCodeRabbit, indent: true, label: "Comment on Linear", help: "Also mirror each new comment onto the Linear issue.", kind: sfBool, b: cr.CommentOnLinear},
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

// crAuthor pre-fills the author field with the effective default when unset, so
// the editor shows what the watch will actually match.
func crAuthor(cr config.CodeRabbitConfig) string {
	if cr.Author == "" {
		return config.DefaultCodeRabbitAuthor
	}
	return cr.Author
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
	"cr_enabled":     {"cr_notify", "cr_send"},
	"review_enabled": {"review_onpropen", "review_send"},
	"brain_enabled":  {"brain_esc", "brain_appr"},
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
	case "enter":
		if fld.sortPick {
			f.openSortPicker(fld)
			return nil, settingsFormNone
		}
		if fld.wsPick {
			return f.openLabelPicker(fld), settingsFormNone
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
		if fld.kind == sfText || fld.kind == sfInt {
			fld.text = dropLastRune(fld.text)
		}
	default:
		switch {
		case fld.kind == sfInt && len(k.Text) == 1 && k.Text >= "0" && k.Text <= "9":
			fld.text += k.Text
		case fld.kind == sfText && k.Text != "":
			fld.text += k.Text
		}
	}
	return nil, settingsFormNone
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
		// picker reopens on the fresh set.
		if fld := f.field(p.key); fld != nil {
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
	rt, err := f.parseInt("review_timeout")
	if err != nil {
		return settingsFormNone
	}
	interval, perr := time.ParseDuration(strings.TrimSpace(f.field("poll_interval").text))
	if perr != nil {
		f.err = "poll interval: " + perr.Error()
		return settingsFormNone
	}

	c := f.cfg
	// Snapshot the tables we touch so a failed Validate can be rolled back cleanly.
	// The slice/map members of Defaults (post_create, symlinks, env, …) are always
	// REPLACED below, never mutated in place, so the value copy is a complete
	// rollback (same reason NotifyConfig.Routing survives untouched).
	oldD, oldN, oldB, oldR, oldC := c.Defaults, c.Notify, c.Brain, c.Review, c.CodeRabbit

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

	c.Review.Enabled = f.field("review_enabled").b
	c.Review.Command = strings.TrimSpace(f.field("review_command").text)
	c.Review.OnPROpen = f.field("review_onpropen").b
	c.Review.SendToAgent = f.field("review_send").b
	c.Review.CommentOnLinear = f.field("review_linear").b
	c.Review.TimeoutSeconds = rt

	c.CodeRabbit.Enabled = f.field("cr_enabled").b
	c.CodeRabbit.Author = strings.TrimSpace(f.field("cr_author").text)
	c.CodeRabbit.Notify = f.field("cr_notify").b
	c.CodeRabbit.SendToAgent = f.field("cr_send").b
	c.CodeRabbit.CommentOnLinear = f.field("cr_linear").b

	if err := c.Validate(); err != nil {
		c.Defaults, c.Notify, c.Brain, c.Review, c.CodeRabbit = oldD, oldN, oldB, oldR, oldC
		f.err = "invalid: " + err.Error()
		return settingsFormNone
	}
	if err := c.Save(f.cfgPath); err != nil {
		c.Defaults, c.Notify, c.Brain, c.Review, c.CodeRabbit = oldD, oldN, oldB, oldR, oldC
		f.err = "save failed: " + err.Error()
		return settingsFormNone
	}
	return settingsFormSaved
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
				// wsPick fields open a picker on enter, not the line editor.
				empty := "(none — enter to add)"
				if fld.wsPick {
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
	hint := "tab/⇧tab section · ↑/↓ field · space toggle · ctrl-s save · esc cancel"
	switch {
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
