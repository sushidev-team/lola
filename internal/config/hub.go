package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/sushidev-team/lola/hubproto"
)

// The [hub] table enrols this daemon with a lola HUB: the team coordinator that
// hands out mechanical chores (security advisories, red dependency bumps) and
// routes the team's signed policy. See lola-hub's DESIGN.md for the contract;
// the rule every key below serves is that a compromised hub must not mean code
// execution on this machine.
//
// Like [remote], the table is a property of the MACHINE and is deliberately
// NOT part of the [defaults] inheritance layer. Neither is the per-project
// opt-in, [[project]].hub_chores: an inherited opt-in would enrol every
// project by accident.
//
// The two keys whose default is the SAFE one (require_sandbox = true,
// max_claims_per_day = 4) are POINTERS in memory, like
// [defaults].manage_daemon: a hand-built HubConfig{} (tests, both UIs) must
// read as "sandbox required" rather than silently flipping to the permissive
// zero value. Read them through SandboxRequired / ClaimsPerDay, never directly.
// A pointer also gives Save its "write only when set" behaviour for free.

const (
	// DefaultHubMaxClaimsPerDay bounds how many chores this daemon claims per
	// local day when [hub].max_claims_per_day is unset.
	DefaultHubMaxClaimsPerDay = 4
	// MaxHubClaimsPerDay is the largest accepted max_claims_per_day.
	MaxHubClaimsPerDay = 100
	// DefaultHubPollSeconds is the hub loop's cadence when poll_seconds is unset.
	DefaultHubPollSeconds = 300
	// MinHubPollSeconds / MaxHubPollSeconds bound an explicit poll_seconds.
	MinHubPollSeconds = 60
	MaxHubPollSeconds = 86400
	// maxHubDeviceNameLen mirrors hubproto's enroll cap on device_name.
	maxHubDeviceNameLen = 64
)

// hubDeviceNameRe mirrors hubproto's enroll device_name pattern, so a name the
// config accepts is never refused at enrollment. A parity test pins the two.
var hubDeviceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]*$`)

// HubConfig is the [hub] table.
//
//   - Enabled gates the hub loop; false (the default) means the daemon never
//     contacts a hub.
//   - URL is the hub's base URL: https, or plain http on a loopback host for
//     development (ValidateHubURL). No path, query or credentials.
//   - DeviceName is the human name sent at enrollment ("" = the hostname).
//   - AcceptKinds narrows the chore kinds this daemon claims; EMPTY means
//     every kind hubproto knows (AcceptsKind).
//   - MaxClaimsPerDay caps claims per local day (nil = 4; 0 = claim nothing).
//   - RequireSandbox refuses a chore for a project without sandbox = "docker"
//     (nil = true). Opting out is a local, explicit decision.
//   - TrustedPolicyKeys pins the admin keys that may sign team policy:
//     key_id → base64url ed25519 public key. The hub cannot add one. It is
//     written as a [hub.trusted_policy_keys] table, and also READ from the
//     array of "<key_id>:<public key>" strings lola-hub prints (TrustedKeyMap).
//   - PollSeconds is the hub loop's cadence (0 = 300).
type HubConfig struct {
	Enabled           bool          `toml:"enabled,omitempty"`
	URL               string        `toml:"url,omitempty"`
	DeviceName        string        `toml:"device_name,omitempty"`
	AcceptKinds       []string      `toml:"accept_kinds,omitempty"`
	MaxClaimsPerDay   *int          `toml:"max_claims_per_day,omitempty"`
	RequireSandbox    *bool         `toml:"require_sandbox,omitempty"`
	TrustedPolicyKeys TrustedKeyMap `toml:"trusted_policy_keys,omitempty"`
	PollSeconds       int           `toml:"poll_seconds,omitzero"`
}

// TrustedKeyMap is [hub].trusted_policy_keys: key_id → base64url public key.
//
// It decodes from BOTH shapes an operator will meet: the table lola writes
//
//	[hub.trusted_policy_keys]
//	k0123456789abcdef = "<base64url>"
//
// and the array of hubproto.FormatTrustedKey entries that lola-hub's
// `policy keygen` and `admin policy keys add` print:
//
//	trusted_policy_keys = ["k0123456789abcdef:<base64url>"]
//
// Without the custom decoder the TOML library dropped the array form WITHOUT
// an error, so a pinned key silently pinned nothing and `lola hub status`
// reported "no trusted policy keys configured" over a config that named one.
// Any other shape is a parse error, never an empty map. Save always writes the
// table form. Key and value validation stays in Validate.
type TrustedKeyMap map[string]string

// UnmarshalTOML implements toml.Unmarshaler.
func (m *TrustedKeyMap) UnmarshalTOML(v any) error {
	out := TrustedKeyMap{}
	switch t := v.(type) {
	case map[string]any:
		for id, raw := range t {
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("hub.trusted_policy_keys.%s must be a string", id)
			}
			out[id] = s
		}
	case []any:
		for i, raw := range t {
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("hub.trusted_policy_keys[%d] must be a \"<key_id>:<public key>\" string", i)
			}
			id, pub, ok := strings.Cut(strings.TrimSpace(s), ":")
			if !ok || id == "" {
				return fmt.Errorf("hub.trusted_policy_keys[%d] must be a \"<key_id>:<public key>\" string", i)
			}
			if _, dup := out[id]; dup {
				return fmt.Errorf("hub.trusted_policy_keys: key id %q listed twice", id)
			}
			out[id] = pub
		}
	default:
		return errors.New("hub.trusted_policy_keys must be a table (key_id = \"<public key>\") or an array of \"<key_id>:<public key>\" strings")
	}
	*m = out
	return nil
}

// SandboxRequired reports the effective require_sandbox (default true).
func (h HubConfig) SandboxRequired() bool {
	return h.RequireSandbox == nil || *h.RequireSandbox
}

// ClaimsPerDay reports the effective max_claims_per_day (default 4).
func (h HubConfig) ClaimsPerDay() int {
	if h.MaxClaimsPerDay == nil {
		return DefaultHubMaxClaimsPerDay
	}
	return *h.MaxClaimsPerDay
}

// EffectivePollSeconds reports poll_seconds with its default applied.
func (h HubConfig) EffectivePollSeconds() int {
	if h.PollSeconds == 0 {
		return DefaultHubPollSeconds
	}
	return h.PollSeconds
}

// AcceptsKind reports whether this daemon accepts chores of kind k: k must be
// a kind hubproto knows, and AcceptKinds (when non-empty) must list it.
func (h HubConfig) AcceptsKind(k hubproto.Kind) bool {
	if !k.Valid() {
		return false
	}
	return len(h.AcceptKinds) == 0 || slices.Contains(h.AcceptKinds, string(k))
}

// TrustedKeyEntries renders TrustedPolicyKeys in hubproto's "<key_id>:<pub>"
// form, sorted by key id, ready for hubproto.ParseTrustedKeys.
func (h HubConfig) TrustedKeyEntries() []string {
	out := make([]string, 0, len(h.TrustedPolicyKeys))
	for id, pub := range h.TrustedPolicyKeys {
		out = append(out, id+":"+strings.TrimSpace(pub))
	}
	sort.Strings(out)
	return out
}

// isZero reports whether the table carries nothing (omitted on Save).
func (h HubConfig) isZero() bool {
	return !h.Enabled && h.URL == "" && h.DeviceName == "" && len(h.AcceptKinds) == 0 &&
		h.MaxClaimsPerDay == nil && h.RequireSandbox == nil && len(h.TrustedPolicyKeys) == 0 &&
		h.PollSeconds == 0
}

// hubTable is the Save mirror: nil (table omitted) when nothing is set. Only
// keys that are set are written, so a default is never frozen into the file.
func hubTable(h HubConfig) *HubConfig {
	if h.isZero() {
		return nil
	}
	return &h
}

// ValidateHubURL is the static rule for [hub].url: https on any host, or plain
// http only on a loopback host (127.0.0.1, ::1, localhost) for development.
// No userinfo, path, query or fragment: every request is signed over its path,
// and a base path a proxy rewrote would break every signature. The client
// applies the same rule before it sends anything.
func ValidateHubURL(raw string) error {
	if raw == "" {
		return errors.New("hub url is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("hub url does not parse")
	}
	if u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return errors.New("hub url must not carry credentials, a query or a fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("hub url must not carry a path")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("hub url has no host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return errors.New("hub url must be https (plain http only for localhost / 127.0.0.1)")
	}
	return errors.New("hub url must be https (plain http only for localhost / 127.0.0.1)")
}

// ValidHubDeviceName reports whether name is acceptable as an enrollment
// device name.
func ValidHubDeviceName(name string) bool {
	return len(name) > 0 && len(name) <= maxHubDeviceNameLen && hubDeviceNameRe.MatchString(name)
}

// validateHub applies the static rules for [hub] and every project's
// hub_chores. Offline like the rest of Validate: whether the hub answers is a
// runtime question.
func (c *Config) validateHub() []error {
	var errs []error
	h := c.Hub
	if h.URL != "" {
		if err := ValidateHubURL(h.URL); err != nil {
			errs = append(errs, fmt.Errorf("hub.url: %v", err))
		}
	} else if h.Enabled {
		errs = append(errs, errors.New("hub.enabled = true needs hub.url (run `lola hub join`)"))
	}
	if h.DeviceName != "" && !ValidHubDeviceName(h.DeviceName) {
		errs = append(errs, fmt.Errorf("hub.device_name must match %s and be at most %d characters", hubDeviceNameRe, maxHubDeviceNameLen))
	}
	seen := map[string]bool{}
	for _, k := range h.AcceptKinds {
		if !hubproto.Kind(k).Valid() {
			errs = append(errs, fmt.Errorf("hub.accept_kinds: unknown kind %q (must be one of %v)", k, hubproto.AllKinds()))
		} else if seen[k] {
			errs = append(errs, fmt.Errorf("hub.accept_kinds: %q listed twice", k))
		}
		seen[k] = true
	}
	if h.MaxClaimsPerDay != nil && (*h.MaxClaimsPerDay < 0 || *h.MaxClaimsPerDay > MaxHubClaimsPerDay) {
		errs = append(errs, fmt.Errorf("hub.max_claims_per_day must be within 0..%d, got %d", MaxHubClaimsPerDay, *h.MaxClaimsPerDay))
	}
	if h.PollSeconds != 0 && (h.PollSeconds < MinHubPollSeconds || h.PollSeconds > MaxHubPollSeconds) {
		errs = append(errs, fmt.Errorf("hub.poll_seconds must be within %d..%d (0 = %d), got %d",
			MinHubPollSeconds, MaxHubPollSeconds, DefaultHubPollSeconds, h.PollSeconds))
	}
	ids := make([]string, 0, len(h.TrustedPolicyKeys))
	for id := range h.TrustedPolicyKeys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !hubproto.ValidKeyID(id) {
			errs = append(errs, fmt.Errorf("hub.trusted_policy_keys: key id %q is malformed", id))
			continue
		}
		if _, err := hubproto.DecodePublicKey(strings.TrimSpace(h.TrustedPolicyKeys[id])); err != nil {
			errs = append(errs, fmt.Errorf("hub.trusted_policy_keys.%s is not a base64url ed25519 public key", id))
		}
	}
	for i := range c.Projects {
		p := &c.Projects[i]
		if !p.HubChores {
			continue
		}
		id := fmt.Sprintf("project %q", p.Name)
		if p.IsStack() {
			errs = append(errs, fmt.Errorf("%s: hub_chores is not offered for a stack (enable it on the member projects)", id))
			continue
		}
		if err := hubproto.ValidateRepo(p.Repo); err != nil {
			errs = append(errs, fmt.Errorf("%s: hub_chores needs repo set to an owner/name GitHub slug", id))
		}
	}
	return errs
}
