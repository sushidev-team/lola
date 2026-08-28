package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// framed builds the wire bytes for one body: a 4-byte big-endian length and the
// body itself. Tests use it to feed the reader shapes a writer would never
// produce, which is exactly the input a network peer can produce.
func framed(body []byte) []byte {
	out := make([]byte, FrameHeaderBytes+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	copy(out[FrameHeaderBytes:], body)
	return out
}

func mustPayload(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// Every frame shape in the protocol survives a write/read round trip byte for
// byte, including the payloads whose wire form is not obvious: []byte is base64
// on the wire, and a resync's SGR escapes must come back unchanged.
func TestFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		frame Frame
	}{
		{
			name:  "req carries cmd and a Request payload",
			frame: Frame{V: 1, Type: FrameReq, ID: "c7", Cmd: "sessions", Payload: mustPayload(t, Request{Cmd: "sessions"})},
		},
		{
			name:  "resp echoes the id",
			frame: Frame{V: 1, Type: FrameResp, ID: "c7", Payload: mustPayload(t, Response{OK: true})},
		},
		{
			name:  "sub carries the advisory viewport",
			frame: Frame{V: 1, Type: FrameSub, ID: "s3", Pane: "lola-fe-42", Payload: mustPayload(t, SubPayload{Cols: 55, Rows: 34})},
		},
		{
			name:  "unsub needs no payload at all",
			frame: Frame{V: 1, Type: FrameUnsub, ID: "s3", Pane: "lola-fe-42"},
		},
		{
			name: "resync preserves SGR and the cursor",
			frame: Frame{V: 1, Type: FrameResync, ID: "s3", Pane: "lola-fe-42", Seq: 1, Payload: mustPayload(t, ResyncPayload{
				Cols: 200, Rows: 50,
				Lines:     []string{"\x1b[2m✻\x1b[0m Cogitated for 24m 46s", "", "❯  "},
				CursorX:   2,
				CursorY:   2,
				AltScreen: true,
			})},
		},
		{
			name:  "pty output is raw bytes",
			frame: Frame{V: 1, Type: FramePTY, Pane: "lola-fe-42", Seq: 9, Payload: mustPayload(t, PTYOutputPayload{Data: []byte{0x1b, '[', 'Z', 0x00, 0xff}})},
		},
		{
			name:  "pty input write",
			frame: Frame{V: 1, Type: FramePTY, Pane: "lola-fe-42", Payload: mustPayload(t, PTYInputPayload{Action: PTYActionWrite, Data: []byte("\x1b[Z")})},
		},
		{
			name:  "err carries the version bounds",
			frame: UnsupportedVersionFrame("c7"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := NewFrameWriter(&buf).WriteFrame(&tc.frame); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			var got Frame
			if err := NewFrameReader(&buf).ReadFrame(&got); err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if got.V != tc.frame.V || got.Type != tc.frame.Type || got.ID != tc.frame.ID ||
				got.Cmd != tc.frame.Cmd || got.Pane != tc.frame.Pane || got.Seq != tc.frame.Seq {
				t.Fatalf("envelope changed:\nwant %+v\ngot  %+v", tc.frame, got)
			}
			if len(tc.frame.Payload) == 0 {
				if len(got.Payload) != 0 {
					t.Fatalf("absent payload came back as %q", got.Payload)
				}
				return
			}
			// Compare semantically: json.Marshal may reorder nothing here, but
			// HTML escaping means the bytes are not guaranteed identical.
			var want, have any
			if err := json.Unmarshal(tc.frame.Payload, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(got.Payload, &have); err != nil {
				t.Fatalf("payload not decodable: %v (%q)", err, got.Payload)
			}
			if !jsonEqual(want, have) {
				t.Fatalf("payload changed:\nwant %v\ngot  %v", want, have)
			}
			if buf.Len() != 0 {
				t.Fatalf("%d bytes left after one frame", buf.Len())
			}
		})
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// The single most dangerous property of reusing a Frame: encoding/json leaves
// fields the JSON does not mention untouched, so without an explicit reset the
// previous frame's Cmd or Pane silently becomes this frame's — which the
// authorizer then reads. ReadFrame must zero the destination first.
func TestReadFrameResetsTheDestination(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	first := Frame{V: 1, Type: FrameReq, ID: "a", Cmd: "kill", Pane: "lola-fe-42", Seq: 7,
		Payload: mustPayload(t, Request{Cmd: "kill", Session: "lola-fe-42"})}
	second := Frame{V: 1, Type: FrameUnsub}
	for _, f := range []Frame{first, second} {
		if err := w.WriteFrame(&f); err != nil {
			t.Fatal(err)
		}
	}

	r := NewFrameReader(&buf)
	var got Frame
	if err := r.ReadFrame(&got); err != nil {
		t.Fatal(err)
	}
	if got.Cmd != "kill" || got.Seq != 7 {
		t.Fatalf("first frame decoded wrong: %+v", got)
	}
	if err := r.ReadFrame(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "" || got.Cmd != "" || got.Pane != "" || got.Seq != 0 || len(got.Payload) != 0 {
		t.Fatalf("stale fields survived into the second frame: %+v", got)
	}
}

// A truncated stream is never mistaken for a clean close: only an EOF landing
// exactly on a frame boundary is io.EOF.
func TestReadFrameTruncated(t *testing.T) {
	body := []byte(`{"v":1,"type":"req","cmd":"sessions"}`)
	full := framed(body)

	tests := []struct {
		name string
		wire []byte
		want error
	}{
		{"clean close on a boundary", nil, io.EOF},
		{"partial header", full[:2], io.ErrUnexpectedEOF},
		{"header only", full[:FrameHeaderBytes], io.ErrUnexpectedEOF},
		{"partial body", full[:len(full)-1], io.ErrUnexpectedEOF},
		{"a whole frame then a partial header", append(append([]byte{}, full...), full[:3]...), io.ErrUnexpectedEOF},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewFrameReader(bytes.NewReader(tc.wire))
			var f Frame
			var err error
			// Drain whole frames first; the error under test is the one after them.
			for {
				err = r.ReadFrame(&f)
				if err != nil {
					break
				}
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// A length prefix that cannot be honoured is refused BEFORE the body is read or
// allocated, which is the whole reason for the prefix. Both cases are fatal to
// the connection, so the reader deliberately does not try to resynchronize.
func TestReadFrameRefusesImpossibleLengths(t *testing.T) {
	tests := []struct {
		name string
		n    uint32
		want error
	}{
		{"zero length", 0, ErrFrameEmpty},
		{"one over the cap", MaxFrameBytes + 1, ErrFrameTooLarge},
		{"absurd length", 0xFFFFFFFF, ErrFrameTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hdr := make([]byte, FrameHeaderBytes)
			binary.BigEndian.PutUint32(hdr, tc.n)
			// No body follows: if the reader tried to read one it would block or
			// report a truncation instead of the refusal under test.
			r := NewFrameReader(bytes.NewReader(hdr))
			var f Frame
			err := r.ReadFrame(&f)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// A frame exactly at the cap is legal; the cap is on the body, not on the body
// plus its prefix.
func TestFrameAtTheCapIsAccepted(t *testing.T) {
	pad := strings.Repeat("x", MaxFrameBytes-len(`{"v":1,"type":"req","cmd":""}`))
	body := []byte(`{"v":1,"type":"req","cmd":"` + pad + `"}`)
	if len(body) != MaxFrameBytes {
		t.Fatalf("test built a %d byte body, want exactly %d", len(body), MaxFrameBytes)
	}
	var f Frame
	if err := NewFrameReader(bytes.NewReader(framed(body))).ReadFrame(&f); err != nil {
		t.Fatalf("a body of exactly MaxFrameBytes was refused: %v", err)
	}
	if len(f.Cmd) != len(pad) {
		t.Fatalf("Cmd truncated: %d bytes", len(f.Cmd))
	}
}

// An oversized OUTBOUND body is a daemon bug: the writer refuses it and writes
// nothing at all, because a truncated resync paints a wrong screen.
func TestWriteFrameRefusesOversizedBodyAndWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	f := Frame{V: 1, Type: FrameResync, Pane: "lola-fe-42"}
	if err := f.SetPayload(ResyncPayload{Cols: 1, Rows: 1, Lines: []string{strings.Repeat("y", MaxFrameBytes)}}); err != nil {
		t.Fatal(err)
	}
	err := NewFrameWriter(&buf).WriteFrame(&f)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("writer emitted %d bytes for a refused frame", buf.Len())
	}
}

// A malformed body is reported as ErrFrameMalformed and not confused with a
// framing failure: the frame boundary was intact, only its contents were not.
func TestReadFrameMalformedBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not json", "}{"},
		{"a bare array", `[1,2,3]`},
		{"wrong type for v", `{"v":"one","type":"req"}`},
		{"wrong type for seq", `{"v":1,"type":"pty","seq":"nine"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f Frame
			err := NewFrameReader(bytes.NewReader(framed([]byte(tc.body)))).ReadFrame(&f)
			if !errors.Is(err, ErrFrameMalformed) {
				t.Fatalf("err = %v, want ErrFrameMalformed", err)
			}
		})
	}
}

// Framing is not policy. An unknown type and an unknown version both DECODE
// fine — refusing them is the dispatcher's job, and it needs the decoded
// envelope (the id to answer on, the version to report) in order to refuse
// usefully rather than dropping the connection blind.
func TestUnknownTypeAndVersionDecodeButAreRefusedByPolicy(t *testing.T) {
	body := []byte(`{"v":99,"type":"pair.begin","id":"p1","payload":{"nonce":"x"}}`)
	var f Frame
	if err := NewFrameReader(bytes.NewReader(framed(body))).ReadFrame(&f); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if f.V != 99 || f.Type != "pair.begin" || f.ID != "p1" {
		t.Fatalf("envelope decoded wrong: %+v", f)
	}
	if SupportedFrameVersion(f.V) {
		t.Errorf("SupportedFrameVersion(99) = true, want false")
	}
	if KnownFrameType(f.Type) || DaemonAcceptsFrame(f.Type) || ClientAcceptsFrame(f.Type) {
		t.Errorf("an unknown type must fail closed in every direction")
	}
}

// Additive fields must not break an older peer: a frame from a newer build
// carrying fields this one has never heard of still decodes.
func TestUnknownFieldsAreTolerated(t *testing.T) {
	body := []byte(`{"v":1,"type":"sub","id":"s3","pane":"lola-fe-42","device":"ignored","payload":{"cols":55,"rows":34,"dpr":3}}`)
	var f Frame
	if err := NewFrameReader(bytes.NewReader(framed(body))).ReadFrame(&f); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	var sp SubPayload
	if err := f.DecodePayload(&sp); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if sp.Cols != 55 || sp.Rows != 34 {
		t.Fatalf("payload = %+v, want cols 55 rows 34", sp)
	}
}

func TestFrameDirections(t *testing.T) {
	tests := []struct {
		typ            string
		toDaemon, toCl bool
	}{
		{FrameReq, true, false},
		{FrameSub, true, false},
		{FrameUnsub, true, false},
		{FrameResp, false, true},
		{FrameResync, false, true},
		{FramePTY, true, true},
		{FrameErr, true, true},
		{"", false, false},
		{"REQ", false, false},
		{"event", false, false},
	}
	for _, tc := range tests {
		if got := DaemonAcceptsFrame(tc.typ); got != tc.toDaemon {
			t.Errorf("DaemonAcceptsFrame(%q) = %v, want %v", tc.typ, got, tc.toDaemon)
		}
		if got := ClientAcceptsFrame(tc.typ); got != tc.toCl {
			t.Errorf("ClientAcceptsFrame(%q) = %v, want %v", tc.typ, got, tc.toCl)
		}
		if got := KnownFrameType(tc.typ); got != (tc.toDaemon || tc.toCl) {
			t.Errorf("KnownFrameType(%q) = %v", tc.typ, got)
		}
	}
}

func TestSupportedFrameVersion(t *testing.T) {
	for _, v := range []int{-1, 0, FrameVersionCurrent + 1, 99} {
		if SupportedFrameVersion(v) {
			t.Errorf("SupportedFrameVersion(%d) = true, want false", v)
		}
	}
	for v := FrameVersionMin; v <= FrameVersionCurrent; v++ {
		if !SupportedFrameVersion(v) {
			t.Errorf("SupportedFrameVersion(%d) = false, want true", v)
		}
	}
}

// The version refusal is the one frame a peer that understands nothing else
// about this build still has to be able to read, so its shape is pinned: the
// DAEMON's own V, both bounds present, and a decodable ErrPayload.
func TestUnsupportedVersionFrameShape(t *testing.T) {
	f := UnsupportedVersionFrame("c7")
	if f.V != FrameVersionCurrent {
		t.Errorf("V = %d, want the daemon's own %d — never the peer's", f.V, FrameVersionCurrent)
	}
	if f.Type != FrameErr || f.ID != "c7" {
		t.Errorf("envelope = %+v", f)
	}
	var ep ErrPayload
	if err := f.DecodePayload(&ep); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if ep.Code != CodeUnsupportedVersion || ep.MinV != FrameVersionMin || ep.MaxV != FrameVersionCurrent {
		t.Fatalf("payload = %+v, want code %q with bounds %d..%d", ep, CodeUnsupportedVersion, FrameVersionMin, FrameVersionCurrent)
	}
	// omitempty would drop a zero bound and leave the client unable to name the
	// side that is behind. Both bounds must actually appear on the wire.
	raw := string(f.Payload)
	if !strings.Contains(raw, `"minV"`) || !strings.Contains(raw, `"maxV"`) {
		t.Fatalf("version bounds missing from the wire form: %s", raw)
	}
}

// An absent payload decodes as the zero value rather than an error, so a frame
// whose body is entirely optional needs no special case at the call site.
func TestDecodePayloadAbsent(t *testing.T) {
	f := Frame{V: 1, Type: FrameUnsub, Pane: "lola-fe-42"}
	sp := SubPayload{Cols: 9, Rows: 9}
	if err := f.DecodePayload(&sp); err != nil {
		t.Fatalf("DecodePayload on an absent body = %v, want nil", err)
	}
	if sp != (SubPayload{Cols: 9, Rows: 9}) {
		t.Fatalf("absent payload overwrote the destination: %+v", sp)
	}
	var bad Frame
	bad.Payload = json.RawMessage(`{"cols":`)
	if err := bad.DecodePayload(&sp); !errors.Is(err, ErrFrameMalformed) {
		t.Fatalf("err = %v, want ErrFrameMalformed", err)
	}
}

// Several goroutines share one connection — replies, pane output and resyncs —
// so a frame must never be interleaved with another frame's bytes.
func TestWriteFrameIsConcurrencySafe(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	const writers, each = 8, 50

	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				f := Frame{V: 1, Type: FramePTY, Pane: "lola-fe-42", Seq: uint64(g*each + i + 1)}
				if err := f.SetPayload(PTYOutputPayload{Data: bytes.Repeat([]byte{byte(g)}, 64)}); err != nil {
					t.Error(err)
					return
				}
				if err := w.WriteFrame(&f); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	r := NewFrameReader(&buf)
	seen := map[uint64]bool{}
	var f Frame
	for {
		err := r.ReadFrame(&f)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("frame %d of %d: %v", len(seen), writers*each, err)
		}
		if seen[f.Seq] {
			t.Fatalf("seq %d appeared twice", f.Seq)
		}
		seen[f.Seq] = true
	}
	if len(seen) != writers*each {
		t.Fatalf("read %d frames, wrote %d", len(seen), writers*each)
	}
}

// The reader's body buffer is reused across frames and bounded by the cap, which
// is the property that keeps a hot pane stream from allocating per frame.
func TestReadFrameReusesItsBuffer(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	for i := 0; i < 3; i++ {
		f := Frame{V: 1, Type: FramePTY, Pane: "lola-fe-42", Seq: uint64(i + 1)}
		if err := f.SetPayload(PTYOutputPayload{Data: bytes.Repeat([]byte{'z'}, 512)}); err != nil {
			t.Fatal(err)
		}
		if err := w.WriteFrame(&f); err != nil {
			t.Fatal(err)
		}
	}
	r := NewFrameReader(&buf)
	var f Frame
	if err := r.ReadFrame(&f); err != nil {
		t.Fatal(err)
	}
	first := &r.buf[0]
	for i := 0; i < 2; i++ {
		if err := r.ReadFrame(&f); err != nil {
			t.Fatal(err)
		}
	}
	if &r.buf[0] != first {
		t.Error("the body buffer was reallocated for a same-sized frame")
	}
	if cap(r.buf) > MaxFrameBytes {
		t.Errorf("buffer grew to %d, past the %d cap", cap(r.buf), MaxFrameBytes)
	}
}

func TestReadFrameNilDestination(t *testing.T) {
	if err := NewFrameReader(bytes.NewReader(nil)).ReadFrame(nil); err == nil {
		t.Fatal("ReadFrame(nil) = nil, want an error")
	}
}
