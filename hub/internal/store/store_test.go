package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

func sampleItem() hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
		Repo: "acme/web", Ecosystem: "npm", Package: "lodash", Vulnerable: "< 4.17.21",
		Fixed: "4.17.21", Advisory: "GHSA-jf85-cpcp-j695", Severity: hubproto.SeverityHigh,
	}}
}

func TestFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "hub.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	err = s.Update(func(d *Data) error {
		d.Orgs["acme"] = &Org{Name: "acme", CreatedAt: now, AdminKeys: map[string]string{"k1": "x"}}
		d.Devices["d1"] = &Device{ID: "d1", Org: "acme", Name: "laptop", CreatedAt: now, Repos: []string{"acme/web"}}
		d.Tokens["h"] = &EnrollToken{Hash: "h", Org: "acme", ExpiresAt: now.Add(time.Hour)}
		d.Items["i1"] = &WorkItem{ID: "i1", Org: "acme", Item: sampleItem(), State: hubproto.StateClaimed,
			Lease: &hubproto.Lease{ItemID: "i1", Device: "d1", Expires: now}, FirstSeen: now, LastSeen: now,
			Escalation: &Escalation{Pending: true, Reason: "critical"}}
		d.Log(Event{At: now, Kind: "test"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", st.Mode().Perm())
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = s2.View(func(d *Data) error {
		w := d.Items["i1"]
		if w == nil || w.Item.DepAdvisory == nil || w.Item.DepAdvisory.Advisory != "GHSA-jf85-cpcp-j695" {
			t.Fatalf("item lost: %+v", w)
		}
		if w.Lease == nil || !w.Lease.Expires.Equal(now) || w.Escalation == nil || !w.Escalation.Pending {
			t.Fatalf("lease/escalation lost: %+v", w)
		}
		if d.Devices["d1"].Repos[0] != "acme/web" || d.Orgs["acme"].AdminKeys["k1"] != "x" || len(d.Events) != 1 {
			t.Fatal("round trip mismatch")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateErrorDiscards(t *testing.T) {
	s, _ := Open("")
	boom := errors.New("boom")
	if err := s.Update(func(d *Data) error {
		d.Orgs["x"] = &Org{Name: "x"}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	_ = s.View(func(d *Data) error {
		if len(d.Orgs) != 0 {
			t.Fatal("failed update was committed")
		}
		return nil
	})
}

// A second process (the admin CLI) writing the file must be seen by the first.
func TestCrossProcessReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.json")
	server, _ := Open(path)
	admin, _ := Open(path)
	if err := admin.Update(func(d *Data) error { d.Orgs["acme"] = &Org{Name: "acme"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := server.Update(func(d *Data) error {
		if d.Orgs["acme"] == nil {
			t.Fatal("server did not reload the admin's write")
		}
		d.Orgs["beta"] = &Org{Name: "beta"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = admin.View(func(d *Data) error {
		if d.Orgs["beta"] == nil || d.Orgs["acme"] == nil {
			t.Fatal("admin lost a write")
		}
		return nil
	})
}

func TestEventLogCapped(t *testing.T) {
	d := NewData()
	for i := 0; i < MaxEvents+10; i++ {
		d.Log(Event{Kind: "e", Detail: string(rune('a' + i%26))})
	}
	if len(d.Events) != MaxEvents {
		t.Fatalf("len = %d", len(d.Events))
	}
}

func TestNoteReporterCountsDistinct(t *testing.T) {
	w := &WorkItem{}
	now := time.Now()
	w.NoteReporter("a", now)
	w.NoteReporter("a", now)
	w.NoteReporter("b", now)
	if w.Reporters != 2 {
		t.Fatalf("reporters = %d", w.Reporters)
	}
}

func TestTokenHelpers(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _ := hubproto.GenerateKey()
	if err := (hubproto.EnrollRequest{Token: tok, DeviceName: "x", PublicKey: hubproto.EncodePublicKey(pub)}).Validate(); err != nil {
		t.Fatalf("token does not satisfy the wire pattern: %v", err)
	}
	if len(tok) != 32 || HashToken(tok) == tok || len(HashToken(tok)) != 64 {
		t.Fatalf("token %q", tok)
	}
	id, _ := NewItemID()
	if !hubproto.ValidItemID(id) {
		t.Fatalf("bad item id %q", id)
	}
}
