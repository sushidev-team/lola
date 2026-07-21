package daemon

// coderabbit.go now holds only the coderabbit-watch force command and its
// save helper. The observer-driven watch poll, watermark advance, transport
// dispatch, and hand-off all live in the provider-agnostic reviewer.go
// (runProviderWatch / routeFindings), keyed by the coderabbit-watch kind's
// guards (ReviewWatermarks / PendingHandoffs). Its invariants are unchanged and
// enforced there: opt-in, fire-once + survives downtime, and UNTRUSTED comment
// text (human sinks full text; the worker gets only a sanitized single-line
// pointer, never the raw comment).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// handleCodeRabbit serves cmd=coderabbit (`lola coderabbit <session>`): it FORCES
// a coderabbit-watch poll now for the named session, IGNORING the kind's
// watermark (it polls with a zero `since`, so the PR's CURRENT feedback is
// re-surfaced and re-routed — the analog of `lola review` ignoring its
// once-per-PR guard). It routes the comments the same way the observer does and
// reports a short outcome for the CLI: skipped (watch off / no open PR), none
// found, found, or error. An unknown session is an error.
func (d *Daemon) handleCodeRabbit(ctx context.Context, sessionID string) (protocol.CodeRabbitData, error) {
	if sessionID == "" {
		return protocol.CodeRabbitData{}, fmt.Errorf("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.CodeRabbitData{}, fmt.Errorf("unknown session %s", sessionID)
	}
	p, ok := d.watchProvider()
	if !ok {
		return protocol.CodeRabbitData{
			Skipped: "not enabled",
			Message: "coderabbit check skipped: no coderabbit-watch provider is enabled",
		}, nil
	}
	if s.Source != "native" || s.Repo == "" || !isReviewablePROpen(s.PR) {
		return protocol.CodeRabbitData{
			Skipped: "no open PR",
			Message: "coderabbit check skipped: session has no open PR to poll",
		}, nil
	}
	fetch := d.watchSeam()
	if fetch == nil {
		return protocol.CodeRabbitData{
			Skipped: "not enabled",
			Message: "coderabbit check skipped: the watch is not available",
		}, nil
	}

	// FORCE: poll from a zero watermark so the current feedback surfaces regardless
	// of what the observer has already routed. Still filter lola's own posted
	// comments when a github pass provider exists (fail-open when unresolved).
	selfLogin := d.selfLoginForWatch(ctx)
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	text, latest, err := fetch(cctx, s.Repo, s.PR.Number, time.Time{}, p.Author, selfLogin)
	cancel()
	if err != nil {
		return protocol.CodeRabbitData{}, fmt.Errorf("coderabbit check failed: %w", err)
	}

	// Keep the watermark monotonic so the next observer cycle does not re-fire what
	// this forced poll just routed (no-op when nothing is newer).
	d.advanceWatermark(s.ID, p.Kind, latest)

	text = strings.TrimSpace(text)
	if text == "" {
		return protocol.CodeRabbitData{
			Ran:     true,
			Message: fmt.Sprintf("coderabbit check: no CodeRabbit comments on PR #%d", s.PR.Number),
		}, nil
	}

	d.routeFindings(ctx, s, p, text)
	return protocol.CodeRabbitData{
		Ran:      true,
		Found:    true,
		Comments: text,
		Message:  "coderabbit check: routed CodeRabbit feedback to the configured sinks\n\n" + reviewHead(text, reviewNotifyHeadBytes),
	}, nil
}

// coderabbitSave persists the session store after a watch mutation, logging any
// failure. Watch state is best-effort durable — at worst a re-surface or re-send
// after a restart.
func (d *Daemon) coderabbitSave() {
	if err := d.sessions.Save(); err != nil {
		d.logf("", "coderabbit: persist sessions: %v", err)
	}
}
