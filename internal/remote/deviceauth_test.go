package remote

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
)

func (r *pairRig) pairedDevice(name string, relayRV string) (*DeviceKey, devices.Device) {
	r.t.Helper()
	pk := r.phoneKey()
	var rl *devices.DeviceRelay
	if relayRV != "" {
		rl = &devices.DeviceRelay{RV: relayRV, BearerHash: strings.Repeat("e", 64), RotatedAt: r.now()}
	}
	dev, err := r.reg.Add(name, "ios", pk.SPKISHA256(), protocol.DefaultTiers, rl)
	if err != nil {
		r.t.Fatal(err)
	}
	return pk, dev
}

func TestPairedCertAuthenticates(t *testing.T) {
	r := newPairRig(t)
	pk, dev := r.pairedDevice("Phone", "")
	c := r.dial(pk, ViaLAN, "", "192.0.2.7:1")
	c.start(r.auth)
	res := c.result()
	if res.err != nil || res.peer.DeviceID != dev.ID || res.peer.Label != "Phone" || res.peer.Via != ViaLAN {
		t.Fatalf("result = %+v", res)
	}
	if got, _ := r.reg.Get(dev.ID); got.LastSeenAt.IsZero() {
		t.Fatal("Touch not called")
	}
}

func TestUnpairedWithoutWindowIsNotPaired(t *testing.T) {
	r := newPairRig(t)
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.7:1")
	c.start(r.auth)
	if res := c.result(); !errors.Is(res.err, ErrUnauthenticated) {
		t.Fatalf("err = %v", res.err)
	}
	if code := errCode(t, c.frame()); code != protocol.CodeNotPaired {
		t.Fatalf("code = %q", code)
	}
}

func TestNotPairedFrameOnPostHandshakeRefusal(t *testing.T) {
	// Paired at handshake time, revoked before Authenticate runs.
	r := newPairRig(t)
	pk, dev := r.pairedDevice("Phone", "")
	c := r.dial(pk, ViaLAN, "", "192.0.2.7:1")
	if _, err := r.reg.Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	c.start(r.auth)
	if res := c.result(); !errors.Is(res.err, ErrUnauthenticated) {
		t.Fatalf("err = %v", res.err)
	}
	f := c.frame()
	if code := errCode(t, f); code != protocol.CodeNotPaired {
		t.Fatalf("code = %q", code)
	}
	var e protocol.ErrPayload
	_ = f.DecodePayload(&e)
	if e.Message != "this device is not paired" {
		t.Fatalf("message = %q", e.Message)
	}
}

func TestRelayRVMustMatchDevice(t *testing.T) {
	r := newPairRig(t)
	rv := strings.Repeat("1", 32)
	pk, dev := r.pairedDevice("Phone", rv)
	noRelay, _ := r.pairedDevice("Direct only", "")

	for name, tc := range map[string]struct {
		key *DeviceKey
		rv  string
	}{
		"another device's rv": {pk, strings.Repeat("2", 32)},
		"empty rv":            {pk, ""},
		"device with no rv":   {noRelay, rv},
	} {
		c := r.dial(tc.key, ViaRelay, tc.rv, "relay")
		c.start(r.auth)
		if res := c.result(); !errors.Is(res.err, ErrUnauthenticated) {
			t.Fatalf("%s: err = %v", name, res.err)
		}
		if code := errCode(t, c.frame()); code != protocol.CodeNotPaired {
			t.Fatalf("%s: code = %q", name, code)
		}
	}
	if !strings.Contains(r.logs.all(), "relay rv mismatch device=") {
		t.Fatalf("no mismatch log:\n%s", r.logs.all())
	}

	c := r.dial(pk, ViaRelay, rv, "relay")
	c.start(r.auth)
	res := c.result()
	if res.err != nil || res.peer.DeviceID != dev.ID || res.peer.Via != ViaRelay {
		t.Fatalf("matching rv = %+v", res)
	}
}

func TestRelayedCandidateNeedsWindowRV(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	rv := strings.Repeat("3", 32)
	r.pairer.SetWindowRelay(tk.ID, rv, strings.Repeat("4", 64))

	for name, got := range map[string]string{"other rv": strings.Repeat("5", 32), "empty rv": ""} {
		c := r.dial(r.phoneKey(), ViaRelay, got, "relay")
		c.start(r.auth)
		if res := c.result(); !errors.Is(res.err, ErrUnauthenticated) {
			t.Fatalf("%s: err = %v", name, res.err)
		}
		if code := errCode(t, c.frame()); code != protocol.CodeNotPaired {
			t.Fatalf("%s: code = %q", name, code)
		}
	}
	if st := r.status(tk.ID); st.State != protocol.PairStateWaiting {
		t.Fatalf("state = %s", st.State)
	}

	c := r.dial(r.phoneKey(), ViaRelay, rv, "relay")
	r.presentAndConfirming(t, tk, c, "Relayed")
	if st := r.status(tk.ID); st.Via != ViaRelay {
		t.Fatalf("via = %q", st.Via)
	}
}

// TestRelayedSilentCandidatesDoNotBlockTheRV: every relayed candidate shares
// one source key (the window rv), so silent ones must not hold its slots.
func TestRelayedSilentCandidatesDoNotBlockTheRV(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	rv := strings.Repeat("3", 32)
	r.pairer.SetWindowRelay(tk.ID, rv, strings.Repeat("4", 64))
	var held []*cand
	for i := 0; i < 2*maxUnpairedPerSource; i++ {
		c := r.dial(r.phoneKey(), ViaRelay, rv, "relay")
		c.start(r.auth)
		held = append(held, c)
	}
	c := r.dial(r.phoneKey(), ViaRelay, rv, "relay")
	r.presentAndConfirming(t, tk, c, "Relayed")
	for _, h := range held {
		_ = h.raw.Close()
		h.result()
	}
}

// TestSourceKeyGroupsAnIPv6Slash64: one host picks addresses inside its /64 at
// will, so they share one candidate budget; IPv4 and v4-mapped addresses key
// by address.
func TestSourceKeyGroupsAnIPv6Slash64(t *testing.T) {
	key := func(addr string) string { return sourceKey(&Handshake{RemoteAddr: addr, Via: ViaLAN}) }
	if a, b := key("[2001:db8:1:2::1]:5000"), key("[2001:db8:1:2:ffff::9%en0]:6000"); a != b {
		t.Fatalf("one /64 keyed twice: %q vs %q", a, b)
	}
	if a, b := key("[2001:db8:1:2::1]:5000"), key("[2001:db8:1:3::1]:5000"); a == b {
		t.Fatalf("two /64s share a key %q", a)
	}
	if a, b := key("192.0.2.1:1"), key("[::ffff:192.0.2.1]:2"); a != b || a != "192.0.2.1" {
		t.Fatalf("v4 keys %q vs %q", a, b)
	}
	if got := sourceKey(&Handshake{Via: ViaRelay, RelayRV: "abc"}); got != "relay:abc" {
		t.Fatalf("relay key %q", got)
	}
}

func TestBrokenRegistryAuthenticatesNobody(t *testing.T) {
	r := newPairRig(t)
	dir := t.TempDir()
	mustWrite(t, dir+"/"+devices.FileName, "{not json")
	broken := devices.Open(dir, r.now, r.logs.logf)
	if broken.Err() == nil {
		t.Fatal("registry not broken")
	}
	p := NewPairer(broken, r.now, r.logs.logf)
	p.SetDaemonSPKI(r.server.SPKISHA256())
	a := newDeviceAuthorizer(broken, p, "h", r.now, r.logs.logf)
	if _, err := p.Begin(); err != nil {
		t.Fatal(err)
	}
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.7:1")
	c.start(a)
	if res := c.result(); !errors.Is(res.err, ErrUnauthenticated) {
		t.Fatalf("err = %v", res.err)
	}
	if code := errCode(t, c.frame()); code != protocol.CodeNotPaired {
		t.Fatalf("code = %q", code)
	}
}

func TestNilTLSIsUnauthenticated(t *testing.T) {
	r := newPairRig(t)
	_, err := r.auth.Authenticate(context.Background(), &Handshake{})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthorizeFrameReadsTiersAndEpochLive(t *testing.T) {
	r := newPairRig(t)
	pk, dev := r.pairedDevice("Phone", "")
	peer := Peer{DeviceID: dev.ID, Epoch: dev.Epoch}
	status := protocol.Frame{V: 2, Type: protocol.FrameReq, Cmd: protocol.CmdSessionTimeline}
	write := protocol.Frame{V: 2, Type: protocol.FramePTY, Payload: []byte(`{"action":"write"}`)}

	if err := r.auth.AuthorizeFrame(context.Background(), peer, &write); err != nil {
		t.Fatalf("shell tier: %v", err)
	}
	// A downgrade applies on the very next frame, on the same connection.
	if _, err := r.reg.SetTiers(dev.ID, []string{protocol.TierRead}); err != nil {
		t.Fatal(err)
	}
	if err := r.auth.AuthorizeFrame(context.Background(), peer, &write); !errors.Is(err, ErrDenied) {
		t.Fatalf("after downgrade: %v", err)
	}
	if err := r.auth.AuthorizeFrame(context.Background(), peer, &status); err != nil {
		t.Fatalf("read after downgrade: %v", err)
	}
	// Never-tier commands are denied to every device.
	never := protocol.Frame{V: 2, Type: protocol.FrameReq, Cmd: protocol.CmdPairBegin}
	if err := r.auth.AuthorizeFrame(context.Background(), peer, &never); !errors.Is(err, ErrDenied) {
		t.Fatalf("never tier: %v", err)
	}
	// A re-pair (new epoch) or a revocation ends the old connection.
	if _, err := r.reg.Add("Phone", "ios", pk.SPKISHA256(), protocol.DefaultTiers, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.auth.AuthorizeFrame(context.Background(), peer, &status); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after re-pair: %v", err)
	}
	if _, err := r.reg.Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.auth.AuthorizeFrame(context.Background(), peer, &status); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after revoke: %v", err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
