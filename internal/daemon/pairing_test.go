package daemon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/mdns"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
)

// startPairingListener brings a real loopback listener up. The pairing
// handlers answer about the listener that is RUNNING, so a stubbed server
// would test the opposite of the claim.
func startPairingListener(t *testing.T, d *Daemon) {
	t.Helper()
	d.shutdownCtx = context.Background()
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }
	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatalf("no listener: %s", d.remoteErr)
	}
}

func newPhoneCert(t *testing.T) (tls.Certificate, [32]byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "phone"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, priv.Public(), priv)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, devices.HashSPKI(leaf.RawSubjectPublicKeyInfo)
}

// beginLoopback opens a pairing on a loopback-only listener (the test path:
// AllowLoopback) and returns the decoded payload.
func beginLoopback(t *testing.T, d *Daemon) (protocol.PairBeginData, remote.PairPayload) {
	t.Helper()
	got, err := d.handlePairBegin(context.Background(), protocol.PairBeginArgs{AllowLoopback: true})
	if err != nil || got.Problem != "" {
		t.Fatalf("pairBegin: err=%v problem=%q", err, got.Problem)
	}
	pl, err := remote.DecodePairPayload(got.Payload)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	return got, pl
}

// presentPhone plays the phone's side up to the confirming state and returns
// the code the phone would show.
func presentPhone(t *testing.T, d *Daemon, pl remote.PairPayload, id, name string) string {
	t.Helper()
	cert, phoneHash := newPhoneCert(t)
	c := dialRemote(t, d.remote.Addrs()[0].Addr, cert)
	cs := c.conn.(*tls.Conn).ConnectionState()
	secret, err := base64.RawURLEncoding.DecodeString(pl.Secret)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := cs.ExportKeyingMaterial("EXPORTER-lola-pair-proof-v1", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	c.send(t, protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "p1", Cmd: protocol.CmdRemotePair},
		protocol.PairRequest{Proof: base64.RawURLEncoding.EncodeToString(protocol.PairProof(secret, ex)), Name: name, Platform: "ios"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := d.handlePairStatus(ctx, protocol.PairStatusArgs{Pairing: id, Since: protocol.PairStateWaiting, WaitMs: 8000})
	if err != nil || st.State != protocol.PairStateConfirming {
		t.Fatalf("status = %+v err=%v, want confirming", st, err)
	}
	exc, err := cs.ExportKeyingMaterial("EXPORTER-lola-pair-code-v1", nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := base64.StdEncoding.DecodeString(pl.Pin)
	if err != nil || len(pin) != 32 {
		t.Fatalf("pin: %v", err)
	}
	return protocol.PairCode(exc, [32]byte(pin), phoneHash)
}

func TestPairBeginProblemWhenListenerOff(t *testing.T) {
	d := newRemoteTestDaemon(t)
	got, err := d.handlePairBegin(context.Background(), protocol.PairBeginArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Problem == "" || got.Payload != "" || got.Pairing != "" {
		t.Fatalf("got %+v, want only a Problem", got)
	}
}

func TestPairBeginReportsWhyTheListenerRefused(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	d.remoteErr = "address already in use"
	got, _ := d.handlePairBegin(context.Background(), protocol.PairBeginArgs{})
	if !strings.Contains(got.Problem, "address already in use") {
		t.Fatalf("problem = %q", got.Problem)
	}
}

func TestPairBeginLoopbackOnlyProblem(t *testing.T) {
	d := newRemoteTestDaemon(t)
	startPairingListener(t, d)
	got, err := d.handlePairBegin(context.Background(), protocol.PairBeginArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Problem, "only accepts this Mac") || got.Payload != "" || got.Pairing != "" {
		t.Fatalf("got %+v, want the loopback Problem and no QR", got)
	}
}

func TestPairBeginAllowLoopback(t *testing.T) {
	d := newRemoteTestDaemon(t)
	startPairingListener(t, d)
	got, pl := beginLoopback(t, d)
	if got.Pin != d.remote.SPKIPin() || pl.Pin != got.Pin {
		t.Errorf("pin %q is not the listener's %q", got.Pin, d.remote.SPKIPin())
	}
	if got.Port != d.remote.Port() || pl.Port != got.Port {
		t.Errorf("port %d, want %d", got.Port, d.remote.Port())
	}
	if !slices.Contains(pl.Addrs, "127.0.0.1") || got.Pairing == "" || got.Relay || pl.Relay != nil {
		t.Errorf("payload = %+v data = %+v", pl, got)
	}
	if got.ExpiresAt.IsZero() {
		t.Error("no expiry")
	}
}

func TestPairBeginNonLoopbackFirst(t *testing.T) {
	hosts, port := listenerDial([]remote.BindAddr{
		{Addr: "127.0.0.1:7717"},
		{Addr: "[::1]:7717"},
		{Addr: "192.168.20.3:7717"},
		{Addr: "[fe80::1%en0]:7717"},
		{Addr: "10.0.0.5:7717"},
		{Addr: "bogus"},
	})
	if port != 7717 {
		t.Fatalf("port = %d", port)
	}
	want := []string{"192.168.20.3", "10.0.0.5", "127.0.0.1", "::1"}
	if !slices.Equal(hosts, want) {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}
}

func TestListenerDialReplacesAWildcardWithReachableAddresses(t *testing.T) {
	hosts, port := listenerDial([]remote.BindAddr{{Addr: "0.0.0.0:7717"}})
	if port != 7717 || !slices.Contains(hosts, "127.0.0.1") {
		t.Fatalf("hosts=%v port=%d", hosts, port)
	}
	if hosts[len(hosts)-1] != "127.0.0.1" {
		t.Errorf("loopback is not last: %v", hosts)
	}
	for _, h := range hosts {
		if h == "0.0.0.0" || strings.Contains(h, "%") {
			t.Errorf("host %q is not dialable", h)
		}
	}
}

func TestPairBeginNeverLogsPayload(t *testing.T) {
	d, buf := newLoggingRemoteDaemon(t)
	startPairingListener(t, d)
	got, pl := beginLoopback(t, d)
	code := presentPhone(t, d, pl, got.Pairing, "Martin's phone")
	if _, err := d.handlePairConfirm(protocol.PairConfirmArgs{Pairing: got.Pairing, Code: code}); err != nil {
		t.Fatal(err)
	}
	logs := buf.String()
	for _, secret := range []string{got.Payload, pl.Secret, code} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log contains a secret:\n%s", logs)
		}
	}
}

func TestPairConfirmWritesRegistryAndAudits(t *testing.T) {
	d, buf := newLoggingRemoteDaemon(t)
	startPairingListener(t, d)
	got, pl := beginLoopback(t, d)
	code := presentPhone(t, d, pl, got.Pairing, "Test phone")

	// A wrong code leaves the registry alone.
	if _, err := d.handlePairConfirm(protocol.PairConfirmArgs{Pairing: got.Pairing, Code: "000000x"}); err == nil {
		t.Fatal("a wrong code was accepted")
	}
	if len(d.devices.List()) != 0 {
		t.Fatal("a wrong code registered a device")
	}
	st, err := d.handlePairConfirm(protocol.PairConfirmArgs{Pairing: got.Pairing, Code: code[:3] + " " + code[3:], Tiers: []string{protocol.TierRead, protocol.TierAnswer}})
	if err != nil {
		t.Fatal(err)
	}
	if st.State != protocol.PairStatePaired || st.Device == nil {
		t.Fatalf("state = %+v", st)
	}
	list := d.devices.List()
	if len(list) != 1 || list[0].ID != st.Device.ID || !slices.Equal(list[0].Tiers, []string{protocol.TierRead, protocol.TierAnswer}) {
		t.Fatalf("registry = %+v", list)
	}
	if !strings.Contains(buf.String(), "devices: paired device="+st.Device.ID) {
		t.Errorf("no audit line:\n%s", buf.String())
	}
}

// connectedPhone registers a phone and holds an authenticated connection.
func connectedPhone(t *testing.T, d *Daemon) (pairedPhone, *remoteClient) {
	t.Helper()
	phone := addPairedPhone(t, d, nil)
	c := dialRemote(t, d.remote.Addrs()[0].Addr, phone.cert)
	c.await(t, func(f *protocol.Frame) bool { return f.Type == protocol.FrameReady })
	return phone, c
}

func connClosed(c *remoteClient) bool {
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var f protocol.Frame
		if err := c.fr.ReadFrame(&f); err != nil {
			return true
		}
	}
}

func TestRevokeDeviceDropsConnections(t *testing.T) {
	d, buf := newLoggingRemoteDaemon(t)
	startPairingListener(t, d)
	phone, c := connectedPhone(t, d)

	got, err := d.handleRevokeDevice(protocol.RevokeDeviceArgs{ID: phone.dev.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != phone.dev.ID || got.Closed != 1 {
		t.Fatalf("got %+v", got)
	}
	if !connClosed(c) {
		t.Fatal("the revoked phone's connection stayed open")
	}
	if len(d.handleDevices().Devices) != 0 {
		t.Fatal("the device is still listed")
	}
	if !strings.Contains(buf.String(), "devices: revoke device="+phone.dev.ID) || !strings.Contains(buf.String(), "saved=file") {
		t.Errorf("no audit line:\n%s", buf.String())
	}
	if _, err := d.handleRevokeDevice(protocol.RevokeDeviceArgs{ID: phone.dev.ID}); err == nil {
		t.Error("revoking twice succeeded")
	}
}

func TestRevokeNotifiesRelay(t *testing.T) {
	d := newRemoteTestDaemon(t)
	phone := addPairedPhone(t, d, nil)
	fired := make(chan struct{}, 4)
	d.devices.Subscribe(func() { fired <- struct{}{} })
	if _, err := d.handleRevokeDevice(protocol.RevokeDeviceArgs{ID: phone.dev.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("a revoke notified no registry subscriber")
	}
}

func TestSetDeviceTiersDropsConnections(t *testing.T) {
	d := newRemoteTestDaemon(t)
	startPairingListener(t, d)
	phone, c := connectedPhone(t, d)

	info, err := d.handleSetDeviceTiers(protocol.SetDeviceTiersArgs{ID: phone.dev.ID, Tiers: []string{protocol.TierRead}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.Tiers, []string{protocol.TierRead}) || info.ID != phone.dev.ID {
		t.Fatalf("info = %+v", info)
	}
	if !connClosed(c) {
		t.Fatal("the tier change left the old connection open")
	}
	if _, err := d.handleSetDeviceTiers(protocol.SetDeviceTiersArgs{ID: phone.dev.ID, Tiers: []string{protocol.TierAnswer}}); err == nil {
		t.Error("tiers without read were accepted")
	}
	if _, err := d.handleSetDeviceTiers(protocol.SetDeviceTiersArgs{ID: "dnope", Tiers: protocol.Tiers}); err == nil {
		t.Error("an unknown device was accepted")
	}
}

var fingerprintRe = regexp.MustCompile(`^[0-9a-f]{4}( [0-9a-f]{4}){3}$`)

func TestDevicesReportsConnected(t *testing.T) {
	d := newRemoteTestDaemon(t)
	startPairingListener(t, d)
	phone := addPairedPhone(t, d, nil)

	list := d.handleDevices()
	if len(list.Devices) != 1 || list.Devices[0].Connected != 0 || list.Problem != "" {
		t.Fatalf("before connecting: %+v", list)
	}
	if !fingerprintRe.MatchString(list.Devices[0].Fingerprint) {
		t.Errorf("fingerprint %q", list.Devices[0].Fingerprint)
	}
	c := dialRemote(t, d.remote.Addrs()[0].Addr, phone.cert)
	c.await(t, func(f *protocol.Frame) bool { return f.Type == protocol.FrameReady })
	list = d.handleDevices()
	if list.Devices[0].Connected != 1 || list.Devices[0].ID != phone.dev.ID {
		t.Fatalf("after connecting: %+v", list)
	}
}

func TestDevicesListIsEmptyNotNull(t *testing.T) {
	d := newRemoteTestDaemon(t)
	if got := d.handleDevices(); got.Devices == nil || len(got.Devices) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// TestPairBeginReplyNobodyReadClosesTheWindow: the pairBegin reply carries the
// window's only secret. A client that hung up before reading it (its own
// timeout fired while the daemon waited on the hub) must not leave a window
// open for its whole TTL, during which any certificate passes TLS.
func TestPairBeginReplyNobodyReadClosesTheWindow(t *testing.T) {
	d, logBuf := newLoggingRemoteDaemon(t)
	d.mdnsStart = func(ctx context.Context, _ string, _ []string) (mdns.Process, error) {
		return stubMDNS{ctx: ctx}, nil
	}
	startPairingListener(t, d)

	got, _ := beginLoopback(t, d)
	d.abandonReply(protocol.Request{Cmd: protocol.CmdPairBegin}, dataResponse(got))
	st, err := d.handlePairStatus(context.Background(), protocol.PairStatusArgs{Pairing: got.Pairing})
	if err != nil || st.State != protocol.PairStateCancelled {
		t.Fatalf("status = %+v err=%v, want cancelled", st, err)
	}

	// End to end: the socket loop calls it when the reply cannot be written.
	cli, srv := net.Pipe()
	done := make(chan struct{})
	go func() { d.handleConn(context.Background(), srv); close(done) }()
	if _, err := cli.Write([]byte(`{"cmd":"pairBegin","args":{"allowLoopback":true}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	_ = cli.Close()
	<-done
	if n := strings.Count(logBuf.String(), "its QR never reached the client"); n != 2 {
		t.Fatalf("%d abandoned windows closed, want 2:\n%s", n, logBuf.String())
	}
}
