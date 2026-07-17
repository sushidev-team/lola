package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTmux installs a shell script standing in for the tmux binary: it
// appends its argv (one line per invocation) to <dir>/args.log, emits the
// canned stdout/stderr, and exits with code. Pattern mirrors
// internal/tmux fake-bin helper; no real tmux is ever run.
func fakeTmux(t *testing.T, stdout, stderr string, code int) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "tmux")
	argsLog = filepath.Join(dir, "args.log")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("echo \"$@\" >> " + argsLog + "\n")
	if stdout != "" {
		b.WriteString("cat <<'EOF'\n" + stdout + "\nEOF\n")
	}
	if stderr != "" {
		b.WriteString("cat <<'EOF' >&2\n" + stderr + "\nEOF\n")
	}
	fmt.Fprintf(&b, "exit %d\n", code)
	if err := os.WriteFile(bin, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

func loggedArgs(t *testing.T, argsLog string) string {
	t.Helper()
	b, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(b), "\n")
}

func TestDirPrecedence(t *testing.T) {
	if got := (&Client{Dir: "/some/where"}).dir(); got != "/some/where" {
		t.Fatalf("explicit Dir: got %q, want /some/where", got)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir to compare fallback against")
	}
	if got := (&Client{}).dir(); got != home {
		t.Fatalf("empty Dir: got %q, want home %q", got, home)
	}
}

// TestRunPinsCwd verifies every tmux invocation executes from Client.Dir, so
// the long-lived tmux server can never inherit (and outlive) a deleted cwd.
func TestRunPinsCwd(t *testing.T) {
	dir := t.TempDir()
	pwdLog := filepath.Join(dir, "pwd.log")
	bin := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\npwd -P >> " + pwdLog + "\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// A distinct dir the command must run from; resolve symlinks so macOS's
	// /var -> /private/var does not defeat the comparison.
	runDir := t.TempDir()
	want, err := filepath.EvalSymlinks(runDir)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Bin: bin, Dir: runDir}
	if _, _, err := c.run(context.Background(), "kill-session", "-t", "=x"); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimSpace(loggedArgs(t, pwdLog))
	if got != want {
		t.Fatalf("tmux ran from %q, want %q", got, want)
	}
}

func TestListSessionsParsesFormatLines(t *testing.T) {
	fixture := "main\t1720000000\t1\nlola-NORI-12-1\t1720003600\t0"
	bin, argsLog := fakeTmux(t, fixture, "", 0)
	c := &Client{Bin: bin}

	got, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	want := []Session{
		{Name: "main", Created: time.Unix(1720000000, 0), Attached: true},
		{Name: "lola-NORI-12-1", Created: time.Unix(1720003600, 0), Attached: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sessions, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].Created.Equal(want[i].Created) || got[i].Name != want[i].Name || got[i].Attached != want[i].Attached {
			t.Errorf("session[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	wantArgs := "-L lola ls -F #{session_name}\t#{session_created}\t#{session_attached}"
	if args := loggedArgs(t, argsLog); args != wantArgs {
		t.Errorf("invoked %q, want %q", args, wantArgs)
	}
}

func TestListSessionsNoServerIsEmptyNotError(t *testing.T) {
	bin, _ := fakeTmux(t, "", "no server running on /private/tmp/tmux-501/default", 1)
	c := &Client{Bin: bin}

	got, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions with no server: want nil error, got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("want empty non-nil slice, got %#v", got)
	}
}

func TestListSessionsOtherFailureWrapsStderr(t *testing.T) {
	bin, _ := fakeTmux(t, "", "error connecting to /private/tmp/tmux-501/default (Permission denied)", 1)
	c := &Client{Bin: bin}

	_, err := c.ListSessions(context.Background())
	if err == nil {
		t.Fatal("want error for non-no-server failure, got nil")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("error %q does not wrap stderr", err)
	}
}

func TestHasUsesExactMatchTarget(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if !c.Has(context.Background(), "lola-NORI-12-1") {
		t.Error("Has: want true on exit 0")
	}
	if args := loggedArgs(t, argsLog); args != "-L lola has-session -t =lola-NORI-12-1" {
		t.Errorf("invoked %q, want exact-match target =lola-NORI-12-1 on lola socket", args)
	}
}

func TestHasMissingSession(t *testing.T) {
	bin, _ := fakeTmux(t, "", "can't find session: =nope", 1)
	c := &Client{Bin: bin}

	if c.Has(context.Background(), "nope") {
		t.Error("Has: want false on exit 1")
	}
}

func TestDefaultServerSessionsNoLFlagFiltersByPrefix(t *testing.T) {
	fixture := "main\nlola-NORI-12-1\nwork\nlola-KOMBU-3-1"
	bin, argsLog := fakeTmux(t, fixture, "", 0)

	got, err := DefaultServerSessions(context.Background(), bin, "lola-")
	if err != nil {
		t.Fatalf("DefaultServerSessions: %v", err)
	}
	want := []string{"lola-NORI-12-1", "lola-KOMBU-3-1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// Crucially NO "-L": this must target the user's DEFAULT tmux server.
	if args := loggedArgs(t, argsLog); args != "list-sessions -F #{session_name}" {
		t.Errorf("invoked %q, want a NO-L list-sessions on the default server", args)
	}
}

func TestDefaultServerSessionsNoServerIsEmptyNotError(t *testing.T) {
	bin, _ := fakeTmux(t, "", "no server running on /private/tmp/tmux-501/default", 1)

	got, err := DefaultServerSessions(context.Background(), bin, "lola-")
	if err != nil {
		t.Fatalf("no default server: want nil error, got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("want empty non-nil slice, got %#v", got)
	}
}

func TestDefaultServerSessionsOtherFailureWrapsStderr(t *testing.T) {
	bin, _ := fakeTmux(t, "", "error connecting to /private/tmp/tmux-501/default (Permission denied)", 1)

	_, err := DefaultServerSessions(context.Background(), bin, "lola-")
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("want error wrapping stderr for non-no-server failure, got %v", err)
	}
}

func TestCapturePaneArgsAndOutput(t *testing.T) {
	fixture := "\x1b[32m$ make test\x1b[0m\nok"
	bin, argsLog := fakeTmux(t, fixture, "", 0)
	c := &Client{Bin: bin}

	out, err := c.CapturePane(context.Background(), "main", 200)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if out != fixture+"\n" {
		t.Errorf("CapturePane output %q, want rendered screen incl. ANSI %q", out, fixture+"\n")
	}
	// capture-pane takes a target-PANE: "=main:" (exact session + active pane),
	// NOT "=main" — a bare "=name" is not a valid pane target ("can't find pane").
	if args := loggedArgs(t, argsLog); args != "-L lola capture-pane -p -e -t =main: -S -200" {
		t.Errorf("invoked %q, want -L lola capture-pane -p -e -t =main: -S -200", args)
	}
}

func TestSendKeysLiteralThenEnter(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if err := c.SendKeys(context.Background(), "main", "fix the CI failure"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	// send-keys also takes a target-PANE, so "=main:" (not "=main").
	want := "-L lola send-keys -t =main: -l fix the CI failure\n-L lola send-keys -t =main: Enter"
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked:\n%s\nwant literal text then Enter:\n%s", args, want)
	}
}

func TestSendKeysErrorWrapsStderr(t *testing.T) {
	bin, _ := fakeTmux(t, "", "can't find session: =gone", 1)
	c := &Client{Bin: bin}

	err := c.SendKeys(context.Background(), "gone", "hello")
	if err == nil || !strings.Contains(err.Error(), "can't find session") {
		t.Errorf("want error wrapping stderr, got %v", err)
	}
}

func TestNewSessionArgs(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if err := c.NewSession(context.Background(), "lola-NORI-12-1", "/work/nori", "claude"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	want := "-L lola new-session -d -s lola-NORI-12-1 -c /work/nori claude"
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked %q, want %q", args, want)
	}
}

func TestNewSessionOmitsEmptyCommand(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if err := c.NewSession(context.Background(), "s1", "/work", ""); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	want := "-L lola new-session -d -s s1 -c /work"
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked %q, want default-shell form %q", args, want)
	}
}

func TestKillSessionArgs(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if err := c.KillSession(context.Background(), "lola-NORI-12-1"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if args := loggedArgs(t, argsLog); args != "-L lola kill-session -t =lola-NORI-12-1" {
		t.Errorf("invoked %q, want exact-match kill-session target on lola socket", args)
	}
}

func TestKillSessionMissingIsError(t *testing.T) {
	bin, _ := fakeTmux(t, "", "can't find session: =gone", 1)
	c := &Client{Bin: bin}

	err := c.KillSession(context.Background(), "gone")
	if err == nil || !strings.Contains(err.Error(), "can't find session") {
		t.Errorf("want error wrapping stderr for missing session, got %v", err)
	}
}

func TestAttachArgs(t *testing.T) {
	c := &Client{Bin: "tmux"}
	got := c.AttachArgs("main")
	want := []string{"tmux", "-L", "lola", "attach-session", "-t", "=main"}
	if len(got) != len(want) {
		t.Fatalf("AttachArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AttachArgs = %v, want %v", got, want)
		}
	}

	abs := &Client{Bin: "/opt/homebrew/bin/tmux"}
	if args := abs.AttachArgs("main"); args[0] != "/opt/homebrew/bin/tmux" {
		t.Errorf("AttachArgs[0] = %q, want configured absolute bin", args[0])
	}
}

func TestAttachArgsCustomSocket(t *testing.T) {
	c := &Client{Bin: "tmux", SocketName: "lolatest"}
	got := c.AttachArgs("main")
	want := []string{"tmux", "-L", "lolatest", "attach-session", "-t", "=main"}
	if len(got) != len(want) {
		t.Fatalf("AttachArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AttachArgs = %v, want %v", got, want)
		}
	}
}

func TestCustomSocketNameOnEveryCommand(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin, SocketName: "lolatest"}

	if err := c.KillSession(context.Background(), "s1"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if args := loggedArgs(t, argsLog); args != "-L lolatest kill-session -t =s1" {
		t.Errorf("invoked %q, want configured socket lolatest on the command", args)
	}
}

func TestAvailable(t *testing.T) {
	bin, _ := fakeTmux(t, "", "", 0)

	if !(&Client{Bin: bin}).Available() {
		t.Error("Available: want true for existing executable (absolute path)")
	}
	if (&Client{Bin: filepath.Join(t.TempDir(), "missing")}).Available() {
		t.Error("Available: want false for missing binary")
	}

	// A bare name is resolved via PATH.
	t.Setenv("PATH", filepath.Dir(bin))
	if !(&Client{Bin: "tmux"}).Available() {
		t.Error("Available: want true for bare name resolved via PATH")
	}
}

func TestConfigureSessionEmitsChromeArgv(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	err := c.ConfigureSession(context.Background(), "lola-NORI-12-1", SessionChrome{
		Brand:       "LOLA",
		Label:       "NORI-12",
		StatusRight: "working",
		DetachKey:   "F12",
		Mouse:       true,
	})
	if err != nil {
		t.Fatalf("ConfigureSession: %v", err)
	}
	// Every set-option is per-session (-t, never -g); the bind-key is a
	// root-table entry confined to the lola socket (leading -L lola).
	want := strings.Join([]string{
		"-L lola set-option -t =lola-NORI-12-1 status on",
		"-L lola set-option -t =lola-NORI-12-1 status-left-length 80",
		"-L lola set-option -t =lola-NORI-12-1 status-right-length 80",
		"-L lola set-option -t =lola-NORI-12-1 status-left LOLA | NORI-12",
		"-L lola set-option -t =lola-NORI-12-1 status-right working | detach F12",
		"-L lola set-option -t =lola-NORI-12-1 mouse on",
		"-L lola bind-key -n F12 detach-client",
	}, "\n")
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("ConfigureSession argv:\n%s\nwant:\n%s", args, want)
	}
}

func TestConfigureSessionDefaultsNoDetachBindingNoMouse(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	err := c.ConfigureSession(context.Background(), "s1", SessionChrome{
		Label:       "NORI-12",
		StatusRight: "idle",
	})
	if err != nil {
		t.Fatalf("ConfigureSession: %v", err)
	}
	got := loggedArgs(t, argsLog)
	if strings.Contains(got, "bind-key") {
		t.Errorf("empty DetachKey must emit no bind-key, got:\n%s", got)
	}
	if strings.Contains(got, "mouse") {
		t.Errorf("Mouse=false must emit no mouse set-option, got:\n%s", got)
	}
	// Brand defaults to LOLA; detach hint defaults to C-b d.
	if !strings.Contains(got, "-L lola set-option -t =s1 status-left LOLA | NORI-12") {
		t.Errorf("status-left should default brand to LOLA, got:\n%s", got)
	}
	if !strings.Contains(got, "-L lola set-option -t =s1 status-right idle | detach C-b d") {
		t.Errorf("status-right should carry status + default detach hint, got:\n%s", got)
	}
}

func TestConfigureSessionBestEffortJoinsErrors(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "boom", 1)
	c := &Client{Bin: bin}

	err := c.ConfigureSession(context.Background(), "s1", SessionChrome{DetachKey: "F12"})
	if err == nil {
		t.Fatal("ConfigureSession: want joined error when tmux fails")
	}
	// Best-effort: every command is attempted despite earlier failures
	// (5 per-session set-options + 1 bind-key).
	lines := strings.Split(loggedArgs(t, argsLog), "\n")
	if len(lines) != 6 {
		t.Errorf("want all 6 commands attempted, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
}

// caseTmux writes a fake tmux that logs argv and exits per a `case "$*"` body,
// so one invocation can answer has-session (existence) differently from the
// window commands — which the fixed-code fakeTmux cannot.
func caseTmux(t *testing.T, caseBody string) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "tmux")
	argsLog = filepath.Join(dir, "args.log")
	script := "#!/bin/sh\necho \"$@\" >> " + argsLog + "\ncase \"$*\" in\n" + caseBody + "\n*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

// TestBuildViewer pins the exact tmux plumbing that assembles the tab-per-agent
// viewer: a holder session, one link-window per tab at contiguous indices 1..N
// with a rename, the placeholder window dropped, and the first real tab
// selected. has-session returns false so no pre-existing viewer is killed.
func TestBuildViewer(t *testing.T) {
	bin, argsLog := caseTmux(t, "*has-session*) exit 1 ;;")
	c := &Client{Bin: bin}
	tabs := []ViewerTab{{Session: "lola-p-a", Name: "a"}, {Session: "lola-p-b", Name: "b"}}
	if err := c.BuildViewer(context.Background(), "lola_viewer", "/wd", tabs); err != nil {
		t.Fatalf("BuildViewer: %v", err)
	}
	log := loggedArgs(t, argsLog)
	for _, w := range []string{
		"new-session -d -s lola_viewer -c /wd",
		"link-window -s =lola-p-a:0 -t =lola_viewer:1",
		"rename-window -t =lola_viewer:1 a",
		"link-window -s =lola-p-b:0 -t =lola_viewer:2",
		"rename-window -t =lola_viewer:2 b",
		"kill-window -t =lola_viewer:0",
		"select-window -t =lola_viewer:1",
	} {
		if !strings.Contains(log, w) {
			t.Errorf("BuildViewer must issue %q\nfull log:\n%s", w, log)
		}
	}
	// A clean build over a non-existent viewer must never kill a session.
	if strings.Contains(log, "kill-session") {
		t.Errorf("clean build must not kill-session:\n%s", log)
	}
}

// A viewer name under the "lola-" agent prefix is rejected before any tmux call,
// so the daemon's Adopt scan can never mistake the viewer for an orphaned agent.
func TestBuildViewerRejectsAgentPrefix(t *testing.T) {
	c := &Client{Bin: "/nonexistent/tmux"} // must error before exec
	err := c.BuildViewer(context.Background(), "lola-oops", "/wd", []ViewerTab{{Session: "lola-x"}})
	if err == nil {
		t.Fatal("BuildViewer must reject a viewer name under the lola- agent prefix")
	}
}

// When no window links (every agent vanished between listing and linking), the
// half-built viewer is torn down and an error returned, so the caller never
// attaches to an empty placeholder shell.
func TestBuildViewerAllLinksFailTearsDown(t *testing.T) {
	bin, argsLog := caseTmux(t, "*has-session*) exit 1 ;;\n*link-window*) exit 1 ;;")
	c := &Client{Bin: bin}
	err := c.BuildViewer(context.Background(), "lola_viewer", "/wd", []ViewerTab{{Session: "lola-p-a"}, {Session: "lola-p-b"}})
	if err == nil {
		t.Fatal("BuildViewer must error when nothing links")
	}
	if log := loggedArgs(t, argsLog); !strings.Contains(log, "kill-session -t =lola_viewer") {
		t.Errorf("a viewer with no linked windows must be torn down:\n%s", log)
	}
}

func TestBuildViewerEmptyTabsErrors(t *testing.T) {
	if err := (&Client{Bin: "/nonexistent/tmux"}).BuildViewer(context.Background(), "lola_viewer", "/wd", nil); err == nil {
		t.Fatal("BuildViewer must error with no tabs")
	}
}
