package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// prsExecTimeout bounds the single `gh pr list` a cmd=prs miss runs, so a hung
// gh can never wedge the socket handler.
const prsExecTimeout = 10 * time.Second

// prsTTL is how long a fetched open-PR list is served before a refresh.
const prsTTL = 20 * time.Second

// prCache memoizes ListOpenPRs results per repo behind a per-repo lock: two
// concurrent picker opens (or a rapid re-open) for the same repo serialize on
// that lock, so the second sees the just-fetched cache instead of a duplicate
// gh exec (a lightweight singleflight).
type prCache struct {
	mu      sync.Mutex
	entries map[string]*prCacheEntry
}

type prCacheEntry struct {
	mu        sync.Mutex // serializes fetches for this repo
	prs       []scm.OpenPR
	fetchedAt time.Time
}

func newPRCache() *prCache { return &prCache{entries: map[string]*prCacheEntry{}} }

func (c *prCache) entry(repo string) *prCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[repo]
	if !ok {
		e = &prCacheEntry{}
		c.entries[repo] = e
	}
	return e
}

// handlePrs serves cmd=prs: the open PRs for a project's repo, cached behind a
// short TTL. Preconditions fail CLOSED — an unknown project, an unconfigured
// repo, or a gh error is a distinct error, never an empty list masquerading as
// "no open PRs".
func (d *Daemon) handlePrs(ctx context.Context, raw json.RawMessage) (protocol.PrsData, error) {
	var args protocol.PrsArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return protocol.PrsData{}, fmt.Errorf("prs: bad args: %w", err)
		}
	}
	project := strings.TrimSpace(args.Project)
	if project == "" {
		return protocol.PrsData{}, errors.New("prs: project required")
	}

	d.mu.Lock()
	p := d.cfg.ProjectByName(project)
	fetch := d.listOpenPRs
	d.mu.Unlock()
	if p == nil {
		return protocol.PrsData{}, fmt.Errorf("unknown project %q", project)
	}
	if p.Repo == "" {
		return protocol.PrsData{}, fmt.Errorf("project %q has no repo configured (set owner/name to list PRs)", project)
	}
	if fetch == nil {
		return protocol.PrsData{}, errors.New("prs: PR listing unavailable")
	}

	prs, fetchedAt, err := d.cachedOpenPRs(ctx, p.Repo, args.Refresh, fetch)
	if err != nil {
		return protocol.PrsData{}, err
	}

	// Decorate with which branches a live session already holds, so the picker
	// can grey those rows.
	held := map[string]bool{}
	for _, s := range d.sessions.Snapshot() {
		if s.Source == "native" && s.Branch != "" && isLiveSession(s) {
			held[s.Branch] = true
		}
	}

	age := int(time.Since(fetchedAt).Seconds())
	data := protocol.PrsData{
		Repo:       p.Repo,
		AgeSeconds: age,
		Stale:      time.Since(fetchedAt) > prsTTL,
		PRs:        make([]protocol.PrRow, 0, len(prs)),
	}
	for _, pr := range prs {
		data.PRs = append(data.PRs, protocol.PrRow{
			Number:      pr.Number,
			Title:       pr.Title,
			Author:      pr.Author,
			Branch:      pr.Branch,
			IsDraft:     pr.IsDraft,
			IsFork:      pr.IsFork,
			Checks:      pr.Checks,
			Review:      pr.Review,
			URL:         pr.URL,
			Status:      openPRStatus(pr),
			AlreadyOpen: held[pr.Branch],
		})
	}
	return data, nil
}

// openPRStatus derives the picker row's status word from an open PR's facts
// via the shared delivery derivation (scm ships facts only). No hysteresis:
// the picker has no prior state for a PR it is only listing.
func openPRStatus(pr scm.OpenPR) string {
	return string(state.DeriveDelivery(&scm.PR{
		Number:         pr.Number,
		URL:            pr.URL,
		State:          "OPEN",
		IsDraft:        pr.IsDraft,
		Mergeable:      pr.Mergeable,
		ReviewDecision: pr.Review,
		ChecksState:    pr.Checks,
	}, state.DeliveryNone))
}

// cachedOpenPRs returns a repo's open PRs, serving a fresh cache entry when one
// exists and re-execing gh (bounded, shutdown-shielded) on a miss/refresh under
// the per-repo lock.
func (d *Daemon) cachedOpenPRs(ctx context.Context, repo string, refresh bool, fetch func(context.Context, string) ([]scm.OpenPR, error)) ([]scm.OpenPR, time.Time, error) {
	e := d.prsCache.entry(repo)
	e.mu.Lock()
	defer e.mu.Unlock()

	if !refresh && !e.fetchedAt.IsZero() && time.Since(e.fetchedAt) <= prsTTL {
		return e.prs, e.fetchedAt, nil
	}

	// Fetch on a shutdown-shielded context with its own deadline so a wedged gh
	// neither hangs graceful shutdown nor is aborted by a client disconnect.
	base := d.shutdownCtx
	if base == nil {
		base = context.WithoutCancel(ctx)
	}
	cctx, cancel := context.WithTimeout(base, prsExecTimeout)
	defer cancel()

	prs, err := fetch(cctx, repo)
	if err != nil {
		// On a refresh failure with a prior snapshot, serve the stale one so the
		// picker degrades to "stale" rather than blanking; otherwise surface it.
		if !e.fetchedAt.IsZero() {
			return e.prs, e.fetchedAt, nil
		}
		return nil, time.Time{}, fmt.Errorf("list open PRs for %s: %w", repo, err)
	}
	e.prs, e.fetchedAt = prs, time.Now()
	return e.prs, e.fetchedAt, nil
}

// isLiveSession reports whether a session still actively HOLDS its branch (and
// its Linear issue — tickets.go asks the same question), so the PR / ticket
// pickers can grey that row. Terminal states free the claim.
//
// It is state.Present — the reconcile shield's classification, which already
// answers "is an agent still running and was its PR not rejected" — plus the
// merged exclusion the pickers need on top: with [reactions.merged] auto off,
// a merged session's record lingers, and its branch is emphatically not still
// held. Reading the two AXES rather than the collapsed status is what makes it
// honest in the case the pickers actually hit: Rollup ranks a waiting agent
// above every PR state, so a session blocked on a human over a merged or
// closed PR read as live and greyed a row anybody was free to take.
//
// EnsureAxes covers a pre-axis snapshot record. It seeds an unstamped record
// to idle/none, i.e. LIVE — the old string form answered false for an empty
// status, but both call sites already require a branch or an issue UUID, which
// an unstamped record does not have.
func isLiveSession(s session.Session) bool {
	s.EnsureAxes()
	return state.Present(s.AgentState, s.Delivery) && s.Delivery != state.DeliveryMerged
}
