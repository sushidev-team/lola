// Cascading create/edit form. Each Linear-backed level only becomes
// available after the prior selection (team gates everything team-scoped);
// selects open a hand-rolled picker overlay (no bubbles dependency).
package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
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
	// Repo tab: the [[project]]'s repository / worktree setup.
	fName fieldID = iota
	fPath
	fRepo
	fDefaultBranch
	fBranchPrefix
	fAgent
	fSymlinks
	fPostCreate
	fEnv
	// Filter tab: which Linear issues this project picks up.
	fEnabled
	fTeam
	fProject
	fCycleMode
	fCycle
	fStates
	fAssignee
	fAssigneeUser
	fCap
	// Labels tab: trigger labels and how a picked-up issue is marked.
	fLabels
	fMatchMode
	fDedup
	fSetLabel
	// Write-back tab (P4): optional lifecycle → Linear state/label/comment.
	fOnSpawnState
	fCommentOnSpawn
	fOnPRState
	fPRRequiresChecks
	fCommentOnPR
	fOnMergedState
	fCommentOnMerged
	fBlockedLabel
	fCommentOnBlocked
	fSave
)

// formTab groups the fields above. A project is one config entry covering
// repository setup, its Linear filter and its write-back, which is more than
// fits one readable column — the tabs are that entry's sections, not separate
// forms.
type formTab int

const (
	tabRepo formTab = iota
	tabFilter
	tabLabels
	tabWriteback
)

var formTabs = []struct {
	tab   formTab
	title string
}{
	{tabRepo, "Repo"},
	{tabFilter, "Filter"},
	{tabLabels, "Labels"},
	{tabWriteback, "Write-back"},
}

// listFields are edited as one entry per line (an open sub-editor), not inline.
var listFields = map[fieldID]bool{fSymlinks: true, fPostCreate: true, fEnv: true}

// textFields are edited inline, character by character.
var textFields = map[fieldID]bool{
	fName: true, fPath: true, fRepo: true, fDefaultBranch: true,
	fBranchPrefix: true, fCap: true,
}

// inheritable maps a field to its config.ProjectInherits bit, for the fields
// that have a [defaults] counterpart. Only these render the inherited ghost and
// respond to ctrl+o.
var inheritable = map[fieldID]func(*config.ProjectInherits) *bool{
	fSymlinks:   func(i *config.ProjectInherits) *bool { return &i.Symlinks },
	fPostCreate: func(i *config.ProjectInherits) *bool { return &i.PostCreate },
	fEnv:        func(i *config.ProjectInherits) *bool { return &i.Env },
	fLabels:     func(i *config.ProjectInherits) *bool { return &i.MatchLabels },
	fMatchMode:  func(i *config.ProjectInherits) *bool { return &i.MatchMode },
	fDedup:      func(i *config.ProjectInherits) *bool { return &i.DedupMode },
	fSetLabel:   func(i *config.ProjectInherits) *bool { return &i.OnSentSetLabel },
	fBlockedLabel: func(i *config.ProjectInherits) *bool {
		return &i.BlockedLabelID
	},
}

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
	poll     config.Project // working copy of the project being edited
	capBuf   string         // text buffer for concurrency_cap

	// Line buffers for the list fields; folded back into poll on save.
	symlinks   []string
	postCreate []string
	env        []string // "KEY=value" per line

	teams   []linear.Team // available before a team is picked
	meta    *teamMeta
	loading string
	loadErr string

	tab     formTab
	cursor  int
	picker  *picker
	editing bool     // a list field is OPEN for line editing
	lineCur int      // which line, while editing
	errs    []string // validation errors shown at the bottom
}

// lineBuf returns the line buffer backing a list field, or nil.
func (f *formModel) lineBuf(fd fieldID) *[]string {
	switch fd {
	case fSymlinks:
		return &f.symlinks
	case fPostCreate:
		return &f.postCreate
	case fEnv:
		return &f.env
	}
	return nil
}

// inherits reports whether the focused field currently takes its value from
// [defaults]. Fields with no [defaults] counterpart always report false.
func (f *formModel) inherits(fd fieldID) bool {
	get, ok := inheritable[fd]
	if !ok {
		return false
	}
	return *get(&f.poll.Inherits)
}

// setInherit flips a field between inheriting and overriding. Reverting to
// inherit refills the field from [defaults] so the ghost shows what will apply.
func (f *formModel) setInherit(fd fieldID, v bool) {
	get, ok := inheritable[fd]
	if !ok {
		return
	}
	*get(&f.poll.Inherits) = v
	if !v {
		return
	}
	d := f.cfg.Defaults
	switch fd {
	case fSymlinks:
		f.symlinks = slices.Clone(d.Symlinks)
	case fPostCreate:
		f.postCreate = slices.Clone(d.PostCreate)
	case fEnv:
		f.env = envLines(d.Env)
	case fLabels:
		f.poll.MatchLabels = slices.Clone(d.MatchLabels)
	case fMatchMode:
		f.poll.MatchMode = orDefault(d.MatchMode, config.DefaultMatchMode)
	case fDedup:
		f.poll.DedupMode = orDefault(d.DedupMode, config.DefaultDedupMode)
	case fSetLabel:
		f.poll.OnSentSetLabel = d.OnSentSetLabel
	case fBlockedLabel:
		f.poll.BlockedLabelID = d.BlockedLabelID
	}
}

// override promotes the focused field to a project-level value. Called whenever
// an edit lands on an inheritable field, so editing IS overriding.
func (f *formModel) override(fd fieldID) { f.setInherit(fd, false) }

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// plural returns the "y"/"ies" tail for an "entr%s" count.
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func newFormModel(cfg *config.Config, existing *config.Project) (*formModel, tea.Cmd) {
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
		// A project that has never polled carries empty enums, which Validate
		// rejects. Seed the same defaults a fresh config gets so opening the
		// form on a non-polling project yields a saveable state.
		if !existing.Polls() {
			seedPollDefaults(&f.poll)
			if f.capBuf == "" {
				f.capBuf = "1"
			}
		}
	} else {
		// A brand-new project inherits everything it can, so it picks up
		// whatever shared setup [defaults] already carries.
		f.poll = config.Project{
			DefaultBranch: config.DefaultBranchName,
			Inherits: config.ProjectInherits{
				Symlinks: true, PostCreate: true, Env: true,
				MatchLabels: true, MatchMode: true, OnSentSetLabel: true,
				BlockedLabelID: true, DedupMode: true, PrioritySort: true,
			},
		}
		seedPollDefaults(&f.poll)
		f.capBuf = "1"
	}
	// Inherited fields show the [defaults] value; overridden ones the project's.
	f.symlinks = slices.Clone(f.poll.Symlinks)
	f.postCreate = slices.Clone(f.poll.PostCreate)
	f.env = envLines(f.poll.Env)
	if f.isNew {
		f.symlinks = slices.Clone(cfg.Defaults.Symlinks)
		f.postCreate = slices.Clone(cfg.Defaults.PostCreate)
		f.env = envLines(cfg.Defaults.Env)
		f.poll.MatchLabels = slices.Clone(cfg.Defaults.MatchLabels)
		f.poll.MatchMode = orDefault(cfg.Defaults.MatchMode, config.DefaultMatchMode)
		f.poll.DedupMode = orDefault(cfg.Defaults.DedupMode, config.DefaultDedupMode)
		f.poll.OnSentSetLabel = cfg.Defaults.OnSentSetLabel
		f.poll.BlockedLabelID = cfg.Defaults.BlockedLabelID
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

// seedPollDefaults fills the polling enums that Validate requires a value for,
// leaving any already-set field alone. Applied to a project with no polling
// config yet so the form opens in a saveable state.
func seedPollDefaults(p *config.Project) {
	if p.CycleMode == "" {
		p.CycleMode = "none"
	}
	if p.MatchMode == "" {
		p.MatchMode = "any"
	}
	if p.AssigneeMode == "" {
		p.AssigneeMode = "anyone"
	}
	if p.DedupMode == "" {
		p.DedupMode = "seen"
	}
}

// fields returns the visible fields of the ACTIVE tab; conditional levels
// appear only once their gating selection exists. fSave is appended to every
// tab so the form can be committed without navigating back to a particular one.
func (f *formModel) fields() []fieldID {
	var fs []fieldID
	switch f.tab {
	case tabRepo:
		fs = []fieldID{fName, fPath, fRepo, fDefaultBranch, fBranchPrefix, fAgent, fSymlinks, fPostCreate, fEnv}
	case tabFilter:
		fs = []fieldID{fEnabled, fTeam}
		if f.poll.TeamID != "" {
			fs = append(fs, fProject, fCycleMode)
			if f.poll.CycleMode == "pinned" {
				fs = append(fs, fCycle)
			}
			fs = append(fs, fStates, fAssignee)
			if f.poll.AssigneeMode == "user" {
				fs = append(fs, fAssigneeUser)
			}
			fs = append(fs, fCap)
		}
	case tabLabels:
		// Team-scoped: label UUIDs only exist within the picked team.
		if f.poll.TeamID != "" {
			fs = append(fs, fLabels, fMatchMode, fDedup)
			if f.poll.DedupMode == "label" {
				fs = append(fs, fSetLabel)
			}
		}
	case tabWriteback:
		if f.poll.TeamID != "" {
			// Grouped spawn / PR / merged / blocked, each state paired with its
			// comment toggle.
			fs = append(fs, fOnSpawnState, fCommentOnSpawn, fOnPRState)
			if f.poll.OnPRStateID != "" || f.poll.CommentOnPR {
				// The "wait for green checks" gate only means anything once the
				// PR transition (state move or comment) is actually configured.
				fs = append(fs, fPRRequiresChecks)
			}
			fs = append(fs, fCommentOnPR, fOnMergedState, fCommentOnMerged, fBlockedLabel, fCommentOnBlocked)
		}
	}
	return append(fs, fSave)
}

// needsTeam reports whether the active tab is empty for want of a team, so the
// view can say so instead of showing a bare Save.
func (f *formModel) needsTeam() bool {
	return f.poll.TeamID == "" && (f.tab == tabLabels || f.tab == tabWriteback)
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
	case tea.KeyPressMsg:
		return f.key(v)
	}
	return nil, formNone
}

func (f *formModel) key(k tea.KeyPressMsg) (tea.Cmd, formEvent) {
	if f.picker != nil {
		return f.pickerKey(k), formNone
	}
	if f.editing {
		return nil, f.editList(k)
	}
	fields := f.fields()
	if f.cursor >= len(fields) {
		f.cursor = len(fields) - 1
	}
	cur := fields[f.cursor]

	switch k.String() {
	case "esc":
		return nil, formCancel
	case "tab":
		f.switchTab(1)
		return nil, formNone
	case "shift+tab":
		f.switchTab(-1)
		return nil, formNone
	case "up":
		if f.cursor > 0 {
			f.cursor--
		}
		return nil, formNone
	case "down":
		if f.cursor < len(fields)-1 {
			f.cursor++
		}
		return nil, formNone
	case "ctrl+r":
		return f.refresh(), formNone
	case "ctrl+o":
		// Toggle inherit ↔ override on the focused field.
		if _, ok := inheritable[cur]; ok {
			f.setInherit(cur, !f.inherits(cur))
		}
		return nil, formNone
	case "enter":
		return f.interact(cur)
	}

	// The name is the [[project]] key and is read-only once the project exists
	// — save() targets origName, so typing over it would silently no-op.
	if cur == fName && !f.isNew {
		return nil, formNone
	}
	if !textFields[cur] {
		if k.String() == "r" {
			return f.refresh(), formNone
		}
		return nil, formNone
	}

	// Inline text editing. fCap keeps its digits-only filter.
	buf := f.textBuf(cur)
	if buf == nil {
		return nil, formNone
	}
	switch {
	case k.Code == tea.KeyBackspace:
		if *buf != "" {
			rs := []rune(*buf)
			*buf = string(rs[:len(rs)-1])
		}
	case k.Text != "": // printable runes, incl. space and paste (bubbletea v2)
		if cur == fCap {
			for _, r := range k.Text {
				if r >= '0' && r <= '9' {
					*buf += string(r)
				}
			}
		} else {
			*buf += k.Text
		}
	}
	return nil, formNone
}

// paste inserts clipboard text into the focused field. An open list sub-editor
// takes a MULTI-line paste as multiple entries (pasting several symlinks at once
// is the point); a single-line field takes the first non-blank line.
func (f *formModel) paste(s string) {
	if f.picker != nil || s == "" {
		return
	}
	fields := f.fields()
	if f.cursor < 0 || f.cursor >= len(fields) {
		return
	}
	cur := fields[f.cursor]

	if f.editing {
		buf := f.lineBuf(cur)
		if buf == nil {
			return
		}
		lines := pasteLines(s)
		if len(lines) == 0 {
			return
		}
		// The first pasted line continues the current entry; the rest become
		// new entries after it, and the cursor follows to the last one.
		(*buf)[f.lineCur] += lines[0]
		if rest := lines[1:]; len(rest) > 0 {
			tail := append(rest, (*buf)[f.lineCur+1:]...)
			*buf = append((*buf)[:f.lineCur+1], tail...)
			f.lineCur += len(rest)
		}
		return
	}

	if cur == fName && !f.isNew {
		return // the config key is read-only on an existing project
	}
	buf := f.textBuf(cur)
	if buf == nil {
		return
	}
	if cur == fCap {
		*buf += pasteDigits(s)
		return
	}
	*buf += pasteInline(s)
}

// switchTab moves to the next/previous tab, resetting the cursor so it can
// never point past the new tab's shorter field list.
func (f *formModel) switchTab(delta int) {
	n := len(formTabs)
	f.tab = formTab((int(f.tab) + delta%n + n) % n)
	f.cursor, f.editing = 0, false
}

// textBuf returns the string backing an inline-editable field.
func (f *formModel) textBuf(fd fieldID) *string {
	switch fd {
	case fName:
		return &f.poll.Name
	case fPath:
		return &f.poll.Path
	case fRepo:
		return &f.poll.Repo
	case fDefaultBranch:
		return &f.poll.DefaultBranch
	case fBranchPrefix:
		return &f.poll.BranchPrefix
	case fCap:
		return &f.capBuf
	}
	return nil
}

// editList drives an OPEN list field: arrows move between lines, enter adds a
// line, backspace edits (or removes an empty line), esc closes back to field
// navigation. Mirrors the project editor's list sub-editor.
func (f *formModel) editList(k tea.KeyPressMsg) formEvent {
	cur := f.fields()[f.cursor]
	buf := f.lineBuf(cur)
	if buf == nil {
		f.editing = false
		return formNone
	}
	switch k.String() {
	case "esc":
		f.editing = false
	case "ctrl+s":
		f.editing = false
		_, ev := f.save()
		return ev
	case "up":
		if f.lineCur > 0 {
			f.lineCur--
		}
	case "down":
		if f.lineCur < len(*buf)-1 {
			f.lineCur++
		}
	case "enter":
		f.lineCur++
		*buf = append((*buf)[:f.lineCur], append([]string{""}, (*buf)[f.lineCur:]...)...)
	case "backspace":
		if (*buf)[f.lineCur] == "" && len(*buf) > 1 {
			*buf = append((*buf)[:f.lineCur], (*buf)[f.lineCur+1:]...)
			if f.lineCur > 0 {
				f.lineCur--
			}
		} else {
			(*buf)[f.lineCur] = dropLastRune((*buf)[f.lineCur])
		}
	default:
		if k.Text != "" {
			(*buf)[f.lineCur] += k.Text
		}
	}
	return formNone
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
	switch {
	case textFields[cur]:
		// enter advances to the next field
		if f.cursor < len(f.fields())-1 {
			f.cursor++
		}
		return nil, formNone
	case cur == fSave:
		return f.save()
	case listFields[cur]:
		// Opening a list field for editing IS overriding it.
		f.override(cur)
		if buf := f.lineBuf(cur); buf != nil && len(*buf) == 0 {
			*buf = []string{""}
		}
		f.editing, f.lineCur = true, 0
		return nil, formNone
	case cur == fAgent:
		f.cycleAgent()
		return nil, formNone
	case boolFields[cur]:
		f.toggleBool(cur)
		return nil, formNone
	default:
		return f.openPicker(cur), formNone
	}
}

// cycleAgent advances the per-project coding-agent override, wrapping through
// "" (inherit [defaults].agent) and each known kind.
func (f *formModel) cycleAgent() {
	opts := projAgentOptions()
	for i, o := range opts {
		if o == f.poll.Agent {
			f.poll.Agent = opts[(i+1)%len(opts)]
			return
		}
	}
	f.poll.Agent = opts[0]
}

// toggleBool flips a boolean field in place.
func (f *formModel) toggleBool(cur fieldID) {
	switch cur {
	case fEnabled:
		f.poll.Enabled = !f.poll.Enabled
	case fPRRequiresChecks:
		f.poll.PRRequiresChecks = !f.poll.PRRequiresChecks
	case fCommentOnSpawn:
		f.poll.CommentOnSpawn = !f.poll.CommentOnSpawn
	case fCommentOnPR:
		f.poll.CommentOnPR = !f.poll.CommentOnPR
	case fCommentOnMerged:
		f.poll.CommentOnMerged = !f.poll.CommentOnMerged
	case fCommentOnBlocked:
		f.poll.CommentOnBlocked = !f.poll.CommentOnBlocked
	}
}

var metaFields = map[fieldID]bool{
	fProject: true, fCycle: true, fStates: true, fLabels: true,
	fAssigneeUser: true, fSetLabel: true,
	fOnSpawnState: true, fOnPRState: true, fOnMergedState: true, fBlockedLabel: true,
}

// boolFields are the toggles: enter flips them in place rather than opening a
// picker.
var boolFields = map[fieldID]bool{
	fEnabled:          true,
	fPRRequiresChecks: true, fCommentOnSpawn: true, fCommentOnPR: true,
	fCommentOnMerged: true, fCommentOnBlocked: true,
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
	case fDedup:
		title = "Dedup mode"
		opts = []pickOpt{{"label", "label (flip trigger label on spawn)"}, {"seen", "seen (local seen-file)"}}
		selected = []string{f.poll.DedupMode}
	case fSetLabel:
		title = "on_sent: set label"
		selected = []string{f.poll.OnSentSetLabel}
		for _, l := range f.meta.Labels {
			opts = append(opts, pickOpt{l.ID, labelDisplay(l)})
		}
	case fOnSpawnState:
		title = "On agent start → state (e.g. In Progress)"
		opts, selected = f.stateOpts(), []string{f.poll.OnSpawnStateID}
	case fOnPRState:
		title = "On PR → state (e.g. In Review)"
		opts, selected = f.stateOpts(), []string{f.poll.OnPRStateID}
	case fOnMergedState:
		title = "On merged → state (e.g. Done)"
		opts, selected = f.stateOpts(), []string{f.poll.OnMergedStateID}
	case fBlockedLabel:
		title = "On blocked → label (optional)"
		opts = append(opts, pickOpt{"", "(none)"})
		for _, l := range f.meta.Labels {
			opts = append(opts, pickOpt{l.ID, labelDisplay(l)})
		}
		selected = []string{f.poll.BlockedLabelID}
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

func (f *formModel) pickerKey(k tea.KeyPressMsg) tea.Cmd {
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
	// Picking a value for an inheritable field promotes it to an override.
	f.override(p.field)
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
			f.poll.OnSentSetLabel = ""
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
	case fDedup:
		f.poll.DedupMode = id
	case fSetLabel:
		f.poll.OnSentSetLabel = id
	case fOnSpawnState:
		f.poll.OnSpawnStateID = id
	case fOnPRState:
		f.poll.OnPRStateID = id
	case fOnMergedState:
		f.poll.OnMergedStateID = id
	case fBlockedLabel:
		f.poll.BlockedLabelID = id
	}
	return nil
}

// stateOpts builds a single-select workflow-state option list led by "(none)"
// (clears the transition), shared by the three write-back state pickers.
func (f *formModel) stateOpts() []pickOpt {
	opts := []pickOpt{{"", "(none)"}}
	for _, s := range f.meta.States {
		opts = append(opts, pickOpt{s.ID, s.Name + " [" + s.Type + "]"})
	}
	return opts
}

// applyProject copies every edited field from src onto dst — the form now owns
// the whole [[project]] (repository setup, Linear filter, write-back), so there
// is nothing on dst to preserve except its identity.
func applyProject(dst *config.Project, src config.Project) {
	dst.Path = src.Path
	dst.DefaultBranch = src.DefaultBranch
	dst.BranchPrefix = src.BranchPrefix
	dst.Agent = src.Agent
	dst.Symlinks = src.Symlinks
	dst.PostCreate = src.PostCreate
	dst.Env = src.Env
	dst.Inherits = src.Inherits

	dst.Enabled = src.Enabled
	dst.TeamID = src.TeamID
	dst.ProjectID = src.ProjectID
	dst.CycleMode = src.CycleMode
	dst.CycleID = src.CycleID
	dst.StateIDs = src.StateIDs
	dst.MatchLabels = src.MatchLabels
	dst.MatchMode = src.MatchMode
	dst.AssigneeMode = src.AssigneeMode
	dst.AssigneeUserID = src.AssigneeUserID
	dst.ConcurrencyCap = src.ConcurrencyCap
	dst.PrioritySort = src.PrioritySort
	dst.DedupMode = src.DedupMode
	dst.OnSentSetLabel = src.OnSentSetLabel
	dst.OnSpawnStateID = src.OnSpawnStateID
	dst.OnPRStateID = src.OnPRStateID
	dst.OnMergedStateID = src.OnMergedStateID
	dst.BlockedLabelID = src.BlockedLabelID
	dst.CommentOnSpawn = src.CommentOnSpawn
	dst.CommentOnPR = src.CommentOnPR
	dst.CommentOnMerged = src.CommentOnMerged
	dst.CommentOnBlocked = src.CommentOnBlocked
	dst.PRRequiresChecks = src.PRRequiresChecks
	if src.Repo != "" {
		dst.Repo = src.Repo
	}
}

func (f *formModel) save() (tea.Cmd, formEvent) {
	f.errs = nil
	p := f.poll
	p.Name = strings.TrimSpace(p.Name)
	p.Path = strings.TrimSpace(p.Path)
	p.Repo = strings.TrimSpace(p.Repo) // format checked by nc.Validate below
	p.DefaultBranch = strings.TrimSpace(p.DefaultBranch)
	p.BranchPrefix = strings.TrimSpace(p.BranchPrefix)
	p.Symlinks = trimDropEmpty(f.symlinks)
	p.PostCreate = trimDropEmpty(f.postCreate)
	p.Env = parseEnvLines(f.env)
	p.ConcurrencyCap = 0
	if f.capBuf != "" {
		n, err := strconv.Atoi(f.capBuf)
		if err != nil || n <= 0 {
			f.errs = append(f.errs, "concurrency_cap must be a positive integer")
		} else {
			p.ConcurrencyCap = n
		}
	}

	// The form edits one [[project]]: the one it opened on (origName), or a new
	// entry keyed by the typed name.
	target := f.origName
	if f.isNew {
		target = p.Name
	}
	if target == "" {
		f.errs = append(f.errs, "name is required")
	}
	if p.Path == "" {
		f.errs = append(f.errs, "path is required — the local repository this project's worktrees fork from")
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
	nc.Projects = slices.Clone(base.Projects)
	idx := -1
	for i := range nc.Projects {
		if nc.Projects[i].Name == target {
			idx = i
			break
		}
	}
	switch {
	case idx >= 0:
		applyProject(&nc.Projects[idx], p)
	case f.isNew && target != "":
		// The form CREATES the project when it opened on nothing — it carries
		// every field a [[project]] needs, so there is no prior entry to attach
		// to. Editing an existing one can still miss (renamed on disk behind us).
		np := config.Project{Name: target}
		applyProject(&np, p)
		nc.Projects = append(nc.Projects, np)
	case target != "":
		f.errs = append(f.errs, fmt.Sprintf("project %q not found", target))
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

// openProjectForm opens the project form for the current selection: the focused
// rail project, or the selected session's project.
func (m *rootModel) openProjectForm() (tea.Model, tea.Cmd) {
	name := ""
	if m.focus == focusPolls {
		if p := m.selectedRailProject(); p != nil {
			name = p.Name
		}
	} else if sel := m.sessions.selected(); sel != nil {
		name = sel.Project
	}
	if name == "" {
		m.sessions.flash, m.sessions.flashGood = "no project to edit here", false
		return m, nil
	}
	pr := m.cfg.ProjectByName(name)
	if pr == nil {
		m.sessions.flash, m.sessions.flashGood = "project "+name+" not found in config", false
		return m, nil
	}
	f, cmd := newFormModel(m.cfg, pr)
	m.form = f
	return m, cmd
}

// ---- view ----

// tabStrip renders the tab bar, marking the active tab and flagging any tab
// carrying a validation error so a problem on a hidden tab is still visible.
func (f *formModel) tabStrip() string {
	var parts []string
	for _, t := range formTabs {
		label := " " + t.title + " "
		if t.tab == f.tab {
			parts = append(parts, selStyle.Render(label))
		} else {
			parts = append(parts, faintText.Render(label))
		}
	}
	return "  " + strings.Join(parts, faintText.Render("·"))
}

func (f *formModel) view(height int) string {
	if f.picker != nil {
		return f.picker.view(height)
	}
	title := "New project"
	if !f.isNew {
		title = "Project: " + f.origName
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")
	b.WriteString(f.tabStrip() + "\n\n")

	fields := f.fields()
	if f.cursor >= len(fields) {
		f.cursor = len(fields) - 1
	}
	if f.needsTeam() {
		b.WriteString("  " + faintText.Render("pick a Linear team on the Filter tab first — labels and states are team-scoped") + "\n\n")
	}

	// Scroll window: the field list (with write-back) can exceed the modal's
	// inner height, and box() silently truncates any body past it — so keep the
	// focused field within a window that fits, mirroring the picker's scroller.
	win := height - 8
	if win < 6 {
		win = 6
	}
	start, end := 0, len(fields)
	if len(fields) > win {
		start = f.cursor - win/2
		if start < 0 {
			start = 0
		}
		if start > len(fields)-win {
			start = len(fields) - win
		}
		end = start + win
	}
	if start > 0 {
		b.WriteString(faintText.Render("  ↑ more") + "\n")
	}
	for i := start; i < end; i++ {
		fd := fields[i]
		marker := "  "
		if i == f.cursor {
			marker = "› "
		}
		val := f.display(fd)
		if f.inherits(fd) {
			// Ghost: the [defaults] value dimmed and tagged, so it reads as
			// "this is what applies" rather than "this is unset".
			val = faintText.Render(stripANSI(val)) + faintText.Render("  inherited")
		}
		line := marker + fmt.Sprintf("%-22s", f.label(fd)) + val
		if i == f.cursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")

		// The focused list field expands inline while open for line editing.
		if f.editing && i == f.cursor {
			if buf := f.lineBuf(fd); buf != nil {
				for j, e := range *buf {
					bullet := faintText.Render("· ")
					caret := ""
					if j == f.lineCur {
						bullet, caret = warnText.Render("▸ "), "_"
					}
					b.WriteString("      " + bullet + e + caret + "\n")
				}
			}
		}
	}
	if end < len(fields) {
		b.WriteString(faintText.Render("  ↓ more") + "\n")
	}

	if f.loading != "" {
		b.WriteString("\n" + faintText.Render(f.loading) + "\n")
	}
	if f.loadErr != "" {
		b.WriteString("\n" + badText.Render(f.loadErr) + "\n")
	}
	if h := fieldHelp(fields[f.cursor]); h != "" {
		b.WriteString("\n" + faintText.Render(h) + "\n")
	}
	if len(f.errs) > 0 {
		b.WriteString("\n")
		for _, e := range f.errs {
			b.WriteString(badText.Render("✗ "+e) + "\n")
		}
	}
	hint := "↑/↓ move · tab/shift-tab section · enter select/edit · ctrl-o inherit/override · r refresh linear · esc back"
	if f.editing {
		hint = "editing " + f.label(fields[f.cursor]) + " — ↑/↓ line · enter new line · esc done"
	}
	b.WriteString("\n" + faintText.Render(hint) + "\n")
	return b.String()
}

// fieldHelp returns a one-line description of the focused field, rendered
// faintly at the bottom of the form. Empty means no help line.
func fieldHelp(fd fieldID) string {
	switch fd {
	case fName:
		return "Unique name for this project — its config key, and the prefix of every session it spawns."
	case fPath:
		return "Local repository path. Worktrees are forked from it; it is never checked out into itself."
	case fDefaultBranch:
		return "Base branch worktrees fork from."
	case fBranchPrefix:
		return "Prefix for a session's branch (e.g. \"lola/\" yields lola/eng-42). Empty inherits [defaults].branch_prefix, then \"lola/\"."
	case fAgent:
		return "Coding agent this project's sessions spawn; empty inherits [defaults].agent. enter cycles."
	case fSymlinks:
		return "One relative path per line, linked from main into each worktree (e.g. .env). Do NOT symlink vendor/ — it breaks PHP autoload; use post-create instead."
	case fPostCreate:
		return "One command per line, run in a fresh worktree before the agent (e.g. composer install)."
	case fEnv:
		return "One KEY=value per line, exported into the session and post-create commands."
	case fEnabled:
		return "enter toggles · off pauses pickup for this project without discarding its filter."
	case fTeam:
		return "Linear team the poll queries issues from."
	case fProject:
		return "Optional Linear project scope; (none) matches any project."
	case fCycleMode:
		return "none = ignore cycles; active = the team's current cycle; pinned = a fixed cycle."
	case fCycle:
		return "The specific cycle to match when cycle mode is pinned."
	case fStates:
		return "Workflow states an issue must be in to be picked up."
	case fLabels:
		return "Trigger labels — issues carrying these (per match mode) are picked up."
	case fMatchMode:
		return "any = issue has at least one trigger label; all = has every one."
	case fAssignee:
		return "Whose issues to pick up: anyone, you (viewer), or a specific user."
	case fAssigneeUser:
		return "The specific Linear user whose issues to pick up."
	case fRepo:
		return "GitHub owner/name for PR checks; empty falls back to the project's repo."
	case fCap:
		return "Max concurrent agent sessions this project may occupy."
	case fDedup:
		return "label = flip a Linear label after spawn (visible, reconcile-driven); seen = local seen-file only."
	case fSetLabel:
		return "Label ADDED after a successful spawn to mark the issue as picked up. Lola removes the trigger label(s) automatically. Must not be a trigger label."
	case fOnSpawnState:
		return "Workflow state the issue moves to when the agent starts (e.g. In Progress). (none) = no move."
	case fCommentOnSpawn:
		return "enter toggles · also post a short comment on the issue when the agent starts."
	case fOnPRState:
		return "Workflow state when the agent's PR is ready (e.g. In Review). (none) = no move."
	case fPRRequiresChecks:
		return "enter toggles · on = wait for a valid PR (open, not draft, all CI/CodeRabbit checks green); off = flip the moment the PR opens."
	case fCommentOnPR:
		return "enter toggles · also comment the PR link on the issue."
	case fOnMergedState:
		return "Workflow state when the PR merges (e.g. Done). (none) = no move."
	case fCommentOnMerged:
		return "enter toggles · also comment when the PR merges."
	case fBlockedLabel:
		return "Label added when the agent is blocked and needs a human (CI retries exhausted). (none) = no label."
	case fCommentOnBlocked:
		return "enter toggles · also comment the block reason when the agent is blocked."
	case fSave:
		return ""
	}
	return ""
}

func (f *formModel) label(fd fieldID) string {
	switch fd {
	case fName:
		return "Name"
	case fPath:
		return "Path"
	case fDefaultBranch:
		return "Default branch"
	case fBranchPrefix:
		return "Branch prefix"
	case fAgent:
		return "Agent"
	case fSymlinks:
		return "Symlinks"
	case fPostCreate:
		return "Post-create"
	case fEnv:
		return "Env (KEY=value)"
	case fEnabled:
		return "Polling enabled"
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
	case fRepo:
		return "GitHub repo"
	case fCap:
		return "Concurrency cap"
	case fDedup:
		return "Dedup mode"
	case fSetLabel:
		return "on_sent set label"
	case fOnSpawnState:
		return "On start → state"
	case fCommentOnSpawn:
		return "  comment on start"
	case fOnPRState:
		return "On PR → state"
	case fPRRequiresChecks:
		return "  require checks"
	case fCommentOnPR:
		return "  comment on PR"
	case fOnMergedState:
		return "On merged → state"
	case fCommentOnMerged:
		return "  comment on merged"
	case fBlockedLabel:
		return "On blocked → label"
	case fCommentOnBlocked:
		return "  comment on blocked"
	case fSave:
		return ""
	}
	return ""
}

// boolDisplay renders a write-back toggle: "on" bright, "off" faint.
func boolDisplay(b bool) string {
	if b {
		return "on"
	}
	return faintText.Render("off")
}

// stateNameOrNone renders a write-back state cell: the state name, or a faint
// "(none)" when the transition is unset.
func (f *formModel) stateNameOrNone(id string) string {
	if id == "" {
		return faintText.Render("(none)")
	}
	return f.stateName(id)
}

func (f *formModel) display(fd fieldID) string {
	sel := "(select)"
	switch fd {
	case fName:
		if f.poll.Name == "" {
			return faintText.Render("(type a name)")
		}
		return f.poll.Name
	case fPath:
		if f.poll.Path == "" {
			return faintText.Render("(required — /path/to/repo)")
		}
		return f.poll.Path
	case fDefaultBranch:
		if f.poll.DefaultBranch == "" {
			return faintText.Render("(" + config.DefaultBranchName + ")")
		}
		return f.poll.DefaultBranch
	case fBranchPrefix:
		if f.poll.BranchPrefix == "" {
			return faintText.Render("(inherits " + f.cfg.BranchPrefixForProject(f.origName) + ")")
		}
		return f.poll.BranchPrefix
	case fAgent:
		if f.poll.Agent == "" {
			return faintText.Render("(inherits " + f.cfg.AgentForProject(f.origName) + ")")
		}
		return f.poll.Agent
	case fSymlinks, fPostCreate, fEnv:
		if buf := f.lineBuf(fd); buf != nil {
			n := len(trimDropEmpty(*buf))
			if n == 0 {
				return faintText.Render("(none — enter to add)")
			}
			return fmt.Sprintf("%d entr%s — enter to edit", n, plural(n))
		}
		return ""
	case fEnabled:
		return boolDisplay(f.poll.Enabled)
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
	case fOnSpawnState:
		return f.stateNameOrNone(f.poll.OnSpawnStateID)
	case fOnPRState:
		return f.stateNameOrNone(f.poll.OnPRStateID)
	case fOnMergedState:
		return f.stateNameOrNone(f.poll.OnMergedStateID)
	case fBlockedLabel:
		if f.poll.BlockedLabelID == "" {
			return faintText.Render("(none)")
		}
		return f.labelName(f.poll.BlockedLabelID)
	case fPRRequiresChecks:
		return boolDisplay(f.poll.PRRequiresChecks)
	case fCommentOnSpawn:
		return boolDisplay(f.poll.CommentOnSpawn)
	case fCommentOnPR:
		return boolDisplay(f.poll.CommentOnPR)
	case fCommentOnMerged:
		return boolDisplay(f.poll.CommentOnMerged)
	case fCommentOnBlocked:
		return boolDisplay(f.poll.CommentOnBlocked)
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
