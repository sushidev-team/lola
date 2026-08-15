//go:build unix

package tmux

import (
	"os/exec"
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
func TestKillProcessGroupReachesChildrenThatIgnoreSIGHUP(t *testing.T) {
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

	if err := killProcessGroup(pgid, 2*time.Second); err != nil {
		t.Fatalf("killProcessGroup: %v", err)
	}
	// The child is gone. It is checked by REAPING it rather than by probing the
	// group: an unreaped zombie answers EPERM to a group probe on macOS, which
	// would read as "still alive" when the process is in fact dead.
	reaped := make(chan error, 1)
	go func() { reaped <- cmd.Wait() }()
	select {
	case <-reaped:
	case <-time.After(2 * time.Second):
		t.Error("the SIGHUP-ignoring child is still alive after killProcessGroup")
	}
}

// Signalling pid 0 / 1 means "every process we may signal" / init: a pane pid
// that resolved to either is a lookup gone wrong, and acting on it would take
// the machine down with it.
func TestKillProcessGroupRefusesDangerousTargets(t *testing.T) {
	for _, pgid := range []int{0, 1, -1} {
		if err := killProcessGroup(pgid, time.Millisecond); err == nil {
			t.Errorf("killProcessGroup(%d) = nil, want a refusal", pgid)
		}
	}
	// lola's own group is the daemon itself (here: the test binary).
	own, err := syscall.Getpgid(syscall.Getpid())
	if err != nil {
		t.Skipf("getpgid: %v", err)
	}
	if err := killProcessGroup(own, time.Millisecond); err == nil {
		t.Error("killProcessGroup on lola's own group must be refused")
	}
}
