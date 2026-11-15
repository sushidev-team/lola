package remote

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
)

// Pairing limits. The state names and reasons live in internal/protocol.
const (
	PairSecretTTL         = 5 * time.Minute
	PairConfirmTTL        = 2 * time.Minute
	PairTerminalRetention = 60 * time.Second
	// PairStatusMaxWait clamps pairStatus's WaitMs; the daemon clamps with it.
	PairStatusMaxWait = 25 * time.Second

	pairSecretBytes = 32
	pairIDBytes     = 8
	// A wrong proof changes no pairing state: with 32 random bytes it teaches
	// the sender nothing, and any peer that can reach the port may send one, so
	// letting wrong proofs burn the secret or fail the pairing would let anyone
	// on the network cancel every pairing. They are counted for the audit line,
	// and badPairProofsAlert-th one of a pairing writes one line that says so.
	badPairProofsAlert = 5
	// pairFirstFrameTimeout bounds the wait for a candidate's remote.pair. The
	// phone sends it straight after the TLS handshake; a peer that sends
	// nothing holds its pre-auth slot for this long, not for handshakeTimeout.
	pairFirstFrameTimeout = 3 * time.Second
	// maxConfirmTries typed codes fail a pairing (a blind guess wins 3e-6).
	maxConfirmTries = 3
	// maxUnpairedConns candidates (all sources) at once while a pairing is open.
	// A slot is taken only once a well-formed remote.pair has arrived, so a
	// silent connection never holds one.
	maxUnpairedConns = 4
	// maxUnpairedPerSource candidates per source key; equals the phone's own
	// PAIR_MAX_PARALLEL so its pairing race never trips it.
	maxUnpairedPerSource = 2
	// maxPlatformRunes bounds the platform string shown in a status.
	maxPlatformRunes = 32

	pairProofLabel = "EXPORTER-lola-pair-proof-v1"
	pairCodeLabel  = "EXPORTER-lola-pair-code-v1"
)

// Ticket is what Begin hands the daemon to build the QR from. Secret is a copy;
// the Pairer keeps its own and zeroes it on the burn.
type Ticket struct {
	ID        string
	Secret    []byte
	ExpiresAt time.Time
}

// ConfirmRelay is the device's long-lived relay credential, minted by the
// daemon before Confirm. Bearer is plaintext, sent once in PairResult.Relay and
// never stored; Record holds hashes only and is written with the registry
// entry. nil means this phone gets no relay credential now.
type ConfirmRelay struct {
	Bearer string
	Record devices.DeviceRelay
}

// pairing is one pairing's memory. Nothing about it is persisted.
type pairing struct {
	id      string
	secret  []byte // zeroed on the burn
	state   string
	reason  string
	expires time.Time // deadline of the CURRENT state; zero once terminal
	termAt  time.Time // when it went terminal

	badProofs int
	tries     int

	// relay window (SetWindowRelay), meaningful only while waiting
	relayRV, relayTokHash string

	// set by the presenting connection (waiting -> confirming)
	code       string
	phone      [32]byte
	name       string
	platform   string
	nameClash  bool
	replaces   string
	via        string
	addr       string
	deviceInfo *protocol.DeviceInfo
	result     *protocol.PairResult

	// busy is held while Confirm writes the registry with p.mu released (the
	// registry's subscribers call back into the Pairer, so p.mu must not be held
	// across Add). Expiry and Reject leave a busy pairing alone.
	busy bool

	changed chan struct{} // closed and replaced on every transition
	timer   *time.Timer
}

// Pairer runs one pairing at a time. Every transition is a compare-and-swap on
// the pairing's state under p.mu: an event that finds another state than the one
// it expects changes nothing and refuses only its own connection.
type Pairer struct {
	reg  *devices.Registry
	now  func() time.Time
	logf func(string, ...any)

	mu      sync.Mutex
	open    *pairing            // the non-terminal pairing, if any
	pairs   map[string]*pairing // open + terminal within retention
	host    string              // for PairResult.Host
	daemon  [32]byte            // the daemon's SPKI hash, for the confirmation code
	daemonK bool

	unpaired  int
	perSource map[string]int

	// candLog throttles the lines an unpaired candidate can cause at will (a
	// refusal, a wrong proof): any certificate passes TLS while a window is open.
	candLog *refusalLog
}

// NewPairer returns a Pairer over reg. now and logf may be nil.
func NewPairer(reg *devices.Registry, now func() time.Time, logf func(string, ...any)) *Pairer {
	if now == nil {
		now = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Pairer{reg: reg, now: now, logf: logf, pairs: map[string]*pairing{}, perSource: map[string]int{},
		candLog: newRefusalLog(now())}
}

// SetDaemonSPKI records the daemon's own SPKI hash. The confirmation code
// binds both keys, and the connection's tls.ConnectionState does not expose the
// local certificate, so the listener supplies it. Until it is set no candidate
// can present (fail closed).
func (p *Pairer) SetDaemonSPKI(h [32]byte) {
	p.mu.Lock()
	p.daemon, p.daemonK = h, true
	p.mu.Unlock()
}

func (p *Pairer) setHost(h string) {
	p.mu.Lock()
	p.host = h
	p.mu.Unlock()
}

// Begin opens a pairing, cancelling any open one first. The window carries no
// relay yet (SetWindowRelay).
func (p *Pairer) Begin() (Ticket, error) {
	secret := make([]byte, pairSecretBytes)
	idb := make([]byte, pairIDBytes)
	if _, err := rand.Read(secret); err != nil {
		return Ticket{}, fmt.Errorf("remote: pairing secret: %w", err)
	}
	if _, err := rand.Read(idb); err != nil {
		return Ticket{}, fmt.Errorf("remote: pairing id: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	if o := p.open; o != nil && !o.busy {
		p.endLocked(o, protocol.PairStateCancelled, "")
	} else if o != nil {
		return Ticket{}, errors.New("a confirmation is being written; try again in a moment")
	}
	pg := &pairing{
		id:      hex.EncodeToString(idb),
		secret:  secret,
		state:   protocol.PairStateWaiting,
		expires: p.now().Add(PairSecretTTL),
		changed: make(chan struct{}),
	}
	p.open = pg
	p.pairs[pg.id] = pg
	p.armLocked(pg)
	p.auditLocked(pg, "-")
	return Ticket{ID: pg.id, Secret: slices.Clone(secret), ExpiresAt: pg.expires}, nil
}

// SetWindowRelay records (rv, tokHash) on pairing id while it is waiting; two
// empty strings clear them. It returns false and changes nothing when id is not
// the open, waiting pairing or only one of the two is empty.
func (p *Pairer) SetWindowRelay(id, rv, tokHash string) bool {
	if (rv == "") != (tokHash == "") {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	pg := p.open
	if pg == nil || pg.id != id || pg.state != protocol.PairStateWaiting {
		return false
	}
	pg.relayRV, pg.relayTokHash = rv, tokHash
	return true
}

// Window reports the open window's relay entry; ok is false when no pairing is
// waiting or it carries no relay rendezvous.
func (p *Pairer) Window() (rv, tokHash string, expires time.Time, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	pg := p.open
	if pg == nil || pg.state != protocol.PairStateWaiting || pg.relayRV == "" {
		return "", "", time.Time{}, false
	}
	return pg.relayRV, pg.relayTokHash, pg.expires, true
}

func (p *Pairer) windowOpen() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	return p.open != nil && p.open.state == protocol.PairStateWaiting
}

func (p *Pairer) pending() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	return p.open != nil
}

// windowRV is the open waiting window's relay rv; "" matches no handshake.
func (p *Pairer) windowRV() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	if pg := p.open; pg != nil && pg.state == protocol.PairStateWaiting {
		return pg.relayRV
	}
	return ""
}

// admitUnpaired takes one candidate slot for source. release is idempotent.
func (p *Pairer) admitUnpaired(source string) (release func(), ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unpaired >= maxUnpairedConns || p.perSource[source] >= maxUnpairedPerSource {
		return nil, false
	}
	p.unpaired++
	p.perSource[source]++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.unpaired--
			if p.perSource[source]--; p.perSource[source] <= 0 {
				delete(p.perSource, source)
			}
		})
	}, true
}

// Status reports a pairing. With a Since state and WaitMs it blocks, up to
// PairStatusMaxWait, until the state differs.
func (p *Pairer) Status(ctx context.Context, a protocol.PairStatusArgs) (protocol.PairStatusData, error) {
	wait := time.Duration(a.WaitMs) * time.Millisecond
	if wait < 0 {
		wait = 0
	}
	if wait > PairStatusMaxWait {
		wait = PairStatusMaxWait
	}
	var deadline <-chan time.Time
	if wait > 0 && a.Since != "" {
		t := time.NewTimer(wait)
		defer t.Stop()
		deadline = t.C
	}
	for {
		p.mu.Lock()
		p.sweepLocked()
		pg := p.pairs[a.Pairing]
		if pg == nil {
			p.mu.Unlock()
			return protocol.PairStatusData{}, errors.New("unknown pairing")
		}
		if deadline == nil || pg.state != a.Since {
			d := p.snapshotLocked(pg)
			p.mu.Unlock()
			return d, nil
		}
		ch := pg.changed
		p.mu.Unlock()
		select {
		case <-ch:
		case <-deadline:
			deadline = nil // one more pass returns the current state
		case <-ctx.Done():
			return protocol.PairStatusData{}, ctx.Err()
		}
	}
}

// Confirm checks the code the human typed and registers the device. It returns
// an error, with the state unchanged, for an unknown pairing, a pairing not
// confirming, a wrong code (the maxConfirmTries-th fails the pairing) and bad
// tiers or name; a registry failure fails the pairing.
func (p *Pairer) Confirm(a protocol.PairConfirmArgs, relay *ConfirmRelay) (protocol.PairStatusData, error) {
	p.mu.Lock()
	p.sweepLocked()
	pg := p.pairs[a.Pairing]
	if pg == nil {
		p.mu.Unlock()
		return protocol.PairStatusData{}, errors.New("unknown pairing")
	}
	if pg.state != protocol.PairStateConfirming || pg.busy {
		p.mu.Unlock()
		return protocol.PairStatusData{}, errors.New("not waiting for a confirmation")
	}
	typed := strings.ReplaceAll(a.Code, " ", "")
	if !hmac.Equal([]byte(typed), []byte(pg.code)) {
		pg.tries++
		left := maxConfirmTries - pg.tries
		if left <= 0 {
			p.endLocked(pg, protocol.PairStateFailed, protocol.PairReasonCodeMismatch)
			p.mu.Unlock()
			return protocol.PairStatusData{}, errors.New("the code does not match the phone; 0 tries left, the pairing has failed")
		}
		p.auditLocked(pg, "-")
		p.mu.Unlock()
		return protocol.PairStatusData{}, fmt.Errorf("the code does not match the phone; %d tries left", left)
	}
	tiers := a.Tiers
	if len(tiers) == 0 {
		tiers = protocol.DefaultTiers
	}
	if !knownTiers(tiers) {
		p.mu.Unlock()
		return protocol.PairStatusData{}, devices.ErrBadTiers
	}
	name := pg.name
	if strings.TrimSpace(a.Name) != "" {
		n, ok := cleanName(a.Name)
		if !ok {
			p.mu.Unlock()
			return protocol.PairStatusData{}, devices.ErrBadName
		}
		name = n
	}
	pg.busy = true
	phone, platform, host := pg.phone, pg.platform, p.host
	p.mu.Unlock()

	var rec *devices.DeviceRelay
	if relay != nil {
		r := relay.Record
		rec = &r
	}
	dev, err := p.reg.Add(name, platform, phone, tiers, rec)

	p.mu.Lock()
	defer p.mu.Unlock()
	pg.busy = false
	if err != nil {
		reason := protocol.PairReasonRegistry
		if errors.Is(err, devices.ErrFull) {
			reason = protocol.PairReasonFull
		}
		p.endLocked(pg, protocol.PairStateFailed, reason)
		p.logf("remote: pair pairing=%s registry write failed: %v", pg.id, err)
		return protocol.PairStatusData{}, fmt.Errorf("could not register the device: %w", err)
	}
	pg.deviceInfo = deviceInfo(dev, phone)
	res := &protocol.PairResult{DeviceID: dev.ID, Name: dev.Name, Tiers: slices.Clone(dev.Tiers), Host: host}
	if relay != nil && rec != nil {
		res.Relay = &protocol.PairRelay{RV: rec.RV, Bearer: relay.Bearer}
	}
	pg.result = res
	p.endLocked(pg, protocol.PairStatePaired, "")
	return p.snapshotLocked(pg), nil
}

// Reject cancels a waiting pairing (closing the QR) or rejects a confirming
// one. A pairing already terminal is reported unchanged.
func (p *Pairer) Reject(id string) (protocol.PairStatusData, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	pg := p.pairs[id]
	if pg == nil {
		return protocol.PairStatusData{}, errors.New("unknown pairing")
	}
	if pg.busy {
		return protocol.PairStatusData{}, errors.New("the confirmation is being written")
	}
	switch pg.state {
	case protocol.PairStateWaiting:
		p.endLocked(pg, protocol.PairStateCancelled, "")
	case protocol.PairStateConfirming:
		p.endLocked(pg, protocol.PairStateRejected, "")
	}
	return p.snapshotLocked(pg), nil
}

// sweep is the timer's entry point.
func (p *Pairer) sweep() {
	p.mu.Lock()
	p.sweepLocked()
	p.mu.Unlock()
}

// sweepLocked expires deadlines that passed and forgets terminal pairings past
// their retention. It is called on every entry, so a missed timer costs nothing.
func (p *Pairer) sweepLocked() {
	now := p.now()
	for id, pg := range p.pairs {
		switch pg.state {
		case protocol.PairStateWaiting, protocol.PairStateConfirming:
			if !pg.busy && !now.Before(pg.expires) {
				p.endLocked(pg, protocol.PairStateExpired, "")
			}
		default:
			if !now.Before(pg.termAt.Add(PairTerminalRetention)) {
				delete(p.pairs, id)
			}
		}
	}
}

// endLocked moves pg to a terminal state, wakes its waiters and audits it.
func (p *Pairer) endLocked(pg *pairing, state, reason string) {
	pg.state, pg.reason = state, reason
	pg.expires = time.Time{}
	pg.termAt = p.now()
	for i := range pg.secret {
		pg.secret[i] = 0
	}
	pg.secret = nil
	pg.code = ""
	pg.relayRV, pg.relayTokHash = "", ""
	if p.open == pg {
		p.open = nil
	}
	p.signalLocked(pg)
	p.armLocked(pg)
	dev := "-"
	if pg.deviceInfo != nil {
		dev = pg.deviceInfo.ID
	}
	p.auditLocked(pg, dev)
}

func (p *Pairer) signalLocked(pg *pairing) {
	close(pg.changed)
	pg.changed = make(chan struct{})
}

// armLocked (re)arms pg's one timer for its next deadline.
func (p *Pairer) armLocked(pg *pairing) {
	if pg.timer != nil {
		pg.timer.Stop()
	}
	at := pg.expires
	if at.IsZero() {
		at = pg.termAt.Add(PairTerminalRetention)
	}
	d := max(at.Sub(p.now()), 0) + time.Millisecond
	pg.timer = time.AfterFunc(d, p.sweep)
}

// logCandidate writes a line an unpaired candidate caused, through candLog.
func (p *Pairer) logCandidate(format string, args ...any) {
	p.candLog.printf(p.now(), p.logf, format, args...)
}

// auditLocked writes the one line per transition. It never carries the secret,
// proof, token, code or payload.
func (p *Pairer) auditLocked(pg *pairing, device string) {
	p.logf("remote: pair pairing=%s state=%s reason=%s device=%s name=%s addr=%s via=%s bad=%d",
		pg.id, pg.state, orDash(pg.reason), device, logSafe(pg.name), logSafe(pg.addr), orDash(pg.via), pg.badProofs)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (p *Pairer) snapshotLocked(pg *pairing) protocol.PairStatusData {
	d := protocol.PairStatusData{Pairing: pg.id, State: pg.state, ExpiresAt: pg.expires}
	switch pg.state {
	case protocol.PairStateConfirming, protocol.PairStatePaired:
		d.DeviceName, d.Platform, d.Via = pg.name, pg.platform, pg.via
	}
	if pg.state == protocol.PairStateConfirming {
		d.NameClash, d.Replaces = pg.nameClash, pg.replaces
		d.TriesLeft = maxConfirmTries - pg.tries
	}
	if pg.state == protocol.PairStatePaired && pg.deviceInfo != nil {
		di := *pg.deviceInfo
		di.Tiers = slices.Clone(di.Tiers)
		d.Device = &di
	}
	if pg.state == protocol.PairStateFailed {
		d.Reason = pg.reason
	}
	return d
}

func deviceInfo(d devices.Device, spki [32]byte) *protocol.DeviceInfo {
	hx := hex.EncodeToString(spki[:8])
	var groups []string
	for i := 0; i+4 <= len(hx); i += 4 {
		groups = append(groups, hx[i:i+4])
	}
	return &protocol.DeviceInfo{
		ID: d.ID, Name: d.Name, Platform: d.Platform, Tiers: slices.Clone(d.Tiers),
		PairedAt: d.PairedAt, LastSeenAt: d.LastSeenAt, Fingerprint: strings.Join(groups, " "),
	}
}

func knownTiers(tiers []string) bool {
	if !slices.Contains(tiers, protocol.TierRead) {
		return false
	}
	for _, t := range tiers {
		if !slices.Contains(protocol.Tiers, t) {
			return false
		}
	}
	return true
}

// cleanName applies the registry's name rules: trimmed, 1..MaxNameRunes, no
// control or format runes.
func cleanName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n == 0 || n > devices.MaxNameRunes || !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return s, true
}

func cleanPlatform(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || !unicode.IsPrint(r) {
			continue
		}
		if n++; n > maxPlatformRunes {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// present runs a pairing candidate's connection (spec 01 §6.4). It ALWAYS
// returns an error: ErrPairingDone once an outcome frame was written (or tried),
// anything else when the candidate was refused before the burn. The connection
// is the caller's to close. source keys the candidate slot (sourceKey).
//
// Nothing an unproven candidate does moves the pairing: silence, a wrong frame
// and a wrong proof each end only that candidate's connection.
func (p *Pairer) present(ctx context.Context, hs *Handshake, phone [32]byte, source string) (Peer, error) {
	if hs.SetReadDeadline != nil {
		_ = hs.SetReadDeadline(time.Now().Add(pairFirstFrameTimeout))
	}
	f, err := hs.NextFrame()
	if err != nil {
		return Peer{}, fmt.Errorf("pairing: first frame: %w", err)
	}
	reqID := f.ID
	if f.Type != protocol.FrameReq || f.Cmd != protocol.CmdRemotePair {
		p.refuse(hs, reqID, protocol.CodeDenied, "pair first")
		return Peer{}, ErrUnauthenticated
	}
	var req protocol.PairRequest
	if err := f.DecodePayload(&req); err != nil {
		p.refuse(hs, reqID, protocol.CodeDenied, "pair first")
		return Peer{}, ErrUnauthenticated
	}
	if hs.TLS == nil {
		return Peer{}, ErrUnauthenticated
	}
	release, ok := p.admitUnpaired(source)
	if !ok {
		p.logCandidate("remote: pair candidate refused: too many pairing attempts addr=%s via=%s", logSafe(hs.RemoteAddr), via(hs))
		p.refuse(hs, reqID, protocol.CodeNotPaired, "this device is not paired")
		return Peer{}, ErrUnauthenticated
	}
	defer release()
	exProof, err := hs.TLS.ExportKeyingMaterial(pairProofLabel, nil, 32)
	if err != nil {
		return Peer{}, ErrUnauthenticated
	}
	exCode, err := hs.TLS.ExportKeyingMaterial(pairCodeLabel, nil, 32)
	if err != nil {
		return Peer{}, ErrUnauthenticated
	}
	proof, perr := base64.RawURLEncoding.DecodeString(req.Proof)

	p.mu.Lock()
	p.sweepLocked()
	pg := p.open
	switch {
	case pg == nil || pg.state != protocol.PairStateWaiting || !p.daemonK:
		msg := "pairing is closed"
		if pg != nil && pg.state == protocol.PairStateConfirming {
			msg = "another phone is pairing"
		}
		p.logCandidate("remote: pair candidate refused: %s addr=%s via=%s", msg, logSafe(hs.RemoteAddr), orDash(hs.Via))
		p.mu.Unlock()
		p.refuse(hs, reqID, protocol.CodePairFailed, msg)
		return Peer{}, ErrUnauthenticated
	}
	want := protocol.PairProof(pg.secret, exProof)
	if perr != nil || !hmac.Equal(proof, want) {
		pg.badProofs++
		if pg.badProofs == badPairProofsAlert {
			p.logf("remote: pair pairing=%s has seen %d wrong proofs; another device on this network may be interfering",
				pg.id, pg.badProofs)
		}
		p.logCandidate("remote: pair pairing=%s refused a wrong proof addr=%s via=%s bad=%d",
			pg.id, logSafe(hs.RemoteAddr), via(hs), pg.badProofs)
		p.mu.Unlock()
		p.refuse(hs, reqID, protocol.CodePairFailed, "pairing failed")
		return Peer{}, ErrUnauthenticated
	}
	name, ok := cleanName(req.Name)
	if !ok {
		p.logf("remote: pair pairing=%s refused a valid proof with an unusable device name addr=%s", pg.id, logSafe(hs.RemoteAddr))
		p.mu.Unlock()
		p.refuse(hs, reqID, protocol.CodePairFailed, "the device name is not accepted")
		return Peer{}, ErrUnauthenticated
	}

	// Burn: CAS waiting -> confirming. The secret is zeroed and the window
	// closes (verifyPeer now refuses every unknown certificate).
	for i := range pg.secret {
		pg.secret[i] = 0
	}
	pg.secret = nil
	pg.relayRV, pg.relayTokHash = "", ""
	pg.code = protocol.PairCode(exCode, p.daemon, phone)
	pg.phone, pg.name, pg.platform = phone, name, cleanPlatform(req.Platform)
	pg.addr, pg.via = hs.RemoteAddr, via(hs)
	pg.nameClash, pg.replaces = false, ""
	for _, d := range p.reg.List() {
		if strings.EqualFold(d.Name, name) {
			pg.nameClash = true
		}
	}
	if d, ok := p.reg.Lookup(phone); ok {
		pg.replaces = d.ID
	}
	pg.state = protocol.PairStateConfirming
	pg.expires = p.now().Add(PairConfirmTTL)
	p.signalLocked(pg)
	p.armLocked(pg)
	p.auditLocked(pg, "-")
	changed := pg.changed
	p.mu.Unlock()

	// One reader from here on: it is the only goroutine that touches the
	// connection until the pairing is terminal.
	if hs.SetReadDeadline != nil {
		_ = hs.SetReadDeadline(time.Time{})
	}
	type read struct {
		f   *protocol.Frame
		err error
	}
	got := make(chan read, 1)
	go func() {
		fr, err := hs.NextFrame()
		got <- read{fr, err}
	}()
	cut := func() {
		if hs.SetReadDeadline != nil {
			_ = hs.SetReadDeadline(time.Now())
		}
		<-got // the watcher has returned; anything it read is discarded
	}

	for {
		select {
		case <-got:
			// The watcher has returned; the connection is dead or misbehaving.
			// A Confirm already writing the registry decides the outcome, so a
			// busy pairing is waited out rather than failed under it.
			for {
				p.mu.Lock()
				if pg.state == protocol.PairStateConfirming && !pg.busy {
					p.endLocked(pg, protocol.PairStateFailed, protocol.PairReasonPhoneLeft)
				}
				done, ch := pg.state != protocol.PairStateConfirming, pg.changed
				p.mu.Unlock()
				if done {
					break
				}
				select {
				case <-ch:
				case <-ctx.Done():
					return Peer{}, ctx.Err()
				}
			}
			p.outcome(hs, reqID, pg)
			return Peer{}, ErrPairingDone
		case <-ctx.Done():
			p.mu.Lock()
			if pg.state == protocol.PairStateConfirming && !pg.busy {
				p.endLocked(pg, protocol.PairStateFailed, protocol.PairReasonPhoneLeft)
			}
			p.mu.Unlock()
			cut()
			return Peer{}, ctx.Err()
		case <-changed:
			p.mu.Lock()
			p.sweepLocked()
			terminal := pg.state != protocol.PairStateConfirming
			changed = pg.changed
			p.mu.Unlock()
			if terminal {
				cut()
				p.outcome(hs, reqID, pg)
				return Peer{}, ErrPairingDone
			}
		}
	}
}

func via(hs *Handshake) string {
	if hs.Via == ViaRelay {
		return ViaRelay
	}
	return ViaLAN
}

// refuse writes one best-effort refusal frame; a write failure changes nothing.
func (p *Pairer) refuse(hs *Handshake, id, code, msg string) {
	if hs.Send == nil {
		return
	}
	f := protocol.ErrorFrame(id, code, msg)
	_ = hs.Send(&f)
}

// outcome writes the frame that answers the remote.pair request, best effort.
func (p *Pairer) outcome(hs *Handshake, id string, pg *pairing) {
	if hs.Send == nil {
		return
	}
	p.mu.Lock()
	state, result := pg.state, pg.result
	p.mu.Unlock()
	switch state {
	case protocol.PairStatePaired:
		data, err := json.Marshal(result)
		if err != nil {
			p.refuse(hs, id, protocol.CodePairFailed, "pairing failed")
			return
		}
		f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameResp, ID: id}
		if err := f.SetPayload(protocol.Response{OK: true, Data: data}); err != nil {
			p.refuse(hs, id, protocol.CodePairFailed, "pairing failed")
			return
		}
		_ = hs.Send(&f)
	case protocol.PairStateRejected:
		p.refuse(hs, id, protocol.CodePairRejected, "pairing was rejected")
	case protocol.PairStateExpired:
		p.refuse(hs, id, protocol.CodePairExpired, "pairing expired")
	default:
		p.refuse(hs, id, protocol.CodePairFailed, "pairing failed")
	}
}
