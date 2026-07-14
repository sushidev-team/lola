// List screen: polls from config merged with live daemon status when the
// socket is reachable.
package tui

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

type listModel struct {
	cursor        int
	status        *protocol.StatusData // nil when the daemon is unreachable
	statusErr     string               // non-dial status failure, if any
	confirmDelete bool
	flash         string
	teamNames     map[string]string // teamID -> "KEY — Name", from caches
}

func newListModel(cfg *config.Config) listModel {
	return listModel{teamNames: teamNamesFromCache(cfg)}
}

// teamNamesFromCache resolves team display names offline from the per-team
// metadata caches of the teams referenced by polls.
func teamNamesFromCache(cfg *config.Config) map[string]string {
	names := map[string]string{}
	for _, p := range cfg.Polls {
		if p.TeamID == "" || names[p.TeamID] != "" {
			continue
		}
		if m, err := loadTeamCache(p.TeamID); err == nil {
			for _, t := range m.Teams {
				names[t.ID] = t.Key + " — " + t.Name
			}
		}
	}
	return names
}

func (l *listModel) pollStatus(name string) *protocol.PollStatus {
	if l.status == nil {
		return nil
	}
	for i := range l.status.Polls {
		if l.status.Polls[i].Name == name {
			return &l.status.Polls[i]
		}
	}
	return nil
}

func (l *listModel) teamDisplay(teamID string) string {
	if teamID == "" {
		return "-"
	}
	if n := l.teamNames[teamID]; n != "" {
		return n
	}
	return shortID(teamID)
}

func (m *rootModel) listView() string {
	l := &m.list
	var b strings.Builder
	b.WriteString(titleStyle.Render("lola — polls") + "\n")
	if l.status != nil {
		fmt.Fprintf(&b, "daemon: %s   ao: %s   linear: %s\n\n",
			goodText.Render("running"),
			yesNoStyled(l.status.AORunning, "reachable", "unreachable"),
			yesNoStyled(l.status.LinearOK, "ok", "error"))
	} else {
		b.WriteString(badText.Render("daemon: not running") + faintText.Render("  (start with: lola run)"))
		if l.statusErr != "" {
			b.WriteString("  " + badText.Render(l.statusErr))
		}
		b.WriteString("\n\n")
	}

	headers := []string{" ", "NAME", "ON", "TEAM", "AO PROJECT", "LAST RUN", "LAST SPAWN", "ERROR"}
	rows := make([][]string, len(m.cfg.Polls))
	for i, p := range m.cfg.Polls {
		enabled := p.Enabled
		lastRun, lastSpawn, lastErr := "-", "-", ""
		if ps := l.pollStatus(p.Name); ps != nil {
			enabled = ps.Enabled
			lastRun, lastSpawn = fmtAgo(ps.LastRun), fmtAgo(ps.LastSpawn)
			lastErr = ps.LastError
			if ps.Running {
				lastRun = "running…"
			}
		}
		marker := " "
		if i == l.cursor {
			marker = "›"
		}
		rows[i] = []string{marker, p.Name, yesNo(enabled), l.teamDisplay(p.TeamID), p.AOProject, lastRun, lastSpawn, lastErr}
	}

	w := colWidths(headers, rows)
	b.WriteString(tblHeader.Render(padCells(headers, w)) + "\n")
	if len(rows) == 0 {
		b.WriteString(faintText.Render("no polls yet — press n to create one") + "\n")
	}
	for i, r := range rows {
		line := padCells(r, w)
		if i == l.cursor {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	switch {
	case l.confirmDelete:
		name := ""
		if p := m.selectedPoll(); p != nil {
			name = p.Name
		}
		b.WriteString(warnText.Render(fmt.Sprintf("delete poll %q? (y/n)", name)) + "\n")
	case l.flash != "":
		b.WriteString(faintText.Render(l.flash) + "\n")
	}
	b.WriteString(faintText.Render("↑/↓ move · n new · enter/e edit · space toggle · d delete · r refresh cache · q quit") + "\n")
	return b.String()
}
