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
	"github.com/sushidev-team/lola/internal/protocol"
)

type goalRecoveryModel struct {
	id                                string
	seq, selected, scroll             int
	busy, uncertain, discard, inspect bool
	err, notice, action               string
	snapshot                          *goal.RecoveryView
	editor                            *settingsForm
	reviewed                          *goalRecoveryDecision
}
type goalRecoveryDecision struct {
	snapshot            goal.RecoveryView
	action, reason, url string
	contract            goal.BriefContract
}
type goalRecoveryMsg struct {
	project, id, op string
	gen, seq        int
	data            json.RawMessage
	err             string
}

var recoveryLabels = map[string]string{
	"sync-brief": "Refresh Linear brief", "accept-brief": "Reconcile brief",
	"dismiss-progress": "Dismiss progress update", "delivery-evidence": "Attach evidence URL",
	"retry-verification": "Retry verification", "retry-replan": "Retry replanning",
}
var recoveryConsequences = map[string]string{
	"sync-brief":         "Read the current parent issue from Linear and retain any changed brief for review. This may hold execution; it does not accept the change or overwrite Linear.",
	"accept-brief":       "Accept this clarification against the retained task plan. Tasks, permissions and budgets stay fixed. Previous whole-goal verification is archived; authorized work remains paused until you resume it separately.",
	"dismiss-progress":   "Retain this update and your reason in history, allowing later progress updates. This neither deletes the Linear comment nor claims it was delivered, and does not resend this update.",
	"delivery-evidence":  "Attach a shared HTTPS artifact link before final PR descriptions are reserved. This does not upload files or establish verification. Published descriptions cannot be replaced here.",
	"retry-verification": "Start a fresh verification round within the accepted budgets. Earlier checks and reviews remain in history. This may run checks and paid specialist calls; the daemon first reconciles any unfinished work.",
	"retry-replan":       "Start a fresh replanning round within the accepted budgets. Earlier plans and work remain in history. This may use a paid coordinator call; the daemon first reconciles any unfinished work.",
}

func (m *rootModel) openGoalRecovery() tea.Cmd {
	g := m.goals.current()
	if g == nil {
		return nil
	}
	m.goals.gen++
	m.goals.loading = false
	m.goals.recovery = &goalRecoveryModel{id: g.ID}
	return m.goalRecoveryRequest("load", "goalRecovery", map[string]string{"id": g.ID})
}
func (m *rootModel) goalRecoveryRequest(op, command string, args any) tea.Cmd {
	v, p := &m.goals, m.goals.recovery
	raw, err := json.Marshal(args)
	if err != nil {
		p.err = err.Error()
		return nil
	}
	p.seq++
	p.busy = true
	p.err = ""
	project, id, gen, seq := v.project, p.id, v.gen, p.seq
	return func() tea.Msg {
		msg := goalRecoveryMsg{project: project, id: id, gen: gen, seq: seq, op: op}
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
func (m *rootModel) handleGoalRecoveryMsg(msg goalRecoveryMsg) tea.Cmd {
	v, p := &m.goals, m.goals.recovery
	if p == nil || msg.project != v.project || msg.id != p.id || msg.gen != v.gen || msg.seq != p.seq {
		return nil
	}
	p.busy = false
	if msg.err != "" {
		if msg.op == "apply" {
			p.uncertain = true
			p.err = "Action was not confirmed: " + msg.err + ". It may already have happened. Reload the local snapshot; do not repeat this request."
		} else {
			p.err = "Could not load recovery: " + msg.err + ". Reload the local snapshot before another decision."
		}
		return nil
	}
	if msg.op == "apply" {
		p.notice = "Action confirmed. Review the refreshed state before another decision."
		p.uncertain = true
		return m.goalRecoveryRequest("load", "goalRecovery", map[string]string{"id": p.id})
	}
	var snapshot goal.RecoveryView
	if err := json.Unmarshal(msg.data, &snapshot); err != nil || snapshot.Goal.ID != p.id || snapshot.Goal.Project != v.project || snapshot.Goal.Revision == 0 {
		p.err = "Invalid recovery snapshot for this goal. Reload before another decision."
		return nil
	}
	p.snapshot = &snapshot
	p.editor = nil
	p.reviewed = nil
	p.action = ""
	p.uncertain = false
	p.discard = false
	p.inspect = false
	p.scroll = 0
	p.selected = 0
	return nil
}
func (p *goalRecoveryModel) actions() []string {
	if p.snapshot == nil {
		return nil
	}
	var out []string
	for _, a := range p.snapshot.Actions {
		if recoveryLabels[a] == "" {
			continue
		}
		if a == "accept-brief" && p.snapshot.PendingBrief == nil {
			continue
		}
		if a == "dismiss-progress" && p.snapshot.LatestProgress == nil {
			continue
		}
		out = append(out, a)
	}
	return out
}
func (p *goalRecoveryModel) choose(action string) {
	if p.busy || p.uncertain || p.err != "" || !slices.Contains(p.actions(), action) {
		return
	}
	p.action = action
	p.notice = ""
	p.scroll = 0
	f := &settingsForm{}
	p.editor = f
	if action == "accept-brief" {
		b := p.snapshot.Brief
		f.fields = append(f.fields,
			setField{key: "title", label: "Clarified title", kind: sfText, text: b.Title, help: "Required, at most 240 UTF-8 bytes."},
			setField{key: "outcome", label: "Clarified outcome", kind: sfList, lines: strings.Split(b.Outcome, "\n"), help: "Explicit clarified outcome; retained tasks must still cover it."},
			setField{key: "acceptance", label: "Acceptance criteria", kind: sfList, lines: slices.Clone(b.Acceptance), help: "1–50 observable criteria, one per line; at most 2000 bytes each."},
			setField{key: "non_goals", label: "Non-goals", kind: sfList, lines: slices.Clone(b.NonGoals), help: "One non-goal per line, at most 50."})
	}
	if action == "delivery-evidence" {
		f.fields = append(f.fields, setField{key: "url", label: "Shared evidence URL", kind: sfText, text: p.snapshot.DeliveryEvidenceURL, help: "Shared HTTPS artifact link without credentials; no upload or verification."})
	}
	if action != "sync-brief" && action != "delivery-evidence" {
		label, help := "Reason (optional)", "Explain the retry; earlier evidence remains in history."
		if action == "accept-brief" || action == "dismiss-progress" {
			label = "Decision reason (required)"
			help = "Explain this decision, at most 2000 UTF-8 bytes."
		}
		f.fields = append(f.fields, setField{key: "reason", label: label, kind: sfList, help: help})
	}
	f.fields = append(f.fields, setField{key: "review", label: "Review recovery decision", kind: sfAction, help: "Inspect the exact source, revision and consequences before confirmation."})
	f.rebase()
}
func (p *goalRecoveryModel) review() error {
	if p.snapshot == nil || !slices.Contains(p.actions(), p.action) {
		return errors.New("reload the recovery snapshot")
	}
	// Freeze all nested data independently of both the form and cached view.
	raw, err := json.Marshal(p.snapshot)
	if err != nil {
		return err
	}
	d := &goalRecoveryDecision{action: p.action}
	if err = json.Unmarshal(raw, &d.snapshot); err != nil {
		return err
	}
	f := p.editor
	if field := f.field("reason"); field != nil {
		d.reason = strings.TrimSpace(strings.Join(field.lines, "\n"))
	}
	if len(d.reason) > 2000 || ((p.action == "accept-brief" || p.action == "dismiss-progress") && d.reason == "") {
		return errors.New("a decision reason is required for reconciliation/dismissal, at most 2000 UTF-8 bytes")
	}
	if p.action == "accept-brief" {
		d.contract = goal.BriefContract{Title: strings.TrimSpace(f.field("title").text), Outcome: strings.TrimSpace(strings.Join(f.field("outcome").lines, "\n")), Acceptance: trimDropEmpty(f.field("acceptance").lines), NonGoals: trimDropEmpty(f.field("non_goals").lines)}
		c := d.contract
		if c.Title == "" || len(c.Title) > 240 || c.Outcome == "" || len(c.Outcome) > 16000 || goal.Marked(c.Outcome) {
			return errors.New("clarified title/outcome required, within 240/16000 bytes and without reserved outcome markers")
		}
		if len(c.Acceptance) < 1 || len(c.Acceptance) > 50 || len(c.NonGoals) > 50 {
			return errors.New("provide 1–50 acceptance criteria and at most 50 non-goals")
		}
		for _, line := range append(slices.Clone(c.Acceptance), c.NonGoals...) {
			if len(line) > 2000 || goal.Marked(line) {
				return errors.New("each criterion/non-goal must be at most 2000 bytes and have no reserved markers")
			}
		}
	}
	if p.action == "delivery-evidence" {
		d.url = strings.TrimSpace(f.field("url").text)
		if err := goal.ValidateEvidenceURL(d.url); err != nil {
			return err
		}
	}
	p.reviewed = d
	p.err = ""
	p.scroll = 0
	p.inspect = false
	return nil
}
func (m *rootModel) applyGoalRecovery() tea.Cmd {
	p := m.goals.recovery
	if p == nil || p.reviewed == nil || p.busy || p.uncertain || p.err != "" {
		return nil
	}
	d := p.reviewed
	id, revision := d.snapshot.Goal.ID, d.snapshot.Goal.Revision
	switch d.action {
	case "sync-brief":
		return m.goalRecoveryRequest("apply", "goalSyncBrief", map[string]string{"id": id})
	case "accept-brief":
		return m.goalRecoveryRequest("apply", "goalAcceptBrief", goal.BriefAcceptance{ID: id, Revision: revision, SourceID: d.snapshot.PendingBrief.ID, Contract: d.contract, Reason: d.reason})
	case "dismiss-progress":
		return m.goalRecoveryRequest("apply", "goalDismissProgress", goal.ProgressResolution{ID: id, Revision: revision, UpdateID: d.snapshot.LatestProgress.ID, Reason: d.reason})
	case "delivery-evidence":
		return m.goalRecoveryRequest("apply", "goalDeliveryEvidence", goal.DeliveryEvidenceRequest{ID: id, Revision: revision, URL: d.url})
	case "retry-verification", "retry-replan":
		return m.goalRecoveryRequest("apply", "goalControl", goal.ControlRequest{ID: id, Revision: revision, Action: d.action, Reason: d.reason})
	}
	return nil
}
func (m *rootModel) updateGoalRecovery(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.goals.recovery
	key := k.String()
	close := func() (tea.Model, tea.Cmd) { m.goals.recovery = nil; return m, m.refreshGoals() }
	if p.busy || key == "ctrl+c" {
		return m, nil
	}
	if p.discard {
		p.discard = false
		if key == "y" || key == "Y" {
			return close()
		}
		return m, nil
	}
	scroll := func() {
		switch key {
		case "up", "k":
			p.scroll = max(0, p.scroll-1)
		case "down", "j":
			p.scroll++
		case "pgup":
			p.scroll = max(0, p.scroll-max(1, m.height/2))
		case "pgdown":
			p.scroll += max(1, m.height/2)
		case "home":
			p.scroll = 0
		case "end":
			p.scroll = 1 << 20
		}
	}
	if p.uncertain || p.snapshot == nil || (p.err != "" && p.editor == nil) {
		if key == "f" {
			return m, m.goalRecoveryRequest("load", "goalRecovery", map[string]string{"id": p.id})
		}
		if key == "esc" {
			return close()
		}
		scroll()
		return m, nil
	}
	if p.inspect {
		if key == "esc" || key == "v" {
			p.inspect = false
			p.scroll = 0
		} else {
			scroll()
		}
		return m, nil
	}
	if p.reviewed != nil {
		switch key {
		case "esc":
			p.reviewed = nil
			p.scroll = 0
		case "a":
			return m, m.applyGoalRecovery()
		default:
			scroll()
		}
		return m, nil
	}
	if p.editor == nil {
		switch key {
		case "esc":
			return close()
		case "f":
			return m, m.goalRecoveryRequest("load", "goalRecovery", map[string]string{"id": p.id})
		case "tab", "right", "shift+tab", "left":
			if actions := p.actions(); len(actions) > 0 {
				delta := 1
				if key == "left" || key == "shift+tab" {
					delta = -1
				}
				p.selected = (p.selected + len(actions) + delta) % len(actions)
			}
		case "enter":
			if actions := p.actions(); len(actions) > 0 {
				p.choose(actions[p.selected])
			}
		default:
			scroll()
		}
		return m, nil
	}
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
	case "ctrl+v":
		p.inspect = true
		p.scroll = 0
	case "up", "shift+tab":
		f.cursor = max(0, f.cursor-1)
	case "down", "tab":
		f.cursor = min(len(f.fields)-1, f.cursor+1)
	case "enter":
		field := f.cur()
		if field.kind == sfList {
			f.openList(field)
		} else if field.kind == sfAction {
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
func recoveryBriefLines(label string, b goal.BriefContract) []string {
	out := []string{label, b.Title, b.Outcome, "Acceptance criteria:"}
	for _, c := range b.Acceptance {
		out = append(out, "  - "+c)
	}
	if len(b.NonGoals) > 0 {
		out = append(out, "Outside this goal:")
		for _, c := range b.NonGoals {
			out = append(out, "  - "+c)
		}
	}
	return out
}
func recoveryProgressState(u goal.ProgressUpdate) string {
	switch {
	case !u.DismissedAt.IsZero():
		return "Dismissed; retained in history"
	case !u.ConfirmedAt.IsZero():
		return "Delivery confirmed"
	case !u.SentAt.IsZero():
		return "Send reserved; delivery unconfirmed"
	case !u.AttemptedAt.IsZero():
		return "Attempted; delivery unconfirmed"
	default:
		return "Pending"
	}
}
func recoverySourceLines(s goal.RecoveryView) []string {
	var out []string
	if source := s.PendingBrief; source != nil {
		out = append(out, "Exact Linear source", source.Title, source.Description, "Source "+source.ID, "")
	}
	out = append(out, "Retained tasks and acceptance")
	for _, task := range s.Goal.Tasks {
		out = append(out, task.Title)
		for _, c := range task.Acceptance {
			out = append(out, "  - "+c)
		}
	}
	return out
}
func (p *goalRecoveryModel) snapshotLines() []string {
	if p.snapshot == nil {
		return []string{"Loading the local recovery snapshot."}
	}
	s := *p.snapshot
	out := []string{fmt.Sprintf("%s · revision %d · %s", s.Goal.Title, s.Goal.Revision, s.Goal.State), s.Goal.SyncError, s.Goal.Reason, "This snapshot is local. Reloading it does not contact Linear or start work.", ""}
	out = append(out, recoveryBriefLines("Current brief", s.Brief)...)
	if s.PendingBrief != nil {
		out = append(out, "", "Changed Linear brief needs reconciliation.")
		out = append(out, recoverySourceLines(s)...)
	}
	if b := s.LatestBrief; b != nil {
		out = append(out, "", "Latest accepted brief decision", b.Reason, "Source "+b.SourceID+" · "+b.AcceptedAt.UTC().Format(time.RFC3339))
		out = append(out, recoveryBriefLines("Previous brief", b.Before)...)
		out = append(out, recoveryBriefLines("Accepted clarification", b.After)...)
	}
	if u := s.LatestProgress; u != nil {
		out = append(out, "", "Latest Linear progress update", u.ID+" · "+recoveryProgressState(*u), u.Summary)
		if u.Resolution != "" {
			out = append(out, "Resolution: "+u.Resolution)
		}
	}
	if s.ProgressError != "" {
		out = append(out, s.ProgressError)
	}
	if s.DeliveryEvidenceURL != "" {
		out = append(out, "", "Shared delivery evidence", s.DeliveryEvidenceURL)
	}
	return out
}
func (p *goalRecoveryModel) reviewLines() []string {
	d := p.reviewed
	out := []string{recoveryLabels[d.action], fmt.Sprintf("%s · reviewed revision %d", d.snapshot.Goal.Title, d.snapshot.Goal.Revision), recoveryConsequences[d.action], ""}
	if d.action == "accept-brief" {
		out = append(out, recoverySourceLines(d.snapshot)...)
		out = append(out, "")
		out = append(out, recoveryBriefLines("Current brief", d.snapshot.Brief)...)
		out = append(out, "")
		out = append(out, recoveryBriefLines("Reviewed clarification", d.contract)...)
	}
	if d.action == "dismiss-progress" {
		u := d.snapshot.LatestProgress
		out = append(out, "Update "+u.ID+" · "+recoveryProgressState(*u), u.Summary)
	}
	if d.url != "" {
		out = append(out, d.url)
	}
	if d.reason != "" {
		out = append(out, "", "Reason: "+d.reason)
	}
	return out
}
func (m *rootModel) goalRecoveryLines() []string {
	p := m.goals.recovery
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
	out := []string{m.vitalsBar(w), boxTitleHi.Render("lola ▸ " + m.projLabel(m.goals.project) + " ▸ Recovery and Linear"), ""}
	body := p.snapshotLines()
	footer := []string{"↑↓/pgup/pgdown scroll · f reload local snapshot · esc goals"}
	if actions := p.actions(); len(actions) > 0 {
		footer = append([]string{fmt.Sprintf("Action %d/%d: %s · tab/←→ choose · enter edit", p.selected+1, len(actions), recoveryLabels[actions[p.selected]])}, footer...)
	}
	editing := p.editor != nil && p.reviewed == nil && !p.inspect
	if p.reviewed != nil {
		body = p.reviewLines()
		footer = []string{"a confirm " + strings.ToLower(recoveryLabels[p.reviewed.action]), "↑↓/pgup/pgdown/home/end scroll · esc back to editing"}
	} else if editing {
		body = []string{recoveryLabels[p.action], recoveryConsequences[p.action], ""}
		footer = []string{p.editor.cur().help, "↑↓/tab field · enter edit/review · ctrl+v inspect source · esc close"}
		if p.editor.editing {
			footer = []string{"Editing " + p.editor.cur().label, "↑↓ line · enter new line · esc done"}
		}
	} else if p.inspect {
		footer = []string{"↑↓/pgup/pgdown/home/end scroll · v/esc back to editing"}
	}
	if p.busy {
		footer = []string{"Waiting for the daemon. No repeated submission."}
	}
	if p.uncertain {
		footer = []string{"f reload local snapshot · esc close · ↑↓ scroll · submission locked"}
	}
	if p.snapshot == nil || (p.err != "" && p.editor == nil) {
		footer = []string{"f reload local snapshot · esc goals"}
		if p.busy {
			footer = []string{"Loading local snapshot…"}
		}
	}
	if p.discard {
		footer = []string{"Unsaved recovery draft — y discard · n keep editing"}
	}
	if p.notice != "" {
		body = append([]string{p.notice, ""}, body...)
	}
	footer = wrap(footer)
	if p.err != "" {
		// Keep every recovery key visible even when an external error is huge.
		errorLines := wrap([]string{"Error: " + p.err})
		limit := max(1, h/2-len(footer))
		if len(errorLines) > limit {
			errorLines = append(errorLines[:max(0, limit-1)], "Error continues…")
		}
		footer = append(errorLines, footer...)
	}
	avail := max(1, h-len(out)-len(footer)-1)
	if editing {
		intro := wrap(body)
		// Long consequences remain readable via the source inspector; leave room for fields.
		if len(intro) > avail/2 {
			intro = append(intro[:max(1, avail/2-1)], "ctrl+v: inspect full context and consequences")
		}
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
		if p.inspect && p.action != "" {
			body = append([]string{recoveryLabels[p.action], recoveryConsequences[p.action], ""}, body...)
		}
		body = wrap(body)
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
