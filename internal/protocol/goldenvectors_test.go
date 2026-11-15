package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The GOLDEN VECTORS: one file of frames, their decoded JSON value and their
// exact wire bytes, read by BOTH sides of the wire.
//
// This test and mobile/src/wire/codec.test.ts load the same
// mobile/src/wire/testdata/*.json. This one asserts that the Go codec
// produces those bytes; the TypeScript one asserts that its encoder produces
// them too, and that its decoder round-trips them. There is no code generator
// between Go, TypeScript and (from M1) Swift — the client's view of this
// protocol is a hand-maintained mirror, in the same spirit as
// desktop/frontend/src/lib/theme.ts mirroring internal/state — so this file is
// the only mechanical thing holding the three together.
//
// It is worth the file it costs because of WHERE the alternative failure lands.
// A field renamed on one side, an omitempty added, a []byte that turns out to
// marshal as null rather than "", a length prefix written little-endian: none of
// those is a compile error anywhere, none shows up in a unit test of either side
// alone, and all of them surface on a physical phone, over a socket, with no
// debugger attached. Here they surface in `make check`.
//
// GO IS THE SOURCE OF TRUTH. If this test fails, the daemon is right and the
// vector is wrong: fix frames.json (and then the TypeScript), never this
// package. The only legitimate reason to change an EXISTING vector's bytes is a
// deliberate, versioned protocol change — and one of those has to bump
// FrameVersionCurrent, which will fail this test loudly enough to notice.

// goldenVectorDir locates the shared vector directory from this package's
// directory. It is deliberately a relative path rather than an embed: the files
// belong to the mobile client, which is where they are edited, and copying them
// here would create exactly the second copy this whole exercise exists to avoid.
//
// The directory holds several files of one schema so that a spec can add vectors
// without editing a shared one: frames.json (envelope and panes), events.json
// (watch, event, attachments) and pairing.json (the remote.pair handshake and
// ready). Every *.json file is loaded, sorted by name, and the cases are the
// union; a case name is unique across all of them. The files named in
// notFrameVectors share the directory but hold another schema, pinned by their
// own writer's test.
const goldenVectorDir = "../../mobile/src/wire/testdata"

// notFrameVectors are the files in goldenVectorDir that are not frame vectors:
// tiers.json is the {cmd: tier} map internal/remote's tiers golden test writes.
var notFrameVectors = map[string]bool{"tiers.json": true}

type goldenVectorFile struct {
	Note  string         `json:"note"`
	Cases []goldenVector `json:"cases"`
}

type goldenVector struct {
	Name string `json:"name"`
	Why  string `json:"why"`
	// Frame is the frame's decoded JSON value. It is kept RAW so the payload
	// reaches protocol.Frame.Payload as a json.RawMessage carrying the source's
	// own key order, which is what makes the byte comparison meaningful: the
	// order inside a payload is the payload struct's declaration order, and a
	// reordering there is a real wire change that this test must catch.
	Frame json.RawMessage `json:"frame"`
	// Hex is the whole wire frame: the 4-byte big-endian length prefix followed
	// by the JSON body.
	Hex string `json:"hex"`
}

// goldenVectorFiles lists the vector files, sorted.
func goldenVectorFiles(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(filepath.FromSlash(goldenVectorDir), "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden vector files under %s (%v)", goldenVectorDir, err)
	}
	vectors := files[:0]
	for _, f := range files {
		if !notFrameVectors[filepath.Base(f)] {
			vectors = append(vectors, f)
		}
	}
	if len(vectors) == 0 {
		t.Fatalf("no golden vector files under %s", goldenVectorDir)
	}
	sort.Strings(vectors)
	return vectors
}

// loadGoldenVectors returns the union of every vector file's cases.
func loadGoldenVectors(t *testing.T) goldenVectorFile {
	t.Helper()
	var all goldenVectorFile
	seen := map[string]string{}
	for _, path := range goldenVectorFiles(t) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read golden vectors: %v", err)
		}
		var gf goldenVectorFile
		if err := json.Unmarshal(b, &gf); err != nil {
			t.Fatalf("%s is not decodable: %v", path, err)
		}
		if len(gf.Cases) == 0 {
			t.Fatalf("%s carries no cases", path)
		}
		for _, c := range gf.Cases {
			if prev, dup := seen[c.Name]; dup {
				t.Fatalf("vector name %q is in both %s and %s", c.Name, prev, path)
			}
			seen[c.Name] = path
		}
		all.Cases = append(all.Cases, gf.Cases...)
	}
	return all
}

// vectorHex is the wire form of a vector's frame, written by this package's
// FrameWriter.
func vectorHex(t *testing.T, tc goldenVector) string {
	t.Helper()
	var f Frame
	if err := json.Unmarshal(tc.Frame, &f); err != nil {
		t.Fatalf("%s: the vector's frame is not a protocol.Frame: %v", tc.Name, err)
	}
	var buf bytes.Buffer
	if err := NewFrameWriter(&buf).WriteFrame(&f); err != nil {
		t.Fatalf("%s: WriteFrame: %v", tc.Name, err)
	}
	return hex.EncodeToString(buf.Bytes())
}

// TestWriteGoldenVectors rewrites the hex of every case in every vector file
// from its frame. It runs only with LOLA_WRITE_VECTORS=1; a normal run compares
// (the encode test below) and never writes. Only the hex string literal changes,
// so a file's formatting and key order are preserved.
func TestWriteGoldenVectors(t *testing.T) {
	if os.Getenv("LOLA_WRITE_VECTORS") != "1" {
		t.Skip("set LOLA_WRITE_VECTORS=1 to rewrite the golden vector hex")
	}
	for _, path := range goldenVectorFiles(t) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var gf goldenVectorFile
		if err := json.Unmarshal(b, &gf); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		text := string(b)
		cursor := 0
		for _, tc := range gf.Cases {
			at := strings.Index(text[cursor:], `"name": "`+tc.Name+`"`)
			if at < 0 {
				t.Fatalf("%s: cannot find case %q in the file text", path, tc.Name)
			}
			at += cursor
			loc := hexLiteral.FindStringSubmatchIndex(text[at:])
			if loc == nil {
				t.Fatalf("%s: case %q has no hex literal", path, tc.Name)
			}
			start, end := at+loc[2], at+loc[3]
			text = text[:start] + vectorHex(t, tc) + text[end:]
			cursor = start
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var hexLiteral = regexp.MustCompile(`"hex":\s*"([0-9a-f]*)"`)

// Every vector's decoded frame, written by this package's FrameWriter, must
// produce exactly the bytes the vector states — prefix included.
func TestGoldenVectorsEncodeToTheStatedBytes(t *testing.T) {
	gf := loadGoldenVectors(t)

	seen := map[string]bool{}
	for _, tc := range gf.Cases {
		if tc.Name == "" {
			t.Fatalf("a vector has no name")
		}
		if seen[tc.Name] {
			t.Fatalf("duplicate vector name %q", tc.Name)
		}
		seen[tc.Name] = true

		t.Run(tc.Name, func(t *testing.T) {
			got := vectorHex(t, tc)
			if got != tc.Hex {
				// The message carries the bytes in the exact shape the vector
				// file wants, so a legitimate protocol change is a copy and a
				// paste rather than a hex dump read by hand.
				body, _ := hex.DecodeString(got)
				t.Fatalf("wire bytes differ (LOLA_WRITE_VECTORS=1 rewrites them).\n want %q\n  got %q\n body %s",
					tc.Hex, got, body[FrameHeaderBytes:])
			}
		})
	}
}

// The stated bytes must also be READABLE by this package, and must decode back
// to the same envelope with a semantically identical payload. Encoding alone
// would let a vector pin a shape the reader cannot accept.
func TestGoldenVectorsDecodeBackToTheSameFrame(t *testing.T) {
	gf := loadGoldenVectors(t)

	for _, tc := range gf.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.Hex)
			if err != nil {
				t.Fatalf("hex: %v", err)
			}
			var want Frame
			if err := json.Unmarshal(tc.Frame, &want); err != nil {
				t.Fatalf("the vector's frame is not a protocol.Frame: %v", err)
			}

			r := NewFrameReader(bytes.NewReader(raw))
			var got Frame
			if err := r.ReadFrame(&got); err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if got.V != want.V || got.Type != want.Type || got.ID != want.ID ||
				got.Cmd != want.Cmd || got.Pane != want.Pane || got.Topic != want.Topic || got.Seq != want.Seq {
				t.Fatalf("envelope changed:\n want %+v\n  got %+v", want, got)
			}
			if len(want.Payload) == 0 {
				if len(got.Payload) != 0 {
					t.Fatalf("absent payload came back as %q", got.Payload)
				}
			} else {
				var a, b any
				if err := json.Unmarshal(want.Payload, &a); err != nil {
					t.Fatalf("vector payload: %v", err)
				}
				if err := json.Unmarshal(got.Payload, &b); err != nil {
					t.Fatalf("decoded payload: %v", err)
				}
				if !jsonEqual(a, b) {
					t.Fatalf("payload changed:\n want %v\n  got %v", a, b)
				}
			}
			// Nothing may follow one frame's bytes: a vector that accidentally
			// held two frames would pass every other assertion here.
			if err := r.ReadFrame(&got); err == nil {
				t.Fatalf("the vector's bytes hold more than one frame")
			}
		})
	}
}

// The framing itself, asserted against the bytes rather than against the codec:
// a 4-byte BIG-ENDIAN prefix whose value is the body length, a body within
// MaxFrameBytes, and no trailing delimiter. Endianness in particular is the kind
// of thing a second implementation gets backwards and a round-trip test of that
// implementation alone can never catch.
func TestGoldenVectorsCarryABigEndianLengthPrefix(t *testing.T) {
	gf := loadGoldenVectors(t)

	for _, tc := range gf.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.Hex)
			if err != nil {
				t.Fatalf("hex: %v", err)
			}
			if len(raw) <= FrameHeaderBytes {
				t.Fatalf("frame is %d bytes, which leaves no body", len(raw))
			}
			body := raw[FrameHeaderBytes:]
			// Written out rather than calling binary.BigEndian, so the test
			// states the byte order instead of asking the codec what it is.
			n := int(raw[0])<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
			if n != len(body) {
				t.Fatalf("prefix says %d bytes, body is %d", n, len(body))
			}
			if n > MaxFrameBytes {
				t.Fatalf("body is %d bytes, over the %d cap", n, MaxFrameBytes)
			}
			if body[len(body)-1] == '\n' {
				t.Fatalf("body ends in a newline; this framing has no delimiter")
			}
			if !json.Valid(body) {
				t.Fatalf("body is not valid JSON: %s", body)
			}
		})
	}
}

// The vector set has to cover every frame kind, or a kind added later ships with
// no cross-language check at all — which is precisely the state this file was
// written to end.
func TestGoldenVectorsCoverEveryFrameKind(t *testing.T) {
	gf := loadGoldenVectors(t)

	covered := map[string]bool{}
	for _, tc := range gf.Cases {
		var f Frame
		if err := json.Unmarshal(tc.Frame, &f); err != nil {
			t.Fatalf("%s: %v", tc.Name, err)
		}
		covered[f.Type] = true
	}
	for _, typ := range []string{FrameReq, FrameResp, FrameSub, FrameUnsub, FrameResync, FramePTY, FrameErr, FrameEvent, FrameReady} {
		if !covered[typ] {
			t.Errorf("no golden vector carries a %q frame", typ)
		}
	}
}

// The three shapes a client is most likely to get wrong, pinned by name so that
// deleting the vector is a deliberate act rather than an accident. Each one has
// already been a real cross-language bug in some protocol somewhere, and each is
// invisible in a Go-only test because Go's own round trip is symmetric.
func TestGoldenVectorsPinTheEasilyMissedShapes(t *testing.T) {
	gf := loadGoldenVectors(t)

	byName := map[string]goldenVector{}
	for _, tc := range gf.Cases {
		byName[tc.Name] = tc
	}

	t.Run("cursorHidden is stated in the negative", func(t *testing.T) {
		hidden, ok := byName["resync_cursor_hidden"]
		if !ok {
			t.Fatal("the resync_cursor_hidden vector is gone")
		}
		visible, ok := byName["resync_cursor_visible"]
		if !ok {
			t.Fatal("the resync_cursor_visible vector is gone")
		}
		if !strings.Contains(string(hidden.Frame), `"cursorHidden": true`) &&
			!strings.Contains(string(hidden.Frame), `"cursorHidden":true`) {
			t.Error("the hidden-cursor vector no longer sets cursorHidden")
		}
		if strings.Contains(string(visible.Frame), "cursorHidden") {
			t.Error("the visible-cursor vector must OMIT cursorHidden: an absent field means visible")
		}
		// And the omission has to be real on the wire, not just in the vector.
		raw, err := hex.DecodeString(visible.Hex)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("cursorHidden")) {
			t.Error("cursorHidden reached the wire for a visible cursor")
		}
	})

	t.Run("pty output data has no omitempty", func(t *testing.T) {
		for _, name := range []string{"pty_output_empty_string", "pty_output_null_data"} {
			v, ok := byName[name]
			if !ok {
				t.Fatalf("the %s vector is gone", name)
			}
			raw, err := hex.DecodeString(v.Hex)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"data":`)) {
				t.Errorf("%s: data was dropped from the wire; PTYOutputPayload.Data carries no omitempty", name)
			}
		}
		// The null case is the one a client breaks on: encoding/json writes a
		// nil []byte as null, and a base64 decoder handed null throws.
		nul := byName["pty_output_null_data"]
		raw, _ := hex.DecodeString(nul.Hex)
		if !bytes.Contains(raw, []byte(`"data":null`)) {
			t.Error("the nil-slice vector no longer carries data:null")
		}
	})

	t.Run("html characters are escaped the way encoding/json escapes them", func(t *testing.T) {
		v, ok := byName["resync_html_escaped_lines"]
		if !ok {
			t.Fatal("the resync_html_escaped_lines vector is gone")
		}
		raw, err := hex.DecodeString(v.Hex)
		if err != nil {
			t.Fatal(err)
		}
		// The escaped forms, written with a doubled backslash so the Go source
		// says exactly which six bytes have to be on the wire.
		for _, esc := range []string{"\\u003c", "\\u003e", "\\u0026"} {
			if !bytes.Contains(raw, []byte(esc)) {
				t.Errorf("%s is missing from the wire form; a JSON.stringify-based client will diverge here", esc)
			}
		}
		for _, lit := range []byte("<>&") {
			if bytes.ContainsRune(raw[FrameHeaderBytes:], rune(lit)) {
				t.Errorf("a literal %q survived into the body", string(lit))
			}
		}
	})
}
