package console

import (
	"github.com/sushidev-team/lola/hub/internal/secret"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

const itemID = "i0123456789abcdef01234567"

func seedEscalated(t *testing.T, e *env) {
	t.Helper()
	pub, _, _ := hubproto.GenerateKey()
	dev := hubproto.DeviceID(pub)
	_ = e.st.Update(func(d *store.Data) error {
		d.Devices[dev] = &store.Device{ID: dev, Org: "acme", Name: "ana-laptop", Member: "Ana Lopez", PolicyVersion: 3, LastSeen: e.now}
		it, _ := admin.BuildItem(admin.ItemFields{Kind: "dep_advisory", Repo: "acme/web", Ecosystem: "npm", Package: "lodash",
			Vulnerable: "< 4.17.21", Fixed: "4.17.21", Advisory: "GHSA-jf85-cpcp-j695", Severity: "high"})
		w := lease.NewItem(itemID, "acme", it, dev, e.now)
		w.State, w.Attempts = hubproto.StateEscalated, 2
		w.Escalation = &store.Escalation{Pending: true, Reason: lease.ReasonAttempts, CreatedAt: e.now}
		w.History = []store.Attempt{
			{Device: dev, DeviceName: "ana-laptop", Member: "Ana Lopez", Action: hubproto.AttemptInReview, PRURL: "https://github.com/acme/web/pull/5", At: e.now.Add(-time.Hour)},
			{Device: dev, DeviceName: "<b>evil</b>", Action: hubproto.AttemptFailed, Reason: hubproto.FailCI, PRURL: "javascript:alert(1)", At: e.now},
		}
		d.Items[itemID] = w
		d.StatsDay("acme", e.now).Done = 4
		d.StatsDay("acme", e.now).FixSecondsSum = 4 * 7200
		return nil
	})
}

func TestEnrollLabelListAndRevoke(t *testing.T) {
	e := newEnv(t)
	e.login()
	tok := e.csrf()
	e.post("/orgs/acme/enroll", url.Values{"csrf": {tok}, "ttl": {"24h"}, "label": {"Ana Lopez"}})
	body := e.get("/orgs/acme").body
	if !strings.Contains(body, "For <strong>Ana Lopez</strong>") {
		t.Fatal("label not shown with the token")
	}
	m := regexp.MustCompile(`/orgs/acme/enroll/([0-9a-f]{12})/revoke`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("token not listed with a revoke action")
	}
	if r := e.post("/orgs/acme/enroll/"+m[1]+"/revoke", url.Values{"csrf": {tok}}); r.status != http.StatusSeeOther {
		t.Fatalf("revoke %d", r.status)
	}
	body = e.get("/orgs/acme").body
	if !strings.Contains(body, "revoked") || strings.Contains(body, "/enroll/"+m[1]+"/revoke") {
		t.Fatal("token not revoked")
	}
	// A bad label is refused, nothing minted.
	e.post("/orgs/acme/enroll", url.Values{"csrf": {tok}, "label": {"<script>"}})
	_ = e.st.View(func(d *store.Data) error {
		if len(d.Tokens) != 1 {
			t.Fatalf("%d tokens", len(d.Tokens))
		}
		return nil
	})
}

func TestItemPageHistoryAndEscaping(t *testing.T) {
	e := newEnv(t)
	e.login()
	seedEscalated(t, e)
	body := e.get("/orgs/acme/items/" + itemID).body
	for _, want := range []string{"Attempt history", "Ana Lopez", `href="https://github.com/acme/web/pull/5"`, "in_review", "ci_failed", "&lt;b&gt;evil"} {
		if !strings.Contains(body, want) {
			t.Fatalf("item page lacks %q", want)
		}
	}
	if strings.Contains(body, "javascript:alert") || strings.Contains(body, "<b>evil") {
		t.Fatal("unsafe history rendered")
	}
	if r := e.get("/orgs/other/items/" + itemID); r.status != http.StatusNotFound {
		t.Fatalf("item of another org: %d", r.status)
	}
}

func TestResolveAndReopenFromConsole(t *testing.T) {
	e := newEnv(t)
	e.login()
	seedEscalated(t, e)
	tok := e.csrf()
	r := e.post("/orgs/acme/items/"+itemID+"/reopen", url.Values{"csrf": {tok}, "return_item": {itemID}})
	if r.status != http.StatusSeeOther || r.loc != "/orgs/acme/items/"+itemID {
		t.Fatalf("reopen %d %q", r.status, r.loc)
	}
	// Reopened: resolve now refuses with a flash, nothing changes.
	e.post("/orgs/acme/items/"+itemID+"/resolve", url.Values{"csrf": {tok}})
	if !strings.Contains(e.get("/orgs/acme").body, "flash error") {
		t.Fatal("no error for resolving an open item")
	}
	_ = e.st.Update(func(d *store.Data) error { d.Items[itemID].State = hubproto.StateEscalated; return nil })
	e.post("/orgs/acme/items/"+itemID+"/resolve", url.Values{"csrf": {tok}})
	_ = e.st.View(func(d *store.Data) error {
		if d.Items[itemID].State != hubproto.StateDone {
			t.Fatalf("state %s", d.Items[itemID].State)
		}
		return nil
	})
	// Without the CSRF token: refused.
	if r := e.post("/orgs/acme/items/"+itemID+"/reopen", url.Values{}); r.status != http.StatusForbidden {
		t.Fatalf("no csrf: %d", r.status)
	}
}

func TestOrgPageShowsMembersStatsAndLinearState(t *testing.T) {
	e := newEnv(t)
	e.login()
	seedEscalated(t, e)
	body := e.get("/orgs/acme").body
	for _, want := range []string{"Ana Lopez", "v3", "No secret key", "1 waiting", "Avg time to fix", "2h", "Resolve", "Reopen", "/orgs/acme/events"} {
		if !strings.Contains(body, want) {
			t.Fatalf("org page lacks %q", want)
		}
	}
	if !strings.Contains(e.get("/").body, "No secret key") {
		t.Fatal("overview hides the missing secret key")
	}
	k, _ := secret.NewKey()
	e.con.cfg.Box, _ = secret.New(k)
	if body := e.get("/orgs/acme").body; !strings.Contains(body, "No Linear key") {
		t.Fatal("org without a key not flagged")
	}
	if err := admin.SetLinearKey(e.st, e.con.cfg.Box, "acme", "lin_api_"+strings.Repeat("k", 40), e.now); err != nil {
		t.Fatal(err)
	}
	if body := e.get("/orgs/acme").body; !strings.Contains(body, "No Linear team") {
		t.Fatal("org without a team not flagged")
	}
	if r := e.get("/orgs/acme/events"); r.status != http.StatusOK || !strings.Contains(r.body, "event log") {
		t.Fatalf("events page %d", r.status)
	}
	if r := e.get("/orgs/nope/events"); r.status != http.StatusNotFound {
		t.Fatalf("events of a missing org %d", r.status)
	}
}

func TestPublishWithRegisterKey(t *testing.T) {
	e := newEnv(t)
	e.login()
	tok := e.csrf()
	apub, apriv, _ := hubproto.GenerateKey()
	kid := hubproto.KeyIDFor(apub)
	p, _ := hubproto.SignPolicy(apriv, kid, hubproto.Policy{Org: "acme", Version: 1, IssuedAt: e.now, Settings: hubproto.Settings{EscalateAfter: 3}})
	raw, _ := jsonMarshal(p)
	e.post("/orgs/acme/policy/publish", url.Values{"csrf": {tok}, "policy": {string(raw)}, "register_key": {hubproto.FormatTrustedKey(kid, apub)}})
	_ = e.st.View(func(d *store.Data) error {
		o := d.Orgs["acme"]
		if o.Policy == nil || o.AdminKeys[kid] == "" {
			t.Fatal("publish with key did not apply")
		}
		return nil
	})
}
