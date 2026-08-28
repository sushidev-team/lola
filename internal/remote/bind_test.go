package remote

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

// noIfaces is the enumerator for the modes that must never consult one. A mode
// that reached it would be resolving an interface list it has no business
// reading.
func noIfaces() ([]NetIface, error) {
	return nil, errors.New("interfaces must not be enumerated for this mode")
}

func TestResolveBindModes(t *testing.T) {
	cases := []struct {
		name string
		mode string
		port int
		want []BindAddr
		err  error
	}{
		{
			name: "localhost binds both loopback families",
			mode: "localhost", port: 7717,
			// Binding 127.0.0.1 alone is a support question waiting to happen:
			// a client that resolves localhost to ::1 finds nothing there.
			want: []BindAddr{{Addr: "127.0.0.1:7717"}, {Addr: "[::1]:7717"}},
		},
		{
			name: "the empty mode is localhost",
			mode: "", port: 7717,
			want: []BindAddr{{Addr: "127.0.0.1:7717"}, {Addr: "[::1]:7717"}},
		},
		{
			name: "all is one dual-stack listener",
			mode: "all", port: 7717,
			want: []BindAddr{{Addr: "[::]:7717"}},
		},
		{
			name: "an IPv4 literal binds exactly that address",
			mode: "192.168.1.5", port: 7717,
			want: []BindAddr{{Addr: "192.168.1.5:7717"}},
		},
		{
			name: "an IPv6 literal binds exactly that address",
			mode: "fd00::1", port: 7717,
			want: []BindAddr{{Addr: "[fd00::1]:7717"}},
		},
		{
			name: "off binds nothing",
			mode: "off", port: 7717,
			err: ErrBindOff,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveBind(tc.mode, tc.port, noIfaces)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("got err %v, want %v", err, tc.err)
				}
				if got != nil {
					t.Errorf("got addresses %v alongside the error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBind: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestResolveBindRefusesAHostname pins the offline rule: turning a listener
// bind into a DNS lookup would make a startup path depend on the network and
// make the answer unstable between runs.
func TestResolveBindRefusesAHostname(t *testing.T) {
	for _, mode := range []string{"myhost.local", "lola.example.com", "LOCALHOST", "0.0.0.0/0", "lan "} {
		if _, err := resolveBind(mode, 7717, noIfaces); err == nil {
			t.Errorf("bind %q was accepted; only the four keywords and an IP literal are", mode)
		}
	}
}

func TestResolveBindRefusesAnImpossiblePort(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 1 << 20} {
		if _, err := resolveBind("localhost", port, noIfaces); err == nil {
			t.Errorf("port %d was accepted", port)
		}
	}
}

// TestLanSkipsTunnelsVirtualBridgesAndPublicAddresses is the whole point of the
// "lan" mode's exclusion set: a VPN, a container bridge or this machine's own
// hotspot is a different population from the network the operator joined, and a
// globally routable address is not a LAN at all.
func TestLanSkipsTunnelsVirtualBridgesAndPublicAddresses(t *testing.T) {
	ifaces := func() ([]NetIface, error) {
		return []NetIface{
			{Name: "lo0", Flags: net.FlagUp | net.FlagLoopback, IPs: []net.IP{net.ParseIP("127.0.0.1")}},
			{Name: "en0", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("192.168.1.20"), net.ParseIP("fd00::20")}},
			{Name: "en1", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("203.0.113.7")}},
			{Name: "en2", IPs: []net.IP{net.ParseIP("192.168.9.9")}}, // down
			{Name: "utun4", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("100.64.0.3"), net.ParseIP("10.9.9.9")}},
			{Name: "bridge100", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("192.168.64.1")}},
			{Name: "docker0", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("172.17.0.1")}},
			{Name: "vmenet0", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("192.168.70.1")}},
			{Name: "ap1", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("192.168.2.1")}},
		}, nil
	}
	got, err := resolveBind("lan", 7717, ifaces)
	if err != nil {
		t.Fatalf("resolveBind: %v", err)
	}
	want := []BindAddr{
		{Addr: "192.168.1.20:7717", Iface: "en0"},
		{Addr: "[fd00::20]:7717", Iface: "en0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestLanZonesIPv6LinkLocal: a link-local IPv6 address is meaningless without
// the interface it was found on.
func TestLanZonesIPv6LinkLocal(t *testing.T) {
	ifaces := func() ([]NetIface, error) {
		return []NetIface{{Name: "en0", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("fe80::1")}}}, nil
	}
	got, err := resolveBind("lan", 7717, ifaces)
	if err != nil {
		t.Fatalf("resolveBind: %v", err)
	}
	want := []BindAddr{{Addr: "[fe80::1%en0]:7717", Iface: "en0"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestLanWithNoQualifyingInterfaceIsAnError, not an empty success: a listener
// that binds nothing and reports success is a daemon that looks healthy and
// cannot be reached.
func TestLanWithNoQualifyingInterfaceIsAnError(t *testing.T) {
	ifaces := func() ([]NetIface, error) {
		return []NetIface{{Name: "utun0", Flags: net.FlagUp, IPs: []net.IP{net.ParseIP("10.0.0.1")}}}, nil
	}
	if _, err := resolveBind("lan", 7717, ifaces); !errors.Is(err, ErrNoBindAddrs) {
		t.Fatalf("got %v, want ErrNoBindAddrs", err)
	}
}

// TestLanFailsClosedWhenInterfacesCannotBeRead.
func TestLanFailsClosedWhenInterfacesCannotBeRead(t *testing.T) {
	boom := errors.New("no route to anything")
	if _, err := resolveBind("lan", 7717, func() ([]NetIface, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("got %v, want the enumeration error", err)
	}
	if _, err := resolveBind("lan", 7717, nil); err == nil {
		t.Fatal("a nil enumerator was accepted")
	}
}

// TestLoopbackAddr backs the insecure build's forced-bind assertion: a bind is
// safe because every ADDRESS it produced is loopback, never because the mode
// happened to read "localhost".
func TestLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:7717":     true,
		"[::1]:7717":         true,
		"127.0.0.53:7717":    true,
		"[::]:7717":          false,
		"192.168.1.5:7717":   false,
		"[fe80::1%en0]:7717": false,
		"garbage":            false,
	}
	for addr, want := range cases {
		if got := loopbackAddr(addr); got != want {
			t.Errorf("loopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}
