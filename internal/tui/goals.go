package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

type goalsModel struct {
	planner               *goalPlannerModel
	project               string
	gen                   int
	rows                  []goal.Overview
	selected              string
	detail                bool
	task                  int
	scroll                int
	loading, loaded, busy bool
	err                   string
	confirm               *goal.ControlRequest
	reason                string
}
type goalsMsg struct {
	project string
	gen     int
	rows    []goal.Overview
	err     string
}
type goalControlMsg struct {
	project string
	gen     int
	err     string
}

func (m *rootModel) enterGoals(project string) (tea.Model, tea.Cmd) {
	m.goals = goalsModel{project: project, gen: m.goals.gen + 1, loading: true}
	m.view = viewGoals
	return m, fetchGoalsCmd(project, m.goals.gen)
}
func fetchGoalsCmd(project string, gen int) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(map[string]string{"project": project})
		resp, err := requestFn(protocol.Request{Cmd: "goalOverview", Args: args})
		v := goalsMsg{project: project, gen: gen}
		if err != nil {
			v.err = err.Error()
			return v
		}
		if !resp.OK {
			v.err = resp.Error
			return v
		}
		if err = json.Unmarshal(resp.Data, &v.rows); err != nil {
			v.err = err.Error()
		}
		return v
	}
}
func (m *rootModel) refreshGoals() tea.Cmd {
	v := &m.goals
	if v.loading || v.busy || v.confirm != nil || v.planner != nil {
		return nil
	}
	v.gen++
	v.loading = true
	return fetchGoalsCmd(v.project, v.gen)
}
func (m *rootModel) handleGoalsMsg(msg goalsMsg) {
	v := &m.goals
	if msg.gen != v.gen || msg.project != v.project {
		return
	}
	v.loading = false
	v.err = msg.err
	if msg.err != "" {
		return
	}
	v.rows = msg.rows
	v.loaded = true
	if v.current() == nil {
		v.selected = ""
		v.detail = false
		v.scroll = 0
		v.task = 0
		if len(v.rows) > 0 {
			v.selected = v.rows[0].ID
		}
	}
	if g := v.current(); g != nil {
		v.task = min(v.task, max(0, len(g.Tasks)-1))
	}
}
func (v *goalsModel) current() *goal.Overview {
	for i := range v.rows {
		if v.rows[i].ID == v.selected {
			return &v.rows[i]
		}
	}
	return nil
}
func (m *rootModel) handleGoalControlMsg(msg goalControlMsg) tea.Cmd {
	v := &m.goals
	if msg.gen != v.gen || msg.project != v.project {
		return nil
	}
	v.busy = false
	if msg.err != "" {
		v.err = "Action was not confirmed: " + msg.err + ". Close the action and refresh before retrying."
		return nil
	}
	v.confirm = nil
	v.reason = ""
	v.err = ""
	return m.refreshGoals()
}
func controlGoalCmd(project string, gen int, req goal.ControlRequest) tea.Cmd {
	return func() tea.Msg {
		args, _ := json.Marshal(req)
		command := "goalControl"
		if req.Action == "publish" {
			command = "goalPublishReviewed"
			args, _ = json.Marshal(struct {
				ID       string `json:"id"`
				Revision uint64 `json:"revision"`
			}{req.ID, req.Revision})
		}
		resp, err := requestFn(protocol.Request{Cmd: command, Args: args})
		if err == nil && !resp.OK {
			err = errors.New(resp.Error)
		}
		msg := goalControlMsg{project: project, gen: gen}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}
func (m *rootModel) updateGoals(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	v := &m.goals
	if v.planner != nil {
		return m.updateGoalPlanner(k)
	}
	if v.busy {
		return m, nil
	}
	if v.confirm != nil {
		switch k.String() {
		case "esc":
			v.confirm = nil
			v.reason = ""
			v.err = ""
			return m, m.refreshGoals()
		case "enter":
			if v.err != "" {
				return m, nil
			}
			req := *v.confirm
			req.Reason = v.reason
			v.busy = true
			return m, controlGoalCmd(v.project, v.gen, req)
		case "backspace":
			if v.confirm.Action != "publish" && v.reason != "" {
				_, n := utf8.DecodeLastRuneInString(v.reason)
				v.reason = v.reason[:len(v.reason)-n]
			}
		default:
			if v.confirm.Action != "publish" && len(v.reason)+len(k.Text) <= 2000 {
				v.reason += k.Text
			}
		}
		return m, nil
	}
	switch k.String() {
	case "n":
		return m, m.openGoalPlanner(false)
	case "b":
		return m, m.openGoalPlanner(true)
	case "esc", "left":
		if v.detail {
			v.detail = false
			v.scroll = 0
			return m, nil
		}
		return m.enterDetail(v.project)
	case "q":
		m.closeAllTerms()
		return m, tea.Quit
	case "f":
		return m, m.refreshGoals()
	case "enter":
		if v.current() != nil {
			v.detail = true
			v.scroll = 0
		}
		return m, nil
	case "up", "k", "down", "j":
		delta := 1
		if k.String() == "up" || k.String() == "k" {
			delta = -1
		}
		if v.detail {
			v.scroll = max(0, v.scroll+delta)
			return m, nil
		}
		i := slices.IndexFunc(v.rows, func(g goal.Overview) bool { return g.ID == v.selected })
		if len(v.rows) > 0 {
			i = max(0, min(len(v.rows)-1, i+delta))
			v.selected = v.rows[i].ID
			v.task = 0
		}
		return m, nil
	case "tab", "shift+tab":
		if g := v.current(); g != nil && len(g.Tasks) > 0 {
			delta := 1
			if k.String() == "shift+tab" {
				delta = -1
			}
			v.task = (v.task + len(g.Tasks) + delta) % len(g.Tasks)
			v.scroll = 0
		}
		return m, nil
	}
	g := v.current()
	if g == nil || !v.detail {
		return m, nil
	}
	if k.String() == "o" && g.Parent.Confirmed {
		return m, openURLCmd("https://linear.app/issue/" + g.Parent.ID)
	}
	if k.String() == "s" && v.task < len(g.Tasks) {
		a := g.Tasks[v.task].Attempts
		if len(a) > 0 && a[len(a)-1].SessionID != "" {
			m.sessions.filter.Project = v.project
			m.sessions.selID = a[len(a)-1].SessionID
			m.view = viewCockpit
			m.focus = focusSessions
			return m, fetchSessionsCmd
		}
	}
	action := map[string]string{"p": "pause", "r": "resume", "x": "stop-workers", "c": "cancel", "u": "publish"}[k.String()]
	if action != "" && !v.loading && v.err == "" && (slices.Contains(g.Actions, action) || (action == "publish" && goalCanPublish(g))) {
		v.confirm = &goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: action}
		v.reason = ""
	}
	return m, nil
}

// Goal content can originate in an agent or Linear. Keep terminal controls out
// of text, then let the existing clipping/layout helpers add only Lola's styles.
func goalText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, stripANSI(s))
}
func (m *rootModel) goalsLines() []string {
	if m.goals.planner != nil {
		return m.goalPlannerLines()
	}
	v := &m.goals
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 24
	}
	out := []string{m.vitalsBar(w), previewLine("lola ▸ "+m.projLabel(v.project)+" ▸ Goals", w)}
	var body []string
	if !v.loaded && v.loading {
		body = append(body, "Loading goals…")
	}
	if v.loaded && len(v.rows) == 0 {
		body = append(body, "No goals in this project yet.", "Press n to plan a goal, or b to inspect saved proposals.")
	}
	g := v.current()
	if v.confirm != nil {
		body = append(body, "Review action: "+v.confirm.Action, fmt.Sprintf("Goal %s · reviewed revision %d", v.confirm.ID, v.confirm.Revision))
		descriptions := map[string]string{
			"publish":      "Create or reconcile the goal parent issue, task issues, dependencies and optional milestone in the configured Linear project. This does not authorize execution.",
			"pause":        "Hold dispatch, verification and integration. Running workers continue.",
			"resume":       "Recheck Linear and policy, then resume within accepted budgets.",
			"stop-workers": "Stop current workers; keep files and evidence. Resume to dispatch again.",
			"cancel":       "End this goal and stop workers. Keep all work. Cannot be resumed.",
		}
		body = append(body, descriptions[v.confirm.Action])
		if v.confirm.Action == "publish" && g != nil {
			body = append(body, fmt.Sprintf("%d tasks. Existing confirmed resources are retained on retry.", len(g.Tasks)))
			if g.MilestoneName != "" {
				body = append(body, "New milestone: "+goalText(g.MilestoneName))
			}
		} else {
			body = append(body, "", "Reason (optional): "+goalText(v.reason))
		}
	} else if !v.detail {
		body = append(body, "Work stays on goal branches. Final PRs require your merge.", "")
		for _, r := range v.rows {
			mark := "  "
			if r.ID == v.selected {
				mark = "> "
			}
			body = append(body, mark+goalText(r.Title)+" · "+r.State)
		}
	} else if g != nil {
		body = append(body, goalText(g.Title)+" · "+g.State, goalText(g.Outcome))
		if g.SyncError != "" {
			body = append(body, "Linear needs reconciliation: "+goalText(g.SyncError))
		}
		if g.Reason != "" {
			body = append(body, goalText(g.Reason))
		}
		if g.Stopping {
			body = append(body, "Workers stopping or stopped; files retained")
		}
		body = append(body, fmt.Sprintf("Revision %d · Linear %s", g.Revision, g.Parent.Identifier))
		if g.Authorized {
			body = append(body, fmt.Sprintf("Observed usage: %d / %d tokens · %d / %d calls", g.Tokens, g.TokenLimit, g.Coverage.Runs, g.RunLimit),
				fmt.Sprintf("$%.2f reported estimate · %d partial, %d missing, %d without dollar cost", g.SpendUSD, g.Coverage.Partial, g.Coverage.Missing, g.Coverage.CostUnavailable),
				"Missing usage is not zero. Deadline: "+g.Deadline.Local().Format("Jan 2 15:04 MST"))
		} else {
			body = append(body, "Execution has not been authorized.")
		}
		body = append(body, "", "Acceptance")
		for _, c := range g.Acceptance {
			body = append(body, "  - "+goalText(c))
		}
		if len(g.NonGoals) > 0 {
			body = append(body, "Outside this goal")
			for _, c := range g.NonGoals {
				body = append(body, "  - "+goalText(c))
			}
		}
		body = append(body, "", "Tasks (tab selects a task)")
		for i, t := range g.Tasks {
			mark := "  "
			if i == v.task {
				mark = "> "
			}
			body = append(body, fmt.Sprintf("%s%d. %s · %s", mark, i+1, goalText(t.Title), t.State))
		}
		if v.task < len(g.Tasks) {
			t := g.Tasks[v.task]
			body = append(body, "", "Selected task: "+goalText(t.Title))
			if len(t.BlockedBy) > 0 {
				body = append(body, fmt.Sprintf("Depends on tasks %v", t.BlockedBy))
			}
			for _, c := range t.Acceptance {
				body = append(body, "  - "+goalText(c))
			}
			for i, a := range t.Attempts {
				body = append(body, fmt.Sprintf("  Attempt %d · %s · %s", i+1, a.State, goalText(a.SessionID)))
				if a.Error != "" {
					body = append(body, "  "+goalText(a.Error))
				}
				if a.Summary != "" {
					body = append(body, "  "+goalText(a.Summary))
				}
			}
		}
		if len(g.Members) > 0 {
			label := "Integrated revisions"
			if g.Verified {
				label = "Verified deliverable"
			}
			body = append(body, "", label)
			for _, r := range g.Members {
				body = append(body, fmt.Sprintf("  %s → %s · %.12s", goalText(r.Repo), goalText(r.Target), r.Commit))
				if r.PRURL != "" {
					body = append(body, "  "+goalText(r.PRURL)+" · "+r.DeliveryState)
				}
			}
		}
	}
	// Wrap detail prose before scrolling, so long criteria remain readable at
	// terminal widths without relying on the terminal's own line wrapping.
	if v.detail || v.confirm != nil {
		var wrapped []string
		style := lipgloss.NewStyle().Width(max(1, w))
		for _, line := range body {
			wrapped = append(wrapped, strings.Split(style.Render(line), "\n")...)
		}
		body = wrapped
	}
	height := max(1, h-5)
	start := min(v.scroll, max(0, len(body)-height))
	if !v.detail || v.confirm != nil {
		start = 0
	}
	// Follow a selected list row even when the goal list is taller than the screen.
	if !v.detail && v.confirm == nil {
		i := slices.IndexFunc(v.rows, func(r goal.Overview) bool { return r.ID == v.selected })
		start = max(0, i+3-height)
	}
	for _, line := range body[start:min(len(body), start+height)] {
		out = append(out, previewLine(line, w))
	}
	for len(out) < h-3 {
		out = append(out, "")
	}
	out = append(out, previewLine(badText.Render(goalText(v.err)), w))
	footer := "↑↓ select · enter inspect · n plan · b proposals · f refresh · esc project"
	controls := ""
	if v.detail {
		footer = "↑↓ scroll · tab task · s session · o Linear · f refresh · esc back"
		if g != nil {
			labels := map[string]string{"pause": "p pause", "resume": "r resume", "stop-workers": "x stop workers", "cancel": "c cancel"}
			var actions []string
			for _, a := range g.Actions {
				actions = append(actions, labels[a])
			}
			if goalCanPublish(g) {
				actions = append(actions, "u publish to Linear")
			}
			controls = strings.Join(actions, " · ")
		}
		if v.err != "" {
			controls = "Refresh before using goal controls"
		}
	}
	if v.confirm != nil {
		footer = "enter apply · esc close action · type optional reason"
		if v.confirm.Action == "publish" {
			footer = "enter publish to Linear · esc close action"
		}
		controls = ""
	}
	if v.busy {
		footer = "Applying reviewed action…"
		controls = ""
	}
	out = append(out, previewLine(faintText.Render(footer), w), previewLine(faintText.Render(controls), w))
	return fitHeight(out, h)
}

func goalCanPublish(g *goal.Overview) bool {
	return g != nil && !g.Authorized && (g.State == "draft" || g.State == "provisioning")
}
