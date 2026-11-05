package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
)

func TestSSHConnectNeedsNoRequestAndIsTheGo(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	got, err := r.d.handleSSHConnect(context.Background(), protocol.SSHConnectArgs{Session: "s1", Host: "dev-box"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Conn.State == protocol.SSHConnRequested || got.Conn.Tab == "" {
		t.Fatalf("no go was given: %+v", got.Conn)
	}
	if got.Conn.Wheel != protocol.SSHWheelAgent {
		t.Fatalf("wheel = %s", got.Conn.Wheel)
	}
	if !strings.Contains(got.Warning, protocol.SSHDataWarning) || !strings.Contains(got.Warning, protocol.SSHUnsandboxedNote) {
		t.Fatalf("warning = %q", got.Warning)
	}
	r.masterUp(t, true)
	r.waitConn(t, "s1", got.Conn.ID, protocol.SSHConnConnected)

	again, err := r.d.handleSSHConnect(context.Background(), protocol.SSHConnectArgs{Session: "s1", Host: "dev-box"})
	if err != nil || again.Conn.ID == got.Conn.ID || again.Conn.Tab == got.Conn.Tab {
		t.Fatalf("a second connect should be a new tab: %+v %v", again.Conn, err)
	}
	r.waitConn(t, "s1", again.Conn.ID, protocol.SSHConnConnected)
	if a, b := r.conn(t, "s1", got.Conn.ID), r.conn(t, "s1", again.Conn.ID); a.Sock == "" || a.Sock != b.Sock {
		t.Fatalf("a new tab should reuse the login: %q %q", a.Sock, b.Sock)
	}
}

// Deleting a tab keeps the master for a tab still on it, and takes every trace of its own.
func TestSSHTrashKeepsASharedMaster(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	ctx := context.Background()
	first, err := r.d.handleSSHConnect(ctx, protocol.SSHConnectArgs{Session: "s1", Host: "dev-box"})
	if err != nil {
		t.Fatal(err)
	}
	r.masterUp(t, true)
	r.waitConn(t, "s1", first.Conn.ID, protocol.SSHConnConnected)
	second, err := r.d.handleSSHConnect(ctx, protocol.SSHConnectArgs{Session: "s1", Host: "dev-box"})
	if err != nil {
		t.Fatal(err)
	}
	r.waitConn(t, "s1", second.Conn.ID, protocol.SSHConnConnected)
	store, err := r.d.sshStore("s1", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.handleSSHSay(ctx, protocol.SSHSayArgs{Session: "s1", Conn: first.Conn.ID, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.handleSSHTrash(ctx, protocol.SSHConnArgs{Conn: first.Conn.ID}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.calls(), "-O exit") {
		t.Fatalf("closed a master another tab uses:\n%s", r.calls())
	}
	store.View(func(st *sshstore.State) {
		if st.Conn(first.Conn.ID) != nil || st.Conn(second.Conn.ID) == nil {
			t.Fatal("wrong connection went")
		}
		for _, it := range st.Items {
			if it.Human != nil && it.Human.Conn == first.Conn.ID {
				t.Fatal("the deleted tab left a message behind")
			}
		}
	})
	if _, err := r.d.handleSSHClose(ctx, protocol.SSHConnArgs{Conn: first.Conn.ID}); err == nil {
		t.Fatal("a deleted connection should be unknown")
	}
	if _, err := r.d.handleSSHTrash(ctx, protocol.SSHConnArgs{Conn: second.Conn.ID}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.calls(), "-O exit") {
		t.Fatalf("the last tab should close the master:\n%s", r.calls())
	}
}

// With two tabs on one host, the agent's next command goes where the human last spoke.
func TestSSHAgentFollowsTheTabTalkedTo(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	ctx := context.Background()
	first, err := r.d.handleSSHConnect(ctx, protocol.SSHConnectArgs{Session: "s1", Host: "dev-box"})
	if err != nil {
		t.Fatal(err)
	}
	r.masterUp(t, true)
	r.waitConn(t, "s1", first.Conn.ID, protocol.SSHConnConnected)
	second, err := r.d.handleSSHConnect(ctx, protocol.SSHConnectArgs{Session: "s1", Host: "dev-box"})
	if err != nil {
		t.Fatal(err)
	}
	r.waitConn(t, "s1", second.Conn.ID, protocol.SSHConnConnected)
	if _, err := r.d.handleSSHSay(ctx, protocol.SSHSayArgs{Session: "s1", Conn: first.Conn.ID, Text: "look here"}); err != nil {
		t.Fatal(err)
	}
	got := r.run(t, "s1", "dev-box", "uptime")
	if got.Block.Conn != first.Conn.ID {
		t.Fatalf("command went to %s, want the tab talked to %s", got.Block.Conn, first.Conn.ID)
	}
}

func TestSSHConnectAnswersAWaitingRequest(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	req, err := r.d.handleSSHAgentRequest("s1", protocol.SSHAgentRequestArgs{Host: "dev-box", Why: "look"})
	if err != nil || req.Refusal != nil {
		t.Fatalf("%v %+v", err, req.Refusal)
	}
	got, err := r.d.handleSSHConnect(context.Background(), protocol.SSHConnectArgs{Session: "s1", Host: "dev-box"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Conn.ID != req.Conn || got.Conn.State == protocol.SSHConnRequested {
		t.Fatalf("request not answered: %+v, want conn %s", got.Conn, req.Conn)
	}
}

func TestSSHConnectKeepsTheProductionRule(t *testing.T) {
	r := newSSHRig(t, hostProd)
	r.session("s1", "")
	if _, err := r.d.handleSSHConnect(context.Background(), protocol.SSHConnectArgs{Session: "s1", Host: "prod-api"}); err == nil {
		t.Fatal("an unsandboxed session connected to production")
	}
	if strings.Contains(r.calls(), "prod-api") {
		t.Fatalf("ssh was called: %s", r.calls())
	}
	r.session("s2", config.SandboxDocker)
	if _, err := r.d.handleSSHConnect(context.Background(), protocol.SSHConnectArgs{Session: "s2", Host: "prod-api"}); err != nil {
		t.Fatalf("a sandboxed session was refused: %v", err)
	}
}

func TestSSHConnectRefusesUnknownInput(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	for name, a := range map[string]protocol.SSHConnectArgs{
		"session": {Session: "nope", Host: "dev-box"},
		"host":    {Session: "s1", Host: "nope"},
		"wheel":   {Session: "s1", Host: "dev-box", Wheel: "nope"},
	} {
		if _, err := r.d.handleSSHConnect(context.Background(), a); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSSHConnectTellsAWaitingAgentOnce(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	queued := func() int {
		t.Helper()
		store, err := r.d.sshStore("s1", true)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		_ = store.Update(func(st *sshstore.State) error {
			for _, q := range st.Queue {
				if strings.Contains(q.Text, "connected this session to dev-box") {
					n++
				}
			}
			return nil
		})
		return n
	}
	if _, err := r.d.handleSSHConnect(context.Background(), protocol.SSHConnectArgs{Session: "s1", Host: "dev-box", Wheel: protocol.SSHWheelHuman}); err != nil {
		t.Fatal(err)
	}
	if queued() != 1 {
		t.Fatalf("notice queued %d times", queued())
	}
	if _, err := r.d.handleSSHConnect(context.Background(), protocol.SSHConnectArgs{Session: "s1", Host: "dev-box"}); err != nil {
		t.Fatal(err)
	}
	if queued() != 1 {
		t.Fatalf("a repeat connect notified again: %d", queued())
	}
}

func TestSSHConnectStaysAHumanCommand(t *testing.T) {
	for _, c := range protocol.SSHAgentCommands {
		if c == protocol.CmdSSHConnect {
			t.Fatal("an agent command")
		}
	}
	if hookBridgeCmds[protocol.CmdSSHConnect] {
		t.Fatal("the hook bridge admits it")
	}
}

func TestSSHHumanRunIsRecordedAndPrivateUntilShared(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	conn := r.connected(t, "s1", "dev-box")
	ctx := context.Background()
	b, err := r.d.handleSSHHumanRun(context.Background(), protocol.SSHHumanRunArgs{Session: "s1", Conn: conn, Command: "uptime"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Actor != protocol.SSHActorHuman || !b.Private || !b.Approval.Granted {
		t.Fatalf("block = %+v", b)
	}
	r.waitBlock(t, "s1", b.ID, protocol.SSHBlockSucceeded)

	if _, err := r.d.handleSSHAgentPoll("s1", protocol.SSHAgentPollArgs{Block: b.ID}); err == nil {
		t.Fatal("the agent read a private block")
	}
	post := []string{"finding", "x", "--severity", "high", "--evidence", b.ID}
	if _, err := r.d.handleSSHAgentPost("s1", protocol.SSHAgentPostArgs{Argv: post}); err == nil {
		t.Fatal("the agent cited a private block")
	}

	if _, err := r.d.handleSSHShareBlock(ctx, protocol.SSHShareBlockArgs{Session: "s1", Block: b.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.handleSSHAgentPoll("s1", protocol.SSHAgentPollArgs{Block: b.ID}); err != nil {
		t.Fatalf("a shared block stayed hidden: %v", err)
	}
}

func TestRunningACommandTakesTheWheelAndAskingGivesItBack(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	conn := r.connected(t, "s1", "dev-box")
	ctx := context.Background()
	b, err := r.d.handleSSHHumanRun(ctx, protocol.SSHHumanRunArgs{Session: "s1", Conn: conn, Command: "uptime"})
	if err != nil {
		t.Fatal(err)
	}
	r.waitBlock(t, "s1", b.ID, protocol.SSHBlockSucceeded)
	if got := r.conn(t, "s1", conn).Wheel; got != protocol.SSHWheelHuman {
		t.Fatalf("wheel = %s after running a command", got)
	}
	run := r.run(t, "s1", "dev-box", "ls")
	if run.Refusal == nil || run.Refusal.Code != protocol.SSHRefuseHumanControl {
		t.Fatalf("the agent ran past a driving human: %+v", run)
	}
	if _, err := r.d.handleSSHSay(ctx, protocol.SSHSayArgs{Session: "s1", Text: "take over"}); err != nil {
		t.Fatal(err)
	}
	if got := r.conn(t, "s1", conn).Wheel; got != protocol.SSHWheelAgent {
		t.Fatalf("wheel = %s after asking the agent", got)
	}
}

func TestStopInterruptsOnlyAWorkingAgent(t *testing.T) {
	r := newSSHRig(t, hostDev)
	s := r.session("s1", "")
	conn := r.connected(t, "s1", "dev-box")
	var sent []string
	r.d.sendEscape = func(_ context.Context, name string) error {
		sent = append(sent, name)
		return nil
	}
	if _, err := r.d.handleSSHTakeWheel(context.Background(), protocol.SSHTakeWheelArgs{Conn: conn, Interrupt: true}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != s.TmuxName {
		t.Fatalf("sent = %v", sent)
	}
	cur, _ := r.d.sessions.Get("s1")
	cur.SetAgentState(state.AgentIdle, "", time.Now())
	r.d.sessions.Upsert(cur)
	if _, err := r.d.handleSSHTakeWheel(context.Background(), protocol.SSHTakeWheelArgs{Conn: conn, Interrupt: true}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("Esc reached an agent at rest: %v", sent)
	}
}

func TestSSHHumanRunIgnoresTheWheelButNeedsAConnection(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	conn := r.connected(t, "s1", "dev-box")
	if _, err := r.d.handleSSHTakeWheel(context.Background(), protocol.SSHTakeWheelArgs{Conn: conn}); err != nil {
		t.Fatal(err)
	}
	b, err := r.d.handleSSHHumanRun(context.Background(), protocol.SSHHumanRunArgs{Session: "s1", Conn: conn, Command: "uptime"})
	if err != nil {
		t.Fatal(err)
	}
	r.waitBlock(t, "s1", b.ID, protocol.SSHBlockSucceeded)

	if _, err := r.d.handleSSHHumanRun(context.Background(), protocol.SSHHumanRunArgs{Session: "other", Conn: conn, Command: "uptime"}); err == nil {
		t.Fatal("ran on another session's connection")
	}
	r.masterUp(t, false)
	r.waitConn(t, "s1", conn, protocol.SSHConnDisconnected)
	if _, err := r.d.handleSSHHumanRun(context.Background(), protocol.SSHHumanRunArgs{Session: "s1", Conn: conn, Command: "uptime"}); err == nil {
		t.Fatal("ran with no connection")
	}
}

func TestAdoptionDropsAnOlderDaemonsPhantomOfAnSSHTab(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("lola-web-1", "")
	phantom := session.Session{ID: "lola-web-1-ssh-1", Source: "native", TmuxName: "lola-web-1-ssh-1"}
	phantom.SetAgentState(state.AgentOrphaned, "", time.Now())
	r.d.sessions.Upsert(phantom)
	// Issue SSH-42 of project "infra": the shape of a tab, with no parent on record.
	real := session.Session{ID: "lola-infra-ssh-42", Source: "native", TmuxName: "lola-infra-ssh-42"}
	real.SetAgentState(state.AgentOrphaned, "", time.Now())
	r.d.sessions.Upsert(real)

	r.d.purgeAuxRecords()

	if _, ok := r.d.sessions.Get("lola-web-1-ssh-1"); ok {
		t.Fatal("the phantom survived")
	}
	if _, ok := r.d.sessions.Get("lola-infra-ssh-42"); !ok {
		t.Fatal("a real session was dropped")
	}
}

type fakeTapper struct {
	*fakeSSHTmux
	sent  []string
	enter []bool
}

func (f *fakeTapper) PipePane(context.Context, string, string) error { return nil }
func (f *fakeTapper) SendText(_ context.Context, _, text string, enter bool) error {
	f.sent, f.enter = append(f.sent, text), append(f.enter, enter)
	return nil
}

func tapRig(t *testing.T) (*sshRig, string, *sshTapReader, *fakeTapper) {
	t.Helper()
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	conn := r.connected(t, "s1", "dev-box")
	store, err := r.d.sshStore("s1", true)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := sshConnCopy(store, conn)
	ft := &fakeTapper{fakeSSHTmux: r.tm}
	r.d.ssh.tmuxFn = func() sshTmux { return ft }
	return r, conn, &sshTapReader{d: r.d, store: store, conn: c, ctx: context.Background()}, ft
}

func TestATerminalCommandBecomesAPrivateBlockAndMovesTheFolder(t *testing.T) {
	r, conn, tap, _ := tapRig(t)
	tap.end(0, "/home/m", false, false) // the first prompt: known, but nobody moved
	c := r.conn(t, "s1", conn)
	if !c.TermTracked || !c.TermAtPrompt || c.Cwd != "/home/m" {
		t.Fatalf("first prompt: %+v", c)
	}
	tap.start("cd /srv/app")
	if got := r.conn(t, "s1", conn); got.TermAtPrompt || got.Wheel != protocol.SSHWheelHuman {
		t.Fatalf("a running command should close the prompt and take the wheel: %+v", got)
	}
	id := tap.block.ID
	tap.output([]byte("hello\r\n"))
	tap.end(0, "/srv/app", true, false)
	b := r.block(t, "s1", id)
	if b.Actor != protocol.SSHActorHuman || !b.Private || b.Origin != protocol.SSHOriginTerminal || b.Status != protocol.SSHBlockSucceeded {
		t.Fatalf("block = %+v", b)
	}
	if got := r.conn(t, "s1", conn); got.Cwd != "/srv/app" || got.TermCwd != "/srv/app" || !got.TermAtPrompt {
		t.Fatalf("the folder did not move: %+v", got)
	}
	if _, err := r.d.handleSSHAgentPoll("s1", protocol.SSHAgentPollArgs{Block: id}); err == nil {
		t.Fatal("the agent read a terminal block")
	}
	tap.start("false")
	tap.end(1, "/srv/app", true, false)
	if got := r.block(t, "s1", tap.d.sshLastBlockForTest(t, "s1")); got.Status != protocol.SSHBlockFailed || got.ExitCode == nil || *got.ExitCode != 1 {
		t.Fatalf("exit not recorded: %+v", got)
	}
	tap.end(0, "relative/evil", false, false)
	if got := r.conn(t, "s1", conn); got.Cwd != "/srv/app" {
		t.Fatalf("took a non-path: %q", got.Cwd)
	}
}

func TestSudoIsOneVisibleEntryAndAgentsAreLabelledToo(t *testing.T) {
	r, conn, tap, _ := tapRig(t)
	tap.end(0, "/home/m", false, false)
	tap.start("sudo -i")
	id := tap.block.ID
	if !r.block(t, "s1", id).Elevated {
		t.Fatal("not labelled")
	}
	if got := r.conn(t, "s1", conn); got.TermElevated != "sudo -i" {
		t.Fatalf("elevated = %q", got.TermElevated)
	}
	if _, err := r.d.handleSSHHumanRun(context.Background(), protocol.SSHHumanRunArgs{Session: "s1", Conn: conn, Command: "uptime"}); err != nil {
		t.Fatal(err)
	}
	tap.output([]byte("root output\n"))
	tap.end(0, "/home/m", true, false)
	if got := r.conn(t, "s1", conn); got.TermElevated != "" {
		t.Fatalf("still elevated: %q", got.TermElevated)
	}
	got := r.run(t, "s1", "dev-box", "sudo systemctl restart api")
	if got.Refusal == nil && (got.Block == nil || !got.Block.Elevated) {
		t.Fatalf("an agent's sudo was not labelled: %+v", got)
	}
}

func TestRunGoesIntoTheShellAtAPromptAndElsewhereOverExec(t *testing.T) {
	r, conn, tap, ft := tapRig(t)
	ctx := context.Background()
	run := func(cmd string) protocol.SSHBlock {
		t.Helper()
		b, err := r.d.handleSSHHumanRun(ctx, protocol.SSHHumanRunArgs{Session: "s1", Conn: conn, Command: cmd})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if b := run("before-any-prompt"); b.ID == "" {
		t.Fatal("an untracked shell must fall back to exec")
	}
	tap.end(0, "/home/m", false, false)
	if b := run("export A=1"); b.ID != "" || b.Origin != protocol.SSHOriginTerminal {
		t.Fatalf("not routed: %+v", b)
	}
	if len(ft.sent) != 1 || ft.sent[0] != "export A=1" || !ft.enter[0] {
		t.Fatalf("sent = %v %v", ft.sent, ft.enter)
	}
	if b := run("while-busy"); b.ID == "" {
		t.Fatal("a busy shell must fall back to exec")
	}
	if len(ft.sent) != 1 {
		t.Fatalf("typed into a busy shell: %v", ft.sent)
	}
}

func TestGoToFolderRunsAtAPromptAndOnlyTypesElsewhere(t *testing.T) {
	r, conn, tap, ft := tapRig(t)
	ctx := context.Background()
	store, _ := r.d.sshStore("s1", true)
	r.d.sshSetCwd(store, conn, "/srv/it's")
	// Not tracked: a line to look at, never run.
	if _, err := r.d.handleSSHTerminalCd(ctx, protocol.SSHConnArgs{Conn: conn}); err != nil {
		t.Fatal(err)
	}
	if ft.sent[0] != ` cd -- '/srv/it'\''s'` || ft.enter[0] {
		t.Fatalf("untracked: %q %v", ft.sent[0], ft.enter[0])
	}
	tap.end(0, "/home/m", false, false)
	if _, err := r.d.handleSSHTerminalCd(ctx, protocol.SSHConnArgs{Conn: conn}); err != nil {
		t.Fatal(err)
	}
	if !ft.enter[1] {
		t.Fatal("a known prompt should run it")
	}
	tap.start("sleep 100")
	if _, err := r.d.handleSSHTerminalCd(ctx, protocol.SSHConnArgs{Conn: conn}); err == nil {
		t.Fatal("typed into a busy shell")
	}
}

func TestHostCanTurnCaptureOff(t *testing.T) {
	off := false
	h := config.SSHHost{Alias: "x", TerminalCapture: &off}
	if h.CapturesTerminal() || !hostDev.CapturesTerminal() {
		t.Fatal("capture default")
	}
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	store, _ := r.d.sshStore("s1", true)
	args := []string{"-S", "s", "alias"}
	if got := r.d.sshShellArgs(store, "c", h, &fakeTapper{fakeSSHTmux: r.tm}, args); len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func (d *Daemon) sshLastBlockForTest(t *testing.T, id string) string {
	t.Helper()
	store, err := d.sshStore(id, false)
	if err != nil {
		t.Fatal(err)
	}
	last := ""
	store.View(func(st *sshstore.State) {
		for _, it := range st.Items {
			if it.Block != nil {
				last = it.Block.ID
			}
		}
	})
	return last
}

func TestAnIdleConnectionWithNoShellCloses(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	conn := r.connected(t, "s1", "dev-box")
	store, _ := r.d.sshStore("s1", true)
	long := time.Now().Add(-time.Hour)
	set := func(linger int, last time.Time) {
		_ = store.Update(func(st *sshstore.State) error {
			c := st.Conn(conn)
			c.IdleLingerSeconds, c.LastUsed = linger, last
			return nil
		})
	}
	set(60, time.Now())
	if r.d.sshIdleExpired(store, conn, time.Now()) {
		t.Fatal("expired while recently used")
	}
	set(60, long)
	if !r.d.sshIdleExpired(store, conn, time.Now()) {
		t.Fatal("never expires")
	}
	set(0, long)
	if r.d.sshIdleExpired(store, conn, time.Now()) {
		t.Fatal("a connection with no linger recorded must not close")
	}
}
