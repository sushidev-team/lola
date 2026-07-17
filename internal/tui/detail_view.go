package tui

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

func (m *rootModel) detailView() string {
	return strings.Join(m.detailLines(), "\n")
}

func (m *rootModel) detailLines() []string {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	d := &m.detail

	out := make([]string, 0, H)
	out = append(out, m.vitalsBar(W))
	out = append(out, previewLine(faintText.Render("lola ▸ ")+d.project, W))

	// Budget: header(2) + message(1) + keybar(1) = 4 chrome lines. Split the rest
	// among the status box, the actions box, and the live strip.
	chrome := len(out) + 2
	avail := H - chrome
	if avail < 9 {
		avail = 9
	}
	statusH := 5
	actions := m.detailActions()
	actionsH := len(actions) + 2
	stripH := avail - statusH - actionsH
	if stripH < 3 {
		stripH = 3
	}

	out = append(out, m.detailStatusBox(W, statusH)...)
	out = append(out, m.detailActionsBox(W, actionsH, actions)...)
	out = append(out, m.detailStripBox(W, stripH)...)
	out = append(out, m.detailMessage(W))
	out = append(out, m.detailKeybar(W))
	return fitHeight(out, H)
}

func (m *rootModel) detailStatusBox(w, h int) []string {
	d := &m.detail
	info, haveInfo := m.detailInfo()
	p := m.cfg.ProjectByName(d.project)

	var body []string
	if p == nil {
		body = append(body, badText.Render("project not found in config"))
		return box(paneTitle(d.project, ""), body, w, h, true)
	}

	agent := info.Agent
	if agent == "" {
		agent = m.cfg.AgentForProject(p.Name)
	}
	base := p.DefaultBranch
	if base == "" {
		base = "main"
	}
	repo := p.Repo
	if repo == "" {
		repo = faintText.Render("(none)")
	}
	body = append(body, fmt.Sprintf("path %s   repo %s   agent %s   base %s",
		compactPath(p.Path), repo, agent, base))

	// Polling line: a project polls Linear iff it has a team; at most one config.
	if p.Polls() {
		dot := faintText.Render("○ paused")
		if p.Enabled {
			dot = goodText.Render("● on")
		}
		body = append(body, "polling  "+dot)
	} else {
		body = append(body, faintText.Render("polling  (off)"))
	}

	// Health + rollup line.
	if haveInfo {
		agentGlyph := goodText.Render("✓")
		if !info.AgentOK {
			agentGlyph = badText.Render("✗")
		}
		roll := fmt.Sprintf("%d live", info.LiveCounted)
		if info.NeedsYou > 0 {
			roll += " · " + statusOrange.Render(fmt.Sprintf("%d need you", info.NeedsYou))
		}
		if info.CIRed > 0 {
			roll += " · " + badText.Render(fmt.Sprintf("%d ci-red", info.CIRed))
		}
		health := fmt.Sprintf("health %s agent    %s", agentGlyph, roll)
		if !info.AgentOK && info.AgentErr != "" {
			health = badText.Render("agent not ready: "+info.AgentErr) + faintText.Render(" — launch verbs disabled")
		}
		body = append(body, health)
	} else {
		body = append(body, faintText.Render("health  (daemon down — status unavailable)"))
	}

	return box(paneTitle(d.project, ""), body, w, h, true)
}

func (m *rootModel) detailActionsBox(w, h int, actions []detailAction) []string {
	d := &m.detail
	body := make([]string, 0, len(actions))
	for i, a := range actions {
		cursor := "  "
		if i == d.cursor {
			cursor = goodText.Render("› ")
		}
		key := a.key
		label := a.label
		desc := a.desc
		row := fmt.Sprintf("%s%s  %-14s %s", cursor, key, label, faintText.Render(desc))
		if !a.enabled {
			row = fmt.Sprintf("%s%s  %-14s %s", cursor, faintText.Render(key), faintText.Render(label), faintText.Render("(coming soon)"))
		}
		if i == d.cursor {
			row = highlightRow(previewLine(row, w-4), w-4, bgSGR(colSel))
		}
		body = append(body, row)
	}
	return box(paneTitle("Actions", ""), body, w, h, true)
}

func (m *rootModel) detailStripBox(w, h int) []string {
	d := &m.detail
	var rows []protocol.SessionInfo
	if m.sessions.data != nil {
		for _, s := range m.sessions.data.Sessions {
			if s.Project == d.project {
				rows = append(rows, s)
			}
		}
		rows = SortSessions(rows)
	}

	var body []string
	if len(rows) == 0 {
		body = append(body, faintText.Render("no live sessions in this project"))
	} else {
		inner := h - 2
		for i, s := range rows {
			if i >= inner {
				body = append(body, faintText.Render(fmt.Sprintf("… %d more", len(rows)-i)))
				break
			}
			issue := s.Issue
			if issue == "" {
				issue = s.ID
			}
			pr := ""
			if s.PRNumber > 0 {
				pr = fmt.Sprintf("#%d", s.PRNumber)
			}
			line := fmt.Sprintf("%-10s %-22s %-14s %s", issue, truncPlain(s.Title, 22), s.Status, pr)
			body = append(body, previewLine(line, w-4))
		}
	}
	return box(paneTitle("Live in "+d.project, ""), body, w, h, true)
}

func (m *rootModel) detailMessage(w int) string {
	d := &m.detail
	switch {
	case d.wtMode:
		launch := "shell"
		if d.wtAgent {
			launch = goodText.Render("agent")
		}
		return previewLine(warnText.Render("new branch: ")+d.wtBranch+"_"+faintText.Render("  · launch: ")+launch+faintText.Render(" (tab) · enter create · esc cancel"), w)
	case d.flash != "":
		if d.flashOK {
			return previewLine(goodText.Render(d.flash), w)
		}
		return previewLine(warnText.Render(d.flash), w)
	case m.daemonOp != "":
		return previewLine(warnText.Render("daemon: "+m.daemonOp+"…"), w)
	}
	return ""
}

func (m *rootModel) detailKeybar(w int) string {
	if m.detail.wtMode {
		return previewLine(faintText.Render("type a branch · tab agent/shell · enter create · esc cancel"), w)
	}
	keys := []string{"↑↓ move", "enter run", "p PR", "t ticket", "w worktree", "P polls", "s sessions", "e edit", "esc back"}
	keys = append(keys, "S settings", "d doctor")
	if m.manageDaemon() {
		keys = append(keys, "^r restart", "^x stop")
	}
	keys = append(keys, "q quit")
	return previewLine(faintText.Render(strings.Join(keys, " · ")), w)
}
