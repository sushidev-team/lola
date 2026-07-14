// Cascading create/edit form. Each Linear-backed level only becomes
// available after the prior selection (team gates everything team-scoped);
// selects open a hand-rolled picker overlay (no bubbles dependency).
package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

type formEvent int

const (
	formNone formEvent = iota
	formCancel
	formSaved
)

type fieldID int

const (
	fName fieldID = iota
	fTeam
	fProject
	fCycleMode
	fCycle
	fStates
	fLabels
	fMatchMode
	fAssignee
	fAssigneeUser
	fNativeProject
	fRepo
	fCap
	fDedup
	fSetLabel
	fRemoveLabel
	fSave
)

type pickOpt struct{ id, label string }

type picker struct {
	title  string
	field  fieldID
	opts   []pickOpt
	multi  bool
	cursor int
	sel    map[string]bool
}

type formModel struct {
	cfg      *config.Config
	isNew    bool
	origName string
	poll     config.Poll // working copy
	capBuf   string      // text buffer for concurrency_cap

	teams   []linear.Team // available before a team is picked
	meta    *teamMeta
	loading string
	loadErr string

	cursor int
	picker *picker
	errs   []string // validation errors shown at the bottom
}

func newFormModel(cfg *config.Config, existing *config.Poll) (*formModel, tea.Cmd) {
	f := &formModel{cfg: cfg, isNew: existing == nil}
	if existing != nil {
		f.origName = existing.Name
		f.poll = *existing
		f.poll.StateIDs = slices.Clone(existing.StateIDs)
		f.poll.MatchLabels = slices.Clone(existing.MatchLabels)
		f.poll.PrioritySort = slices.Clone(existing.PrioritySort)
		if existing.ConcurrencyCap > 0 {
			f.capBuf = strconv.Itoa(existing.ConcurrencyCap)
		}
	} else {
		f.poll = config.Poll{CycleMode: "none", MatchMode: "any", AssigneeMode: "anyone", DedupMode: "seen"}
		f.capBuf = "1"
	}
	var cmd tea.Cmd
	if f.poll.TeamID != "" {
		f.loading = "loading team metadata…"
		cmd = loadMetaCmd(cfg, f.poll.TeamID, false)
	} else {
		f.loading = "loading teams…"
		cmd = fetchTeamsCmd(cfg)
	}
	return f, cmd
}

// fields returns the currently visible fields; conditional levels appear
// only once their gating selection exists.
func (f *formModel) fields() []fieldID {
	fs := []fieldID{fName, fTeam}
	if f.poll.TeamID != "" {
		fs = append(fs, fProject, fCycleMode)
		if f.poll.CycleMode == "pinned" {
			fs = append(fs, fCycle)
		}
		fs = append(fs, fStates, fLabels, fMatchMode, fAssignee)
		if f.poll.AssigneeMode == "user" {
			fs = append(fs, fAssigneeUser)
		}
		fs = append(fs, fNativeProject, fRepo, fCap, fDedup)
		if f.poll.DedupMode == "label" {
			fs = append(fs, fSetLabel, fRemoveLabel)
		}
	}
	return append(fs, fSave)
}

func (f *formModel) update(msg tea.Msg) (tea.Cmd, formEvent) {
	switch v := msg.(type) {
	case teamsMsg:
		f.loading = ""
		if v.err != nil {
			f.loadErr = "linear teams: " + v.err.Error()
		} else {
			f.teams, f.loadErr = v.teams, ""
		}
	case metaMsg:
		f.loading = ""
		if v.err != nil {
			f.loadErr = "linear: " + v.err.Error()
		} else if v.teamID == f.poll.TeamID {
			f.meta, f.loadErr = v.meta, ""
		}
	case tea.KeyMsg:
		return f.key(v)
	}
	return nil, formNone
}

func (f *formModel) key(k tea.KeyMsg) (tea.Cmd, formEvent) {
	if f.picker != nil {
		return f.pickerKey(k), formNone
	}
	fields := f.fields()
	if f.cursor >= len(fields) {
		f.cursor = len(fields) - 1
	}
	cur := fields[f.cursor]

	switch k.String() {
	case "esc":
		return nil, formCancel
	case "up", "shift+tab":
		if f.cursor > 0 {
			f.cursor--
		}
		return nil, formNone
	case "down", "tab":
		if f.cursor < len(fields)-1 {
			f.cursor++
		}
		return nil, formNone
	case "ctrl+r":
		return f.refresh(), formNone
	case "enter":
		return f.interact(cur)
	}

	// Text editing on name/repo/cap; on other fields plain 'r' refreshes.
	switch cur {
	case fName, fRepo, fCap:
		switch k.Type {
		case tea.KeyRunes, tea.KeySpace:
			s := string(k.Runes)
			if k.Type == tea.KeySpace {
				s = " "
			}
			switch cur {
			case fName:
				f.poll.Name += s
			case fRepo:
				f.poll.Repo += s
			default:
				for _, r := range s {
					if r >= '0' && r <= '9' {
						f.capBuf += string(r)
					}
				}
			}
		case tea.KeyBackspace:
			switch {
			case cur == fName && f.poll.Name != "":
				rs := []rune(f.poll.Name)
				f.poll.Name = string(rs[:len(rs)-1])
			case cur == fRepo && f.poll.Repo != "":
				rs := []rune(f.poll.Repo)
				f.poll.Repo = string(rs[:len(rs)-1])
			case cur == fCap && f.capBuf != "":
				f.capBuf = f.capBuf[:len(f.capBuf)-1]
			}
		}
	default:
		if k.String() == "r" {
			return f.refresh(), formNone
		}
	}
	return nil, formNone
}

func (f *formModel) refresh() tea.Cmd {
	f.loadErr = ""
	if f.poll.TeamID == "" {
		f.loading = "loading teams…"
		return fetchTeamsCmd(f.cfg)
	}
	f.loading = "refreshing team metadata…"
	return loadMetaCmd(f.cfg, f.poll.TeamID, true)
}

func (f *formModel) interact(cur fieldID) (tea.Cmd, formEvent) {
	switch cur {
	case fName, fRepo, fCap:
		// enter advances to the next field
		if f.cursor < len(f.fields())-1 {
			f.cursor++
		}
		return nil, formNone
	case fSave:
		return f.save()
	default:
		return f.openPicker(cur), formNone
	}
}

var metaFields = map[fieldID]bool{
	fProject: true, fCycle: true, fStates: true, fLabels: true,
	fAssigneeUser: true, fSetLabel: true, fRemoveLabel: true,
}

func (f *formModel) openPicker(cur fieldID) tea.Cmd {
	if metaFields[cur] && f.meta == nil {
		if f.poll.TeamID == "" {
			f.loadErr = "select a team first"
			return nil
		}
		f.loading = "loading team metadata…"
		return loadMetaCmd(f.cfg, f.poll.TeamID, false)
	}

	var (
		opts     []pickOpt
		multi    bool
		title    string
		selected []string
	)
	switch cur {
	case fTeam:
		teams := f.teams
		if f.meta != nil && len(f.meta.Teams) > 0 {
			teams = f.meta.Teams
		}
		if len(teams) == 0 {
			f.loading = "loading teams…"
			return fetchTeamsCmd(f.cfg)
		}
		title = "Team"
		for _, t := range teams {
			opts = append(opts, pickOpt{t.ID, t.Key + " — " + t.Name})
		}
		selected = []string{f.poll.TeamID}
	case fProject:
		title = "Project (optional)"
		opts = append(opts, pickOpt{"", "(none)"})
		for _, p := range f.meta.Projects {
			opts = append(opts, pickOpt{p.ID, p.Name + " (" + p.State + ")"})
		}
		selected = []string{f.poll.ProjectID}
	case fCycleMode:
		title = "Cycle mode"
		opts = []pickOpt{{"none", "none"}, {"active", "active"}, {"pinned", "pinned"}}
		selected = []string{f.poll.CycleMode}
	case fCycle:
		title = "Pinned cycle"
		for _, c := range f.meta.Cycles {
			lbl := fmt.Sprintf("#%d %s", c.Number, c.Name)
			if f.meta.ActiveCycle != nil && c.ID == f.meta.ActiveCycle.ID {
				lbl += " (active)"
			}
			opts = append(opts, pickOpt{c.ID, lbl})
		}
		if len(opts) == 0 {
			f.loadErr = "team has no cycles"
			return nil
		}
		selected = []string{f.poll.CycleID}
	case fStates:
		multi, title = true, "Workflow states"
		for _, s := range f.meta.States {
			opts = append(opts, pickOpt{s.ID, s.Name + " [" + s.Type + "]"})
		}
		selected = f.poll.StateIDs
	case fLabels:
		multi, title = true, "Trigger labels"
		for _, l := range f.meta.Labels {
			opts = append(opts, pickOpt{l.ID, labelDisplay(l)})
		}
		selected = f.poll.MatchLabels
	case fMatchMode:
		title = "Label match mode"
		opts = []pickOpt{{"any", "any"}, {"all", "all"}}
		selected = []string{f.poll.MatchMode}
	case fAssignee:
		title = "Assignee"
		opts = []pickOpt{{"anyone", "anyone"}, {"me", "me"}, {"user", "specific user"}}
		selected = []string{f.poll.AssigneeMode}
	case fAssigneeUser:
		title = "Assigned user"
		for _, u := range f.meta.Members {
			lbl := u.Name + " <" + u.Email + ">"
			if !u.Active {
				lbl += " (inactive)"
			}
			opts = append(opts, pickOpt{u.ID, lbl})
		}
		if len(opts) == 0 {
			f.loadErr = "team has no members"
			return nil
		}
		selected = []string{f.poll.AssigneeUserID}
	case fNativeProject:
		if len(f.cfg.Projects) == 0 {
			f.loadErr = "no [[project]] entries in config.toml — define one before creating a poll"
			return nil
		}
		title = "Project"
		for _, pr := range f.cfg.Projects {
			lbl := pr.Name
			if pr.Repo != "" {
				lbl += " (" + pr.Repo + ")"
			}
			opts = append(opts, pickOpt{pr.Name, lbl})
		}
		selected = []string{f.poll.Project}
	case fDedup:
		title = "Dedup mode"
		opts = []pickOpt{{"label", "label (flip trigger label on spawn)"}, {"seen", "seen (local seen-file)"}}
		selected = []string{f.poll.DedupMode}
	case fSetLabel, fRemoveLabel:
		if cur == fSetLabel {
			title = "on_sent: set label"
			selected = []string{f.poll.OnSentSetLabel}
		} else {
			title = "on_sent: remove label"
			selected = []string{f.poll.OnSentRemoveLabel}
		}
		for _, l := range f.meta.Labels {
			opts = append(opts, pickOpt{l.ID, labelDisplay(l)})
		}
	}

	p := &picker{title: title, field: cur, opts: opts, multi: multi, sel: map[string]bool{}}
	for _, id := range selected {
		if id != "" {
			p.sel[id] = true
		}
	}
	if !multi {
		for i, o := range opts {
			if p.sel[o.id] {
				p.cursor = i
				break
			}
		}
	}
	f.picker = p
	return nil
}

func (f *formModel) pickerKey(k tea.KeyMsg) tea.Cmd {
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
	case " ":
		if p.multi && len(p.opts) > 0 {
			id := p.opts[p.cursor].id
			if p.sel[id] {
				delete(p.sel, id)
			} else {
				p.sel[id] = true
			}
		}
	case "enter":
		f.picker = nil
		return f.applyPick(p)
	}
	return nil
}

func (f *formModel) applyPick(p *picker) tea.Cmd {
	if len(p.opts) == 0 {
		return nil
	}
	if p.multi {
		var ids []string
		for _, o := range p.opts { // keep option order, not toggle order
			if p.sel[o.id] {
				ids = append(ids, o.id)
			}
		}
		switch p.field {
		case fStates:
			f.poll.StateIDs = ids
		case fLabels:
			f.poll.MatchLabels = ids
		}
		return nil
	}

	id := p.opts[p.cursor].id
	switch p.field {
	case fTeam:
		if id != f.poll.TeamID {
			f.poll.TeamID = id
			// Everything team-scoped is invalid across teams.
			f.poll.ProjectID, f.poll.CycleID = "", ""
			f.poll.StateIDs, f.poll.MatchLabels = nil, nil
			f.poll.AssigneeUserID = ""
			f.poll.OnSentSetLabel, f.poll.OnSentRemoveLabel = "", ""
			f.meta = nil
			f.loading = "loading team metadata…"
			return loadMetaCmd(f.cfg, id, false)
		}
	case fProject:
		f.poll.ProjectID = id
	case fCycleMode:
		f.poll.CycleMode = id
		if id != "pinned" {
			f.poll.CycleID = ""
		}
	case fCycle:
		f.poll.CycleID = id
	case fMatchMode:
		f.poll.MatchMode = id
	case fAssignee:
		f.poll.AssigneeMode = id
		if id != "user" {
			f.poll.AssigneeUserID = ""
		}
	case fAssigneeUser:
		f.poll.AssigneeUserID = id
	case fNativeProject:
		f.poll.Project = id
	case fDedup:
		f.poll.DedupMode = id
	case fSetLabel:
		f.poll.OnSentSetLabel = id
	case fRemoveLabel:
		f.poll.OnSentRemoveLabel = id
	}
	return nil
}

func (f *formModel) save() (tea.Cmd, formEvent) {
	f.errs = nil
	p := f.poll
	p.Name = strings.TrimSpace(p.Name)
	p.Repo = strings.TrimSpace(p.Repo) // format checked by nc.Validate below
	p.ConcurrencyCap = 0
	if f.capBuf != "" {
		n, err := strconv.Atoi(f.capBuf)
		if err != nil || n <= 0 {
			f.errs = append(f.errs, "concurrency_cap must be a positive integer")
		} else {
			p.ConcurrencyCap = n
		}
	}
	if len(p.PrioritySort) == 0 {
		p.PrioritySort = []string{"priority", "createdAt"}
	}

	// The poll's [[project]] reference is resolved by nc.Validate below
	// (ProjectByName). Surface an early, friendly error when it is unset.
	if p.Project == "" {
		f.errs = append(f.errs, "project is required — pick a [[project]] entry")
	}

	// Rebase on the on-disk config: the daemon (enable/disable) or another
	// TUI action may have persisted changes since this form opened; building
	// on the stale snapshot would silently revert them.
	base := f.cfg
	path, pathErr := config.DefaultPath()
	if pathErr == nil {
		if fresh, err := config.Load(path); err == nil {
			base = fresh
		}
	}
	nc := *base
	nc.Polls = slices.Clone(base.Polls)
	idx := -1
	if !f.isNew {
		for i := range nc.Polls {
			if nc.Polls[i].Name == f.origName {
				idx = i
				break
			}
		}
	}
	if idx >= 0 {
		nc.Polls[idx] = p
	} else {
		nc.Polls = append(nc.Polls, p)
	}
	// A fresh config has no global cap; default it so the first save works.
	if nc.Defaults.GlobalCap <= 0 {
		nc.Defaults.GlobalCap = 4
	}
	if err := nc.Validate(); err != nil {
		f.errs = append(f.errs, strings.Split(err.Error(), "\n")...)
	}
	if len(f.errs) > 0 {
		return nil, formNone
	}

	err := pathErr
	if err == nil {
		err = nc.Save(path)
	}
	if err != nil {
		f.errs = append(f.errs, "save failed: "+err.Error())
		return nil, formNone
	}
	return nil, formSaved
}

// ---- view ----

func (f *formModel) view(height int) string {
	if f.picker != nil {
		return f.picker.view(height)
	}
	title := "New poll"
	if !f.isNew {
		title = "Edit poll: " + f.origName
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")

	fields := f.fields()
	if f.cursor >= len(fields) {
		f.cursor = len(fields) - 1
	}
	for i, fd := range fields {
		marker := "  "
		if i == f.cursor {
			marker = "› "
		}
		line := marker + fmt.Sprintf("%-22s", f.label(fd)) + f.display(fd)
		if i == f.cursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("  " + faintText.Render(fmt.Sprintf("%-22spriority, createdAt (default)", "Priority sort")) + "\n")

	if f.loading != "" {
		b.WriteString("\n" + faintText.Render(f.loading) + "\n")
	}
	if f.loadErr != "" {
		b.WriteString("\n" + badText.Render(f.loadErr) + "\n")
	}
	if len(f.errs) > 0 {
		b.WriteString("\n")
		for _, e := range f.errs {
			b.WriteString(badText.Render("✗ "+e) + "\n")
		}
	}
	b.WriteString("\n" + faintText.Render("↑/↓ move · enter select/edit · r refresh linear cache · esc back") + "\n")
	return b.String()
}

func (f *formModel) label(fd fieldID) string {
	switch fd {
	case fName:
		return "Name"
	case fTeam:
		return "Team"
	case fProject:
		return "Project"
	case fCycleMode:
		return "Cycle mode"
	case fCycle:
		return "Pinned cycle"
	case fStates:
		return "Workflow states"
	case fLabels:
		return "Trigger labels"
	case fMatchMode:
		return "Label match mode"
	case fAssignee:
		return "Assignee"
	case fAssigneeUser:
		return "Assigned user"
	case fNativeProject:
		return "Project"
	case fRepo:
		return "GitHub repo"
	case fCap:
		return "Concurrency cap"
	case fDedup:
		return "Dedup mode"
	case fSetLabel:
		return "on_sent set label"
	case fRemoveLabel:
		return "on_sent remove label"
	case fSave:
		return ""
	}
	return ""
}

func (f *formModel) display(fd fieldID) string {
	sel := "(select)"
	switch fd {
	case fName:
		if f.poll.Name == "" {
			return faintText.Render("(type a name)")
		}
		return f.poll.Name
	case fTeam:
		if f.poll.TeamID == "" {
			return sel
		}
		teams := f.teams
		if f.meta != nil {
			teams = f.meta.Teams
		}
		for _, t := range teams {
			if t.ID == f.poll.TeamID {
				return t.Key + " — " + t.Name
			}
		}
		return shortID(f.poll.TeamID)
	case fProject:
		if f.poll.ProjectID == "" {
			return "(none)"
		}
		if f.meta != nil {
			for _, p := range f.meta.Projects {
				if p.ID == f.poll.ProjectID {
					return p.Name
				}
			}
		}
		return shortID(f.poll.ProjectID)
	case fCycleMode:
		return f.poll.CycleMode
	case fCycle:
		if f.poll.CycleID == "" {
			return sel
		}
		if f.meta != nil {
			for _, c := range f.meta.Cycles {
				if c.ID == f.poll.CycleID {
					return fmt.Sprintf("#%d %s", c.Number, c.Name)
				}
			}
		}
		return shortID(f.poll.CycleID)
	case fStates:
		return f.joinNames(f.poll.StateIDs, f.stateName)
	case fLabels:
		return f.joinNames(f.poll.MatchLabels, f.labelName)
	case fMatchMode:
		return f.poll.MatchMode
	case fAssignee:
		return f.poll.AssigneeMode
	case fAssigneeUser:
		if f.poll.AssigneeUserID == "" {
			return sel
		}
		if f.meta != nil {
			for _, u := range f.meta.Members {
				if u.ID == f.poll.AssigneeUserID {
					return u.Name
				}
			}
		}
		return shortID(f.poll.AssigneeUserID)
	case fNativeProject:
		if f.poll.Project == "" {
			return sel
		}
		return f.poll.Project
	case fRepo:
		if f.poll.Repo == "" {
			// The daemon owns the fallback: PR checks use the [[project]]
			// repo when this is empty (dispatch/observer/reconcile).
			return faintText.Render("(owner/name — empty falls back to the [[project]] repo)")
		}
		return f.poll.Repo
	case fCap:
		if f.capBuf == "" {
			return faintText.Render(fmt.Sprintf("(default: %d)", f.cfg.Defaults.ConcurrencyCap))
		}
		return f.capBuf
	case fDedup:
		return f.poll.DedupMode
	case fSetLabel:
		if f.poll.OnSentSetLabel == "" {
			return sel
		}
		return f.labelName(f.poll.OnSentSetLabel)
	case fRemoveLabel:
		if f.poll.OnSentRemoveLabel == "" {
			return sel
		}
		return f.labelName(f.poll.OnSentRemoveLabel)
	case fSave:
		return "[ Save ]"
	}
	return ""
}

func (f *formModel) joinNames(ids []string, name func(string) string) string {
	if len(ids) == 0 {
		return "(none)"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = name(id)
	}
	return strings.Join(parts, ", ")
}

func (f *formModel) stateName(id string) string {
	if f.meta != nil {
		for _, s := range f.meta.States {
			if s.ID == id {
				return s.Name
			}
		}
	}
	return shortID(id)
}

func (f *formModel) labelName(id string) string {
	if f.meta != nil {
		for _, l := range f.meta.Labels {
			if l.ID == id {
				return labelDisplay(l)
			}
		}
	}
	return shortID(id)
}

func (p *picker) view(height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(p.title) + "\n\n")

	// Simple scroll window so long option lists fit the screen.
	win := height - 6
	if win < 5 {
		win = 5
	}
	start := 0
	if len(p.opts) > win {
		start = p.cursor - win/2
		if start < 0 {
			start = 0
		}
		if start > len(p.opts)-win {
			start = len(p.opts) - win
		}
	}
	end := start + win
	if end > len(p.opts) {
		end = len(p.opts)
	}
	if start > 0 {
		b.WriteString(faintText.Render("  ↑ more") + "\n")
	}
	for i := start; i < end; i++ {
		o := p.opts[i]
		marker := "  "
		if i == p.cursor {
			marker = "› "
		}
		check := ""
		if p.multi {
			check = "[ ] "
			if p.sel[o.id] {
				check = "[x] "
			}
		} else if p.sel[o.id] {
			check = "• "
		}
		line := marker + check + o.label
		if i == p.cursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < len(p.opts) {
		b.WriteString(faintText.Render("  ↓ more") + "\n")
	}

	help := "↑/↓ move · enter select · esc cancel"
	if p.multi {
		help = "↑/↓ move · space toggle · enter confirm · esc cancel"
	}
	b.WriteString("\n" + faintText.Render(help) + "\n")
	return b.String()
}
