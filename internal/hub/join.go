package hub

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// The local halves of `lola hub join | leave | status`. They operate on the
// config file and the state directory directly (the daemon need not run), and
// leave telling a running daemon to reload to the caller.

// JoinOptions configures Join. ConfigPath and StateDir are required.
type JoinOptions struct {
	URL        string
	Token      string
	DeviceName string // "" = the sanitized hostname
	ConfigPath string
	StateDir   string
	// NewClient is the client seam (nil = NewClient).
	NewClient func(url string, key *Key) (*Client, error)
	// Now is the clock seam (nil = time.Now).
	Now func() time.Time
}

// JoinResult reports what Join did.
type JoinResult struct {
	Identity   Identity
	KeyCreated bool
}

// ErrTokenUsed wraps a failure AFTER the hub accepted the one-time token:
// the token is spent, so retrying with it fails; an admin issues a new one.
var ErrTokenUsed = errors.New("hub: the hub accepted the one-time token (it is now used)")

// pendingJoin is state/hub-join-pending.json.
type pendingJoin struct {
	URL      string    `json:"url"`
	DeviceID string    `json:"device_id"`
	At       time.Time `json:"at"`
}

// Join creates the device key if needed, enrolls it with the hub using a
// one-time token, writes state/hub.json and sets [hub].enabled + url in the
// config. Joining a different hub than the one recorded is refused: leave
// first, so an identity is never silently replaced.
//
// The token is single-use, so everything that can fail is tried BEFORE it is
// sent: the key is created, a pending-join record is written to the state
// directory (proving it writable) and the config file is loaded and its
// directory probed. After the hub accepts the token, the identity is saved
// first, then the config is RE-LOADED (a person may have edited it while the
// hub answered) and only [hub].enabled / url / device_name are set on it.
// A failure past that point wraps ErrTokenUsed and says what was and was not
// saved.
func Join(ctx context.Context, o JoinOptions) (JoinResult, error) {
	if o.ConfigPath == "" || o.StateDir == "" {
		return JoinResult{}, errors.New("hub: join needs a config path and a state dir")
	}
	url := strings.TrimSuffix(strings.TrimSpace(o.URL), "/")
	if err := config.ValidateHubURL(url); err != nil {
		return JoinResult{}, err
	}
	name := o.DeviceName
	if name == "" {
		name = DefaultDeviceName()
	}
	if !config.ValidHubDeviceName(name) {
		return JoinResult{}, errors.New("hub: device name must be 1-64 letters, digits, spaces, dots, dashes or underscores")
	}
	if _, err := config.Load(o.ConfigPath); err != nil {
		return JoinResult{}, err
	}
	if prev, err := LoadIdentity(o.StateDir); err == nil && prev.URL != url {
		return JoinResult{}, fmt.Errorf("hub: already joined to %s; run `lola hub leave` first", prev.URL)
	}
	if err := probeWritable(filepath.Dir(o.ConfigPath)); err != nil {
		return JoinResult{}, fmt.Errorf("hub: the config directory is not writable, so the join could not be saved (the token was not sent): %w", err)
	}

	_, keyErr := LoadKey(o.StateDir)
	key, err := LoadOrCreateKey(o.StateDir)
	if err != nil {
		return JoinResult{}, err
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	pending, err := json.Marshal(pendingJoin{URL: url, DeviceID: key.DeviceID(), At: now().UTC()})
	if err != nil {
		return JoinResult{}, err
	}
	if err := writeAtomic(o.StateDir, PendingJoinFile, append(pending, '\n')); err != nil {
		return JoinResult{}, fmt.Errorf("hub: the state directory is not writable (the token was not sent): %w", err)
	}
	newClient := o.NewClient
	if newClient == nil {
		newClient = NewClient
	}
	c, err := newClient(url, key)
	if err != nil {
		_ = removeIfExists(filepath.Join(o.StateDir, PendingJoinFile))
		return JoinResult{}, err
	}
	resp, err := c.Enroll(ctx, o.Token, name)
	if errors.Is(err, ErrInvalidResponse) {
		// The hub answered 200 with a reply lola refuses: it has most likely
		// recorded the device and used the token. The pending record stays.
		return JoinResult{}, fmt.Errorf("%w, but its reply did not validate, so this machine is not joined: %v; ask an admin for a new token", ErrTokenUsed, err)
	}
	if err != nil {
		// Refused or unreachable: the hub did not record this device, so the
		// pending record is noise. (A timeout may still have used the token;
		// the hub's answer is the only proof and there was none.)
		_ = removeIfExists(filepath.Join(o.StateDir, PendingJoinFile))
		return JoinResult{}, err
	}
	// The hub may have made the name unique (-2, -3); keep what it stored.
	stored := name
	if resp.DeviceName != "" && config.ValidHubDeviceName(resp.DeviceName) {
		stored = resp.DeviceName
	}
	member := ""
	if resp.Member != "" && hubproto.ValidMemberLabel(resp.Member) {
		member = resp.Member
	}
	id := Identity{URL: url, DeviceID: resp.DeviceID, Org: resp.Org, DeviceName: stored, Member: member, JoinedAt: now().UTC()}
	if err := SaveIdentity(o.StateDir, id); err != nil {
		return JoinResult{}, fmt.Errorf("%w, but saving this machine's identity failed, so it is not joined: %v; ask an admin for a new token", ErrTokenUsed, err)
	}
	_ = removeIfExists(filepath.Join(o.StateDir, PendingJoinFile))
	_ = ClearRevocation(o.StateDir)
	// Re-read the file right before writing it: the enroll round trip can take
	// seconds, and a save from the copy loaded above would drop an edit made
	// meanwhile.
	cfg, err := config.Load(o.ConfigPath)
	if err == nil {
		cfg.Hub.Enabled = true
		cfg.Hub.URL = url
		if o.DeviceName != "" {
			cfg.Hub.DeviceName = o.DeviceName
		}
		err = cfg.Save(o.ConfigPath)
	}
	if err != nil {
		return JoinResult{Identity: id, KeyCreated: errors.Is(keyErr, ErrNoKey)},
			fmt.Errorf("%w and this machine's identity is saved, but config.toml was not updated: %v; set [hub] enabled = true and url = %q by hand", ErrTokenUsed, err, url)
	}
	return JoinResult{Identity: id, KeyCreated: errors.Is(keyErr, ErrNoKey)}, nil
}

// probeWritable creates and removes a temp file in dir.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".lola-join-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// InterruptedJoin reports a pending-join record a join left behind: it was
// cut short after the token may have been sent.
func InterruptedJoin(dir string) (url string, at time.Time, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, PendingJoinFile))
	if err != nil {
		return "", time.Time{}, false
	}
	var p pendingJoin
	if json.Unmarshal(data, &p) != nil || config.ValidateHubURL(p.URL) != nil {
		return "", time.Time{}, false
	}
	return p.URL, p.At, true
}

// DefaultDeviceName is the hostname reduced to what enrollment accepts
// (".local" dropped, other characters folded to "-", 64 max), or "lola".
func DefaultDeviceName() string {
	h, err := os.Hostname()
	if err != nil {
		return "lola"
	}
	h = strings.TrimSuffix(h, ".local")
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 64 {
			break
		}
	}
	name := strings.TrimLeft(b.String(), " ._-")
	if !config.ValidHubDeviceName(name) {
		return "lola"
	}
	return name
}

// LeaveOptions configures Leave.
type LeaveOptions struct {
	ConfigPath string
	StateDir   string
	// ForgetKey also deletes the device key; without it the key is kept so a
	// later join re-uses the same device identity.
	ForgetKey bool
	// Now is the clock seam (nil = time.Now).
	Now func() time.Time
}

// Leave removes the local identity (and the stored policy) and sets
// [hub].enabled = false, keeping the rest of the table. The hub is not told:
// an admin revokes the device there. It records the hub URL and org it left
// (state/hub-left.json), so what a leave keeps — sessions, the daily claim
// count — can be traced to the hub it belonged to.
func Leave(o LeaveOptions) error {
	if o.ConfigPath == "" || o.StateDir == "" {
		return errors.New("hub: leave needs a config path and a state dir")
	}
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return err
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	if id, err := LoadIdentity(o.StateDir); err == nil {
		if err := saveLeft(o.StateDir, LeftRecord{URL: id.URL, Org: id.Org, DeviceID: id.DeviceID, At: now().UTC()}); err != nil {
			return err
		}
	} else if r, ok := LoadRevocation(o.StateDir); ok {
		if err := saveLeft(o.StateDir, LeftRecord{URL: r.URL, Org: r.Org, DeviceID: r.DeviceID, At: now().UTC()}); err != nil {
			return err
		}
	}
	if err := RemoveIdentity(o.StateDir); err != nil {
		return err
	}
	if err := errors.Join(ClearRevocation(o.StateDir), removeIfExists(filepath.Join(o.StateDir, PendingJoinFile))); err != nil {
		return err
	}
	if o.ForgetKey {
		if err := RemoveKey(o.StateDir); err != nil {
			return err
		}
	}
	if !cfg.Hub.Enabled {
		return nil
	}
	cfg.Hub.Enabled = false
	return cfg.Save(o.ConfigPath)
}

// Status is the LOCAL view `lola hub status` prints.
type Status struct {
	Enabled  bool
	URL      string
	Joined   bool
	Identity Identity
	// IdentityErr is set when hub.json exists but does not load.
	IdentityErr string
	KeyPresent  bool
	// KeyMismatch: the key on disk is not the one that enrolled.
	KeyMismatch bool
	KeyErr      string
	Policy      *StoredPolicy
	PolicyErr   string
	// Revoked is set when the hub refused this device's signature and lola
	// dropped the identity (MarkRevoked).
	Revoked *Revocation
	// InterruptedJoin names a hub a join was cut short for after the token
	// may have been sent ("" = none).
	InterruptedJoin string
}

// Membership is the one-word answer to "is this machine in a team": joined,
// revoked or not joined.
func (s Status) Membership() string {
	switch {
	case s.Joined:
		return "joined"
	case s.Revoked != nil:
		return "revoked"
	}
	return "not joined"
}

// LocalStatus gathers the local hub state; it never contacts the hub.
func LocalStatus(cfg *config.Config, dir string) Status {
	var s Status
	if cfg != nil {
		s.Enabled, s.URL = cfg.Hub.Enabled, cfg.Hub.URL
	}
	id, err := LoadIdentity(dir)
	switch {
	case err == nil:
		s.Joined, s.Identity = true, id
	case !errors.Is(err, ErrNotJoined):
		s.IdentityErr = err.Error()
	}
	if !s.Joined {
		if r, ok := LoadRevocation(dir); ok {
			s.Revoked = &r
		}
		if u, _, ok := InterruptedJoin(dir); ok {
			s.InterruptedJoin = u
		}
	}
	key, err := LoadKey(dir)
	switch {
	case err == nil:
		s.KeyPresent = true
		s.KeyMismatch = s.Joined && key.DeviceID() != s.Identity.DeviceID
	case !errors.Is(err, ErrNoKey):
		s.KeyErr = err.Error()
	}
	var trusted map[string]ed25519.PublicKey
	if cfg != nil {
		trusted, err = TrustedKeys(cfg.Hub)
	} else {
		err = ErrNoTrustedKeys
	}
	if err != nil {
		s.PolicyErr = err.Error()
		return s
	}
	sp, err := LoadPolicy(dir, trusted)
	if err != nil {
		s.PolicyErr = err.Error()
		return s
	}
	s.Policy = &sp
	return s
}

// Render formats the status for a terminal.
func (s Status) Render() string {
	var b strings.Builder
	state := "disabled"
	if s.Enabled {
		state = "enabled"
	}
	fmt.Fprintf(&b, "hub:      %s", state)
	if s.URL != "" {
		fmt.Fprintf(&b, " (%s)", s.URL)
	}
	b.WriteByte('\n')
	switch {
	case s.Joined:
		fmt.Fprintf(&b, "joined:   org %s as %q, device %s", s.Identity.Org, s.Identity.DeviceName, s.Identity.DeviceID)
		if s.Identity.Member != "" {
			fmt.Fprintf(&b, ", member %q", s.Identity.Member)
		}
		if !s.Identity.JoinedAt.IsZero() {
			fmt.Fprintf(&b, " since %s", s.Identity.JoinedAt.Format(time.RFC3339))
		}
		b.WriteByte('\n')
		if s.Identity.URL != s.URL && s.URL != "" {
			fmt.Fprintf(&b, "warning:  joined %s but [hub].url is %s\n", s.Identity.URL, s.URL)
		}
	case s.IdentityErr != "":
		fmt.Fprintf(&b, "joined:   unreadable identity: %s\n", s.IdentityErr)
	case s.Revoked != nil:
		fmt.Fprintf(&b, "joined:   revoked: %s (org %s) stopped accepting this device on %s; ask an admin for a new token and join again\n",
			s.Revoked.URL, s.Revoked.Org, s.Revoked.At.Format(time.RFC3339))
	default:
		b.WriteString("joined:   no (run `lola hub join <url>` with the token on stdin)\n")
	}
	if s.InterruptedJoin != "" {
		fmt.Fprintf(&b, "warning:  a join to %s was cut short; its token may already be used\n", s.InterruptedJoin)
	}
	switch {
	case s.KeyErr != "":
		fmt.Fprintf(&b, "key:      %s\n", s.KeyErr)
	case s.KeyMismatch:
		b.WriteString("key:      MISMATCH: the device key is not the one that enrolled; leave and join again\n")
	case s.KeyPresent:
		b.WriteString("key:      present\n")
	default:
		b.WriteString("key:      none\n")
	}
	if s.Policy != nil {
		fmt.Fprintf(&b, "policy:   verified %s (its keys are locked while [hub].apply_policy is on)\n", s.Policy.VerifiedAt.Format(time.RFC3339))
		for _, l := range PolicySummary(s.Policy.Policy) {
			fmt.Fprintf(&b, "          %s\n", l)
		}
	} else {
		fmt.Fprintf(&b, "policy:   none (%s)\n", s.PolicyErr)
	}
	return b.String()
}
