package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
)

type goalGraphEditor struct {
	output   goalplan.Output
	reason   string
	selected int
	review   *goalplan.EditRequest
	discard  string // task, notes, graph
	remove   bool
}

func (p *goalPlannerModel) openGraph() {
	b, _ := json.Marshal(p.record.Output)
	g := &goalGraphEditor{}
	_ = json.Unmarshal(b, &g.output) // snapshot: forms never mutate the saved record
	p.graph = g
	p.mode = "graph"
	p.scroll = 0
	p.raw = false
	p.acknowledged = false
	p.err = ""
}

func (p *goalPlannerModel) graphTaskForm() *settingsForm {
	g := p.graph
	t := g.output.Tasks[g.selected]
	f := &settingsForm{fields: []setField{
		{key: "title", label: "Title", kind: sfText, text: t.Title, help: "Name this task's deliverable."},
		{key: "description", label: "Description", kind: sfList, lines: strings.Split(t.Description, "\n"), help: "Describe the implementation boundary. Enter edits lines; Escape returns."},
		{key: "acceptance", label: "Acceptance criteria", kind: sfList, lines: slices.Clone(t.Acceptance), help: "One observable result per line."},
	}}
	for i, criterion := range p.record.Input.Request.Acceptance {
		f.fields = append(f.fields, setField{key: fmt.Sprintf("cover:%d", i), label: fmt.Sprintf("Criterion %d", i+1), kind: sfBool, b: slices.Contains(t.Covers, i+1), help: criterion, section: sectionAt(i, "Goal acceptance coverage")})
	}
	for i, earlier := range g.output.Tasks[:g.selected] {
		f.fields = append(f.fields, setField{key: fmt.Sprintf("dep:%d", i), label: fmt.Sprintf("Depends on task %d", i+1), kind: sfBool, b: slices.Contains(t.BlockedBy, i+1), help: earlier.Title, section: sectionAt(i, "Earlier task dependencies")})
	}
	for _, member := range p.record.Input.Members {
		f.fields = append(f.fields,
			setField{key: "app:" + member.ID, label: "Include repository", kind: sfBool, b: slices.Contains(t.Apps, member.ID), help: member.ID + " · " + member.Repo, section: member.ID},
			setField{key: "paths:" + member.ID, label: "Writable paths", kind: sfList, lines: slices.Clone(t.Paths[member.ID]), help: "One repository-relative path per line. Empty means read-only context. " + member.ID},
		)
		if p.record.Input.Stack {
			labels := strings.Join(member.RoutingLabels, ", ")
			if labels == "" {
				labels = "always included"
			}
			f.fields[len(f.fields)-2].help += " · Routing: " + labels
		}
	}
	for i, label := range p.record.Input.Labels {
		name := label.Name
		if label.Group != "" {
			name = label.Group + "/" + name
		}
		f.fields = append(f.fields, setField{key: "label:" + label.ID, label: name, kind: sfBool, b: slices.Contains(t.LabelIDs, label.ID), help: "Linear label: " + name + " · " + label.ID, section: sectionAt(i, "Linear routing labels")})
	}
	f.fields = append(f.fields, setField{key: "apply", label: "Keep task changes", kind: sfAction, help: "Keep these fields in your draft. Nothing is saved to Lola yet."})
	f.rebase()
	return f
}

func sectionAt(index int, title string) string {
	if index == 0 {
		return title
	}
	return ""
}

func (p *goalPlannerModel) graphNotesForm() *settingsForm {
	f := &settingsForm{fields: []setField{
		{key: "unknowns", label: "Open questions", kind: sfList, lines: slices.Clone(p.graph.output.Unknowns), help: "One unresolved question per line, up to 20. Keep material unknowns visible."},
		{key: "reason", label: "Revision reason", kind: sfList, lines: strings.Split(p.graph.reason, "\n"), help: "Explain why this revision is needed (8–2000 bytes). Retained with the edit history."},
		{key: "apply", label: "Keep notes", kind: sfAction, help: "Keep these notes in your draft, then review the complete graph."},
	}}
	f.rebase()
	return f
}

func (p *goalPlannerModel) applyGraphForm() {
	f, g := p.editor, p.graph
	if p.mode == "graphnotes" {
		g.output.Unknowns = trimDropEmpty(f.field("unknowns").lines)
		g.reason = strings.TrimSpace(strings.Join(f.field("reason").lines, "\n"))
	} else {
		t := goalplan.ProposedTask{TaskDraft: goal.TaskDraft{Title: strings.TrimSpace(f.field("title").text), Description: strings.Join(f.field("description").lines, "\n"), Acceptance: trimDropEmpty(f.field("acceptance").lines), Paths: map[string][]string{}}}
		for i := range p.record.Input.Request.Acceptance {
			if f.field(fmt.Sprintf("cover:%d", i)).b {
				t.Covers = append(t.Covers, i+1)
			}
		}
		for i := 0; i < g.selected; i++ {
			if f.field(fmt.Sprintf("dep:%d", i)).b {
				t.BlockedBy = append(t.BlockedBy, i+1)
			}
		}
		for _, member := range p.record.Input.Members {
			if f.field("app:" + member.ID).b {
				t.Apps = append(t.Apps, member.ID)
				t.Paths[member.ID] = trimDropEmpty(f.field("paths:" + member.ID).lines)
			}
		}
		for _, label := range p.record.Input.Labels {
			if f.field("label:" + label.ID).b {
				t.LabelIDs = append(t.LabelIDs, label.ID)
			}
		}
		g.output.Tasks[g.selected] = t
	}
	p.mode = "graph"
	p.scroll = 0
	p.err = ""
}

// Remap 1-based references with the order. Removing a referenced prerequisite
// or moving it after a dependent is refused, never silently weakened.
func reorderGoalTasks(tasks []goalplan.ProposedTask, order []int) ([]goalplan.ProposedTask, error) {
	if len(order) == 0 {
		return nil, errors.New("Keep at least one task")
	}
	positions := map[int]int{}
	for next, old := range order {
		if old < 0 || old >= len(tasks) || positions[old+1] != 0 {
			return nil, errors.New("Invalid task order")
		}
		positions[old+1] = next + 1
	}
	out := make([]goalplan.ProposedTask, 0, len(order))
	for next, old := range order {
		t := tasks[old]
		t.BlockedBy = nil
		for _, dep := range tasks[old].BlockedBy {
			n := positions[dep]
			if n == 0 {
				return nil, errors.New("Another task depends on this one. Edit its dependencies before removing this task")
			}
			if n > next {
				return nil, errors.New("A prerequisite must stay before the task that depends on it")
			}
			t.BlockedBy = append(t.BlockedBy, n)
		}
		slices.Sort(t.BlockedBy)
		out = append(out, t)
	}
	return out, nil
}

func (p *goalPlannerModel) prepareGraphReview() error {
	g := p.graph
	raw, err := json.Marshal(g.output)
	if err != nil {
		return err
	}
	out, err := goalplan.Parse(raw, p.record.Input)
	if err != nil {
		return err
	}
	original, _ := json.Marshal(p.record.Output)
	if bytes.Equal(raw, original) {
		return errors.New("Change a task or open question before creating a revision")
	}
	request := goalplan.EditRequest{ID: p.record.ID, Project: p.record.Input.Request.Project, Revision: p.record.Revision, EditID: goal.NewID(), Reason: g.reason, Output: out}
	if err = request.Validate(); err != nil {
		return err
	}
	g.review = &request // Parse returned an independent copy, not mutable form data.
	p.mode = "graphreview"
	p.scroll = 0
	p.raw = false
	p.acknowledged = false
	p.err = ""
	return nil
}

func (m *rootModel) updateGoalGraph(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.goals.planner
	g := p.graph
	key := k.String()
	close := func() (tea.Model, tea.Cmd) { m.goals.planner = nil; return m, m.refreshGoals() }
	if p.busy {
		if key == "esc" {
			return close()
		}
		return m, nil
	}
	if p.uncertain {
		if key == "f" {
			return m, m.goalPlannerRequest("inspect", "goalProposal", map[string]string{"id": p.requestID}, p.requestID)
		}
		if key == "esc" {
			return close()
		}
		if key == "up" || key == "down" || key == "pgup" || key == "pgdown" || key == "home" || key == "end" {
			p.scroll = goalGraphScroll(p.scroll, key, m.height)
		}
		return m, nil
	}
	if g.discard != "" {
		if key == "y" || key == "Y" {
			if g.discard == "graph" {
				p.graph = nil
				p.mode = "review"
				p.scroll = 0
				p.err = ""
				return m, nil
			}
			p.mode = "graph"
			p.scroll = 0
			p.err = ""
		}
		g.discard = ""
		return m, nil
	}
	if g.remove {
		g.remove = false
		if key == "y" || key == "Y" {
			order := []int{}
			for i := range g.output.Tasks {
				if i != g.selected {
					order = append(order, i)
				}
			}
			next, err := reorderGoalTasks(g.output.Tasks, order)
			if err != nil {
				p.err = err.Error()
			} else {
				g.output.Tasks = next
				g.selected = min(g.selected, len(next)-1)
				p.err = ""
			}
		}
		return m, nil
	}
	if p.mode == "graphtask" || p.mode == "graphnotes" {
		f := p.editor
		if f.editing {
			if key == "ctrl+c" {
				g.discard = "task"
				return m, nil
			}
			if key == "ctrl+s" {
				f.editing = false
			} else {
				f.editList(k)
			}
			return m, nil
		}
		switch key {
		case "esc", "ctrl+c":
			if f.dirty() {
				g.discard = "task"
			} else {
				p.mode = "graph"
				p.scroll = 0
			}
		case "up", "shift+tab":
			f.cursor = max(0, f.cursor-1)
		case "down", "tab":
			f.cursor = min(len(f.fields)-1, f.cursor+1)
		case "enter":
			fld := f.cur()
			switch fld.kind {
			case sfList:
				f.openList(fld)
			case sfBool:
				fld.b = !fld.b
			case sfAction:
				p.applyGraphForm()
			}
		case "space":
			if f.cur().kind == sfBool {
				f.cur().b = !f.cur().b
			} else if f.cur().kind == sfText {
				f.cur().text += " "
			}
		case "backspace":
			if f.cur().kind == sfText {
				f.cur().text = dropLastRune(f.cur().text)
			}
		default:
			f.typeInto(f.cur(), k)
		}
		return m, nil
	}
	if p.mode == "graphreview" {
		switch key {
		case "esc", "ctrl+c":
			p.mode = "graph"
			p.scroll = 0
			p.acknowledged = false
			g.review = nil
		case "space":
			p.acknowledged = !p.acknowledged
		case "s":
			if p.acknowledged && g.review != nil {
				return m, m.goalPlannerRequest("edit", "goalEditPlan", *g.review, p.requestID)
			}
		case "v":
			p.raw = !p.raw
			p.scroll = 0
		default:
			p.scroll = goalGraphScroll(p.scroll, key, m.height)
		}
		return m, nil
	}
	switch key {
	case "esc", "ctrl+c":
		g.discard = "graph"
	case "up", "k":
		g.selected = max(0, g.selected-1)
	case "down", "j":
		g.selected = min(len(g.output.Tasks)-1, g.selected+1)
	case "enter", "e":
		p.editor = p.graphTaskForm()
		p.mode = "graphtask"
		p.scroll = 0
		p.err = ""
	case "n":
		if len(g.output.Tasks) >= p.record.Input.Request.MaxTasks {
			p.err = "Maximum task count reached"
			break
		}
		g.output.Tasks = append(g.output.Tasks, goalplan.ProposedTask{})
		g.selected = len(g.output.Tasks) - 1
		p.editor = p.graphTaskForm()
		p.mode = "graphtask"
		p.scroll = 0
		p.err = ""
	case "m":
		p.editor = p.graphNotesForm()
		p.mode = "graphnotes"
		p.scroll = 0
		p.err = ""
	case "r":
		if err := p.prepareGraphReview(); err != nil {
			p.err = err.Error()
		}
	case "x":
		g.remove = true
	case "u", "d":
		target := g.selected - 1
		if key == "d" {
			target = g.selected + 1
		}
		if target < 0 || target >= len(g.output.Tasks) {
			break
		}
		order := make([]int, len(g.output.Tasks))
		for i := range order {
			order[i] = i
		}
		order[target], order[g.selected] = order[g.selected], order[target]
		next, err := reorderGoalTasks(g.output.Tasks, order)
		if err != nil {
			p.err = err.Error()
		} else {
			g.output.Tasks = next
			g.selected = target
			p.err = ""
		}
	}
	return m, nil
}

func goalGraphScroll(scroll int, key string, height int) int {
	switch key {
	case "up", "k":
		return max(0, scroll-1)
	case "down", "j":
		return scroll + 1
	case "pgup":
		return max(0, scroll-max(1, height/2))
	case "pgdown":
		return scroll + max(1, height/2)
	case "home":
		return 0
	case "end":
		return 1 << 20
	}
	return scroll
}

func (p *goalPlannerModel) graphReviewLines() []string {
	g := p.graph
	if g.review == nil {
		return []string{"No reviewed edit. Return to the task graph."}
	}
	if p.raw {
		b, _ := json.MarshalIndent(g.review, "", "  ")
		return strings.Split(string(b), "\n")
	}
	// Reuse the complete plan renderer with a temporary immutable record.
	copyRecord := *p.record
	copyRecord.Output = &g.review.Output
	copyRecord.State = "editing"
	preview := *p
	preview.record = &copyRecord
	preview.raw = false
	out := []string{"Review proposal revision", "Edit ID: " + g.review.EditID, "Reason: " + g.review.Reason, ""}
	out = append(out, preview.reviewLines()...)
	return append(out, "", "Saving creates a proposal revision only. No model call, issues or workers.", "The original model output and this edit's reason remain in the planning record.")
}
