package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/notify"
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
// so one repository gets one pull request at a time. A pull request in
// review holds the repository even when its agent has ended: the work is
// open until the PR merges or closes.
func (d *Daemon) hubRepoBusy(repo string) bool {
	for _, s := range d.sessions.Snapshot() {
		live := hubLive(s)
		for _, w := range s.HubWorks() {
			if w.Open() && (live || w.InReview || w.Progress != "") &&
				w.Kind == string(hubproto.KindDepAdvisory) && strings.EqualFold(s.Repo, repo) {
				return true
			}
		}
	}
	return false
}

// hubNewGroup is a fresh fail group id (hubproto.ValidFailGroup): random, so
// it says nothing about this machine or the session.
func hubNewGroup() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// hubClaimGroup claims several dependency advisories of one repository for
// ONE session. It runs every gate of hubClaimItem per item — an item a gate
// refuses is simply left out and stays on offer — then re-reads each from
// GitHub, claims each lease, and spawns one agent with GroupBriefing. A
// single item takes hubClaimItem's path unchanged.
func (d *Daemon) hubClaimGroup(ctx context.Context, api hubAPI, items []hubproto.Item, snap hubSnapshot, req hubClaimReq) (string, error) {
	id, _, err := d.hubClaimGroupFull(ctx, api, items, snap, req)
	return id, err
}

// hubClaimGroupItems is hubClaimGroup for the loop: the item ids it claimed.
func (d *Daemon) hubClaimGroupItems(ctx context.Context, api hubAPI, items []hubproto.Item, snap hubSnapshot, req hubClaimReq) ([]string, error) {
	_, claimed, err := d.hubClaimGroupFull(ctx, api, items, snap, req)
	return claimed, err
}

func (d *Daemon) hubClaimGroupFull(ctx context.Context, api hubAPI, items []hubproto.Item, snap hubSnapshot, req hubClaimReq) (string, []string, error) {
	single := func(it hubproto.Item) (string, []string, error) {
		id, err := d.hubClaimItem(ctx, api, it, snap, req)
		if err != nil {
			return "", nil, err
		}
		return id, []string{it.ID}, nil
	}
	if len(items) == 1 {
		return single(items[0])
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
	firstWhy := ""
	note := func(why string) {
		if firstWhy == "" {
			firstWhy = why
		}
	}
	for _, it := range items {
		switch {
		case !hubproto.ValidItemID(it.ID) || hubproto.Validate(it) != nil || it.Kind != hubproto.KindDepAdvisory:
			note(hub.ReasonInvalidItem)
			continue
		case len(cands) > 0 && it.DepAdvisory.Repo != cands[0].DepAdvisory.Repo:
			note(hub.ReasonRepoMismatch)
			continue
		case working[it.ID]:
			note(hub.ReasonWorking)
			continue
		case !req.human && d.hubCoolingDown(it.ID, now):
			note(hub.ReasonCooldown)
			continue
		case !d.hubBeginClaim(it.ID):
			note(hub.ReasonInFlight)
			continue
		}
		defer d.hubEndClaim(it.ID)
		cands = append(cands, it)
	}
	if len(cands) == 0 {
		if firstWhy == "" {
			firstWhy = hub.ReasonOther
		}
		return "", nil, hubRefuse(firstWhy, "no advisory left to claim")
	}
	if len(cands) == 1 {
		d.hubEndClaim(cands[0].ID) // hubClaimItem takes its own
		return single(cands[0])
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
	var kind agent.Kind
	if pp != nil {
		p = *pp
		p.PostCreate = slices.Clone(p.PostCreate)
		p.Symlinks = slices.Clone(p.Symlinks)
		p.Env = maps.Clone(p.Env)
		projectCap = d.cfg.EffectiveCap(pp)
		kind = agent.Parse(d.cfg.AgentForProject(p.Name))
		agentBin = sandboxedBin(d.cfg, p.Name, kind.Binary())
		prefix = d.cfg.BranchPrefixForProject(p.Name)
	}
	nat := d.native
	health := d.runtimeHealth
	d.mu.Unlock()
	if pickErr != nil {
		return "", nil, pickErr
	}
	var accProject *config.Project
	if pp != nil {
		accProject = &p
	}

	// 3. The acceptance rules per item. The daily cap counts sessions, so a
	// group is charged once, like one claim.
	claimsToday := d.hubClaimsToday(now)
	var accepted []hubproto.Item
	firstWhy = ""
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
		return "", nil, hubRefuse(firstWhy, "")
	}

	branch := prefix + "hub-advisories-" + now.Local().Format("20060102-1504")
	id := runtime.ManualSessionID(p.Name, branch)
	if _, exists := d.sessions.Get(id); exists {
		return "", nil, hubRefuse(hub.ReasonBranchTaken, "a session already works "+branch)
	}
	if err := d.hubGates(ctx, p, kind, agentBin, nat, health, now); err != nil {
		return "", nil, err
	}

	// 4. One slot, taken before GitHub is asked (a capped machine spends no
	// gh call), then GitHub's answer per item: the unconfirmed are left out.
	pCap, gCap := projectCap, snap.globalCap
	if req.human {
		pCap, gCap = noProjectCap, math.MaxInt
	}
	release, fits := d.reserveSlots(p.Name, 1, pCap, gCap)
	if !fits {
		return "", nil, hubRefuse(hub.ReasonCapped, "")
	}
	defer release()
	if d.hub.verify == nil {
		return "", nil, hubRefuse(hub.ReasonNotConfirmed, "cannot verify the offers on GitHub")
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
		return "", nil, hubRefuse(hub.ReasonNotConfirmed, "GitHub confirms none of them")
	}
	brief, err := hub.GroupBriefing(confirmed)
	if err != nil {
		return "", nil, hubRefuse(hub.ReasonInvalidItem, "")
	}

	// 5. One count, then a lease per item. Every item of the group carries
	// one fail group id, so the hub escalates their failure as one issue.
	if !req.human {
		if err := d.hubCountClaim(now, 1); err != nil {
			return "", nil, hubRefuse(hub.ReasonPersist, "persist claim count: "+err.Error())
		}
	}
	group := hubNewGroup()
	var works []session.HubWork
	var claimedItems []hubproto.Item
	var claimedIDs []string
	for _, it := range confirmed {
		lease, err := api.Claim(ctx, it.ID)
		if err != nil {
			d.logf("", "hub: claim of %s refused: %v", it.ID, err)
			continue
		}
		d.hubNoteLease(lease, now)
		d.hubForgetClaims(it.ID, now)
		works = append(works, session.HubWork{ItemID: it.ID, Kind: string(it.Kind), ClaimedAt: now, LeaseExpires: lease.Expires, Label: hubLabel(it), Group: group})
		claimedItems = append(claimedItems, it)
		claimedIDs = append(claimedIDs, it.ID)
	}
	if len(works) == 0 {
		if !req.human {
			if rerr := d.hubCountClaim(now, -1); rerr != nil {
				d.logf("", "hub: refund claim count: %v", rerr)
			}
		}
		return "", nil, hubRefuse(hub.ReasonClaimRefused, "claim refused")
	}
	if len(claimedItems) != len(confirmed) {
		// The briefing must name exactly what this session holds.
		if brief, err = hub.GroupBriefing(claimedItems); err != nil {
			return "", nil, hubRefuse(hub.ReasonInvalidItem, "")
		}
	}

	brief = withScope(brief, req.scope)

	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	sess, err := nat.OpenManualAgent(cctx, p, id, branch, "", brief, "")
	cancel()
	if err != nil {
		d.hubSpawnFailed(ctx, api, claimedIDs, now, group, err)
		return "", nil, fmt.Errorf("%w: %v", errHubSpawn, err)
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
	if !req.human {
		d.hubNotify(ctx, "Hub chore claimed", fmt.Sprintf("Session %s works %d advisories.", id, len(works)), notify.Info)
	}
	return id, claimedIDs, nil
}
