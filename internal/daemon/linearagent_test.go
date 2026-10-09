package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/board"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// fakeAgentAPI is a hermetic linear.AgentAPI that records every write.
type fakeAgentAPI struct {
	mu         sync.Mutex
	sessions   []linear.AgentSession
	prompts    map[string][]linear.AgentPrompt
	activities map[string][]linear.AgentActivity
	plans      map[string][]linear.PlanStep
	urls       map[string][]linear.ExternalURL
}

func newFakeAgentAPI() *fakeAgentAPI {
	return &fakeAgentAPI{
		prompts:    map[string][]linear.AgentPrompt{},
		activities: map[string][]linear.AgentActivity{},
		plans:      map[string][]linear.PlanStep{},
		urls:       map[string][]linear.ExternalURL{},
	}
}

func (f *fakeAgentAPI) AgentViewer(context.Context) (linear.User, error) {
	return linear.User{ID: "agent-user", Name: "lola"}, nil
}

func (f *fakeAgentAPI) AgentSessions(context.Context) ([]linear.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]linear.AgentSession(nil), f.sessions...), nil
}

func (f *fakeAgentAPI) AgentPrompts(_ context.Context, id, after string) ([]linear.AgentPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []linear.AgentPrompt
	for _, p := range f.prompts[id] {
		if p.CreatedAt > after {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeAgentAPI) CreateAgentActivity(_ context.Context, id string, a linear.AgentActivity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activities[id] = append(f.activities[id], a)
	// Like Linear: the first activity takes a session out of pending.
	for i := range f.sessions {
		if f.sessions[i].ID == id && f.sessions[i].Status == "pending" {
			f.sessions[i].Status = "active"
		}
	}
	return nil
}

func (f *fakeAgentAPI) UpdateAgentPlan(_ context.Context, id string, plan []linear.PlanStep) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plans[id] = plan
	return nil
}

func (f *fakeAgentAPI) SetAgentExternalURLs(_ context.Context, id string, urls []linear.ExternalURL) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls[id] = urls
	return nil
}

func (f *fakeAgentAPI) kinds(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.activities[id] {
		out = append(out, fmt.Sprint(a.Content["type"]))
	}
	return out
}

func (f *fakeAgentAPI) bodies(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b strings.Builder
	for _, a := range f.activities[id] {
		fmt.Fprintf(&b, "%v|%v|%v\n", a.Content["type"], a.Content["body"], a.Content["action"])
	}
	return b.String()
}

func agentDaemon(t *testing.T, projects ...config.Project) (*Daemon, *fakeAgentAPI, *fakeNative) {
	t.Helper()
	cfg := testConfig(projects...)
	cfg.LinearAgent = config.LinearAgentConfig{Enabled: true, ClientID: "cid", PollInterval: config.DefaultLinearAgentPollInterval}
	nat := &fakeNative{}
	d := newTestDaemon(t, cfg, &linear.Fake{}, nat)
	api := newFakeAgentAPI()
	d.agent.apiOverride = api
	t.Cleanup(d.connWg.Wait) // async notice flushes and Linear posts finish first
	return d, api, nat
}

func pendingSession(id, ident, team string) linear.AgentSession {
	return linear.AgentSession{ID: id, Status: "pending", URL: "https://linear.app/x/" + id,
		Issue: &linear.Issue{ID: "uuid-" + ident, Identifier: ident, Title: "t " + ident, TeamID: team}}
}

// Delegating an issue to lola spawns a session with NO label setup: the
// project only needs its team.
func TestLinearAgentDelegationSpawnsWithoutLabels(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", Repo: "acme/app", DefaultBranch: "main", TeamID: "team-1"}
	d, api, nat := agentDaemon(t, p)
	api.sessions = []linear.AgentSession{pendingSession("as-1", "ENG-7", "team-1")}

	d.linearAgentCycle(context.Background())
	if calls := nat.spawnCalls(); len(calls) != 1 || calls[0].identifier != "ENG-7" || calls[0].project != "app" {
		t.Fatalf("spawns = %+v", calls)
	}
	s, ok := d.sessions.Get("lola-app-eng-7")
	if !ok || s.AgentSessionID != "as-1" {
		t.Fatalf("session not bound: %+v", s)
	}
	if got := api.kinds("as-1"); len(got) != 1 || got[0] != "thought" {
		t.Fatalf("want one acknowledgement thought, got %v", got)
	}
	if d.agentSessionURL("as-1") == "" {
		t.Fatal("the session URL must be cached for the wire")
	}

	// Next cycle: no second spawn (the session is no longer pending and the
	// ledger remembers it).
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("re-dispatched: %+v", nat.spawnCalls())
	}
}

func TestLinearAgentRejectsUnroutableTeam(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"}
	d, api, nat := agentDaemon(t, p)
	api.sessions = []linear.AgentSession{pendingSession("as-1", "OPS-1", "team-ops")}
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 0 {
		t.Fatal("must not spawn for an unconfigured team")
	}
	if got := api.kinds("as-1"); len(got) != 1 || got[0] != "error" {
		t.Fatalf("want one error activity, got %v", got)
	}
	// Not repeated on the next cycle.
	api.sessions[0].Status = "pending"
	d.linearAgentCycle(context.Background())
	if got := api.kinds("as-1"); len(got) != 1 {
		t.Fatalf("error repeated: %v", got)
	}
}

func TestLinearAgentPrefersProjectFilteredRoute(t *testing.T) {
	wide := config.Project{Name: "wide", Path: "/tmp/w", TeamID: "team-1"}
	narrow := config.Project{Name: "narrow", Path: "/tmp/n", TeamID: "team-1", ProjectID: "lp-9"}
	d, _, _ := agentDaemon(t, wide, narrow)
	if p, ok := d.projectForAgentIssue(linear.Issue{TeamID: "team-1", ProjectID: "lp-9"}); !ok || p.Name != "narrow" {
		t.Fatalf("got %q", p.Name)
	}
	if p, ok := d.projectForAgentIssue(linear.Issue{TeamID: "team-1", ProjectID: "other"}); !ok || p.Name != "wide" {
		t.Fatalf("got %q", p.Name)
	}
}

// At the concurrency cap the session is QUEUED (said once) and started when a
// slot frees.
func TestLinearAgentQueuesAtCap(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"}
	d, api, nat := agentDaemon(t, p)
	d.cfg.Defaults.GlobalCap = 1
	busy := session.Session{ID: "app-eng-1", Source: "native", Project: "app", Issue: "ENG-1",
		AgentState: state.AgentWorking, Delivery: state.DeliveryNone}
	d.sessions.Upsert(busy)
	api.sessions = []linear.AgentSession{pendingSession("as-2", "ENG-2", "team-1")}

	d.linearAgentCycle(context.Background())
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 0 {
		t.Fatal("must not spawn past the cap")
	}
	if got := api.kinds("as-2"); len(got) != 1 || got[0] != "thought" || !strings.Contains(api.bodies("as-2"), "Queued") {
		t.Fatalf("want one queued thought, got %s", api.bodies("as-2"))
	}

	d.sessions.Update("app-eng-1", func(s *session.Session) bool { s.AgentState = state.AgentDead; return true })
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("queued session must start once a slot frees: %+v", nat.spawnCalls())
	}
}

// A delegation for an issue already running binds instead of spawning.
func TestLinearAgentBindsExistingSession(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"}
	d, api, nat := agentDaemon(t, p)
	d.sessions.Upsert(session.Session{ID: "app-eng-3", Source: "native", Project: "app", Issue: "ENG-3",
		AgentState: state.AgentWorking, Delivery: state.DeliveryNone})
	api.sessions = []linear.AgentSession{pendingSession("as-3", "ENG-3", "team-1")}
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 0 {
		t.Fatal("must bind, not spawn")
	}
	if s, _ := d.sessions.Get("app-eng-3"); s.AgentSessionID != "as-3" {
		t.Fatalf("not bound: %+v", s)
	}
}

// The mirror streams each CHANGE once: plan checklist, PR link, delivery.
func TestLinearAgentMirrorsChangesOnce(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"}
	d, api, _ := agentDaemon(t, p)
	d.sessions.Upsert(session.Session{ID: "app-eng-4", Source: "native", Project: "app", Issue: "ENG-4",
		AgentState: state.AgentWorking, Delivery: state.DeliveryCIPending, AgentSessionID: "as-4",
		AgentMirror: session.AgentMirror{Started: true},
		Board:       board.Board{Phase: "implementing", Todos: []board.Todo{{Text: "a", State: board.TodoDone}, {Text: "b", State: board.TodoActive}}},
		PR:          &scm.PR{Number: 12, URL: "https://github.com/acme/app/pull/12"}})

	d.linearAgentCycle(context.Background())
	d.linearAgentCycle(context.Background())

	if plan := api.plans["as-4"]; len(plan) != 2 || plan[0].Status != "completed" || plan[1].Status != "inProgress" {
		t.Fatalf("plan = %+v", plan)
	}
	if u := api.urls["as-4"]; len(u) != 1 || u[0].URL != "https://github.com/acme/app/pull/12" {
		t.Fatalf("urls = %+v", u)
	}
	b := api.bodies("as-4")
	if strings.Count(b, "Phase: implementing") != 1 || strings.Count(b, "Opened pull request") != 1 || strings.Count(b, "CI running") != 1 {
		t.Fatalf("each change must be posted exactly once:\n%s", b)
	}

	d.sessions.Update("app-eng-4", func(s *session.Session) bool {
		s.SetDelivery(state.DeliveryMerged, time.Now())
		return true
	})
	d.linearAgentCycle(context.Background())
	d.linearAgentCycle(context.Background())
	if strings.Count(api.bodies("as-4"), "Merged #12") != 1 {
		t.Fatalf("merge must end the thread once:\n%s", api.bodies("as-4"))
	}
}

// A submitted plan goes to Linear as an approve / request-changes elicitation,
// and the human's reply there is the verdict.
func TestLinearAgentPlanApprovalFromLinear(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"}
	d, api, _ := agentDaemon(t, p)
	s := gatedSession(session.PlanSubmitted)
	s.ID, s.Project, s.Issue, s.TmuxName = "app-eng-5", "app", "ENG-5", "app-eng-5"
	s.Plan, s.PlanRound, s.AgentSessionID = "do the thing", 1, "as-5"
	d.sessions.Upsert(s)
	d.paneTail = func(context.Context, string, int) (string, error) { return restingPane, nil }
	var typed []string
	d.sendKeys = func(_ context.Context, _ string, text string) error { typed = append(typed, text); return nil }

	d.linearAgentCycle(context.Background())
	api.mu.Lock()
	acts := api.activities["as-5"]
	api.mu.Unlock()
	if len(acts) == 0 || acts[len(acts)-1].Signal != "select" || !strings.Contains(fmt.Sprint(acts[len(acts)-1].Content["body"]), "do the thing") {
		t.Fatalf("want a select elicitation carrying the plan, got %+v", acts)
	}

	// The agent's own activity must be ignored; the human's "Approve" decides.
	later := func(s int) string {
		return time.Now().Add(time.Duration(s) * time.Second).UTC().Format(time.RFC3339Nano)
	}
	approvedAt := later(2)
	api.mu.Lock()
	api.prompts["as-5"] = []linear.AgentPrompt{
		{ID: "p0", Body: "approve", UserID: "agent-user", CreatedAt: later(1)},
		{ID: "p1", Body: "Approve", UserID: "human", CreatedAt: approvedAt},
	}
	api.mu.Unlock()
	d.linearAgentCycle(context.Background())
	cur, _ := d.sessions.Get("app-eng-5")
	if cur.PlanGate != session.PlanApproved {
		t.Fatalf("gate = %s", cur.PlanGate)
	}
	if cur.AgentMirror.PromptCursor != approvedAt {
		t.Fatalf("cursor = %q", cur.AgentMirror.PromptCursor)
	}
	d.connWg.Wait() // decidePlan delivers asynchronously
	d.flushAgentNotices(context.Background(), "app-eng-5")
	if len(typed) != 1 || !strings.Contains(typed[0], "approved") {
		t.Fatalf("typed = %q", typed)
	}
}

// Any other human reply is queued and typed into the agent at its prompt.
func TestLinearAgentRelaysReplies(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"}
	d, api, _ := agentDaemon(t, p)
	s := gatedSession(session.PlanNone)
	s.ID, s.Project, s.Issue, s.TmuxName, s.AgentSessionID = "app-eng-6", "app", "ENG-6", "app-eng-6", "as-6"
	d.sessions.Upsert(s)
	api.prompts["as-6"] = []linear.AgentPrompt{{ID: "p1", Body: "use the v2 endpoint", UserID: "human", CreatedAt: "2026-01-01T00:00:01Z"}}
	d.paneTail = func(context.Context, string, int) (string, error) { return "✻ Thinking… (3s)\n", nil } // mid-turn: stays queued
	d.linearAgentCycle(context.Background())
	d.connWg.Wait()
	cur, _ := d.sessions.Get("app-eng-6")
	if len(cur.PendingNotices) != 1 || !strings.Contains(cur.PendingNotices[0], "use the v2 endpoint") {
		t.Fatalf("notices = %+v", cur.PendingNotices)
	}
	// Handled once.
	d.linearAgentCycle(context.Background())
	if cur, _ := d.sessions.Get("app-eng-6"); len(cur.PendingNotices) != 1 {
		t.Fatalf("relayed twice: %+v", cur.PendingNotices)
	}
}

func TestLinearAgentDisabledDoesNothing(t *testing.T) {
	d, api, nat := agentDaemon(t, config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"})
	d.cfg.LinearAgent.Enabled = false
	api.sessions = []linear.AgentSession{pendingSession("as-1", "ENG-7", "team-1")}
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 0 || len(api.kinds("as-1")) != 0 {
		t.Fatal("a disabled agent must not touch Linear or spawn")
	}
	if d.linearAgentStatus() != nil {
		t.Fatal("status must be nil while disabled")
	}
}

func TestParseAgentToken(t *testing.T) {
	if tok, err := ParseAgentToken(`{"access_token":"a","refresh_token":"r"}`); err != nil || tok.AccessToken != "a" || tok.RefreshToken != "r" {
		t.Fatalf("json: %+v %v", tok, err)
	}
	if tok, err := ParseAgentToken("lin_oauth_raw"); err != nil || tok.AccessToken != "lin_oauth_raw" {
		t.Fatalf("raw: %+v %v", tok, err)
	}
	if _, err := ParseAgentToken(`{"nope":1}`); err == nil || strings.Contains(err.Error(), "nope") {
		t.Fatalf("bad json must fail without echoing the value: %v", err)
	}
}

// An expired token is refreshed and the rotated token persisted.
func TestLinearAgentRefreshesExpiredToken(t *testing.T) {
	d, _, _ := agentDaemon(t, config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"})
	d.agent.apiOverride = nil
	api := newFakeAgentAPI()
	var stored linear.OAuthToken
	var usedToken string
	d.agent.loadToken = func(config.LinearAgentConfig) (linear.OAuthToken, error) {
		return linear.OAuthToken{AccessToken: "old", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Hour)}, nil
	}
	d.agent.refresh = func(_ context.Context, _ config.LinearAgentConfig, rt string) (linear.OAuthToken, error) {
		if rt != "r1" {
			t.Errorf("refresh token = %q", rt)
		}
		return linear.OAuthToken{AccessToken: "new", RefreshToken: "r2", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	d.agent.storeToken = func(_ config.LinearAgentConfig, tk linear.OAuthToken) error { stored = tk; return nil }
	d.agent.newAPI = func(_, token string) linear.AgentAPI { usedToken = token; return api }

	d.linearAgentCycle(context.Background())
	if usedToken != "new" || stored.RefreshToken != "r2" {
		t.Fatalf("used=%q stored=%+v", usedToken, stored)
	}
	if st := d.linearAgentStatus(); st == nil || !st.Connected || st.AgentName != "lola" {
		t.Fatalf("status = %+v", st)
	}
}

func TestLinearWebhookDoorbell(t *testing.T) {
	d, _, _ := agentDaemon(t, config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"})
	d.agentWebhookSecret = func(config.LinearAgentConfig) (string, error) { return "s3cret", nil }
	h := d.linearWebhookHandler(func() config.LinearAgentConfig { return d.cfg.LinearAgent })
	sign := func(body string) string {
		m := hmac.New(sha256.New, []byte("s3cret"))
		m.Write([]byte(body))
		return hex.EncodeToString(m.Sum(nil))
	}
	post := func(body, sig string) int {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Linear-Signature", sig)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	drain := func() bool {
		select {
		case <-d.agent.wake:
			return true
		default:
			return false
		}
	}

	fresh := fmt.Sprintf(`{"type":"AgentSessionEvent","webhookTimestamp":%d}`, time.Now().UnixMilli())
	if code := post(fresh, sign(fresh)); code != http.StatusOK || !drain() {
		t.Fatalf("valid delivery: code=%d", code)
	}
	if code := post(fresh, sign("other")); code != http.StatusUnauthorized || drain() {
		t.Fatalf("bad signature must be refused and must not ring: %d", code)
	}
	stale := fmt.Sprintf(`{"webhookTimestamp":%d}`, time.Now().Add(-time.Hour).UnixMilli())
	if code := post(stale, sign(stale)); code != http.StatusUnauthorized || drain() {
		t.Fatalf("a replayed delivery must be refused: %d", code)
	}
	d.agentWebhookSecret = func(config.LinearAgentConfig) (string, error) { return "", fmt.Errorf("none") }
	if code := post(fresh, sign(fresh)); code != http.StatusServiceUnavailable || drain() {
		t.Fatalf("no secret must refuse every delivery: %d", code)
	}
}

// A "yes" written BEFORE the current plan was posted to Linear must not
// approve it: it is relayed to the agent as an ordinary message.
func TestLinearAgentStaleReplyDoesNotDecidePlan(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"}
	d, api, _ := agentDaemon(t, p)
	s := gatedSession(session.PlanSubmitted)
	s.ID, s.Project, s.Issue, s.TmuxName = "app-eng-8", "app", "ENG-8", "app-eng-8"
	s.Plan, s.PlanRound, s.AgentSessionID = "plan", 1, "as-8"
	d.sessions.Upsert(s)
	d.paneTail = func(context.Context, string, int) (string, error) { return "✻ Thinking… (3s)\n", nil }
	api.prompts["as-8"] = []linear.AgentPrompt{{ID: "old", Body: "yes", UserID: "human", CreatedAt: "2020-01-01T00:00:00Z"}}

	d.linearAgentCycle(context.Background()) // relays BEFORE the plan is mirrored
	d.linearAgentCycle(context.Background())
	cur, _ := d.sessions.Get("app-eng-8")
	if cur.PlanGate != session.PlanSubmitted {
		t.Fatalf("a stale reply decided the plan: %s", cur.PlanGate)
	}
	if len(cur.PendingNotices) != 1 || !strings.Contains(cur.PendingNotices[0], "yes") {
		t.Fatalf("the stale reply must be relayed as a message: %+v", cur.PendingNotices)
	}
	if !planVerdictFor(cur, linear.AgentPrompt{CreatedAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}) {
		t.Fatal("a reply after the post must count as the verdict")
	}
	if planVerdictFor(cur, linear.AgentPrompt{CreatedAt: "not a time"}) {
		t.Fatal("an unparseable timestamp must fail closed")
	}
}

// The cap is re-checked inside the dispatch critical section: a slot taken
// between the loop's early check and the spawn queues the delegation instead
// of spawning past the cap.
func TestLinearAgentRechecksCapBeforeSpawn(t *testing.T) {
	p := config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"}
	d, api, nat := agentDaemon(t, p)
	d.cfg.Defaults.GlobalCap = 1
	api.sessions = []linear.AgentSession{pendingSession("as-9", "ENG-9", "team-1")}
	// The health check runs after the early cap check: a concurrent poll fills
	// the last slot right there.
	d.runtimeHealth = func(string) error {
		d.sessions.Upsert(session.Session{ID: "app-eng-1", Source: "native", Project: "app", Issue: "ENG-1",
			AgentState: state.AgentWorking, Delivery: state.DeliveryNone})
		return nil
	}
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 0 {
		t.Fatalf("spawned past the cap: %+v", nat.spawnCalls())
	}
	if !strings.Contains(api.bodies("as-9"), "Queued") {
		t.Fatalf("must be queued, got:\n%s", api.bodies("as-9"))
	}
	if d.inflight.Has("uuid-ENG-9") {
		t.Fatal("a refused admit must release its in-flight claim")
	}
}

// When the rotated token cannot be persisted, the NEXT refresh must use the
// in-memory refresh token (storage holds one Linear already invalidated), and
// concurrent callers refresh once, not once each.
func TestLinearAgentRefreshUsesInMemoryTokenAndIsExclusive(t *testing.T) {
	d, _, _ := agentDaemon(t, config.Project{Name: "app", Path: "/tmp/app", TeamID: "team-1"})
	d.agent.apiOverride = nil
	now := time.Now()
	d.agent.now = func() time.Time { return now }
	var mu sync.Mutex
	var used []string
	n := 0
	d.agent.loadToken = func(config.LinearAgentConfig) (linear.OAuthToken, error) {
		return linear.OAuthToken{AccessToken: "a0", RefreshToken: "r0", ExpiresAt: now.Add(-time.Hour)}, nil
	}
	d.agent.storeToken = func(config.LinearAgentConfig, linear.OAuthToken) error { return fmt.Errorf("no keychain") }
	d.agent.refresh = func(_ context.Context, _ config.LinearAgentConfig, rt string) (linear.OAuthToken, error) {
		mu.Lock()
		defer mu.Unlock()
		used = append(used, rt)
		n++
		time.Sleep(5 * time.Millisecond)
		return linear.OAuthToken{AccessToken: fmt.Sprintf("a%d", n), RefreshToken: fmt.Sprintf("r%d", n), ExpiresAt: now.Add(time.Hour)}, nil
	}
	d.agent.newAPI = func(_, _ string) linear.AgentAPI { return newFakeAgentAPI() }

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := d.ensureAgentAPI(context.Background(), d.cfg.LinearAgent, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(used) != 1 || used[0] != "r0" {
		t.Fatalf("concurrent callers must refresh once with r0, got %v", used)
	}

	now = now.Add(2 * time.Hour) // the refreshed token expires
	if _, err := d.ensureAgentAPI(context.Background(), d.cfg.LinearAgent, ""); err != nil {
		t.Fatal(err)
	}
	if len(used) != 2 || used[1] != "r1" {
		t.Fatalf("the second refresh must use the in-memory rotated token r1, got %v", used)
	}
}
