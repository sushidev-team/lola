//go:build unix

package proctree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"
)

// The bug this guards: `tmux kill-session` only hangs the pane's own process up
// (SIGHUP), so a child that IGNORES SIGHUP — `composer dev`'s `php artisan
// serve`, and the `php -S` under it — survives as an orphan of pid 1 and keeps
// the project's port bound. The next session's dev server then starts on 8001.
//
// So the test child is exactly that shape: its own process group, a
// SIGHUP-ignoring grandchild, and a parent that is killed the way tmux would.
func TestKillGroupReachesChildrenThatIgnoreSIGHUP(t *testing.T) {
	// The child ignores SIGHUP, exactly like `php artisan serve` under
	// `composer dev`, and sits in its own process group the way a tmux pane's
	// process does.
	cmd := exec.Command("sh", "-c", `trap '' HUP; sleep 30`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	time.Sleep(200 * time.Millisecond) // let the trap be installed

	// What `tmux kill-session` amounts to for the pane process: a hangup. This
	// child survives it — which is why killing the tmux session alone left the
	// dev server holding the project's port.
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		t.Fatalf("sighup: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Skipf("the child did not survive SIGHUP on this host (%v) — nothing left to prove", err)
	}

	if err := KillGroup(pgid, 2*time.Second); err != nil {
		t.Fatalf("KillGroup: %v", err)
	}
	// The child is gone. It is checked by REAPING it rather than by probing the
	// group: an unreaped zombie answers EPERM to a group probe on macOS, which
	// would read as "still alive" when the process is in fact dead.
	reaped := make(chan error, 1)
	go func() { reaped <- cmd.Wait() }()
	select {
	case <-reaped:
	case <-time.After(2 * time.Second):
		t.Error("the SIGHUP-ignoring child is still alive after KillGroup")
	}
}

// Signalling pid 0 / 1 means "every process we may signal" / init: a pane pid
// that resolved to either is a lookup gone wrong, and acting on it would take
// the machine down with it.
func TestKillGroupRefusesDangerousTargets(t *testing.T) {
	for _, pgid := range []int{0, 1, -1} {
		if err := KillGroup(pgid, time.Millisecond); err == nil {
			t.Errorf("KillGroup(%d) = nil, want a refusal", pgid)
		}
	}
	// lola's own group is the daemon itself (here: the test binary).
	own, err := syscall.Getpgid(syscall.Getpid())
	if err != nil {
		t.Skipf("getpgid: %v", err)
	}
	if err := KillGroup(own, time.Millisecond); err == nil {
		t.Error("KillGroup on lola's own group must be refused")
	}
}

// The bug the ppid walk exists for, end to end: a descendant that left the
// pane's process group. Claude Code's Bash tool makes exactly this shape — it
// puts every command it runs in its own group so it can time one out without
// touching the agent — so a `php artisan serve --port=8000` an agent started is
// unreachable by a kill of the pane's group and holds the port after teardown.
//
// The helper below is the "pane": its own group, one child in a group of its
// own. Only the ppid walk finds that child.
func TestTreeGroupsAndKillGroupsReachADescendantThatLeftTheGroup(t *testing.T) {
	pane := exec.Command(os.Args[0], "-test.run=TestProctreeHelperProcess")
	pane.Env = append(os.Environ(), "GO_PROCTREE_HELPER=1")
	pane.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := pane.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := pane.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	panePID := pane.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-panePID, syscall.SIGKILL)
		_ = pane.Wait()
	})

	var escapee int
	if _, err := fmt.Fscanln(out, &escapee); err != nil {
		t.Fatalf("helper did not report its escaped child: %v", err)
	}

	tbl, err := Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	paneGroup, escGroup := tbl.Group(panePID), tbl.Group(escapee)
	if paneGroup == 0 || escGroup == 0 {
		t.Fatalf("process table missing pane %d (%d) or escapee %d (%d)", panePID, paneGroup, escapee, escGroup)
	}
	if paneGroup == escGroup {
		t.Skipf("the child did not get its own process group on this host — nothing left to prove")
	}
	groups := tbl.TreeGroups(panePID)
	if !slices.Contains(groups, escGroup) {
		t.Fatalf("TreeGroups(%d) = %v, missing the escaped group %d — this is the port that stays bound",
			panePID, groups, escGroup)
	}

	if err := KillGroups(groups, 2*time.Second); err != nil {
		t.Fatalf("KillGroups: %v", err)
	}
	// The escapee's parent is dead too, so init reaps it — no zombie to confuse
	// the probe, unlike a child of this test process.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(escapee, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the escaped descendant is still alive after KillGroups")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestProctreeHelperProcess is not a test: it is the "pane" process the test
// above drives. It spawns one child in a NEW process group, prints its pid, and
// waits to be killed.
func TestProctreeHelperProcess(t *testing.T) {
	if os.Getenv("GO_PROCTREE_HELPER") != "1" {
		t.Skip("helper process for TestTreeGroupsAndKillGroupsReachADescendantThatLeftTheGroup")
	}
	child := exec.Command("sh", "-c", `trap '' HUP TERM; sleep 30`)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	fmt.Println(child.Process.Pid)
	os.Stdout.Sync()
	time.Sleep(30 * time.Second)
}
