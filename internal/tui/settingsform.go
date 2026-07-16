// Global settings editor (S): edits the config.toml tables that are NOT
// poll-scoped — [defaults], [notify], [brain], [review], and [coderabbit] — from
// the TUI, so the opt-in feature toggles no longer need hand-editing. Reached
// with 'S' from the cockpit; saved back to config.toml (atomic) and the daemon
// reloaded, exactly like the poll and project editors.
//
// Fields are a flat, navigable list grouped by section header. Three kinds:
// bool (space/enter toggles), text (type inline), and int (digits, validated on
// save). The Slack webhook and Linear key are secrets and are NEVER edited here —
// [notify] exposes only the env-var NAME that holds the webhook, never its value.
package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
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
)

type setField struct {
	key         string // stable identifier used by save()
	section     string // non-empty ⇒ a top-level section header is drawn ABOVE this field
	sectionNote string // faint one-liner beside the section header (what the section is FOR)
	subsection  string // non-empty ⇒ an indented sub-header is drawn ABOVE this field
	indent      bool   // this field sits under a subsection (rendered indented)
	label       string
	help        string
	kind        setFieldKind
	b           bool     // sfBool
	text        string   // sfText / sfInt (int held as text, parsed on save) / sfEnum (current selection)
	options     []string // sfEnum: the values cycled through, in order
}

type settingsForm struct {
	cfgPath string
	cfg     *config.Config
	fields  []setField
	cursor  int
	scroll  int // first visible field-region line (cursor-following viewport)
	err     string
}

// newSettingsForm builds the editor pre-filled from the live config. The int
// fields render the resolved values (e.g. review.timeout defaults to 300 once
// enabled), so saving without touching them is a faithful round-trip.
func newSettingsForm(cfgPath string, cfg *config.Config) *settingsForm {
	itoa := strconv.Itoa
	d, n, br, rv, cr := cfg.Defaults, cfg.Notify, cfg.Brain, cfg.Review, cfg.CodeRabbit
	return &settingsForm{
		cfgPath: cfgPath,
		cfg:     cfg,
		fields: []setField{
			// [defaults]
			{key: "global_cap", section: "[defaults]", sectionNote: "caps & poll interval", label: "Global cap", help: "Max concurrent sessions across all polls. Must be > 0.", kind: sfInt, text: itoa(d.GlobalCap)},
			{key: "concurrency_cap", label: "Concurrency cap", help: "Default per-poll cap (a poll's own cap overrides). 0 = no per-poll default.", kind: sfInt, text: itoa(d.ConcurrencyCap)},
			{key: "poll_interval", label: "Poll interval", help: "How often each poll ticks, as a Go duration (e.g. 60s, 2m). Clamped up to 30s.", kind: sfText, text: d.PollInterval.String()},
			{key: "agent", label: "Coding agent", help: "Default coding agent each session spawns (a [[project]] can override). space/enter cycles claude|codex|opencode.", kind: sfEnum, options: agentKindStrings(), text: defaultAgentDisplay(d.Agent)},

			// [notify]
			{key: "notify_desktop", section: "[notify]", sectionNote: "desktop / Slack alerts", label: "Desktop banners", help: "Native desktop notifications (macOS only).", kind: sfBool, b: n.Desktop},
			{key: "slack_webhook_env", label: "Slack webhook env", help: "NAME of the env var holding the Slack webhook URL (never the URL itself — that stays a secret). Empty = no Slack.", kind: sfText, text: n.SlackWebhookEnv},

			// [brain]
			{key: "brain_enabled", section: "[brain]", sectionNote: "claude notification summaries", label: "Enabled", help: "Opt-in headless-claude summarizer for escalation / approved notifications.", kind: sfBool, b: br.Enabled},
			{key: "brain_model", label: "Model", help: "claude --model override; empty = claude's default.", kind: sfText, text: br.Model},
			{key: "brain_timeout", label: "Timeout seconds", help: "Hard cap per summary call. Must be >= 0.", kind: sfInt, text: itoa(br.TimeoutSeconds)},
			{key: "brain_esc", label: "Summarize escalation", help: "Summarize WHY a session is blocked on escalation.", kind: sfBool, b: br.SummarizeEscalation},
			{key: "brain_appr", label: "Summarize approved", help: "Summarize PR risk on approved+green.", kind: sfBool, b: br.SummarizeApproved},

			// CodeRabbit — one section, two subsections. [review] runs the CLI on
			// PR-open; [coderabbit] watches the PR for the app's comments. They are
			// separate config tables but the same integration, so they read as one.
			{key: "review_enabled", section: "CodeRabbit", sectionNote: "CLI review + PR-comment watch", subsection: "CLI review — execs `coderabbit review` locally on PR-open [review]", indent: true, label: "Enabled", help: "Opt-in CodeRabbit CLI QA pass: execs `coderabbit review` against the worktree when a session first opens a PR.", kind: sfBool, b: rv.Enabled},
			{key: "review_command", indent: true, label: "Command", help: "coderabbit argv override (space-split); empty = built-in default.", kind: sfText, text: rv.Command},
			{key: "review_onpropen", indent: true, label: "On PR open", help: "Run the pass automatically when a session first opens a PR.", kind: sfBool, b: rv.OnPROpen},
			{key: "review_send", indent: true, label: "Send to agent", help: "Feed findings back to the worker via the send-keys gate.", kind: sfBool, b: rv.SendToAgent},
			{key: "review_linear", indent: true, label: "Comment on Linear", help: "Also post findings as a Linear comment.", kind: sfBool, b: rv.CommentOnLinear},
			{key: "review_timeout", indent: true, label: "Timeout seconds", help: "Hard cap per review pass. Must be >= 0.", kind: sfInt, text: itoa(rv.TimeoutSeconds)},

			{key: "cr_enabled", subsection: "PR-comment watch — polls the PR for the app's comments [coderabbit]", indent: true, label: "Enabled", help: "Opt-in PR-comment watch: polls the GitHub PR for comments the CodeRabbit app (or another bot) leaves, and routes them. Unlike the CLI review above, this needs no local coderabbit binary.", kind: sfBool, b: cr.Enabled},
			{key: "cr_author", indent: true, label: "Author", help: "Login substring matched against comment authors. Default coderabbitai.", kind: sfText, text: crAuthor(cr)},
			{key: "cr_notify", indent: true, label: "Notify", help: "Surface each new comment to a human.", kind: sfBool, b: cr.Notify},
			{key: "cr_send", indent: true, label: "Send to agent", help: "Relay each new comment to the worker via the send-keys gate.", kind: sfBool, b: cr.SendToAgent},
			{key: "cr_linear", indent: true, label: "Comment on Linear", help: "Also mirror each new comment onto the Linear issue.", kind: sfBool, b: cr.CommentOnLinear},
		},
	}
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
// override carries that (see projectform.go).
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

func (f *settingsForm) update(k tea.KeyPressMsg) settingsFormEvent {
	f.err = ""
	fld := &f.fields[f.cursor]
	switch k.String() {
	case "esc":
		return settingsFormCancel
	case "ctrl+s":
		return f.save()
	case "up":
		if f.cursor > 0 {
			f.cursor--
		}
	case "down", "tab":
		if f.cursor < len(f.fields)-1 {
			f.cursor++
		}
	case "enter":
		switch fld.kind {
		case sfBool:
			f.toggleBool(fld)
		case sfEnum:
			cycleEnum(fld)
		}
	case "space":
		// Space toggles a bool and cycles an enum, but is a literal character in a
		// text field (e.g. the review command argv); an int field ignores it.
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
	return settingsFormNone
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
	// Snapshot the tables we touch so a failed Validate can be rolled back cleanly
	// (NotifyConfig.Routing is a map we never edit — the value copy keeps its ref).
	oldD, oldN, oldB, oldR, oldC := c.Defaults, c.Notify, c.Brain, c.Review, c.CodeRabbit

	c.Defaults.GlobalCap = gc
	c.Defaults.ConcurrencyCap = cc
	c.Defaults.PollInterval = interval
	c.Defaults.Agent = settingsAgentValue(f.field("agent").text)

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

// fieldRegion renders every section/subsection header and field line, and
// records the line index of each field's OWN row (fieldLine[i]) so the scroller
// can keep the focused field visible.
func (f *settingsForm) fieldRegion() (lines []string, fieldLine []int) {
	fieldLine = make([]int, len(f.fields))
	for i := range f.fields {
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
		onField := i == f.cursor
		// Fields under a subsection are indented one level so the hierarchy reads.
		indent := ""
		if fld.indent {
			indent = "    "
		}
		marker := "  "
		lab := fmt.Sprintf("%-22s", fld.label)
		if onField {
			marker, lab = "› ", selStyle.Render(lab)
		}
		var val string
		switch fld.kind {
		case sfBool:
			val = boolGlyph(fld.b)
		case sfEnum:
			val = enumGlyph(fld.text)
		default:
			val = fld.text
			if onField {
				val += "_"
			}
		}
		fieldLine[i] = len(lines)
		lines = append(lines, indent+marker+lab+val)
	}
	return lines, fieldLine
}

// footerLines is the PINNED footer: the focused field's help, any error, and the
// key hint. Always shown below the (scrolling) field region.
func (f *settingsForm) footerLines() []string {
	var out []string
	if help := f.fields[f.cursor].help; help != "" {
		out = append(out, "", faintText.Render(help))
	}
	if f.err != "" {
		out = append(out, "", badText.Render("✗ "+f.err))
	}
	out = append(out, "", faintText.Render("↑/↓ field · space toggle · type edits · ctrl-s save · esc cancel"))
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
	region, _ := f.fieldRegion()
	all := append([]string{titleStyle.Render("settings"), ""}, region...)
	all = append(all, f.footerLines()...)
	return strings.Join(all, "\n") + "\n"
}

// scrolledView renders the body to fit exactly bodyBudget lines: a windowed,
// cursor-following field region with the footer pinned at the bottom. The first
// two returned lines are the (liftable) title + a blank, matching view's shape.
func (f *settingsForm) scrolledView(bodyBudget int) string {
	region, fieldLine := f.fieldRegion()
	footer := f.footerLines()
	avail := bodyBudget - len(footer)
	if avail < 1 {
		avail = 1
	}
	win := f.window(region, fieldLine, avail)

	out := make([]string, 0, bodyBudget+2)
	out = append(out, titleStyle.Render("settings"), "")
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
// text field, which shows a typing cursor. An empty value (an "inherit"
// selection) reads as such rather than as a blank.
func enumGlyph(v string) string {
	if v == "" {
		v = "(inherit)"
	}
	return faintText.Render("‹ ") + v + faintText.Render(" ›")
}

// settingsFormModal floats the settings editor over the dimmed cockpit, lifting
// its leading title into the box header (mirrors projectFormModal). The body is
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
	return strings.Join(placeModal(m.cockpitLines(), modal, W), "\n")
}
