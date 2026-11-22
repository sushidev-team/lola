package linear

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPKCEChallengeIsS256OfVerifier(t *testing.T) {
	v, c, err := NewPKCE()
	if err != nil || len(v) < 43 {
		t.Fatalf("verifier %q err %v", v, err)
	}
	sum := sha256.Sum256([]byte(v))
	if c != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatalf("challenge %q does not match the verifier", c)
	}
	u, _ := url.Parse(AgentAuthorizeURLPKCE("cid", "http://localhost/cb", "st", c))
	q := u.Query()
	if q.Get("code_challenge") != c || q.Get("code_challenge_method") != "S256" || q.Get("actor") != "app" {
		t.Fatalf("authorize URL = %s", u)
	}
}

// Without a client secret the exchange and the refresh carry the verifier / the
// client id only, and no empty client_secret field.
func TestOAuthWorksWithoutAClientSecret(t *testing.T) {
	var forms []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms = append(forms, r.PostForm)
		io.WriteString(w, `{"access_token":"A","refresh_token":"R","expires_in":60}`)
	}))
	defer srv.Close()
	o := OAuthClient{TokenURL: srv.URL, ClientID: "cid"}
	if _, err := o.Exchange(context.Background(), "code", "http://localhost/cb", "ver"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Refresh(context.Background(), "R"); err != nil {
		t.Fatal(err)
	}
	for i, f := range forms {
		if _, has := f["client_secret"]; has || f.Get("client_id") != "cid" {
			t.Fatalf("form %d = %v", i, f)
		}
	}
	if forms[0].Get("code_verifier") != "ver" {
		t.Fatalf("verifier missing: %v", forms[0])
	}
}
