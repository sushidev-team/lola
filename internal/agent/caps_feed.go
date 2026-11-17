package agent

// feedCap is the Feed half of Caps, apart so the reader's work never meets the gates'.
//
// Only claude is on. internal/agentfeed has a codex reader, but its fixtures are synthetic:
// it was written from a survey of the format and has never read a rollout the app could
// show. Turn codex on here with a real capture checked in beside the test that reads it.
// opencode has no reader at all, because its local data predates its current write path.
func feedCap(k Kind) bool {
	return Parse(string(k)) == Claude
}
