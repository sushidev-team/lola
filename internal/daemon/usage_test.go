package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/quota"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sysload"
	"github.com/sushidev-team/lola/internal/usage"
)

// assertHeldTick runs one tick and checks the health-gate contract: an error
// naming want, no Linear call, no spawn, no in-flight claim, no seen file, and
// the reason recorded as the poll's LastError.
func assertHeldTick(t *testing.T, d *Daemon, fake *linear.Fake, nat *fakeNative, want string) {
	t.Helper()
	_, err := d.tick(context.Background(), "p1", false)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("tick err = %v, want it to mention %q", err, want)
	}
	if names := fake.CallNames(); len(names) != 0 {
		t.Errorf("held tick must make NO linear calls, got %v", names)
	}
	if len(nat.spawnCalls()) != 0 {
		t.Errorf("held tick must not spawn, got %v", nat.spawnCalls())
	}
	if d.inflight.Has("uuid-FE-1") {
		t.Error("held tick must not claim in-flight")
	}
	if _, err := os.Stat(seenPath(d, "p1")); !os.IsNotExist(err) {
		t.Errorf("held tick must not write seen, stat err = %v", err)
	}
	if got := d.status.get("p1").LastError; !strings.Contains(got, want) {
		t.Errorf("LastError = %q, want it to mention %q", got, want)
	}
}

func spendFixture(t *testing.T, cfg *config.Config) (*Daemon, *linear.Fake, *fakeNative) {
	t.Helper()
	fake := &linear.Fake{Issues: []linear.Issue{testIssue("FE-1", 1, "2024-01-01T00:00:00Z")}}
	nat := &fakeNative{}
	d := newTestDaemon(t, cfg, fake, nat)
	d.spend.root = t.TempDir()
	d.spend.codexHome = ""
	d.spend.codex = usage.NewCodexScanner(filepath.Join(d.home, "worktrees"))
	d.spend.opencodeDB = ""
	d.spend.sample = func(context.Context) sysload.Sample { return sysload.Sample{Load1: 0.1, CPUs: 8, FreeMemPercent: 80} }
	return d, fake, nat
}

func today() string { return time.Now().Format(usage.DayFormat) }

func TestTickHeldByGlobalBudget(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	cfg.Budget.DailyTokens = 5_000_000
	d, fake, nat := spendFixture(t, cfg)
	// Usage recorded by a session of ANOTHER project still counts globally,
	// and so do lola's own helpers.
	d.spend.ledger.Set(today(), "other-1", usage.Entry{Project: "other", Totals: usage.Totals{Input: 4_000_000}})
	d.spend.ledger.Set(today(), helperSource, usage.Entry{Project: "", Totals: usage.Totals{Output: 200_000}}) // weighs 1M
	assertHeldTick(t, d, fake, nat, "budget.daily_tokens")
}

func TestTickHeldByProjectBudgetOnlyForThatProject(t *testing.T) {
	cfg := testConfig(labelPoll("p1"), labelPoll("p2"))
	cfg.Projects[0].DailyBudgetTokens = 2_000_000
	d, fake, nat := spendFixture(t, cfg)
	d.spend.ledger.Set(today(), "p1-1", usage.Entry{Project: "p1", Totals: usage.Totals{Input: 2_500_000}})
	assertHeldTick(t, d, fake, nat, "daily_budget_tokens")

	// p2 has no limit of its own and there is no global one: it dispatches.
	if _, err := d.tick(context.Background(), "p2", false); err != nil {
		t.Fatalf("p2 tick: %v", err)
	}
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("p2 must dispatch, spawns = %v", nat.spawnCalls())
	}
}

func TestTickBudgetIgnoresYesterday(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	cfg.Budget.DailyTokens = 1_000
	d, _, nat := spendFixture(t, cfg)
	d.spend.ledger.Set(time.Now().AddDate(0, 0, -1).Format(usage.DayFormat), "p1-1", usage.Entry{Project: "p1", Totals: usage.Totals{Input: 1_000_000}})
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("yesterday's spend must not hold today, spawns = %v", nat.spawnCalls())
	}
}

func TestTickHeldByMachineLoad(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	cfg.Load.MaxLoadPerCPU = 1.5
	d, fake, nat := spendFixture(t, cfg)
	d.spend.sample = func(context.Context) sysload.Sample { return sysload.Sample{Load1: 20, CPUs: 8, FreeMemPercent: 50} }
	assertHeldTick(t, d, fake, nat, "machine busy")
	if st := d.usageStatus(time.Now()); st.Load == nil || st.Load.Busy == "" {
		t.Errorf("status must carry the busy sample, got %+v", st.Load)
	}
}

func TestTickLoadUnknownDoesNotHold(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	cfg.Load.MaxLoadPerCPU = 0.01
	cfg.Load.MinFreeMemoryPercent = 99
	d, _, nat := spendFixture(t, cfg)
	d.spend.sample = func(context.Context) sysload.Sample { return sysload.Sample{Load1: -1, CPUs: 8, FreeMemPercent: -1} }
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("an unreadable machine must not hold dispatch: %v", err)
	}
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("spawns = %v", nat.spawnCalls())
	}
}

func transcriptLine(id string, at time.Time, output int64) string {
	return `{"type":"assistant","timestamp":"` + at.UTC().Format(time.RFC3339Nano) +
		`","message":{"id":"` + id + `","model":"claude-sonnet-5-5","usage":{"input_tokens":0,"output_tokens":` +
		strconv.FormatInt(output, 10) + `,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
}

func TestUsagePassAttributesSessionsAndHelpers(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	d, _, _ := spendFixture(t, cfg)
	now := time.Now()

	s := session.Session{ID: "p1-1", Source: "native", Project: "p1"}
	d.sessions.Upsert(s)
	// No hook has reported a transcript yet: the worktree slug is used.
	dir := usage.SlugDir(d.spend.root, filepath.Join(d.home, "worktrees", "p1", "p1-1"))
	writeFile(t, filepath.Join(dir, "a.jsonl"), transcriptLine("m1", now, 1_000_000))
	// A review pass ran in the same worktree: same directory, same session.
	writeFile(t, filepath.Join(dir, "review.jsonl"), transcriptLine("m2", now, 1_000_000))
	// lola's helpers have a slug of their own.
	writeFile(t, filepath.Join(usage.SlugDir(d.spend.root, d.spend.helpers), "h.jsonl"), transcriptLine("h1", now, 100_000))

	d.usagePass(context.Background(), now)

	u := d.sessionUsage("p1-1")
	if u == nil || u.Tokens != 2_000_000 || u.TodayTokens != 2_000_000 || u.TotalUSD < 19.99 || u.TotalUSD > 20.01 {
		t.Fatalf("session usage = %+v, want 2M tokens (~$20) today", u)
	}
	st := d.usageStatus(now)
	if st.Tokens != 2_100_000 || st.Weighted != 10_500_000 {
		t.Errorf("today = %d tokens / %d weighted, want 2.1M / 10.5M (session + helpers)", st.Tokens, st.Weighted)
	}
	if len(st.Projects) != 1 || st.Projects[0].Name != "p1" || st.Projects[0].Tokens != 2_000_000 {
		t.Errorf("projects = %+v, want only p1 at 2M (helpers belong to no project)", st.Projects)
	}

	// The ledger outlives the session: tear it down, reload from disk.
	d.sessions.Delete("p1-1")
	d.usagePass(context.Background(), now)
	back, err := usage.LoadLedger(d.spend.path)
	if err != nil {
		t.Fatal(err)
	}
	if all, _ := back.Day(today()); all.Tokens() != 2_100_000 {
		t.Errorf("persisted day total = %d, want the removed session still counted", all.Tokens())
	}
	if d.sessionUsage("p1-1") != nil {
		t.Error("a removed session must drop out of the per-session cache")
	}
}

func TestUsageDirTrustsOnlyTranscriptsUnderRoot(t *testing.T) {
	root := "/r/projects"
	in := session.Session{ID: "p-1", Project: "p", Source: "native", TranscriptPath: "/r/projects/-x/abc.jsonl"}
	if got := usageDir(root, "/h", in); got != "/r/projects/-x" {
		t.Errorf("reported transcript dir = %q", got)
	}
	out := in
	out.TranscriptPath = "/etc/passwd.jsonl"
	if got := usageDir(root, "/h", out); got != usage.SlugDir(root, "/h/worktrees/p/p-1") {
		t.Errorf("a path outside root must fall back to the worktree slug, got %q", got)
	}
	if got := usageDir("", "/h", in); got != "" {
		t.Errorf("no root = %q, want empty", got)
	}
}

func TestBudgetNotifyFiresOncePerDay(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	cfg.Budget = config.BudgetConfig{DailyTokens: 1_000, Notify: true}
	d, _, _ := spendFixture(t, cfg)
	rec := &recordingNotifier{}
	d.notifier = rec
	d.spend.ledger.Set(today(), "p1-1", usage.Entry{Project: "p1", Totals: usage.Totals{Input: 2_000}})
	d.notifyBudgets(context.Background(), today())
	d.notifyBudgets(context.Background(), today())
	if n := len(rec.notes()); n != 1 {
		t.Fatalf("notifications = %d, want exactly 1", n)
	}
}

func TestTickBudgetCountsWeightedNotRawTokens(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	cfg.Budget.DailyTokens = 5_000_000
	d, _, nat := spendFixture(t, cfg)
	// 40M raw tokens of cache reads weigh only 4M: under the limit.
	d.spend.ledger.Set(today(), "p1-1", usage.Entry{Project: "p1", Totals: usage.Totals{CacheRead: 40_000_000}})
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("cache reads must not exhaust a weighted budget: %v", err)
	}
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("spawns = %v", nat.spawnCalls())
	}
}

func TestUsagePassRanksAgainstFinishedSessionsAndFlagsBurn(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	d, _, _ := spendFixture(t, cfg)
	now := time.Now()
	// Ten finished sessions, each ~$1 over six active windows (=$1/active h).
	for i := 0; i < 10; i++ {
		d.spend.ledger.Set(now.AddDate(0, 0, -2).Format(usage.DayFormat), "old-"+strconv.Itoa(i),
			usage.Entry{Project: "p1", Totals: usage.Totals{Output: 100_000, CostUSD: 1, Slots: 6}})
	}
	s := session.Session{ID: "p1-1", Source: "native", Project: "p1"}
	d.sessions.Upsert(s)
	dir := usage.SlugDir(d.spend.root, filepath.Join(d.home, "worktrees", "p1", "p1-1"))
	// $10 of sonnet output in the last few minutes: top of the history and far
	// above the $5/h burn floor.
	writeFile(t, filepath.Join(dir, "a.jsonl"), transcriptLine("m1", now.Add(-2*time.Minute), 1_000_000))

	d.usagePass(context.Background(), now)
	u := d.sessionUsage("p1-1")
	if u == nil || u.Level != usage.LevelTop || u.Of != 10 || u.Percentile != 100 {
		t.Fatalf("rank = %+v, want top of 10 finished sessions", u)
	}
	if !u.Burning || u.TokensPerHour <= 0 {
		t.Fatalf("burn = %+v, want burning", u)
	}
}

func TestUsageStatusCarriesLiveQuotas(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	d, _, _ := spendFixture(t, cfg)
	now := time.Now()
	err := quota.RecordClaude(quota.ClaudePath(d.home), quota.Snapshot{At: now, Windows: []quota.Window{
		{Label: "5h", UsedPercent: 42, ResetsAt: now.Add(time.Hour)},
		{Label: "7d", UsedPercent: 90, ResetsAt: now.Add(-time.Minute)}, // already reset
	}})
	if err != nil {
		t.Fatal(err)
	}
	d.usagePass(context.Background(), now)
	q := d.usageStatus(now).Quotas
	if len(q) != 1 || q[0].Agent != "claude" || len(q[0].Windows) != 1 || q[0].Windows[0].UsedPercent != 42 {
		t.Fatalf("quotas = %+v, want claude 5h only (the reset 7d window dropped)", q)
	}
}

func TestUsagePassCountsCodexLogsInTheWorktree(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	d, _, _ := spendFixture(t, cfg)
	d.spend.codexHome = t.TempDir()
	now := time.Now()
	wt := filepath.Join(d.home, "worktrees", "p1", "p1-1")
	d.sessions.Upsert(session.Session{ID: "p1-1", Source: "native", Project: "p1", Agent: "codex"})
	ts := now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	logDir := filepath.Join(d.spend.codexHome, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	writeFile(t, filepath.Join(logDir, "rollout-a.jsonl"),
		`{"type":"session_meta","payload":{"cwd":"`+wt+`"}}`+"\n"+
			`{"timestamp":"`+ts+`","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":50}}}}`+"\n")
	// A codex log from the user's own work, outside lola's worktrees.
	writeFile(t, filepath.Join(logDir, "rollout-b.jsonl"),
		`{"type":"session_meta","payload":{"cwd":"/elsewhere"}}`+"\n"+
			`{"timestamp":"`+ts+`","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":9999999,"cached_input_tokens":0,"output_tokens":0}}}}`+"\n")

	d.usagePass(context.Background(), now)
	u := d.sessionUsage("p1-1")
	if u == nil || u.Tokens != 1050 || u.Agent != "codex" || u.TotalUSD != 0 {
		t.Fatalf("codex usage = %+v, want 1050 tokens, no price", u)
	}
	if st := d.usageStatus(now); st.Tokens != 1050 {
		t.Errorf("day total = %d, want only lola's codex log", st.Tokens)
	}
}

func TestUsagePassCountsOpencodeMessagesInTheWorktree(t *testing.T) {
	cfg := testConfig(labelPoll("p1"))
	d, _, _ := spendFixture(t, cfg)
	now := time.Now()
	wt := filepath.Join(d.home, "worktrees", "p1", "p1-1")
	d.spend.opencodeDB = filepath.Join(t.TempDir(), "opencode.db")
	writeFile(t, d.spend.opencodeDB, "")
	d.spend.opencode = usage.NewOpencodeScanner(filepath.Join(d.home, "worktrees"))
	d.spend.opencode.RunSQL = func(context.Context, string, string) ([]byte, error) {
		ms := strconv.FormatInt(now.Add(-time.Minute).UnixMilli(), 10)
		return []byte(`[{"id":"m","dir":"` + wt + `","created":` + ms + `,"updated":` + ms + `,"i":500,"o":40,"r":10,"cr":2000,"cw":0,"cost":0}]`), nil
	}
	d.sessions.Upsert(session.Session{ID: "p1-1", Source: "native", Project: "p1", Agent: "opencode"})
	d.usagePass(context.Background(), now)
	u := d.sessionUsage("p1-1")
	if u == nil || u.Tokens != 2550 || u.Agent != "opencode" {
		t.Fatalf("opencode usage = %+v, want 2550 tokens", u)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type recordingNotifier struct {
	mu  sync.Mutex
	got []notify.Note
}

func (r *recordingNotifier) Notify(_ context.Context, n notify.Note) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, n)
}

func (r *recordingNotifier) notes() []notify.Note {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.Note(nil), r.got...)
}
