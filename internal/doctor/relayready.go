package doctor

import (
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
)

// checkRemoteRelay names the hub-relay row: can a phone reach this machine
// off the LAN/VPN? Never critical.
const checkRemoteRelay = "phone relay"

// RelayResult reports the relay while [remote] is enabled; the bool is false
// otherwise. dir is the hub state dir. daemonUp is the daemon check's OK: the
// status file is read only while a daemon answers, because a crashed daemon
// leaves its last state behind. Details come from fixed sentences and the
// closed reason vocabulary the daemon writes; the status file's free-text
// error is never shown.
func RelayResult(cfg *config.Config, dir string, daemonUp bool) (Result, bool) {
	if cfg == nil || !cfg.Remote.Enabled {
		return Result{}, false
	}
	row := func(ok bool, detail string, fix FixID) (Result, bool) {
		return Result{Name: checkRemoteRelay, OK: ok, Detail: detail, Fix: fix}, true
	}
	if cfg.Remote.RelayOff {
		return row(true, "off: phones connect on LAN/VPN only", FixNone)
	}
	if !daemonUp {
		return row(false, "daemon not running; relay state unknown", FixStartDaemon)
	}
	local := hub.LocalStatus(cfg, dir)
	if local.Revoked != nil {
		return row(false, "the hub refused this device", FixRejoinHub)
	}
	if !local.Joined {
		return row(false, "join a hub to reach phones off-network", FixJoinHub)
	}
	st, ok := hub.LoadRelayStatus(dir)
	if !ok {
		return row(true, "relay state not reported yet", FixNone)
	}
	switch st.State {
	case "refused":
		return row(false, "the hub refused this device", FixRejoinHub)
	case "unsupported":
		return row(false, "this hub has no relay; it needs an upgrade", FixRelayHubUpgrade)
	case "backoff":
		class := st.Reason
		if class == "" {
			class = "unreachable"
		}
		return row(false, "cannot reach the hub relay: "+class, FixNone)
	case "off":
		if st.Reason == "no_phones" {
			return row(true, "no paired phone", FixNone)
		}
		return row(true, "off", FixNone)
	case "connected", "syncing", "connecting":
		return row(true, st.State, FixNone)
	}
	return row(true, "relay state not reported yet", FixNone)
}
