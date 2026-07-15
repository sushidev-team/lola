package vtterm
import ("os/exec";"testing";"time")
func TestSpikeAgentAttach(t *testing.T) {
	argv := []string{"tmux","-L","lola","attach-session","-t","=spike-agent"}
	term, err := New(exec.Command(argv[0], argv[1:]...), 80, 20)
	if err != nil { t.Fatalf("New: %v", err) }
	defer term.Close()
	deadline := time.Now().Add(1500*time.Millisecond)
	for time.Now().Before(deadline) {
		select { case <-term.Frames(): case <-time.After(100*time.Millisecond): }
	}
	t.Logf("exited=%v", term.Exited())
	t.Logf("RENDER:\n%s", func() string { ls := term.Render(); out := ""; for _, l := range ls { out += l+"\n" }; return out }())
}
