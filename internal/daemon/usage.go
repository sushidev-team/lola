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
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sysload"
	"github.com/sushidev-team/lola/internal/usage"
)

// Spend + load: the ESTIMATED cost of what lola's agents run (internal/usage),
// the daily budgets that hold dispatch once it is reached, and the [load] hold
// on a machine that is already saturated. All three only ever decide whether a
// tick may START something; nothing here touches a live session.
//
// The usage pass runs on its own minute loop rather than inside the observer:
// a first scan of a long session reads megabytes of transcript, and the
// observer's tmux/gh cadence must never wait on that. It is local file I/O
// only — no exec — and every later pass reads just the bytes appended since.

const (
	// usageInterval paces the spend scan. A budget is therefore enforced up to
	// one interval late, which at list prices is cents, not dollars.
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
	if s.Source != "native" || s.Project == "" || s.ID == "" {
		return ""
	}
	return usage.SlugDir(root, filepath.Join(home, "worktrees", s.Project, s.ID))
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

	sp.mu.Lock()
	changed := false
	keep := map[string]bool{}
	infos := make(map[string]protocol.UsageInfo, len(snap))
	scan := func(dir, source, project string) map[string]usage.Totals {
		if dir == "" || keep[dir] {
			return nil
		}
		keep[dir] = true
		days, err := sp.scanner.ScanDir(dir)
		if err != nil {
			d.logf("", "usage: scan %s: %v", source, err)
		}
		for day, t := range days {
			if sp.ledger.Set(day, source, project, t) {
				changed = true
			}
		}
		return days
	}
	for _, s := range snap {
		days := scan(usageDir(sp.root, d.home, s), s.ID, s.Project)
		if len(days) == 0 {
			continue
		}
		total := usage.Sum(days)
		infos[s.ID] = protocol.UsageInfo{TotalUSD: total.CostUSD, TodayUSD: days[today].CostUSD, Tokens: total.Tokens()}
	}
	scan(usage.SlugDir(sp.root, sp.helpers), helperSource, "")
	sp.scanner.Retain(keep)
	if sp.ledger.Prune(now, usage.KeepDays) {
		changed = true
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

// budgetLimit is one configured limit and what has been spent against it.
type budgetLimit struct {
	project string // "" = the global limit
	budget  float64
	spent   float64
}

func (b budgetLimit) reached() bool { return b.budget > 0 && b.spent >= b.budget }

func (b budgetLimit) message() string {
	if b.project == "" {
		return fmt.Sprintf("daily budget reached: ~$%.2f of $%.2f spent today (budget.daily_usd); new dispatch resumes tomorrow or when the limit is raised", b.spent, b.budget)
	}
	return fmt.Sprintf("project daily budget reached: ~$%.2f of $%.2f spent today (daily_budget_usd); new dispatch resumes tomorrow or when the limit is raised", b.spent, b.budget)
}

// budgetLimits reports the global limit and every project limit for today.
func (d *Daemon) budgetLimits(today string) []budgetLimit {
	d.mu.Lock()
	global := d.cfg.Budget.DailyUSD
	projects := map[string]float64{}
	for _, p := range d.cfg.Projects {
		if p.DailyBudgetUSD > 0 {
			projects[p.Name] = p.DailyBudgetUSD
		}
	}
	d.mu.Unlock()

	d.spend.mu.Lock()
	all, by := d.spend.ledger.Day(today)
	d.spend.mu.Unlock()

	out := []budgetLimit{{budget: global, spent: all.CostUSD}}
	for name, b := range projects {
		out = append(out, budgetLimit{project: name, budget: b, spent: by[name].CostUSD})
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
		title := "Daily budget reached"
		if b.project != "" {
			title = "Daily budget reached: " + d.displayName(b.project)
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
	d.spend.mu.Unlock()

	us := &protocol.UsageStatus{Day: today, TodayUSD: all.CostUSD, Tokens: all.Tokens(), Load: load}
	budgets := map[string]float64{}
	for _, b := range limits {
		if b.project == "" {
			us.BudgetUSD = b.budget
		} else {
			budgets[b.project] = b.budget
		}
	}
	for name, t := range by {
		us.Projects = append(us.Projects, protocol.ProjectSpend{Name: name, TodayUSD: t.CostUSD, BudgetUSD: budgets[name]})
		delete(budgets, name)
	}
	for name, b := range budgets { // a limited project that spent nothing yet
		us.Projects = append(us.Projects, protocol.ProjectSpend{Name: name, BudgetUSD: b})
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
