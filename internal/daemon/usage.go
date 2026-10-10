package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/quota"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sysload"
	"github.com/sushidev-team/lola/internal/usage"
)

// Usage + load: the tokens lola's agents use (internal/usage), how heavy each
// session is against the user's own history, the daily token budgets that hold
// dispatch once reached, and the [load] hold
// on a machine that is already saturated. All three only ever decide whether a
// tick may START something; nothing here touches a live session.
//
// The usage pass runs on its own minute loop rather than inside the observer:
// a first scan of a long session reads megabytes of transcript, and the
// observer's tmux/gh cadence must never wait on that. It is local file I/O
// only — no exec — and every later pass reads just the bytes appended since.

const (
	// usageInterval paces the usage scan. A budget is therefore enforced up to
	// one interval late — one minute of usage, a rounding error on a day.
	usageInterval = time.Minute

	// helperSource is the ledger key for lola's own claude helpers ([brain],
	// [statusagent]): global spend, charged to no project.
	helperSource = "lola:helpers"
)

// spendState is the daemon's usage + load bookkeeping. Its own lock, never
// held across an exec or while taking d.mu.
type spendState struct {
	mu      sync.Mutex
	scanner *usage.Scanner
	ledger  *usage.Ledger
	path    string // the ledger file; "" = in-memory only
	// root is claude-code's projects directory; "" disables scanning (no home).
	root string
	// helpers is the working directory [brain]/[statusagent] runs start in, so
	// their transcripts land in one known slug (see helperWorkDir).
	helpers   string
	bySession map[string]protocol.UsageInfo
	// notified remembers, per budget scope ("" = global, else the project),
	// the day its limit notification fired, so it fires once per day.
	notified map[string]string
	// load is the last [load] sample, for cmd=status; nil when [load] is off.
	load *protocol.LoadInfo
	// sample is the machine-load probe seam (sysload.Read in production).
	sample func(context.Context) sysload.Sample
	// quotas is the last read of the agents' subscription limits;
	// codexHome is where codex keeps its logs ("" = never look).
	quotas    []protocol.QuotaInfo
	codexHome string
	// codex sums codex session logs that ran in lola's worktrees.
	codex *usage.CodexScanner
}

func newSpendState(home string, logf func(string, ...any)) *spendState {
	path := filepath.Join(home, "state", "usage.json")
	ledger, err := usage.LoadLedger(path)
	if err != nil {
		// A corrupt ledger restarts today's count rather than blocking the
		// daemon; the transcripts are still on disk, so the next pass rebuilds
		// every live session's figure.
		logf("usage ledger unreadable, starting empty: %v", err)
	}
	return &spendState{
		scanner:   usage.NewScanner(),
		ledger:    ledger,
		path:      path,
		root:      usage.ProjectsRoot(),
		helpers:   filepath.Join(home, "helpers"),
		bySession: map[string]protocol.UsageInfo{},
		notified:  map[string]string{},
		sample:    sysload.Read,
		codexHome: quota.CodexHome(),
		codex:     usage.NewCodexScanner(filepath.Join(home, "worktrees")),
	}
}

// helperWorkDir is where the [brain]/[statusagent] claude runs start: a fixed,
// empty directory of lola's own, so claude-code files all of their transcripts
// under one ~/.claude/projects slug the usage pass can sum. It also keeps them
// out of whatever directory the daemon happened to be launched from (launchd
// starts it in /), where a project CLAUDE.md would otherwise be loaded into
// every summary. "" when it cannot be created: the helper then inherits the
// daemon's cwd, exactly as before, and its spend is simply not attributed.
func helperWorkDir() string {
	home, err := config.Home()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, "helpers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// usageDir is the transcript directory to sum for s: the one claude-code
// reported through its hooks when that lies under root (a hook payload is not
// a reason to walk an arbitrary directory), else the slug of the session's
// worktree. "" when neither is known.
func usageDir(root, home string, s session.Session) string {
	if root == "" {
		return ""
	}
	if s.TranscriptPath != "" {
		dir := filepath.Dir(filepath.Clean(s.TranscriptPath))
		if rel, err := filepath.Rel(root, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, string(filepath.Separator)) {
			return dir
		}
	}
	return usage.SlugDir(root, sessionWorktree(home, s))
}

func (d *Daemon) usageLoop(ctx context.Context) {
	defer d.wg.Done()
	d.safeUsagePass(ctx)
	t := time.NewTicker(usageInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.safeUsagePass(ctx)
		}
	}
}

func (d *Daemon) safeUsagePass(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "usage pass panic (daemon keeps running): %v", r)
		}
	}()
	d.usagePass(ctx, time.Now())
}

// usagePass re-sums every session's transcripts (incrementally) and lola's
// helpers, writes the per-day totals into the ledger, persists it when it
// changed, refreshes the [load] sample, and fires any newly reached budget
// notification.
func (d *Daemon) usagePass(ctx context.Context, now time.Time) {
	sp := d.spend
	today := now.Format(usage.DayFormat)
	snap := d.sessions.Snapshot()

	// codex logs are read before taking the lock: they are not keyed by
	// directory, so one pass over the newest logs serves every session.
	if sp.codexHome != "" {
		sp.codex.Scan(filepath.Join(sp.codexHome, "sessions"))
	}

	sp.mu.Lock()
	changed := false
	keep := map[string]bool{}
	infos := make(map[string]protocol.UsageInfo, len(snap))
	record := func(source string, e usage.Entry, days map[string]usage.Totals) {
		for day, t := range days {
			e.Totals = t
			if sp.ledger.Set(day, source, e) {
				changed = true
			}
		}
	}
	scan := func(dir, source string) map[string]usage.Totals {
		if dir == "" || keep[dir] {
			return nil
		}
		keep[dir] = true
		days, err := sp.scanner.ScanDir(dir)
		if err != nil {
			d.logf("", "usage: scan %s: %v", source, err)
		}
		return days
	}
	type live struct {
		agent  string
		days   map[string]usage.Totals
		recent usage.Totals
	}
	lives := map[string]live{}
	burnFrom := now.Add(-usage.BurnWindow).Truncate(usage.SlotDuration)
	for _, s := range snap {
		dir := usageDir(sp.root, d.home, s)
		days := scan(dir, s.ID)
		recent := sp.scanner.Recent(dir, burnFrom)
		// A session's codex usage is every codex log that ran in its
		// worktree: a codex worker, and any codex-session review pass.
		if wt := sessionWorktree(d.home, s); wt != "" {
			days = usage.MergeDays(days, sp.codex.For(wt))
			recent.Add(sp.codex.Recent(wt, burnFrom))
		}
		if len(days) == 0 {
			continue
		}
		agent := s.Agent
		if agent == "" {
			agent = "claude"
		}
		record(s.ID, usage.Entry{Project: s.Project, Agent: agent}, days)
		lives[s.ID] = live{agent: agent, days: days, recent: recent}
	}
	record(helperSource, usage.Entry{}, scan(usage.SlugDir(sp.root, sp.helpers), helperSource))
	sp.scanner.Retain(keep)
	if sp.ledger.Prune(now, usage.KeepDays) {
		changed = true
	}
	hist := history(sp.ledger, snap)
	hours := now.Sub(burnFrom).Hours()
	for id, l := range lives {
		sc := usage.ScaleFor(l.agent)
		h := hist[l.agent]
		total := usage.Sum(l.days)
		rank := usage.RankAmong(sc.Weight(total), h.weights, sc)
		info := protocol.UsageInfo{
			Agent:       l.agent,
			Tokens:      total.Tokens(),
			TodayTokens: l.days[today].Tokens(),
			TotalUSD:    total.CostUSD,
			TodayUSD:    l.days[today].CostUSD,
			Level:       rank.Level,
			Percentile:  rank.Percentile,
			Of:          rank.Of,
		}
		if hours > 0 && sc.Weight(l.recent)/hours >= usage.BurnThreshold(h.rates, sc) {
			info.Burning = true
			info.TokensPerHour = int64(float64(l.recent.Tokens()) / hours)
		}
		infos[id] = info
	}
	sp.bySession = infos
	var saveErr error
	if changed && sp.path != "" {
		saveErr = sp.ledger.Save(sp.path)
	}
	sp.mu.Unlock()
	if saveErr != nil {
		d.logf("", "usage: persist ledger: %v", saveErr)
	}

	quotas := readQuotas(quota.ClaudePath(d.home), sp.codexHome, now)
	sp.mu.Lock()
	sp.quotas = quotas
	sp.mu.Unlock()

	d.mu.Lock()
	lim := d.cfg.Load
	d.mu.Unlock()
	if lim.Enabled() {
		d.sampleLoad(ctx, lim)
	} else {
		sp.mu.Lock()
		sp.load = nil
		sp.mu.Unlock()
	}
	d.notifyBudgets(ctx, today)
}

// readQuotas reads each agent's last observed subscription limits, dropping
// windows that have already reset. Local file reads only: the claude figures
// are what `lola hook statusline` recorded, the codex ones the tail of its
// newest session log.
func readQuotas(claudePath, codexHome string, now time.Time) []protocol.QuotaInfo {
	var out []protocol.QuotaInfo
	add := func(agent string, s quota.Snapshot) {
		s, ok := s.Live(now)
		if !ok {
			return
		}
		qi := protocol.QuotaInfo{Agent: agent, Plan: s.Plan, At: s.At}
		for _, w := range s.Windows {
			qi.Windows = append(qi.Windows, protocol.QuotaWindow{Label: w.Label, UsedPercent: w.UsedPercent, ResetsAt: w.ResetsAt})
		}
		out = append(out, qi)
	}
	if s, err := quota.ReadClaude(claudePath); err == nil {
		add("claude", s)
	}
	if s, ok := quota.LatestCodex(codexHome); ok {
		add("codex", s)
	}
	return out
}

// sessionWorktree is s's worktree directory: the persisted one, else the
// path lola derives for a native session. "" when neither is known.
func sessionWorktree(home string, s session.Session) string {
	if s.Worktree != "" {
		return s.Worktree
	}
	if s.Source != "native" || s.Project == "" || s.ID == "" {
		return ""
	}
	return filepath.Join(home, "worktrees", s.Project, s.ID)
}

// agentHistory is one agent's finished sessions on its own Scale: whole-session
// weights, and the per-active-hour averages of those active long enough.
type agentHistory struct{ weights, rates []float64 }

// history is what a live session is ranked against, per agent: every FINISHED
// session in the ledger (live ones would rank against themselves, and a fleet
// spawned together would push each other down), never lola's helpers.
func history(l *usage.Ledger, snap []session.Session) map[string]agentHistory {
	liveIDs := make(map[string]bool, len(snap))
	for _, s := range snap {
		liveIDs[s.ID] = true
	}
	out := map[string]agentHistory{}
	for src, e := range l.BySource() {
		agent := e.Agent
		if agent == "" {
			agent = "claude"
		}
		sc := usage.ScaleFor(agent)
		w := sc.Weight(e.Totals)
		if src == helperSource || liveIDs[src] || w <= 0 {
			continue
		}
		h := out[agent]
		h.weights = append(h.weights, w)
		if r := usage.HourlyRate(e.Totals, sc); r > 0 {
			h.rates = append(h.rates, r)
		}
		out[agent] = h
	}
	return out
}

// sampleLoad reads the machine and records the sample for cmd=status,
// returning the hold reason ("" = not busy, or nothing known).
func (d *Daemon) sampleLoad(ctx context.Context, lim config.LoadConfig) string {
	s := d.spend.sample(ctx)
	busy := sysload.Busy(s, sysload.Limits{MaxLoadPerCPU: lim.MaxLoadPerCPU, MinFreeMemoryPercent: lim.MinFreeMemoryPercent})
	d.spend.mu.Lock()
	d.spend.load = &protocol.LoadInfo{Load1: s.Load1, CPUs: s.CPUs, FreeMemPercent: s.FreeMemPercent, Busy: busy}
	d.spend.mu.Unlock()
	return busy
}

// budgetLimit is one configured limit and what has been used against it, both
// in weighted tokens (usage.Totals.Weighted).
type budgetLimit struct {
	project string // "" = the global limit
	budget  int64
	used    int64
}

func (b budgetLimit) reached() bool { return b.budget > 0 && b.used >= b.budget }

func (b budgetLimit) message() string {
	key, what := "budget.daily_tokens", "daily token budget"
	if b.project != "" {
		key, what = "daily_budget_tokens", "project daily token budget"
	}
	return fmt.Sprintf("%s reached: %s of %s weighted tokens used today (%s); new dispatch resumes tomorrow or when the limit is raised",
		what, fmtTokens(b.used), fmtTokens(b.budget), key)
}

// fmtTokens renders a token count as 950, 12.3k, 4.5M.
func fmtTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// budgetLimits reports the global limit and every project limit for today.
func (d *Daemon) budgetLimits(today string) []budgetLimit {
	d.mu.Lock()
	global := d.cfg.Budget.DailyTokens
	projects := map[string]int64{}
	for _, p := range d.cfg.Projects {
		if p.DailyBudgetTokens > 0 {
			projects[p.Name] = p.DailyBudgetTokens
		}
	}
	d.mu.Unlock()

	d.spend.mu.Lock()
	all, by := d.spend.ledger.Day(today)
	d.spend.mu.Unlock()

	out := []budgetLimit{{budget: global, used: all.Weighted()}}
	for name, b := range projects {
		out = append(out, budgetLimit{project: name, budget: b, used: by[name].Weighted()})
	}
	return out
}

// dispatchHold is the spend + load gate a tick runs right after the runtime
// health gate, with the same contract: a non-empty answer skips the tick and
// becomes the poll's LastError, and nothing is mutated. It names the reason,
// the number and the config key, because "dispatch is held" with no why is
// exactly the silent failure the status line exists to prevent.
func (d *Daemon) dispatchHold(ctx context.Context, project string, now time.Time) string {
	for _, b := range d.budgetLimits(now.Format(usage.DayFormat)) {
		if (b.project == "" || b.project == project) && b.reached() {
			return b.message()
		}
	}
	d.mu.Lock()
	lim := d.cfg.Load
	d.mu.Unlock()
	if lim.Enabled() {
		return d.sampleLoad(ctx, lim)
	}
	return ""
}

// notifyBudgets fires one notification per limit per day once it is reached
// (only with [budget].notify). Remembered in memory: a restart may repeat the
// day's notice once, which is better than persisting a flag nobody reads.
func (d *Daemon) notifyBudgets(ctx context.Context, today string) {
	d.mu.Lock()
	on := d.cfg.Budget.Notify
	notifier := d.notifier
	d.mu.Unlock()
	if !on || notifier == nil {
		return
	}
	for _, b := range d.budgetLimits(today) {
		if !b.reached() {
			continue
		}
		d.spend.mu.Lock()
		fired := d.spend.notified[b.project] == today
		d.spend.notified[b.project] = today
		d.spend.mu.Unlock()
		if fired {
			continue
		}
		title := "Daily token budget reached"
		if b.project != "" {
			title = "Daily token budget reached: " + d.displayName(b.project)
		}
		notifier.Notify(ctx, notify.Note{Title: title, Body: b.message(), Priority: notify.Action})
		d.logf(b.project, "%s", b.message())
	}
}

// displayName renders a project for a human ([[project]].label when set).
func (d *Daemon) displayName(name string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg.DisplayNameFor(name)
}

// usageStatus is cmd=status's Usage block.
func (d *Daemon) usageStatus(now time.Time) *protocol.UsageStatus {
	today := now.Format(usage.DayFormat)
	limits := d.budgetLimits(today)
	d.spend.mu.Lock()
	all, by := d.spend.ledger.Day(today)
	var load *protocol.LoadInfo
	if d.spend.load != nil {
		l := *d.spend.load
		load = &l
	}
	quotas := append([]protocol.QuotaInfo(nil), d.spend.quotas...)
	d.spend.mu.Unlock()

	us := &protocol.UsageStatus{Day: today, Tokens: all.Tokens(), Weighted: all.Weighted(), TodayUSD: all.CostUSD, Load: load, Quotas: quotas}
	budgets := map[string]int64{}
	for _, b := range limits {
		if b.project == "" {
			us.BudgetTokens = b.budget
		} else {
			budgets[b.project] = b.budget
		}
	}
	for name, t := range by {
		us.Projects = append(us.Projects, protocol.ProjectSpend{Name: name, Tokens: t.Tokens(), Weighted: t.Weighted(), BudgetTokens: budgets[name]})
		delete(budgets, name)
	}
	for name, b := range budgets { // a limited project that used nothing yet
		us.Projects = append(us.Projects, protocol.ProjectSpend{Name: name, BudgetTokens: b})
	}
	sort.Slice(us.Projects, func(i, j int) bool { return us.Projects[i].Name < us.Projects[j].Name })
	return us
}

// sessionUsage is the session's last scanned spend, nil when nothing is known.
func (d *Daemon) sessionUsage(id string) *protocol.UsageInfo {
	d.spend.mu.Lock()
	defer d.spend.mu.Unlock()
	if u, ok := d.spend.bySession[id]; ok {
		return &u
	}
	return nil
}
