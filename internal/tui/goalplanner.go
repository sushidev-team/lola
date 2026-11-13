package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/protocol"
)

// Uses the settings form's field renderer and line-editor primitives only.
// Settings persistence and its key dispatcher are deliberately not used here.
type goalPlannerModel struct {
	editor                                 *settingsForm
	graph                                  *goalGraphEditor
	mode                                   string // brief, saved, review
	seq                                    int
	busy, acknowledged, uncertain, discard bool
	raw                                    bool
	err, requestID                         string
	rows                                   []goalplan.Summary
	selected, scroll                       int
	record                                 *goalplan.Record
}

type goalPlannerMsg struct {
	project  string
	gen, seq int
	op, id   string
	data     json.RawMessage
	err      string
}

func newGoalBrief() *settingsForm {
	f := &settingsForm{fields: []setField{
		{key: "title", label: "Title", kind: sfText, help: "Name the outcome this goal should deliver."},
		{key: "outcome", label: "Desired outcome", kind: sfList, help: "Enter edits the brief. Escape returns to fields."},
		{key: "acceptance", label: "Acceptance criteria", kind: sfList, help: "One observable result per line. The coordinator must cover every criterion."},
		{key: "non_goals", label: "Outside this goal", kind: sfList, help: "Optional exclusions, one per line."},
		{key: "max_tasks", label: "Maximum tasks", kind: sfInt, text: "10", help: "Bound the proposal to 1–50 tasks."},
		{key: "timeout", label: "Planning timeout", kind: sfInt, text: "180", help: "One model call, limited to 30–240 seconds."},
		{key: "model", label: "Planning model", kind: sfText, help: "Optional model; blank uses the configured agent default."},
		{key: "milestone_name", label: "New milestone name", kind: sfText, help: "Optional. Created in the existing Linear project only when you publish."},
		{key: "milestone_id", label: "Existing milestone ID", kind: sfText, help: "Optional existing Linear milestone UUID. Use this or a new name, not both."},
		{key: "context", label: "Context files", kind: sfList, help: "Optional member:path entries, one committed file per line. No working-tree reads."},
		{key: "request", label: "Request proposal", kind: sfAction, help: "Enter makes one bounded model call. No Linear writes or coding workers."},
	}}
	f.rebase()
	return f
}

func (m *rootModel) openGoalPlanner(saved bool) tea.Cmd {
	v := &m.goals
	v.gen++           // isolate outstanding overview/control replies from this review
	v.loading = false // the old overview response was invalidated above
	p := &goalPlannerModel{editor: newGoalBrief(), mode: "brief"}
	v.planner = p
	if saved {
		p.mode = "saved"
		return m.goalPlannerRequest("list", "goalProposals", map[string]string{"project": v.project}, "")
	}
	return nil
}

// Marshal before the asynchronous command starts: the payload is the exact
// brief or revision reviewed, independent of subsequent UI navigation.
func (m *rootModel) goalPlannerRequest(op, command string, args any, id string) tea.Cmd {
	v, p := &m.goals, m.goals.planner
	raw, err := json.Marshal(args)
	if err != nil {
		p.err = err.Error()
		return nil
	}
	p.seq++
	p.busy = true
	p.err = ""
	project, gen, seq := v.project, v.gen, p.seq
	return func() tea.Msg {
		msg := goalPlannerMsg{project: project, gen: gen, seq: seq, op: op, id: id}
		resp, err := requestFn(protocol.Request{Cmd: command, Args: raw})
		if err == nil && resp == nil {
			err = errors.New("empty daemon response")
		}
		if err == nil && !resp.OK {
			err = errors.New(resp.Error)
		}
		if err != nil {
			msg.err = err.Error()
		} else {
			msg.data = resp.Data
		}
		return msg
	}
}

func (m *rootModel) handleGoalPlannerMsg(msg goalPlannerMsg) {
	v, p := &m.goals, m.goals.planner
	if p == nil || msg.project != v.project || msg.gen != v.gen || msg.seq != p.seq {
		return
	}
	p.busy = false
	failure := func(reason string) {
		p.err = reason + ". Refresh the saved result before another action."
		p.acknowledged = false
		p.uncertain = true
	}
	if msg.err != "" {
		if msg.op == "list" {
			p.err = "Could not load saved proposals: " + msg.err + ". Press f to refresh."
		} else {
			failure("Response was not confirmed: " + msg.err)
		}
		return
	}
	switch msg.op {
	case "list":
		var rows []goalplan.Summary
		if err := json.Unmarshal(msg.data, &rows); err != nil {
			p.err = "Invalid proposal list. Press f to refresh."
			return
		}
		for _, r := range rows {
			if r.Project != v.project {
				p.err = "Proposal list belongs to another project. Press f to refresh."
				return
			}
		}
		p.rows = rows
		p.selected = min(p.selected, max(0, len(rows)-1))
		p.err = ""
	case "inspect", "propose", "edit":
		var r goalplan.Record
		if err := json.Unmarshal(msg.data, &r); err != nil {
			failure("Invalid proposal result")
			return
		}
		if r.ID != msg.id || r.Input.Request.ID != r.ID || r.Input.Request.Project != v.project || r.Input.Repair != nil || r.Revision == 0 {
			failure("Proposal result does not match this request and project")
			return
		}
		if (r.State == "proposed" || r.State == "accepted") && r.Output == nil {
			failure("Proposal result is missing its task plan")
			return
		}
		if msg.op == "edit" && (p.graph == nil || p.graph.review == nil || !r.HasEdit(p.graph.review.EditID)) {
			failure("Saved proposal is missing the reviewed edit identity")
			return
		}
		p.graph = nil
		p.record = &r
		p.requestID = r.ID
		p.mode = "review"
		p.scroll = 0
		p.raw = false
		p.acknowledged = false
		p.uncertain = false
		p.err = ""
	case "accept":
		var g goal.Goal
		if err := json.Unmarshal(msg.data, &g); err != nil || p.record == nil || g.ID != p.record.GoalID || g.ID == "" || g.Project != v.project {
			failure("Acceptance response does not identify the reviewed local goal")
			return
		}
		p.record.State = "accepted"
		p.acknowledged = false
		p.uncertain = false
		p.err = ""
	}
}

func (p *goalPlannerModel) briefRequest(project string) (goalplan.Request, error) {
	field := p.editor.field
	tasks, err := strconv.Atoi(field("max_tasks").text)
	if err != nil {
		return goalplan.Request{}, errors.New("Maximum tasks must be a whole number from 1 to 50")
	}
	timeout, err := strconv.Atoi(field("timeout").text)
	if err != nil {
		return goalplan.Request{}, errors.New("Planning timeout must be a whole number from 30 to 240")
	}
	r := goalplan.Request{ID: goal.NewID(), Project: project, Title: strings.TrimSpace(field("title").text), Outcome: strings.TrimSpace(strings.Join(field("outcome").lines, "\n")), Acceptance: trimDropEmpty(field("acceptance").lines), NonGoals: trimDropEmpty(field("non_goals").lines), MilestoneID: strings.TrimSpace(field("milestone_id").text), MilestoneName: strings.TrimSpace(field("milestone_name").text), Model: strings.TrimSpace(field("model").text), MaxTasks: tasks, TimeoutSeconds: timeout}
	// Normalize's zero values are CLI defaults; an explicitly entered zero in
	// this form must not silently change the reviewed planning limit.
	if tasks < 1 || tasks > 50 || timeout < 30 || timeout > 240 {
		return r, errors.New("Use 1–50 tasks and a planning timeout of 30–240 seconds")
	}
	for _, line := range trimDropEmpty(field("context").lines) {
		member, path, ok := strings.Cut(line, ":")
		member = strings.TrimSpace(member)
		path = strings.TrimSpace(path)
		if !ok || member == "" || path == "" {
			return r, errors.New("Context files use member:path, one committed file per line")
		}
		if r.ContextPaths == nil {
			r.ContextPaths = map[string][]string{}
		}
		r.ContextPaths[member] = append(r.ContextPaths[member], path)
	}
	return r, r.Normalize()
}

func (m *rootModel) updateGoalPlanner(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.goals.planner
	if p.graph != nil {
		return m.updateGoalGraph(k)
	}
	close := func() (tea.Model, tea.Cmd) { m.goals.planner = nil; return m, m.refreshGoals() }
	key := k.String()
	if key == "ctrl+c" {
		return m, nil
	}
	if p.discard {
		p.discard = false
		if key == "y" || key == "Y" {
			return close()
		}
		return m, nil
	}
	// A running paid request can be left; its durable ID stays discoverable in
	// Saved proposals. A late reply cannot land on another project or editor.
	if p.busy {
		if key == "esc" {
			return close()
		}
		return m, nil
	}
	if p.mode == "brief" {
		f := p.editor
		if f.editing {
			// The settings editor's ctrl-s path persists config; never invoke it here.
			if key == "ctrl+s" {
				f.editing = false
			} else {
				f.editList(k)
			}
			return m, nil
		}
		switch key {
		case "esc":
			if f.dirty() {
				p.discard = true
				return m, nil
			}
			return close()
		case "up", "shift+tab":
			f.cursor = max(0, f.cursor-1)
		case "down", "tab":
			f.cursor = min(len(f.fields)-1, f.cursor+1)
		case "enter":
			fld := f.cur()
			if fld.kind == sfList {
				f.openList(fld)
			}
			if fld.kind == sfAction {
				r, err := p.briefRequest(m.goals.project)
				if err != nil {
					p.err = err.Error()
					return m, nil
				}
				p.requestID = r.ID
				p.mode = "review"
				p.record = nil
				p.acknowledged = false
				p.uncertain = false
				p.scroll = 0
				return m, m.goalPlannerRequest("propose", "goalPropose", r, r.ID)
			}
		case "backspace":
			if f.cur().kind == sfText || f.cur().kind == sfInt {
				f.cur().text = dropLastRune(f.cur().text)
			}
		case "space":
			if f.cur().kind == sfText {
				f.cur().text += " "
			}
		default:
			f.typeInto(f.cur(), k)
		}
		return m, nil
	}
	switch key {
	case "esc":
		return close()
	case "n":
		p.editor = newGoalBrief()
		p.mode = "brief"
		p.requestID = ""
		p.record = nil
		p.err = ""
		p.uncertain = false
		p.acknowledged = false
		p.scroll = 0
	case "b":
		p.mode = "saved"
		p.scroll = 0
		return m, m.goalPlannerRequest("list", "goalProposals", map[string]string{"project": m.goals.project}, "")
	case "f":
		if p.mode == "saved" {
			return m, m.goalPlannerRequest("list", "goalProposals", map[string]string{"project": m.goals.project}, "")
		}
		if p.requestID != "" {
			p.acknowledged = false
			p.record = nil
			return m, m.goalPlannerRequest("inspect", "goalProposal", map[string]string{"id": p.requestID}, p.requestID)
		}
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		delta := 1
		if key == "up" || key == "k" {
			delta = -1
		}
		if key == "pgup" {
			delta = -max(1, m.height/2)
		}
		if key == "pgdown" {
			delta = max(1, m.height/2)
		}
		if p.mode == "saved" {
			p.selected = max(0, min(max(0, len(p.rows)-1), p.selected+delta))
		} else {
			p.scroll = max(0, p.scroll+delta)
			if key == "home" {
				p.scroll = 0
			}
			if key == "end" {
				p.scroll = 1 << 20
			}
		}
	case "enter":
		if p.mode == "saved" && len(p.rows) > 0 {
			p.requestID = p.rows[p.selected].ID
			p.record = nil
			p.mode = "review"
			p.scroll = 0
			return m, m.goalPlannerRequest("inspect", "goalProposal", map[string]string{"id": p.requestID}, p.requestID)
		}
	case "e":
		if p.mode == "review" && p.record != nil && p.record.State == "proposed" && p.record.Output != nil && len(p.record.Edits) < 50 && !p.uncertain {
			p.openGraph()
		}
	case "v":
		if p.mode == "review" && p.record != nil {
			p.raw = !p.raw
			p.scroll = 0
		}
	case "space":
		if p.record != nil && p.record.State == "proposed" && !p.uncertain {
			p.acknowledged = !p.acknowledged
		}
	case "a":
		if p.record != nil && p.record.State == "proposed" && p.acknowledged && !p.uncertain {
			return m, m.goalPlannerRequest("accept", "goalAcceptPlan", struct {
				ID       string `json:"id"`
				Revision uint64 `json:"revision"`
			}{p.record.ID, p.record.Revision}, p.record.ID)
		}
	case "g":
		if p.record != nil && p.record.State == "accepted" && p.record.GoalID != "" {
			m.goals.selected = p.record.GoalID
			m.goals.detail = true
			return close()
		}
	}
	return m, nil
}

func (p *goalPlannerModel) reviewLines() []string {
	out := []string{"Proposal ID: " + p.requestID}
	r := p.record
	if p.raw && r != nil {
		b, _ := json.MarshalIndent(r, "", "  ")
		return append(out, strings.Split(string(b), "\n")...)
	}
	if r == nil {
		return append(out, "Refresh result to inspect the durable request; this never repeats the model call.")
	}
	out = append(out, fmt.Sprintf("%s · %s · revision %d", r.Input.Request.Title, r.State, r.Revision), r.Input.Request.Outcome, "", "Goal acceptance")
	for i, c := range r.Input.Request.Acceptance {
		out = append(out, fmt.Sprintf("  %d. %s", i+1, c))
	}
	if len(r.Input.Request.NonGoals) > 0 {
		out = append(out, "Outside this goal")
		out = append(out, r.Input.Request.NonGoals...)
	}
	if r.Input.Request.MilestoneName != "" {
		out = append(out, "New milestone on publication: "+r.Input.Request.MilestoneName)
	}
	if r.Input.Request.MilestoneID != "" {
		out = append(out, "Existing milestone: "+r.Input.Request.MilestoneID)
	}
	if r.Error != "" {
		out = append(out, "", "Planning result: "+r.Error)
	}
	if r.Output != nil {
		if len(r.Output.Unknowns) > 0 {
			out = append(out, "", "Open questions")
			out = append(out, r.Output.Unknowns...)
		}
		for i, t := range r.Output.Tasks {
			out = append(out, "", fmt.Sprintf("%d. %s", i+1, t.Title), t.Description, "Repositories: "+strings.Join(t.Apps, ", "), fmt.Sprintf("Covers criteria: %v · Depends on tasks: %v", t.Covers, t.BlockedBy))
			for _, c := range t.Acceptance {
				out = append(out, "  - "+c)
			}
			members := make([]string, 0, len(t.Paths))
			for member := range t.Paths {
				members = append(members, member)
			}
			slices.Sort(members)
			for _, member := range members {
				scope := strings.Join(t.Paths[member], ", ")
				if scope == "" {
					scope = "Read-only context"
				}
				out = append(out, member+": "+scope)
			}
			if len(t.LabelIDs) > 0 {
				out = append(out, "Routing label IDs: "+strings.Join(t.LabelIDs, ", "))
			}
		}
	}
	model := r.Input.Request.Model
	if model == "" {
		model = "agent default"
	}
	out = append(out, "", "Planning sources", "Agent: "+r.Agent+" · Model: "+model, "Committed files only; uncommitted changes were excluded.")
	for _, member := range r.Input.Members {
		out = append(out, member.ID+" · "+member.Snapshot.Target+" · "+member.Snapshot.Commit)
		for _, file := range member.Snapshot.Files {
			out = append(out, "  "+file.Path)
		}
	}
	if len(r.Edits) > 0 {
		out = append(out, "", "Revision history (full original model output in v record)")
		for _, edit := range r.Edits {
			out = append(out, fmt.Sprintf("Revision %d · %s · %s", edit.Revision, edit.At.Format("2006-01-02 15:04"), edit.Request.Reason))
		}
	}
	if r.State == "accepted" {
		out = append(out, "", "Local goal created: "+r.GoalID, "Publication and execution authorization remain separate steps.")
	} else if r.State == "proposed" {
		out = append(out, "", "Accepting creates a local goal and rechecks commits and routing.", "It does not publish Linear issues or authorize execution.")
	}
	return out
}

func (m *rootModel) goalPlannerLines() []string {
	p := m.goals.planner
	w, h := max(30, m.width), max(12, m.height)
	wrap := func(lines []string) []string {
		var out []string
		for _, line := range lines {
			out = append(out, strings.Split(lipgloss.NewStyle().Width(w).Render(goalText(line)), "\n")...)
		}
		return out
	}
	bounded := func(lines []string, limit int) []string {
		lines = wrap(lines)
		if len(lines) > limit {
			lines = append(lines[:limit-1], "…")
		}
		return lines
	}
	out := []string{m.vitalsBar(w), boxTitleHi.Render("lola ▸ " + m.projLabel(m.goals.project) + " ▸ Plan a goal"), ""}
	var body, footer []string
	selectedEnd := 0
	formMode := p.mode == "brief" || p.mode == "graphtask" || p.mode == "graphnotes"
	if formMode {
		if p.mode == "brief" {
			body = append(body, "Describe the outcome; review the coordinator's tasks before creating a goal.", "")
		} else if p.mode == "graphtask" {
			body = append(body, fmt.Sprintf("Edit task %d · draft only", p.graph.selected+1), "Goal criteria stay fixed. Each task needs coverage and a writable deliverable.", "")
		} else {
			body = append(body, "Open questions and revision reason · draft only", "")
		}
		help := p.editor.cur().help
		if p.editor.cur().key == "request" {
			help = fmt.Sprintf("One paid model call: up to %s tasks / %ss. No issues or workers.", p.editor.field("max_tasks").text, p.editor.field("timeout").text)
		}
		if p.editor.editing {
			footer = append(footer, "Editing "+p.editor.cur().label, "↑↓ line · enter new line · esc done")
		} else {
			footer = append(footer, bounded([]string{help}, 3)...)
			footer = append(footer, "↑↓/tab field · enter edit/action · space toggle · esc back")
		}
	} else if p.mode == "graph" {
		g := p.graph
		body = append(body, "Edit task graph · unsaved draft", p.record.Input.Request.Title, "Goal criteria:")
		for i, criterion := range p.record.Input.Request.Acceptance {
			body = append(body, fmt.Sprintf("  %d. %s", i+1, criterion))
		}
		body = append(body, "")
		for i, task := range g.output.Tasks {
			mark := "  "
			if i == g.selected {
				mark = "> "
			}
			title := task.Title
			if title == "" {
				title = "Untitled task"
			}
			body = append(body, fmt.Sprintf("%s%d. %s", mark, i+1, title), fmt.Sprintf("    Covers %v · Depends on %v · %s", task.Covers, task.BlockedBy, strings.Join(task.Apps, ", ")))
			if i == g.selected {
				selectedEnd = len(wrap(body))
			}
		}
		body = append(body, "", "Reason: "+g.reason, fmt.Sprintf("Open questions: %d (m to edit)", len(g.output.Unknowns)))
		footer = append(footer, "↑↓ select · enter edit · n add · x remove · u/d move earlier/later", "m questions/reason · r review revision · esc discard draft")
	} else if p.mode == "graphreview" {
		body = p.graphReviewLines()
		ack := "[ ]"
		if p.acknowledged {
			ack = "[x]"
		}
		footer = append(footer, ack+" I reviewed scope, dependencies, coverage and open questions.", "space acknowledge · s save proposal revision · esc edit graph", "↑↓/pgup/pgdown scroll · v exact edit request")
	} else if p.mode == "saved" {
		body = append(body, "Saved proposals", "Inspect a saved result without requesting another model call.", "")
		if len(p.rows) == 0 && !p.busy && p.err == "" {
			body = append(body, "No saved proposals yet. Press n to write a brief.")
		}
		for i, r := range p.rows {
			mark := "  "
			if i == p.selected {
				mark = "> "
			}
			body = append(body, mark+r.Title+" · "+r.State)
			if i == p.selected {
				selectedEnd = len(wrap(body))
			}
		}
		footer = append(footer, "↑↓ select · enter inspect · f refresh · n new brief · esc goals")
	} else {
		body = p.reviewLines()
		if p.record != nil && p.record.State == "proposed" && !p.uncertain {
			ack := "[ ]"
			if p.acknowledged {
				ack = "[x]"
			}
			footer = append(footer, ack+" I reviewed tasks, scope, coverage and open questions.", "space acknowledge · a accept as local goal · e edit tasks")
		}
		if p.record != nil && p.record.State == "accepted" {
			footer = append(footer, "g open local goal; publication and execution are separate")
		}
		footer = append(footer, "↑↓/pgup/pgdown scroll · v record · f refresh result", "b saved · n new brief · esc goals")
	}
	if p.graph != nil {
		if p.uncertain {
			footer = []string{"Unconfirmed result: f refresh saved proposal · esc goals", "Refresh reads the saved revision; it never resends the edit.", "↑↓/pgup/pgdown scroll reviewed content"}
		}
		if p.graph.discard != "" {
			footer = []string{"Unsaved changes — y discard · any other key keep editing"}
		}
		if p.graph.remove {
			footer = []string{fmt.Sprintf("Remove task %d? y remove · any other key keep", p.graph.selected+1), "Removal is refused while another task depends on it."}
		}
	}
	if p.busy {
		footer = []string{"Waiting for this operation. Escape returns to Goals.", "Find this request again in Saved proposals; no automatic retry."}
	}
	if p.discard {
		footer = []string{"Unsaved brief — y discard · n keep editing"}
	}
	if p.err != "" {
		footer = append(bounded([]string{"Error: " + p.err}, 3), footer...)
	}
	footer = wrap(footer)
	avail := max(1, h-len(out)-len(footer)-1)
	if formMode {
		intro := wrap(body)
		region, anchors := p.editor.fieldRegion()
		// Wrap field values without losing the cursor's line anchor.
		var wrapped []string
		translated := make([]int, len(anchors))
		for i, line := range region {
			for j, anchor := range anchors {
				if anchor == i {
					translated[j] = len(wrapped)
				}
			}
			wrapped = append(wrapped, wrap([]string{line})...)
		}
		body = append(intro, p.editor.window(wrapped, translated, max(1, avail-len(intro)))...)
	} else {
		body = wrap(body)
		if p.mode == "saved" || p.mode == "graph" {
			p.scroll = max(0, selectedEnd-avail)
		}
		p.scroll = min(p.scroll, max(0, len(body)-avail))
		end := min(len(body), p.scroll+avail)
		body = body[p.scroll:end]
	}
	for _, line := range body {
		out = append(out, previewLine(line, w))
	}
	for len(out) < h-len(footer)-1 {
		out = append(out, "")
	}
	out = append(out, "")
	out = append(out, footer...)
	return fitHeight(out, h)
}
