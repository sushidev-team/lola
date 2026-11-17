package agent

// feedCap is the Feed half of Caps, apart so the reader's work never meets the gates'.
//
// Claude and codex are on. The codex reader (internal/agentfeed) was checked against
// rollouts of codex-cli 0.167 to 0.169, and a capture of those records sits beside the
// test that reads it. opencode has no reader at all, because its local data predates its
// current write path.
func feedCap(k Kind) bool {
	switch Parse(string(k)) {
	case Claude, Codex:
		return true
	}
	return false
}
