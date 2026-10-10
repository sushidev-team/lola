package daemon

// Tests for turn checkpoints (checkpoints.go): what the Stop / user_prompt hooks
// record, the turn diff's base, the mid-turn refusal on restore, and fork
// naming + briefing + context inheritance.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/checkpoint"
	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// fakeCheckpoints is an in-memory checkpointStore. Each Record with a new
// "state" (the state field, bumped by tests) appends one checkpoint.
type fakeCheckpoints struct {
	mu       sync.Mutex
	list     []checkpoint.Checkpoint
	state    int
	restored []int
	recorded chan string
	// inRestore, when set, is closed once Restore has started and Restore then
	// blocks until release is closed — to observe what runs concurrently.
	inRestore chan struct{}
	release   chan struct{}
}

func (f *fakeCheckpoints) Record(_ context.Context, _, _, label string) (checkpoint.Checkpoint, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tree := fmt.Sprintf("tree%d", f.state)
	if n := len(f.list); n > 0 && f.list[n-1].Tree == tree {
		return f.list[n-1], false, nil
	}
	cp := checkpoint.Checkpoint{Seq: len(f.list) + 1, SHA: fmt.Sprintf("sha%d", len(f.list)+1), Tree: tree,
		Head: "head", Label: label, Created: time.Now()}
	f.list = append(f.list, cp)
	if f.recorded != nil {
		f.recorded <- label
	}
	return cp, true, nil
}

func (f *fakeCheckpoints) List(context.Context, string, string) ([]checkpoint.Checkpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]checkpoint.Checkpoint(nil), f.list...), nil
}

func (f *fakeCheckpoints) Get(_ context.Context, _, _ string, seq int) (cp, prev checkpoint.Checkpoint, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.list {
		if c.Seq == seq {
			if i > 0 {
				prev = f.list[i-1]
			}
			return c, prev, nil
		}
	}
	return cp, prev, checkpoint.ErrNotFound
}

func (f *fakeCheckpoints) Restore(ctx context.Context, dir, s string, seq int) (checkpoint.Checkpoint, error) {
	if _, _, err := f.Get(ctx, dir, s, seq); err != nil {
		return checkpoint.Checkpoint{}, err
	}
	if f.inRestore != nil {
		close(f.inRestore)
		<-f.release
	}
	f.mu.Lock()
	f.state++
	f.mu.Unlock()
	safety, _, err := f.Record(ctx, dir, s, fmt.Sprintf("before restore to #%d", seq))
	f.mu.Lock()
	f.restored = append(f.restored, seq)
	f.mu.Unlock()
	return safety, err
}

func ckptSess(t *testing.T, d *Daemon, ident string, a state.AgentState) session.Session {
	t.Helper()
	s := nativeSess(ident, "working")
	s.Agent = "claude"
	s.Title = "Fix the thing"
	s.Worktree = t.TempDir()
	s.SetAgentState(a, "", time.Now())
	d.sessions.Upsert(s)
	// A resting pane unless a test says otherwise: restore demands one.
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWaiting, nil }
	got, _ := d.sessions.Get(s.ID)
	return got
}

func TestHookStopRecordsTurnsAndUserPromptOnlyTheBaseline(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	ck := &fakeCheckpoints{recorded: make(chan string, 8)}
	d.checkpoints = ck
	s := ckptSess(t, d, "CK-1", state.AgentIdle)
	ctx := context.Background()

	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "user_prompt"})
	select {
	case l := <-ck.recorded:
		if l != "start" {
			t.Fatalf("baseline label = %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("user_prompt on a session with no checkpoints recorded nothing")
	}

	ck.state++
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "stop"})
	select {
	case l := <-ck.recorded:
		if l != "turn 1" {
			t.Fatalf("turn label = %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop hook recorded nothing")
	}

	// A later turn START records nothing — only the baseline is taken there.
	ck.state++
	d.recordCheckpoint(ctx, s.ID, "user_prompt")
	// An unchanged state on stop is deduped by the store.
	ck.state--
	d.recordCheckpoint(ctx, s.ID, "stop")
	ck.state += 2
	d.recordCheckpoint(ctx, s.ID, "stop")

	list, _ := ck.List(ctx, "", "")
	var labels []string
	for _, c := range list {
		labels = append(labels, c.Label)
	}
	if got := strings.Join(labels, ","); got != "start,turn 1,turn 2" {
		t.Errorf("labels = %s", got)
	}
}

func TestRecordSkipsShellsAndMissingWorktrees(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	ck := &fakeCheckpoints{}
	d.checkpoints = ck
	shell := ckptSess(t, d, "CK-2", state.AgentIdle)
	shell.Agentless = true
	d.sessions.Upsert(shell)
	gone := ckptSess(t, d, "CK-3", state.AgentIdle)
	gone.Worktree = "/nonexistent/lola/worktree"
	d.sessions.Upsert(gone)

	d.recordCheckpoint(context.Background(), shell.ID, "stop")
	d.recordCheckpoint(context.Background(), gone.ID, "stop")
	if len(ck.list) != 0 {
		t.Errorf("recorded %d checkpoints for a shell / a missing worktree", len(ck.list))
	}
}

func TestCheckpointDiffIsAgainstThePreviousCheckpoint(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	ck := &fakeCheckpoints{}
	d.checkpoints = ck
	s := ckptSess(t, d, "CK-4", state.AgentIdle)
	ctx := context.Background()
	ck.Record(ctx, "", "", "start")
	ck.state++
	ck.Record(ctx, "", "", "turn 1")

	var from, to string
	d.commitDiff = func(_ context.Context, _, f, tt string) (gitdiff.Result, error) {
		from, to = f, tt
		return gitdiff.Result{Files: []gitdiff.File{{Path: "a.go", Status: "modified"}}}, nil
	}
	data, err := d.handleCheckpointDiff(ctx, protocol.CheckpointArgs{Session: s.ID, Seq: 2})
	if err != nil {
		t.Fatal(err)
	}
	if from != "sha1" || to != "sha2" || data.Base != "checkpoint #1" || len(data.Files) != 1 {
		t.Errorf("diff %s..%s base %q files %d", from, to, data.Base, len(data.Files))
	}
	if _, err := d.handleCheckpointDiff(ctx, protocol.CheckpointArgs{Session: s.ID, Seq: 1}); err != nil || from != "head" {
		t.Errorf("oldest checkpoint must diff against its HEAD: from=%q err=%v", from, err)
	}
	if _, err := d.handleCheckpointDiff(ctx, protocol.CheckpointArgs{Session: s.ID, Seq: 9}); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Errorf("unknown seq err = %v", err)
	}
}

func TestRestoreRefusedMidTurnAndReportsTheSafetyCheckpoint(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	ck := &fakeCheckpoints{}
	d.checkpoints = ck
	ctx := context.Background()
	ck.Record(ctx, "", "", "turn 1")

	busy := ckptSess(t, d, "CK-5", state.AgentWorking)
	if _, err := d.handleRestoreCheckpoint(ctx, protocol.CheckpointArgs{Session: busy.ID, Seq: 1}); err == nil || !strings.Contains(err.Error(), "mid-turn") {
		t.Fatalf("restore of a working agent: err = %v", err)
	}
	if len(ck.restored) != 0 {
		t.Fatal("a mid-turn restore touched the worktree")
	}
	if data, _ := d.handleCheckpoints(ctx, busy.ID); data.Restorable {
		t.Error("Restorable must be false mid-turn")
	}

	idle := ckptSess(t, d, "CK-6", state.AgentIdle)
	data, err := d.handleRestoreCheckpoint(ctx, protocol.CheckpointArgs{Session: idle.ID, Seq: 1})
	if err != nil {
		t.Fatal(err)
	}
	if data.Seq != 1 || data.Safety != 2 || !strings.Contains(data.Message, "#2") {
		t.Errorf("restore data = %+v", data)
	}
}

func TestForkStartsANamedAgentSessionFromTheCheckpoint(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, nat)
	ck := &fakeCheckpoints{}
	d.checkpoints = ck
	ctx := context.Background()
	ck.Record(ctx, "", "", "start")
	ck.state++
	ck.Record(ctx, "", "", "turn 1")
	parent := ckptSess(t, d, "CK-7", state.AgentWorking) // a fork never needs the parent idle

	out, err := d.handleForkCheckpoint(ctx, protocol.CheckpointArgs{Session: parent.ID, Seq: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(nat.forks) != 1 {
		t.Fatalf("forks = %d", len(nat.forks))
	}
	spec := nat.forks[0]
	if spec.Branch != "lola/ck-7-fork-2" || spec.Head != "head" || spec.Tree != "tree1" || spec.ContextKey != "ck-7" || spec.Agent != "claude" {
		t.Errorf("spec = %+v", spec)
	}
	for _, want := range []string{"FORK of lola session `" + parent.ID + "`", "checkpoint #2", "Linear issue **CK-7**", "`lola/ck-7-fork-2`", "against `develop`"} {
		if !strings.Contains(spec.Prompt, want) {
			t.Errorf("fork briefing lacks %q:\n%s", want, spec.Prompt)
		}
	}
	if _, ok := d.sessions.Get(out.SessionID); !ok {
		t.Error("fork session not recorded in the store")
	}

	// A second fork from the same checkpoint gets the next free name.
	if _, err := d.handleForkCheckpoint(ctx, protocol.CheckpointArgs{Session: parent.ID, Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if got := nat.forks[1].Branch; got != "lola/ck-7-fork-2-2" {
		t.Errorf("second fork branch = %q", got)
	}
	// A slot whose branch or worktree outlived its session record (an older fork
	// killed with its worktree kept) is skipped, not reused.
	nat.takenBranches = map[string]bool{"lola/ck-7-fork-1": true}
	ck.Record(ctx, "", "", "turn 2") // seq 3; fork #1 instead
	if _, err := d.handleForkCheckpoint(ctx, protocol.CheckpointArgs{Session: parent.ID, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if got := nat.forks[2].Branch; got != "lola/ck-7-fork-1-2" {
		t.Errorf("fork past a leftover branch = %q, want the next slot", got)
	}
	if _, err := d.handleForkCheckpoint(ctx, protocol.CheckpointArgs{Session: parent.ID, Seq: 2, Agent: "bogus"}); err == nil {
		t.Error("an unknown agent override was accepted")
	}
}

// The acceptance path against a REAL repository, through the daemon: two turns
// end (Stop hook), the second was bad, and a restore from the "UI" command
// brings the worktree back to turn 1 without a single git command by hand.
func TestCheckpointAcceptanceRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	d.checkpoints = checkpoint.Git{}
	d.commitDiff = gitdiff.Between
	s := ckptSess(t, d, "CK-9", state.AgentIdle)
	dir := s.Worktree
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("app.go", "v0\n")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	ctx := context.Background()

	d.recordCheckpoint(ctx, s.ID, "user_prompt") // baseline
	write("app.go", "good\n")
	d.recordCheckpoint(ctx, s.ID, "stop") // turn 1
	write("app.go", "broken\n")
	write("junk.go", "junk\n")
	d.recordCheckpoint(ctx, s.ID, "stop") // turn 2, the bad one

	list, err := d.handleCheckpoints(ctx, s.ID)
	if err != nil || len(list.Checkpoints) != 3 {
		t.Fatalf("checkpoints = %+v, %v", list, err)
	}
	diff, err := d.handleCheckpointDiff(ctx, protocol.CheckpointArgs{Session: s.ID, Seq: 3})
	if err != nil || len(diff.Files) != 2 || diff.Base != "checkpoint #2" {
		t.Fatalf("turn 2 diff = %+v, %v", diff, err)
	}

	res, err := d.handleRestoreCheckpoint(ctx, protocol.CheckpointArgs{Session: s.ID, Seq: 2})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "app.go")); string(b) != "good\n" {
		t.Errorf("app.go = %q after restore", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk.go")); !os.IsNotExist(err) {
		t.Error("the bad turn's new file survived the restore")
	}
	// Nothing changed since turn 2 ended, so the pre-restore state IS #3 — the
	// dedupe names it as the undo instead of minting a duplicate.
	if res.Safety != 3 {
		t.Errorf("safety checkpoint = #%d, want #3", res.Safety)
	}
}

// The axis can be stale: a live agent whose pane is not visibly resting is
// refused, while a gone agent (nothing left to edit files) needs no pane.
func TestRestoreDemandsALiveRestingPane(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	ck := &fakeCheckpoints{}
	d.checkpoints = ck
	ctx := context.Background()
	ck.Record(ctx, "", "", "turn 1")

	idle := ckptSess(t, d, "CK-10", state.AgentIdle)
	d.paneTail = func(context.Context, string, int) (string, error) {
		return "✻ Harmonizing… (5m 58s · ↓ 17.9k tokens)\n", nil
	}
	if _, err := d.handleRestoreCheckpoint(ctx, protocol.CheckpointArgs{Session: idle.ID, Seq: 1}); err == nil {
		t.Fatal("restored under an agent whose pane shows a running turn")
	}
	gone := ckptSess(t, d, "CK-11", state.AgentDead)
	d.paneTail = func(context.Context, string, int) (string, error) { return "", errors.New("no pane") }
	if _, err := d.handleRestoreCheckpoint(ctx, protocol.CheckpointArgs{Session: gone.ID, Seq: 1}); err != nil {
		t.Fatalf("a dead agent's worktree must be restorable: %v", err)
	}
	if len(ck.restored) != 1 {
		t.Errorf("restored = %v", ck.restored)
	}
}

// Nothing may type into the agent while its files are being replaced: a send
// that arrives mid-restore waits, and goes out once the restore is done.
func TestSendsWaitOutARestore(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	ck := &fakeCheckpoints{inRestore: make(chan struct{}), release: make(chan struct{})}
	d.checkpoints = ck
	ctx := context.Background()
	ck.Record(ctx, "", "", "turn 1")
	s := ckptSess(t, d, "CK-12", state.AgentIdle)

	// The send records whether the restore had already finished when it ran;
	// ck.restored is appended inside Restore, i.e. while the gate is held.
	restoredAtSend := make(chan int, 1)
	d.sendKeys = func(context.Context, string, string) error {
		ck.mu.Lock()
		restoredAtSend <- len(ck.restored)
		ck.mu.Unlock()
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := d.handleRestoreCheckpoint(ctx, protocol.CheckpointArgs{Session: s.ID, Seq: 1})
		done <- err
	}()
	<-ck.inRestore
	sent := make(chan error, 1)
	go func() { sent <- d.typeToAgent(ctx, s.ID, s.TmuxName, "hello", time.Second) }()
	select {
	case <-sent:
		t.Fatal("a send went through while the restore was replacing files")
	case <-time.After(150 * time.Millisecond):
	}
	close(ck.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-sent; err != nil {
		t.Fatalf("the queued send failed (its timeout must start after the wait): %v", err)
	}
	if n := <-restoredAtSend; n != 1 {
		t.Errorf("the send ran with %d restores finished, want it after the restore", n)
	}
}
