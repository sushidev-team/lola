//go:build !unix

package tmux

import (
	"errors"
	"time"
)

// On a platform without POSIX process groups there is nothing to signal, so
// KillSessionTree degrades to the plain kill-session it already falls back to
// whenever the pane's pid cannot be resolved. lola targets macOS/Linux (it
// drives tmux); this file only keeps the package buildable elsewhere.

func processGroupOf(int) (int, error) { return 0, errors.New("process groups unsupported on this platform") }

func killProcessGroup(int, time.Duration) error {
	return errors.New("process groups unsupported on this platform")
}
