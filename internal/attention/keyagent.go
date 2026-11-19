package attention

import "strings"

// keyAgentCues are the phrases ssh, git and 1Password print when a signing key
// is held by an agent that will not answer (locked vault, dismissed prompt).
// Lower-case; matched against normalized pane text.
var keyAgentCues = []string{
	"agent refused operation",
	"communication with agent failed",
	"1password is locked",
	"1password locked",
	"unlock 1password",
	"unlock your 1password",
}

// KeyAgentLocked reports whether the pane shows the SSH key agent refusing to
// sign, the usual look of a locked 1Password. The pane is captured without
// joining wrapped rows, so escapes are dropped and every whitespace run
// (including a row break inside a phrase) folds to one space before matching.
// The text is attacker-influenceable: the answer only selects a fixed
// notification and decides nothing else.
func KeyAgentLocked(text string) bool {
	low := strings.Join(strings.Fields(strings.ToLower(stripANSI(text))), " ")
	for _, c := range keyAgentCues {
		if strings.Contains(low, c) {
			return true
		}
	}
	return false
}
