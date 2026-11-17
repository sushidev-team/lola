package agent

import "slices"

// claudeScreens are the slash commands that open a screen in Claude Code: a dialog that only
// takes keys, where typed text goes astray and a stray key can change a setting. Only /config,
// /usage, /cost and the help screens were measured. The rest is judgement that errs on the safe
// side, because wrongly calling something a screen costs a person one trip to the terminal.
// desktop/frontend/src/lib/slash.ts draws its list from the same words, and
// TestScreenCommandsMatchTheApp holds the two together.
var claudeScreens = []string{
	"/config", "/usage", "/cost", "/help", "/model", "/resume", "/agents", "/mcp", "/permissions", "/login", "/logout",
	"/doctor", "/memory", "/hooks", "/rewind", "/status", "/theme", "/vim", "/terminal-setup",
}

// ScreenCommand says whether word is known to open a screen in this agent. An agent nobody has
// measured knows none, so its commands take the ordinary path.
func ScreenCommand(k Kind, word string) bool {
	return k == Claude && slices.Contains(claudeScreens, word)
}
