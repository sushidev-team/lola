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
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/protocol"
)

type goalAuthorizationModel struct {
	id                                          string
	seq                                         int
	busy, uncertain, acknowledged, discard, raw bool
	err                                         string
	options                                     *goal.StartOptions
	editor                                      *settingsForm
	reviewed                                    *goal.Authorization
	checks                                      []int
	artifacts                                   map[int][]int
	serial, scroll                              int
}
type goalAuthorizationMsg struct {
	project, id, op string
	gen, seq        int
	data            json.RawMessage
	err             string
}

var goalLimitFields = []struct{ key, label, help string }{
	{"concurrency", "Concurrent tasks", "1–8 tasks at once."},
	{"max_tasks", "Maximum tasks", "1–50, including any replacement tasks."},
	{"runtime_minutes", "Goal runtime (min)", "1–10080 minutes."},
	{"tokens", "Observed token limit", "Positive limit. Delayed usage reports are not an exact billing cap."},
	{"attempts_per_task", "Attempts per task", "1–5 attempts."},
	{"attempt_minutes", "Attempt time (min)", "Positive and no longer than the goal runtime."},
	{"review_rounds", "Review rounds", "1–3 rounds."},
	{"agent_runs", "Total agent calls", "1–1000; includes coordinator, workers and reviewers."},
	{"target_refreshes", "Target refreshes", "0 disables automatic refresh; maximum 3."},
	{"spend_usd", "Observed spend (USD)", "0 disables the spend limit. Delayed observations are not an exact billing cap."},
}

func goalCanAuthorize(g *goal.Overview) bool {
	return g != nil && !g.Authorized && g.State == "planned" && g.SyncError == ""
}
func (m *rootModel) openGoalAuthorization() tea.Cmd {
	g := m.goals.current()
	if !goalCanAuthorize(g) {
		return nil
	}
	m.goals.gen++
	m.goals.loading = false
	m.goals.authorization = &goalAuthorizationModel{id: g.ID}
	return m.goalAuthorizationRequest("options", "goalStartOptions", map[string]string{"id": g.ID})
}
func (m *rootModel) goalAuthorizationRequest(op, command string, args any) tea.Cmd {
	v, p := &m.goals, m.goals.authorization
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
		msg := goalAuthorizationMsg{project: project, id: id, gen: gen, seq: seq, op: op}
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
func (m *rootModel) handleGoalAuthorizationMsg(msg goalAuthorizationMsg) tea.Cmd {
	v, p := &m.goals, m.goals.authorization
	if p == nil || msg.project != v.project || msg.id != p.id || msg.gen != v.gen || msg.seq != p.seq {
		return nil
	}
	p.busy = false
	if msg.err != "" {
		p.acknowledged = false
		if msg.op == "start" {
			p.uncertain = true
			p.err = "Authorization was not confirmed: " + msg.err + ". It may already be accepted. Close and refresh Goals before another action."
		} else {
			p.err = "Could not load authorization: " + msg.err + ". Press f to reload current settings."
		}
		return nil
	}
	if msg.op == "start" {
		v.authorization = nil
		return m.refreshGoals()
	}
	var options goal.StartOptions
	if err := json.Unmarshal(msg.data, &options); err != nil || options.Goal.ID != p.id || options.Goal.Project != v.project || options.Authorization.ID != p.id || options.Authorization.Revision == 0 || options.Authorization.Revision != options.Goal.Revision || !goalCanAuthorize(&options.Goal) || len(options.Authorization.Policy.Members) == 0 {
		p.err = "Invalid authorization settings for this goal. Press f to reload."
		return nil
	}
	// Keep server-owned repository identities separate from the editable form.
	p.options = &options
	p.uncertain = false
	p.reviewed = nil
	p.acknowledged = false
	p.initEditor()
	return nil
}
func authorizationKey(serial int, key string) string { return fmt.Sprintf("check.%d.%s", serial, key) }
func (p *goalAuthorizationModel) initEditor() {
	f := &settingsForm{}
	p.editor = f
	p.checks = nil
	p.artifacts = map[int][]int{}
	p.serial = 0
	a := p.options.Authorization
	for i, member := range a.Policy.Members {
		section := ""
		if i == 0 {
			section = "Repositories"
		}
		f.fields = append(f.fields, setField{key: fmt.Sprintf("member.%d", i), label: member.ID + " paths", kind: sfList, lines: slices.Clone(member.Paths), section: section, help: member.Repo + " → " + member.Target + ". One allowed prefix per line; blank permits repository subject to task scope."})
	}
	f.fields = append(f.fields, setField{key: "add_check", label: "Add acceptance check", kind: sfAction, section: "Acceptance checks", help: "Required: your verification command, installed image digest, timeout and optional evidence."})
	for _, c := range a.Policy.Checks {
		p.addCheck(c)
	}
	for i, profile := range p.options.Profiles {
		help := "Evidence: " + strings.Join(profile.Artifacts, ", ")
		if len(profile.Artifacts) == 0 {
			help = "No additional evidence kinds required."
		}
		if profile.Required {
			help = "Required by project settings. " + help
		}
		section := ""
		if i == 0 {
			section = "Specialist reviewers"
		}
		f.fields = append(f.fields, setField{key: fmt.Sprintf("profile.%d", i), label: profile.ID + " · " + profile.Kind, kind: sfBool, b: profile.Required || slices.Contains(a.Policy.Profiles, profile.ID), section: section, help: help})
	}
	values := map[string]json.RawMessage{}
	b, _ := json.Marshal(a.Policy.Limits)
	_ = json.Unmarshal(b, &values)
	for i, item := range goalLimitFields {
		value := string(values[item.key])
		if value == "" {
			value = "0"
		}
		kind := sfInt
		if item.key == "spend_usd" {
			kind = sfText
		}
		field := setField{key: item.key, label: item.label, kind: kind, text: value, help: item.help}
		if i == 0 {
			field.section = "Execution limits"
		}
		f.fields = append(f.fields, field)
	}
	f.fields = append(f.fields,
		setField{key: "publish_pr", label: "Publish draft PRs", kind: sfBool, b: a.Policy.PublishPR, help: "Allow verified goal branches and draft PRs; final merge remains manual."},
		setField{key: "compatibility", label: "Compatibility", kind: sfList, help: "Used when publishing: 1–20 requirements, one per line, at most 500 bytes each. Required for stacks; optional for one repository."},
		setField{key: "merge_order", label: "Human merge order", kind: sfList, help: "Used when publishing: every repository ID exactly once, one per line, first to last. IDs: " + strings.Join(memberIDs(a.Policy), ", ")},
		setField{key: "require_human_acceptance", label: "Require acceptance", kind: sfBool, b: a.Policy.RequireHumanAcceptance, help: "Require a separate human acceptance decision after all final PRs are merged. Requires draft PR publication."},
		setField{key: "replan", label: "Allow task replanning", kind: sfBool, b: a.Policy.Replan, help: "Bounded decomposition after conclusive verification failures."},
		setField{key: "review", label: "Review authorization", kind: sfAction, help: "Inspect the complete policy before authorizing paid agent calls and workers."})
	if a.Policy.Rollout != nil {
		f.field("compatibility").lines = slices.Clone(a.Policy.Rollout.Compatibility)
		f.field("merge_order").lines = slices.Clone(a.Policy.Rollout.MergeOrder)
	}
	f.cursor = 0
	f.scroll = 0
	f.rebase()
}
func (p *goalAuthorizationModel) insertBefore(key string, fields []setField) {
	f := p.editor
	for i, field := range f.fields {
		if field.key == key {
			f.fields = slices.Insert(f.fields, i, fields...)
			return
		}
	}
}
func (p *goalAuthorizationModel) focus(key string) {
	for i, field := range p.editor.fields {
		if field.key == key {
			p.editor.cursor = i
			return
		}
	}
}
func (p *goalAuthorizationModel) addCheck(c goalpolicy.Check) {
	serial := p.serial
	p.serial++
	p.checks = append(p.checks, serial)
	key := func(s string) string { return authorizationKey(serial, s) }
	options := []string{""}
	for _, m := range p.options.Authorization.Policy.Members {
		options = append(options, m.ID)
	}
	fields := []setField{
		{key: key("id"), label: "Check ID", kind: sfText, text: c.ID, subsection: "Acceptance check", help: "Unique name for this check."},
		{key: key("member"), label: "Run from", kind: sfEnum, text: c.Member, options: options, help: "Enter cycles repositories. Blank means the combined repository root."},
		{key: key("command"), label: "Command", kind: sfList, lines: strings.Split(c.Command, "\n"), help: "Exact verification command. Runs in a disposable container without network access."},
		{key: key("image"), label: "Image digest", kind: sfText, text: c.Image, help: "Already installed image pinned by sha256 digest; mutable tags are refused."},
		{key: key("timeout"), label: "Timeout (seconds)", kind: sfInt, text: strconv.Itoa(c.TimeoutSeconds), help: "1–3600 seconds."},
		{key: key("memory"), label: "Memory (MiB)", kind: sfInt, text: strconv.Itoa(c.MemoryMiB), help: "0 uses 2048 MiB. Explicit limits: 256–8192 MiB."},
		{key: key("add_evidence"), label: "Add captured evidence", kind: sfAction, help: "Evidence needs an ID, kind and path from the combined repository root."},
		{key: key("remove"), label: "Remove check", kind: sfAction, help: "Remove this check and its evidence from the unsaved policy."},
	}
	p.insertBefore("add_check", fields)
	p.checkHeading()
	for _, a := range c.Artifacts {
		p.addArtifact(serial, a)
	}
	p.focus(key("id"))
}
func (p *goalAuthorizationModel) checkHeading() {
	p.editor.field("add_check").section = "Acceptance checks"
	for i, serial := range p.checks {
		f := p.editor.field(authorizationKey(serial, "id"))
		f.section = ""
		if i == 0 {
			f.section = "Acceptance checks"
			p.editor.field("add_check").section = ""
		}
	}
}

func (p *goalAuthorizationModel) addArtifact(check int, a goalpolicy.ArtifactSpec) {
	serial := p.serial
	p.serial++
	p.artifacts[check] = append(p.artifacts[check], serial)
	key := func(s string) string { return authorizationKey(check, fmt.Sprintf("evidence.%d.%s", serial, s)) }
	p.insertBefore(authorizationKey(check, "add_evidence"), []setField{
		{key: key("id"), label: "Evidence ID", kind: sfText, text: a.ID, help: "Unique identity within this check."},
		{key: key("kind"), label: "Evidence kind", kind: sfText, text: a.Kind, help: "Match the evidence kind required by a selected specialist."},
		{key: key("path"), label: "Evidence path", kind: sfText, text: a.Path, help: "Relative to the combined repository root; include the repository ID."},
		{key: key("remove"), label: "Remove evidence", kind: sfAction, help: "Remove from this unsaved policy."},
	})
	p.focus(key("id"))
}
func (p *goalAuthorizationModel) authorization() (goal.Authorization, error) {
	// Deep copy: neither later edits nor the options response can mutate review.
	b, _ := json.Marshal(p.options.Authorization)
	var a goal.Authorization
	_ = json.Unmarshal(b, &a)
	f := p.editor
	for i := range a.Policy.Members {
		a.Policy.Members[i].Paths = trimDropEmpty(f.field(fmt.Sprintf("member.%d", i)).lines)
	}
	a.Policy.Checks = nil
	for _, serial := range p.checks {
		field := func(s string) *setField { return f.field(authorizationKey(serial, s)) }
		timeout, err := strconv.Atoi(field("timeout").text)
		if err != nil {
			return a, errors.New("Check timeout must be a whole number from 1 to 3600")
		}
		memory, err := strconv.Atoi(field("memory").text)
		if err != nil {
			return a, errors.New("Check memory must be a whole number of MiB")
		}
		c := goalpolicy.Check{ID: field("id").text, Member: field("member").text, Command: strings.Join(field("command").lines, "\n"), Image: field("image").text, TimeoutSeconds: timeout, MemoryMiB: memory}
		for _, n := range p.artifacts[serial] {
			prefix := fmt.Sprintf("evidence.%d.", n)
			c.Artifacts = append(c.Artifacts, goalpolicy.ArtifactSpec{ID: field(prefix + "id").text, Kind: field(prefix + "kind").text, Path: field(prefix + "path").text})
		}
		a.Policy.Checks = append(a.Policy.Checks, c)
	}
	a.Policy.Profiles = nil
	for i, profile := range p.options.Profiles {
		if profile.Required || f.field(fmt.Sprintf("profile.%d", i)).b {
			a.Policy.Profiles = append(a.Policy.Profiles, profile.ID)
		}
	}
	limits := map[string]json.RawMessage{}
	for _, item := range goalLimitFields {
		s := strings.TrimSpace(f.field(item.key).text)
		if item.key == "spend_usd" {
			n, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return a, fmt.Errorf("%s must be a number", item.label)
			}
			s = strconv.FormatFloat(n, 'f', -1, 64)
		} else {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return a, fmt.Errorf("%s must be a whole number", item.label)
			}
			s = strconv.FormatInt(n, 10)
		}
		limits[item.key] = json.RawMessage(s)
	}
	b, err := json.Marshal(limits)
	if err != nil {
		return a, errors.New("Limits must be finite numbers")
	}
	if err = json.Unmarshal(b, &a.Policy.Limits); err != nil {
		return a, err
	}
	a.Policy.PublishPR = f.field("publish_pr").b
	a.Policy.RequireHumanAcceptance = f.field("require_human_acceptance").b
	a.Policy.Replan = f.field("replan").b
	a.Policy.Rollout = nil
	compatibility, order := trimDropEmpty(f.field("compatibility").lines), trimDropEmpty(f.field("merge_order").lines)
	if a.Policy.PublishPR && (len(a.Policy.Members) > 1 || len(compatibility) > 0 || len(order) > 0) {
		a.Policy.Rollout = &goalpolicy.Rollout{Compatibility: compatibility, MergeOrder: order}
	}
	if err = a.Policy.ValidateAuthorization(); err != nil {
		return a, err
	}
	if a.Policy.Limits.MaxTasks < len(p.options.Goal.Tasks) {
		return a, errors.New("Maximum tasks is below this goal's current task count")
	}
	return a, nil
}
func (p *goalAuthorizationModel) action(key string) {
	if key == "add_check" {
		if len(p.checks) >= 50 {
			p.err = "At most 50 checks are allowed."
			return
		}
		p.addCheck(goalpolicy.Check{ID: fmt.Sprintf("acceptance-%d", p.serial+1), Member: p.options.Authorization.Policy.Members[0].ID, TimeoutSeconds: 300})
		return
	}
	for _, serial := range p.checks {
		if key == authorizationKey(serial, "remove") {
			prefix := authorizationKey(serial, "")
			p.editor.fields = slices.DeleteFunc(p.editor.fields, func(f setField) bool { return strings.HasPrefix(f.key, prefix) })
			p.checks = slices.DeleteFunc(p.checks, func(n int) bool { return n == serial })
			delete(p.artifacts, serial)
			p.checkHeading()
			p.focus("add_check")
			return
		}
		if key == authorizationKey(serial, "add_evidence") {
			if len(p.artifacts[serial]) >= 20 {
				p.err = "At most 20 evidence entries per check."
				return
			}
			p.addArtifact(serial, goalpolicy.ArtifactSpec{})
			return
		}
		for _, n := range p.artifacts[serial] {
			prefix := authorizationKey(serial, fmt.Sprintf("evidence.%d.", n))
			if key == prefix+"remove" {
				p.editor.fields = slices.DeleteFunc(p.editor.fields, func(f setField) bool { return strings.HasPrefix(f.key, prefix) })
				p.artifacts[serial] = slices.DeleteFunc(p.artifacts[serial], func(i int) bool { return i == n })
				p.focus(authorizationKey(serial, "add_evidence"))
				return
			}
		}
	}
}
func (m *rootModel) updateGoalAuthorization(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.goals.authorization
	key := k.String()
	close := func() (tea.Model, tea.Cmd) { m.goals.authorization = nil; return m, m.refreshGoals() }
	if key == "ctrl+c" {
		return m, nil
	}
	if p.busy {
		return m, nil
	} // authorization must settle before leaving this screen
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
	if p.editor == nil {
		if key == "esc" {
			return close()
		}
		if key == "f" {
			return m, m.goalAuthorizationRequest("options", "goalStartOptions", map[string]string{"id": p.id})
		}
		return m, nil
	}
	if p.reviewed != nil {
		switch key {
		case "esc":
			p.reviewed = nil
			p.acknowledged = false
			p.raw = false
			p.scroll = 0
		case "space":
			p.acknowledged = !p.acknowledged
		case "a":
			if p.acknowledged {
				return m, m.goalAuthorizationRequest("start", protocol.GoalStartV2, *p.reviewed)
			}
		case "v":
			p.raw = !p.raw
			p.scroll = 0
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
			for i, profile := range p.options.Profiles {
				if fld.key == fmt.Sprintf("profile.%d", i) && profile.Required {
					p.err = "This specialist is required by project settings."
					return m, nil
				}
			}
			fld.b = !fld.b
		case sfEnum:
			cycleEnum(fld)
		case sfAction:
			if key != "enter" {
				break
			}
			p.err = ""
			if fld.key == "review" {
				a, err := p.authorization()
				if err != nil {
					p.err = err.Error()
				} else {
					p.reviewed = &a
					p.acknowledged = false
					p.scroll = 0
					p.raw = false
				}
			} else {
				p.action(fld.key)
			}
		case sfText:
			if key == "space" {
				fld.text += " "
			}
		}
	case "backspace":
		if f.cur().kind == sfText || f.cur().kind == sfInt {
			f.cur().text = dropLastRune(f.cur().text)
		}
	default:
		f.typeInto(f.cur(), k)
	}
	return m, nil
}
func (p *goalAuthorizationModel) reviewLines() []string {
	a := p.reviewed
	if p.raw {
		b, _ := json.MarshalIndent(a, "", "  ")
		return strings.Split(string(b), "\n")
	}
	g := p.options.Goal
	out := []string{fmt.Sprintf("Start %s · reviewed revision %d", g.Title, a.Revision), g.Outcome, "Workers may begin after acceptance. Final merge remains manual.", "", "Goal acceptance"}
	out = append(out, g.Acceptance...)
	if len(g.NonGoals) > 0 {
		out = append(out, "Outside this goal")
		out = append(out, g.NonGoals...)
	}
	for _, task := range g.Tasks {
		out = append(out, "", task.Title+" · "+strings.Join(task.Apps, ", "))
		out = append(out, task.Acceptance...)
	}
	out = append(out, "", "Repositories and allowed scope")
	for _, member := range a.Policy.Members {
		paths := strings.Join(member.Paths, ", ")
		if paths == "" {
			paths = "Entire repository, restricted by each task's scope"
		}
		out = append(out, member.ID+" · "+member.Repo+" → "+member.Target, member.Source, "Paths: "+paths)
	}
	out = append(out, goalRolloutLines(a.Policy.Rollout)...)
	for _, c := range a.Policy.Checks {
		member := c.Member
		if member == "" {
			member = "combined repository root"
		}
		out = append(out, "", fmt.Sprintf("Check %s · %s · %ds · %d MiB", c.ID, member, c.TimeoutSeconds, c.MemoryLimitBytes()>>20))
		out = append(out, strings.Split(c.Command, "\n")...)
		out = append(out, c.Image)
		for _, a := range c.Artifacts {
			out = append(out, a.ID+": "+a.Kind+" from "+a.Path)
		}
	}
	profiles := strings.Join(a.Policy.Profiles, ", ")
	if profiles == "" {
		profiles = "None selected"
	}
	out = append(out, "", "Specialist reviews: "+profiles, "", "Execution limits")
	values := map[string]json.RawMessage{}
	b, _ := json.Marshal(a.Policy.Limits)
	_ = json.Unmarshal(b, &values)
	for _, item := range goalLimitFields {
		value := string(values[item.key])
		if value == "" {
			value = "0"
		}
		out = append(out, item.label+": "+value)
	}
	out = append(out, "Token and spend limits use delayed observations, not exact billing caps.", fmt.Sprintf("Publish verified goal branches and draft PRs: %t", a.Policy.PublishPR), fmt.Sprintf("Allow bounded task replanning: %t", a.Policy.Replan))
	out = append(out, fmt.Sprintf("Require separate human acceptance after final delivery: %t", a.Policy.RequireHumanAcceptance))
	return out
}
func (m *rootModel) goalAuthorizationLines() []string {
	p := m.goals.authorization
	w, h := max(30, m.width), max(12, m.height)
	wrap := func(lines []string) []string {
		var out []string
		for _, line := range lines {
			out = append(out, strings.Split(lipgloss.NewStyle().Width(w).Render(goalText(line)), "\n")...)
		}
		return out
	}
	out := []string{m.vitalsBar(w), boxTitleHi.Render("lola ▸ " + m.projLabel(m.goals.project) + " ▸ Authorize execution"), ""}
	var body, footer []string
	if p.reviewed != nil {
		body = p.reviewLines()
		ack := "[ ]"
		if p.acknowledged {
			ack = "[x]"
		}
		footer = []string{ack + " I authorize this scope, checks, limits and paid agent calls.", "space acknowledge · a authorize and start", "↑↓/pgup/pgdown scroll · v full policy · esc back to limits"}
	} else if p.editor != nil {
		body = []string{fmt.Sprintf("%s · revision %d", p.options.Goal.Title, p.options.Authorization.Revision), "Set checks and limits; review before starting workers.", ""}
		footer = []string{p.editor.cur().help, "↑↓/tab field · enter edit/toggle/action · esc back"}
		if p.editor.editing {
			footer = []string{"Editing " + p.editor.cur().label, "↑↓ line · enter new line · esc done"}
		}
	} else {
		body = []string{"Loading the goal's current execution settings."}
		footer = []string{"f reload settings · esc goals"}
	}
	if p.busy {
		footer = []string{"Waiting for the daemon. No repeated submission."}
	}
	if p.uncertain {
		footer = []string{"esc close and refresh Goals; do not repeat authorization"}
	}
	if p.discard {
		footer = []string{"Unsaved execution policy — y discard · n keep editing"}
	}
	if p.err != "" {
		footer = append([]string{"Error: " + p.err}, footer...)
	}
	footer = wrap(footer)
	avail := max(1, h-len(out)-len(footer)-1)
	if p.editor != nil && p.reviewed == nil {
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
