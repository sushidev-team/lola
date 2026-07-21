package daemon

// reviewer.go is the HEART of the flexible review system (PLAN flexible-review
// §2–5). It generalizes the two hardcoded review tables — the [review] CLI pass
// (review.go) and the [coderabbit] PR-comment watch (coderabbit.go) — into a set
// of pluggable PROVIDERS, each with:
//
//   - a KIND (coderabbit-cli | coderabbit-watch | claude-session) mapping to one
//     of two execution SHAPES: a sync "pass" (exec, return findings) or a "watch"
//     (poll the PR for bot comments against a watermark);
//   - a set of TRANSPORTS (the sinks its findings route to: notify + agent via
//     the always-on `lola` transport, plus opt-in `github` and `linear`);
//   - for pass shapes, an ordered FALLBACK chain of other kinds tried when this
//     provider cannot answer (unavailable / over-quota / timeout).
//
// The descriptors live on d.reviewProviders (guarded by d.mu), built by
// setReviewProvidersLocked from the [[review.provider]] catalog OR synthesized
// from the legacy tables. The raw exec CLIENTS stay single func-field seams
// (d.reviewRun / d.claudeReviewRun / d.coderabbitComments / d.postPRComment) so
// the existing fake-install test model keeps working; the descriptor never
// captures a seam — runReviewChain / runProviderWatch look it up under d.mu AT
// CALL TIME (late binding), so a fake installed after setup still wins.
//
// Every invariant of the two legacy features is preserved per kind: fire-once
// guards (now kind-keyed maps), the sanitize + AtPrompt idle-gate on the worker
// hand-off, human-only untrusted text on notify/linear/github, fail-closed
// graceful skips, the per-cycle shutdown-shielded budget, and secret discipline.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/review"
	"github.com/sushidev-team/lola/internal/reviewclaude"
	"github.com/sushidev-team/lola/internal/session"
)

// provKind is the daemon-side provider kind. It mirrors config's (unexported)
// kind values as plain strings so guard maps key by kind and descriptors are
// built from the catalog by string conversion.
type provKind string

const (
	kindCoderabbitCLI   provKind = "coderabbit-cli"
	kindCoderabbitWatch provKind = "coderabbit-watch"
	kindClaudeSession   provKind = "claude-session"
)

// provShape is a kind's execution shape.
type provShape int

const (
	shapePass  provShape = iota // exec once, return findings (cli / claude)
	shapeWatch                  // poll the PR for bot comments against a watermark
)

// handoffStyle selects the worker-agent payload: the FULL sanitized findings
// (cli / claude) or a SINGLE-LINE pointer to the PR (watch).
type handoffStyle int

const (
	handoffFull    handoffStyle = iota // full sanitized findings
	handoffPointer                     // single-line PR pointer (no raw comment text)
)

// reviewProvider is one resolved provider descriptor. It carries only the
// routing/guard facts the daemon needs; the exec client is reached through a
// late-bound seam keyed by Kind (see passSeam / watchSeam), never captured here.
type reviewProvider struct {
	Kind        provKind
	Shape       provShape
	Enabled     bool
	OnPROpen    bool                // pass shapes: run on the PR-open transition
	Transports  config.TransportSet // resolved sinks (always contains lola)
	Notify      bool                // lola: fire the notify sink
	SendToAgent bool                // lola: fire the worker hand-off
	Handoff     handoffStyle
	Fallback    []provKind // ordered chain (pass shapes only)
	Author      string     // watch only
	// TimeoutSeconds is the pass provider's own exec bound; it feeds the per-cycle
	// budget ceiling only (each exec self-bounds via its client), 0 ⇒ default.
	TimeoutSeconds int
}

// setReviewProvidersLocked (re)builds the provider descriptor set AND the per-kind
// exec clients from nc. Caller holds d.mu. It consumes nc.EffectiveReviewProviders
// so a catalog file wins and a legacy-only file is synthesized identically. Called
// from Run and handleReload so enabling/disabling a provider (or changing its
// command/timeout/transports/fallback) takes effect live.
//
// The clients (d.review/d.reviewRun for coderabbit-cli, d.claudeReview/
// d.claudeReviewRun for claude-session) are rebuilt for EVERY enabled pass
// provider — including a fallback-only one — so the chain can reach it; a nil
// seam means that kind's binary is unavailable, which the chain treats as
// "can't answer" (advance / skip). The watch fetch seam is stateless and left
// as wired in newDaemon.
func (d *Daemon) setReviewProvidersLocked(nc *config.Config) {
	eff := nc.EffectiveReviewProviders()

	// Reset the pass clients; each enabled pass provider rebuilds its own below.
	d.review, d.reviewRun = nil, nil
	d.claudeReview, d.claudeReviewRun = nil, nil

	descs := make([]reviewProvider, 0, len(eff))
	for _, cp := range eff {
		desc := reviewProvider{
			Kind:           provKind(string(cp.Provider)),
			Enabled:        cp.Enabled,
			OnPROpen:       cp.OnPROpen,
			Transports:     cp.Transports,
			Notify:         cp.Notify,
			SendToAgent:    cp.SendToAgent,
			Author:         cp.Author,
			Fallback:       toDaemonKinds(cp.Fallback),
			TimeoutSeconds: cp.TimeoutSeconds,
		}
		switch desc.Kind {
		case kindClaudeSession:
			desc.Shape, desc.Handoff = shapePass, handoffFull
			if cp.Enabled {
				if cl := buildClaudeReview(cp); cl != nil {
					d.claudeReview = cl
					d.claudeReviewRun = cl.Review
				}
			}
		case kindCoderabbitWatch:
			desc.Shape, desc.Handoff = shapeWatch, handoffPointer
		default: // coderabbit-cli
			desc.Shape, desc.Handoff = shapePass, handoffFull
			if cp.Enabled {
				if cl := buildReview(config.ReviewConfig{
					Enabled:        true,
					Command:        cp.Command,
					TimeoutSeconds: cp.TimeoutSeconds,
				}); cl != nil {
					d.review = cl
					d.reviewRun = cl.Review
				}
			}
		}
		descs = append(descs, desc)
	}
	d.reviewProviders = descs
}

// toDaemonKinds converts a catalog fallback chain (config's unexported provKind)
// into the daemon-side provKind slice by string conversion.
func toDaemonKinds[K ~string](in []K) []provKind {
	if len(in) == 0 {
		return nil
	}
	out := make([]provKind, len(in))
	for i, k := range in {
		out[i] = provKind(string(k))
	}
	return out
}

// buildClaudeReview constructs the headless-claude review client for a
// claude-session provider, or nil when claude is not on PATH (so the seam stays
// nil and the chain skips this provider). Model / timeout come from the entry.
func buildClaudeReview(cp config.ReviewProvider) *reviewclaude.Client {
	cl := &reviewclaude.Client{Model: cp.Model}
	if cp.TimeoutSeconds > 0 {
		cl.Timeout = time.Duration(cp.TimeoutSeconds) * time.Second
	}
	if !cl.Available() {
		return nil
	}
	return cl
}

// reviewUnavailableWarnLocked returns a one-line startup warning naming any
// enabled PASS provider whose binary is unavailable (its seam came back nil), or
// "" when everything enabled is available. Caller holds d.mu. Mirrors the old
// "[review].enabled but no coderabbit" single log line, generalized per kind.
func (d *Daemon) reviewUnavailableWarnLocked() string {
	var missing []string
	for _, p := range d.reviewProviders {
		if !p.Enabled || p.Shape != shapePass {
			continue
		}
		switch p.Kind {
		case kindCoderabbitCLI:
			if d.reviewRun == nil {
				missing = append(missing, "coderabbit-cli (coderabbit not on PATH; run: coderabbit auth login)")
			}
		case kindClaudeSession:
			if d.claudeReviewRun == nil {
				missing = append(missing, "claude-session (claude not on PATH)")
			}
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "review: enabled provider(s) unavailable, that pass will not run: " + strings.Join(missing, "; ")
}

// --- descriptor queries (all snapshot d.reviewProviders under d.mu) ----------

// providerByKind returns the descriptor for k (enabled or not).
func (d *Daemon) providerByKind(k provKind) (reviewProvider, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.reviewProviders {
		if p.Kind == k {
			return p, true
		}
	}
	return reviewProvider{}, false
}

// appliesIndependently returns the enabled providers that run PER SESSION on
// their own — i.e. every enabled provider EXCEPT one that is referenced in
// another enabled provider's fallback chain. A fallback-only provider runs ONLY
// when reached via runReviewChain, never as its own independent pass, so it is
// never double-reviewed / double-handed-off.
func (d *Daemon) appliesIndependently() []reviewProvider {
	d.mu.Lock()
	provs := append([]reviewProvider(nil), d.reviewProviders...)
	d.mu.Unlock()

	referenced := map[provKind]bool{}
	for _, p := range provs {
		if !p.Enabled {
			continue
		}
		for _, fb := range p.Fallback {
			referenced[fb] = true
		}
	}
	var out []reviewProvider
	for _, p := range provs {
		if p.Enabled && !referenced[p.Kind] {
			out = append(out, p)
		}
	}
	return out
}

// anyPassOnPROpenLocked reports whether any enabled pass provider triggers on
// PR-open — the generalized gate for installing the per-cycle review budget.
// Caller holds d.mu.
func (d *Daemon) anyPassOnPROpenLocked() bool {
	for _, p := range d.reviewProviders {
		if p.Enabled && p.Shape == shapePass && p.OnPROpen {
			return true
		}
	}
	return false
}

// reviewCycleBudgetLocked returns the outer per-cycle budget: the LARGEST enabled
// pass provider timeout (so the ceiling never cuts a legitimately-slow provider
// short), defaulting to DefaultReviewTimeoutSeconds. Caller holds d.mu.
func (d *Daemon) reviewCycleBudgetLocked() time.Duration {
	max := config.DefaultReviewTimeoutSeconds
	for _, p := range d.reviewProviders {
		if p.Enabled && p.Shape == shapePass && p.TimeoutSeconds > max {
			max = p.TimeoutSeconds
		}
	}
	return time.Duration(max) * time.Second
}

// primaryPassProvider returns the pass provider the manual `lola review` command
// forces by default: the first enabled, independently-applying pass provider.
func (d *Daemon) primaryPassProvider() (reviewProvider, bool) {
	for _, p := range d.appliesIndependently() {
		if p.Shape == shapePass {
			return p, true
		}
	}
	return reviewProvider{}, false
}

// resolveReviewForce resolves the provider `lola review [--provider kind]` forces.
// An empty kind falls back to primaryPassProvider (the daemon-wide default); a
// named kind must be a configured, ENABLED provider (its shape is checked by the
// caller so a watch kind can be reported distinctly). Returns false when nothing
// matches so the caller reports "skipped", not an error.
func (d *Daemon) resolveReviewForce(kind string) (reviewProvider, bool) {
	if kind == "" {
		return d.primaryPassProvider()
	}
	p, ok := d.providerByKind(provKind(kind))
	if !ok || !p.Enabled {
		return reviewProvider{}, false
	}
	return p, true
}

// watchProvider returns the enabled coderabbit-watch descriptor, if any.
func (d *Daemon) watchProvider() (reviewProvider, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.reviewProviders {
		if p.Enabled && p.Shape == shapeWatch {
			return p, true
		}
	}
	return reviewProvider{}, false
}

// anyGithubPassLocked reports whether any enabled pass provider posts to github —
// the condition under which the watch must filter lola's own comments. Caller
// holds d.mu.
func (d *Daemon) anyGithubPassLocked() bool {
	for _, p := range d.reviewProviders {
		if p.Enabled && p.Shape == shapePass && p.Transports.Has(config.TransportGitHub) {
			return true
		}
	}
	return false
}

// --- late-bound seam lookups (read the live seam under d.mu at call time) -----

func (d *Daemon) passSeam(k provKind) func(ctx context.Context, dir, base string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch k {
	case kindCoderabbitCLI:
		return d.reviewRun
	case kindClaudeSession:
		return d.claudeReviewRun
	}
	return nil
}

func (d *Daemon) watchSeam() func(ctx context.Context, repo string, pr int, since time.Time, author, selfLogin string) (string, time.Time, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.coderabbitComments
}

func (d *Daemon) postSeam() func(ctx context.Context, repo string, pr int, body string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.postPRComment
}

// resolveSelfLogin returns lola's own gh login, resolved AT MOST ONCE per daemon
// lifetime (selfLoginOnce), so the watch's self-feedback filter adds no per-cycle
// gh exec. A resolution failure is fail-open: it caches "" (filter disabled) and
// is never retried — the default author "coderabbitai" already can't collide with
// lola's login.
func (d *Daemon) resolveSelfLogin(ctx context.Context) string {
	d.selfLoginOnce.Do(func() {
		d.mu.Lock()
		fn := d.authedLogin
		d.mu.Unlock()
		if fn == nil {
			return
		}
		login, err := fn(ctx)
		if err != nil {
			d.logf("", "review: could not resolve gh login (self-feedback filter disabled): %v", err)
			return
		}
		d.mu.Lock()
		d.selfLogin = login
		d.mu.Unlock()
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.selfLogin
}

// selfLoginForWatch resolves the self-filter login only when a github-posting
// pass provider is configured (otherwise "" — no filter, no exec).
func (d *Daemon) selfLoginForWatch(ctx context.Context) string {
	d.mu.Lock()
	need := d.anyGithubPassLocked()
	d.mu.Unlock()
	if !need {
		return ""
	}
	return d.resolveSelfLogin(ctx)
}

// --- per-session run (observer entry points) ---------------------------------

// runReviewProviders runs every independently-applying provider for s in one
// observer cycle: a pass provider fires its PR-open chain (guarded once per PR
// per kind); a watch provider polls its watermark. Each kind's guards are
// independent, so one firing never suppresses another. No-op when no providers
// are configured.
func (d *Daemon) runReviewProviders(ctx context.Context, s session.Session) {
	for _, p := range d.appliesIndependently() {
		if p.Shape == shapeWatch {
			d.runProviderWatch(ctx, s, p)
			continue
		}
		d.runProviderPassOnPROpen(ctx, s, p)
	}
}

// runProviderPassOnPROpen is the observer-driven auto-trigger for one pass
// provider: it runs the chain the first time s has an open PR (opt-in via
// OnPROpen), guarded so it fires at most once per PR per kind (ReviewedPRs[kind]).
func (d *Daemon) runProviderPassOnPROpen(ctx context.Context, s session.Session, p reviewProvider) {
	if !p.Enabled || !p.OnPROpen {
		return
	}
	if s.Source != "native" || !isReviewablePROpen(s.PR) {
		return
	}
	if s.ReviewedPRs[string(p.Kind)] == s.PR.Number {
		return // already reviewed this PR for this kind — never re-run on the cadence
	}
	d.runReviewChain(ctx, s, p, true)
}

// runReviewChain runs the primary provider p and, when p can't answer, advances
// through p.Fallback (PLAN §3.1). It ALWAYS runs the exec (the once-per-PR gate
// lives in runProviderPassOnPROpen; the manual command calls here to ignore it).
//
//   - The chain guard (ReviewedPRs[p.Kind]) is stamped BEFORE any exec so a crash
//     or the next cycle can never double-fire; it is keyed on the PRIMARY kind so
//     a fell-through fallback does not re-fire next cycle.
//   - Attempt list = [primary] + fallback. An entry whose seam is nil (binary
//     unavailable) is skipped. The first available entry runs.
//   - err == nil ⇒ route the findings under the PRIMARY's transports, STOP.
//   - err in {ErrNotFound, ErrTimeout, ErrQuota} ⇒ advance to the next entry.
//   - err == ErrAuth / ErrExit ⇒ graceful skip, STOP (no fallback): auth is an
//     operator fix and a real exit must not silently burn the paid fallback.
//   - chain exhausted ⇒ graceful skip (guard left set, logged once).
//
// useCycleBudget selects the exec context: the auto-trigger passes true to run
// under the shared per-cycle budget; the manual command passes false so it runs
// under its own caller ctx (immune to a concurrent cycle cancelling it).
func (d *Daemon) runReviewChain(ctx context.Context, s session.Session, p reviewProvider, useCycleBudget bool) reviewResult {
	d.mu.Lock()
	home := d.home
	proj := d.cfg.ProjectByName(s.Project)
	d.mu.Unlock()

	if s.Project == "" || proj == nil {
		return reviewResult{Skipped: "session has no project to review"}
	}
	base := proj.DefaultBranch
	if base == "" {
		base = config.DefaultBranchName
	}
	dir := filepath.Join(home, "worktrees", proj.Name, s.ID)

	// Stamp the one-shot chain guard BEFORE the (long) exec (a no-op when the PR
	// already matches or the session has no PR yet).
	if s.PR != nil && s.PR.Number > 0 {
		d.stampReviewed(s.ID, p.Kind, s.PR.Number)
	}

	execCtx := ctx
	if useCycleBudget {
		execCtx = d.reviewContext(ctx)
	}

	prNum := 0
	if s.PR != nil {
		prNum = s.PR.Number
	}

	attempts := append([]provKind{p.Kind}, p.Fallback...)
	var lastErr error
	for _, k := range attempts {
		run := d.passSeam(k)
		if run == nil {
			continue // unavailable: advance (fail-closed to no review for this entry)
		}
		findings, err := run(execCtx, dir, base)
		if err == nil {
			if k != p.Kind {
				d.logf("", "review: %s primary %s could not answer; fallback %s reviewed PR #%d", s.ID, p.Kind, k, prNum)
			}
			d.routeFindings(ctx, s, p, findings)
			return reviewResult{Ran: true, Findings: strings.TrimSpace(findings)}
		}
		lastErr = err
		if isFallbackErr(err) {
			d.logf("", "review: %s %s could not answer PR #%d (%v) — trying next provider", s.ID, k, prNum, err)
			continue // advance to the next fallback entry
		}
		// ErrAuth / ErrExit / anything else: graceful skip, NO fallback.
		d.logf("", "review: pass for %s (PR #%d) via %s failed, skipping: %v", s.ID, prNum, k, err)
		return reviewResult{Err: err}
	}
	// Exhausted (every entry unavailable or a fallback-class error).
	if lastErr != nil {
		d.logf("", "review: %s chain for PR #%d exhausted without an answer: %v", s.ID, prNum, lastErr)
		return reviewResult{Err: lastErr}
	}
	return reviewResult{Skipped: "no available review provider"}
}

// isFallbackErr reports whether err is a "provider can't answer right now" class
// that should advance to a fallback (over BOTH the review and reviewclaude
// sentinels). ErrAuth / ErrExit are deliberately excluded — they are a graceful
// stop, not a fall-through.
func isFallbackErr(err error) bool {
	switch {
	case errors.Is(err, review.ErrNotFound), errors.Is(err, review.ErrTimeout), errors.Is(err, review.ErrQuota):
		return true
	case errors.Is(err, reviewclaude.ErrNotFound), errors.Is(err, reviewclaude.ErrTimeout), errors.Is(err, reviewclaude.ErrQuota):
		return true
	}
	return false
}

// runProviderWatch polls session s's open PR for new bot feedback for the watch
// provider p and routes it. No-op when the watch is off, the PR is not open, the
// session has no repo, or nothing is newer than the kind's watermark. The
// watermark advances BEFORE routing (fire-once, survives downtime); the fetch
// filters lola's own github-posted comments when a github pass provider exists.
func (d *Daemon) runProviderWatch(ctx context.Context, s session.Session, p reviewProvider) {
	if !p.Enabled {
		return
	}
	if s.Source != "native" || s.Repo == "" || !isReviewablePROpen(s.PR) {
		return
	}
	fetch := d.watchSeam()
	if fetch == nil {
		return
	}
	since := s.ReviewWatermarks[string(p.Kind)]
	selfLogin := d.selfLoginForWatch(ctx)

	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	text, latest, err := fetch(cctx, s.Repo, s.PR.Number, since, p.Author, selfLogin)
	cancel()
	if err != nil {
		d.logf("", "review: watch (%s) for %s (PR #%d) failed: %v", p.Kind, s.ID, s.PR.Number, err)
		return
	}

	// Advance the watermark FIRST — before any routing — so a failed notify / send
	// never re-surfaces the same comment. A deferred worker hand-off carries the
	// text on PendingHandoffs, so advancing now loses nothing.
	if latest.After(since) {
		d.advanceWatermark(s.ID, p.Kind, latest)
	}
	if strings.TrimSpace(text) == "" {
		return // watermark moved (or not); nothing new to route
	}
	d.routeFindings(ctx, s, p, text)
}

// --- unified transport dispatch ----------------------------------------------

// routeFindings surfaces the (UNTRUSTED) findings at the provider's resolved
// sinks (PLAN §5.1). It replaces routeReviewFindings + routeCodeRabbit.
//
//   - CLEAN (findings == ""): an optional Info "no issues" notify (if p.Notify),
//     then RETURN — skip agent/linear/github (github especially: gh rejects an
//     empty body).
//   - else per resolved sink:
//   - notify (p.Notify): a human Action notify with a short HEAD. Full text,
//     NO sanitize; per-kind title so a claude review is never labelled CodeRabbit.
//   - agent (p.SendToAgent): the EXISTING gated hand-off — full sanitized
//     findings (handoffFull) or a single-line pointer (handoffPointer).
//   - linear (transport present): a Linear comment (human sink, no sanitize).
//   - github (transport present, pass shapes only): a `gh pr comment` post.
//
// Only the agent sink sanitizes + idle-gates; notify/linear/github get full
// untrusted text — the opposite treatment.
func (d *Daemon) routeFindings(ctx context.Context, s session.Session, p reviewProvider, findings string) {
	d.mu.Lock()
	notifier := d.notifier
	d.mu.Unlock()
	if notifier == nil {
		notifier = notify.New(notify.NotifyConfig{})
	}
	lbl := labelsFor(p.Kind)

	findings = strings.TrimSpace(findings)
	if findings == "" {
		if p.Notify {
			notifier.Notify(ctx, notify.Note{
				Title:    lbl.notifyTitle,
				Body:     fmt.Sprintf("%s: %s found no issues", issueLabel(s), lbl.notifyTitle),
				Priority: notify.Info,
				URL:      prURL(s),
			})
		}
		d.logf("", "review: %s (%s) clean (no issues)", s.ID, p.Kind)
		return
	}

	// notify — UNTRUSTED sink #1 (display-only, full text safe).
	if p.Notify {
		notifier.Notify(ctx, notify.Note{
			Title:    lbl.notifyTitle,
			Body:     reviewHead(findings, reviewNotifyHeadBytes),
			Priority: notify.Action,
			URL:      prURL(s),
		})
	}
	// agent — UNTRUSTED sink #2, the ONLY sanitized + idle-gated one.
	if p.SendToAgent {
		d.sendHandoffToAgent(ctx, s, p, handoffStash(s, p, findings))
	}
	// linear — UNTRUSTED sink #3 (API payload shown to a human, no sanitize).
	if p.Transports.Has(config.TransportLinear) {
		d.commentOnLinear(ctx, s, p, findings)
	}
	// github — UNTRUSTED sink #4 (a PR comment; pass shapes only).
	if p.Shape == shapePass && p.Transports.Has(config.TransportGitHub) {
		d.postGithubSink(ctx, s, p, findings)
	}
	d.logf("", "review: %s (%s) routed (notify=%v agent=%v linear=%v github=%v)", s.ID, p.Kind,
		p.Notify, p.SendToAgent, p.Transports.Has(config.TransportLinear), p.Shape == shapePass && p.Transports.Has(config.TransportGitHub))
}

// handoffStash is the text stashed for the worker hand-off: the RAW findings for
// a full hand-off (the send re-applies the preamble + sanitize), or the
// single-line PR pointer for a watch (never the raw comment text).
func handoffStash(s session.Session, p reviewProvider, findings string) string {
	if p.Handoff == handoffPointer {
		return coderabbitAgentPointer(s)
	}
	return findings
}

// sendHandoffToAgent is the generalized send-keys hand-off (PLAN §5.1), keyed on
// PendingHandoffs[p.Kind]. It enforces the SAME send-keys safety gate as the
// reaction engine: a mid-turn worker defers (stash, never type); an idle worker
// has AtPrompt CONSUMED atomically (clearing the pending entry) before the send,
// so a hook that resumed the agent meanwhile cancels the send (re-stashed).
//
// stash is the RAW hand-off text (findings for handoffFull, pointer for
// handoffPointer); the sanitized/prefixed message is rendered here, immediately
// before the send — so the stash and every other sink hold the readable text
// while the pane only ever receives sanitized bytes.
func (d *Daemon) sendHandoffToAgent(ctx context.Context, s session.Session, p reviewProvider, stash string) {
	if s.TmuxName == "" {
		return
	}
	if !s.AtPrompt {
		d.deferHandoff(s.ID, p.Kind, stash)
		d.logf("", "review: %s (%s) worker is mid-turn — deferring the hand-off", s.ID, p.Kind)
		return
	}

	msg := stash
	if p.Handoff == handoffFull {
		msg = labelsFor(p.Kind).agentPreamble + stash
	}
	msg = sanitizeAgentText(msg)

	var (
		sent     bool
		tmuxName string
	)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if !cur.AtPrompt {
			return false // a hook resumed the agent between the read above and here
		}
		cur.AtPrompt = false
		if cur.PendingHandoffs != nil {
			delete(cur.PendingHandoffs, string(p.Kind)) // consumed
		}
		tmuxName = cur.TmuxName
		sent = true
		return true
	})
	if !sent {
		d.deferHandoff(s.ID, p.Kind, stash)
		d.logf("", "review: %s (%s) worker no longer idle at prompt — deferring the hand-off", s.ID, p.Kind)
		return
	}

	sctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if err := d.sendKeys(sctx, tmuxName, msg); err != nil {
		// Gate already consumed; do not roll back (that would re-fire and spam).
		d.logf("", "review: %s (%s) send-keys of hand-off failed: %v", s.ID, p.Kind, err)
		return
	}
	d.reviewSave()
	d.logf("", "review: %s (%s) handed feedback to the worker", s.ID, p.Kind)
}

// deferHandoff stashes the hand-off text on PendingHandoffs[kind] for a later
// idle-cycle delivery, idempotently (a repeat stash of the same text is a no-op),
// and persists.
func (d *Daemon) deferHandoff(id string, k provKind, stash string) {
	changed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		if cur.PendingHandoffs == nil {
			cur.PendingHandoffs = map[string]string{}
		}
		if cur.PendingHandoffs[string(k)] == stash {
			return false
		}
		cur.PendingHandoffs[string(k)] = stash
		changed = true
		return true
	})
	if changed {
		d.reviewSave()
	}
}

// flushReviewHandoffs delivers any hand-off deferred earlier (worker mid-turn)
// once the worker is idle at its prompt again. It attempts every pending kind;
// the first successful send CONSUMES AtPrompt, so the rest re-defer for the next
// idle cycle (matching the legacy one-at-a-time flush). Called every cycle.
func (d *Daemon) flushReviewHandoffs(ctx context.Context, id string) {
	s, ok := d.sessions.Get(id)
	if !ok || len(s.PendingHandoffs) == 0 || !s.AtPrompt {
		return
	}
	for kStr := range s.PendingHandoffs {
		p, ok := d.providerByKind(provKind(kStr))
		if !ok {
			continue // kind no longer configured; leave the stash for a future config
		}
		d.flushPendingHandoff(ctx, id, p)
	}
}

// flushPendingHandoff re-reads the record and delivers the deferred hand-off for
// p.Kind, no-op when nothing is pending or the worker is still busy (the send
// just re-stashes then).
func (d *Daemon) flushPendingHandoff(ctx context.Context, id string, p reviewProvider) {
	s, ok := d.sessions.Get(id)
	if !ok || !s.AtPrompt {
		return
	}
	stash := s.PendingHandoffs[string(p.Kind)]
	if stash == "" {
		return
	}
	d.sendHandoffToAgent(ctx, s, p, stash)
}

// commentOnLinear posts the findings as a Linear comment via the P4 write-back
// client, best-effort. The body is bounded and UNTRUSTED but display-only, so no
// control-byte sanitization is needed. Linear-unavailable / auth failures are
// logged (dropping the cached client on auth error) — never fatal, never blocking.
func (d *Daemon) commentOnLinear(ctx context.Context, s session.Session, p reviewProvider, findings string) {
	if s.IssueUUID == "" {
		return
	}
	api, err := d.ensureLinear()
	if err != nil {
		d.logf("", "review: linear unavailable for %s (%s) comment: %v", s.ID, p.Kind, err)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	body := labelsFor(p.Kind).notifyTitle + ":\n\n" + findings
	if err := api.CreateComment(cctx, s.IssueUUID, body); err != nil {
		d.wbLinErr(fmt.Sprintf("%s comment for %s", p.Kind, issueLabel(s)), err)
		return
	}
	d.logf("", "review: %s (%s) posted findings as a Linear comment", issueLabel(s), p.Kind)
}

// postGithubSink posts the findings to the session's PR as a plain `gh pr comment`
// (PLAN §5.2, Locked decision 1). It is idempotent per PR per kind
// (PostedGitHubPRs[kind]) and fail-closed:
//
//   - missing repo / no PR / nil seam ⇒ silent skip.
//   - already SETTLED for this PR (success or permanent-fail) ⇒ no-op (no spam).
//   - success ⇒ stamp the settle guard, log once.
//   - PERMANENT gh error (422/403/no write permission) ⇒ stamp the guard (stop
//     retrying), log once.
//   - TRANSIENT error (5xx/timeout) ⇒ leave unstamped, log once, retry next cycle.
//
// The body is the full UNTRUSTED findings — NOT sanitized (a PR comment is a
// human sink, never re-fed to the agent as control) — bounded inside PostPRComment.
// It IS run through neutralizeBotTriggers first so a `@coderabbitai` mention that
// happens to appear in the findings can never kick off a fresh CodeRabbit review
// on the PR (the "check the PR but never trigger a new CodeRabbit there" guarantee
// that makes a watch-only posture safe even alongside a github-posting provider).
func (d *Daemon) postGithubSink(ctx context.Context, s session.Session, p reviewProvider, findings string) {
	if s.Repo == "" || s.PR == nil || s.PR.Number <= 0 {
		return // fail-closed: nowhere to post
	}
	if s.PostedGitHubPRs[string(p.Kind)] == s.PR.Number {
		return // already settled this PR for this kind
	}
	post := d.postSeam()
	if post == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	err := post(cctx, s.Repo, s.PR.Number, neutralizeBotTriggers(findings))
	if err == nil {
		d.stampGithubSettled(s.ID, p.Kind, s.PR.Number)
		d.logf("", "review: %s (%s) posted findings to PR #%d as a github comment", s.ID, p.Kind, s.PR.Number)
		return
	}
	if isPermanentGhError(err) {
		// Settle the guard so a permanent rejection (e.g. no write permission on a
		// fork) is never retried and never spams the log per cycle.
		d.stampGithubSettled(s.ID, p.Kind, s.PR.Number)
		d.logf("", "review: %s (%s) github post to PR #%d permanently rejected, giving up: %v", s.ID, p.Kind, s.PR.Number, err)
		return
	}
	d.logf("", "review: %s (%s) github post to PR #%d failed (transient, will retry): %v", s.ID, p.Kind, s.PR.Number, err)
}

// botTriggerRe matches an @-mention of the CodeRabbit GitHub app (@coderabbit or
// @coderabbitai, case-insensitive). lola posts its own review findings as a plain
// PR comment via the `github` transport; a live @coderabbitai mention in that body
// would be parsed by the CodeRabbit app as a command and start a BRAND-NEW
// CodeRabbit review on the PR. That defeats a watch-only posture (read CodeRabbit's
// own automatic review, never invoke a new one) and can silently burn a review
// credit, so lola defuses the mention before posting.
var botTriggerRe = regexp.MustCompile(`(?i)@(coderabbit)`)

// neutralizeBotTriggers rewrites any @coderabbit / @coderabbitai mention in a body
// lola is about to post to a PR so it can never trigger a new CodeRabbit review. A
// zero-width space is inserted after the `@`, which breaks both GitHub's @-mention
// detection AND any literal `@coderabbitai` command scan while leaving the text
// visually unchanged. This is the enforcing mechanism behind "check the PR but
// never trigger a new CodeRabbit there"; it applies only to the github sink (the
// worker/notify/linear sinks never reach the CodeRabbit app).
func neutralizeBotTriggers(body string) string {
	return botTriggerRe.ReplaceAllString(body, "@\u200b$1")
}

// isPermanentGhError classifies a gh error as permanent (the post can never
// succeed for this PR: 422 unprocessable, 403 forbidden / no write permission) vs
// transient (5xx, timeout, network — retry next cycle). The error is the
// secret-scrubbed ghError, so matching on its text is safe.
func isPermanentGhError(err error) bool {
	if err == nil {
		return false
	}
	l := strings.ToLower(err.Error())
	for _, cue := range []string{
		"http 422", "http 403", "422", "403",
		"forbidden", "unprocessable", "not have permission", "must have write access", "resource not accessible",
	} {
		if strings.Contains(l, cue) {
			return true
		}
	}
	return false
}

// coderabbitAgentPointer builds the single-line instruction handed to the worker
// for a watch hand-off. It is derived only from the PR number (our own text — no
// untrusted content), so it submits cleanly and carries nothing attacker-authored
// into the pane.
func coderabbitAgentPointer(s session.Session) string {
	n := 0
	if s.PR != nil {
		n = s.PR.Number
	}
	return fmt.Sprintf(config.CodeRabbitAgentPointerFmt, n, n)
}

// --- per-kind labels ---------------------------------------------------------

// provLabels carries the per-kind human strings so a claude-session's findings
// read "Claude review" and a coderabbit's read "CodeRabbit" (never mislabelled).
type provLabels struct {
	notifyTitle   string
	agentPreamble string // full hand-off only
}

func labelsFor(k provKind) provLabels {
	switch k {
	case kindClaudeSession:
		return provLabels{config.ClaudeReviewNotifyTitle, config.ClaudeReviewToAgentPreamble}
	case kindCoderabbitWatch:
		return provLabels{config.CodeRabbitNotifyTitle, ""} // pointer hand-off, no preamble
	default: // coderabbit-cli
		return provLabels{config.ReviewNotifyTitle, config.ReviewToAgentPreamble}
	}
}

// --- kind-keyed guard mutations ----------------------------------------------

// stampReviewed sets the once-per-PR pass guard ReviewedPRs[kind] before the exec.
func (d *Daemon) stampReviewed(id string, k provKind, pr int) {
	d.sessions.Update(id, func(cur *session.Session) bool {
		if cur.ReviewedPRs == nil {
			cur.ReviewedPRs = map[string]int{}
		}
		if cur.ReviewedPRs[string(k)] == pr {
			return false
		}
		cur.ReviewedPRs[string(k)] = pr
		return true
	})
	d.reviewSave()
}

// advanceWatermark moves the watch guard ReviewWatermarks[kind] forward.
func (d *Daemon) advanceWatermark(id string, k provKind, latest time.Time) {
	d.sessions.Update(id, func(cur *session.Session) bool {
		if cur.ReviewWatermarks == nil {
			cur.ReviewWatermarks = map[string]time.Time{}
		}
		if !latest.After(cur.ReviewWatermarks[string(k)]) {
			return false
		}
		cur.ReviewWatermarks[string(k)] = latest
		return true
	})
	d.coderabbitSave()
}

// stampGithubSettled marks the github sink SETTLED (success or permanent-fail)
// for this PR/kind so it never re-posts or re-fails on the cadence.
func (d *Daemon) stampGithubSettled(id string, k provKind, pr int) {
	d.sessions.Update(id, func(cur *session.Session) bool {
		if cur.PostedGitHubPRs == nil {
			cur.PostedGitHubPRs = map[string]int{}
		}
		if cur.PostedGitHubPRs[string(k)] == pr {
			return false
		}
		cur.PostedGitHubPRs[string(k)] = pr
		return true
	})
	d.reviewSave()
}
