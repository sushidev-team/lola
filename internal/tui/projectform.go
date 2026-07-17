// Project editor (P): edits a [[project]]'s worktree setup — path, repo, default
// branch, symlinks, post_create, and env — from the TUI, so these no longer need
// hand-editing in config.toml. Reached with 'P' on the selected session's
// project; saved back to config.toml (atomic) and the daemon reloaded.
//
// Multi-value fields (symlinks / post_create / env) are edited as one entry per
// line: Enter inserts a newline, up/down move between fields, Ctrl-S saves, Esc
// cancels. This deliberately avoids symlinking vendor/ (breaks PHP autoload) —
// use post_create ("composer install") instead; the field help says so.
package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
)

type projectFormEvent int

const (
	projFormNone projectFormEvent = iota
	projFormCancel
	projFormSaved
)

type projFieldKind int

const (
	pfText  projFieldKind = iota // single-line
	pfList                       // one value per line
	pfEnv                        // one KEY=value per line
	pfAgent                      // fixed set cycled with space/enter (agent override)
)

type projField struct {
	label   string
	help    string
	kind    projFieldKind
	text    string   // pfText / pfAgent (current selection; "" = inherit for pfAgent)
	lines   []string // pfList / pfEnv (one entry per line)
	options []string // pfAgent: the values cycled through, in order
}

type projectForm struct {
	cfgPath string
	cfg     *config.Config
	idx     int // index into cfg.Projects
	name    string
	fields  []projField
	cursor  int  // which field
	editing bool // a list/env field is OPEN for line editing
	lineCur int  // which line, while editing
	err     string
}

// newProjectForm builds an editor for the named project, or (nil,false) if the
// project is not in config.
func newProjectForm(cfgPath string, cfg *config.Config, projectName string) (*projectForm, bool) {
	idx := -1
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == projectName {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, false
	}
	p := cfg.Projects[idx]
	return &projectForm{
		cfgPath: cfgPath, cfg: cfg, idx: idx, name: p.Name,
		fields: []projField{
			{label: "Path", help: "Local repository path.", kind: pfText, text: p.Path},
			{label: "GitHub repo", help: "owner/name for PR checks.", kind: pfText, text: p.Repo},
			{label: "Default branch", help: "Base branch worktrees fork from.", kind: pfText, text: p.DefaultBranch},
			{label: "Symlinks", help: "One relative path per line, linked from main into each worktree (e.g. .env). Do NOT symlink vendor/ — it breaks PHP autoload; use post_create instead.", kind: pfList, lines: p.Symlinks},
			{label: "Post-create", help: "One command per line, run in a fresh worktree before the agent (e.g. composer install).", kind: pfList, lines: p.PostCreate},
			{label: "Env (KEY=value)", help: "One KEY=value per line, exported into the session and post_create commands.", kind: pfEnv, lines: envLines(p.Env)},
			// Appended last on purpose: save() and the form tests index the fields
			// above by position, so the override slots in without shifting them.
			{label: "Agent", help: "Coding agent for this project's sessions; empty inherits the [defaults].agent global. space/enter cycles.", kind: pfAgent, text: p.Agent, options: projAgentOptions()},
		},
	}, true
}

// projAgentOptions is the per-project override picker's cycle order: an empty
// value (inherit [defaults].agent) followed by each concrete kind, so a project
// can inherit the global default or pin its own agent.
func projAgentOptions() []string {
	out := make([]string, 0, len(agent.Kinds)+1)
	out = append(out, "") // inherit the global default
	for _, k := range agent.Kinds {
		out = append(out, k.String())
	}
	return out
}

// agentField returns the pfAgent field (index-independent, so save() need not
// know where it sits in the list), or nil if absent.
func (f *projectForm) agentField() *projField {
	for i := range f.fields {
		if f.fields[i].kind == pfAgent {
			return &f.fields[i]
		}
	}
	return nil
}

// cycleProjAgent advances a pfAgent field to the next option (wrapping). A value
// outside the option set resets to the first (inherit).
func cycleProjAgent(fld *projField) {
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

func envLines(env map[string]string) []string {
	lines := make([]string, 0, len(env))
	for k, v := range env {
		lines = append(lines, k+"="+v)
	}
	sort.Strings(lines)
	return lines
}

func (f *projectForm) update(k tea.KeyPressMsg) projectFormEvent {
	f.err = ""
	if f.editing {
		return f.editList(k)
	}
	// Field navigation. Single-line text fields edit inline; list/env fields are
	// OPENED with enter (so arrows then move lines, not fields).
	fld := &f.fields[f.cursor]
	switch k.String() {
	case "esc":
		return projFormCancel
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
		case pfText:
			// text edits inline; enter is a no-op
		case pfAgent:
			cycleProjAgent(fld)
		default: // pfList / pfEnv: open the field for line editing
			if len(fld.lines) == 0 {
				fld.lines = []string{""}
			}
			f.editing, f.lineCur = true, 0
		}
	case "space":
		// Space cycles the agent picker but is a literal character in a text field.
		switch fld.kind {
		case pfAgent:
			cycleProjAgent(fld)
		case pfText:
			fld.text += " "
		}
	case "backspace":
		if fld.kind == pfText {
			fld.text = dropLastRune(fld.text)
		}
	default:
		if fld.kind == pfText && k.Text != "" {
			fld.text += k.Text
		}
	}
	return projFormNone
}

// editList drives the OPEN list/env field: arrows move between lines, enter adds
// a line, backspace edits (or removes an empty line), esc closes back to field
// navigation.
func (f *projectForm) editList(k tea.KeyPressMsg) projectFormEvent {
	fld := &f.fields[f.cursor]
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
	return projFormNone
}

func dropLastRune(s string) string {
	if r := []rune(s); len(r) > 0 {
		return string(r[:len(r)-1])
	}
	return s
}

// save writes the edited fields back into the project and persists config.toml.
// It snapshots the project first so a rejected edit (bad agent value caught by
// Validate, or a failed write) is rolled back and never leaves the in-memory
// config dirty — mirroring the global settings editor.
func (f *projectForm) save() projectFormEvent {
	p := &f.cfg.Projects[f.idx]
	old := *p // rollback target if Validate/Save rejects the edit
	p.Path = strings.TrimSpace(f.fields[0].text)
	p.Repo = strings.TrimSpace(f.fields[1].text)
	p.DefaultBranch = strings.TrimSpace(f.fields[2].text)
	p.Symlinks = trimDropEmpty(f.fields[3].lines)
	p.PostCreate = trimDropEmpty(f.fields[4].lines)
	p.Env = parseEnvLines(f.fields[5].lines)
	if af := f.agentField(); af != nil {
		p.Agent = strings.TrimSpace(af.text) // "" = inherit [defaults].agent
	}
	if err := f.cfg.Validate(); err != nil {
		*p = old
		f.err = "invalid: " + err.Error()
		return projFormNone
	}
	if err := f.cfg.Save(f.cfgPath); err != nil {
		*p = old
		f.err = "save failed: " + err.Error()
		return projFormNone
	}
	return projFormSaved
}

// trimDropEmpty trims each entry and drops blanks — nil when nothing remains.
func trimDropEmpty(in []string) []string {
	var out []string
	for _, e := range in {
		if t := strings.TrimSpace(e); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseEnvLines turns "KEY=value" entries into a map (later keys win); nil when
// empty. An entry without '=' is ignored.
func parseEnvLines(in []string) map[string]string {
	var out map[string]string
	for _, ln := range in {
		k, v, ok := strings.Cut(strings.TrimSpace(ln), "=")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

// openProjectForm opens the editor for the project of the current selection (the
// selected session's project, or the focused poll's project).
func (m *rootModel) openProjectForm() (tea.Model, tea.Cmd) {
	name := ""
	if m.focus == focusPolls {
		if p := m.selectedPoll(); p != nil {
			name = p.Project
		}
	} else if sel := m.sessions.selected(); sel != nil {
		name = sel.Project
	}
	if name == "" {
		m.sessions.flash, m.sessions.flashGood = "no project to edit here", false
		return m, nil
	}
	f, ok := newProjectForm(m.cfgPath, m.cfg, name)
	if !ok {
		m.sessions.flash, m.sessions.flashGood = "project "+name+" not found in config", false
		return m, nil
	}
	m.projForm = f
	return m, nil
}

// projectFormModal floats the project editor over the dimmed cockpit, lifting
// its leading title into the box header (mirrors formModal).
func (m *rootModel) projectFormModal() string {
	lines := strings.Split(strings.TrimRight(m.projForm.view(), "\n"), "\n")
	title := "project"
	if len(lines) > 0 {
		title = stripANSI(lines[0])
	}
	body := lines
	if len(body) >= 2 {
		body = body[2:]
	}
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
	if mh > 30 {
		mh = 30
	}
	if mh < 8 {
		mh = 8
	}
	for i := range body {
		body[i] = previewLine(body[i], mw-4)
	}
	modal := box(title, body, mw, mh, true)
	return strings.Join(placeModal(m.backdropLines(), modal, W), "\n")
}

// view renders the editor body (the modal frame is added by projectFormModal).
func (f *projectForm) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("edit project: "+f.name) + "\n\n")
	for i := range f.fields {
		fld := &f.fields[i]
		onField := i == f.cursor
		open := onField && f.editing // this list/env field is being line-edited
		marker := "  "
		lab := fmt.Sprintf("%-16s", fld.label)
		switch {
		case open:
			marker, lab = boxTitleHi.Render("▸ "), boxTitleHi.Render(lab) // open for editing
		case onField:
			marker, lab = "› ", selStyle.Render(lab)
		}
		if fld.kind == pfText {
			line := marker + lab + fld.text
			if onField {
				line += "_" // text fields edit inline
			}
			b.WriteString(line + "\n")
			continue
		}
		if fld.kind == pfAgent {
			val := fld.text
			if val == "" {
				val = "(inherit default)"
			}
			b.WriteString(marker + lab + faintText.Render("‹ ") + val + faintText.Render(" ›") + "\n")
			continue
		}
		// list/env: label, then one indented entry per line.
		b.WriteString(marker + lab + "\n")
		if len(fld.lines) == 0 {
			b.WriteString("      " + faintText.Render("(none — enter to add)") + "\n")
		}
		for j, e := range fld.lines {
			bullet := faintText.Render("· ")
			caret := ""
			if open && j == f.lineCur {
				bullet, caret = warnText.Render("▸ "), "_"
			}
			b.WriteString("      " + bullet + e + caret + "\n")
		}
	}
	if f.err != "" {
		b.WriteString("\n" + badText.Render("✗ "+f.err) + "\n")
	}
	hint := "↑/↓ field · enter edit list · type edits text · ctrl-s save · esc cancel"
	if f.editing {
		hint = "editing " + f.fields[f.cursor].label + " — ↑/↓ line · enter new line · esc done"
	}
	b.WriteString("\n" + faintText.Render(hint) + "\n")
	return b.String()
}
