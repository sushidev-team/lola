// Package reviewrun is the tiny hand-off protocol between the two halves of a
// VISIBLE review pass: `lola review-run` (the child that executes the review
// inside its own tmux pane, so a human can watch it) and the daemon that
// started that pane and needs the result.
//
// The pane is a display, not a channel — its text is wrapped, scrolled and
// eventually overwritten, so nothing may be parsed back out of it. The child
// therefore writes two files into a state directory instead: the findings
// verbatim, and a one-line status naming the outcome CLASS. The daemon polls for
// the status file, reads the findings beside it, and maps the class back onto
// the same Err* sentinels a direct exec would have returned — so the fallback
// chain, the retry budget and every transport behave identically whether the
// pass ran visibly or not.
//
// The package is a leaf on purpose: it imports the two review clients only to
// classify their sentinels, and nothing imports it but main and the daemon.
package reviewrun

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sushidev-team/lola/internal/review"
	"github.com/sushidev-team/lola/internal/reviewagent"
)

const (
	// findingsName holds the pass's findings verbatim (empty file = clean).
	findingsName = "findings.txt"
	// statusName is written LAST and is the completion signal the daemon polls
	// for: "<class>\n<message>". Its presence means the child is done.
	statusName = "status"
)

// Outcome classes. They are provider-agnostic: a coderabbit-cli timeout and a
// codex-session timeout both come back as ClassTimeout, exactly as the two
// packages' own ErrTimeout sentinels are treated alike by the chain.
const (
	ClassOK       = "ok"
	ClassNotFound = "notfound"
	ClassTimeout  = "timeout"
	ClassAuth     = "auth"
	ClassQuota    = "quota"
	ClassExit     = "exit"
	ClassFailed   = "failed" // anything else (git failed, unreadable stream, …)
)

// Status is a finished child's outcome.
type Status struct {
	Class   string
	Message string
}

// StateDir is the per-session directory holding one visible pass's files. It
// lives under the lola home (never in the worktree, which the user's agent is
// working in and which a kill may remove underneath us).
func StateDir(home, sessionID string) string {
	return filepath.Join(home, "cache", "review", sessionID)
}

// Prepare creates (and empties) the state directory, so a stale status from the
// previous pass can never be read as this one's completion signal.
func Prepare(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, n := range []string{findingsName, statusName} {
		if err := os.Remove(filepath.Join(dir, n)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Write records a finished pass: the findings first, the status LAST — the
// daemon treats the status file as "everything is on disk", so the order is
// load-bearing.
func Write(dir, findings string, passErr error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, findingsName), []byte(findings), 0o600); err != nil {
		return err
	}
	class, msg := Classify(passErr)
	body := class + "\n" + strings.ReplaceAll(msg, "\n", " ")
	return os.WriteFile(filepath.Join(dir, statusName), []byte(body), 0o600)
}

// Read returns the finished pass's status and findings. ok is false while the
// status file is absent — the child is still running (or never started).
func Read(dir string) (st Status, findings string, ok bool, err error) {
	raw, err := os.ReadFile(filepath.Join(dir, statusName))
	if os.IsNotExist(err) {
		return Status{}, "", false, nil
	}
	if err != nil {
		return Status{}, "", false, err
	}
	class, msg, _ := strings.Cut(strings.TrimRight(string(raw), "\n"), "\n")
	f, err := os.ReadFile(filepath.Join(dir, findingsName))
	if err != nil && !os.IsNotExist(err) {
		return Status{}, "", false, err
	}
	return Status{Class: strings.TrimSpace(class), Message: strings.TrimSpace(msg)}, string(f), true, nil
}

// Classify maps a pass error onto its transport-safe class and message. The
// message is the error's own text, which both packages already scrub through
// their redactSecrets before it can carry anything credential-shaped.
func Classify(err error) (class, message string) {
	switch {
	case err == nil:
		return ClassOK, ""
	case errors.Is(err, review.ErrNotFound), errors.Is(err, reviewagent.ErrNotFound):
		return ClassNotFound, err.Error()
	case errors.Is(err, review.ErrTimeout), errors.Is(err, reviewagent.ErrTimeout):
		return ClassTimeout, err.Error()
	case errors.Is(err, review.ErrQuota), errors.Is(err, reviewagent.ErrQuota):
		return ClassQuota, err.Error()
	case errors.Is(err, review.ErrAuth), errors.Is(err, reviewagent.ErrAuth):
		return ClassAuth, err.Error()
	case errors.Is(err, review.ErrExit), errors.Is(err, reviewagent.ErrExit):
		return ClassExit, err.Error()
	}
	return ClassFailed, err.Error()
}

// Err maps a status back to an error the review chain understands: the
// fallback-class sentinels advance the chain, the graceful-stop ones stop it,
// and ClassOK is no error at all. The child's message is wrapped in so a log
// line reads the same as a direct exec's.
func (s Status) Err() error {
	switch s.Class {
	case ClassOK, "":
		return nil
	case ClassNotFound:
		return wrap(reviewagent.ErrNotFound, s.Message)
	case ClassTimeout:
		return wrap(reviewagent.ErrTimeout, s.Message)
	case ClassQuota:
		return wrap(reviewagent.ErrQuota, s.Message)
	case ClassAuth:
		return wrap(reviewagent.ErrAuth, s.Message)
	case ClassExit:
		return wrap(reviewagent.ErrExit, s.Message)
	}
	if s.Message != "" {
		return fmt.Errorf("review: visible pass failed: %s", s.Message)
	}
	return errors.New("review: visible pass failed")
}

// wrap keeps the sentinel matchable (errors.Is) while carrying the child's own
// message. A message that already restates the sentinel is not repeated.
func wrap(sentinel error, msg string) error {
	if msg == "" || strings.Contains(msg, sentinel.Error()) {
		if msg == "" {
			return sentinel
		}
		return fmt.Errorf("%w: %s", sentinel, strings.TrimSpace(strings.TrimPrefix(msg, sentinel.Error())))
	}
	return fmt.Errorf("%w: %s", sentinel, msg)
}
