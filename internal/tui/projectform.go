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
	pfText projFieldKind = iota // single-line
	pfList                      // one value per line
	pfEnv                       // one KEY=value per line
)

type projField struct {
	label string
	help  string
	kind  projFieldKind
	buf   string // for list/env: entries joined by "\n"
}

type projectForm struct {
	cfgPath string
	cfg     *config.Config
	idx     int // index into cfg.Projects
	name    string
	fields  []projField
	cursor  int
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
			{label: "Path", help: "Local repository path.", kind: pfText, buf: p.Path},
			{label: "GitHub repo", help: "owner/name for PR checks.", kind: pfText, buf: p.Repo},
			{label: "Default branch", help: "Base branch worktrees fork from.", kind: pfText, buf: p.DefaultBranch},
			{label: "Symlinks", help: "One relative path per line, linked from main into each worktree (e.g. .env). Do NOT symlink vendor/ — it breaks PHP autoload; use post_create instead.", kind: pfList, buf: strings.Join(p.Symlinks, "\n")},
			{label: "Post-create", help: "One command per line, run in a fresh worktree before the agent (e.g. composer install).", kind: pfList, buf: strings.Join(p.PostCreate, "\n")},
			{label: "Env (KEY=value)", help: "One KEY=value per line, exported into the session and post_create commands.", kind: pfEnv, buf: envJoin(p.Env)},
		},
	}, true
}

func envJoin(env map[string]string) string {
	lines := make([]string, 0, len(env))
	for k, v := range env {
		lines = append(lines, k+"="+v)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func (f *projectForm) update(k tea.KeyPressMsg) projectFormEvent {
	f.err = ""
	switch k.String() {
	case "esc":
		return projFormCancel
	case "ctrl+s":
		return f.save()
	case "up":
		if f.cursor > 0 {
			f.cursor--
		}
		return projFormNone
	case "down", "tab":
		if f.cursor < len(f.fields)-1 {
			f.cursor++
		}
		return projFormNone
	case "enter":
		if fld := &f.fields[f.cursor]; fld.kind != pfText {
			fld.buf += "\n" // a new entry in a list/env field
		}
		return projFormNone
	case "backspace":
		fld := &f.fields[f.cursor]
		if r := []rune(fld.buf); len(r) > 0 {
			fld.buf = string(r[:len(r)-1])
		}
		return projFormNone
	}
	if k.Text != "" {
		f.fields[f.cursor].buf += k.Text
	}
	return projFormNone
}

// save writes the edited fields back into the project and persists config.toml.
func (f *projectForm) save() projectFormEvent {
	p := &f.cfg.Projects[f.idx]
	p.Path = strings.TrimSpace(f.fields[0].buf)
	p.Repo = strings.TrimSpace(f.fields[1].buf)
	p.DefaultBranch = strings.TrimSpace(f.fields[2].buf)
	p.Symlinks = splitLinesTrim(f.fields[3].buf)
	p.PostCreate = splitLinesTrim(f.fields[4].buf)
	p.Env = parseEnvLines(f.fields[5].buf)
	if err := f.cfg.Save(f.cfgPath); err != nil {
		f.err = "save failed: " + err.Error()
		return projFormNone
	}
	return projFormSaved
}

// splitLinesTrim splits on newlines, trims each, and drops empties — nil when
// nothing remains (so an emptied list clears the field).
func splitLinesTrim(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseEnvLines turns "KEY=value" lines into a map (later keys win); nil when
// empty. A line without '=' is ignored.
func parseEnvLines(s string) map[string]string {
	var out map[string]string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		k, v, ok := strings.Cut(ln, "=")
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
		body[i] = previewLine(body[i], mw-2)
	}
	modal := box(title, body, mw, mh, true)
	return strings.Join(placeModal(m.cockpitLines(), modal, W), "\n")
}

// view renders the editor body (the modal frame is added by projectFormModal).
func (f *projectForm) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("edit project: "+f.name) + "\n\n")
	for i := range f.fields {
		fld := &f.fields[i]
		marker := "  "
		lab := fmt.Sprintf("%-16s", fld.label)
		if i == f.cursor {
			marker = "› "
			lab = selStyle.Render(lab)
		}
		val := fld.buf
		if fld.kind == pfText {
			line := marker + lab + val
			if i == f.cursor {
				line += "_"
			}
			b.WriteString(line + "\n")
			continue
		}
		// list/env: label, then one indented entry per line (the last is edited).
		b.WriteString(marker + lab + "\n")
		entries := strings.Split(val, "\n")
		for j, e := range entries {
			caret := ""
			if i == f.cursor && j == len(entries)-1 {
				caret = "_"
			}
			b.WriteString("      " + faintText.Render("· ") + e + caret + "\n")
		}
	}
	if f.err != "" {
		b.WriteString("\n" + badText.Render("✗ "+f.err) + "\n")
	}
	b.WriteString("\n" + faintText.Render("↑/↓ field · enter new line (lists) · ctrl-s save · esc cancel") + "\n")
	return b.String()
}
