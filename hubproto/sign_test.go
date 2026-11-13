package hubproto

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestCanonicalString(t *testing.T) {
	got := CanonicalString("POST", "/v1/heartbeat", "1700000000", "0123456789abcdef", []byte("{}"))
	want := "POST\n/v1/heartbeat\n1700000000\n0123456789abcdef\n" +
		"44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	empty := CanonicalString("GET", "/v1/offers", "1", "0123456789abcdef", nil)
	if !strings.HasSuffix(empty, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") {
		t.Fatalf("empty body hash: %q", empty)
	}
}

func TestKeyEncodingRoundTrip(t *testing.T) {
	pub, _ := mustKey(t)
	enc := EncodePublicKey(pub)
	if strings.ContainsAny(enc, "=+/") {
		t.Fatalf("not raw url: %q", enc)
	}
	got, err := DecodePublicKey(enc)
	if err != nil || !bytes.Equal(got, pub) {
		t.Fatalf("round trip: %v", err)
	}
	for _, bad := range []string{"", enc + "=", enc[:42], enc + "A", strings.Replace(enc, enc[:1], "+", 1), "!" + enc[1:]} {
		if _, err := DecodePublicKey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDeviceID(t *testing.T) {
	pub, _ := mustKey(t)
	id := DeviceID(pub)
	if !ValidDeviceID(id) || len(id) != 32 {
		t.Fatalf("bad id %q", id)
	}
	if DeviceID(pub) != id {
		t.Fatal("not deterministic")
	}
	pub2, _ := mustKey(t)
	if DeviceID(pub2) == id {
		t.Fatal("collision")
	}
	if ValidDeviceID(strings.ToUpper(id)) || ValidDeviceID(id+"0") {
		t.Fatal("loose device id")
	}
}

func TestNewNonce(t *testing.T) {
	a, _ := NewNonce()
	b, _ := NewNonce()
	if a == b || !nonceRe.MatchString(a) {
		t.Fatalf("nonce %q %q", a, b)
	}
}

func signed(t *testing.T, method, target string, body []byte, priv ed25519.PrivateKey, dev string, at time.Time) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, "https://hub.example"+target, bytes.NewReader(body))
	if err := SignAt(req, dev, priv, body, at, "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestSignVerify(t *testing.T) {
	pub, priv := mustKey(t)
	dev := DeviceID(pub)
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"free_slots":2}`)

	t.Run("ok", func(t *testing.T) {
		req := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
		sh, err := Verify(req, body, pub, now)
		if err != nil {
			t.Fatal(err)
		}
		if sh.Device != dev || sh.Nonce != "0123456789abcdef0123456789abcdef" || !sh.Timestamp.Equal(now) {
			t.Fatalf("headers %+v", sh)
		}
	})
	t.Run("Sign uses fresh nonce", func(t *testing.T) {
		req := httptest.NewRequest("GET", "https://hub.example/v1/offers", nil)
		if err := Sign(req, dev, priv, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(req, nil, pub, time.Now()); err != nil {
			t.Fatal(err)
		}
	})

	other, otherPriv := mustKey(t)
	cases := []struct {
		name string
		req  func() *http.Request
		body []byte
		pub  ed25519.PublicKey
		now  time.Time
		want error
	}{
		{"tampered body", func() *http.Request { return signed(t, "POST", PathHeartbeat, body, priv, dev, now) }, []byte(`{"free_slots":9}`), pub, now, ErrBadSignature},
		{"tampered path", func() *http.Request {
			r := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
			r.URL.Path = PathFindings
			return r
		}, body, pub, now, ErrBadSignature},
		{"tampered query", func() *http.Request {
			r := signed(t, "GET", "/v1/offers?limit=1", nil, priv, dev, now)
			r.URL.RawQuery = "limit=100"
			return r
		}, nil, pub, now, ErrBadSignature},
		{"tampered method", func() *http.Request {
			r := signed(t, "POST", ReleasePath("item1"), nil, priv, dev, now)
			r.Method = "GET"
			return r
		}, nil, pub, now, ErrBadSignature},
		{"tampered nonce", func() *http.Request {
			r := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
			r.Header.Set(HeaderNonce, "ffffffffffffffffffffffffffffffff")
			return r
		}, body, pub, now, ErrBadSignature},
		{"tampered timestamp", func() *http.Request {
			r := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
			r.Header.Set(HeaderTimestamp, "1700000001")
			return r
		}, body, pub, now, ErrBadSignature},
		{"skew past", func() *http.Request { return signed(t, "POST", PathHeartbeat, body, priv, dev, now) }, body, pub, now.Add(MaxSkew + time.Second), ErrSkew},
		{"skew future", func() *http.Request { return signed(t, "POST", PathHeartbeat, body, priv, dev, now) }, body, pub, now.Add(-MaxSkew - time.Second), ErrSkew},
		{"device header for other key", func() *http.Request { return signed(t, "POST", PathHeartbeat, body, priv, DeviceID(other), now) }, body, other, now, ErrBadSignature},
		{"wrong key for device", func() *http.Request { return signed(t, "POST", PathHeartbeat, body, otherPriv, dev, now) }, body, other, now, ErrWrongDevice},
		{"missing signature", func() *http.Request {
			r := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
			r.Header.Del(HeaderSignature)
			return r
		}, body, pub, now, ErrMissingHeader},
		{"repeated header", func() *http.Request {
			r := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
			r.Header.Add(HeaderNonce, "0123456789abcdef0123456789abcdef")
			return r
		}, body, pub, now, ErrBadHeader},
		{"newline in nonce", func() *http.Request {
			r := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
			r.Header[HeaderNonce] = []string{"0123456789abcdef\n/v1/x"}
			return r
		}, body, pub, now, ErrBadHeader},
		{"negative timestamp", func() *http.Request {
			r := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
			r.Header.Set(HeaderTimestamp, "-1")
			return r
		}, body, pub, now, ErrBadHeader},
		{"padded signature", func() *http.Request {
			r := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
			r.Header.Set(HeaderSignature, r.Header.Get(HeaderSignature)+"==")
			return r
		}, body, pub, now, ErrBadHeader},
		{"short key", func() *http.Request { return signed(t, "POST", PathHeartbeat, body, priv, dev, now) }, body, pub[:16], now, ErrBadKey},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Verify(c.req(), c.body, c.pub, c.now)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
	t.Run("skew edge accepted", func(t *testing.T) {
		req := signed(t, "POST", PathHeartbeat, body, priv, dev, now)
		if _, err := Verify(req, body, pub, now.Add(MaxSkew)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSignAtRejectsBadInputs(t *testing.T) {
	pub, priv := mustKey(t)
	req := httptest.NewRequest("GET", "/v1/offers", nil)
	if err := SignAt(req, "NOT-AN-ID", priv, nil, time.Now(), "0123456789abcdef"); err == nil {
		t.Error("bad device id accepted")
	}
	if err := SignAt(req, DeviceID(pub), priv, nil, time.Now(), "short"); err == nil {
		t.Error("short nonce accepted")
	}
	if err := SignAt(req, DeviceID(pub), priv[:10], nil, time.Now(), "0123456789abcdef"); err == nil {
		t.Error("bad private key accepted")
	}
}
