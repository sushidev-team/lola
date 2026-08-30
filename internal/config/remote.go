package config

import (
	"fmt"
	"net"
	"slices"
)

// The [remote] table configures the PHONE LISTENER: the TLS socket
// internal/remote binds so a paired mobile client can read the session list,
// answer a parked agent and attach to a pane. It is the one table in this file
// whose default is chosen for what it GRANTS rather than for what it costs —
// enabling it hands a device on the network the ability to type arbitrary prose
// into an autonomous coding agent and, at the higher capability tiers, to tear
// down worktrees. So it defaults to DISABLED, and an absent table means
// Enabled=false with zero behavior change.
//
// The table is deliberately PLAIN: no [defaults] inheritance, no pointer
// discipline beyond the on-disk mirror every optional section uses. A listener
// is a property of the machine, not of a project, so there is nothing for a
// project to override.
//
// Validation here is BUILD-TAG INDEPENDENT and offline, and both halves of that
// are load-bearing. Offline, because a hostname cannot be resolved at load time
// without turning a config read into a network call, so `bind` is either one of
// the four keywords or an IP literal and anything else is an error rather than a
// lookup. Build-tag independent, because the TUI and the desktop app are built
// WITHOUT the lola_insecure tag and must not reject a config the daemon accepts:
// the M1 restriction — the listener exists only in a lola_insecure build, and
// that build forces the bind to localhost whatever this table says — is enforced
// at RUNTIME in internal/remote, which logs the override, not here.
//
// Keys this table deliberately does NOT have yet: `advertise` (mDNS) and
// [remote.push] (APNs) belong to later milestones, and this repo rejects keys
// that quietly do nothing — the same rule that makes an unknown priority_sort
// key an error. BurntSushi/toml ignores unknown keys, so a config written ahead
// of the daemon still loads.

const (
	// DefaultRemotePort is the TCP port the listener binds when [remote].port is
	// unset or 0. It is in the ephemeral-adjacent range on purpose: nothing
	// standard claims it, and it is far from the ports a dev command binds.
	DefaultRemotePort = 7717
	// DefaultRemoteBind is the bind mode for a present [remote] table that omits
	// the key: loopback only, which is what an SSH forward, a tunnel or a
	// tailnet wants and is the only mode that puts no bearer of any kind on a
	// network interface.
	DefaultRemoteBind = "localhost"
)

// RemoteBinds are the keyword values [remote].bind accepts. Anything else must
// parse as an IP literal; a hostname is rejected rather than resolved (see the
// offline rule above). Same posture as UIThemes and PrioritySortKeys: a typo
// that silently fell back to a safer or less safe mode would leave the operator
// with no signal, and here the two directions of that mistake are not
// symmetric.
//
//	off        bind nothing — keep the port and the rest of the table, listen
//	           on no interface. Distinct from enabled = false only in intent,
//	           and kept because "stop listening, do not lose my settings" is a
//	           thing operators do.
//	localhost  loopback only.
//	lan        only private interfaces (RFC1918 / ULA / link-local) whose names
//	           are not tunnels or virtual bridges. On a laptop that means every
//	           network the machine ever joins, conference WiFi included; the
//	           bound interfaces are logged by name at startup.
//	all        0.0.0.0. Never a default.
var RemoteBinds = []string{"off", "localhost", "lan", "all"}

// RemoteConfig is the [remote] table.
//
//   - Enabled gates the whole feature; false (the default, and the value for an
//     absent table) means the daemon never binds anything.
//   - Bind selects the interfaces, as one of RemoteBinds or an IP literal. ""
//     means DefaultRemoteBind.
//   - Port is the TCP port; 0 means DefaultRemotePort.
type RemoteConfig struct {
	Enabled bool   `toml:"enabled"`
	Bind    string `toml:"bind"`
	Port    int    `toml:"port"`

	// InsecureLAN lets a milestone-1 daemon honour a non-loopback Bind instead
	// of forcing loopback. It exists so a PHYSICAL phone can reach the daemon:
	// a Simulator shares the Mac's loopback and never needs it, but a real
	// device cannot reach 127.0.0.1 on another machine.
	//
	// It is a CONFIG KEY rather than an environment variable, and that was a
	// correction. The first version read LOLA_REMOTE_INSECURE_LAN, reasoning
	// that the permission should not persist — but the daemon is normally
	// started by the TUI's ^r or the desktop app's restart button, and neither
	// can set an environment variable. So the opt-in was lost on every restart
	// and the listener silently dropped back to loopback, which presents as a
	// phone that connected yesterday and cannot today. A permission nobody can
	// grant through the UI that runs the daemon is not a safe default, it is an
	// unusable one.
	//
	// What actually contains this is the BUILD TAG: none of the insecure path
	// exists in a release binary, so the key is inert there. Within a
	// lola_insecure build it still takes TWO deliberate keys — this one AND a
	// Bind naming something other than loopback — so a config that merely says
	// bind = "lan" still binds loopback.
	//
	// It is deleted with the rest of the tag. M2's per-device identities and
	// mutual TLS make binding to a LAN an ordinary thing needing no opt-in.
	InsecureLAN bool `toml:"insecure_lan"`
}

// BindMode returns the EFFECTIVE bind selector: [remote].bind when set, else
// DefaultRemoteBind. It never returns "", so no caller has to repeat the
// fallback and there is one unambiguous value to resolve interfaces from.
func (r RemoteConfig) BindMode() string {
	if r.Bind != "" {
		return r.Bind
	}
	return DefaultRemoteBind
}

// ListenPort returns the EFFECTIVE port: [remote].port when non-zero, else
// DefaultRemotePort.
func (r RemoteConfig) ListenPort() int {
	if r.Port != 0 {
		return r.Port
	}
	return DefaultRemotePort
}

// Listens reports whether this configuration should actually bind a socket.
// Both halves matter: `enabled = false` is the off switch, and `bind = "off"`
// is the keep-my-settings-but-stop variant. Callers must not test Enabled
// alone.
func (r RemoteConfig) Listens() bool {
	return r.Enabled && r.BindMode() != "off"
}

// --- on-disk mirror --------------------------------------------------------
//
// Pointer-per-field (the [brain] / [statusagent] pattern) so Load can tell an
// ABSENT key (take the default) from an explicit zero the operator wants
// preserved. It matters for the same reason it does there: `bind = "off"` and
// `port = 0` are meaningful values a resolve step must not confuse with "unset",
// and an absent table has to stay distinguishable from a present, disabled one
// so Save never grows a [remote] block on a config that never mentioned it.

type fileRemoteConfig struct {
	Enabled     *bool   `toml:"enabled,omitempty"`
	Bind        *string `toml:"bind,omitempty"`
	Port        *int    `toml:"port,omitempty"`
	InsecureLAN *bool   `toml:"insecure_lan,omitempty"`
}

// resolveRemote materializes the [remote] table. A nil (absent) mirror yields
// the zero RemoteConfig — disabled, zero behavior change, and omitted again on
// the next Save. A present table starts from the defaults and overlays each
// explicitly-set key, so `enabled = true` alone resolves a complete, working
// configuration.
func resolveRemote(fr *fileRemoteConfig) RemoteConfig {
	if fr == nil {
		return RemoteConfig{}
	}
	r := RemoteConfig{
		Bind: DefaultRemoteBind,
		Port: DefaultRemotePort,
	}
	if fr.Enabled != nil {
		r.Enabled = *fr.Enabled
	}
	if fr.Bind != nil {
		r.Bind = *fr.Bind
	}
	if fr.Port != nil {
		r.Port = *fr.Port
	}
	if fr.InsecureLAN != nil {
		r.InsecureLAN = *fr.InsecureLAN
	}
	return r
}

// remoteFile builds the on-disk mirror for Save. A zero (unconfigured) table
// returns nil so [remote] is omitted entirely; otherwise every field is written
// explicitly so the round-trip is exact and an operator's explicit false/0/""
// survives.
func remoteFile(r RemoteConfig) *fileRemoteConfig {
	if r == (RemoteConfig{}) {
		return nil
	}
	return &fileRemoteConfig{
		Enabled:     &r.Enabled,
		Bind:        &r.Bind,
		Port:        &r.Port,
		InsecureLAN: &r.InsecureLAN,
	}
}

// validateRemote applies the static rules. It is complete validation for this
// table: [remote] needs nothing external, and deliberately does not check two
// things it could be tempted to.
//
// It does NOT reject `enabled = true` with `bind = "off"` — that pairing is the
// documented "keep my port, stop listening" state, and rejecting it would force
// an operator to delete settings in order to pause the feature.
//
// It does NOT treat `lan` or `all` as errors, under any build. Whether the
// insecure M1 path is compiled in is a property of the BINARY, and the TUI and
// the desktop app are built without that tag; a rule that depended on it would
// have those two surfaces reject a config the daemon happily runs. The forcing
// belongs to the listener, which knows which build it is.
func (c *Config) validateRemote() []error {
	var errs []error
	r := c.Remote
	if r.Port < 0 || r.Port > 65535 {
		errs = append(errs, fmt.Errorf("remote.port must be within 1..65535 (0 uses the default %d), got %d",
			DefaultRemotePort, r.Port))
	}
	if r.Bind != "" && !slices.Contains(RemoteBinds, r.Bind) && net.ParseIP(r.Bind) == nil {
		errs = append(errs, fmt.Errorf("remote.bind must be one of %v or an IP literal (empty uses %q), got %q",
			RemoteBinds, DefaultRemoteBind, r.Bind))
	}
	return errs
}
