//go:build unix

package tmux

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

// processGroupOf returns the process group pid leads, which for a tmux pane's
// own process is itself: tmux puts every pane in a fresh session + process
// group, so the pane's children inherit that group unless they deliberately
// leave it (setsid).
func processGroupOf(pid int) (int, error) {
	return syscall.Getpgid(pid)
}

// killProcessGroup terminates a whole process group: SIGTERM first, then
// SIGKILL to whatever is left once grace has passed.
//
// The graceful step is not politeness — a dev server asked to stop closes its
// listening socket, whereas one that is SIGKILLed can leave the port in the
// kernel for a moment and the replacement lands on 8001. It polls with signal 0
// so a group that shuts down immediately costs one tick, not the whole grace.
func killProcessGroup(pgid int, grace time.Duration) error {
	if pgid <= 1 {
		return fmt.Errorf("refusing to signal process group %d", pgid)
	}
	// Never signal our OWN group: that is the daemon (and, in a test, the test
	// binary). A pane pid that resolves to it means the lookup went wrong, and
	// the blast radius of being wrong here is the whole process.
	if own, err := syscall.Getpgid(syscall.Getpid()); err == nil && own == pgid {
		return errors.New("refusing to signal lola's own process group")
	}
	// EPERM is NOT a reason to stop: on macOS a group whose leader is an
	// unreaped zombie answers EPERM even though its live members are perfectly
	// signalable, and those members are precisely what this exists to reach.
	// ESRCH means the group is already gone.
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil &&
		!errors.Is(err, syscall.EPERM) {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil &&
		!errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
		return err
	}
	return nil
}
