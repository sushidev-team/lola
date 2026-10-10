package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/board"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/secrets"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// The NATIVE LINEAR AGENT ([linear_agent]). lola is installed in the workspace
// as an OAuth app actor, so a human can delegate or @mention it; Linear opens an
// Agent Session for each, and this loop:
//
//  1. DISPATCHES a new (pending) session on an issue: it binds to a live lola
//     session already on that issue, or spawns one through handleOpenTicket —
//     the same claim → seen → spawn → write-back ordering a manual open takes,
//     so it can never double-spawn against a poll tick. The issue's TEAM (and
//     Linear project, when a [[project]] filters by one) picks the project.
//  2. MIRRORS each bound lola session into its Agent Session, one activity per
//     CHANGE (AgentMirror is the persisted watermark): the agent's `lola
//     report` todos become the Linear plan checklist, phase / blocker / note
//     become thoughts, the PR becomes the session's external link, CI and
//     review state become actions, a needs-you stop becomes an elicitation, a
//     submitted plan becomes an approve / request-changes elicitation, and a
//     merge or close ends the session with a response.
//  3. RELAYS the humans' replies (prompt activities) back: a reply to a pending
//     plan is the verdict (decidePlan); anything else is queued as a notice and
//     typed into the agent at its next resting prompt (flushAgentNotices).
//
// The ledger (state/linear-agent.json) is the crash guard, like seen: an Agent
// Session is recorded BEFORE its spawn, so a crash mid-spawn never spawns it
// twice. Linear's own status does the rest — once lola has emitted an activity
// a session is no longer `pending`.
//
// TRUST: everything read from Linear is written by people in the workspace.
// The loop routes on ids; prompt text reaches the agent only through the
// sanitize + idle gate, exactly as a `lola answer` does, and the board it
// mirrors OUT is display data going to a human sink.

const (
	// agentExecTimeout bounds every Linear call the loop makes.
	agentExecTimeout = 20 * time.Second
	// agentLedgerTTL drops ledger entries nobody has touched for this long.
	agentLedgerTTL = 30 * 24 * time.Hour
	// agentMaxBody clips text lola puts into an activity.
	agentMaxBody = 8 << 10
	// agentTokenEnv is the env-var fallback for the agent token (a raw access
	// token or the JSON the keychain item holds) — for hosts without a keychain.
	agentTokenEnv = "LOLA_LINEAR_AGENT_TOKEN"
)

// approveWords are the replies that approve a pending plan. Everything else a
// human writes while a plan is pending is a request for changes.
var approveWords = map[string]bool{
	"approve": true, "approved": true, "approve plan": true, "lgtm": true,
	"yes": true, "ok": true, "go": true, "ship it": true,
}

const (
	planOptionApprove = "Approve"
	planOptionChanges = "Request changes"
)

// agentLedgerEntry is one Agent Session lola has acted on.
type agentLedgerEntry struct {
	State   string    `json:"state"` // queued | spawning | bound | rejected
	Session string    `json:"session,omitempty"`
	At      time.Time `json:"at"`
}

// linearAgentRuntime is the loop's state, all behind mu.
type linearAgentRuntime struct {
	mu        sync.Mutex
	refreshMu sync.Mutex // serializes token refreshes (rotation makes them exclusive)
	api       linear.AgentAPI
	token     linear.OAuthToken
	viewerID  string
	status    protocol.LinearAgentStatus
	urls      map[string]string // agent session id → Linear URL (for the wire)
	ledger    map[string]agentLedgerEntry
	loaded    bool
	wake      chan struct{}
	webhookOn string

	// Seams (tests).
	apiOverride linear.AgentAPI
	loadToken   func(cfg config.LinearAgentConfig) (linear.OAuthToken, error)
	storeToken  func(cfg config.LinearAgentConfig, t linear.OAuthToken) error
	refresh     func(ctx context.Context, cfg config.LinearAgentConfig, refreshToken string) (linear.OAuthToken, error)
	newAPI      func(endpoint, token string) linear.AgentAPI
	now         func() time.Time
}

func newLinearAgentRuntime() *linearAgentRuntime {
	return &linearAgentRuntime{
		urls:       map[string]string{},
		ledger:     map[string]agentLedgerEntry{},
		wake:       make(chan struct{}, 1),
		loadToken:  loadAgentToken,
		storeToken: storeAgentToken,
		refresh:    refreshAgentToken,
		newAPI:     func(endpoint, token string) linear.AgentAPI { return linear.NewBearer(endpoint, token) },
		now:        time.Now,
	}
}

// loadAgentToken resolves the stored OAuth token (keychain, then env). The
// value is either the JSON `lola linear-agent login` writes or a bare access
// token; neither ever reaches a log line.
func loadAgentToken(cfg config.LinearAgentConfig) (linear.OAuthToken, error) {
	raw, err := secrets.Resolve("linear agent token", cfg.TokenKeychain, agentTokenEnv)
	if err != nil {
		return linear.OAuthToken{}, fmt.Errorf("%w — run `lola linear-agent login`", err)
	}
	return ParseAgentToken(raw)
}

// ParseAgentToken decodes a stored agent token (JSON or a bare access token).
func ParseAgentToken(raw string) (linear.OAuthToken, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		var t linear.OAuthToken
		if err := json.Unmarshal([]byte(raw), &t); err != nil || t.AccessToken == "" {
			return linear.OAuthToken{}, errors.New("linear agent token: stored value is not a valid token — run `lola linear-agent login`")
		}
		return t, nil
	}
	if raw == "" {
		return linear.OAuthToken{}, errors.New("linear agent token is empty — run `lola linear-agent login`")
	}
	return linear.OAuthToken{AccessToken: raw}, nil
}

func storeAgentToken(cfg config.LinearAgentConfig, t linear.OAuthToken) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return secrets.Store(cfg.TokenKeychain, string(b))
}

func refreshAgentToken(ctx context.Context, cfg config.LinearAgentConfig, refreshToken string) (linear.OAuthToken, error) {
	secret, err := secrets.Resolve("linear agent client secret", cfg.ClientSecretKeychain, cfg.ClientSecretEnv)
	if err != nil {
		return linear.OAuthToken{}, err
	}
	return linear.OAuthClient{ClientID: cfg.ClientID, ClientSecret: secret}.Refresh(ctx, refreshToken)
}

// wakeLinearAgent rings the loop so it runs a cycle now (a plan was submitted,
// a webhook arrived). Never blocks.
func (d *Daemon) wakeLinearAgent() {
	if d.agent == nil {
		return
	}
	select {
	case d.agent.wake <- struct{}{}:
	default:
	}
}

// agentSessionURL is the Linear URL of a bound Agent Session, "" when unknown.
func (d *Daemon) agentSessionURL(id string) string {
	if id == "" || d.agent == nil {
		return ""
	}
	d.agent.mu.Lock()
	defer d.agent.mu.Unlock()
	return d.agent.urls[id]
}

// linearAgentStatus is the cmd=status block, nil while the feature is off.
func (d *Daemon) linearAgentStatus() *protocol.LinearAgentStatus {
	d.mu.Lock()
	on := d.cfg.LinearAgent.Enabled
	d.mu.Unlock()
	if !on || d.agent == nil {
		return nil
	}
	d.agent.mu.Lock()
	defer d.agent.mu.Unlock()
	st := d.agent.status
	st.Enabled = true
	st.Webhook = d.agent.webhookOn
	return &st
}

// linearAgentLoop runs the agent cycle every poll_interval (or sooner when
// rung). The loop stops on shutdown; each cycle runs shielded and bounded, like
// the observer.
func (d *Daemon) linearAgentLoop(ctx context.Context) {
	defer d.wg.Done()
	for {
		interval := d.safeLinearAgentCycle(ctx)
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-d.agent.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

func (d *Daemon) safeLinearAgentCycle(ctx context.Context) (next time.Duration) {
	next = config.DefaultLinearAgentPollInterval
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "linear agent: cycle panic (daemon keeps running): %v", r)
		}
	}()
	return d.linearAgentCycle(context.WithoutCancel(ctx))
}

// linearAgentCycle is one pass: dispatch new sessions, mirror and relay for
// bound ones. It returns the delay until the next pass.
func (d *Daemon) linearAgentCycle(ctx context.Context) time.Duration {
	d.mu.Lock()
	cfg := d.cfg.LinearAgent
	endpoint := d.cfg.Linear.Endpoint
	d.mu.Unlock()
	if !cfg.Enabled {
		return config.DefaultLinearAgentPollInterval
	}
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = config.DefaultLinearAgentPollInterval
	}
	ra := d.agent
	ra.loadLedger(d.home, d.logf)

	api, err := d.ensureAgentAPI(ctx, cfg, endpoint)
	if err != nil {
		ra.setError(err)
		return interval
	}
	cctx, cancel := context.WithTimeout(ctx, agentExecTimeout)
	sessions, err := api.AgentSessions(cctx)
	cancel()
	if err != nil && isAuthErr(err) {
		// An expired/revoked access token: refresh once and retry.
		if api, err = d.refreshAgentAPI(ctx, cfg, endpoint, api); err == nil {
			cctx, cancel = context.WithTimeout(ctx, agentExecTimeout)
			sessions, err = api.AgentSessions(cctx)
			cancel()
		}
	}
	if err != nil {
		ra.setError(err)
		return interval
	}
	ra.mu.Lock()
	for _, as := range sessions {
		if as.URL != "" {
			ra.urls[as.ID] = as.URL
		}
	}
	ra.status.Connected = true
	ra.status.LastError = ""
	ra.status.LastPoll = ra.now()
	ra.mu.Unlock()

	for _, as := range sessions {
		d.considerAgentSession(ctx, api, as)
	}
	for _, s := range d.sessions.Snapshot() {
		if s.AgentSessionID == "" || s.Source != "native" || s.AgentMirror.Done {
			continue // a finished thread is not polled for replies any more
		}
		d.relayAgentPrompts(ctx, api, s)
		if cur, ok := d.sessions.Get(s.ID); ok {
			d.mirrorAgentSession(ctx, api, cur)
		}
	}
	ra.pruneLedger()
	ra.saveLedger(d.home, d.logf)
	return interval
}

// ensureAgentAPI returns the agent client, building it from the stored token
// (refreshing a token past its expiry first).
func (d *Daemon) ensureAgentAPI(ctx context.Context, cfg config.LinearAgentConfig, endpoint string) (linear.AgentAPI, error) {
	ra := d.agent
	ra.mu.Lock()
	if ra.apiOverride != nil {
		api := ra.apiOverride
		need := ra.viewerID == ""
		ra.mu.Unlock()
		if need {
			d.resolveAgentViewer(ctx, api)
		}
		return api, nil
	}
	if api := ra.liveAPILocked(); api != nil {
		ra.mu.Unlock()
		return api, nil
	}
	ra.mu.Unlock()

	// One refresher at a time: Linear ROTATES the refresh token, so two
	// concurrent refreshes with the same one (the loop and an async post) make
	// one fail — and a reused refresh token may revoke the whole family.
	ra.refreshMu.Lock()
	defer ra.refreshMu.Unlock()
	ra.mu.Lock()
	if api := ra.liveAPILocked(); api != nil { // someone refreshed while we waited
		ra.mu.Unlock()
		return api, nil
	}
	tok := ra.token
	ra.mu.Unlock()
	// The IN-MEMORY token is the newest one: after a rotation whose keychain
	// write failed (no keychain, a write error), storage still holds the refresh
	// token Linear already invalidated. Storage is read only when memory is empty.
	if tok.AccessToken == "" {
		loaded, err := ra.loadToken(cfg)
		if err != nil {
			return nil, err
		}
		tok = loaded
	}
	if tok.Expired(ra.now(), time.Minute) {
		return d.refreshWith(ctx, cfg, endpoint, tok)
	}
	return d.installAgentToken(ctx, endpoint, tok), nil
}

// liveAPILocked is the cached client while its token is still valid. ra.mu held.
func (ra *linearAgentRuntime) liveAPILocked() linear.AgentAPI {
	if ra.api != nil && !ra.token.Expired(ra.now(), time.Minute) {
		return ra.api
	}
	return nil
}

// refreshAgentAPI forces a token refresh after stale was rejected (a 401).
// When another caller already replaced stale meanwhile, that client is used
// instead of refreshing again.
func (d *Daemon) refreshAgentAPI(ctx context.Context, cfg config.LinearAgentConfig, endpoint string, stale linear.AgentAPI) (linear.AgentAPI, error) {
	ra := d.agent
	ra.mu.Lock()
	if ra.apiOverride != nil {
		ra.mu.Unlock()
		return nil, errors.New("linear agent: token rejected")
	}
	ra.mu.Unlock()
	ra.refreshMu.Lock()
	defer ra.refreshMu.Unlock()
	ra.mu.Lock()
	if ra.api != nil && ra.api != stale {
		api := ra.api
		ra.mu.Unlock()
		return api, nil
	}
	tok := ra.token
	ra.mu.Unlock()
	if tok.RefreshToken == "" {
		loaded, err := ra.loadToken(cfg)
		if err != nil {
			return nil, err
		}
		tok = loaded
	}
	return d.refreshWith(ctx, cfg, endpoint, tok)
}

func (d *Daemon) refreshWith(ctx context.Context, cfg config.LinearAgentConfig, endpoint string, tok linear.OAuthToken) (linear.AgentAPI, error) {
	ra := d.agent
	cctx, cancel := context.WithTimeout(ctx, agentExecTimeout)
	fresh, err := ra.refresh(cctx, cfg, tok.RefreshToken)
	cancel()
	if err != nil {
		ra.mu.Lock()
		ra.api = nil
		ra.mu.Unlock()
		return nil, fmt.Errorf("linear agent: token refresh failed: %w", err)
	}
	if err := ra.storeToken(cfg, fresh); err != nil {
		// The fresh token stays in memory and keeps refreshing for this run
		// (ensureAgentAPI prefers it); only the next START needs a login, once
		// the stored refresh token has been rotated away.
		d.logf("", "linear agent: could not persist the refreshed token: %v", err)
	}
	return d.installAgentToken(ctx, endpoint, fresh), nil
}

func (d *Daemon) installAgentToken(ctx context.Context, endpoint string, tok linear.OAuthToken) linear.AgentAPI {
	ra := d.agent
	api := ra.newAPI(endpoint, tok.AccessToken)
	ra.mu.Lock()
	ra.api = api
	ra.token = tok
	ra.viewerID = ""
	ra.mu.Unlock()
	d.resolveAgentViewer(ctx, api)
	return api
}

func (d *Daemon) resolveAgentViewer(ctx context.Context, api linear.AgentAPI) {
	cctx, cancel := context.WithTimeout(ctx, agentExecTimeout)
	u, err := api.AgentViewer(cctx)
	cancel()
	if err != nil {
		return // retried next cycle; the id only filters lola's own activities
	}
	ra := d.agent
	ra.mu.Lock()
	ra.viewerID = u.ID
	ra.status.AgentName = u.Name
	ra.mu.Unlock()
}

func (ra *linearAgentRuntime) setError(err error) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	ra.status.Connected = false
	ra.status.LastError = err.Error()
	ra.status.LastPoll = ra.now()
}

// considerAgentSession dispatches one Agent Session if it is new (pending and
// unseen) or was queued earlier.
func (d *Daemon) considerAgentSession(ctx context.Context, api linear.AgentAPI, as linear.AgentSession) {
	ra := d.agent
	ra.mu.Lock()
	entry, known := ra.ledger[as.ID]
	ra.mu.Unlock()

	switch {
	case known && entry.State == "queued":
		if as.Status == "complete" || as.Status == "stale" || as.Status == "error" {
			ra.forget(as.ID) // a human dismissed it while it waited
			return
		}
	case known:
		return
	case as.Status != "pending":
		return
	}
	if as.Issue == nil || as.Issue.Identifier == "" {
		if !known {
			ra.record(as.ID, "rejected", "")
			d.postAgentActivity(ctx, api, as.ID, linear.ErrorActivity("lola works on issues — delegate or mention it on an issue to start a session."))
		}
		return
	}
	is := *as.Issue

	// Already running here (a poll or a human started it): bind, don't spawn.
	if s, ok := d.liveSessionForIssue(is.Identifier); ok {
		d.bindAgentSession(ctx, api, as.ID, s, fmt.Sprintf("Already working on %s in lola session `%s` — I'll keep this thread updated.", is.Identifier, s.ID))
		return
	}

	p, ok := d.projectForAgentIssue(is)
	if !ok {
		ra.record(as.ID, "rejected", "")
		d.postAgentActivity(ctx, api, as.ID, linear.ErrorActivity(
			fmt.Sprintf("No lola project is configured for this issue's team. Add a `[[project]]` with `team_id = %q` to lola's config, then delegate again.", is.TeamID)))
		d.logf("", "linear agent: no project for %s (team %s) — rejected session %s", is.Identifier, is.TeamID, as.ID)
		return
	}

	// Concurrency cap: queue (and say so once) rather than spawn past it. The
	// check runs again inside openTicket's critical section (admit below); this
	// early one only avoids a claim when the cap is plainly full.
	d.mu.Lock()
	pollCap := d.cfg.EffectiveCap(&p)
	globalCap := d.cfg.Defaults.GlobalCap
	agentBin := agent.Parse(d.cfg.AgentForProject(p.Name)).Binary()
	health := d.runtimeHealth
	d.mu.Unlock()
	hasSlot := func() error {
		if Budget(pollCap, globalCap, NativeLiveCounted(d.sessions.Snapshot())) <= 0 {
			return errCapped
		}
		return nil
	}
	queue := func() {
		if !known {
			ra.record(as.ID, "queued", "")
			d.postAgentActivity(ctx, api, as.ID, linear.Thought("Queued — lola is at its concurrency cap. I'll start as soon as a slot frees up.", false))
			d.logf("", "linear agent: %s queued (capped)", is.Identifier)
		} else {
			ra.record(as.ID, "queued", "")
		}
	}
	if hasSlot() != nil {
		queue()
		return
	}
	if health != nil {
		if err := health(agentBin); err != nil {
			if !known {
				ra.record(as.ID, "queued", "")
				d.postAgentActivity(ctx, api, as.ID, linear.Thought("Queued — lola's runtime is not ready on its machine right now. I'll start once it is.", false))
				d.logf("", "linear agent: %s queued (runtime: %v)", is.Identifier, err)
			}
			return
		}
	}

	// Crash guard: the ledger says "spawning" BEFORE the spawn is attempted.
	ra.record(as.ID, "spawning", "")
	ra.saveLedger(d.home, d.logf)
	od, err := d.openTicket(ctx, protocol.OpenTicketArgs{
		Project: p.Name, Identifier: is.Identifier, UUID: is.ID, Title: is.Title, Branch: is.BranchName,
	}, hasSlot)
	if errors.Is(err, errCapped) {
		queue() // lost the race for the last slot: back to the queue, not rejected
		ra.saveLedger(d.home, d.logf)
		return
	}
	if err != nil {
		// A concurrent claim (a tick spawning the same issue right now) binds on
		// the next cycle once the session exists; any other failure is final for
		// this Agent Session — say why, a human can delegate again.
		if s, ok := d.liveSessionForIssue(is.Identifier); ok {
			d.bindAgentSession(ctx, api, as.ID, s, fmt.Sprintf("Working on %s in lola session `%s`.", is.Identifier, s.ID))
			return
		}
		ra.record(as.ID, "rejected", "")
		d.postAgentActivity(ctx, api, as.ID, linear.ErrorActivity("lola could not start a session: "+clipBody(err.Error(), 500)))
		d.logf("", "linear agent: spawn for %s failed: %v", is.Identifier, err)
		return
	}
	s, ok := d.sessions.Get(od.SessionID)
	if !ok {
		return
	}
	msg := fmt.Sprintf("Started lola session `%s` on branch `%s`.", s.ID, s.Branch)
	if s.PlanGate == session.PlanPlanning {
		msg += " This project requires plan approval: I'll post a plan here for you to approve before I write code."
	}
	d.bindAgentSession(ctx, api, as.ID, s, msg)
	d.logf("", "linear agent: %s dispatched by Agent Session %s → %s", is.Identifier, as.ID, s.ID)
}

// bindAgentSession ties a lola session to an Agent Session and acknowledges.
// A newer delegation on the same issue takes over the binding (and its
// mirror watermark restarts, so the new thread gets the current picture).
func (d *Daemon) bindAgentSession(ctx context.Context, api linear.AgentAPI, agentSessionID string, s session.Session, ack string) {
	d.agent.record(agentSessionID, "bound", s.ID)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.AgentSessionID != agentSessionID {
			cur.AgentSessionID = agentSessionID
			cur.AgentMirror = session.AgentMirror{}
		}
		cur.AgentMirror.Started = true
		return true
	})
	if err := d.sessions.Save(); err != nil {
		d.logf("", "linear agent: persist sessions: %v", err)
	}
	d.postAgentActivity(ctx, api, agentSessionID, linear.Thought(ack, false))
}

// liveSessionForIssue returns the live native session on an issue, if any.
func (d *Daemon) liveSessionForIssue(identifier string) (session.Session, bool) {
	for _, s := range d.sessions.Snapshot() {
		if s.Source == "native" && s.Issue == identifier && !s.IsAgentless() && nativeSessionPresent(s) {
			return s, true
		}
	}
	return session.Session{}, false
}

// projectForAgentIssue routes an issue to the first [[project]] (config
// order) on the issue's team whose Linear-project filter, if any, matches.
// A project with a Linear-project filter is preferred over a team-wide one.
func (d *Daemon) projectForAgentIssue(is linear.Issue) (config.Project, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var teamWide *config.Project
	for i := range d.cfg.Projects {
		p := &d.cfg.Projects[i]
		if is.TeamID == "" || p.TeamID != is.TeamID || p.Path == "" {
			continue
		}
		if p.ProjectID != "" {
			if p.ProjectID == is.ProjectID {
				return *p, true
			}
			continue
		}
		if teamWide == nil {
			teamWide = p
		}
	}
	if teamWide != nil {
		return *teamWide, true
	}
	return config.Project{}, false
}

// relayAgentPrompts handles the human prompts that arrived since the cursor.
func (d *Daemon) relayAgentPrompts(ctx context.Context, api linear.AgentAPI, s session.Session) {
	cctx, cancel := context.WithTimeout(ctx, agentExecTimeout)
	prompts, err := api.AgentPrompts(cctx, s.AgentSessionID, s.AgentMirror.PromptCursor)
	cancel()
	if err != nil {
		d.logf("", "linear agent: prompts for %s: %v", s.ID, err)
		return
	}
	d.agent.mu.Lock()
	self := d.agent.viewerID
	d.agent.mu.Unlock()
	for _, p := range prompts {
		if p.CreatedAt <= s.AgentMirror.PromptCursor {
			continue
		}
		if self == "" || p.UserID != self {
			d.handleAgentPrompt(ctx, api, s.ID, p)
		}
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if p.CreatedAt <= cur.AgentMirror.PromptCursor {
				return false
			}
			cur.AgentMirror.PromptCursor = p.CreatedAt
			return true
		})
		s.AgentMirror.PromptCursor = p.CreatedAt
	}
	if len(prompts) > 0 {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "linear agent: persist sessions: %v", err)
		}
	}
}

// handleAgentPrompt acts on one human message from the Agent Session.
func (d *Daemon) handleAgentPrompt(ctx context.Context, api linear.AgentAPI, id string, p linear.AgentPrompt) {
	cur, ok := d.sessions.Get(id)
	if !ok {
		return
	}
	body := strings.TrimSpace(p.Body)
	if p.Signal == "stop" {
		// lola does not tear sessions down from Linear: a stop is a request to
		// halt, and the safe halt for an autonomous worker is a human at lola.
		d.postAgentActivity(ctx, api, cur.AgentSessionID, linear.Response(
			fmt.Sprintf("Stop received. lola does not kill sessions from Linear — the session `%s` keeps its worktree; stop it in lola (`lola kill %s`) if it should end.", id, id)))
		if body == "" {
			return
		}
	}
	if body == "" {
		return
	}
	if planVerdictFor(cur, p) {
		approve := approveWords[strings.ToLower(strings.Trim(body, " .!"))]
		comment := body
		if strings.EqualFold(body, planOptionChanges) {
			// The bare option says nothing actionable; ask for the substance.
			d.postAgentActivity(ctx, api, cur.AgentSessionID, linear.Elicitation("What should change in the plan? Reply with your feedback and I'll pass it on."))
			return
		}
		if err := d.decidePlan(ctx, id, approve, comment, "linear"); err != nil {
			d.postAgentActivity(ctx, api, cur.AgentSessionID, linear.ErrorActivity(clipBody(err.Error(), 500)))
			return
		}
		if approve {
			d.postAgentActivity(ctx, api, cur.AgentSessionID, linear.Thought("Plan approved — implementing.", false))
		} else {
			d.postAgentActivity(ctx, api, cur.AgentSessionID, linear.Thought("Feedback passed to the agent — it will revise the plan.", false))
		}
		return
	}
	// Any other message goes to the agent as typed text, at its next prompt.
	msg := "A human replied in the Linear agent session for this issue:\n\n" + clipBody(body, agentMaxBody)
	d.sessions.Update(id, func(c *session.Session) bool {
		c.PendingNotices = appendNotice(c.PendingNotices, msg)
		return true
	})
	if err := d.sessions.Save(); err != nil {
		d.logf("", "linear agent: persist sessions: %v", err)
	}
	d.postAgentActivity(ctx, api, cur.AgentSessionID, linear.Thought("Passing that to the agent.", true))
	d.flushAgentNoticesAsync(id)
}

// planVerdictFor reports whether a prompt is a verdict on the CURRENT plan: a
// plan is waiting, this exact round was posted into the Agent Session, and the
// human wrote the prompt after it was posted. Anything else — a "yes" sent
// before the plan existed, a reply to an earlier round — is relayed to the
// agent as an ordinary message. An unparseable timestamp fails closed.
func planVerdictFor(s session.Session, p linear.AgentPrompt) bool {
	if s.PlanGate != session.PlanSubmitted || s.AgentMirror.PlanRound != s.PlanRound || s.AgentMirror.PlanPostedAt.IsZero() {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, p.CreatedAt)
	return err == nil && at.After(s.AgentMirror.PlanPostedAt)
}

// mirrorAgentSession streams what changed since the last pass.
func (d *Daemon) mirrorAgentSession(ctx context.Context, api linear.AgentAPI, s session.Session) {
	m := s.AgentMirror
	if m.Done {
		return
	}
	next := m
	asID := s.AgentSessionID
	s.EnsureAxes()

	// Plan checklist from the agent's own report.
	if h := todosHash(s.Board.Todos); h != m.PlanHash && len(s.Board.Todos) > 0 {
		if d.agentCall(ctx, func(c context.Context) error { return api.UpdateAgentPlan(c, asID, planSteps(s.Board.Todos)) }) {
			next.PlanHash = h
		}
	}
	// Phase, blocker, note as thoughts.
	if p := string(s.Board.Phase); p != "" && p != m.Phase {
		if d.postAgentActivity(ctx, api, asID, linear.Thought("Phase: "+p, true)) {
			next.Phase = p
		}
	}
	if b := s.Board.Blocked; b != m.Blocked {
		if b == "" || d.postAgentActivity(ctx, api, asID, linear.Elicitation("Blocked: "+clipBody(b, 1000))) {
			next.Blocked = b
		}
	}
	if n := s.Board.Note; n != "" && n != m.Note {
		if d.postAgentActivity(ctx, api, asID, linear.Thought(clipBody(n, 1000), true)) {
			next.Note = n
		}
	}
	// A plan waiting for approval.
	if s.PlanGate == session.PlanSubmitted && s.PlanRound > m.PlanRound {
		body := fmt.Sprintf("**Plan for approval (round %d)** — reply **%s** to let me start coding, or reply with what should change.\n\n%s",
			s.PlanRound, planOptionApprove, clipBody(s.Plan, agentMaxBody))
		if d.postAgentActivity(ctx, api, asID, linear.Elicitation(body, planOptionApprove, planOptionChanges)) {
			next.PlanRound = s.PlanRound
			next.PlanPostedAt = d.agent.now()
		}
	}
	// The PR as the session's external link.
	if s.PR != nil && s.PR.URL != "" && s.PR.URL != m.PRURL {
		if d.agentCall(ctx, func(c context.Context) error {
			return api.SetAgentExternalURLs(c, asID, []linear.ExternalURL{{Label: fmt.Sprintf("PR #%d", s.PR.Number), URL: s.PR.URL}})
		}) {
			d.postAgentActivity(ctx, api, asID, linear.Action("Opened pull request", fmt.Sprintf("#%d", s.PR.Number), s.PR.URL))
			next.PRURL = s.PR.URL
		}
	}
	// Delivery state as actions; merged / closed end the thread.
	if dl := string(s.Delivery); dl != m.Delivery && s.PR != nil {
		act, ok := deliveryActivity(s)
		if !ok || d.postAgentActivity(ctx, api, asID, act) {
			next.Delivery = dl
			if s.Delivery == state.DeliveryMerged || s.Delivery == state.DeliveryClosed {
				next.Done = true
			}
		}
	}
	// The agent stopped for a human.
	needs := s.AgentState == state.AgentWaitingInput &&
		(s.InputReason == state.InputQuestion || s.InputReason == state.InputPermission || s.InputReason == state.InputDialog)
	if needs != m.NeedsYou {
		if !needs {
			next.NeedsYou = false
		} else {
			body := "The agent is waiting for input."
			if n := strings.TrimSpace(s.LastNotification); n != "" {
				body += " It says: " + clipBody(n, 500)
			}
			body += " Reply here and I'll pass it on, or answer it in lola."
			if d.postAgentActivity(ctx, api, asID, linear.Elicitation(body)) {
				next.NeedsYou = true
			}
		}
	}
	// The agent is gone without a merge.
	if !next.Done && (s.AgentState == state.AgentDead || s.AgentState == state.AgentExited) && s.PR == nil {
		if d.postAgentActivity(ctx, api, asID, linear.ErrorActivity(fmt.Sprintf("The lola session `%s` ended before opening a PR. Revive it in lola, or delegate again.", s.ID))) {
			next.Done = true
		}
	}
	if next != m {
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if cur.AgentSessionID != asID {
				return false // rebound meanwhile; the new binding starts fresh
			}
			next.PromptCursor = cur.AgentMirror.PromptCursor // owned by relayAgentPrompts
			cur.AgentMirror = next
			return true
		})
		if err := d.sessions.Save(); err != nil {
			d.logf("", "linear agent: persist sessions: %v", err)
		}
	}
}

// deliveryActivity renders a delivery-state change, ok=false for states not
// worth a line (none / draft).
func deliveryActivity(s session.Session) (linear.AgentActivity, bool) {
	n := fmt.Sprintf("#%d", s.PR.Number)
	switch s.Delivery {
	case state.DeliveryCIPending:
		return linear.Action("CI running", n, ""), true
	case state.DeliveryCIFailed:
		return linear.Action("CI failed", n, "lola is relaying the failure to the agent."), true
	case state.DeliveryMergeConflict:
		return linear.Action("Merge conflict", n, ""), true
	case state.DeliveryChangesRequested:
		return linear.Action("Changes requested", n, "Relayed to the agent."), true
	case state.DeliveryReviewPending:
		return linear.Action("Ready for review", n, "CI is green."), true
	case state.DeliveryApproved:
		return linear.Action("Approved", n, "Ready to merge."), true
	case state.DeliveryMerged:
		return linear.Response(fmt.Sprintf("Merged %s — done.", n)), true
	case state.DeliveryClosed:
		return linear.Response(fmt.Sprintf("Pull request %s was closed without merging.", n)), true
	}
	return linear.AgentActivity{}, false
}

// planSteps maps the agent's todos onto Linear's plan vocabulary.
func planSteps(todos []board.Todo) []linear.PlanStep {
	out := make([]linear.PlanStep, 0, len(todos))
	for _, t := range todos {
		st := "pending"
		switch t.State {
		case board.TodoActive:
			st = "inProgress"
		case board.TodoDone:
			st = "completed"
		}
		out = append(out, linear.PlanStep{Content: t.Text, Status: st})
	}
	return out
}

func todosHash(todos []board.Todo) string {
	if len(todos) == 0 {
		return ""
	}
	h := sha256.New()
	for _, t := range todos {
		fmt.Fprintf(h, "%s\x00%s\x00", t.State, t.Text)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// clipBody bounds text lola places into an activity.
func clipBody(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// postAgentActivity emits one activity, bounded; it reports success and logs
// (never the content) on failure.
func (d *Daemon) postAgentActivity(ctx context.Context, api linear.AgentAPI, sessionID string, a linear.AgentActivity) bool {
	return d.agentCall(ctx, func(c context.Context) error { return api.CreateAgentActivity(c, sessionID, a) })
}

func (d *Daemon) agentCall(ctx context.Context, fn func(context.Context) error) bool {
	cctx, cancel := context.WithTimeout(ctx, agentExecTimeout)
	defer cancel()
	if err := fn(cctx); err != nil {
		d.logf("", "linear agent: %v", err)
		return false
	}
	return true
}

// postAgentActivityAsync emits an activity off the caller's goroutine (a
// socket handler), using the loop's current client.
func (d *Daemon) postAgentActivityAsync(agentSessionID string, a linear.AgentActivity) {
	if d.agent == nil || !d.beginConnWork() {
		return
	}
	go func() {
		defer d.connWg.Done()
		defer func() {
			if r := recover(); r != nil {
				d.logf("", "linear agent: async post panicked: %v", r)
			}
		}()
		d.mu.Lock()
		cfg := d.cfg.LinearAgent
		endpoint := d.cfg.Linear.Endpoint
		d.mu.Unlock()
		if !cfg.Enabled {
			return
		}
		api, err := d.ensureAgentAPI(context.Background(), cfg, endpoint)
		if err != nil {
			return
		}
		d.postAgentActivity(context.Background(), api, agentSessionID, a)
	}()
}

// --- ledger persistence ----------------------------------------------------

func agentLedgerPath(home string) string { return filepath.Join(home, "state", "linear-agent.json") }

func (ra *linearAgentRuntime) loadLedger(home string, logf func(string, string, ...any)) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	if ra.loaded {
		return
	}
	ra.loaded = true
	b, err := os.ReadFile(agentLedgerPath(home))
	if err != nil {
		return
	}
	var m map[string]agentLedgerEntry
	if err := json.Unmarshal(b, &m); err != nil {
		logf("", "linear agent: ledger unreadable, starting empty: %v", err)
		return
	}
	for k, v := range m {
		ra.ledger[k] = v
	}
}

func (ra *linearAgentRuntime) saveLedger(home string, logf func(string, string, ...any)) {
	ra.mu.Lock()
	b, err := json.MarshalIndent(ra.ledger, "", "  ")
	ra.mu.Unlock()
	if err != nil {
		return
	}
	path := agentLedgerPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		logf("", "linear agent: persist ledger: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		logf("", "linear agent: persist ledger: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		logf("", "linear agent: persist ledger: %v", err)
	}
}

func (ra *linearAgentRuntime) record(id, st, sess string) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	ra.ledger[id] = agentLedgerEntry{State: st, Session: sess, At: ra.now()}
}

func (ra *linearAgentRuntime) forget(id string) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	delete(ra.ledger, id)
}

func (ra *linearAgentRuntime) pruneLedger() {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	cut := ra.now().Add(-agentLedgerTTL)
	for k, v := range ra.ledger {
		if v.At.Before(cut) {
			delete(ra.ledger, k)
		}
	}
}
