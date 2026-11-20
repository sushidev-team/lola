// Package devices is the registry of paired phones: who may connect to the
// remote listener and with which capability tiers. It is a stdlib-and-protocol
// leaf. A device is identified by the SHA-256 of its certificate's
// SubjectPublicKeyInfo; names are display only and never decide anything.
//
// The registry fails closed. An unreadable file authenticates nobody and is
// never rewritten; a revocation survives a restart even when devices.json
// cannot be written (see Revoke).
package devices

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
)

const (
	FileName               = "devices.json"
	RevokedFileName        = "devices.revoked"
	MembersFileName        = "members.json"
	MembersRevokedFileName = "members.revoked"
	OwnersFileName         = "owners.json"
	OwnersRevokedFileName  = "owners.revoked"
	FileVersion            = 1
	MaxDevices             = 16
	MaxNameRunes           = 64
	maxPlatformRunes       = 32
	lastSeenPersistEvery   = 10 * time.Minute
)

// Device is one paired phone.
type Device struct {
	ID         string       `json:"id"`                 // "d" + hex(SPKI hash[:8]) = 17 chars
	Name       string       `json:"name"`               // display only
	Platform   string       `json:"platform,omitempty"` // display only
	SPKI       string       `json:"spki"`               // std base64 of SHA-256(SubjectPublicKeyInfo)
	Tiers      []string     `json:"tiers"`              // subset of protocol.Tiers, always contains "read"
	Epoch      uint64       `json:"epoch"`              // file epoch when Add wrote this entry
	PairedAt   time.Time    `json:"pairedAt"`
	LastSeenAt time.Time    `json:"lastSeenAt,omitzero"`
	Relay      *DeviceRelay `json:"relay,omitempty"` // nil = no relay rendezvous
	// Hub is set on a team member entry (members.json only): the hub identity
	// the owner pinned. Display and re-verification data; the TLS key (SPKI)
	// is what authenticates.
	Hub *HubIdentity `json:"hub,omitempty"`
}

// DeviceRelay is the device's hub rendezvous. HASHES ONLY: the plaintext
// bearer exists on the phone and, once, in the reply that delivers it.
type DeviceRelay struct {
	RV         string    `json:"rv"`                    // ^[0-9a-f]{32}$
	BearerHash string    `json:"bearer_sha256"`         // ^[0-9a-f]{64}$ = hex(sha256(bearer ASCII))
	PrevHash   string    `json:"prev_sha256,omitempty"` // previous bearer during rotation
	PrevUntil  time.Time `json:"prev_until,omitzero"`   // set iff PrevHash != ""
	RotatedAt  time.Time `json:"rotated_at"`
}

// Has reports whether the device holds the tier.
func (d Device) Has(tier string) bool { return slices.Contains(d.Tiers, tier) }

var (
	ErrBroken        = errors.New("devices: registry file is unreadable")
	ErrUnknownDevice = errors.New("devices: no such device")
	ErrFull          = errors.New("devices: device limit reached")
	ErrBadTiers      = errors.New("devices: tiers must be known and include read")
	ErrBadName       = errors.New("devices: name is empty, too long or not printable")
	ErrBadRelay      = errors.New("devices: relay record is malformed")
	ErrBadMember     = errors.New("devices: member entry needs a hub identity")
	ErrRevokeUnsaved = errors.New("devices: revoked for this run only: neither devices.json nor devices.revoked could be written")
)

// The relay shape rules repeat hubproto's (this package does not import it);
// TestRelayRegexesMatchHubproto pins them equal.
var (
	relayIDRe   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	relayHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type file struct {
	Version int      `json:"version"`
	Epoch   uint64   `json:"epoch"`
	Devices []Device `json:"devices"`
}

// Registry is safe for concurrent use. Lookups are read-locked: they run on
// every inbound frame.
type Registry struct {
	mu          sync.RWMutex
	dir         string
	file, jfile string // devices.json and devices.revoked, or the members pair
	st          file
	byKey       map[[32]byte]int
	byID        map[string]int
	byRV        map[string]int
	broken      error
	unsaved     bool
	journal     bool // the journal may hold records devices.json has not absorbed
	lastPersist time.Time
	subs        []func()
	now         func() time.Time
	logf        func(string, ...any)

	// Seams: tests swap these to make the disk fail.
	write         func(path string, b []byte) error
	appendJournal func(path, spki string, epoch uint64) error
}

// Open loads the registry from home. It never returns nil: a file it must not
// trust yields a registry whose Err is non-nil and which authenticates nobody.
func Open(home string, now func() time.Time, logf func(string, ...any)) *Registry {
	return open(home, FileName, RevokedFileName, now, logf)
}

// OpenOwners opens the registry of teammates whose shared sessions this machine
// will attach to (owners.json): the other direction of a pin. It is separate
// from members.json so pinning a key to attach to their sessions never lets
// them attach to ours.
func OpenOwners(home string, now func() time.Time, logf func(string, ...any)) *Registry {
	return open(home, OwnersFileName, OwnersRevokedFileName, now, logf)
}

// OpenMembers opens the registry of team members who may attach to shared
// sessions. It is the same registry in its own files (members.json,
// members.revoked), never devices.json: an older lola reading phones would
// otherwise take a teammate for a phone with the read tier.
func OpenMembers(home string, now func() time.Time, logf func(string, ...any)) *Registry {
	return open(home, MembersFileName, MembersRevokedFileName, now, logf)
}

func open(home, name, jname string, now func() time.Time, logf func(string, ...any)) *Registry {
	if now == nil {
		now = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	r := &Registry{
		dir: home, file: name, jfile: jname, now: now, logf: logf,
		write: writeAtomic, appendJournal: appendJournalLine,
		st: file{Version: FileVersion},
	}
	if fi, err := os.Stat(home); err == nil && fi.IsDir() {
		if m := fi.Mode().Perm(); m&0o077 != 0 {
			logf("devices: %s is mode %04o; 0700 is expected for the directory holding %s", home, m, r.file)
		}
	}
	if err := r.load(); err != nil {
		r.break_(err)
	}
	r.reindex()
	return r
}

func (r *Registry) path() string        { return filepath.Join(r.dir, r.file) }
func (r *Registry) journalPath() string { return filepath.Join(r.dir, r.jfile) }

func (r *Registry) break_(cause error) {
	r.broken = fmt.Errorf("%w: %v (move %s away to start with an empty registry)", ErrBroken, cause, r.path())
	r.st = file{Version: FileVersion}
	r.logf("devices: %v", r.broken)
}

func (r *Registry) load() error {
	denied, err := readJournal(r.journalPath())
	if err != nil {
		return fmt.Errorf("%s: %w", r.jfile, err)
	}
	r.journal = len(denied) > 0
	b, err := os.ReadFile(r.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	if f.Version != FileVersion {
		return fmt.Errorf("unsupported version %d", f.Version)
	}
	ids, keys, rvs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	out := f.Devices[:0:0]
	for _, d := range f.Devices {
		raw, err := base64.StdEncoding.DecodeString(d.SPKI)
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("device %q: spki is not 32 bytes of base64", d.ID)
		}
		d.SPKI = base64.StdEncoding.EncodeToString(raw)
		if d.ID == "" || ids[d.ID] {
			return fmt.Errorf("duplicate or empty device id %q", d.ID)
		}
		if keys[d.SPKI] {
			return fmt.Errorf("device %q: duplicate spki", d.ID)
		}
		ids[d.ID], keys[d.SPKI] = true, true
		d.Tiers = r.cleanTiers(d)
		if d.Relay != nil {
			if err := validRelay(d.Relay); err != nil {
				r.logf("devices: dropping the relay record of %s: %v", d.ID, err)
				d.Relay = nil
			} else {
				if rvs[d.Relay.RV] {
					return fmt.Errorf("device %q: relay rv is held by two devices", d.ID)
				}
				rvs[d.Relay.RV] = true
			}
		}
		var k [32]byte
		copy(k[:], raw)
		if ep, ok := denied[k]; ok && d.Epoch <= ep {
			r.logf("devices: %s is revoked in %s; hiding it", d.ID, r.jfile)
			continue
		}
		out = append(out, d)
	}
	f.Devices = out
	r.st = f
	return nil
}

// cleanTiers drops unknown names (fewer rights, never more), orders by
// protocol.Tiers and guarantees read.
func (r *Registry) cleanTiers(d Device) []string {
	var out []string
	for _, t := range protocol.Tiers {
		if slices.Contains(d.Tiers, t) {
			out = append(out, t)
		}
	}
	if len(out) != len(dedup(d.Tiers)) {
		r.logf("devices: %s: dropped unknown tiers", d.ID)
	}
	if !slices.Contains(out, protocol.TierRead) {
		r.logf("devices: %s: no read tier; keeping [read]", d.ID)
		return []string{protocol.TierRead}
	}
	return out
}

func dedup(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func validRelay(rl *DeviceRelay) error {
	switch {
	case !relayIDRe.MatchString(rl.RV):
		return errors.New("rv is not 32 lowercase hex")
	case !relayHashRe.MatchString(rl.BearerHash):
		return errors.New("bearer_sha256 is not 64 lowercase hex")
	case rl.PrevHash != "" && !relayHashRe.MatchString(rl.PrevHash):
		return errors.New("prev_sha256 is not 64 lowercase hex")
	case rl.PrevHash == rl.BearerHash && rl.PrevHash != "":
		return errors.New("prev_sha256 equals bearer_sha256")
	case (rl.PrevHash != "") != !rl.PrevUntil.IsZero():
		return errors.New("prev_until must be set exactly when prev_sha256 is")
	}
	return nil
}

func (r *Registry) reindex() {
	r.byKey = make(map[[32]byte]int, len(r.st.Devices))
	r.byID = make(map[string]int, len(r.st.Devices))
	r.byRV = map[string]int{}
	for i, d := range r.st.Devices {
		raw, _ := base64.StdEncoding.DecodeString(d.SPKI)
		var k [32]byte
		copy(k[:], raw)
		r.byKey[k], r.byID[d.ID] = i, i
		if d.Relay != nil {
			r.byRV[d.Relay.RV] = i
		}
	}
}

func (f file) clone() file {
	c := f
	c.Devices = make([]Device, len(f.Devices))
	for i, d := range f.Devices {
		c.Devices[i] = d.clone()
	}
	return c
}

func (d Device) clone() Device {
	d.Tiers = slices.Clone(d.Tiers)
	if d.Hub != nil {
		h := *d.Hub
		d.Hub = &h
	}
	if d.Relay != nil {
		rl := *d.Relay
		d.Relay = &rl
	}
	return d
}

// Err is non-nil exactly when the registry is broken (wraps ErrBroken).
func (r *Registry) Err() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.broken
}

// Unsaved is non-nil while a revocation lives only in memory.
func (r *Registry) Unsaved() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.unsaved {
		return ErrRevokeUnsaved
	}
	return nil
}

func (r *Registry) Lookup(spki [32]byte) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.byKey[spki]
	if !ok || r.broken != nil {
		return Device{}, false
	}
	return r.st.Devices[i].clone(), true
}

func (r *Registry) Get(id string) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.byID[id]
	if !ok || r.broken != nil {
		return Device{}, false
	}
	return r.st.Devices[i].clone(), true
}

// LookupRV returns the device whose Relay.RV is rv.
func (r *Registry) LookupRV(rv string) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.byRV[rv]
	if !ok || r.broken != nil {
		return Device{}, false
	}
	return r.st.Devices[i].clone(), true
}

// List returns a copy sorted by PairedAt.
func (r *Registry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Device, len(r.st.Devices))
	for i, d := range r.st.Devices {
		out[i] = d.clone()
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].PairedAt.Equal(out[j].PairedAt) {
			return out[i].PairedAt.Before(out[j].PairedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Subscribe registers fn, called after every Add/Revoke/SetTiers/SetRelay,
// outside the registry lock.
func (r *Registry) Subscribe(fn func()) {
	r.mu.Lock()
	r.subs = append(r.subs, fn)
	r.mu.Unlock()
}

func (r *Registry) notify(subs []func()) {
	for _, fn := range subs {
		fn()
	}
}

// HashSPKI is the identity of a certificate key.
func HashSPKI(rawSPKI []byte) [32]byte { return sha256.Sum256(rawSPKI) }

// IDFor is the device id for a key hash: deterministic, 17 characters.
func IDFor(h [32]byte) string { return "d" + hex.EncodeToString(h[:8]) }

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	if n == 0 || n > MaxNameRunes || !utf8.ValidString(name) {
		return "", ErrBadName
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return "", ErrBadName
		}
	}
	return name, nil
}

func checkTiers(in []string) ([]string, error) {
	for _, t := range in {
		if !slices.Contains(protocol.Tiers, t) {
			return nil, ErrBadTiers
		}
	}
	if !slices.Contains(in, protocol.TierRead) {
		return nil, ErrBadTiers
	}
	var out []string
	for _, t := range protocol.Tiers {
		if slices.Contains(in, t) {
			out = append(out, t)
		}
	}
	return out, nil
}

func cleanPlatform(p string) string {
	p = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return -1
		}
		return c
	}, strings.TrimSpace(p))
	if r := []rune(p); len(r) > maxPlatformRunes {
		p = string(r[:maxPlatformRunes])
	}
	return p
}

// checkRelay validates rl and that no OTHER device holds its rv.
func checkRelay(st *file, self string, rl *DeviceRelay) (*DeviceRelay, error) {
	if rl == nil {
		return nil, nil
	}
	if err := validRelay(rl); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRelay, err)
	}
	for _, d := range st.Devices {
		if d.ID != self && d.Relay != nil && d.Relay.RV == rl.RV {
			return nil, fmt.Errorf("%w: rv is held by another device", ErrBadRelay)
		}
	}
	c := *rl
	return &c, nil
}

// Add registers a device, or REPLACES the entry of an SPKI already present
// (same id, new name/tiers/epoch/pairedAt/relay).
func (r *Registry) Add(name, platform string, spki [32]byte, tiers []string, relay *DeviceRelay) (Device, error) {
	name, err := checkName(name)
	if err != nil {
		return Device{}, err
	}
	tiers, err = checkTiers(tiers)
	if err != nil {
		return Device{}, err
	}
	platform = cleanPlatform(platform)
	return r.mutate(func(st *file) (int, error) {
		id := IDFor(spki)
		at := -1
		for i, d := range st.Devices {
			if d.ID == id {
				at = i
			}
		}
		if at < 0 && len(st.Devices) >= MaxDevices {
			return 0, ErrFull
		}
		rl, err := checkRelay(st, id, relay)
		if err != nil {
			return 0, err
		}
		st.Epoch++
		d := Device{
			ID: id, Name: name, Platform: platform,
			SPKI:  base64.StdEncoding.EncodeToString(spki[:]),
			Tiers: tiers, Epoch: st.Epoch, PairedAt: r.now().UTC(), Relay: rl,
		}
		if at < 0 {
			st.Devices = append(st.Devices, d)
			return len(st.Devices) - 1, nil
		}
		st.Devices[at] = d
		return at, nil
	})
}

func (r *Registry) SetTiers(id string, tiers []string) (Device, error) {
	tiers, err := checkTiers(tiers)
	if err != nil {
		return Device{}, err
	}
	return r.mutate(func(st *file) (int, error) {
		i := indexOf(st, id)
		if i < 0 {
			return 0, ErrUnknownDevice
		}
		st.Devices[i].Tiers = tiers
		return i, nil
	})
}

// SetRelay replaces (or with nil clears) the device's rendezvous record.
func (r *Registry) SetRelay(id string, relay *DeviceRelay) (Device, error) {
	return r.mutate(func(st *file) (int, error) {
		i := indexOf(st, id)
		if i < 0 {
			return 0, ErrUnknownDevice
		}
		rl, err := checkRelay(st, id, relay)
		if err != nil {
			return 0, err
		}
		st.Devices[i].Relay = rl
		return i, nil
	})
}

func indexOf(st *file, id string) int {
	for i, d := range st.Devices {
		if d.ID == id {
			return i
		}
	}
	return -1
}

// mutate applies fn to a copy, persists it, and only then swaps memory: a
// failed write leaves the registry as it was.
func (r *Registry) mutate(fn func(st *file) (int, error)) (Device, error) {
	r.mu.Lock()
	if r.broken != nil {
		err := r.broken
		r.mu.Unlock()
		return Device{}, err
	}
	next := r.st.clone()
	i, err := fn(&next)
	if err == nil {
		err = r.persist(next)
	}
	if err != nil {
		r.mu.Unlock()
		return Device{}, err
	}
	r.st = next
	r.reindex()
	dev := next.Devices[i].clone()
	subs := slices.Clone(r.subs)
	r.mu.Unlock()
	r.notify(subs)
	return dev, nil
}

// Revoke removes a device. Memory drops it at once in every outcome; what
// differs is how durable that is (see the package doc and the spec's §3.2):
//  1. devices.json rewritten without it, journal truncated;
//  2. else one line appended to devices.revoked;
//  3. else ErrRevokeUnsaved and Unsaved() reports it until a later save works.
func (r *Registry) Revoke(id string) (Device, error) {
	r.mu.Lock()
	if r.broken != nil {
		err := r.broken
		r.mu.Unlock()
		return Device{}, err
	}
	i := indexOf(&r.st, id)
	if i < 0 {
		r.mu.Unlock()
		return Device{}, ErrUnknownDevice
	}
	dev := r.st.Devices[i].clone()
	next := r.st.clone()
	next.Devices = slices.Delete(next.Devices, i, i+1)
	saveErr := r.persist(next)
	var result error
	if saveErr != nil {
		if jerr := r.appendJournal(r.journalPath(), dev.SPKI, dev.Epoch); jerr == nil {
			r.journal = true
			result = fmt.Errorf("revoked; %s could not be rewritten, the revocation is kept in %s: %w", r.file, r.jfile, saveErr)
		} else {
			r.unsaved = true
			r.logf("devices: revocation of %s is memory-only: save: %v; journal: %v", dev.ID, saveErr, jerr)
			result = ErrRevokeUnsaved
		}
	}
	r.st = next
	r.reindex()
	subs := slices.Clone(r.subs)
	r.mu.Unlock()
	r.notify(subs)
	return dev, result
}

// Touch records that the device was just seen. It persists at most every
// lastSeenPersistEvery, except that a registry holding an unsaved revocation
// retries on every call.
func (r *Registry) Touch(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.byID[id]
	if !ok || r.broken != nil {
		return
	}
	now := r.now().UTC()
	r.st.Devices[i].LastSeenAt = now
	if r.unsaved || now.Sub(r.lastPersist) >= lastSeenPersistEvery {
		if err := r.persist(r.st); err != nil {
			r.logf("devices: saving last-seen: %v", err)
		}
	}
}

// persist writes st atomically. Success makes devices.json authoritative: the
// journal is truncated and any unsaved flag cleared. Called with r.mu held.
func (r *Registry) persist(st file) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return err
	}
	if err := r.write(r.path(), append(b, '\n')); err != nil {
		return err
	}
	r.unsaved = false
	r.lastPersist = r.now()
	if r.journal {
		if err := truncateJournal(r.journalPath()); err != nil {
			// Harmless: journal lines only hide entries up to their epoch.
			r.logf("devices: truncating %s: %v", r.jfile, err)
		} else {
			r.journal = false
		}
	}
	return nil
}

// writeAtomic is temp file + chmod 0600 + sync + rename in the same directory.
func writeAtomic(path string, b []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	name := tmp.Name()
	tmp = nil
	return os.Rename(name, path)
}

// HubIdentity is what a team member entry remembers of the hub-side identity
// the owner confirmed: the member device's hub id and ed25519 key, and the
// fingerprint the person compared.
type HubIdentity struct {
	DeviceID    string `json:"device_id"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	Member      string `json:"member,omitempty"`
}

// AddMember registers a team member's inner TLS key in a registry opened with
// OpenMembers. A member holds the read tier and nothing else: what it may
// reach is decided per shared session by the daemon, never by tiers.
func (r *Registry) AddMember(name string, spki [32]byte, hub HubIdentity) (Device, error) {
	name, err := checkName(name)
	if err != nil {
		return Device{}, err
	}
	if hub.DeviceID == "" || hub.PublicKey == "" || hub.Fingerprint == "" {
		return Device{}, ErrBadMember
	}
	return r.mutate(func(st *file) (int, error) {
		id := IDFor(spki)
		at := indexOf(st, id)
		if at < 0 && len(st.Devices) >= MaxDevices {
			return 0, ErrFull
		}
		st.Epoch++
		h := hub
		d := Device{
			ID: id, Name: name, Platform: "member",
			SPKI:  base64.StdEncoding.EncodeToString(spki[:]),
			Tiers: []string{protocol.TierRead}, Epoch: st.Epoch, PairedAt: r.now().UTC(), Hub: &h,
		}
		if at < 0 {
			st.Devices = append(st.Devices, d)
			return len(st.Devices) - 1, nil
		}
		st.Devices[at] = d
		return at, nil
	})
}
