//go:build lola_insecure

package daemon

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
)

// cmd=pairBegin, the M1 build.
//
// This answers "how does a phone reach you" with the facts of the LISTENER THAT
// IS RUNNING, which is the only reason the question is put to the daemon at all.
// Every other place the same four values exist is a copy that can be stale:
// ~/.lola/remote-dev-key is the key only when the script that wrote it also
// started this daemon (a `lola run` from another shell, or the TUI's ^r
// re-exec, carries whatever that shell exported), the SPKI pin in the startup
// log is the last listener's rather than this one's, and a config file's port is
// what was asked for rather than what was bound. A desktop that rendered a code
// from any of those would produce a scan the daemon answers with "authenticate
// first" — indistinguishable, from the phone, from a bad camera read.
//
// The file is TAG-SPLIT for the same reason remotelisten.go is, and the split is
// at the handler rather than only inside internal/remote (whose InsecureKey
// returns "" in an ordinary build anyway) because the two absences are not the
// same thing: a release binary built without the tag contains no reachable code
// that assembles a bearer key into a response, so the command is absent by
// construction rather than by behaviour. pairbegin_off.go is what it gets.
//
// The name is mobile/PLAN.md's: the desktop's "connect a phone" affordance calls
// cmd=pairBegin, and M2 changes what that command RETURNS (a pairing window with
// a qr_secret) rather than what it is called. It matters that the name is the
// reserved one — internal/remote's deniedCommands has listed pairBegin since
// before it existed, so adding the case here grants a remote peer exactly
// nothing, whereas inventing a fresh name would have been remotely reachable
// the moment it was wired and would have had to be denied by hand.
func (d *Daemon) handlePairBegin(_ context.Context) (protocol.PairBeginData, error) {
	// Lock order is remoteMu -> d.mu and never the reverse, so the config is
	// read and RELEASED before the listener is looked at — the same shape
	// startRemote uses.
	d.mu.Lock()
	listens := d.cfg.Remote.Listens()
	d.mu.Unlock()

	d.remoteMu.Lock()
	srv := d.remote
	lastErr := d.remoteErr
	d.remoteMu.Unlock()

	if srv == nil {
		// A rendered state, not an error: the caller is a human who pressed a
		// button and needs to know which of the two things to fix.
		if !listens {
			return protocol.PairBeginData{
				Problem: "The phone listener is off. Enable [remote] and reload before connecting a phone.",
			}, nil
		}
		if lastErr != "" {
			// The REASON the listener refused, rather than a guess at one. An
			// earlier version sent the operator to the log to find an address
			// that could not be bound, which was wrong the first time it
			// mattered: the cause was a missing bearer key, and no address had
			// been attempted at all.
			return protocol.PairBeginData{
				Problem: "[remote] is enabled but the listener did not start: " + lastErr,
			}, nil
		}
		return protocol.PairBeginData{
			Problem: "[remote] is enabled but no listener came up, and the daemon recorded no reason — " +
				"it may still be starting. The daemon log has the detail.",
		}, nil
	}

	hosts, port := listenerDial(srv.Addrs())
	if len(hosts) == 0 || port == 0 {
		return protocol.PairBeginData{
			Problem: "The listener is up but reported no address to connect to.",
		}, nil
	}

	key := srv.InsecureKey()
	if key == "" {
		// Reachable when a Server was built with some other authorizer. The
		// tagged build has only the one, so this is the honest refusal rather
		// than a dead branch: a listener that does not authenticate with a
		// shared key has no key to put in a code.
		return protocol.PairBeginData{
			Hosts:   hosts,
			Port:    port,
			Pin:     srv.SPKIPin(),
			Problem: "This listener does not authenticate with a shared key, so there is nothing to put in a code.",
		}, nil
	}

	code, err := remote.EncodeConnectCode(remote.ConnectCode{
		Addrs: hosts,
		Port:  port,
		Pin:   srv.SPKIPin(),
		Key:   key,
	})
	if err != nil {
		// EncodeConnectCode never puts a field VALUE in its error, so this is
		// safe to return verbatim; it names which field was missing.
		return protocol.PairBeginData{}, fmt.Errorf("pairBegin: %w", err)
	}

	// Nothing is logged here, at any level. d.logf reaches ~/.lola/daemon.log
	// AND stderr, and the code contains the bearer key.
	return protocol.PairBeginData{
		Code:     code,
		Hosts:    hosts,
		Port:     port,
		Pin:      srv.SPKIPin(),
		Key:      key,
		Insecure: true,
	}, nil
}

// listenerDial reduces the bound addresses to the host halves a client dials —
// in bind order, without duplicates — and the port they were taken on.
//
// It reads what was BOUND rather than what was configured, which is what makes
// the loopback rail visible instead of assumed: an M1 daemon without the LAN
// opt-in overrides any non-loopback bind, so the answer is 127.0.0.1 and ::1
// whatever [remote] says, and an address the daemon cannot deliver is never
// offered to a phone.
//
// A WILDCARD is the one thing it does not pass through. bind = "all" binds
// 0.0.0.0 and [::], and neither can be dialled: handing a phone "::" produces a
// code that scans perfectly and then times out, which is the worst failure
// shape available because it looks like every other network problem. So an
// unspecified address is REPLACED by the machine's own reachable addresses —
// what "every interface" meant — with loopback appended, since a Simulator
// shares the Mac's loopback and is the one client for which it is the right
// answer. The phone's chooseAddress ranks loopback last, so ordering here only
// has to put the routable ones in the list.
//
// An address it cannot split is DROPPED rather than passed through: the host is
// about to be dialled by a client, and half of a host:port pair would fail with
// less context than simply not offering it. The port is read from the FIRST
// address that parsed, for the same reason: every listener of one Server binds
// the one port, and reading it back out of a bound address rather than out of
// the config is what makes the answer describe reality.
func listenerDial(addrs []remote.BindAddr) ([]string, int) {
	out := make([]string, 0, len(addrs))
	seen := map[string]bool{}
	port := 0
	add := func(host string) {
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		out = append(out, host)
	}
	for _, ba := range addrs {
		host, p, err := net.SplitHostPort(ba.Addr)
		if err != nil || host == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			continue
		}
		if port == 0 {
			port = n
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			for _, h := range remote.ReachableHosts() {
				add(h)
			}
			// Loopback last and always, so a Simulator still has something to
			// dial even on a machine with no private interface at all.
			add("127.0.0.1")
			continue
		}
		// A ZONE-SCOPED address is dropped, not offered. "fe80::1%en0" names an
		// interface on THIS machine; the zone is meaningless on the phone and
		// the address without it is ambiguous. bind = "lan" binds them because
		// they are real local addresses, and a Mac has many — so without this
		// the connect code leads with a string no other device can dial, and
		// the desktop shows it as THE address because it shows the first one.
		if strings.Contains(host, "%") {
			continue
		}
		add(host)
	}
	return out, port
}
