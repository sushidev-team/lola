package remote

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// TestReqFrameReachesHandleAndEchoesID proves the plumbing the whole design
// rests on: an envelope decodes into a protocol.Request, that Request reaches
// the daemon's own dispatcher unchanged, and the daemon's Response comes back
// on the SAME correlation id — which is what makes multiplexing possible at all.
func TestReqFrameReachesHandleAndEchoesID(t *testing.T) {
	r := newRig(t)
	r.handler.fn = func(_ context.Context, req protocol.Request) protocol.Response {
		return protocol.Response{OK: true, Data: json.RawMessage(`{"seen":"` + req.Cmd + `"}`)}
	}

	r.req("c7", "sessions", protocol.Request{Session: "lola-fe-42"})

	f := r.next()
	if f.Type != protocol.FrameResp {
		t.Fatalf("got type %q, want %q", f.Type, protocol.FrameResp)
	}
	if f.ID != "c7" {
		t.Errorf("got id %q, want %q", f.ID, "c7")
	}
	var resp protocol.Response
	if err := json.Unmarshal(f.Payload, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK || string(resp.Data) != `{"seen":"sessions"}` {
		t.Errorf("got %+v, want the handler's own response", resp)
	}
	got := r.handler.requests()
	if len(got) != 1 || got[0].Cmd != "sessions" || got[0].Session != "lola-fe-42" {
		t.Errorf("handler saw %+v, want one sessions request carrying the session id", got)
	}
}

// TestEnvelopeCmdWinsOverPayloadCmd is the fail-open bug this transport must
// not have: authorization reads the ENVELOPE's Cmd without unmarshalling, so a
// payload naming a different command would be authorized as one thing and
// executed as another.
func TestEnvelopeCmdWinsOverPayloadCmd(t *testing.T) {
	r := newRig(t)
	r.req("c1", "sessions", protocol.Request{Cmd: "kill", Session: "lola-fe-42"})
	r.next()

	got := r.handler.requests()
	if len(got) != 1 {
		t.Fatalf("handler saw %d requests, want 1", len(got))
	}
	if got[0].Cmd != "sessions" {
		t.Errorf("handler saw cmd %q; the payload must never override the envelope", got[0].Cmd)
	}
}

// TestForceIsClearedOnEveryRemoteRequest pins the one field whose misuse
// discards uncommitted work. kill is reachable remotely; a forced kill is not.
func TestForceIsClearedOnEveryRemoteRequest(t *testing.T) {
	r := newRig(t)
	r.req("c1", "kill", protocol.Request{Session: "lola-fe-42", Force: true})
	r.next()

	got := r.handler.requests()
	if len(got) != 1 {
		t.Fatalf("handler saw %d requests, want 1", len(got))
	}
	if got[0].Force {
		t.Error("Force reached the handler; a dirty worktree is the one gate teardown has")
	}
	if got[0].Session != "lola-fe-42" {
		t.Errorf("Session was lost: %+v", got[0])
	}
}

// TestConcurrentRequestsDoNotSerialize is the reason this package exists beside
// internal/daemon's handleConn rather than reusing it. A slow command must not
// delay a fast one on the same connection.
func TestConcurrentRequestsDoNotSerialize(t *testing.T) {
	r := newRig(t)
	release := make(chan struct{})
	r.handler.fn = func(_ context.Context, req protocol.Request) protocol.Response {
		if req.Cmd == "openTicket" {
			<-release
		}
		return protocol.Response{OK: true}
	}

	r.req("slow", "openTicket", protocol.Request{})
	r.req("fast", "sessions", protocol.Request{})

	f := r.next()
	if f.ID != "fast" {
		t.Fatalf("first reply was %q; the slow command blocked the fast one", f.ID)
	}
	close(release)
	if f := r.next(); f.ID != "slow" {
		t.Fatalf("second reply was %q, want slow", f.ID)
	}
}

// TestRequestOverflowIsRefusedRatherThanQueued proves the bound is real and
// that hitting it does not reintroduce head-of-line blocking: the refusal comes
// back while the four slow requests are still in flight.
func TestRequestOverflowIsRefusedRatherThanQueued(t *testing.T) {
	r := newRig(t)
	release := make(chan struct{})
	started := make(chan struct{}, reqConcurrency)
	r.handler.fn = func(_ context.Context, req protocol.Request) protocol.Response {
		if req.Cmd == "openTicket" {
			started <- struct{}{}
			<-release
		}
		return protocol.Response{OK: true}
	}

	for i := 0; i < reqConcurrency; i++ {
		r.req("slow", "openTicket", protocol.Request{})
	}
	for i := 0; i < reqConcurrency; i++ {
		select {
		case <-started:
		case <-time.After(testTimeout):
			t.Fatalf("only %d of %d slow requests started", i, reqConcurrency)
		}
	}

	r.req("over", "sessions", protocol.Request{})
	p := r.wantErr(protocol.CodeRateLimited)
	if p.Message == "" {
		t.Error("a rate-limit refusal must carry a human line")
	}
	close(release)
}

// TestMalformedPayloadIsRefusedOnItsIDWithoutClosing is the one recoverable
// failure: the framing is intact, so the refusal is scoped to the frame.
func TestMalformedPayloadIsRefusedOnItsIDWithoutClosing(t *testing.T) {
	r := newRig(t)
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "bad", Cmd: "sessions"}
	f.Payload = json.RawMessage(`"not an object"`)
	r.send(f)

	r.wantErr(protocol.CodeInternal)
	if r.handler.count() != 0 {
		t.Error("a malformed payload reached the handler")
	}
	r.wantOpen()
}

// TestUnsupportedVersionClosesAndNamesBothBounds: the refusal has to carry the
// daemon's own bounds, because that is what lets the app say which SIDE is
// behind instead of showing a connect error.
func TestUnsupportedVersionClosesAndNamesBothBounds(t *testing.T) {
	for _, v := range []int{0, -1, protocol.FrameVersionCurrent + 1, 99} {
		r := newRig(t)
		r.send(protocol.Frame{V: v, Type: protocol.FrameReq, ID: "x", Cmd: "sessions"})

		p := r.wantErr(protocol.CodeUnsupportedVersion)
		if p.MinV != protocol.FrameVersionMin || p.MaxV != protocol.FrameVersionCurrent {
			t.Errorf("v=%d: got bounds %d..%d, want %d..%d", v, p.MinV, p.MaxV,
				protocol.FrameVersionMin, protocol.FrameVersionCurrent)
		}
		r.wantClosed()
		if r.handler.count() != 0 {
			t.Errorf("v=%d: a frame with an unknown envelope version reached the handler", v)
		}
	}
}

// TestUnknownFrameTypeCloses covers both halves of the direction table: a type
// this build does not know at all, and a daemon-to-client type arriving
// inbound. Neither is guessed at.
func TestUnknownFrameTypeCloses(t *testing.T) {
	cases := []struct{ name, typ string }{
		{"unknown", "pair.begin"},
		{"empty", ""},
		{"outbound only: resp", protocol.FrameResp},
		{"outbound only: resync", protocol.FrameResync},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.send(protocol.Frame{V: protocol.FrameVersionCurrent, Type: tc.typ, ID: "x"})
			r.wantErr(protocol.CodeUnknownType)
			r.wantClosed()
			if r.handler.count() != 0 {
				t.Error("the frame reached the handler")
			}
		})
	}
}

// TestOversizedFramePrefixClosesBeforeAllocating writes a length prefix the
// codec must refuse without reading a byte of body — the property a
// bufio.Scanner cannot have, and the reason the wire is length-prefixed.
func TestOversizedFramePrefixClosesBeforeAllocating(t *testing.T) {
	r := newRig(t)
	r.rawFrame(protocol.MaxFrameBytes+1, nil)

	r.wantErr(protocol.CodeFrameTooLarge)
	r.wantClosed()
	if r.handler.count() != 0 {
		t.Error("an oversized frame reached the handler")
	}
}

// TestZeroLengthFrameCloses: there is no empty frame in this protocol, so a
// zero prefix is a bug or a probe and the stream cannot be trusted after it.
func TestZeroLengthFrameCloses(t *testing.T) {
	r := newRig(t)
	r.rawFrame(0, nil)
	r.wantErr(protocol.CodeFrameTooLarge)
	r.wantClosed()
}

// TestUndecodableEnvelopeCloses: a malformed ENVELOPE has no id to answer on,
// so unlike a malformed payload it is fatal to the connection.
func TestUndecodableEnvelopeCloses(t *testing.T) {
	r := newRig(t)
	body := []byte("{not json")
	r.rawFrame(uint32(len(body)), body)
	r.wantErr(protocol.CodeInternal)
	r.wantClosed()
	if r.handler.count() != 0 {
		t.Error("an undecodable frame reached the handler")
	}
}

// TestDeniedCommandsNeverReachHandle walks the closed denial list. Each is
// refused BEFORE the authorizer is consulted, so no authorizer can grant one,
// and each closes the connection.
func TestDeniedCommandsNeverReachHandle(t *testing.T) {
	denied := []string{
		"stop", "reload", "renameProject", "hookEvent",
		"pairBegin", "pairStatus", "pairConfirm", "devices", "revokeDevice",
		"remote.hello", "remote.anything", "",
	}
	for _, cmd := range denied {
		t.Run("cmd="+cmd, func(t *testing.T) {
			r := newRig(t)
			r.req("d", cmd, protocol.Request{})
			r.wantErr(protocol.CodeUnknownCmd)
			r.wantClosed()
			if r.handler.count() != 0 {
				t.Errorf("%q reached the handler", cmd)
			}
			for _, s := range r.auth.seen() {
				if strings.HasSuffix(s, ":"+cmd) {
					t.Errorf("%q was offered to the authorizer; the denials must run first", cmd)
				}
			}
		})
	}
}

// TestAuthorizerRefusalCloses proves the second gate is wired and that its
// refusal never leaks the authorizer's reasoning to the peer.
func TestAuthorizerRefusalCloses(t *testing.T) {
	r := newRig(t, func(r *rig, _ *Options) { r.auth.frameErr = ErrDenied })

	r.req("d", "kill", protocol.Request{Session: "lola-fe-42"})
	p := r.wantErr(protocol.CodeDenied)
	if strings.Contains(p.Message, ErrDenied.Error()) {
		t.Errorf("the refusal echoed the authorizer's error text: %q", p.Message)
	}
	r.wantClosed()
	if r.handler.count() != 0 {
		t.Error("a denied frame reached the handler")
	}
}

// TestFailedAuthenticationClosesBeforeAnyFrame: an unauthenticated peer never
// gets as far as the frame loop.
func TestFailedAuthenticationClosesBeforeAnyFrame(t *testing.T) {
	r := newRig(t, func(r *rig, _ *Options) { r.auth.authErr = ErrUnauthenticated })
	r.wantClosed()
	r.req("x", "sessions", protocol.Request{})
	if r.handler.count() != 0 {
		t.Error("a frame from an unauthenticated peer reached the handler")
	}
}
