//go:build lola_insecure

package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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

// TestInsecureAuthorizerRequiresAUsableKey. An empty key that authenticated
// everyone would be strictly worse than no listener, so the listener does not
// start without one — and the refusal names the length, never the value.
func TestInsecureAuthorizerRequiresAUsableKey(t *testing.T) {
	cases := []struct{ name, key string }{
		{"unset", ""},
		{"too short", "hunter2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(InsecureKeyEnv, tc.key)
			a, err := newAuthorizer(func(string, ...any) {})
			if err == nil {
				t.Fatal("a short or missing key was accepted")
			}
			if a != nil {
				t.Fatal("an authorizer was returned alongside the error")
			}
			if !errors.Is(err, ErrNoAuthorizer) {
				t.Fatalf("got %v, want ErrNoAuthorizer", err)
			}
			if tc.key != "" && strings.Contains(err.Error(), tc.key) {
				t.Errorf("the error repeated the key back: %v", err)
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
		a, err := newAuthorizer(r.log.logf)
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
