package admin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

func TestEnrollTokenLabelListAndRevoke(t *testing.T) {
	st := newStore(t)
	if _, _, err := CreateEnrollToken(st, "acme", "<script>", time.Hour, now); err == nil {
		t.Fatal("bad label accepted")
	}
	tok, _, err := CreateEnrollToken(st, "acme", "Ana Lopez", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	list, err := ListEnrollTokens(st, "acme", now)
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	ti := list[0]
	if ti.Label != "Ana Lopez" || ti.Status != store.TokenUnused || ti.ID != store.HashToken(tok)[:store.TokenIDLen] {
		t.Fatalf("%+v", ti)
	}
	if strings.Contains(ti.ID, tok) || strings.Contains(tok, ti.ID) {
		t.Fatal("the list carries the secret")
	}
	if err := RevokeEnrollToken(st, "other", ti.ID, now); err == nil {
		t.Fatal("revoked across orgs")
	}
	if err := RevokeEnrollToken(st, "acme", ti.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := RevokeEnrollToken(st, "acme", ti.ID, now); err == nil {
		t.Fatal("revoked twice")
	}
	list, _ = ListEnrollTokens(st, "", now)
	if list[0].Status != store.TokenRevoked {
		t.Fatalf("status %s", list[0].Status)
	}
	// The API's enroll check (unused and now.Before(ExpiresAt)) refuses it.
	_ = st.View(func(d *store.Data) error {
		tk := d.Tokens[store.HashToken(tok)]
		if now.Before(tk.ExpiresAt) {
			t.Fatal("revoked token still enrolls")
		}
		return nil
	})
	if err := RevokeEnrollToken(st, "acme", "xyz", now); err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestUsedTokenShowsDeviceAndCannotBeRevoked(t *testing.T) {
	st := newStore(t)
	tok, _, _ := CreateEnrollToken(st, "acme", "", time.Hour, now)
	_ = st.Update(func(d *store.Data) error {
		at := now
		d.Devices["d1"] = &store.Device{ID: "d1", Org: "acme", Name: "laptop"}
		tk := d.Tokens[store.HashToken(tok)]
		tk.UsedAt, tk.UsedBy = &at, "d1"
		return nil
	})
	list, _ := ListEnrollTokens(st, "acme", now)
	if list[0].Status != store.TokenUsed || list[0].UsedBy != "laptop" {
		t.Fatalf("%+v", list[0])
	}
	if err := RevokeEnrollToken(st, "acme", list[0].ID, now); err == nil {
		t.Fatal("used token revoked")
	}
}

func TestCreateEnrollTokensIsAllOrNothing(t *testing.T) {
	st := newStore(t)
	toks, _, err := CreateEnrollTokens(st, "acme", "Team", time.Hour, 3, now)
	if err != nil || len(toks) != 3 || toks[0] == toks[1] {
		t.Fatalf("%v %v", toks, err)
	}
	if _, _, err := CreateEnrollTokens(st, "acme", "", time.Hour, MaxTokensPerCall+1, now); err == nil {
		t.Fatal("count cap ignored")
	}
}

func TestInviteFormatAndChecks(t *testing.T) {
	pub, _, _ := hubproto.GenerateKey()
	trusted := hubproto.FormatTrustedKey("k1", pub)
	got, err := CheckTrusted(trusted)
	if err != nil || got != trusted {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := CheckTrusted("k1:nope"); err == nil {
		t.Fatal("bad key accepted")
	}
	u, err := CheckHubURL("https://hub.example.com/")
	if err != nil || u != "https://hub.example.com" {
		t.Fatalf("%q %v", u, err)
	}
	for _, bad := range []string{"http://hub.example.com", "https://hub.example.com/#x", "https://u:p@hub", "ftp://hub", "hub.example.com", "https://hub?x=1"} {
		if _, err := CheckHubURL(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := CheckHubURL("http://127.0.0.1:8099"); err != nil {
		t.Fatal("loopback http refused")
	}
	tok := strings.Repeat("T", 32)
	if s := FormatInvite(u, tok, trusted); s != "lola-hub:https://hub.example.com#"+tok+"#"+trusted {
		t.Fatal(s)
	}
	if s := FormatInvite(u, tok, ""); s != "lola-hub:https://hub.example.com#"+tok {
		t.Fatal(s)
	}
}

func TestInitOrg(t *testing.T) {
	st, _ := store.Open("")
	if err := InitOrg(st, "acme", "team-1", now); err != nil {
		t.Fatal(err)
	}
	if err := InitOrg(st, "acme", "", now); !errors.Is(err, ErrExists) {
		t.Fatalf("second init: %v", err)
	}
	if err := InitOrg(st, "beta", "bad team!", now); err == nil {
		t.Fatal("bad team accepted")
	}
	_ = st.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearTeam != "team-1" || d.Orgs["beta"] != nil {
			t.Fatal("init state")
		}
		return nil
	})
}

func signedPolicy(t *testing.T, org string, v int) ([]byte, string) {
	t.Helper()
	pub, priv, _ := hubproto.GenerateKey()
	kid := hubproto.KeyIDFor(pub)
	p, err := hubproto.SignPolicy(priv, kid, hubproto.Policy{Org: org, Version: v, IssuedAt: now, Settings: hubproto.Settings{EscalateAfter: 3}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	return raw, hubproto.FormatTrustedKey(kid, pub)
}

func TestPublishPolicyWithKeyIsOneTransaction(t *testing.T) {
	st := newStore(t)
	raw, trusted := signedPolicy(t, "acme", 1)
	_, other := signedPolicy(t, "acme", 1)
	if _, err := PublishPolicyWithKey(st, "", raw, other, now); err == nil {
		t.Fatal("registered a key that did not sign the bundle")
	}
	_ = st.View(func(d *store.Data) error {
		if len(d.Orgs["acme"].AdminKeys) != 0 {
			t.Fatal("a refused publish left a key behind")
		}
		return nil
	})
	if _, err := PublishPolicy(st, "", raw, now); err == nil {
		t.Fatal("published without any key")
	}
	p, err := PublishPolicyWithKey(st, "", raw, trusted, now)
	if err != nil || p.Version != 1 {
		t.Fatalf("%v %v", p, err)
	}
	// A tampered bundle still fails, even with its key registered in the
	// same call.
	tampered := []byte(strings.Replace(string(raw), `"escalate_after":3`, `"escalate_after":9`, 1))
	if _, err := PublishPolicyWithKey(st, "", tampered, trusted, now); err == nil {
		t.Fatal("tampered bundle published")
	}
}

func TestResolveAndReopenEscalated(t *testing.T) {
	st := newStore(t)
	const id = "i0123456789abcdef01234567"
	_ = st.Update(func(d *store.Data) error {
		it, _ := BuildItem(advFields())
		w := lease.NewItem(id, "acme", it, "dev", now)
		w.State, w.Attempts = hubproto.StateEscalated, 2
		w.Escalation = &store.Escalation{Reason: lease.ReasonAttempts, LinearID: "ENG-1"}
		d.Items[id] = w
		return nil
	})
	if err := ReopenItem(st, "other", id, now); err == nil {
		t.Fatal("reopened across orgs")
	}
	if err := ReopenItem(st, "acme", id, now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveItem(st, "acme", id, now); !errors.Is(err, ErrNotEscalated) {
		t.Fatalf("resolve of an open item: %v", err)
	}
	_ = st.Update(func(d *store.Data) error { d.Items[id].State = hubproto.StateEscalated; return nil })
	if err := ResolveItem(st, "acme", id, now); err != nil {
		t.Fatal(err)
	}
	_ = st.View(func(d *store.Data) error {
		w := d.Items[id]
		if w.State != hubproto.StateDone || w.Escalation == nil || w.Escalation.LinearID != "ENG-1" {
			t.Fatalf("%+v", w)
		}
		days := d.Stats["acme"]
		if len(days) == 0 || days[len(days)-1].Done != 1 {
			t.Fatalf("resolve not counted: %+v", days)
		}
		return nil
	})
}
