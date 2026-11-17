package protocol

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type pairingVectors struct {
	Proof struct {
		Secret        string `json:"secret"`
		ExporterProof string `json:"exporterProof"`
		Proof         string `json:"proof"`
	} `json:"proof"`
	Code struct {
		ExporterCode   string `json:"exporterCode"`
		DaemonSPKIHash string `json:"daemonSPKIHash"`
		PhoneSPKIHash  string `json:"phoneSPKIHash"`
		Code           string `json:"code"`
	} `json:"code"`
	Payloads []struct {
		Name    string          `json:"name"`
		Encoded string          `json:"encoded"`
		Decoded json.RawMessage `json:"decoded"`
	} `json:"payloads"`
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// The vectors are produced by an independent implementation (HMAC-SHA256 in
// Python). The Swift Core tests read the same file, so one change here is a change
// on every side. The payload round trip is internal/remote's TestPairPayloadVectors.
func TestPairingVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/pairing_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v pairingVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}

	got := base64.RawURLEncoding.EncodeToString(PairProof(mustHex(t, v.Proof.Secret), mustHex(t, v.Proof.ExporterProof)))
	if got != v.Proof.Proof {
		t.Errorf("PairProof = %s, want %s", got, v.Proof.Proof)
	}

	var d, p [32]byte
	copy(d[:], mustHex(t, v.Code.DaemonSPKIHash))
	copy(p[:], mustHex(t, v.Code.PhoneSPKIHash))
	if c := PairCode(mustHex(t, v.Code.ExporterCode), d, p); c != v.Code.Code {
		t.Errorf("PairCode = %s, want %s", c, v.Code.Code)
	}
	// The code depends on both identities and the order they are given in.
	if PairCode(mustHex(t, v.Code.ExporterCode), p, d) == v.Code.Code {
		t.Error("PairCode ignores the order of the SPKI hashes")
	}

	if len(v.Payloads) != 3 {
		t.Fatalf("want 3 payload vectors, got %d", len(v.Payloads))
	}
	for _, pl := range v.Payloads {
		if len(pl.Encoded) < 7 || pl.Encoded[:6] != "lola2." {
			t.Errorf("%s: encoded %q lacks the lola2. prefix", pl.Name, pl.Encoded)
			continue
		}
		dec, err := base64.RawURLEncoding.DecodeString(pl.Encoded[6:])
		if err != nil {
			t.Errorf("%s: %v", pl.Name, err)
			continue
		}
		var a, b any
		if json.Unmarshal(dec, &a) != nil || json.Unmarshal(pl.Decoded, &b) != nil {
			t.Errorf("%s: not JSON", pl.Name)
			continue
		}
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Errorf("%s: encoded payload decodes to %s, vector says %s", pl.Name, ja, jb)
		}
	}
}
