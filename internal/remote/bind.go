package remote

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Resolving [remote].bind into the actual addresses to listen on.
//
// The mode's NAME will be read as a guarantee, so each one means exactly one
// thing and nothing is inferred:
//
//	off        nothing is bound. Distinct from enabled = false only in intent.
//	localhost  loopback only — what an SSH forward, a tunnel or a tailnet
//	           wants, and the only mode that puts no listener on a network
//	           interface at all.
//	lan        only interfaces whose addresses are private (RFC1918, ULA,
//	           link-local) AND whose names are not tunnels or virtual bridges,
//	           so a VPN, an OrbStack bridge or a hotspot interface does not
//	           silently become a listener. On a laptop this still means every
//	           network the machine ever joins, conference WiFi included, which
//	           is why the bound interfaces are logged BY NAME at startup: which
//	           interfaces it actually took is otherwise unanswerable.
//	all        0.0.0.0 and [::]. Never a default.
//	<IP>       exactly that address.
//
// Resolution is OFFLINE, like config's validation of the same key: a hostname
// is refused rather than resolved, because turning a listener bind into a DNS
// lookup makes a startup path depend on the network and makes the answer
// unstable between runs.

// ErrBindOff is returned when the mode is "off". It is an error rather than an
// empty address list so a caller cannot mistake "the operator turned it off"
// for "nothing matched", which is the far more alarming outcome that mode "lan"
// can legitimately produce.
var ErrBindOff = errors.New("remote: bind is off")

// ErrNoBindAddrs is returned when a mode resolved to no address at all — a
// "lan" bind on a machine with no qualifying interface, most plausibly a
// laptop with WiFi off. It is an error because a listener that binds nothing
// and reports success is a daemon that looks healthy and cannot be reached.
var ErrNoBindAddrs = errors.New("remote: no interface matched the bind mode")

// excludedIfacePrefixes are interface NAME prefixes that never qualify for
// "lan", however private their addresses look. Each one is a network this
// machine bridges or tunnels rather than one it joined: a VPN (utun, tun, tap),
// a container or VM bridge (bridge, docker, vmenet), or the interface that
// serves this machine's own hotspot (ap). Binding a listener to any of them
// exposes it to a different population than the operator had in mind when they
// wrote "lan".
var excludedIfacePrefixes = []string{"utun", "tun", "tap", "bridge", "docker", "vmenet", "ap"}

// BindAddr is one address the listener will bind, with the interface it came
// from so the startup log can name it. Iface is "" for the modes that name an
// address directly rather than an interface.
type BindAddr struct {
	Addr  string // host:port, ready for net.Listen
	Iface string
}

// NetIface is the interface-enumeration seam's output: enough of a net.Interface
// to decide, and no methods, so a test can describe a machine without having
// one. The production enumerator is systemIfaces.
type NetIface struct {
	Name  string
	Flags net.Flags
	IPs   []net.IP
}

// systemIfaces is the production enumerator.
func systemIfaces() ([]NetIface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]NetIface, 0, len(ifs))
	for _, ifi := range ifs {
		addrs, err := ifi.Addrs()
		if err != nil {
			// One unreadable interface must not cost the whole enumeration:
			// it simply cannot qualify.
			continue
		}
		ni := NetIface{Name: ifi.Name, Flags: ifi.Flags}
		for _, a := range addrs {
			switch v := a.(type) {
			case *net.IPNet:
				ni.IPs = append(ni.IPs, v.IP)
			case *net.IPAddr:
				ni.IPs = append(ni.IPs, v.IP)
			}
		}
		out = append(out, ni)
	}
	return out, nil
}

// resolveBind turns a bind mode and a port into the addresses to listen on.
// ifaces is the enumeration seam and is consulted only by mode "lan".
//
// The result is ORDERED and deduplicated, so a startup log and a test read the
// same way twice.
func resolveBind(mode string, port int, ifaces func() ([]NetIface, error)) ([]BindAddr, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("remote: port %d is out of range", port)
	}
	p := strconv.Itoa(port)
	switch mode {
	case "off":
		return nil, ErrBindOff

	case "localhost", "":
		// Both loopback families, because a client may resolve "localhost" to
		// either and a phone forwarding over SSH may pick the one lola did not
		// bind. Listening on 127.0.0.1 alone is a support question waiting to
		// happen.
		return []BindAddr{
			{Addr: net.JoinHostPort("127.0.0.1", p)},
			{Addr: net.JoinHostPort("::1", p)},
		}, nil

	case "all":
		// One dual-stack listener: Go binds :: with IPV6_V6ONLY off, so a
		// second 0.0.0.0 listener on the same port would fail to bind on most
		// systems rather than add anything.
		return []BindAddr{{Addr: net.JoinHostPort("::", p)}}, nil

	case "lan":
		return lanBinds(p, ifaces)
	}

	ip := net.ParseIP(mode)
	if ip == nil {
		// A hostname lands here. config.Validate rejects it too, but this path
		// is reached with whatever is in the file, and guessing at a name is
		// exactly what the offline rule forbids.
		return nil, fmt.Errorf("remote: bind %q is neither a known mode nor an IP literal", mode)
	}
	return []BindAddr{{Addr: net.JoinHostPort(ip.String(), p)}}, nil
}

// lanBinds selects the private addresses of real, up, non-tunnel interfaces.
func lanBinds(port string, ifaces func() ([]NetIface, error)) ([]BindAddr, error) {
	if ifaces == nil {
		return nil, errors.New("remote: no interface enumerator")
	}
	list, err := ifaces()
	if err != nil {
		return nil, fmt.Errorf("remote: enumerate interfaces: %w", err)
	}
	var out []BindAddr
	seen := map[string]bool{}
	for _, ifi := range list {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if excludedIface(ifi.Name) {
			continue
		}
		for _, ip := range ifi.IPs {
			if !privateIP(ip) {
				continue
			}
			host := ip.String()
			if ip.To4() == nil && ip.IsLinkLocalUnicast() {
				// An IPv6 link-local address is only meaningful with its zone,
				// and the zone is the interface it was found on.
				host += "%" + ifi.Name
			}
			addr := net.JoinHostPort(host, port)
			if seen[addr] {
				continue
			}
			seen[addr] = true
			out = append(out, BindAddr{Addr: addr, Iface: ifi.Name})
		}
	}
	if len(out) == 0 {
		return nil, ErrNoBindAddrs
	}
	return out, nil
}

// excludedIface reports whether an interface name is one of the tunnels or
// virtual bridges "lan" never takes.
func excludedIface(name string) bool {
	n := strings.ToLower(name)
	for _, p := range excludedIfacePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// privateIP reports whether ip is on a network the operator plausibly meant by
// "lan": RFC1918 and its IPv6 equivalents (ULA), plus link-local, which is what
// a directly-cabled or AWDL-adjacent peer arrives on. Everything else —
// including a globally routable address handed out by an ISP — is refused,
// because "lan" must never resolve to a public listener by accident.
func privateIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return false
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// loopbackAddr reports whether an address resolved by resolveBind is loopback.
// It is what the insecure build's forced-localhost assertion is written
// against: a bind is safe when every address it produced is loopback, not when
// the MODE happened to read "localhost".
func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
