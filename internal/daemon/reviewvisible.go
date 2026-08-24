package daemon

// reviewvisible.go runs a review pass WHERE YOU CAN SEE IT: instead of an
// invisible child of the daemon, the pass runs as `lola review-run` inside its
// own tmux session named "<sessionID>-review" on lola's own tmux server, beside
// the worker's session and the embedded shell tabs.
//
//	tmux -L lola attach -t =lola-nori-app-nor-357-review
//
// Two things make that more than a cosmetic change. The pass is asked to
// NARRATE: claude prints nothing at all until it finishes, so its visible run
// switches to the stream format and renders each event to a plain line
// (internal/reviewagent/stream.go), while codex and opencode already narrate on
// stderr and simply have it teed to the pane — either way it shows each file the
// reviewer reads. And the pane HOLDS after the pass ends, so the findings stay
// readable; the next review for that session replaces the whole session.
//
// The pane is a DISPLAY, never a channel: it wraps, scrolls and is eventually
// overwritten, so the daemon never parses it. The child writes the findings and
// an outcome class to a state directory (internal/reviewrun) and the daemon
// polls for that, mapping the class back onto the same Err* sentinels a direct
// exec would have produced — the fallback chain, the retry budget and every
// transport cannot tell the difference.
//
// Everything degrades to the direct exec: no tmux, no session id, or a tmux
// that will not start the session all fall back to the in-process client.

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/reviewrun"
)

const (
	// reviewPaneSuffix names a session's review pane. It mirrors the embedded
	// shell tabs' "-shell-N" convention, and runtime.IsAuxSession keeps adoption
	// from mistaking either for a session of its own.
	reviewPaneSuffix = "-review"
	// reviewPollInterval is how often the daemon looks for the child's status
	// file. A review runs for minutes; a 2s poll is free.
	reviewPollInterval = 2 * time.Second
	// reviewVisibleGrace is how long past the pass's own timeout the daemon
	// waits before giving up on the child (which self-bounds and normally
	// writes a timeout status of its own). It covers process start-up and the
	// write itself, and only then is the pane torn down.
	reviewVisibleGrace = 60 * time.Second
)

// reviewPaneName is the tmux session name of s's review pane.
func reviewPaneName(sessionID string) string { return sessionID + reviewPaneSuffix }

// visibleSeam wraps a pass provider's exec so it runs in a review pane. The
// returned function has the pass-seam signature, so it drops straight into
// d.passRuns; direct is the in-process client call it falls back to whenever a
// pane cannot be used.
func (d *Daemon) visibleSeam(cp config.ReviewProvider, direct passRun) passRun {
	return func(ctx context.Context, sessionID, dir, base string) (string, error) {
		if sessionID == "" {
			return direct(ctx, sessionID, dir, base)
		}
		findings, err, ran := d.runInReviewPane(ctx, cp, sessionID, dir, base)
		if !ran {
			return direct(ctx, sessionID, dir, base)
		}
		return findings, err
	}
}

// runInReviewPane starts the child in a fresh review pane and waits for its
// result. ran is false when the pane could not be used at all (no tmux, tmux
// refused the session) — the caller then falls back to the direct exec, so a
// broken pane never costs a review.
func (d *Daemon) runInReviewPane(ctx context.Context, cp config.ReviewProvider, sessionID, dir, base string) (findings string, err error, ran bool) {
	d.mu.Lock()
	home, lolaBin := d.home, d.lolaBin
	d.mu.Unlock()
	tm := d.tmuxClient()
	if tm == nil || !tm.Available() || lolaBin == "" {
		return "", nil, false
	}

	state := reviewrun.StateDir(home, sessionID)
	if perr := reviewrun.Prepare(state); perr != nil {
		d.logf("", "review: %s could not prepare the review pane state dir: %v", sessionID, perr)
		return "", nil, false
	}

	// One pane per session: the previous review's pane (held open so its output
	// stayed readable) is replaced, never accumulated.
	name := reviewPaneName(sessionID)
	kctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	if tm.Has(kctx, name) {
		_ = tm.KillSession(kctx, name)
	}
	cancel()

	sctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	startErr := tm.NewSession(sctx, name, dir, reviewPaneCommand(lolaBin, cp, dir, base, state))
	cancel()
	if startErr != nil {
		d.logf("", "review: %s could not open the review pane (running it in the background instead): %v", sessionID, startErr)
		return "", nil, false
	}
	d.logf("", "review: %s (%s) running in pane %s", sessionID, cp.Provider, name)

	st, findings, err := d.awaitReviewPane(ctx, tm, name, state, time.Now().Add(visibleDeadline(cp)))
	if err != nil {
		return "", err, true
	}
	return findings, st.Err(), true
}

// awaitReviewPane polls the state directory until the child reports, the
// caller's context ends, or deadline passes (in which case the pane is torn
// down — a wedged child must not hold a pane, and the chain needs a timeout to
// fall back on). The deadline is a parameter, not derived here, so a test can
// drive the give-up path without waiting minutes for it.
func (d *Daemon) awaitReviewPane(ctx context.Context, tm tmuxKiller, name, state string, deadline time.Time) (reviewrun.Status, string, error) {
	tick := time.NewTicker(reviewPollInterval)
	defer tick.Stop()
	for {
		st, findings, done, err := reviewrun.Read(state)
		if err != nil {
			return reviewrun.Status{}, "", fmt.Errorf("review: reading the review pane result: %w", err)
		}
		if done {
			return st, findings, nil
		}
		if time.Now().After(deadline) {
			kctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reactExecTimeout)
			_ = tm.KillSession(kctx, name)
			cancel()
			return reviewrun.Status{
				Class:   reviewrun.ClassTimeout,
				Message: "the review pane never reported its result",
			}, "", nil
		}
		select {
		case <-ctx.Done():
			// Shutdown (or a cancelled manual command): leave the pane alone —
			// it is the user's window onto a run that is still going, and the
			// next daemon replaces it.
			return reviewrun.Status{}, "", ctx.Err()
		case <-tick.C:
		}
	}
}

// tmuxKiller is the slice of the tmux client awaitReviewPane needs, so tests can
// drive the wait without a tmux server.
type tmuxKiller interface {
	KillSession(ctx context.Context, name string) error
}

// visibleDeadline is how long the daemon waits on the child: the pass's own
// timeout plus the grace window.
func visibleDeadline(cp config.ReviewProvider) time.Duration {
	secs := cp.TimeoutSeconds
	if secs <= 0 {
		secs = config.DefaultReviewTimeoutSeconds
	}
	return time.Duration(secs)*time.Second + reviewVisibleGrace
}

// reviewPaneCommand builds the ONE shell-command argument tmux passes to the
// user's login shell. As with the agent launch line, that shell may be
// fish/csh, so the real command is wrapped in an explicit `sh -c '<line>'` and
// every value is quoted. Nothing secret appears here — the child inherits the
// daemon's environment for its own auth, exactly as a direct exec would.
func reviewPaneCommand(lolaBin string, cp config.ReviewProvider, dir, base, state string) string {
	args := []string{
		shQuoteArg(lolaBin), "review-run",
		"--kind", shQuoteArg(string(cp.Provider)),
		"--dir", shQuoteArg(dir),
		"--base", shQuoteArg(base),
		"--state", shQuoteArg(state),
	}
	if cp.Model != "" {
		args = append(args, "--model", shQuoteArg(cp.Model))
	}
	if cp.Command != "" {
		args = append(args, "--command", shQuoteArg(cp.Command))
	}
	// base_flag is passed ONLY when it differs from the child's own default, so
	// the common line stays short — but an explicitly EMPTY one must still cross
	// (it means "append no base at all", which is not the default).
	if cp.BaseFlag != config.DefaultReviewBaseFlag && config.IsCLIKind(string(cp.Provider)) {
		args = append(args, "--base-flag", shQuoteArg(cp.BaseFlag))
	}
	if cp.TimeoutSeconds > 0 {
		args = append(args, "--timeout-seconds", fmt.Sprint(cp.TimeoutSeconds))
	}
	return "sh -c " + shQuoteArg(strings.Join(args, " "))
}

// safeWord matches an argument that needs no quoting at all (the common case:
// paths, kinds, branch names).
var safeWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shQuoteArg single-quotes an argument for the POSIX line above, mirroring
// internal/runtime's own shQuote (kept separate: that one is unexported and
// serves the agent launch line).
func shQuoteArg(s string) string {
	if s != "" && safeWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// killReviewPane tears down a session's review pane, best-effort. It runs when
// the session itself is killed, so a dead worker never leaves a review pane
// behind, and when its state directory is dropped.
func (d *Daemon) killReviewPane(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	tm := d.tmuxClient()
	if tm == nil || !tm.Available() {
		return
	}
	name := reviewPaneName(sessionID)
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if tm.Has(cctx, name) {
		_ = tm.KillSession(cctx, name)
	}
}

// reviewStateDir is the on-disk home of a session's visible-pass files, exposed
// for the kill path to clean up.
func (d *Daemon) reviewStateDir(sessionID string) string {
	d.mu.Lock()
	home := d.home
	d.mu.Unlock()
	return filepath.Join(reviewrun.StateDir(home, sessionID))
}
