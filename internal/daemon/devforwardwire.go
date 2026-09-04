package daemon

// Publishing an ACTIVE session's dev servers on the local network, so a phone
// can open the app its agent is building.
//
// THE PROBLEM. internal/devurl already knows a session's dev addresses — it
// reads what the dev tabs printed — and they are all loopback, because that is
// what `php artisan serve` and vite bind by default. No address typed into a
// phone browser reaches them, and the fix every framework suggests (bind
// 0.0.0.0) publishes a half-built application, with whatever auth it has so far
// and an agent actively writing to it, to every peer on a network the laptop
// did not choose, permanently, on a well-known port.
//
// WHAT THIS DOES INSTEAD. While a session is the ACTIVE one, each of its
// loopback dev addresses gets a listener on one private interface, on a random
// high port, published as SessionInfo.DevForwards. It ends when the session
// stops being active, when its dev tabs change, when the daemon stops, or when
// the operator turns the key off. The exposure is real and narrow rather than
// permanent and broad; mobile/PLAN.md's M7 tunnel is what removes it entirely,
// and this is the ninety per cent that ships first.
//
// OFF UNLESS ASKED FOR ([remote].dev_forward), like every other key in this
// repo that puts something on a network.
//
// THE RAIL. A forward's target is only ever a string that was already in this
// session's DevURLs, which the daemon derived itself from its own dev tabs. No
// client names a port. Without that rail a caller picks the address and this
// becomes a proxy into everything on the Mac's loopback — Postgres, Redis,
// every other project's dev server, and lola's own unix socket.

import (
	"io"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/sushidev-team/lola/internal/devforward"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
)

// liveForward is one published dev server, as the daemon tracks it.
//
// The closer rather than *devforward.Forward, so the seam below can be
// satisfied without opening a socket. A test that had to construct a real
// Forward would need a constructor in the library that exists only for tests —
// and a library that carries test scaffolding is how the scaffolding ends up in
// production paths.
type liveForward struct {
	addr   string
	target string
	closer io.Closer
}

// maxDevForwards bounds how many listeners exist at once.
//
// One session's dev commands print a handful of addresses (an app and a
// bundler, typically), and only the active session is forwarded — so reaching
// this cap means something is wrong rather than busy, and a bound is cheaper
// than discovering the file-descriptor limit.
const maxDevForwards = 8

// devForwardKey identifies a forward: the session it belongs to and the exact
// loopback address it publishes.
type devForwardKey struct {
	session string
	target  string
}

// syncDevForwards brings the live forwards in line with what is active.
//
// Called from the observe loop after the dev state is derived, so it runs on
// facts (tmux said these tabs are alive, the panes printed these addresses)
// rather than on intent. Everything it does is idempotent: a cycle that changes
// nothing opens and closes nothing.
func (d *Daemon) syncDevForwards() {
	d.mu.Lock()
	on := d.cfg.Remote.DevForward
	d.mu.Unlock()

	want := map[devForwardKey]string{} // key -> the http(s) URL it came from
	if on {
		for _, s := range d.sessions.Snapshot() {
			if !s.DevActive {
				continue
			}
			for _, raw := range s.DevURLs {
				target, ok := loopbackTarget(raw)
				if !ok {
					continue
				}
				want[devForwardKey{session: s.ID, target: target}] = raw
			}
		}
	}

	d.forwardMu.Lock()
	defer d.forwardMu.Unlock()

	// CLOSE FIRST. A session that stopped being active, or whose dev tabs
	// changed, must lose its listener before anything else takes its place —
	// and closing frees the descriptors the opens below might need.
	for key, fwd := range d.forwards {
		if _, keep := want[key]; keep {
			continue
		}
		_ = fwd.closer.Close()
		delete(d.forwards, key)
		d.logf("", "remote: stopped publishing %s for %s", fwd.target, key.session)
	}

	if len(want) == 0 {
		d.publishForwardURLs()
		return
	}

	host := d.forwardBindHost()
	if host == "" {
		// No private address to publish on: the machine is on no network, or
		// only on interfaces the bind rules exclude. Nothing to do and nothing
		// to warn about — the addresses are still reachable on the Mac itself.
		d.publishForwardURLs()
		return
	}

	// Sorted, so a cap that is hit truncates the same way twice rather than by
	// map order.
	keys := make([]devForwardKey, 0, len(want))
	for key := range want {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].session != keys[j].session {
			return keys[i].session < keys[j].session
		}
		return keys[i].target < keys[j].target
	})

	for _, key := range keys {
		if _, live := d.forwards[key]; live {
			continue
		}
		if len(d.forwards) >= maxDevForwards {
			d.logf("", "remote: not publishing %s: already at %d forwards", key.target, maxDevForwards)
			break
		}
		addr, closer, err := d.openForward(host, key.target)
		if err != nil {
			// Best-effort, like every other thing that puts a socket on a
			// network here: a port that cannot be bound costs one link, never
			// the session or the cycle.
			d.logf("", "remote: cannot publish %s: %v", key.target, err)
			continue
		}
		if d.forwards == nil {
			d.forwards = map[devForwardKey]liveForward{}
		}
		d.forwards[key] = liveForward{addr: addr, target: key.target, closer: closer}
		d.logf("", "remote: publishing %s at %s for %s", key.target, addr, key.session)
	}
	d.publishForwardURLs()
}

// openForward is the seam over internal/devforward, so tests need no listener.
// It answers the address a browser goes to and a closer, which is everything
// the daemon does with a forward.
func (d *Daemon) openForward(host, target string) (string, io.Closer, error) {
	if d.forwardOpen != nil {
		return d.forwardOpen(host, target)
	}
	f, err := devforward.Open(host, target)
	if err != nil {
		return "", nil, err
	}
	return f.Addr, f, nil
}

// publishForwardURLs writes what is live onto the session records, so every
// client sees the same list. Caller holds forwardMu.
func (d *Daemon) publishForwardURLs() {
	bySession := map[string][]session.DevForward{}
	for key, fwd := range d.forwards {
		bySession[key.session] = append(bySession[key.session], session.DevForward{
			URL:  "http://" + fwd.addr,
			From: fwd.target,
		})
	}
	for id := range bySession {
		// By the ORIGINAL address, so the order a person sees follows the ports
		// they know rather than whatever the kernel handed out.
		sort.Slice(bySession[id], func(i, j int) bool {
			return bySession[id][i].From < bySession[id][j].From
		})
	}
	for _, s := range d.sessions.Snapshot() {
		urls := bySession[s.ID]
		d.sessions.Update(s.ID, func(rec *session.Session) bool {
			if equalForwards(rec.DevForwards, urls) {
				return false
			}
			rec.DevForwards = urls
			return true
		})
	}
}

func equalForwards(a, b []session.DevForward) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// forwardBindHost picks the address a forward is published on.
//
// The SAME filtered set internal/remote's "lan" mode binds — private, and not a
// VPN, container bridge, hotspot or AirDrop interface — so a forward appears
// exactly where the phone already reaches the daemon and nowhere else. IPv4 is
// preferred because it is what a human types.
func (d *Daemon) forwardBindHost() string {
	hosts := remote.ReachableHosts()
	if d.forwardHosts != nil {
		hosts = d.forwardHosts()
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil && ip.To4() != nil {
			return h
		}
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil && !strings.Contains(h, "%") {
			return h
		}
	}
	return ""
}

// loopbackTarget turns one of internal/devurl's http(s) URLs into a host:port
// this can dial, and reports whether it is publishable at all.
//
// It re-checks that the host is loopback even though devurl only ever produces
// loopback URLs: this is the value a listener is opened for, and the rail is
// worth restating where it is used rather than trusting a package two hops away
// to have meant what it says.
func loopbackTarget(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	host := u.Hostname()
	// "localhost" is accepted as the ONE name, and resolved here rather than at
	// dial time. Every other name is refused: resolving it would make this check
	// a DNS lookup whose answer can change between the check and the dial, which
	// is the shape of every TOCTOU bug, and a name that resolves off-box would
	// turn a forward into a proxy for another machine.
	//
	// It is not an edge case. vite prints "Local: http://localhost:5175/" and
	// internal/devurl carries that through verbatim, so refusing the name meant
	// a session's bundler was silently the one address that never appeared —
	// which reads as a broken feature rather than as a rule being enforced.
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", false
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port), true
}

// stopDevForwards closes every listener. Called on shutdown, and safe to call
// twice.
func (d *Daemon) stopDevForwards() {
	d.forwardMu.Lock()
	defer d.forwardMu.Unlock()
	for key, fwd := range d.forwards {
		_ = fwd.closer.Close()
		delete(d.forwards, key)
	}
}

// devForwardsFor reports the published URLs of one session, for tests and for
// anything that needs them without a store read.
func (d *Daemon) devForwardsFor(sessionID string) []string {
	d.forwardMu.Lock()
	defer d.forwardMu.Unlock()
	var out []string
	for key, fwd := range d.forwards {
		if key.session == sessionID {
			out = append(out, "http://"+fwd.addr)
		}
	}
	sort.Strings(out)
	return out
}

// devForwardInfos maps the record's pairs onto the wire's.
func devForwardInfos(in []session.DevForward) []protocol.DevForward {
	if len(in) == 0 {
		return nil
	}
	out := make([]protocol.DevForward, 0, len(in))
	for _, f := range in {
		out = append(out, protocol.DevForward{URL: f.URL, From: f.From})
	}
	return out
}
