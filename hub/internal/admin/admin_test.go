package admin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

var now = time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *store.File {
	t.Helper()
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateOrg(st, "acme", now); err != nil {
		t.Fatal(err)
	}
	return st
}

func advFields() ItemFields {
	return ItemFields{Kind: "dep_advisory", Repo: "acme/web", Ecosystem: "npm", Package: "lodash",
		Vulnerable: "< 4.17.21", Fixed: "4.17.21", Advisory: "GHSA-jf85-cpcp-j695", Severity: "high"}
}

func TestCreateOrgAndToken(t *testing.T) {
	st := newStore(t)
	if err := CreateOrg(st, "acme", now); !errors.Is(err, ErrExists) {
		t.Fatalf("dup org: %v", err)
	}
	if err := CreateOrg(st, "Bad Org", now); err == nil {
		t.Fatal("bad org name accepted")
	}
	tok, exp, err := CreateEnrollToken(st, "acme", "", time.Hour, now)
	if err != nil || !exp.Equal(now.Add(time.Hour)) || len(tok) != 32 {
		t.Fatalf("token %q %v %v", tok, exp, err)
	}
	if _, _, err := CreateEnrollToken(st, "nope", "", time.Hour, now); err == nil {
		t.Fatal("token for missing org")
	}
	if _, _, err := CreateEnrollToken(st, "acme", "", time.Second, now); err == nil {
		t.Fatal("ttl too short accepted")
	}
	_ = st.View(func(d *store.Data) error {
		if d.Tokens[store.HashToken(tok)] == nil {
			t.Fatal("token not stored hashed")
		}
		return nil
	})
}

func TestAddItemValidatesAndDedups(t *testing.T) {
	st := newStore(t)
	it, err := BuildItem(advFields())
	if err != nil {
		t.Fatal(err)
	}
	id, created, err := AddItem(st, "acme", it, now)
	if err != nil || !created {
		t.Fatalf("add: %v %v", created, err)
	}
	id2, created, err := AddItem(st, "acme", it, now.Add(time.Minute))
	if err != nil || created || id2 != id {
		t.Fatalf("re-add: %s %v %v", id2, created, err)
	}
	_ = st.View(func(d *store.Data) error {
		w := d.Items[id]
		if w.State != hubproto.StateOpen || w.Reporters != 1 || w.ReporterIDs[0] != Reporter || w.Item.ID != id {
			t.Fatalf("stored %+v", w)
		}
		return nil
	})
	if _, _, err := AddItem(st, "nope", it, now); err == nil {
		t.Fatal("add to missing org")
	}
	bad := advFields()
	bad.Repo = "not a repo"
	if _, err := BuildItem(bad); err == nil {
		t.Fatal("invalid repo accepted")
	}
	bad = advFields()
	bad.Vulnerable = "< 1.0 ; rm -rf /"
	if _, err := BuildItem(bad); err == nil {
		t.Fatal("word in range accepted")
	}
	if _, err := BuildItem(ItemFields{Kind: "shell"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	bump, err := BuildItem(ItemFields{Kind: "red_bump_pr", Repo: "acme/web", PR: "12", Branch: "dependabot/npm/x-1", Package: "x", Author: hubproto.AuthorDependabot})
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := AddItem(st, "acme", bump, now); err != nil || !created {
		t.Fatalf("bump: %v", err)
	}
	if _, err := BuildItem(ItemFields{Kind: "red_bump_pr", Repo: "acme/web", PR: "x"}); err == nil {
		t.Fatal("non-numeric pr accepted")
	}
}

func TestCriticalAddedItemEscalates(t *testing.T) {
	st := newStore(t)
	f := advFields()
	f.Severity = "critical"
	it, _ := BuildItem(f)
	id, _, err := AddItem(st, "acme", it, now)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.View(func(d *store.Data) error {
		if x := d.Items[id].Escalation; x == nil || !x.Pending {
			t.Fatal("critical not escalated")
		}
		return nil
	})
}

func TestRevokeScopedToOrg(t *testing.T) {
	st := newStore(t)
	_ = CreateOrg(st, "beta", now)
	pub, _, _ := hubproto.GenerateKey()
	id := hubproto.DeviceID(pub)
	_ = st.Update(func(d *store.Data) error {
		d.Devices[id] = &store.Device{ID: id, Org: "beta", Name: "x", PublicKey: hubproto.EncodePublicKey(pub)}
		return nil
	})
	if _, err := RevokeDevice(st, "acme", id, now); err == nil {
		t.Fatal("revoked another org's device")
	}
	if _, err := RevokeDevice(st, "beta", id, now); err != nil {
		t.Fatal(err)
	}
	if _, err := RevokeDevice(st, "beta", id, now); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("double revoke: %v", err)
	}
}

func TestPurgeScopedToOrg(t *testing.T) {
	st := newStore(t)
	_ = CreateOrg(st, "beta", now)
	it, _ := BuildItem(advFields())
	id, _, _ := AddItem(st, "acme", it, now)
	if err := PurgeItem(st, "beta", id, "", now); err == nil {
		t.Fatal("purged another org's item")
	}
	if err := PurgeItem(st, "acme", id, hubproto.StateDone, now); err == nil {
		t.Fatal("purged in the wrong state")
	}
	if err := PurgeItem(st, "acme", id, "", now); err != nil {
		t.Fatal(err)
	}
	if n, err := PurgeState(st, "acme", hubproto.StateOpen, now); err != nil || n != 0 {
		t.Fatalf("purge state %d %v", n, err)
	}
}

func TestPolicyKeyAndPublish(t *testing.T) {
	st := newStore(t)
	pub, priv, _ := hubproto.GenerateKey()
	kid := hubproto.KeyIDFor(pub)
	unsigned := hubproto.Policy{Org: "acme", Version: 1, IssuedAt: now, Settings: hubproto.Settings{EscalateAfter: 3}}
	p, _ := hubproto.SignPolicy(priv, kid, unsigned)
	raw, _ := json.Marshal(p)
	if _, err := PublishPolicy(st, "acme", raw, now); err == nil {
		t.Fatal("published with no registered key")
	}
	trusted, err := AddPolicyKey(st, "acme", kid, hubproto.EncodePublicKey(pub), now)
	if err != nil || !strings.HasPrefix(trusted, kid+":") {
		t.Fatalf("add key %q %v", trusted, err)
	}
	if _, err := PublishPolicy(st, "beta", raw, now); err == nil {
		t.Fatal("published under the wrong org")
	}
	if _, err := PublishPolicy(st, "acme", raw, now); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishPolicy(st, "acme", raw, now); err == nil {
		t.Fatal("republished the same version")
	}
	if _, err := PublishPolicy(st, "acme", []byte(`{"org":"acme","settings":{"post_create":"x"}}`), now); err == nil {
		t.Fatal("executing key accepted")
	}
}
