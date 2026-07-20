package vtterm

import (
	"os/exec"
	"testing"
	"time"
)

func TestSpikeAgentAttach(t *testing.T) {
	// A developer spike: it attaches to a real `tmux -L lola` session and just
	// logs the render, so it needs the tmux binary on PATH. CI runners (and any
	// machine without tmux) don't have it — skip rather than fail, the same way
	// the rest of the suite keeps external tools behind exec seams.
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	argv := []string{"tmux", "-L", "lola", "attach-session", "-t", "=spike-agent"}
	term, err := New(exec.Command(argv[0], argv[1:]...), 80, 20)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer term.Close()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-term.Frames():
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Logf("exited=%v", term.Exited())
	t.Logf("RENDER:\n%s", func() string {
		ls := term.Render()
		out := ""
		for _, l := range ls {
			out += l + "\n"
		}
		return out
	}())
}
