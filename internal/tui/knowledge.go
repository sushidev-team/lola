package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

type knowledgeModel struct {
	improvements                                 *improvementModel
	project                                      string
	gen, seq, selected, scroll                   int
	rows                                         []knowledge.Lesson
	loaded, busy, detail, raw, filtering         bool
	filter, err, notice, mode                    string
	editor                                       *settingsForm
	subject                                      *knowledge.Lesson
	conflicts                                    []knowledge.Lesson
	capture                                      *knowledge.CaptureRequest
	decision                                     *knowledge.Acceptance
	correction, acknowledged, uncertain, discard bool
	export                                       map[string]string
}
type knowledgeMsg struct {
	project, op string
	gen, seq    int
	data        json.RawMessage
	err         string
}

func (m *rootModel) enterKnowledge(project string) (tea.Model, tea.Cmd) {
	m.knowledge = knowledgeModel{project: project, gen: m.knowledge.gen + 1}
	m.view = viewKnowledge
	return m, m.knowledgeRequest("list", "knowledgeList", map[string]any{"scope": knowledge.Scope{Project: project}})
}

// Freeze arguments before dispatch; late replies cannot change another view.
func (m *rootModel) knowledgeRequest(op, command string, args any) tea.Cmd {
	p := &m.knowledge
	if p.busy {
		return nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		p.err = err.Error()
		return nil
	}
	p.seq++
	p.busy = true
	p.err = ""
	project, gen, seq := p.project, p.gen, p.seq
	return func() tea.Msg {
		msg := knowledgeMsg{project: project, gen: gen, seq: seq, op: op}
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
func (m *rootModel) refreshKnowledge() tea.Cmd {
	p := &m.knowledge
	if p.editor != nil {
		return nil
	}
	return m.knowledgeRequest("list", "knowledgeList", map[string]any{"scope": knowledge.Scope{Project: p.project}})
}
func (m *rootModel) handleKnowledgeMsg(msg knowledgeMsg) tea.Cmd {
	p := &m.knowledge
	if m.view != viewKnowledge || p.improvements != nil || msg.project != p.project || msg.gen != p.gen || msg.seq != p.seq {
		return nil
	}
	p.busy = false
	mutation := msg.op == "capture" || msg.op == "accept" || msg.op == "dispute"
	fail := func(err string) {
		p.err = err
		if mutation {
			p.uncertain = true
			p.acknowledged = false
			p.err = "Result not confirmed: " + err + ". It may already be saved. Close and refresh lessons before another decision."
		}
	}
	if msg.err != "" {
		fail(msg.err)
		return nil
	}
	switch msg.op {
	case "list":
		var rows []knowledge.Lesson
		if err := json.Unmarshal(msg.data, &rows); err != nil {
			fail(err.Error())
			return nil
		}
		for _, l := range rows {
			if l.Scope.Project != p.project {
				fail("Lesson list does not match this project")
				return nil
			}
		}
		id := ""
		if l := p.current(); l != nil {
			id = l.ID
		}
		p.rows = rows
		p.loaded = true
		p.selected = 0
		for i, l := range p.visible() {
			if l.ID == id {
				p.selected = i
			}
		}
		if p.current() == nil {
			p.detail = false
		}
	case "export":
		var files map[string]string
		if err := json.Unmarshal(msg.data, &files); err != nil {
			fail(err.Error())
			return nil
		}
		p.export = files
		p.scroll = 0
		if len(files) == 0 {
			p.notice = "No export content is available."
		}
	case "capture", "accept":
		var l knowledge.Lesson
		if err := json.Unmarshal(msg.data, &l); err != nil {
			fail(err.Error())
			return nil
		}
		if l.ID == "" || l.Version < 1 || l.Scope.Project != p.project || (msg.op == "capture" && (p.capture == nil || l.Source.ID != p.capture.CaptureID)) || (msg.op == "accept" && (p.decision == nil || l.ID != p.decision.ID)) {
			fail("Reply does not identify the reviewed lesson")
			return nil
		}
		p.notice = fmt.Sprintf("Saved as %s, version %d.", l.Status, l.Version)
		p.closeForm()
		return m.refreshKnowledge()
	case "dispute":
		p.notice = "Lesson disputed; excluded from future guidance."
		p.closeForm()
		return m.refreshKnowledge()
	}
	return nil
}
func (p *knowledgeModel) visible() []knowledge.Lesson {
	var out []knowledge.Lesson
	for _, l := range p.rows {
		text := l.Topic + " " + l.Summary + " " + string(l.Status) + " " + strings.Join(l.Tags, " ") + " " + strings.Join(l.Paths, " ")
		if strings.Contains(strings.ToLower(text), strings.ToLower(p.filter)) {
			out = append(out, l)
		}
	}
	return out
}
func (p *knowledgeModel) current() *knowledge.Lesson {
	rows := p.visible()
	if p.selected < 0 || p.selected >= len(rows) {
		return nil
	}
	return &rows[p.selected]
}
func (p *knowledgeModel) closeForm() {
	p.mode = ""
	p.editor = nil
	p.subject = nil
	p.capture = nil
	p.decision = nil
	p.conflicts = nil
	p.acknowledged = false
	p.uncertain = false
	p.discard = false
	p.raw = false
	p.scroll = 0
	p.err = ""
}
func (p *knowledgeModel) openForm(mode string) {
	l := p.current()
	if mode != "capture" && (l == nil || l.Status == knowledge.Superseded || (mode == "accept" && l.Policy)) {
		p.err = "This lesson cannot be used for that decision."
		return
	}
	p.closeForm()
	p.mode = mode
	p.notice = ""
	p.export = nil
	f := &settingsForm{}
	p.editor = f
	if mode == "capture" {
		f.fields = []setField{
			{key: "topic", label: "Topic", kind: sfText, help: "Short name for this reusable lesson."},
			{key: "summary", label: "Lesson", kind: sfList, help: "8–2000 characters. Enter edits; Escape returns to fields."},
			{key: "rationale", label: "Why it matters", kind: sfList, help: "Optional rationale. Keep credentials and private user data out of lessons."},
			{key: "paths", label: "Applicable path prefixes", kind: sfList, help: "One repository-relative prefix per line. Blank means all paths."},
			{key: "tags", label: "Tags", kind: sfList, help: "One search tag per line."},
			{key: "goal", label: "Goal ID (optional)", kind: sfText, help: "Blank scopes the lesson to this project."},
			{key: "correction", label: "Explicit correction", kind: sfBool, help: "Activates when eligible; conflicting or mandatory policy changes stay pending."},
			{key: "policy", label: "Policy proposal", kind: sfBool, help: "Keep as a proposal for normal code review; never activate here."},
			{key: "prevention", label: "Proposed prevention", kind: sfEnum, options: []string{"none", "regression_test", "lint", "skill"}, text: "none", help: "Optional prevention idea, not an executable rule."},
			{key: "change", label: "Proposed change", kind: sfList, help: "Required when proposing prevention; at least 8 characters."},
			{key: "validation", label: "How to verify prevention", kind: sfList, help: "Required when proposing prevention; at least 8 characters."},
			{key: "evidence", label: "Evidence references", kind: sfList, help: "One reference per line. Worker assertions do not establish verification."},
		}
	} else {
		// Copy the reviewed subject; a later list response must never replace it.
		b, _ := json.Marshal(l)
		_ = json.Unmarshal(b, &p.subject)
		f.fields = []setField{{key: "evidence", label: "Decision evidence", kind: sfList, help: "Explain verification or the dispute, at least 8 characters."}}
		if mode == "accept" {
			var watch []string
			for _, r := range l.Code {
				watch = append(watch, r.Member+":"+r.Path)
			}
			expiry := ""
			if !l.ExpiresAt.IsZero() {
				expiry = l.ExpiresAt.Format(time.RFC3339Nano)
			}
			f.fields = append(f.fields, setField{key: "watch", label: "Watch committed code", kind: sfList, lines: watch, help: "member:path per line. Reads the default branch; existing references must be retained or replaced."}, setField{key: "expires", label: "Expiry (RFC3339)", kind: sfText, text: expiry, help: "Optional timestamp with timezone. Clearing explicitly removes the expiry."})
			for _, other := range p.rows {
				if other.ID != l.ID && other.Status == knowledge.Active && other.Scope.GoalID == l.Scope.GoalID && other.Topic == l.Topic && slices.Equal(other.Paths, l.Paths) && other.Summary != l.Summary {
					p.conflicts = append(p.conflicts, other)
					f.fields = append(f.fields, setField{key: "replace." + other.ID, label: fmt.Sprintf("Replace lesson %d", len(p.conflicts)), kind: sfBool, help: fmt.Sprintf("%s · v%d: %s", other.ID, other.Version, other.Summary), section: "Conflicting guidance"})
				}
			}
		}
	}
	f.fields = append(f.fields, setField{key: "review", label: "Review decision", kind: sfAction, help: "Review scope, evidence and consequences before saving."})
	f.rebase()
}
func learningLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func (p *knowledgeModel) review() error {
	f := p.editor
	p.capture = nil
	p.decision = nil
	p.acknowledged = false
	p.raw = false
	p.scroll = 0
	prose := func(key string) string { return strings.TrimSpace(strings.Join(f.field(key).lines, "\n")) }
	if p.mode == "capture" {
		a := knowledge.CaptureRequest{Scope: knowledge.Scope{Project: p.project, GoalID: strings.TrimSpace(f.field("goal").text)}, CaptureID: goal.NewID(), Proposal: knowledge.Proposal{Topic: strings.TrimSpace(f.field("topic").text), Summary: prose("summary"), Rationale: prose("rationale"), Paths: learningLines(f.field("paths").lines), Tags: learningLines(f.field("tags").lines), Evidence: learningLines(f.field("evidence").lines), Policy: f.field("policy").b}}
		if kind := f.field("prevention").text; kind != "none" {
			a.Proposal.Prevention = &knowledge.PreventionIdea{Kind: kind, Change: prose("change"), Validation: prose("validation")}
		}
		p.correction = f.field("correction").b
		kind := "operator"
		if p.correction {
			kind = "correction"
		}
		if err := knowledge.Validate(a.Scope, a.Proposal, knowledge.Source{Kind: kind, ID: a.CaptureID}); err != nil {
			return err
		}
		p.capture = &a
		return nil
	}
	if p.subject == nil {
		return errors.New("Select a lesson first")
	}
	a := knowledge.Acceptance{Scope: p.subject.Scope, ID: p.subject.ID, Version: p.subject.Version, Evidence: prose("evidence")}
	limit := 4000
	if p.mode == "dispute" {
		limit = 2000
	}
	if len(a.Evidence) < 8 || len(a.Evidence) > limit {
		return fmt.Errorf("Evidence must contain 8–%d bytes", limit)
	}
	if p.mode == "accept" {
		for _, line := range learningLines(f.field("watch").lines) {
			member, path, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(member) == "" || strings.TrimSpace(path) == "" {
				return errors.New("Code references use member:path, one per line")
			}
			a.CodeTargets = append(a.CodeTargets, knowledge.CodeTarget{Member: strings.TrimSpace(member), Path: strings.TrimSpace(path)})
		}
		if len(p.subject.Code) > 0 && len(a.CodeTargets) == 0 {
			return errors.New("Retain or replace existing committed code references")
		}
		if expiry := strings.TrimSpace(f.field("expires").text); expiry != "" {
			var err error
			a.Expires, err = time.Parse(time.RFC3339Nano, expiry)
			if err != nil {
				return errors.New("Expiry needs RFC3339 time and timezone, for example 2027-01-01T12:00:00Z")
			}
		}
		for _, l := range p.conflicts {
			if f.field("replace." + l.ID).b {
				a.Supersedes = append(a.Supersedes, l.ID)
			}
		}
	}
	p.decision = &a
	return nil
}
func (m *rootModel) updateKnowledge(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.knowledge.improvements != nil {
		return m.updateImprovements(msg)
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	key := k.String()
	p := &m.knowledge
	if p.busy {
		return m, nil
	}
	close := func() (tea.Model, tea.Cmd) { p.closeForm(); return m, m.refreshKnowledge() }
	if p.discard {
		p.discard = false
		if key == "y" || key == "Y" {
			return close()
		}
		return m, nil
	}
	if p.uncertain {
		if key == "esc" {
			return close()
		}
		return m, nil
	}
	if key == "ctrl+c" {
		if p.editor != nil {
			p.discard = true
		}
		return m, nil
	}
	if p.capture != nil || p.decision != nil {
		switch key {
		case "esc":
			p.capture = nil
			p.decision = nil
			p.acknowledged = false
			p.raw = false
			p.scroll = 0
		case "space":
			p.acknowledged = !p.acknowledged
		case "a":
			if p.acknowledged {
				if p.capture != nil {
					command := "knowledgeCapture"
					if p.correction {
						command = "knowledgeCorrect"
					}
					return m, m.knowledgeRequest("capture", command, *p.capture)
				}
				command := "knowledgeAccept"
				if p.mode == "dispute" {
					command = "knowledgeDispute"
				}
				return m, m.knowledgeRequest(p.mode, command, *p.decision)
			}
		case "v":
			p.raw = !p.raw
			p.scroll = 0
		default:
			p.scrollKey(key, m.height)
		}
		return m, nil
	}
	if p.editor != nil {
		f := p.editor
		if f.editing {
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
		case "enter", "space":
			fld := f.cur()
			switch fld.kind {
			case sfList:
				if key == "enter" {
					f.openList(fld)
				}
			case sfBool:
				fld.b = !fld.b
			case sfEnum:
				cycleEnum(fld)
			case sfAction:
				if key == "enter" {
					p.err = ""
					if err := p.review(); err != nil {
						p.err = err.Error()
					}
				}
			case sfText:
				if key == "space" {
					fld.text += " "
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
	if p.filtering {
		switch key {
		case "esc", "enter":
			p.filtering = false
		case "backspace":
			p.filter = dropLastRune(p.filter)
		default:
			p.filter += k.Text
		}
		p.selected = 0
		p.scroll = 0
		return m, nil
	}
	if len(p.export) > 0 {
		if key == "esc" {
			p.export = nil
			p.scroll = 0
		} else {
			p.scrollKey(key, m.height)
		}
		return m, nil
	}
	if p.detail {
		switch key {
		case "esc":
			p.detail = false
			p.raw = false
			p.scroll = 0
		case "a":
			if p.err == "" {
				p.openForm("accept")
			}
		case "d":
			if p.err == "" {
				p.openForm("dispute")
			}
		case "v":
			p.raw = !p.raw
			p.scroll = 0
		case "f":
			return m, m.refreshKnowledge()
		default:
			p.scrollKey(key, m.height)
		}
		return m, nil
	}
	switch key {
	case "esc":
		p.gen++
		m.view = viewDetail
	case "f":
		return m, m.refreshKnowledge()
	case "/":
		p.filtering = true
	case "n":
		p.openForm("capture")
	case "i":
		return m, m.openImprovements()
	case "e":
		return m, m.knowledgeRequest("export", "knowledgeExport", map[string]any{"scope": knowledge.Scope{Project: p.project}})
	case "up", "k":
		p.selected = max(0, p.selected-1)
	case "down", "j":
		p.selected = min(max(0, len(p.visible())-1), p.selected+1)
	case "enter":
		if p.current() != nil {
			p.detail = true
			p.scroll = 0
			p.raw = false
		}
	}
	return m, nil
}
func (p *knowledgeModel) scrollKey(key string, height int) {
	switch key {
	case "up", "k":
		p.scroll = max(0, p.scroll-1)
	case "down", "j":
		p.scroll++
	case "pgup":
		p.scroll = max(0, p.scroll-max(1, height/2))
	case "pgdown":
		p.scroll += max(1, height/2)
	case "home":
		p.scroll = 0
	case "end":
		p.scroll = 1 << 20
	}
}
func lessonDescription(l *knowledge.Lesson) []string {
	scope := "Project scope"
	if l.Scope.GoalID != "" {
		scope = "Goal " + l.Scope.GoalID
	}
	expiry := "No expiry"
	if !l.ExpiresAt.IsZero() {
		expiry = l.ExpiresAt.Format(time.RFC3339Nano)
	}
	paths := strings.Join(l.Paths, ", ")
	if paths == "" {
		paths = "All paths"
	}
	verification := l.Verification
	if verification == "" {
		verification = "Not verified"
	}
	out := []string{fmt.Sprintf("%s · %s · version %d", l.Topic, l.Status, l.Version), l.Summary, l.Rationale, "", scope + " · " + paths, "Source: " + l.Source.Kind + " · " + l.Source.ID, "Verification: " + verification, "Expiry: " + expiry}
	if l.Policy {
		out = append(out, "Mandatory policy proposal; cannot activate here.")
	}
	if l.ReviewReason != "" {
		out = append(out, "Needs review: "+l.ReviewReason)
	}
	out = append(out, "Evidence:")
	out = append(out, l.Evidence...)
	for _, r := range l.Code {
		out = append(out, "Watch: "+r.Member+":"+r.Path)
	}
	if len(l.Code) == 0 {
		out = append(out, "No committed code references tracked.")
	}
	return out
}
func (p *knowledgeModel) reviewLines() []string {
	if p.raw {
		var v any = p.decision
		if p.capture != nil {
			v = p.capture
		}
		b, _ := json.MarshalIndent(v, "", "  ")
		return strings.Split(string(b), "\n")
	}
	if a := p.capture; a != nil {
		consequence := "Discovery remains a candidate until verified and accepted."
		if p.correction {
			consequence = "Explicit correction activates unless conflicting or a mandatory policy proposal."
		}
		out := []string{"Record lesson", consequence, "Project: " + a.Scope.Project, "Goal: " + a.Scope.GoalID, a.Proposal.Topic, a.Proposal.Summary, a.Proposal.Rationale, "Paths: " + strings.Join(a.Proposal.Paths, ", "), "Tags: " + strings.Join(a.Proposal.Tags, ", "), fmt.Sprintf("Mandatory policy proposal: %t", a.Proposal.Policy), "Evidence:"}
		out = append(out, a.Proposal.Evidence...)
		if idea := a.Proposal.Prevention; idea != nil {
			out = append(out, "Prevention: "+idea.Kind, idea.Change, "Validation: "+idea.Validation)
		}
		return append(out, "Capture reference: "+a.CaptureID)
	}
	out := lessonDescription(p.subject)
	a := p.decision
	if p.mode == "dispute" {
		return append(out, "", "Dispute: exclude from future guidance and retain history.", "Decision evidence: "+a.Evidence)
	}
	out = append(out, "", "Accept: activate this exact lesson version.", "Decision evidence: "+a.Evidence)
	expiry := "No expiry"
	if !a.Expires.IsZero() {
		expiry = a.Expires.Format(time.RFC3339Nano)
	}
	out = append(out, "New expiry: "+expiry, "Supersedes: "+strings.Join(a.Supersedes, ", "))
	for _, r := range a.CodeTargets {
		out = append(out, "Watch committed code: "+r.Member+":"+r.Path)
	}
	if len(a.CodeTargets) == 0 {
		out = append(out, "No code references; code changes will not automatically invalidate this lesson.")
	}
	return out
}
func (m *rootModel) knowledgeLines() []string {
	if m.knowledge.improvements != nil {
		return m.improvementLines()
	}
	p := &m.knowledge
	w, h := max(30, m.width), max(12, m.height)
	wrap := func(lines []string) []string {
		var out []string
		for _, line := range lines {
			for _, part := range strings.Split(line, "\n") {
				out = append(out, strings.Split(lipgloss.NewStyle().Width(w).Render(goalText(part)), "\n")...)
			}
		}
		return out
	}
	out := []string{m.vitalsBar(w), boxTitleHi.Render("lola ▸ " + m.projLabel(p.project) + " ▸ Workspace learning"), ""}
	var body, footer []string
	reviewed := p.capture != nil || p.decision != nil
	switch {
	case reviewed:
		body = p.reviewLines()
		ack := "[ ]"
		if p.acknowledged {
			ack = "[x]"
		}
		footer = []string{ack + " I reviewed this lesson, scope, evidence and decision.", "space acknowledge · a confirm decision · esc back to edit", "↑↓/pgup/pgdown scroll · v full decision"}
	case p.editor != nil:
		body = []string{"Record a discovery or curate verified guidance; review before saving."}
		if p.subject != nil {
			body = []string{fmt.Sprintf("%s %s · version %d", p.mode, p.subject.Topic, p.subject.Version)}
		}
		footer = []string{p.editor.cur().help, "↑↓/tab field · enter edit/toggle/action · esc close form"}
		if p.editor.editing {
			footer = []string{"Editing " + p.editor.cur().label, "↑↓ line · enter new line · esc done"}
		}
	case len(p.export) > 0:
		body = []string{"Reviewable export — no repository files written.", "Copy reviewed content into a branch for normal code review.", ""}
		var names []string
		for name := range p.export {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			body = append(body, name)
			body = append(body, strings.Split(p.export[name], "\n")...)
			body = append(body, "")
		}
		footer = []string{"↑↓/pgup/pgdown scroll · esc lessons"}
	case p.detail && p.current() != nil:
		l := p.current()
		body = lessonDescription(l)
		if p.raw {
			b, _ := json.MarshalIndent(l, "", "  ")
			body = strings.Split(string(b), "\n")
		}
		actions := "d dispute"
		if !l.Policy && l.Status != knowledge.Superseded {
			actions = "a accept · " + actions
		}
		if l.Status == knowledge.Superseded {
			actions = "Superseded lesson; read only"
		}
		footer = []string{actions + " · v full provenance · f refresh", "↑↓/pgup/pgdown scroll · esc lessons"}
	default:
		body = []string{"Candidates need verification; corrections may activate within their scope.", ""}
		rows := p.visible()
		for i, l := range rows {
			mark := "  "
			if i == p.selected {
				mark = "> "
			}
			body = append(body, fmt.Sprintf("%s%s · %s · v%d", mark, l.Topic, l.Status, l.Version))
		}
		if p.loaded && len(rows) == 0 {
			body = append(body, "No lessons match. Record a lesson or clear the search.")
		}
		footer = []string{"↑↓ select · enter inspect · n record lesson · / search", "i improvements · e export · f refresh · esc project"}
		if p.filter != "" || p.filtering {
			footer = append([]string{"Search: " + p.filter}, footer...)
		}
		if p.filtering {
			footer = append(footer, "Type to search · enter/esc done")
		}
	}
	if p.busy {
		footer = []string{"Waiting for the daemon. No repeated submission."}
	}
	if p.uncertain {
		footer = []string{"esc close and refresh lessons; do not repeat this decision"}
	}
	if p.discard {
		footer = []string{"Unsaved learning decision — y discard · n keep editing"}
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
	if p.editor != nil && !reviewed {
		intro := wrap(body)
		region, anchors := p.editor.fieldRegion()
		var lines []string
		translated := make([]int, len(anchors))
		for i, line := range region {
			for j, a := range anchors {
				if a == i {
					translated[j] = len(lines)
				}
			}
			lines = append(lines, wrap([]string{line})...)
		}
		body = append(intro, p.editor.window(lines, translated, max(1, avail-len(intro)))...)
	} else {
		body = wrap(body)
		if !p.detail && !reviewed && len(p.export) == 0 { // Keep the selected wrapped row in view.
			prefix := wrap([]string{"Candidates need verification; corrections may activate within their scope.", ""})
			anchor := len(prefix)
			for i, l := range p.visible() {
				if i >= p.selected {
					break
				}
				anchor += len(wrap([]string{fmt.Sprintf("  %s · %s · v%d", l.Topic, l.Status, l.Version)}))
			}
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
