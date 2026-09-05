package remote

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleCode is the shape a real M1 daemon produces: a loopback-forced listener
// on both families, a standard-base64 SPKI pin and the 32 hex characters
// contrib/lola-mobile-dev.sh generates.
func sampleCode() ConnectCode {
	return ConnectCode{
		V:     1,
		Addrs: []string{"127.0.0.1", "::1"},
		Port:  7717,
		Pin:   "C4td4uyeJMSyxfoAsB3i98Kd6JhkpOTf3Oxipiq+sxI=",
		Key:   "0123456789abcdef0123456789abcdef",
	}
}

func TestConnectCodeRoundTrips(t *testing.T) {
	want := sampleCode()
	tok, err := EncodeConnectCode(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeConnectCode(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Port != want.Port || got.Pin != want.Pin || got.Key != want.Key {
		t.Fatalf("round trip changed the code: %+v", got)
	}
	if strings.Join(got.Addrs, ",") != strings.Join(want.Addrs, ",") {
		t.Fatalf("addrs round trip: got %v want %v", got.Addrs, want.Addrs)
	}
}

// The GOLDEN VECTOR: one token, in one file, read by BOTH ends of the hand-off.
//
// This test and mobile/src/lib/pairpayload.test.ts load the same
// mobile/src/lib/testdata/connectcode.json. This one asserts that
// EncodeConnectCode produces that exact token from those exact fields and reads
// it back; the TypeScript one asserts that parsePairing reads the same token
// into the same values and picks the same address to dial.
//
// It is worth the file it costs because of WHERE the alternative failure lands.
// One side writing padded base64 and the other raw, a field renamed, a pin in
// the other alphabet, a port that arrives as a string: none of those is a
// compile error on either side, none shows up in a unit test of one side alone,
// and all of them surface as a phone refusing to scan a square — which looks
// exactly like a dirty lens, a dim room or a broken camera. Here they surface in
// `make check`.
//
// GO IS THE SOURCE OF TRUTH, the same way it is for the wire vectors in
// mobile/src/wire/testdata/frames.json. If this fails, the daemon is right and
// the fixture is wrong: fix connectcode.json and then the TypeScript, never this
// package. Changing the token is a wire break and the diff should say so.
const connectCodeVectorPath = "../../mobile/src/lib/testdata/connectcode.json"

type connectCodeVector struct {
	Token    string      `json:"token"`
	Fields   ConnectCode `json:"fields"`
	DialHost string      `json:"dialHost"`
	Refused  []struct {
		Name  string `json:"name"`
		Why   string `json:"why"`
		Token string `json:"token"`
	} `json:"refused"`
}

func loadConnectCodeVector(t *testing.T) connectCodeVector {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(connectCodeVectorPath))
	if err != nil {
		t.Fatalf("read the shared connect-code vector: %v", err)
	}
	var v connectCodeVector
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s is not decodable: %v", connectCodeVectorPath, err)
	}
	return v
}

func TestConnectCodeIsAGoldenVector(t *testing.T) {
	v := loadConnectCodeVector(t)

	got, err := EncodeConnectCode(v.Fields)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got != v.Token {
		t.Fatalf("connect code bytes changed\n got: %s\nwant: %s", got, v.Token)
	}

	// And back, so the fixture pins a value this package can actually read
	// rather than only one it can write.
	back, err := DecodeConnectCode(v.Token)
	if err != nil {
		t.Fatalf("decode the vector's own token: %v", err)
	}
	if back.Port != v.Fields.Port || back.Pin != v.Fields.Pin || back.Key != v.Fields.Key {
		t.Fatalf("the vector did not round trip: %+v", back)
	}
	if strings.Join(back.Addrs, ",") != strings.Join(v.Fields.Addrs, ",") {
		t.Fatalf("addrs: got %v want %v", back.Addrs, v.Fields.Addrs)
	}

	// The address the phone will dial. Under lola_insecure the listener binds
	// loopback only, so the fixture's answer is the Simulator's working case and
	// the TypeScript picks it with the same rule.
	if len(back.Addrs) == 0 || back.Addrs[0] != v.DialHost {
		t.Fatalf("dial host: got %v want %q", back.Addrs, v.DialHost)
	}

	// The sample this package's other tests build by hand must stay the same
	// code, or the fixture and the unit tests would describe two formats.
	if tok, err := EncodeConnectCode(sampleCode()); err != nil || tok != v.Token {
		t.Fatalf("sampleCode() has drifted from the shared vector:\n got: %s (%v)\nwant: %s", tok, err, v.Token)
	}
}

// Every token the fixture says must be refused, refused HERE too. The mobile
// decoder is deliberately more tolerant than this one in places it documents
// (a bare `host`, a padded body, a base64url pin), so the shared refusals are
// only the ones both ends genuinely agree on — a fixture that asserted Go's
// extra strictness would fail the TypeScript for being right.
func TestConnectCodeRefusesTheSharedBadCases(t *testing.T) {
	for _, c := range loadConnectCodeVector(t).Refused {
		t.Run(c.Name, func(t *testing.T) {
			if _, err := DecodeConnectCode(c.Token); err == nil {
				t.Fatalf("accepted a code the shared vector says must be refused (%s)", c.Why)
			}
		})
	}
}

// TestConnectCodePrefixIsVersioned. The mobile decoder matches lola(\d+)\. and
// accepts only its own digit, so M2's differently-shaped blob becomes "lola2."
// and neither side ever decodes a payload whose fields it does not have.
func TestConnectCodePrefixIsVersioned(t *testing.T) {
	tok, err := EncodeConnectCode(sampleCode())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// The DIGIT is the version, which is what lets an M2 client name the code
	// it is looking at instead of reporting a corrupt scan.
	if !strings.HasPrefix(tok, "lola1.") {
		t.Fatalf("prefix changed: %s", tok)
	}
}

// TestConnectCodeIsNotAURL is the reason this is a token at all: a scheme the
// OS routes hands the bearer key to whichever app claimed it, and people scan
// with the system camera.
func TestConnectCodeIsNotAURL(t *testing.T) {
	tok, err := EncodeConnectCode(sampleCode())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(tok, "://") || strings.Contains(tok, "?") || strings.Contains(tok, "&") {
		t.Fatalf("the connect code must not look like a URL: %s", tok)
	}
}

// TestConnectCodeBodyIsURLSafe: the token gets pasted through shells, READMEs
// and issue trackers by anyone debugging a failed scan, so no character in it
// may need escaping.
func TestConnectCodeBodyIsURLSafe(t *testing.T) {
	tok, err := EncodeConnectCode(sampleCode())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	body := strings.TrimPrefix(tok, "lola1.")
	for i, r := range body {
		ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			t.Fatalf("byte %d of the body is %q, which is not base64url", i, r)
		}
	}
	if strings.Contains(body, "=") {
		t.Fatal("the body must be unpadded base64url")
	}
}

func TestEncodeConnectCodeRefusesAnIncompleteCode(t *testing.T) {
	cases := map[string]func(*ConnectCode){
		"no address": func(c *ConnectCode) { c.Addrs = nil },
		"no pin":     func(c *ConnectCode) { c.Pin = "" },
		"no key":     func(c *ConnectCode) { c.Key = "" },
		"port 0":     func(c *ConnectCode) { c.Port = 0 },
		"port hi":    func(c *ConnectCode) { c.Port = 70000 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := sampleCode()
			mutate(&c)
			if _, err := EncodeConnectCode(c); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
}

// TestEncodeConnectCodeErrorsNeverCarryTheKey: every refusal in this package
// names the field, never the value, and the key is the field that matters.
func TestEncodeConnectCodeErrorsNeverCarryTheKey(t *testing.T) {
	c := sampleCode()
	c.Pin = ""
	_, err := EncodeConnectCode(c)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), c.Key) {
		t.Fatalf("the error carries the bearer key: %v", err)
	}
}

func TestDecodeConnectCodeFailsClosed(t *testing.T) {
	body := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return "lola1." + base64.RawURLEncoding.EncodeToString(b)
	}
	good := sampleCode()

	cases := []struct{ name, in string }{
		{"empty", ""},
		{"plain text", "hello"},
		{"a URL", "lola://connect?host=127.0.0.1&key=secret"},
		{"M2's prefix", "lola2.eyJ2IjoxfQ"},
		{"prefix only", "lola1."},
		{"body is not base64url", "lola1.!!!!"},
		{"body is not JSON", "lola1." + base64.RawURLEncoding.EncodeToString([]byte("nope"))},
		{"unknown version", body(map[string]any{"v": 2, "addrs": good.Addrs, "port": good.Port, "pin": good.Pin, "key": good.Key})},
		{"no address", body(map[string]any{"v": 1, "port": good.Port, "pin": good.Pin, "key": good.Key})},
		{"no pin", body(map[string]any{"v": 1, "addrs": good.Addrs, "port": good.Port, "key": good.Key})},
		{"no key", body(map[string]any{"v": 1, "addrs": good.Addrs, "port": good.Port, "pin": good.Pin})},
		{"port out of range", body(map[string]any{"v": 1, "addrs": good.Addrs, "port": 70000, "pin": good.Pin, "key": good.Key})},
		{"unknown field", body(map[string]any{"v": 1, "addrs": good.Addrs, "port": good.Port, "pin": good.Pin, "key": good.Key, "cmd": "rm -rf"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeConnectCode(tc.in); err == nil {
				t.Fatalf("decoded something that is not a connect code: %q", tc.in)
			} else if !errors.Is(err, ErrNotConnectCode) {
				t.Fatalf("refusal should be ErrNotConnectCode, got %v", err)
			}
		})
	}
}

// TestDecodeConnectCodeBoundsItsInput: Decode is the half a phone runs over
// camera input, so an unbounded body would be an unbounded allocation driven by
// whatever was pointed at the lens.
func TestDecodeConnectCodeBoundsItsInput(t *testing.T) {
	huge := "lola1." + strings.Repeat("A", maxConnectCodeBytes)
	if _, err := DecodeConnectCode(huge); err == nil {
		t.Fatal("an oversized token must be refused before it is decoded")
	}
}

// TestDecodeConnectCodeToleratesSurroundingWhitespace: the token is pasted by
// hand as often as it is scanned, and a trailing newline out of a terminal must
// not read as a corrupt code.
func TestDecodeConnectCodeToleratesSurroundingWhitespace(t *testing.T) {
	tok, err := EncodeConnectCode(sampleCode())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := DecodeConnectCode("  " + tok + "\n"); err != nil {
		t.Fatalf("a pasted token should decode: %v", err)
	}
}

// TestEncodeConnectCodeDefaultsTheVersion so a caller that fills the four facts
// cannot accidentally emit v=0, which every decoder refuses.
func TestEncodeConnectCodeDefaultsTheVersion(t *testing.T) {
	c := sampleCode()
	c.V = 0
	tok, err := EncodeConnectCode(c)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeConnectCode(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.V != ConnectCodeVersion {
		t.Fatalf("version %d, want %d", got.V, ConnectCodeVersion)
	}
}
