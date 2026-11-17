package tui

// sshcockpit.go is the SSH glance outside the workspace: the list's chip, the
// detail card's block and the keybar's H. It reads SessionInfo.SSH only, which the
// daemon builds from memory, so none of it costs a fetch.

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshtab"
)

// sshChipRank orders connections by what they ask of a human, the most urgent
// first. -1 means no chip at all: a session whose every connection ended reads as
// an ordinary session.
func sshChipRank(c *protocol.SSHConnInfo) int {
	switch {
	case c.State == protocol.SSHConnClosed || c.State == protocol.SSHConnDenied:
		return -1
	case c.Prompt != nil, c.State == protocol.SSHConnAuthenticating:
		return 0
	case c.PendingApprovals > 0:
		return 1
	case c.State == protocol.SSHConnRequested:
		return 2
	case c.Wheel == protocol.SSHWheelHuman:
		return 3
	case c.Grant != nil:
		return 4
	case c.State == protocol.SSHConnConnecting:
		return 5
	case c.State == protocol.SSHConnDisconnected:
		return 6
	case c.State == protocol.SSHConnFailed:
		return 7
	case c.RunningBlocks > 0:
		return 8
	}
	return 9
}

func sshChipWord(c *protocol.SSHConnInfo) string {
	switch sshChipRank(c) {
	case 0:
		if c.Prompt != nil {
			return statusOrange.Render("asking")
		}
		return statusOrange.Render("sign in")
	case 1:
		return statusOrange.Render(fmt.Sprintf("%d↑", c.PendingApprovals))
	case 2:
		return statusOrange.Render("wants to connect")
	case 3:
		return sshAccentBold.Render("you")
	case 4:
		return warnText.Render("grant " + sshClock(c.Grant.ExpiresAt.Sub(sshNow())))
	case 5:
		return faintText.Render("connecting…")
	case 6:
		return warnText.Render("disconnected")
	case 7:
		return badText.Render("could not connect")
	case 8:
		return sshInfo.Render("running")
	}
	return faintText.Render("connected")
}

// sshTopConn is the connection a row speaks for, and how many others are live.
func sshTopConn(info *protocol.SSHSessionInfo) (*protocol.SSHConnInfo, int) {
	if info == nil {
		return nil, 0
	}
	var top *protocol.SSHConnInfo
	live := 0
	for i := range info.Connections {
		c := &info.Connections[i]
		r := sshChipRank(c)
		if r < 0 {
			continue
		}
		live++
		if top == nil || r < sshChipRank(top) {
			top = c
		}
	}
	return top, max(0, live-1)
}

// sshChip is the list cell: tier, host, then one state word. "" means no chip.
func sshChip(si protocol.SessionInfo) string {
	c, others := sshTopConn(si.SSH)
	if c == nil {
		return ""
	}
	tier := faintText
	if si.SSH.NeedsHuman {
		tier = statusOrange
	}
	out := tier.Render(sshTierShort(c.Tier)+" "+truncPlain(sshHostLabel(c), 14)) + " " + sshChipWord(c)
	if others > 0 {
		out += faintText.Render(fmt.Sprintf(" +%d", others))
	}
	return out
}

func anySSHChip(list []protocol.SessionInfo) bool {
	for _, si := range list {
		if sshChip(si) != "" {
			return true
		}
	}
	return false
}

// sshDetailLines is the detail card's block. The headline is the agent's claim and
// wears its ring. The running command is a fact.
func sshDetailLines(si protocol.SessionInfo, w int) []string {
	info := si.SSH
	if info == nil || len(info.Connections) == 0 {
		return nil
	}
	c, _ := sshTopConn(info)
	if c == nil {
		c = &info.Connections[len(info.Connections)-1]
	}
	head := sshTierShort(c.Tier) + " " + sshHostLabel(c) + " · " + stripANSI(sshStateWord(c, sshNow()))
	wheel := "wheel agent"
	if c.Wheel == protocol.SSHWheelHuman {
		wheel = sshAccent.Render("wheel you")
	}
	out := []string{"ssh:      " + head + " · " + wheel}
	if info.Headline != "" {
		out = append(out, faintText.Render("          ○ "+truncPlain(sshLine(info.Headline), max(10, w))))
	}
	if info.RunningCommand != "" {
		out = append(out, "          "+sshInfo.Render("▪ running ")+truncPlain(sshLine(info.RunningCommand), max(10, w)))
	}
	if info.NeedsHuman && info.NeedsHumanWhy != "" {
		out = append(out, "          "+statusOrange.Render(sshLine(info.NeedsHumanWhy)))
	}
	hint := "H workspace"
	if n := sshPendingTotal(info); n > 0 {
		hint += fmt.Sprintf(" · %d to approve", n)
	}
	if info.OpenItems > 0 {
		hint += fmt.Sprintf(" · %d open", info.OpenItems)
	}
	return append(out, "          "+faintText.Render(hint))
}

func sshPendingTotal(info *protocol.SSHSessionInfo) int {
	n := 0
	for i := range info.Connections {
		n += info.Connections[i].PendingApprovals
	}
	return n
}

// sshKeybarCell is the cockpit keybar's H: orange with a count when it needs you.
func sshKeybarCell(si *protocol.SessionInfo) string {
	if si == nil || si.SSH == nil || len(si.SSH.Connections) == 0 {
		return ""
	}
	if si.SSH.NeedsHuman {
		text := "H ssh"
		if n := sshPendingTotal(si.SSH); n > 0 {
			text += fmt.Sprintf(" %d", n)
		}
		return statusOrange.Render(text)
	}
	return "H ssh"
}

// sshDrivingMessage is the line above the keybar while a human has the wheel and
// is inside that connection's terminal.
func (m *rootModel) sshDrivingMessage() string {
	e := m.currentEmbed()
	if !m.embedFocused || e == nil || !sshIsTab(e.sessionID, e.tmuxName) {
		return ""
	}
	si := m.sessions.byID(e.sessionID)
	if si == nil || si.SSH == nil {
		return ""
	}
	for i := range si.SSH.Connections {
		c := &si.SSH.Connections[i]
		if c.Tab == e.tmuxName && c.Wheel == protocol.SSHWheelHuman {
			return sshAccentBold.Render("You have the wheel.") + " " +
				sshAccent.Render(strings.TrimSpace(protocol.SSHDrivingNote)+" ^q, then W to hand back.")
		}
	}
	return ""
}

// sshIsTab reports whether name is one of this session's ssh tabs, anchored at
// both ends: "lola-fe-4" is a prefix of "lola-fe-42-ssh-1".
func sshIsTab(id, name string) bool { return sshtab.Index(id, name) > 0 }

// sshTabLabel is the tab chip: "ssh", then "ssh2" for a later tab.
func sshTabLabel(id, name string) string {
	if n := sshtab.Index(id, name); n > 1 {
		return fmt.Sprintf("ssh%d", n)
	}
	return "ssh"
}
