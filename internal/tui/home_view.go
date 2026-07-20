package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// homeView renders the project-list landing screen to a full frame.
func (m *rootModel) homeView() string {
	return strings.Join(m.homeLines(), "\n")
}

func (m *rootModel) homeLines() []string {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	h := &m.home

	out := make([]string, 0, H)
	out = append(out, m.vitalsBar(W))
	out = append(out, previewLine(faintText.Render("lola ")+"▸ projects", W))
	if h.filtering {
		out = append(out, previewLine(faintText.Render("/")+h.filter+"_", W))
	}

	// Panel height = everything not spent on the header rows, the message line,
	// and the keybar.
	used := len(out) + 2 // + message + keybar
	panelH := H - used
	if panelH < 3 {
		panelH = 3
	}
	out = append(out, m.projectsPanel(W, panelH)...)
	out = append(out, m.homeMessage(W))
	out = append(out, m.homeKeybar(W))
	return fitHeight(out, H)
}

// projectsPanel renders the bordered projects table (header + one row per
// project), highlighting the selection and windowing to panelH.
func (m *rootModel) projectsPanel(w, panelH int) []string {
	h := &m.home
	rows := h.rows(m.cfg)
	info := h.infoByName()

	// Poll posture comes from config (authoritative, daemon-up or down): a
	// project has at most one polling config (its own).
	cfgPolls, cfgEnabled := map[string]int{}, map[string]int{}
	for i := range m.cfg.Projects {
		pr := m.cfg.Projects[i]
		if pr.Polls() {
			cfgPolls[pr.Name] = 1
			if pr.Enabled {
				cfgEnabled[pr.Name] = 1
			}
		}
	}

	header := []string{"PROJECT", "PATH", "POLL", "LIVE", "ATTENTION", "LAST"}
	cells := make([][]string, 0, len(rows))
	for _, p := range rows {
		cells = append(cells, homeRowCells(p, info[p.Name], h.data != nil, cfgPolls[p.Name], cfgEnabled[p.Name]))
	}

	body := make([]string, 0, panelH)
	if len(rows) == 0 {
		body = append(body,
			"",
			"  No projects yet.",
			"  Press "+goodText.Render("a")+" to add your first repo.",
			faintText.Render("  (a project is any local git checkout; polling is optional.)"),
		)
	} else {
		widths := colWidths(header, cells)
		body = append(body, tblHeader.Render(padCells(header, widths)))
		// Window the rows around the cursor within the panel's inner height.
		innerH := panelH - 3 // borders (2) + header (1)
		if innerH < 1 {
			innerH = 1
		}
		start := viewportStart(h.cursor, len(cells), innerH)
		for i := start; i < len(cells) && i < start+innerH; i++ {
			line := padCells(cells[i], widths)
			if i == h.cursor {
				line = highlightRow(line, w-4, bgSGR(colSel))
			}
			body = append(body, line)
		}
	}

	title := paneTitle("Projects", fmt.Sprintf("%d", len(rows)))
	return box(title, body, w, panelH, true)
}

// homeRowCells builds one project row. Rows always render from config; when
// daemon decoration is present (haveData), the live columns are filled, else
// they show placeholders so the list stays useful with the daemon down.
// pollCount/enabled come from config so poll posture reads correctly either way.
func homeRowCells(p config.Project, info protocol.ProjectInfo, haveData bool, pollCount, enabled int) []string {
	path := p.Path
	if path == "" {
		path = faintText.Render("(no path)")
	} else {
		path = compactPath(path)
	}

	poll := homePollCell(info, haveData, pollCount, enabled)

	live, attn, last := faintText.Render("—"), faintText.Render("—"), faintText.Render("—")
	if haveData {
		live = fmt.Sprintf("%d", info.LiveCounted)
		attn = homeAttention(info)
		last = shortAgo(info.LastRun)
	}
	return []string{p.DisplayName(), path, poll, live, attn, last}
}

// homePollCell describes the project's polling posture as a shape-distinct glyph
// (not color-only). Poll counts are from config (always known); the daemon adds
// the ⚠ missing (bad path) and ⚠ err (poll error) states when it is up.
func homePollCell(info protocol.ProjectInfo, haveData bool, pollCount, enabled int) string {
	if haveData && !info.PathOK {
		return warnText.Render("⚠ missing")
	}
	if haveData && info.LastError != "" {
		return warnText.Render("⚠ err")
	}
	if pollCount == 0 {
		return warnText.Render("⚠ no polls")
	}
	if enabled > 0 {
		return goodText.Render(fmt.Sprintf("● %d on", enabled))
	}
	return faintText.Render("○ paused")
}

func homeAttention(info protocol.ProjectInfo) string {
	var parts []string
	if info.NeedsYou > 0 {
		parts = append(parts, statusOrange.Render(fmt.Sprintf("%d need", info.NeedsYou)))
	}
	if info.CIRed > 0 {
		parts = append(parts, badText.Render(fmt.Sprintf("%d ci", info.CIRed)))
	}
	if len(parts) == 0 {
		return faintText.Render("—")
	}
	return strings.Join(parts, faintText.Render("·"))
}

func (m *rootModel) homeMessage(w int) string {
	h := &m.home
	switch {
	case h.confirmRemove:
		return previewLine(warnText.Render(fmt.Sprintf("remove project %q from config? (y/n)", h.removeTarget)), w)
	case h.adding:
		return previewLine(warnText.Render("new project name: ")+h.addInput+"_"+faintText.Render("  · enter create · esc cancel"), w)
	case h.flash != "":
		if h.flashGood {
			return previewLine(goodText.Render(h.flash), w)
		}
		return previewLine(warnText.Render(h.flash), w)
	case h.daemonDown:
		return previewLine(badText.Render("daemon: not running")+faintText.Render(m.daemonDownHint()), w)
	case h.dataErr != "":
		return previewLine(badText.Render("projects: "+h.dataErr), w)
	case m.daemonOp != "":
		return previewLine(warnText.Render("daemon: "+m.daemonOp+"…"), w)
	}
	return ""
}

func (m *rootModel) homeKeybar(w int) string {
	h := &m.home
	switch {
	case h.filtering:
		return previewLine(faintText.Render("type to filter · enter apply · esc clear"), w)
	case h.adding:
		return previewLine(faintText.Render("type a name · enter create · esc cancel"), w)
	case h.confirmRemove:
		return previewLine(warnText.Render("y")+faintText.Render(" remove · ")+warnText.Render("n")+faintText.Render(" cancel"), w)
	}
	keys := []string{"↑↓ move", "enter open", "a add", "e edit", "space poll", "x remove", "/ filter", "esc back"}
	keys = append(keys, "S settings", "d doctor")
	if m.manageDaemon() {
		if m.home.data == nil {
			keys = append(keys, "^r start daemon")
		} else {
			keys = append(keys, "^r restart", "^x stop")
		}
	}
	keys = append(keys, "q quit")
	return previewLine(faintText.Render(strings.Join(keys, " · ")), w)
}

// ---- small helpers ----

// compactPath abbreviates a home-relative path to ~/… for display.
func compactPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// shortAgo renders a compact "2m"/"3h"/"5d" since t, or "—" for the zero time.
func shortAgo(t time.Time) string {
	if t.IsZero() {
		return faintText.Render("—")
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
