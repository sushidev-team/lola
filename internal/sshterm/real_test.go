package sshterm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBootstrapInRealBash runs the real script through real bash under a real tmux with
// a real pipe-pane, on a private server. It proves what no fake can: that the hooks fire
// and the bytes reach the parser. It skips where bash or tmux is missing.
func TestBootstrapInRealBash(t *testing.T) {
	for _, b := range []string{"bash", "tmux", "sh"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skip(b + " not available")
		}
	}
	dir, err := os.MkdirTemp("/tmp", "lolart")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	home := filepath.Join(dir, "home")
	_ = os.MkdirAll(filepath.Join(home, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("export FROM_PROFILE=yes\nPROMPT_COMMAND='true'\nPS1='$ '\n"), 0o644)
	sock := filepath.Join(dir, "s")
	tm := func(args ...string) string {
		out, _ := exec.Command("tmux", append([]string{"-S", sock, "-f", "/dev/null"}, args...)...).CombinedOutput()
		return string(out)
	}
	t.Cleanup(func() { tm("kill-server") })

	const n = "n0nce"
	script := filepath.Join(dir, "boot.sh")
	_ = os.WriteFile(script, []byte(Bootstrap(n)), 0o644)
	raw := filepath.Join(dir, "raw")
	tm("new-session", "-d", "-s", "t", "-x", "120", "-y", "30",
		"cd "+home+" && env -i HOME="+home+" SHELL=/bin/bash PATH="+os.Getenv("PATH")+" TERM=xterm sh "+script)
	tm("pipe-pane", "-t", "t", "cat >> "+raw)

	var log []string
	var output strings.Builder
	p := NewParser(n, Events{
		Start:  func(c string) { log = append(log, "start "+c) },
		Output: func(b []byte) { output.Write(b) },
		End: func(exit int, cwd string, cmd, full bool) {
			log = append(log, fmt.Sprintf("end %d %s %v %v", exit, filepath.Base(cwd), cmd, full))
		},
	})
	send := func(line string) {
		tm("send-keys", "-t", "t", "-l", line)
		tm("send-keys", "-t", "t", "Enter")
		time.Sleep(700 * time.Millisecond)
	}
	time.Sleep(time.Second)
	send("echo hello-$FROM_PROFILE")
	send("false")
	send("cd sub")
	send("echo twice")
	send("echo twice")
	send(" echo hidden-from-timeline")
	send("export KEPT=1")
	send("echo kept-$KEPT | tr a-z A-Z")
	send("( exit 7 )")
	time.Sleep(500 * time.Millisecond)

	b, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	p.Feed(b)
	got := strings.Join(log, "\n")
	want := []string{
		"start echo hello-$FROM_PROFILE\nend 0 home true false",
		"start false\nend 1 home true false",
		"start cd sub\nend 0 sub true false",
		"start echo twice\nend 0 sub true false\nstart echo twice\nend 0 sub true false",
		"end 0 sub false false", // the hidden line: a prompt, and no command
		"start export KEPT=1\nend 0 sub true false",
		"start echo kept-$KEPT | tr a-z A-Z\nend 0 sub true false",
		"start ( exit 7 )\nend 7 sub true false",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "hidden-from-timeline") || strings.Contains(output.String(), "HIDDEN") {
		t.Errorf("a space-prefixed line reached the record:\n%s", got)
	}
	o := output.String()
	for _, w := range []string{"hello-yes", "KEPT-1"} {
		if !strings.Contains(o, w) {
			t.Errorf("output missing %q: %q", w, o)
		}
	}
	if pane := tm("capture-pane", "-p", "-t", "t"); strings.Contains(pane, "6973") || strings.Contains(pane, "LOLA") {
		t.Errorf("a marker reached the screen:\n%s", pane)
	}
}

func TestTrackedReportsAndKeepsTheExitStatus(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	for _, c := range []struct {
		body string
		code int
		cwd  string
	}{
		{"cd " + dir, 0, dir},
		{"cd " + dir + "; exit 3", 3, dir},
		{"cd /nonexistent-lola || exit 1", 1, ""},
	} {
		var stderr strings.Builder
		f := NewCwdFilter("n", &stderr)
		cmd := exec.Command("sh", "-c", Tracked("n", c.body))
		cmd.Stderr = f
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		cwd := f.Close()
		if code != c.code {
			t.Errorf("%q: exit %d, want %d", c.body, code, c.code)
		}
		if c.cwd != "" && !strings.HasSuffix(cwd, filepath.Base(c.cwd)) {
			t.Errorf("%q: cwd %q", c.body, cwd)
		}
		if cwd == "" {
			t.Errorf("%q: no cwd reported", c.body)
		}
		if strings.Contains(stderr.String(), "__LOLA") {
			t.Errorf("%q: the marker reached the output: %q", c.body, stderr.String())
		}
	}
}
