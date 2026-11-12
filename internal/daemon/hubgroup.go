package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
)

// Grouped chores ([hub].group_chores, on by default): a repository's open
// dependency advisories go to ONE agent and one pull request instead of one
// session per advisory. Nothing changes on the wire — each advisory is still
// claimed, leased, renewed and reported on its own (Session.HubItem plus
// Session.HubBatch) — only the session and the briefing are shared.

// hubGroupOffers splits offers into groups of dependency advisories per
// repository (in offer order, at most config.MaxGroupedChores each) and the
// rest, which are claimed one by one.
func hubGroupOffers(items []hubproto.Item, group bool) (groups [][]hubproto.Item, single []hubproto.Item) {
	if !group {
		return nil, items
	}
	byRepo := map[string][]hubproto.Item{}
	var order []string
	for _, it := range items {
		if it.Kind != hubproto.KindDepAdvisory || it.DepAdvisory == nil {
			single = append(single, it)
			continue
		}
		repo := it.DepAdvisory.Repo
		if _, seen := byRepo[repo]; !seen {
			order = append(order, repo)
		}
		byRepo[repo] = append(byRepo[repo], it)
	}
	for _, repo := range order {
		g := byRepo[repo]
		for len(g) > 0 {
			n := min(len(g), config.MaxGroupedChores)
			groups = append(groups, g[:n])
			g = g[n:]
		}
	}
	return groups, single
}

// hubRepoBusy reports whether a hub session on this machine still works an
// advisory of repo: new advisories for it wait for that session to finish,
// so one repository gets one pull request at a time.
func (d *Daemon) hubRepoBusy(repo string) bool {
	for _, s := range d.sessions.Snapshot() {
		if !hubLive(s) {
			continue
		}
		for _, w := range s.HubWorks() {
			if w.Open() && w.Kind == string(hubproto.KindDepAdvisory) && strings.EqualFold(s.Repo, repo) {
				return true
			}
		}
	}
	return false
}

// hubClaimGroup claims several dependency advisories of one repository for
// ONE session. It runs every gate of hubClaimItem per item — an item a gate
// refuses is simply left out and stays on offer — then re-reads each from
// GitHub, claims each lease, and spawns one agent with GroupBriefing. A
// single item takes hubClaimItem's path unchanged.
func (d *Daemon) hubClaimGroup(ctx context.Context, api hubAPI, items []hubproto.Item, snap hubSnapshot, req hubClaimReq) (string, error) {
	if len(items) == 1 {
		return d.hubClaimItem(ctx, api, items[0], snap, req)
	}
	now := d.hub.now()

	// 1. Local gates per item: valid, not worked here, not cooling down, no
	// claim in flight, and every one of the same repository as the first.
	working := map[string]bool{}
	for _, s := range d.sessions.Snapshot() {
		for _, w := range s.HubWorks() {
			working[w.ItemID] = true
		}
	}
	var cands []hubproto.Item
	for _, it := range items {
		switch {
		case !hubproto.ValidItemID(it.ID) || hubproto.Validate(it) != nil || it.Kind != hubproto.KindDepAdvisory:
			continue
		case len(cands) > 0 && it.DepAdvisory.Repo != cands[0].DepAdvisory.Repo:
			continue
		case working[it.ID]:
			continue
		case !req.human && d.hubCoolingDown(it.ID, now):
			continue
		case !d.hubBeginClaim(it.ID):
			continue
		}
		defer d.hubEndClaim(it.ID)
		cands = append(cands, it)
	}
	if len(cands) == 0 {
		return "", errors.New("no advisory left to claim")
	}
	if len(cands) == 1 {
		d.hubEndClaim(cands[0].ID) // hubClaimItem takes its own
		return d.hubClaimItem(ctx, api, cands[0], snap, req)
	}

	// 2. The project, exactly as for one item (they share the repository).
	d.mu.Lock()
	var pp *config.Project
	var pickErr error
	if req.human {
		pp, pickErr = hubPickProject(d.cfg, cands[0], req.project, snap.h)
	} else {
		pp = hub.ProjectFor(d.cfg, cands[0])
	}
	var p config.Project
	projectCap, agentBin, prefix := 0, "", ""
	if pp != nil {
		p = *pp
		p.PostCreate = slices.Clone(p.PostCreate)
		p.Symlinks = slices.Clone(p.Symlinks)
		p.Env = maps.Clone(p.Env)
		projectCap = d.cfg.EffectiveCap(pp)
		agentBin = sandboxedBin(d.cfg, p.Name, agent.Parse(d.cfg.AgentForProject(p.Name)).Binary())
		prefix = d.cfg.BranchPrefixForProject(p.Name)
	}
	nat := d.native
	health := d.runtimeHealth
	d.mu.Unlock()
	if pickErr != nil {
		return "", pickErr
	}
	var accProject *config.Project
	if pp != nil {
		accProject = &p
	}

	// 3. The acceptance rules per item. The daily cap counts sessions, so a
	// group is charged once, like one claim.
	claimsToday := d.hubClaimsToday(now)
	var accepted []hubproto.Item
	firstWhy := ""
	for _, it := range cands {
		var ok bool
		var why string
		if req.human {
			ok, why = hub.AcceptIntent(it, accProject, snap.h)
		} else {
			ok, why = hub.Accept(it, accProject, snap.h, claimsToday)
		}
		if !ok {
			if firstWhy == "" {
				firstWhy = why
			}
			continue
		}
		accepted = append(accepted, it)
	}
	if len(accepted) == 0 {
		return "", errors.New(firstWhy)
	}

	branch := prefix + "hub-advisories-" + now.Local().Format("20060102-1504")
	id := runtime.ManualSessionID(p.Name, branch)
	if _, exists := d.sessions.Get(id); exists {
		return "", errors.New("a session already works " + branch)
	}
	if msg := d.dispatchHold(ctx, p.Name, now); msg != "" {
		return "", errors.New("dispatch held: " + msg)
	}
	if nat == nil {
		return "", errors.New("native runtime unavailable")
	}
	if err := health(agentBin); err != nil {
		return "", errors.New("runtime unavailable: " + err.Error())
	}

	// 4. GitHub's answer is the fact, per item: the unconfirmed are left out.
	if d.hub.verify == nil {
		return "", errors.New("cannot verify the offers on GitHub")
	}
	var confirmed []hubproto.Item
	for _, it := range accepted {
		v, err := d.hub.verify(ctx, it)
		if err != nil || v.Item.ID != it.ID || v.Item.Kind != it.Kind || hubproto.Validate(v.Item) != nil {
			d.logf("", "hub: not grouping %s: GitHub does not confirm it", it.ID)
			continue
		}
		confirmed = append(confirmed, v.Item)
	}
	if len(confirmed) == 0 {
		return "", errors.New("GitHub confirms none of them")
	}
	brief, err := hub.GroupBriefing(confirmed)
	if err != nil {
		return "", errors.New(hub.ReasonInvalidItem)
	}

	// 5. One slot, one count, then a lease per item.
	pCap, gCap := projectCap, snap.globalCap
	if req.human {
		pCap, gCap = noProjectCap, math.MaxInt
	}
	release, fits := d.reserveSlots(p.Name, 1, pCap, gCap)
	if !fits {
		return "", errors.New("capped")
	}
	defer release()
	if !req.human {
		if err := d.hubCountClaim(now, 1); err != nil {
			return "", errors.New("persist claim count: " + err.Error())
		}
	}
	var works []session.HubWork
	var claimedItems []hubproto.Item
	for _, it := range confirmed {
		lease, err := api.Claim(ctx, it.ID)
		if err != nil {
			d.logf("", "hub: claim of %s refused: %v", it.ID, err)
			continue
		}
		d.hubForgetClaims(it.ID, now)
		works = append(works, session.HubWork{ItemID: it.ID, Kind: string(it.Kind), ClaimedAt: now, LeaseExpires: lease.Expires, Label: hubLabel(it)})
		claimedItems = append(claimedItems, it)
	}
	if len(works) == 0 {
		if !req.human {
			if rerr := d.hubCountClaim(now, -1); rerr != nil {
				d.logf("", "hub: refund claim count: %v", rerr)
			}
		}
		return "", errors.New("claim refused")
	}
	if len(claimedItems) != len(confirmed) {
		// The briefing must name exactly what this session holds.
		if brief, err = hub.GroupBriefing(claimedItems); err != nil {
			return "", errors.New(hub.ReasonInvalidItem)
		}
	}

	brief = withScope(brief, req.scope)

	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	sess, err := nat.OpenManualAgent(cctx, p, id, branch, "", brief, "")
	cancel()
	if err != nil {
		d.logf("", "hub: spawn for %d grouped items failed, releasing them: %v", len(works), err)
		for _, w := range works {
			d.hubEnqueue(hubOutcome{ItemID: w.ItemID, ClaimedAt: now, Action: hubActRelease, Since: now})
		}
		d.hubOutcomes(ctx, api)
		return "", fmt.Errorf("%w: %v", errHubSpawn, err)
	}
	sess.Repo = p.Repo
	sess.Title = fmt.Sprintf("hub: %d advisories in %s", len(works), claimedItems[0].DepAdvisory.Repo)
	sess.HubItem = works[0]
	sess.HubBatch = works[1:]
	d.sessions.Upsert(sess)
	d.recordSessionEvent("", sess)
	if err := d.sessions.Save(); err != nil {
		d.logf("", "hub: persist sessions after spawning %s: %v", id, err)
	}
	who := "the hub loop"
	if req.human {
		who = "a person"
	}
	d.logf("", "hub: claimed %d advisories of %s for %s, session %s (%s)", len(works), claimedItems[0].DepAdvisory.Repo, p.Name, id, who)
	return id, nil
}
