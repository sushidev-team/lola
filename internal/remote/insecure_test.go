//go:build lola_insecure

package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// This file is the tagged half. Everything it asserts is about the two rails
// that hold while the M1 bearer-key path is compiled in: the bind is forced to
// loopback whatever config says, and every accept is loud.

// TestInsecureListenForcesALoopbackBind is the one that matters most. A shared
// bearer secret must never reach a network interface, so "lan", "all" and an
// explicit LAN address are all overridden — and the assertion is on the
// ADDRESSES actually bound, not on the mode string, because that is what
// decides who can reach the port.
func TestInsecureListenForcesALoopbackBind(t *testing.T) {
	t.Setenv(InsecureKeyEnv, strings.Repeat("k", 32))

	for _, mode := range []string{"lan", "all", "0.0.0.0", "::", "192.168.1.5"} {
		t.Run("bind="+mode, func(t *testing.T) {
			var lines []string
			s, err := Listen(context.Background(), Options{
				Bind:   mode,
				Port:   freePort(t),
				Dir:    t.TempDir(),
				Handle: (&stubHandler{}).handle,
				Logf:   func(f string, a ...any) { lines = append(lines, fmtSprintf(f, a...)) },
			})
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer s.Close()

			addrs := s.Addrs()
			if len(addrs) == 0 {
				t.Fatal("nothing was bound")
			}
			for _, ba := range addrs {
				if !loopbackAddr(ba.Addr) {
					t.Errorf("bound %s for bind = %q; the insecure build must never leave loopback", ba.Addr, mode)
				}
			}
			log := strings.Join(lines, "\n")
			if !strings.Contains(log, "overridden to localhost") {
				t.Errorf("the override was not logged:\n%s", log)
			}
		})
	}
}

// TestInsecureListenLeavesLocalhostAndOffAlone: the override is a floor, not a
// rewrite. "off" in particular must keep meaning off.
func TestInsecureListenLeavesOffAlone(t *testing.T) {
	t.Setenv(InsecureKeyEnv, strings.Repeat("k", 32))
	_, err := Listen(context.Background(), Options{
		Bind: "off", Port: freePort(t), Dir: t.TempDir(), Handle: (&stubHandler{}).handle,
	})
	if !errors.Is(err, ErrBindOff) {
		t.Fatalf("got %v, want ErrBindOff", err)
	}
}

// TestInsecureAuthorizerRejectsAShortOverride. An explicitly supplied key that
// is too weak is refused, and the refusal names the length, never the value.
// An UNSET variable is no longer an error — see the generation tests below.
func TestInsecureAuthorizerRejectsAShortOverride(t *testing.T) {
	t.Setenv(InsecureKeyEnv, "hunter2")
	a, err := newAuthorizer(t.TempDir(), func(string, ...any) {})
	if err == nil {
		t.Fatal("a short key was accepted")
	}
	if a != nil {
		t.Fatal("an authorizer was returned alongside the error")
	}
	if !errors.Is(err, ErrNoAuthorizer) {
		t.Fatalf("got %v, want ErrNoAuthorizer", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error repeated the key back: %v", err)
	}
}

// A missing key USED to stop the listener starting, which was silent, looked
// like a bind failure, and happened on every restart that did not carry the
// environment — which is every restart the TUI's ^r and the desktop app's
// restart button perform. One is generated and persisted instead.
func TestInsecureAuthorizerGeneratesAKeyWhenThereIsNone(t *testing.T) {
	t.Setenv(InsecureKeyEnv, "")
	dir := t.TempDir()

	a, err := newAuthorizer(dir, func(string, ...any) {})
	if err != nil {
		t.Fatalf("newAuthorizer: %v", err)
	}
	if a == nil {
		t.Fatal("no authorizer")
	}

	path := filepath.Join(dir, insecureKeyFile)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the key was not persisted: %v", err)
	}
	// 0600, the same discipline device.key gets.
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(b))) < insecureMinKeyLen {
		t.Error("the generated key is shorter than the minimum it exists to enforce")
	}
}

// The whole point of persisting it: a restart authenticates the phone that was
// already paired, instead of silently becoming a different daemon.
func TestInsecureAuthorizerReusesTheStoredKey(t *testing.T) {
	t.Setenv(InsecureKeyEnv, "")
	dir := t.TempDir()

	if _, err := newAuthorizer(dir, func(string, ...any) {}); err != nil {
		t.Fatalf("first: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, insecureKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newAuthorizer(dir, func(string, ...any) {}); err != nil {
		t.Fatalf("second: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(dir, insecureKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("the key changed across restarts, which unpairs every phone")
	}
}

// An explicit key still wins, so an operator supplying their own is never
// overridden by something already on disk.
func TestInsecureAuthorizerPrefersTheEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(InsecureKeyEnv, "")
	if _, err := newAuthorizer(dir, func(string, ...any) {}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stored, err := os.ReadFile(filepath.Join(dir, insecureKeyFile))
	if err != nil {
		t.Fatal(err)
	}

	override := strings.Repeat("o", 32)
	t.Setenv(InsecureKeyEnv, override)
	a, err := newAuthorizer(dir, func(string, ...any) {})
	if err != nil {
		t.Fatalf("newAuthorizer: %v", err)
	}
	ia, ok := a.(*insecureAuthorizer)
	if !ok {
		t.Fatalf("got %T", a)
	}
	if string(ia.key) != override {
		t.Error("the stored key won over an explicit override")
	}
	if after, _ := os.ReadFile(filepath.Join(dir, insecureKeyFile)); string(after) != string(stored) {
		t.Error("an override rewrote the stored key")
	}
}

// Regenerating is M1's stand-in for revocation: the old key must stop working.
func TestRegenerateInsecureKeyReplacesIt(t *testing.T) {
	t.Setenv(InsecureKeyEnv, "")
	dir := t.TempDir()

	if _, err := newAuthorizer(dir, func(string, ...any) {}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, insecureKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := RegenerateInsecureKey(dir, nil); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, insecureKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatal("the key did not change, so no phone was invalidated")
	}
	if len(strings.TrimSpace(string(after))) < insecureMinKeyLen {
		t.Error("the replacement is shorter than the minimum")
	}
}

// The bind rail's hole is opened by TWO deliberate acts, and the test asserts
// on the ADDRESSES actually bound rather than the mode string, for the same
// reason the forcing test does.
func TestInsecureListenHonoursTheBindWhenTheLANOptInIsSet(t *testing.T) {
	t.Setenv(InsecureKeyEnv, strings.Repeat("k", 32))
	t.Setenv(InsecureLANEnv, "1")

	srv, err := Listen(context.Background(), Options{
		Bind: "lan", Port: freePort(t), Dir: t.TempDir(), Handle: (&stubHandler{}).handle,
	})
	if err != nil {
		t.Skipf("no usable private interface on this host: %v", err)
	}
	defer srv.Close()

	addrs := srv.Addrs()
	if len(addrs) == 0 {
		t.Fatal("nothing was bound")
	}
	for _, ba := range addrs {
		if loopbackAddr(ba.Addr) {
			t.Errorf("bound %s; the opt-in should have honoured the LAN bind", ba.Addr)
		}
	}
}

// Half an opt-in is no opt-in: the variable alone, without a config that names
// a non-loopback bind, changes nothing.
func TestInsecureLANOptInAloneDoesNotWiden(t *testing.T) {
	t.Setenv(InsecureKeyEnv, strings.Repeat("k", 32))
	t.Setenv(InsecureLANEnv, "1")

	srv, err := Listen(context.Background(), Options{
		Bind: "localhost", Port: freePort(t), Dir: t.TempDir(), Handle: (&stubHandler{}).handle,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()

	for _, ba := range srv.Addrs() {
		if !loopbackAddr(ba.Addr) {
			t.Errorf("bound %s; a localhost bind must stay loopback", ba.Addr)
		}
	}
}

// And a value that is merely present must not count as consent.
func TestInsecureLANOptInRequiresAnAffirmative(t *testing.T) {
	for _, v := range []string{"", "0", "false", "no", "maybe", " "} {
		t.Run("value="+v, func(t *testing.T) {
			t.Setenv(InsecureLANEnv, v)
			if insecureLANAllowed() {
				t.Errorf("%q was read as consent", v)
			}
		})
	}
}

// withInsecureAuth arms a rig with the REAL bearer-key authorizer, built
// against the rig's own log capture so the per-accept warning is observable.
func withInsecureAuth(t *testing.T, key string) func(*rig, *Options) {
	t.Helper()
	t.Setenv(InsecureKeyEnv, key)
	return func(r *rig, _ *Options) {
		a, err := newAuthorizer(t.TempDir(), r.log.logf)
		if err != nil {
			t.Fatalf("newAuthorizer: %v", err)
		}
		r.authOverride = a
	}
}

// TestInsecureHandshakeAcceptsTheKeyAndThenServes.
func TestInsecureHandshakeAcceptsTheKeyAndThenServes(t *testing.T) {
	key := strings.Repeat("k", 32)
	r := newRig(t, withInsecureAuth(t, key))

	hello := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "h1", Cmd: helloCmd}
	if err := hello.SetPayload(insecureHello{Key: key}); err != nil {
		t.Fatalf("payload: %v", err)
	}
	r.send(hello)

	ack := r.next()
	if ack.Type != protocol.FrameResp || ack.ID != "h1" {
		t.Fatalf("got %+v, want a resp acknowledging the hello", ack)
	}
	var resp protocol.Response
	if err := json.Unmarshal(ack.Payload, &resp); err != nil || !resp.OK {
		t.Fatalf("acknowledgement %s / %v, want OK", ack.Payload, err)
	}

	r.req("c1", "sessions", protocol.Request{})
	f := r.next()
	if f.Type != protocol.FrameResp || f.ID != "c1" {
		t.Fatalf("got %+v, want the sessions reply", f)
	}
}

// TestInsecureHandshakeRefusesAWrongKeyAndSaysNothingUseful. Everything a
// failed attempt learns is that it failed.
func TestInsecureHandshakeRefusesAWrongKeyAndSaysNothingUseful(t *testing.T) {
	key := strings.Repeat("k", 32)
	arm := withInsecureAuth(t, key)

	cases := []struct {
		name  string
		frame func() protocol.Frame
	}{
		{"a wrong key", func() protocol.Frame {
			f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "h1", Cmd: helloCmd}
			_ = f.SetPayload(insecureHello{Key: strings.Repeat("z", 32)})
			return f
		}},
		{"an empty key", func() protocol.Frame {
			f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "h1", Cmd: helloCmd}
			_ = f.SetPayload(insecureHello{})
			return f
		}},
		{"a request instead of a hello", func() protocol.Frame {
			f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "h1", Cmd: "sessions"}
			_ = f.SetPayload(protocol.Request{})
			return f
		}},
		{"a subscribe instead of a hello", func() protocol.Frame {
			return protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameSub, ID: "h1", Pane: "lola-fe-42"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, arm)
			r.send(tc.frame())

			p := r.wantErr(protocol.CodeDenied)
			if strings.Contains(p.Message, key) || strings.Contains(p.Message, InsecureKeyEnv) {
				t.Errorf("the refusal leaked something: %q", p.Message)
			}
			r.wantClosed()
			if r.handler.count() != 0 {
				t.Error("a frame reached the handler before authentication")
			}
			if subs, _, _ := r.bus.snapshot(); len(subs) != 0 {
				t.Errorf("a subscription was opened before authentication: %v", subs)
			}
		})
	}
}

// TestEveryAcceptIsLoud. A daemon running the M1 path should be impossible to
// forget about, so the warning is per accept rather than once at startup.
func TestEveryAcceptIsLoud(t *testing.T) {
	key := strings.Repeat("k", 32)
	arm := withInsecureAuth(t, key)
	for i := 0; i < 2; i++ {
		r := newRig(t, arm)
		hello := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "h", Cmd: helloCmd}
		if err := hello.SetPayload(insecureHello{Key: key}); err != nil {
			t.Fatalf("payload: %v", err)
		}
		r.send(hello)
		r.next()

		if log := r.log.all(); !strings.Contains(log, "WARNING") {
			t.Fatalf("accept %d logged no warning:\n%s", i, log)
		}
	}
}

// TestHelloIsUnreachableAfterTheHandshake: the hello lives in the remote.*
// namespace, which the unconditional denials refuse, so a second one can never
// be forwarded to the daemon's dispatcher.
func TestHelloIsUnreachableAfterTheHandshake(t *testing.T) {
	if !CommandDenied(helloCmd) {
		t.Fatalf("%q is not denied; a second hello would reach d.handle", helloCmd)
	}
	key := strings.Repeat("k", 32)
	r := newRig(t, withInsecureAuth(t, key))
	hello := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "h", Cmd: helloCmd}
	if err := hello.SetPayload(insecureHello{Key: key}); err != nil {
		t.Fatalf("payload: %v", err)
	}
	r.send(hello)
	r.next()

	r.send(hello)
	r.wantErr(protocol.CodeUnknownCmd)
	r.wantClosed()
	if r.handler.count() != 0 {
		t.Error("a second hello reached the handler")
	}
}

// TestInsecureListenEndToEnd proves the tagged Listen actually serves: a real
// loopback TLS listener, the in-band hello, then a request.
func TestInsecureListenEndToEnd(t *testing.T) {
	key := strings.Repeat("k", 32)
	t.Setenv(InsecureKeyEnv, key)
	h := &stubHandler{}
	s, err := Listen(context.Background(), Options{
		Bind: "lan", Port: freePort(t), Dir: t.TempDir(),
		Handle: h.handle, Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer s.Close()

	conn, err := tls.Dial("tcp", s.Addrs()[0].Addr, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(testTimeout))

	fw := protocol.NewFrameWriter(conn)
	fr := protocol.NewFrameReader(conn)

	hello := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "h", Cmd: helloCmd}
	if err := hello.SetPayload(insecureHello{Key: key}); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if err := fw.WriteFrame(&hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	var ack protocol.Frame
	if err := fr.ReadFrame(&ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Type != protocol.FrameResp || ack.ID != "h" {
		t.Fatalf("got %+v, want the hello acknowledgement", ack)
	}

	req := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "c1", Cmd: "sessions"}
	if err := req.SetPayload(protocol.Request{}); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if err := fw.WriteFrame(&req); err != nil {
		t.Fatalf("write req: %v", err)
	}
	var got protocol.Frame
	if err := fr.ReadFrame(&got); err != nil {
		t.Fatalf("read resp: %v", err)
	}
	if got.Type != protocol.FrameResp || got.ID != "c1" || h.count() != 1 {
		t.Fatalf("got %+v with %d handler calls, want one sessions reply", got, h.count())
	}
}
