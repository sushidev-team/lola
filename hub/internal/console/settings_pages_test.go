package console

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// The console writes an org's secrets and never shows one again.
func TestOrgSecretsAreWriteOnly(t *testing.T) {
	e := newEnv(t)
	k, _ := secret.NewKey()
	e.con.cfg.Box, _ = secret.New(k)
	e.login()
	csrf := e.csrf()
	key := "lin_api_" + strings.Repeat("Z", 40)
	const hook = "https://hooks.slack.com/services/T0001/B0001/HOOKSECRET"

	if r := e.post("/orgs/acme/linear-key", url.Values{"csrf": {csrf}, "key": {"hunter2"}}); r.status != http.StatusSeeOther {
		t.Fatalf("bad key: %d", r.status)
	}
	if body := e.get("/orgs/acme").body; !strings.Contains(body, "not a Linear API key") || strings.Contains(body, "hunter2") {
		t.Fatal("a malformed key was not refused, or was echoed")
	}
	if r := e.post("/orgs/acme/digest", url.Values{"csrf": {csrf}, "webhook": {"https://169.254.169.254/x"}}); r.status != http.StatusSeeOther {
		t.Fatalf("bad webhook: %d", r.status)
	}
	if body := e.get("/orgs/acme").body; !strings.Contains(body, "Slack incoming webhook") {
		t.Fatal("a non-Slack webhook was not refused")
	}
	e.post("/orgs/acme/linear-key", url.Values{"csrf": {csrf}, "key": {key}})
	e.post("/orgs/acme/digest", url.Values{"csrf": {csrf}, "webhook": {hook}})
	for _, page := range []string{"/", "/orgs/acme", "/orgs/acme/events"} {
		body := e.get(page).body
		if strings.Contains(body, key) || strings.Contains(body, "HOOKSECRET") {
			t.Fatalf("%s shows a secret", page)
		}
	}
	_ = e.st.View(func(d *store.Data) error {
		o := d.Orgs["acme"]
		if got, err := e.con.cfg.Box.Open("acme", secret.FieldLinearKey, o.LinearKey); err != nil || got != key {
			t.Fatalf("stored key: %q %v", got, err)
		}
		if got, err := e.con.cfg.Box.Open("acme", secret.FieldDigestWebhook, o.DigestWebhook); err != nil || got != hook {
			t.Fatalf("stored webhook: %q %v", got, err)
		}
		return nil
	})
	e.post("/orgs/acme/linear-key", url.Values{"csrf": {csrf}, "clear": {"1"}})
	_ = e.st.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearKey != "" {
			t.Fatal("clear left the key")
		}
		return nil
	})
}

// Without a secret key the console refuses to store a secret.
func TestOrgSecretsNeedSecretKey(t *testing.T) {
	e := newEnv(t)
	e.login()
	e.post("/orgs/acme/linear-key", url.Values{"csrf": {e.csrf()}, "key": {"lin_api_" + strings.Repeat("Z", 40)}})
	if body := e.get("/orgs/acme").body; !strings.Contains(body, secret.EnvKey) {
		t.Fatal("no hint at the missing secret key")
	}
	_ = e.st.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearKey != "" {
			t.Fatal("a key was stored without a secret key")
		}
		return nil
	})
}

var codeRe = regexp.MustCompile(`id="create-code"[^>]*value="([A-Za-z0-9_-]+)"`)

func TestCreateCodeShownOnce(t *testing.T) {
	e := newEnv(t)
	e.login()
	if r := e.post("/create-codes", url.Values{"csrf": {e.csrf()}}); r.status != http.StatusSeeOther || r.loc != "/" {
		t.Fatalf("mint: %d %s", r.status, r.loc)
	}
	m := codeRe.FindStringSubmatch(e.get("/").body)
	if m == nil {
		t.Fatal("the create code is not shown")
	}
	if codeRe.MatchString(e.get("/").body) {
		t.Fatal("the create code is shown twice")
	}
	_ = e.st.View(func(d *store.Data) error {
		if !d.CreateCodes[store.HashToken(m[1])].Usable(e.now) {
			t.Fatal("the shown code is not the stored one")
		}
		return nil
	})
	if r := e.do(http.MethodPost, "/create-codes", url.Values{"csrf": {"nope"}}, e.origin()); r.status != http.StatusForbidden {
		t.Fatalf("minting without the csrf token: %d", r.status)
	}
}

func TestRemoveAdminKey(t *testing.T) {
	e := newEnv(t)
	e.login()
	pub, _, _ := hubproto.GenerateKey()
	if _, err := admin.AddPolicyKey(e.st, "acme", "k1", hubproto.EncodePublicKey(pub), e.now); err != nil {
		t.Fatal(err)
	}
	if r := e.post("/orgs/acme/policy/keys/k1/remove", url.Values{"csrf": {e.csrf()}}); r.status != http.StatusSeeOther {
		t.Fatalf("remove: %d", r.status)
	}
	_ = e.st.View(func(d *store.Data) error {
		if len(d.Orgs["acme"].AdminKeys) != 0 {
			t.Fatal("the key is still registered")
		}
		return nil
	})
}
