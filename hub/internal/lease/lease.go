// Package lease is the PURE work-item state machine: no I/O, no clock of its
// own (every call takes now), no store access beyond the item it is handed.
//
//	open ──claim──▶ claimed ──progress──▶ in_review ──complete──▶ done
//	  ▲                │ │                    │
//	  │◀──release──────┘ ├──complete──▶ done  ├──fail──▶ (as from claimed)
//	  │◀──expiry+grace───┤                    └──idle 14d──▶ open
//	  │                  ├──fail (attempts < escalate_after)──▶ open
//	  │                  ├──fail (attempts ≥ escalate_after)──▶ escalated
//	  │                  └──fail not_reproducible──▶ failed
//	  └──not re-reported (stale)──▶ failed
//
// A critical item is escalated IN PARALLEL with offering it: its Escalation is
// opened when it is first seen, while its state stays open.
//
// A lapsed lease does not hand the item to the next device at once: it is
// held for the previous holder for a grace window (hubproto.LeaseGraceFactor x
// TTL), so a laptop that slept through a renewal gets its own work back.
// Every way a device lets an item go (fail, release, refuse, expiry past
// grace, max lease age) puts that device in a cooldown for that item, so the
// same device is not handed the same chore straight back.
package lease

import (
	"errors"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// Defaults.
const (
	DefaultTTL           = 30 * time.Minute
	DefaultEscalateAfter = 2
	// ReopenAfter is how long a done/failed item must have been closed before
	// a re-reported finding reopens it: a fix may take a day to land
	// (review, merge, the next Dependabot scan), and reopening sooner would
	// hand the same chore to a second teammate.
	ReopenAfter = 24 * time.Hour
)

// Errors.
var (
	// ErrConflict: the item is not open (claimed by someone, or closed), or
	// it is held for another device's grace window.
	ErrConflict = errors.New("lease: item not open")
	// ErrLeaseLost: the caller does not hold a live lease on the item (nor
	// own it in review).
	ErrLeaseLost = errors.New("lease: lease not held")
	// ErrRevoked: the device was revoked; it can do nothing.
	ErrRevoked = errors.New("lease: device revoked")
	// ErrWrongOrg: the item belongs to another org.
	ErrWrongOrg = errors.New("lease: item in another org")
	// ErrMaxAge: the claim outlived the max lease age. The item was let go
	// (a timeout attempt is counted) and the caller lost it.
	ErrMaxAge = errors.New("lease: max lease age reached")
)

// actor checks that dev may act on w at all.
func actor(w *store.WorkItem, dev *store.Device) error {
	if dev == nil || dev.Revoked() {
		return ErrRevoked
	}
	if dev.Org != w.Org {
		return ErrWrongOrg
	}
	return nil
}

// Escalation reasons.
const (
	ReasonCritical = "critical"
	ReasonAttempts = "attempts"
)

// Config tunes the machine.
type Config struct {
	TTL           time.Duration // lease length; <=0 means DefaultTTL
	EscalateAfter int           // failed attempts before escalating; <=0 means default
	MaxLeaseAge   time.Duration // longest one claim may be renewed; <=0 means hubproto.DefaultMaxLeaseAge
}

func (c Config) ttl() time.Duration {
	if c.TTL <= 0 {
		return DefaultTTL
	}
	return c.TTL
}

// Grace is how long a lapsed lease's item is held for its previous holder.
func (c Config) Grace() time.Duration { return hubproto.LeaseGraceFactor * c.ttl() }

func (c Config) escalateAfter() int {
	if c.EscalateAfter <= 0 {
		return DefaultEscalateAfter
	}
	return c.EscalateAfter
}

func (c Config) maxLeaseAge() time.Duration {
	if c.MaxLeaseAge <= 0 {
		return hubproto.DefaultMaxLeaseAge
	}
	return c.MaxLeaseAge
}

// entry builds a history entry done by dev (nil = the hub).
func entry(dev *store.Device, action hubproto.AttemptAction, reason hubproto.FailReason, pr string, now time.Time) store.Attempt {
	a := store.Attempt{Action: action, Reason: reason, PRURL: pr, At: now.UTC()}
	if dev != nil {
		a.Device, a.DeviceName, a.Member = dev.ID, dev.Name, dev.Member
	}
	return a
}

// coolDown keeps w from being offered to device for hubproto.DeviceCooldown.
func coolDown(w *store.WorkItem, device string, now time.Time) {
	w.CoolDown(device, now, now.Add(hubproto.DeviceCooldown))
}

// Expire turns a claimed item whose lease has passed into an open item HELD
// for its previous holder until the grace window (counted from the lease's
// expiry) ends. It reports whether it changed w. An expiry is not a failure:
// it does not count an attempt.
func Expire(w *store.WorkItem, now time.Time, cfg Config) bool {
	if w.State != hubproto.StateClaimed || (w.Lease != nil && now.Before(w.Lease.Expires)) {
		return false
	}
	if w.Lease != nil && w.Lease.Device != "" {
		w.Grace = &store.Grace{Device: w.Lease.Device, Until: w.Lease.Expires.Add(cfg.Grace()).UTC()}
	}
	w.State = hubproto.StateOpen
	w.Lease = nil
	return true
}

// Tick applies the clock to w: Expire, then EndGrace (a lease that lapsed
// longer than the grace window ago goes through both at once).
func Tick(w *store.WorkItem, now time.Time, cfg Config) (lapsed, graceEnded bool) {
	lapsed = Expire(w, now, cfg)
	graceEnded = EndGrace(w, now)
	return lapsed, graceEnded
}

// Due reports whether Tick would change w at now.
func Due(w *store.WorkItem, now time.Time) bool {
	if w.State == hubproto.StateClaimed && (w.Lease == nil || !now.Before(w.Lease.Expires)) {
		return true
	}
	return w.Grace != nil && !now.Before(w.Grace.Until)
}

// EndGrace releases an item whose grace window ran out: the previous holder
// gets an "expired" history entry and the cooldown. It reports whether it
// changed w.
func EndGrace(w *store.WorkItem, now time.Time) bool {
	g := w.Grace
	if g == nil || now.Before(g.Until) {
		return false
	}
	w.Grace = nil
	if w.State == hubproto.StateOpen {
		w.ClaimedAt = time.Time{}
		w.AddHistory(store.Attempt{Device: g.Device, Action: hubproto.AttemptExpired, At: now.UTC()})
		coolDown(w, g.Device, now)
	}
	return true
}

// HeldForOther reports whether w is in a grace window held for a device other
// than device.
func HeldForOther(w *store.WorkItem, device string, now time.Time) bool {
	return w.Grace != nil && w.Grace.Device != device && now.Before(w.Grace.Until)
}

// takeBack re-leases an item held in dev's grace window. The claim keeps its
// ClaimedAt: it is the same claim, not a new attempt.
func takeBack(w *store.WorkItem, dev *store.Device, now time.Time, cfg Config) bool {
	if w.State != hubproto.StateOpen || w.Grace == nil || w.Grace.Device != dev.ID || !now.Before(w.Grace.Until) {
		return false
	}
	if w.ClaimedAt.IsZero() {
		w.ClaimedAt = now.UTC()
	}
	w.Grace = nil
	w.State = hubproto.StateClaimed
	w.Lease = &hubproto.Lease{ItemID: w.ID, Device: dev.ID, Expires: now.Add(cfg.ttl()).UTC(), ClaimedAt: w.ClaimedAt}
	return true
}

// holds checks that dev holds a live lease on w (after expiry; a lease in
// dev's own grace window is taken back first).
func holds(w *store.WorkItem, dev *store.Device, now time.Time, cfg Config) error {
	if err := actor(w, dev); err != nil {
		return err
	}
	Tick(w, now, cfg)
	takeBack(w, dev, now, cfg)
	if w.State == hubproto.StateClaimed && w.Lease != nil && w.Lease.Device == dev.ID {
		return nil
	}
	return ErrLeaseLost
}

// holdsOrOwns is holds, or dev owns w in review.
func holdsOrOwns(w *store.WorkItem, dev *store.Device, now time.Time, cfg Config) error {
	err := holds(w, dev, now, cfg)
	if errors.Is(err, ErrLeaseLost) && w.State == hubproto.StateInReview && w.Owner == dev.ID {
		return nil
	}
	return err
}

// overAge reports whether w's claim has outlived the max lease age.
func overAge(w *store.WorkItem, now time.Time, cfg Config) bool {
	return !w.ClaimedAt.IsZero() && now.Sub(w.ClaimedAt) >= cfg.maxLeaseAge()
}

// Claim leases an open item to device. The previous holder's claim inside its
// grace window takes the item back as the same claim; anybody else's is a
// conflict until the window ends. A take-back past the max lease age times
// out instead (ErrMaxAge, the item changed).
func Claim(w *store.WorkItem, dev *store.Device, now time.Time, cfg Config) (hubproto.Lease, error) {
	if err := actor(w, dev); err != nil {
		return hubproto.Lease{}, err
	}
	Tick(w, now, cfg)
	if takeBack(w, dev, now, cfg) {
		if overAge(w, now, cfg) {
			timeout(w, dev, now, cfg)
			return hubproto.Lease{}, ErrMaxAge
		}
		return *w.Lease, nil
	}
	if w.State != hubproto.StateOpen || HeldForOther(w, dev.ID, now) {
		return hubproto.Lease{}, ErrConflict
	}
	at := now.UTC()
	l := hubproto.Lease{ItemID: w.ID, Device: dev.ID, Expires: now.Add(cfg.ttl()).UTC(), ClaimedAt: at}
	w.State = hubproto.StateClaimed
	w.Lease = &l
	w.ClaimedAt = at
	w.Grace = nil
	w.AddHistory(entry(dev, hubproto.AttemptClaimed, "", "", now))
	return l, nil
}

// Renew extends device's live lease on w by one TTL from now; inside its
// grace window it takes the item back. Past the max lease age it refuses with
// ErrMaxAge and counts a timeout attempt (the item changed).
func Renew(w *store.WorkItem, dev *store.Device, now time.Time, cfg Config) (hubproto.Lease, error) {
	if err := holds(w, dev, now, cfg); err != nil {
		return hubproto.Lease{}, err
	}
	if w.ClaimedAt.IsZero() {
		// A lease stored before claimed_at existed: its clock starts now.
		w.ClaimedAt = now.UTC()
	}
	if overAge(w, now, cfg) {
		timeout(w, dev, now, cfg)
		return hubproto.Lease{}, ErrMaxAge
	}
	w.Lease.Expires = now.Add(cfg.ttl()).UTC()
	w.Lease.ClaimedAt = w.ClaimedAt
	return *w.Lease, nil
}

// timeout lets go of a claim that outlived the max lease age: a timeout
// attempt, the cooldown, then open or escalated like any failure.
func timeout(w *store.WorkItem, dev *store.Device, now time.Time, cfg Config) {
	w.Lease, w.Grace = nil, nil
	w.ClaimedAt = time.Time{}
	w.LastFail = hubproto.FailTimeout
	w.AddHistory(entry(dev, hubproto.AttemptExpired, hubproto.FailTimeout, "", now))
	coolDown(w, dev.ID, now)
	w.Attempts++
	reopenOrEscalate(w, now, cfg)
}

// reopenOrEscalate ends a counted attempt: open again below escalate_after,
// escalated (no longer offered) at it.
func reopenOrEscalate(w *store.WorkItem, now time.Time, cfg Config) bool {
	if w.Attempts < cfg.escalateAfter() {
		w.State = hubproto.StateOpen
		w.Owner = ""
		return false
	}
	w.State = hubproto.StateEscalated
	return OpenEscalation(w, ReasonAttempts, now)
}

// Progress records the PR dev opened for w. From claimed (dev holds the
// lease) the item moves to in_review: the lease is dropped and dev becomes
// the owner. In review, the owner's call updates the outcome and resets the
// idle clock. Anybody else gets ErrLeaseLost.
func Progress(w *store.WorkItem, dev *store.Device, out store.Outcome, now time.Time, cfg Config) error {
	if err := holdsOrOwns(w, dev, now, cfg); err != nil {
		return err
	}
	out.Device, out.At = dev.ID, now.UTC()
	if w.State == hubproto.StateClaimed {
		w.State = hubproto.StateInReview
		w.Lease = nil
		w.Owner = dev.ID
		w.AddHistory(entry(dev, hubproto.AttemptInReview, "", out.PRURL, now))
	} else if w.Outcome == nil || w.Outcome.PRURL != out.PRURL {
		// A new PR for the same item is worth a line; a delivery update is not.
		w.AddHistory(entry(dev, hubproto.AttemptInReview, "", out.PRURL, now))
	}
	w.Outcome = &out
	w.ProgressAt = now.UTC()
	return nil
}

// Complete closes w as done with the reported outcome: the lease holder from
// claimed, or the owner from in_review.
func Complete(w *store.WorkItem, dev *store.Device, out store.Outcome, now time.Time, cfg Config) error {
	if err := holdsOrOwns(w, dev, now, cfg); err != nil {
		return err
	}
	out.Device, out.At = dev.ID, now.UTC()
	w.State = hubproto.StateDone
	w.Lease = nil
	w.Owner = dev.ID
	w.ClaimedAt = time.Time{}
	w.Outcome = &out
	w.ClosedAt = now.UTC()
	w.AddHistory(entry(dev, hubproto.AttemptCompleted, "", out.PRURL, now))
	return nil
}

// Release hands w back to open without counting an attempt. The device cools
// down on it.
func Release(w *store.WorkItem, dev *store.Device, now time.Time, cfg Config) error {
	if err := holds(w, dev, now, cfg); err != nil {
		return err
	}
	w.State = hubproto.StateOpen
	w.Lease = nil
	w.ClaimedAt = time.Time{}
	w.AddHistory(entry(dev, hubproto.AttemptReleased, "", "", now))
	coolDown(w, dev.ID, now)
	return nil
}

// Fail records a failed attempt by the lease holder (claimed) or the owner
// (in_review); pr is the PR the attempt left behind ("" = none). It reports
// whether this failure opened an escalation. The device cools down on the
// item whatever the reason.
//
//   - refused / sandbox_required are LOCAL policy, not a failed fix: like a
//     release, the item reopens and no attempt is counted.
//   - not_reproducible closes the item as failed (the finding no longer
//     holds); it counts an attempt but never escalates.
//   - anything else counts an attempt; at escalate_after attempts the item
//     goes to escalated (no longer offered), otherwise it reopens.
func Fail(w *store.WorkItem, dev *store.Device, reason hubproto.FailReason, pr string, now time.Time, cfg Config) (bool, error) {
	if err := holdsOrOwns(w, dev, now, cfg); err != nil {
		return false, err
	}
	w.Lease = nil
	w.ClaimedAt = time.Time{}
	w.LastFail = reason
	w.AddHistory(entry(dev, hubproto.AttemptFailed, reason, pr, now))
	coolDown(w, dev.ID, now)
	switch reason {
	case hubproto.FailRefused, hubproto.FailSandbox:
		w.State = hubproto.StateOpen
		w.Owner = ""
		return false, nil
	case hubproto.FailObsolete:
		w.Attempts++
		w.State = hubproto.StateFailed
		w.ClosedAt = now.UTC()
		return false, nil
	}
	w.Attempts++
	return reopenOrEscalate(w, now, cfg), nil
}

// OpenEscalation marks w for its ONE Linear issue. It reports whether it
// opened one. A pending escalation is never opened twice, and neither is a
// sent one within the same episode. But an item whose escalation was sent
// and which went back on offer since (resolved and re-reported, `items
// reopen`, an idle review) — or whose issue the hub already closed — is
// announced again when it escalates again: otherwise it would sit in
// escalated, never offered, with its only issue closed. The new escalation
// is filed under the same stable issue id, so the sender finds the old
// issue and reopens it rather than filing a second.
func OpenEscalation(w *store.WorkItem, reason string, now time.Time) bool {
	if x := w.Escalation; x != nil {
		if x.Pending || (x.Err != store.EscalationClosed && x.Episode == w.Reopened) {
			return false
		}
	}
	w.Escalation = &store.Escalation{Pending: true, Reason: reason, CreatedAt: now.UTC(), Episode: w.Reopened}
	return true
}

// reopen puts a closed or parked item back on offer, recording who-less
// "reopened" in its history.
func reopen(w *store.WorkItem, now time.Time) {
	w.State = hubproto.StateOpen
	w.Lease, w.Grace = nil, nil
	w.Outcome = nil
	w.Owner = ""
	w.ClaimedAt, w.ProgressAt = time.Time{}, time.Time{}
	w.Reopened++
	w.AddHistory(entry(nil, hubproto.AttemptReopened, "", "", now))
}

// Observe applies a re-reported finding to an existing item: it bumps the
// reporter bookkeeping and reopens an item closed (done / failed) at least
// ReopenAfter ago, with its attempts and outcome reset. It reports whether the
// item reopened. Open, claimed, in_review and escalated items keep their
// state: a PR is open for an in_review one, and an escalated item belongs to
// a person now.
func Observe(w *store.WorkItem, device string, now time.Time) bool {
	w.NoteReporter(device, now.UTC())
	if w.State != hubproto.StateDone && w.State != hubproto.StateFailed {
		return false
	}
	if now.Sub(w.ClosedAt) < ReopenAfter {
		return false
	}
	reopen(w, now)
	w.Attempts = 0
	w.LastFail = ""
	w.ClosedAt = time.Time{}
	return true
}

// ReopenIdle reopens an in_review item whose owner made no progress call for
// hubproto.InReviewIdleReopen: the PR was abandoned. It reports whether it
// changed w.
func ReopenIdle(w *store.WorkItem, now time.Time) bool {
	if w.State != hubproto.StateInReview || now.Sub(w.ProgressAt) < hubproto.InReviewIdleReopen {
		return false
	}
	reopen(w, now)
	return true
}

// Stale reports whether an open item has not been re-reported for after.
// Whether some device still reports its repo (so silence means the finding
// is gone) is the caller's to check.
func Stale(w *store.WorkItem, now time.Time, after time.Duration) bool {
	return w.State == hubproto.StateOpen && w.Grace == nil && now.Sub(w.LastSeen) >= after
}

// CloseStale closes an open item nobody reports any more as failed, with a
// "closed_stale" history entry. It counts no attempt. It reports whether it
// changed w.
func CloseStale(w *store.WorkItem, now time.Time) bool {
	if w.State != hubproto.StateOpen || w.Grace != nil {
		return false
	}
	w.State = hubproto.StateFailed
	w.ClosedAt = now.UTC()
	w.AddHistory(entry(nil, hubproto.AttemptClosedStale, hubproto.FailObsolete, "", now))
	return true
}

// Resolve closes an escalated item as done: a person fixed it (admin `items
// resolve`). Anything but escalated is ErrConflict.
func Resolve(w *store.WorkItem, now time.Time) error {
	if w.State != hubproto.StateEscalated {
		return ErrConflict
	}
	w.State = hubproto.StateDone
	w.ClosedAt = now.UTC()
	return nil
}

// Reopen puts an escalated item back on offer with its attempts reset (admin
// `items reopen`). The escalation record stays: the item keeps its one Linear
// issue, and if it escalates again that issue is announced (and reopened)
// again, since reopen starts a new episode. Anything but escalated is
// ErrConflict.
func Reopen(w *store.WorkItem, now time.Time) error {
	if w.State != hubproto.StateEscalated {
		return ErrConflict
	}
	reopen(w, now)
	w.Attempts = 0
	w.LastFail = ""
	w.Cooldown = nil
	return nil
}

// NewItem builds the work item for a first-seen finding. A critical finding
// opens its escalation immediately and is still offered.
func NewItem(id, org string, item hubproto.Item, device string, now time.Time) *store.WorkItem {
	item.ID = id
	w := &store.WorkItem{
		ID: id, Org: org, Key: hubproto.DedupKey(item), Item: item,
		State: hubproto.StateOpen, FirstSeen: now.UTC(),
	}
	w.NoteReporter(device, now.UTC())
	if item.Severity() == hubproto.SeverityCritical {
		OpenEscalation(w, ReasonCritical, now)
	}
	return w
}

// Revoke lets go of everything a revoked device held on w: its lease, its
// grace window, or the item it owned in review (which reopens: nobody is
// left to finish that PR). It reports whether it changed w.
func Revoke(w *store.WorkItem, device string) bool {
	switch {
	case w.State == hubproto.StateClaimed && w.Lease != nil && w.Lease.Device == device:
		w.State = hubproto.StateOpen
		w.Lease = nil
		w.ClaimedAt = time.Time{}
		return true
	case w.State == hubproto.StateOpen && w.Grace != nil && w.Grace.Device == device:
		w.Grace = nil
		w.ClaimedAt = time.Time{}
		return true
	case w.State == hubproto.StateInReview && w.Owner == device:
		w.State = hubproto.StateOpen
		w.Owner = ""
		w.Outcome = nil
		w.ClaimedAt, w.ProgressAt = time.Time{}, time.Time{}
		return true
	}
	return false
}
