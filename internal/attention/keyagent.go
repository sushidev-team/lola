package attention

import "strings"

// keyAgentCues are the phrases ssh, git and 1Password print when a signing key
// is held by an agent that will not answer (locked vault, dismissed prompt).
// Lower-case, no spaces: text is compared with all whitespace removed, so a
// terminal wrap between or inside words cannot hide a cue.
var keyAgentCues = []string{
	"agentrefusedoperation",
	"communicationwithagentfailed",
	"1passwordislocked",
	"1passwordlocked",
	"unlock1password",
	"unlockyour1password",
}

// keyAgentRows is how many of the last non-blank rows count. The capture
// includes scrollback, and a refusal the human already dealt with must stop
// matching once it has scrolled up out of the live screen.
const keyAgentRows = 20

// KeyAgentLocked reports whether the pane's recent rows show the SSH key agent
// refusing to sign, the usual look of a locked 1Password. The pane is captured
// without joining wrapped rows, so escapes are dropped and whitespace is
// ignored before matching. The text is attacker-influenceable: the answer only
// selects a fixed notification and decides nothing else.
func KeyAgentLocked(text string) bool {
	rows := strings.Split(strings.TrimRight(stripANSI(text), " \t\r\n"), "\n")
	if len(rows) > keyAgentRows {
		rows = rows[len(rows)-keyAgentRows:]
	}
	low := strings.Join(strings.Fields(strings.ToLower(strings.Join(rows, "\n"))), "")
	for _, c := range keyAgentCues {
		if strings.Contains(low, c) {
			return true
		}
	}
	return false
}
