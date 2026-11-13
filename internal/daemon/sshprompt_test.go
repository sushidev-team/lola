package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/tmux"
)

const testSecret = "hunter2-correct-horse"

type fakePrompter struct {
	*fakeTapper
	in         tmux.PaneInput
	inErr      error
	pasted     []string
	pasteErr   error
	interrupt  int
	pane       string
	captureErr error
}

func (f *fakePrompter) PaneInput(context.Context, string) (tmux.PaneInput, error) {
	return f.in, f.inErr
}
func (f *fakePrompter) PasteSecret(_ context.Context, _, s string) error {
	if f.pasteErr != nil {
		return f.pasteErr
	}
	f.pasted = append(f.pasted, s)
	return nil
}
func (f *fakePrompter) SendInterrupt(context.Context, string) error { f.interrupt++; return nil }

func (f *fakePrompter) CaptureFrom(ctx context.Context, n string, back int) (string, error) {
	if f.captureErr != nil {
		return "", f.captureErr
	}
	if f.pane != "" {
		return f.pane, nil
	}
	return f.fakeTapper.CaptureFrom(ctx, n, back)
}

func promptRig(t *testing.T) (*sshRig, string, *sshTapReader, *fakePrompter) {
	t.Helper()
	r, conn, tap, ft := tapRig(t)
	fp := &fakePrompter{fakeTapper: ft}
	r.d.ssh.tmuxFn = func() sshTmux { return fp }
	return r, conn, tap, fp
}

func (r *sshRig) wirePrompt(t *testing.T, conn string) *protocol.SSHPromptInfo {
	t.Helper()
	data, err := r.d.handleSSHTimeline(protocol.SSHTimelineArgs{Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range data.Connections {
		if c.ID == conn {
			return c.Prompt
		}
	}
	t.Fatalf("no connection %s", conn)
	return nil
}

func (f *fakePrompter) show(line string, row int) {
	f.in = tmux.PaneInput{Open: true, Row: row, Text: line}
}

func noSecretOnDisk(t *testing.T, home string) {
	t.Helper()
	err := filepath.WalkDir(home, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr == nil && strings.Contains(string(raw), testSecret) {
			t.Errorf("the secret is in %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestACommandPromptReachesTheWireAndNoFile(t *testing.T) {
	r, conn, tap, _ := promptRig(t)
	tap.start("sudo ls")
	id := tap.block.ID
	tap.output([]byte("[sudo] password for m: "))
	tap.promptRead()
	q := r.wirePrompt(t, conn)
	if q == nil || q.Kind != protocol.SSHPromptSecret || q.Stage != protocol.SSHPromptCommand || q.Line != "[sudo] password for m:" || q.Block != id || q.Command != "sudo ls" {
		t.Fatalf("prompt = %+v", q)
	}
	// A pending question lives in memory only: a restart must not revive a stale card.
	store, _ := r.d.sshStore("s1", false)
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(r.home, "state", "ssh", "s1", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"prompt"`) || strings.Contains(string(raw), q.ID) {
		t.Fatalf("a prompt reached state.json: %s", raw)
	}
	// The glance flags it for a human.
	g := r.d.sshGlance("s1")
	if !g.NeedsHuman || !strings.Contains(g.NeedsHumanWhy, "asking") || g.Connections[0].Prompt == nil {
		t.Fatalf("glance = %+v", g)
	}
}

func TestOrdinaryOutputAndItsEndClearAPrompt(t *testing.T) {
	r, conn, tap, _ := promptRig(t)
	tap.start("sudo ls")
	tap.output([]byte("Password: "))
	tap.promptRead()
	if r.wirePrompt(t, conn) == nil {
		t.Fatal("no prompt")
	}
	tap.output([]byte("\r\nworking\r\n"))
	tap.promptRead()
	if q := r.wirePrompt(t, conn); q != nil {
		t.Fatalf("output moved on, still %+v", q)
	}
	tap.output([]byte("Password: "))
	tap.promptRead()
	tap.end(1, "/srv", true, false)
	if q := r.wirePrompt(t, conn); q != nil {
		t.Fatalf("the command ended, still %+v", q)
	}
}

func TestAMidLineStallIsOfferedAsTheTerminal(t *testing.T) {
	r, conn, tap, _ := promptRig(t)
	tap.start("terraform apply")
	tap.output([]byte("Enter a value: "))
	tap.promptRead()
	data, _ := r.d.handleSSHTimeline(protocol.SSHTimelineArgs{Session: "s1"})
	if c := data.Connections[0]; c.ID != conn || c.Prompt != nil || !c.TermOpenLine {
		t.Fatalf("a question nobody recognized must degrade to the terminal hint: %+v", c)
	}
}

func TestAnAnswerGoesToThePaneAndNowhereElse(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	tap.start("sudo ls")
	tap.output([]byte("[sudo] password for m: "))
	tap.promptRead()
	fp.show("$ sudo ls\n[sudo] password for m: ", 3)
	q := r.wirePrompt(t, conn)
	d, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret})
	if err != nil || d.Status != protocol.SSHPromptSent {
		t.Fatalf("answer: %+v %v", d, err)
	}
	if len(fp.pasted) != 1 || fp.pasted[0] != testSecret {
		t.Fatalf("pasted = %q", fp.pasted)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), testSecret) {
		t.Fatal("the reply echoed the secret")
	}
	if r.wirePrompt(t, conn) != nil {
		t.Fatal("an answered prompt is still pending")
	}
	// The same id again: a stale card must not type.
	_, err = r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret})
	if !errors.Is(err, sshPromptGone) || len(fp.pasted) != 1 {
		t.Fatalf("second answer: %v, pasted %d", err, len(fp.pasted))
	}
	id := tap.block.ID
	tap.output([]byte("\r\nlisting\r\n"))
	tap.end(0, "/srv", true, false)
	store, _ := r.d.sshStore("s1", false)
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	noSecretOnDisk(t, r.home)
	out, err := r.d.handleSSHOutput(protocol.SSHOutputArgs{Session: "s1", Block: id, Stream: protocol.SSHStreamStdout})
	if err != nil || !strings.Contains(out.Data, "listing") {
		t.Fatalf("the output after an answer is kept: %+v %v", out, err)
	}
}

func TestARequestWithNoPendingPromptIsRefused(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	send := func(id string) error {
		_, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: id, Action: protocol.SSHPromptSend, Secret: "x"})
		return err
	}
	if err := send("nope"); !errors.Is(err, sshPromptGone) {
		t.Fatalf("no prompt: %v", err)
	}
	tap.start("sudo ls")
	tap.output([]byte("Password: "))
	tap.promptRead()
	q := r.wirePrompt(t, conn)
	if err := send("wrong-id"); !errors.Is(err, sshPromptGone) {
		t.Fatalf("wrong id: %v", err)
	}
	// The pending record says waiting, the pane says otherwise: the pane wins.
	fp.in = tmux.PaneInput{Open: false}
	if err := send(q.ID); !errors.Is(err, sshPromptGone) {
		t.Fatalf("pane moved on: %v", err)
	}
	if len(fp.pasted) != 0 {
		t.Fatalf("typed into a shell: %q", fp.pasted)
	}
	tap.output([]byte("x"))
	tap.output([]byte("\rPassword: "))
	tap.promptRead()
	q = r.wirePrompt(t, conn)
	fp.show("$ other thing, not asking", 4)
	if err := send(q.ID); !errors.Is(err, sshPromptGone) || len(fp.pasted) != 0 {
		t.Fatalf("a different line: %v %q", err, fp.pasted)
	}
}

func TestAPaneThatCannotAnswerFailsClosedAndKeepsTheCard(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	tap.start("sudo ls")
	tap.output([]byte("Password: "))
	tap.promptRead()
	q := r.wirePrompt(t, conn)
	fp.inErr = errors.New("tmux down")
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret}); err == nil || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("err = %v", err)
	}
	fp.inErr = nil
	fp.show("Password: ", 1)
	fp.pasteErr = errors.New("tmux paste-buffer: " + "no pane")
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret}); err == nil || strings.Contains(err.Error(), "paste-buffer") {
		t.Fatalf("err = %v", err)
	}
	if got := r.wirePrompt(t, conn); got == nil || got.ID != q.ID {
		t.Fatalf("a failed send should leave the card: %+v", got)
	}
}

func TestAnAnswerRefusesWhatATerminalWouldActOn(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	tap.start("sudo ls")
	tap.output([]byte("Password: "))
	tap.promptRead()
	fp.show("Password: ", 1)
	q := r.wirePrompt(t, conn)
	for _, s := range []string{"", "a\nrm -rf /", "a\rb", "a\x03", "a\x1b[A", strings.Repeat("x", protocol.SSHMaxPromptSecretBytes+1)} {
		if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: s}); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	if len(fp.pasted) != 0 || r.wirePrompt(t, conn) == nil {
		t.Fatalf("pasted %q", fp.pasted)
	}
}

func TestAnEchoedAnswerStaysOutOfTheBlockAndTheHandback(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	tap.start("fake-prompt")
	id := tap.block.ID
	tap.output([]byte("Password: "))
	tap.promptRead()
	fp.show("Password: ", 1)
	q := r.wirePrompt(t, conn)
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret}); err != nil {
		t.Fatal(err)
	}
	// A fake prompt with echo on: the pty hands the answer straight back, split over reads.
	tap.output([]byte(testSecret[:5]))
	tap.output([]byte(testSecret[5:] + "\r\nafter\r\n"))
	tap.end(0, "/srv", true, false)
	store, _ := r.d.sshStore("s1", false)
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	noSecretOnDisk(t, r.home)
	out, _ := r.d.handleSSHOutput(protocol.SSHOutputArgs{Session: "s1", Block: id, Stream: protocol.SSHStreamStdout})
	if strings.Contains(out.Data, testSecret) || !strings.Contains(out.Data, "after") {
		t.Fatalf("output = %q", out.Data)
	}
	if b := r.block(t, "s1", id); strings.Contains(b.Preview, testSecret) {
		t.Fatalf("preview = %q", b.Preview)
	}
	// The pane's history holds it, and a handback transcript reads that history.
	r.tm.mu.Lock()
	tab := r.conn(t, "s1", conn).Tab
	r.tm.lines[tab] = "Password: " + testSecret
	r.tm.mu.Unlock()
	p, err := r.d.handleSSHHandbackPreview(context.Background(), protocol.SSHConnArgs{Conn: conn})
	if err != nil || strings.Contains(p.Transcript, testSecret) {
		t.Fatalf("handback preview: %+v %v", p, err)
	}
}

func TestAnHonestAnswerCostsNoOutputAndNoTranscript(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	tap.start("sudo ls")
	id := tap.block.ID
	tap.output([]byte("Password: "))
	tap.promptRead()
	fp.show("Password: ", 1)
	q := r.wirePrompt(t, conn)
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret}); err != nil {
		t.Fatal(err)
	}
	tap.output([]byte("\r\nfile.txt\r\n"))
	tap.end(0, "/srv", true, false)
	out, _ := r.d.handleSSHOutput(protocol.SSHOutputArgs{Session: "s1", Block: id, Stream: protocol.SSHStreamStdout})
	if !strings.Contains(out.Data, "file.txt") {
		t.Fatalf("output = %q", out.Data)
	}
	store, _ := r.d.sshStore("s1", false)
	c, _ := sshConnCopy(store, conn)
	if c.AnswerEchoTab != "" {
		t.Fatalf("no echo happened, yet the transcript is withheld: %q", c.AnswerEchoTab)
	}
}

func TestTheSameQuestionComingBackCountsAsAFailedAnswer(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	tap.start("sudo ls")
	tap.output([]byte("[sudo] password for m: "))
	tap.promptRead()
	fp.show("[sudo] password for m: ", 1)
	q := r.wirePrompt(t, conn)
	if q.Attempt != 0 {
		t.Fatalf("attempt = %d", q.Attempt)
	}
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: "bad"}); err != nil {
		t.Fatal(err)
	}
	tap.output([]byte("\r\nSorry, try again.\r\n[sudo] password for m: "))
	tap.promptRead()
	again := r.wirePrompt(t, conn)
	if again == nil || again.Attempt != 1 || again.ID == q.ID {
		t.Fatalf("retry = %+v", again)
	}
}

func TestAConfirmSendsTheWordItsQuestionTakes(t *testing.T) {
	for _, tc := range []struct{ line, action, want string }{
		{"Are you sure you want to continue connecting (yes/no/[fingerprint])? ", protocol.SSHPromptYes, "yes"},
		{"Are you sure you want to continue connecting (yes/no/[fingerprint])? ", protocol.SSHPromptNo, "no"},
		{"Continue? [Y/n] ", protocol.SSHPromptYes, "y"},
		{"Continue? [Y/n] ", protocol.SSHPromptNo, "n"},
	} {
		r, conn, tap, fp := promptRig(t)
		tap.start("apt install")
		tap.output([]byte(tc.line))
		tap.promptRead()
		fp.show(tc.line, 1)
		q := r.wirePrompt(t, conn)
		if q.Kind != protocol.SSHPromptConfirm {
			t.Fatalf("%q kind %q", tc.line, q.Kind)
		}
		// Text cannot be sent to a yes/no, and a yes cannot answer a secret.
		if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: "text"}); err == nil {
			t.Fatal("typed text into a yes/no")
		}
		q = r.wirePrompt(t, conn)
		if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: tc.action}); err != nil {
			t.Fatal(err)
		}
		if len(fp.pasted) != 1 || fp.pasted[0] != tc.want {
			t.Fatalf("%q %s: pasted %q", tc.line, tc.action, fp.pasted)
		}
	}
}

func TestCancelPressesCtrlC(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	tap.start("sudo ls")
	tap.output([]byte("Password: "))
	tap.promptRead()
	fp.show("Password: ", 1)
	q := r.wirePrompt(t, conn)
	d, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptCancel})
	if err != nil || d.Status != protocol.SSHPromptCancelled || fp.interrupt != 1 || len(fp.pasted) != 0 {
		t.Fatalf("%+v %v interrupt=%d pasted=%q", d, err, fp.interrupt, fp.pasted)
	}
}

func signInRig(t *testing.T) (*sshRig, string, *fakePrompter) {
	t.Helper()
	r, conn, _, fp := promptRig(t)
	store, _ := r.d.sshStore("s1", false)
	_ = store.Update(func(st *sshstore.State) error {
		c := st.Conn(conn)
		c.State, c.TabAlive = protocol.SSHConnAuthenticating, true
		return nil
	})
	return r, conn, fp
}

func TestSignInReadsThePaneAndTellsARetryFromAWait(t *testing.T) {
	r, conn, fp := signInRig(t)
	c := r.conn(t, "s1", conn)
	read := func() { r.d.sshReadSignInPrompt(conn, c.Tab, true) }

	fp.in = tmux.PaneInput{Open: false}
	read()
	if q := r.wirePrompt(t, conn); q != nil {
		t.Fatalf("nothing asked: %+v", q)
	}
	line := "Enter passphrase for key '/k/id': "
	fp.show(line, 5)
	read()
	q := r.wirePrompt(t, conn)
	if q == nil || q.Stage != protocol.SSHPromptSignIn || q.Kind != protocol.SSHPromptSecret || q.Attempt != 0 {
		t.Fatalf("prompt = %+v", q)
	}
	read()
	if same := r.wirePrompt(t, conn); same.ID != q.ID {
		t.Fatal("a second read of the same line reset the card")
	}
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret}); err != nil {
		t.Fatal(err)
	}
	// ssh has not read it yet: the same line on the same row is not a failure.
	read()
	if got := r.wirePrompt(t, conn); got != nil {
		t.Fatalf("an unread answer brought the card back: %+v", got)
	}
	fp.show(line, 6)
	read()
	if got := r.wirePrompt(t, conn); got == nil || got.Attempt != 1 {
		t.Fatalf("retry = %+v", got)
	}
	store, _ := r.d.sshStore("s1", false)
	r.d.sshMarkConnected(store, conn)
	if got := r.wirePrompt(t, conn); got != nil {
		t.Fatalf("signed in, still %+v", got)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	noSecretOnDisk(t, r.home)
}

func TestCancelAtSignInEndsTheAttempt(t *testing.T) {
	r, conn, fp := signInRig(t)
	c := r.conn(t, "s1", conn)
	fp.show("user@box's password: ", 2)
	r.d.sshReadSignInPrompt(conn, c.Tab, true)
	q := r.wirePrompt(t, conn)
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptCancel}); err != nil {
		t.Fatal(err)
	}
	if got := r.conn(t, "s1", conn); got.State != protocol.SSHConnFailed || got.StateDetail != "Sign-in cancelled." || fp.interrupt != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestPromptAnswerIsHumanOnly(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "docker")
	if err := r.d.bridgeAdmit(protocol.Request{Cmd: protocol.CmdSSHPromptAnswer, Session: "s1", HookToken: r.d.hookToken("s1")}); err == nil {
		t.Fatal("admitted on the hook bridge")
	}
	if !remote.CommandDenied(protocol.CmdSSHPromptAnswer) {
		t.Fatal("reachable by a paired device")
	}
}

// A prompt that leaves echo on, at sign-in, where there is no tap to drop the line. The
// transcript reads the pane, so the check is on the pane when the connection comes up.
func TestAnAnswerEchoedAtSignInWithholdsTheTranscript(t *testing.T) {
	cases := []struct {
		name     string
		pane     string
		err      error
		withheld bool
	}{
		{"echoed", "Verification code: " + testSecret + "\nWelcome\n$ ", nil, true},
		{"honest", "Verification code: \nWelcome\n$ ", nil, false},
		{"an unreadable pane", "", errors.New("tmux is gone"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, conn, fp := signInRig(t)
			c := r.conn(t, "s1", conn)
			fp.show("Verification code: ", 5)
			r.d.sshReadSignInPrompt(conn, c.Tab, true)
			q := r.wirePrompt(t, conn)
			if q == nil || q.Kind != protocol.SSHPromptSecret {
				t.Fatalf("prompt = %+v", q)
			}
			if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret}); err != nil {
				t.Fatal(err)
			}
			fp.pane, fp.captureErr = tc.pane, tc.err
			store, _ := r.d.sshStore("s1", false)
			r.d.sshMarkConnected(store, conn)
			got, _ := sshConnCopy(store, conn)
			if (got.AnswerEchoTab == c.Tab) != tc.withheld {
				t.Fatalf("AnswerEchoTab = %q, want withheld = %v", got.AnswerEchoTab, tc.withheld)
			}
			if err := store.Flush(); err != nil {
				t.Fatal(err)
			}
			noSecretOnDisk(t, r.home)
		})
	}
}

func TestAYesAtSignInIsNotAnEcho(t *testing.T) {
	r, conn, fp := signInRig(t)
	c := r.conn(t, "s1", conn)
	fp.show("Are you sure you want to continue connecting (yes/no/[fingerprint])? ", 5)
	r.d.sshReadSignInPrompt(conn, c.Tab, true)
	q := r.wirePrompt(t, conn)
	if q == nil || q.Kind != protocol.SSHPromptConfirm {
		t.Fatalf("prompt = %+v", q)
	}
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptYes}); err != nil {
		t.Fatal(err)
	}
	fp.pane = q.Line + " yes\n$ "
	store, _ := r.d.sshStore("s1", false)
	r.d.sshMarkConnected(store, conn)
	if got, _ := sshConnCopy(store, conn); got.AnswerEchoTab != "" {
		t.Fatalf("a typed yes withheld the transcript")
	}
}

func TestTheWireSaysWhenTheAgentRanCommandsHere(t *testing.T) {
	r, conn, _, _ := promptRig(t)
	store, _ := r.d.sshStore("s1", false)
	info := func() protocol.SSHConnInfo {
		data, err := r.d.handleSSHTimeline(protocol.SSHTimelineArgs{Session: "s1"})
		if err != nil {
			t.Fatal(err)
		}
		return data.Connections[0]
	}
	if info().AgentRan {
		t.Fatal("nothing ran yet")
	}
	id := "b-agent"
	block := func(status string) {
		_ = store.Update(func(st *sshstore.State) error {
			for i := range st.Items {
				if st.Items[i].Block != nil && st.Items[i].Block.ID == id {
					st.Items[i].Block.Status = status
					return nil
				}
			}
			st.Items = append(st.Items, &protocol.SSHTimelineItem{ID: id, Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: id, Conn: conn, Actor: protocol.SSHActorAgent, Status: status}})
			return nil
		})
	}
	block(protocol.SSHBlockPendingApproval)
	if info().AgentRan {
		t.Fatal("a command nobody approved has not run")
	}
	block(protocol.SSHBlockDenied)
	if info().AgentRan {
		t.Fatal("a denied command has not run")
	}
	block(protocol.SSHBlockRunning)
	if !info().AgentRan {
		t.Fatal("a running command has run")
	}
}
