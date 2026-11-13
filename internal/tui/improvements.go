package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

type improvementModel struct {
	scope                                         knowledge.Scope
	seq, selected, scroll                         int
	busy, uncertain, discard, loaded, follow, raw bool
	mode, err, notice, after                      string
	total                                         int
	rows                                          []knowledge.ImprovementRecord
	record                                        *knowledge.ImprovementRecord
	proposal                                      *knowledge.Improvement
	history                                       []knowledge.ImprovementDecision
	sources                                       *knowledge.ImprovementSources
	lesson                                        *knowledge.Lesson
	files                                         map[string]string
	editor                                        *settingsForm
	decision                                      string
	reviewed                                      *improvementReview
}
type improvementReview struct {
	Proposal knowledge.Improvement
	Request  knowledge.ImprovementTriage
}
type improvementMsg struct {
	project, op string
	gen, seq    int
	data        json.RawMessage
	err         string
}
type improvementInspection struct {
	Proposal *knowledge.Improvement
	History  []knowledge.ImprovementDecision
}

var improvementLabels = map[string]string{"tracked": "Track", "dismissed": "Dismiss", "reopened": "Reopen"}

func sameLearningScope(a, b knowledge.Scope) bool {
	return a.Project == b.Project && a.GoalID == b.GoalID
}

func (m *rootModel) openImprovements() tea.Cmd {
	p := &m.knowledge
	p.gen++
	p.improvements = &improvementModel{scope: knowledge.Scope{Project: p.project}, mode: "catalog"}
	return m.improvementCatalog(false)
}

// Requests freeze arguments at dispatch and never retry effects. The generation
// excludes replies from a closed/reopened view, even within the same project.
func (m *rootModel) improvementRequest(op string, read func() (json.RawMessage, error)) tea.Cmd {
	p := m.knowledge.improvements
	if p == nil || p.busy {
		return nil
	}
	p.seq++
	p.busy = true
	p.err = ""
	project, gen, seq := m.knowledge.project, m.knowledge.gen, p.seq
	return func() tea.Msg {
		data, err := read()
		msg := improvementMsg{project: project, gen: gen, seq: seq, op: op, data: data}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}
func improvementRPC(command string, args any) func() (json.RawMessage, error) {
	raw, err := json.Marshal(args)
	return func() (json.RawMessage, error) {
		if err != nil {
			return nil, err
		}
		r, e := requestFn(protocol.Request{Cmd: command, Args: raw})
		if e != nil {
			return nil, e
		}
		if r == nil {
			return nil, errors.New("empty daemon response")
		}
		if !r.OK {
			return nil, errors.New(r.Error)
		}
		return r.Data, nil
	}
}
func (m *rootModel) improvementCatalog(more bool) tea.Cmd {
	p := m.knowledge.improvements
	after := ""
	op := "catalog"
	if more {
		after = p.after
		op = "catalog-more"
		if after == "" {
			return nil
		}
	}
	return m.improvementRequest(op, improvementRPC("knowledgeImprovementCatalog", map[string]any{"scope": p.scope, "after": after}))
}
func (m *rootModel) inspectImprovement(record knowledge.ImprovementRecord) tea.Cmd {
	p := m.knowledge.improvements
	p.record = &record
	// Both reads must succeed before replacing any reviewed or uncertain state.
	args := map[string]any{"scope": record.Scope, "id": record.ID}
	current := improvementRPC("knowledgeImprovement", args)
	history := improvementRPC("knowledgeImprovementDecisions", args)
	return m.improvementRequest("inspect", func() (json.RawMessage, error) {
		var out improvementInspection
		if record.Available {
			b, e := current()
			if e != nil {
				return nil, e
			}
			if e = json.Unmarshal(b, &out.Proposal); e != nil {
				return nil, e
			}
		}
		b, e := history()
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &out.History); e != nil {
			return nil, e
		}
		return json.Marshal(out)
	})
}
func (m *rootModel) improvementSources(revision string, more bool) tea.Cmd {
	p := m.knowledge.improvements
	if p.record == nil {
		return nil
	}
	after := ""
	op := "sources"
	if more {
		if p.sources == nil || p.sources.NextAfter == "" {
			return nil
		}
		after = p.sources.NextAfter
		op = "sources-more"
	}
	// Retain the requested identity separately from the response.
	request := knowledge.ImprovementSources{ID: p.record.ID, Revision: revision, Scope: p.scope}
	if !more {
		p.sources = &request
		p.selected = 0
	}
	return m.improvementRequest(op, improvementRPC("knowledgeImprovementSources", map[string]any{"scope": p.scope, "id": p.record.ID, "revision": revision, "after": after}))
}
func (m *rootModel) handleImprovementMsg(msg improvementMsg) tea.Cmd {
	v, p := &m.knowledge, m.knowledge.improvements
	if m.view != viewKnowledge || p == nil || msg.project != v.project || msg.gen != v.gen || msg.seq != p.seq {
		return nil
	}
	p.busy = false
	fail := func(reason string) {
		p.err = reason + ". Reload before another decision."
		if msg.op == "apply" {
			p.uncertain = true
			p.err = "Decision not confirmed: " + reason + ". It may be saved; reload, do not repeat."
		}
	}
	if msg.err != "" {
		fail(msg.err)
		return nil
	}
	invalid := func() { fail("Reply does not match the requested scope, source or revision") }
	switch msg.op {
	case "catalog", "catalog-more":
		var result knowledge.ImprovementCatalog
		if json.Unmarshal(msg.data, &result) != nil {
			invalid()
			return nil
		}
		after := ""
		if msg.op == "catalog-more" {
			after = p.after
		}
		last := after
		for _, r := range result.Records {
			if r.ID <= last || r.Revision == "" || !sameLearningScope(r.Scope, p.scope) {
				invalid()
				return nil
			}
			last = r.ID
		}
		if result.NextAfter != "" && (result.NextAfter != last || last == after) {
			invalid()
			return nil
		}
		if msg.op == "catalog" {
			p.rows = nil
			p.selected = 0
			p.scroll = 0
		}
		p.rows = append(p.rows, result.Records...)
		p.after = result.NextAfter
		p.total = result.Total
		p.loaded = true
		p.mode = "catalog"
		p.editor = nil
	case "inspect":
		var result improvementInspection
		if json.Unmarshal(msg.data, &result) != nil || p.record == nil {
			invalid()
			return nil
		}
		current := result.Proposal
		if p.record.Available && (current == nil || current.ID != p.record.ID || current.Revision == "" || !sameLearningScope(current.Scope, p.scope)) {
			invalid()
			return nil
		}
		for _, d := range result.History {
			if d.Revision == "" || !sameLearningScope(d.Scope, p.scope) {
				invalid()
				return nil
			}
		}
		if current != nil {
			p.record.Revision = current.Revision
			p.record.State = current.State
			p.record.Topic = current.Topic
		}
		p.record.DecisionCount = len(result.History)
		for i := range p.rows {
			if p.rows[i].ID == p.record.ID {
				p.rows[i] = *p.record
			}
		}
		p.proposal = current
		p.history = result.History
		p.sources = nil
		p.lesson = nil
		p.files = nil
		p.editor = nil
		p.reviewed = nil
		p.decision = ""
		p.uncertain = false
		p.discard = false
		p.mode = "detail"
		p.scroll = 0
		p.selected = 0
	case "sources", "sources-more":
		var result knowledge.ImprovementSources
		if json.Unmarshal(msg.data, &result) != nil || p.sources == nil || result.ID != p.sources.ID || result.Revision != p.sources.Revision || !sameLearningScope(result.Scope, p.scope) {
			invalid()
			return nil
		}
		after := ""
		if msg.op == "sources-more" {
			after = p.sources.NextAfter
		}
		last := after
		for _, r := range result.References {
			if r.ID <= last || r.Version < 1 {
				invalid()
				return nil
			}
			last = r.ID
		}
		if result.NextAfter != "" && (result.NextAfter != last || last == after) {
			invalid()
			return nil
		}
		if msg.op == "sources-more" {
			result.References = append(slices.Clone(p.sources.References), result.References...)
		}
		p.sources = &result
		p.mode = "sources"
		p.scroll = 0
	case "lesson":
		var result knowledge.Lesson
		if json.Unmarshal(msg.data, &result) != nil || p.sources == nil || p.selected >= len(p.sources.References) {
			invalid()
			return nil
		}
		ref := p.sources.References[p.selected]
		if result.ID != ref.ID || result.Version != ref.Version || !sameLearningScope(result.Scope, p.scope) {
			invalid()
			return nil
		}
		p.raw = false
		p.lesson = &result
		p.mode = "lesson"
		p.scroll = 0
	case "export":
		var result map[string]string
		if json.Unmarshal(msg.data, &result) != nil {
			invalid()
			return nil
		}
		p.files = result
		p.mode = "export"
		p.scroll = 0
	case "apply":
		var result knowledge.Improvement
		if json.Unmarshal(msg.data, &result) != nil || p.reviewed == nil || result.ID != p.reviewed.Request.ID || result.Revision != p.reviewed.Request.Revision || !sameLearningScope(result.Scope, p.scope) {
			invalid()
			return nil
		}
		p.uncertain = true
		p.notice = "Decision saved; reading current proposal and history."
		return m.inspectImprovement(*p.record)
	}
	return nil
}
func (p *improvementModel) choose(decision string) {
	if p.proposal == nil || p.err != "" || p.busy || p.uncertain || improvementLabels[decision] == "" {
		return
	}
	p.decision = decision
	p.notice = ""
	p.scroll = 0
	p.editor = &settingsForm{fields: []setField{{key: "evidence", label: "Decision evidence", kind: sfList, help: "8–2000 UTF-8 bytes; explain verification or a follow-up task. No credentials."}, {key: "review", label: "Review decision", kind: sfAction, help: "Review the exact source revision before confirming."}}}
	p.editor.rebase()
}
func (p *improvementModel) review() error {
	evidence := strings.TrimSpace(strings.Join(p.editor.field("evidence").lines, "\n"))
	if len(evidence) < 8 || len(evidence) > 2000 || strings.ContainsFunc(evidence, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return errors.New("evidence needs 8–2000 UTF-8 bytes without control characters")
	}
	raw, err := json.Marshal(p.proposal)
	if err != nil {
		return err
	}
	review := &improvementReview{}
	if err = json.Unmarshal(raw, &review.Proposal); err != nil {
		return err
	}
	review.Request = knowledge.ImprovementTriage{Scope: review.Proposal.Scope, ID: review.Proposal.ID, Revision: review.Proposal.Revision, Decision: p.decision, Evidence: evidence}
	p.reviewed = review
	p.err = ""
	p.scroll = 0
	return nil
}
func (m *rootModel) updateImprovements(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	key := k.String()
	p := m.knowledge.improvements
	if p.busy {
		return m, nil
	}
	closeForm := func() {
		p.editor = nil
		p.reviewed = nil
		p.decision = ""
		p.discard = false
		p.err = ""
		p.scroll = 0
	}
	if p.discard {
		if key == "y" || key == "Y" {
			closeForm()
		} else {
			p.discard = false
		}
		return m, nil
	}
	if p.uncertain {
		if key == "f" && p.record != nil {
			return m, m.inspectImprovement(*p.record)
		}
		if key == "esc" {
			m.knowledge.gen++
			m.knowledge.improvements = nil
		}
		return m, nil
	}
	if key == "ctrl+c" {
		if p.editor != nil {
			p.discard = true
		}
		return m, nil
	}
	if p.reviewed != nil {
		switch key {
		case "esc":
			p.reviewed = nil
			p.scroll = 0
		case "a":
			if p.err == "" {
				return m, m.improvementRequest("apply", improvementRPC("knowledgeDecideImprovement", p.reviewed.Request))
			}
		default:
			p.scrollKey(key, m.height)
		}
		return m, nil
	}
	if p.editor != nil {
		f := p.editor
		if f.editing {
			f.editList(k)
			return m, nil
		}
		switch key {
		case "esc":
			if f.dirty() {
				p.discard = true
			} else {
				closeForm()
			}
		case "up", "shift+tab":
			f.cursor = max(0, f.cursor-1)
		case "down", "tab":
			f.cursor = min(len(f.fields)-1, f.cursor+1)
		case "enter":
			fld := f.cur()
			if fld.kind == sfList {
				f.openList(fld)
			} else if fld.kind == sfAction {
				if p.mode == "scope" {
					p.scope.GoalID = strings.TrimSpace(f.field("goal").text)
					p.rows = nil
					p.after = ""
					p.loaded = false
					p.total = 0
					p.record = nil
					p.proposal = nil
					p.history = nil
					p.sources = nil
					p.files = nil
					return m, m.improvementCatalog(false)
				}
				if err := p.review(); err != nil {
					p.err = err.Error()
				}
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
	if key == "f" {
		if p.record != nil && p.mode != "catalog" {
			return m, m.inspectImprovement(*p.record)
		}
		return m, m.improvementCatalog(false)
	}
	switch p.mode {
	case "catalog", "scope":
		switch key {
		case "esc":
			m.knowledge.gen++
			m.knowledge.improvements = nil
		case "g":
			p.mode = "scope"
			p.decision = ""
			p.editor = &settingsForm{fields: []setField{{key: "goal", label: "Goal ID (optional)", kind: sfText, text: p.scope.GoalID, help: "Blank for project-wide proposals; exact goal ID for a goal."}, {key: "load", label: "Load scope", kind: sfAction, help: "Read proposals and retained decisions for this scope."}}}
			p.editor.rebase()
		case "n":
			return m, m.improvementCatalog(true)
		case "enter":
			if len(p.rows) > 0 {
				return m, m.inspectImprovement(p.rows[p.selected])
			}
		default:
			p.selectKey(key, len(p.rows), m.height)
		}
	case "detail":
		switch key {
		case "esc":
			p.mode = "catalog"
			p.scroll = 0
			p.selected = 0
		case "h":
			p.mode = "history"
			p.scroll = 0
			p.selected = 0
		case "s":
			if p.proposal != nil {
				return m, m.improvementSources(p.proposal.Revision, false)
			}
		case "e":
			if p.proposal != nil && p.err == "" {
				p.files = nil
				return m, m.improvementRequest("export", improvementRPC("knowledgeExportImprovement", map[string]any{"scope": p.scope, "id": p.proposal.ID, "revision": p.proposal.Revision}))
			}
		case "t":
			p.choose("tracked")
		case "d":
			p.choose("dismissed")
		case "r":
			p.choose("reopened")
		default:
			p.scrollKey(key, m.height)
		}
	case "history":
		switch key {
		case "esc":
			p.mode = "detail"
			p.scroll = 0
		case "enter":
			if len(p.history) > 0 {
				return m, m.improvementSources(p.history[p.selected].Revision, false)
			}
		default:
			p.selectKey(key, len(p.history), m.height)
		}
	case "sources":
		switch key {
		case "esc":
			p.mode = "detail"
			p.scroll = 0
		case "n":
			if p.sources != nil {
				return m, m.improvementSources(p.sources.Revision, true)
			}
		case "enter":
			if p.sources != nil && len(p.sources.References) > 0 {
				ref := p.sources.References[p.selected]
				return m, m.improvementRequest("lesson", improvementRPC("knowledgeVersion", map[string]any{"scope": p.scope, "id": ref.ID, "version": ref.Version}))
			}
		default:
			if p.sources != nil {
				p.selectKey(key, len(p.sources.References), m.height)
			}
		}
	case "lesson", "export":
		if key == "v" && p.mode == "lesson" {
			p.raw = !p.raw
			p.scroll = 0
			return m, nil
		}
		if key == "esc" {
			previous := p.mode
			p.mode = "detail"
			if previous == "lesson" {
				p.mode = "sources"
			}
			p.scroll = 0
		} else {
			p.scrollKey(key, m.height)
		}
	}
	return m, nil
}
func (p *improvementModel) scrollKey(key string, height int) {
	v := knowledgeModel{scroll: p.scroll}
	v.scrollKey(key, height)
	p.scroll = v.scroll
}
func (p *improvementModel) selectKey(key string, count, height int) {
	switch key {
	case "up", "k":
		p.selected = max(0, p.selected-1)
		p.follow = true
	case "down", "j":
		p.selected = min(max(0, count-1), p.selected+1)
		p.follow = true
	default:
		p.scrollKey(key, height)
	}
}
func improvementDescription(p *knowledge.Improvement) []string {
	out := []string{p.Topic, p.Problem, fmt.Sprintf("%s · %s · %d distinct reporting sources", p.State, p.Trigger, p.Occurrences), "Repetition is not independent proof.", "Paths: " + strings.Join(p.Scope.Paths, ", "), ""}
	if p.Trigger == "policy" {
		out = append(out, "Policy proposal: normal code review is required; triage never installs rules.")
	}
	for _, s := range p.Suggestions {
		out = append(out, s.Kind+": "+s.Change, "Validation: "+s.Validation, "")
	}
	if p.OmittedSuggestions > 0 {
		out = append(out, fmt.Sprintf("%d more suggestions; inspect exact source lessons.", p.OmittedSuggestions))
	}
	return append(out, "Proposal: "+p.ID, "Revision: "+p.Revision)
}
func (m *rootModel) improvementLines() []string {
	p := m.knowledge.improvements
	w, h := max(30, m.width), max(12, m.height)
	wrap := func(lines []string) []string {
		var out []string
		for _, s := range lines {
			for _, line := range strings.Split(s, "\n") {
				out = append(out, strings.Split(lipgloss.NewStyle().Width(w).Render(goalText(line)), "\n")...)
			}
		}
		return out
	}
	scope := "Project scope"
	if p.scope.GoalID != "" {
		scope = "Goal: " + p.scope.GoalID
	}
	out := []string{m.vitalsBar(w), previewLine(boxTitleHi.Render("lola ▸ "+m.projLabel(p.scope.Project)+" ▸ Improvements"), w), ""}
	body := []string{scope}
	footer := []string{"↑↓/pgup/pgdown scroll · f reload · esc back"}
	anchors := []int{}
	row := func(i int, text string) {
		anchors = append(anchors, len(wrap(body)))
		mark := "  "
		if i == p.selected {
			mark = "> "
		}
		body = append(body, mark+text)
	}
	switch {
	case p.reviewed != nil:
		body = append(body, improvementLabels[p.reviewed.Request.Decision]+" reviewed proposal", "")
		body = append(body, improvementDescription(&p.reviewed.Proposal)...)
		body = append(body, "", "Decision evidence: "+p.reviewed.Request.Evidence, "Triage records a decision only; no lesson activation, agent, issue or rule installation.")
		footer = []string{"↑↓/pgup/pgdown scroll · a confirm · esc edit"}
	case p.editor != nil:
		if p.mode == "scope" {
			body = append(body, "Read saved proposals for a project or one exact goal.")
		} else {
			body = append(body, "Review before saving; implementation follows ordinary task/code review.")
		}
		if p.decision != "" && p.proposal != nil {
			body = append(body, improvementLabels[p.decision]+": "+p.proposal.Topic)
		}
		footer = []string{p.editor.cur().help, "↑↓/tab field · enter edit/action · esc back"}
		if p.editor.editing {
			footer = []string{"Editing " + p.editor.cur().label, "↑↓ line · enter new line · esc done"}
		}
	default:
		switch p.mode {
		case "catalog", "scope":
			body = append(body, fmt.Sprintf("%d of %d proposals; withdrawn decisions remain inspectable.", len(p.rows), p.total), "")
			for i, r := range p.rows {
				state := r.State
				if !r.Available {
					state = "withdrawn; history retained"
				}
				row(i, r.Topic+" · "+state)
			}
			if p.loaded && len(p.rows) == 0 {
				body = append(body, "No proposals or retained decisions in this scope.")
			}
			footer = []string{"↑↓ select · enter inspect · g scope · n more", "f reload · esc lessons"}
		case "detail":
			if p.proposal != nil {
				body = append(body, improvementDescription(p.proposal)...)
				footer = []string{"t track · d dismiss · r reopen · e export", "s sources · h history · f reload · esc catalog", "↑↓/pgup/pgdown scroll"}
			} else if p.record != nil {
				body = append(body, p.record.Topic, "Withdrawn: current sources no longer support this proposal.", "Retained decisions and their sources remain inspectable; triage/export unavailable.")
				footer = []string{"h history · f reload · esc catalog", "↑↓/pgup/pgdown scroll"}
			}
		case "history":
			body = append(body, "Retained decisions — Enter inspects their exact sources.", "")
			for i, d := range p.history {
				row(i, d.Decision+" · "+d.At.Format(time.RFC3339))
				body = append(body, d.Evidence, "Revision: "+d.Revision, "")
			}
			if len(p.history) == 0 {
				body = append(body, "No decisions recorded.")
			}
			footer = []string{"↑↓ select · enter sources · pgup/pgdown scroll", "f reload · esc proposal"}
		case "sources":
			if p.sources != nil {
				body = append(body, "Exact sources: "+p.sources.Revision, fmt.Sprintf("%d of %d loaded", len(p.sources.References), p.sources.Total))
				for i, r := range p.sources.References {
					row(i, fmt.Sprintf("%s · v%d", r.ID, r.Version))
				}
			}
			footer = []string{"↑↓ select · enter lesson · n more sources", "f reload · esc proposal"}
		case "lesson":
			body = append(body, "Recorded source version; current eligibility may differ.")
			if p.raw {
				b, _ := json.MarshalIndent(p.lesson, "", "  ")
				body = append(body, string(b))
			} else if p.lesson != nil {
				body = append(body, lessonDescription(p.lesson)...)
			}
			footer = []string{"v full provenance · f reload · esc sources", "↑↓/pgup/pgdown scroll"}
		case "export":
			body = append(body, "Read-only preview. No files written or rules installed.", "Copy reviewed content into an ordinary task branch for code review.")
			names := []string{}
			for name := range p.files {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				body = append(body, "", name, p.files[name])
			}
		}
	}
	if p.busy {
		footer = []string{"Waiting for daemon; no repeated submission."}
	}
	if p.uncertain {
		footer = []string{"Locked · f reload · esc lessons", "Do not repeat this decision."}
	}
	if p.discard {
		footer = []string{"Unsaved decision · y discard · n keep editing"}
	}
	if p.notice != "" {
		footer = append([]string{p.notice}, footer...)
	}
	if p.err != "" {
		footer = append([]string{"Error: " + p.err}, footer...)
	}
	footer = wrap(footer)
	if len(footer) > h-5 {
		footer = append(footer[:h-7], footer[len(footer)-2:]...)
	}
	avail := max(1, h-len(out)-len(footer)-1)
	if p.editor != nil && p.reviewed == nil {
		intro := wrap(body)
		region, at := p.editor.fieldRegion()
		lines := []string{}
		translated := make([]int, len(at))
		for i, line := range region {
			for j, a := range at {
				if a == i {
					translated[j] = len(lines)
				}
			}
			lines = append(lines, wrap([]string{line})...)
		}
		// Keep the selected field reachable even when a long title consumes the intro.
		if len(intro) > avail-1 {
			intro = intro[:max(0, avail-1)]
		}
		body = append(intro, p.editor.window(lines, translated, max(1, avail-len(intro)))...)
	} else {
		body = wrap(body)
		if p.follow && p.selected < len(anchors) {
			p.follow = false
			anchor := anchors[p.selected]
			if anchor < p.scroll {
				p.scroll = anchor
			}
			if anchor >= p.scroll+avail {
				p.scroll = anchor - avail + 1
			}
		}
		p.scroll = min(p.scroll, max(0, len(body)-avail))
		body = body[p.scroll:min(len(body), p.scroll+avail)]
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
