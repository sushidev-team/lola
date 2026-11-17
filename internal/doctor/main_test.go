package doctor

import (
	"os"
	"testing"
)

// TestMain keeps every test off the machine's real Claude Code data: Check looks for the
// newest lola transcript, and a test run must never read the owner's.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lola-doctor-claude")
	if err != nil {
		panic(err)
	}
	os.Setenv("CLAUDE_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
