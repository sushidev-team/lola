//go:build !unix

package proctree

import (
	"errors"
	"time"
)

// Process groups are a POSIX concept. Every caller already degrades to the
// plain tmux kill-session it falls back to whenever a group cannot be resolved
// or signalled; lola targets macOS/Linux (it drives tmux), and this file only
// keeps the package buildable elsewhere.

func GroupOf(int) (int, error) {
	return 0, errors.New("process groups unsupported on this platform")
}

func KillGroup(int, time.Duration) error {
	return errors.New("process groups unsupported on this platform")
}

func KillGroups([]int, time.Duration) error {
	return errors.New("process groups unsupported on this platform")
}
