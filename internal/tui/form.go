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
	fRepo
	fCap
	fDedup
	fSetLabel
	// Linear write-back (P4): optional lifecycle → Linear state/label/comment.
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
	poll     config.Project // working copy of the project being polling-configured
	capBuf   string         // text buffer for concurrency_cap

	teams   []linear.Team // available before a team is picked
	meta    *teamMeta
	loading string
	loadErr string

	cursor int
	picker *picker
	errs   []string // validation errors shown at the bottom
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
		f.poll = config.Project{}
		seedPollDefaults(&f.poll)
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
		fs = append(fs, fRepo, fCap, fDedup)
		if f.poll.DedupMode == "label" {
			fs = append(fs, fSetLabel)
		}
		// Write-back lifecycle → Linear (all optional). Grouped spawn / PR /
		// merged / blocked, each state paired with its comment toggle.
		fs = append(fs, fOnSpawnState, fCommentOnSpawn, fOnPRState)
		if f.poll.OnPRStateID != "" || f.poll.CommentOnPR {
			// The "wait for green checks" gate only means anything once the PR
			// transition (state move or comment) is actually configured.
			fs = append(fs, fPRRequiresChecks)
		}
		fs = append(fs, fCommentOnPR, fOnMergedState, fCommentOnMerged, fBlockedLabel, fCommentOnBlocked)
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
	case tea.KeyPressMsg:
		return f.key(v)
	}
	return nil, formNone
}

func (f *formModel) key(k tea.KeyPressMsg) (tea.Cmd, formEvent) {
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

	// Text editing on name/repo/cap; on other fields plain 'r' refreshes. The
	// name is the [[project]] key and is read-only once the project exists —
	// save() targets origName, so letting it be typed over would silently no-op.
	if cur == fName && !f.isNew {
		return nil, formNone
	}
	switch cur {
	case fName, fRepo, fCap:
		switch {
		case k.Code == tea.KeyBackspace:
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
		case k.Text != "": // printable runes, incl. space and paste (bubbletea v2)
			s := k.Text
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
	switch {
	case cur == fName || cur == fRepo || cur == fCap:
		// enter advances to the next field
		if f.cursor < len(f.fields())-1 {
			f.cursor++
		}
		return nil, formNone
	case cur == fSave:
		return f.save()
	case boolFields[cur]:
		f.toggleBool(cur)
		return nil, formNone
	default:
		return f.openPicker(cur), formNone
	}
}

// toggleBool flips a write-back boolean toggle in place.
func (f *formModel) toggleBool(cur fieldID) {
	switch cur {
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

// boolFields are the write-back toggles: enter flips them in place rather than
// opening a picker.
var boolFields = map[fieldID]bool{
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

// applyPolling copies the Linear polling + write-back fields from src onto dst,
// leaving dst's repository/worktree setup (path, agent, env, post_create) intact.
// Saving the form always turns polling on. src.Repo overrides only when set.
func applyPolling(dst *config.Project, src config.Project) {
	dst.Enabled = true
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

	// The polling config attaches to an existing [[project]]: the one being
	// edited (origName), or the one picked in the Project field for a new config.
	target := f.origName
	if f.isNew {
		target = p.Name
	}
	if target == "" {
		f.errs = append(f.errs, "name is required")
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
	if idx < 0 {
		if target != "" {
			f.errs = append(f.errs, fmt.Sprintf("project %q not found", target))
		}
	} else {
		applyPolling(&nc.Projects[idx], p)
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
	title := "Linear polling"
	if !f.isNew {
		title = "Linear polling: " + f.origName
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")

	fields := f.fields()
	if f.cursor >= len(fields) {
		f.cursor = len(fields) - 1
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
		line := marker + fmt.Sprintf("%-22s", f.label(fd)) + f.display(fd)
		if i == f.cursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < len(fields) {
		b.WriteString(faintText.Render("  ↓ more") + "\n")
	} else {
		b.WriteString("  " + faintText.Render(fmt.Sprintf("%-22spriority, createdAt (default)", "Priority sort")) + "\n")
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
	b.WriteString("\n" + faintText.Render("↑/↓ move · enter select/edit · r refresh linear cache · esc back") + "\n")
	return b.String()
}

// fieldHelp returns a one-line description of the focused field, rendered
// faintly at the bottom of the form. Empty means no help line.
func fieldHelp(fd fieldID) string {
	switch fd {
	case fName:
		return "The [[project]] this polling config belongs to (its config key)."
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
