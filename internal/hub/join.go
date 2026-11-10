package hub

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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

// Join creates the device key if needed, enrolls it with the hub using a
// one-time token, writes state/hub.json and sets [hub].enabled + url in the
// config. Joining a different hub than the one recorded is refused: leave
// first, so an identity is never silently replaced.
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
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return JoinResult{}, err
	}
	if prev, err := LoadIdentity(o.StateDir); err == nil && prev.URL != url {
		return JoinResult{}, fmt.Errorf("hub: already joined to %s; run `lola hub leave` first", prev.URL)
	}

	_, keyErr := LoadKey(o.StateDir)
	key, err := LoadOrCreateKey(o.StateDir)
	if err != nil {
		return JoinResult{}, err
	}
	newClient := o.NewClient
	if newClient == nil {
		newClient = NewClient
	}
	c, err := newClient(url, key)
	if err != nil {
		return JoinResult{}, err
	}
	resp, err := c.Enroll(ctx, o.Token, name)
	if err != nil {
		return JoinResult{}, err
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	id := Identity{URL: url, DeviceID: resp.DeviceID, Org: resp.Org, DeviceName: name, JoinedAt: now().UTC()}
	if err := SaveIdentity(o.StateDir, id); err != nil {
		return JoinResult{}, err
	}
	cfg.Hub.Enabled = true
	cfg.Hub.URL = url
	if o.DeviceName != "" {
		cfg.Hub.DeviceName = o.DeviceName
	}
	if err := cfg.Save(o.ConfigPath); err != nil {
		return JoinResult{}, err
	}
	return JoinResult{Identity: id, KeyCreated: errors.Is(keyErr, ErrNoKey)}, nil
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
}

// Leave removes the local identity (and the stored policy) and sets
// [hub].enabled = false, keeping the rest of the table. The hub is not told:
// an admin revokes the device there.
func Leave(o LeaveOptions) error {
	if o.ConfigPath == "" || o.StateDir == "" {
		return errors.New("hub: leave needs a config path and a state dir")
	}
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return err
	}
	if err := RemoveIdentity(o.StateDir); err != nil {
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
		if !s.Identity.JoinedAt.IsZero() {
			fmt.Fprintf(&b, " since %s", s.Identity.JoinedAt.Format(time.RFC3339))
		}
		b.WriteByte('\n')
		if s.Identity.URL != s.URL && s.URL != "" {
			fmt.Fprintf(&b, "warning:  joined %s but [hub].url is %s\n", s.Identity.URL, s.URL)
		}
	case s.IdentityErr != "":
		fmt.Fprintf(&b, "joined:   unreadable identity: %s\n", s.IdentityErr)
	default:
		b.WriteString("joined:   no (run `lola hub join <url> <token>`)\n")
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
		fmt.Fprintf(&b, "policy:   verified %s (display only)\n", s.Policy.VerifiedAt.Format(time.RFC3339))
		for _, l := range PolicySummary(s.Policy.Policy) {
			fmt.Fprintf(&b, "          %s\n", l)
		}
	} else {
		fmt.Fprintf(&b, "policy:   none (%s)\n", s.PolicyErr)
	}
	return b.String()
}
