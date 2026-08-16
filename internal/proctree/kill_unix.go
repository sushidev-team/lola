//go:build unix

package proctree

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

// pollInterval is how often a group under termination is probed. Short enough
// that a group which exits at once costs one tick rather than the whole grace.
const pollInterval = 50 * time.Millisecond

// settleAfterKill is how long SIGKILL gets to actually take effect before this
// returns. SIGKILL is asynchronous — the caller's very next act is starting the
// replacement dev server, and a listening socket is released when the process
// is reaped, not when the signal is delivered.
const settleAfterKill = 500 * time.Millisecond

// GroupOf returns the process group pid leads or belongs to, which for a tmux
// pane's own process is itself: tmux puts every pane in a fresh session +
// process group.
func GroupOf(pid int) (int, error) {
	return syscall.Getpgid(pid)
}

// KillGroup terminates one process group: SIGTERM, then SIGKILL to whatever is
// left once grace has passed.
func KillGroup(pgid int, grace time.Duration) error {
	return KillGroups([]int{pgid}, grace)
}

// KillGroups terminates several process groups at once, sharing ONE grace
// window: every group is asked to stop first, they wind down concurrently, and
// only then is the remainder killed. Per-group grace would multiply the wait by
// the number of groups while the caller (a dev take-over) is holding a user
// waiting on a port.
//
// The graceful step is not politeness — a dev server asked to stop closes its
// listening socket, whereas one that is SIGKILLed can leave the port in the
// kernel for a moment and the replacement lands on 8001.
func KillGroups(pgids []int, grace time.Duration) error {
	var errs []error
	var live []int
	seen := map[int]bool{}
	for _, pgid := range pgids {
		if seen[pgid] {
			continue
		}
		seen[pgid] = true
		if err := refuse(pgid); err != nil {
			errs = append(errs, err)
			continue
		}
		// EPERM is NOT a reason to stop: on macOS a group whose leader is an
		// unreaped zombie answers EPERM even though its live members are
		// perfectly signalable, and those members are precisely what this
		// exists to reach. ESRCH means the group is already gone.
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.EPERM) {
			if !errors.Is(err, syscall.ESRCH) {
				errs = append(errs, fmt.Errorf("SIGTERM %d: %w", pgid, err))
			}
			continue
		}
		live = append(live, pgid)
	}
	if len(live) == 0 {
		return errors.Join(errs...)
	}

	if waitGone(live, grace) {
		return errors.Join(errs...)
	}
	for _, pgid := range live {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil &&
			!errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
			errs = append(errs, fmt.Errorf("SIGKILL %d: %w", pgid, err))
		}
	}
	waitGone(live, settleAfterKill)
	return errors.Join(errs...)
}

// refuse rejects the group targets that must never be signalled.
func refuse(pgid int) error {
	if pgid <= 1 {
		return fmt.Errorf("refusing to signal process group %d", pgid)
	}
	// Never signal our OWN group: that is the daemon (and, in a test, the test
	// binary). A pane pid that resolves to it means the lookup went wrong, and
	// the blast radius of being wrong here is the whole process.
	if own, err := syscall.Getpgid(syscall.Getpid()); err == nil && own == pgid {
		return errors.New("refusing to signal lola's own process group")
	}
	return nil
}

// waitGone polls until every group has disappeared, or the window closes.
func waitGone(pgids []int, window time.Duration) bool {
	deadline := time.Now().Add(window)
	for {
		left := false
		for _, pgid := range pgids {
			if err := syscall.Kill(-pgid, 0); !errors.Is(err, syscall.ESRCH) {
				left = true
				break
			}
		}
		if !left {
			return true
		}
		if !time.Now().Add(pollInterval).Before(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}
