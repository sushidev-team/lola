package knowledge

import (
	"strings"
	"testing"
	"time"
)

func TestCodeMaintenancePreservesProofAndRequiresReacceptance(t *testing.T) {
	s, p := fixture(t)
	l, err := s.Capture(Scope{Project: "p"}, p, Source{Kind: "worker", ID: "s"}, false)
	if err != nil {
		t.Fatal(err)
	}
	code := []CodeReference{{CodeTarget: CodeTarget{Member: "p", Path: "src"}, Source: "/repo", Branch: "main", Commit: strings.Repeat("a", 40), Digest: strings.Repeat("b", 64)}}
	l, err = s.AcceptWithCode(l.ID, l.Version, "verified regression test", nil, time.Time{}, code)
	if err != nil {
		t.Fatal(err)
	}
	original := Reference{ID: l.ID, Version: l.Version}
	if err = s.RecordDelivery("session", []Reference{original}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReviewCode(l.ID, l.Version, "referenced code changed"); err != nil {
		t.Fatal(err)
	}
	changed := s.List()[0]
	if changed.Status != Candidate || changed.Version != l.Version+1 || changed.Verification != l.Verification || changed.ReviewReason == "" {
		t.Fatalf("lost proof: %+v", changed)
	}
	if _, err = s.Accept(changed.ID, changed.Version, "reviewed against new code", nil, time.Time{}); err == nil {
		t.Fatal("reactivated stale anchors")
	}
	if err = s.ReviewCode(l.ID, l.Version, "referenced code changed"); err == nil {
		t.Fatal("stale version accepted")
	}
	code[0].Digest = strings.Repeat("c", 64)
	accepted, err := s.AcceptWithCode(changed.ID, changed.Version, "reviewed against new code", nil, time.Time{}, code)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ReviewReason != "" || accepted.Status != Active {
		t.Fatal("fresh acceptance failed")
	}
	if err = s.ReviewCode(changed.ID, changed.Version, "lesson expired"); err == nil {
		t.Fatal("old maintenance demoted fresh proof")
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	historic, ok := s.Version(s.Delivery("session")[0])
	if !ok || historic.Code[0].Digest != strings.Repeat("b", 64) || historic.Status != Active {
		t.Fatal("restart lost supplied proof")
	}
}
