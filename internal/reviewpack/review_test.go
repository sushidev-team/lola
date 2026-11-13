package reviewpack

import (
	"os"
	"reflect"
	"testing"
)

func TestReviewTracksExactPackageAcrossPublicationAndCleanup(t *testing.T) {
	home, work, m := fixture(t)
	p, err := Publish(home, "s~web", work, "acme/web", 42, m)
	if err != nil {
		t.Fatal(err)
	}
	r, err := MarkReviewed(home, "s~web", p.ID, "")
	if err != nil || r.Package != p.ID || r.At.IsZero() {
		t.Fatalf("review: %+v %v", r, err)
	}
	retry, err := MarkReviewed(home, "s~web", p.ID, "")
	if err != nil || !reflect.DeepEqual(r, retry) {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	m.Body = "Corrected material at the same head"
	next, err := Publish(home, "s~web", work, "acme/web", 42, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MarkReviewed(home, "s~web", next.ID, ""); err == nil {
		t.Fatal("stale write replaced review")
	}
	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	got, err := Reviewed(home, "s~web")
	if err != nil || !reflect.DeepEqual(r, got) {
		t.Fatalf("readback: %+v %v", got, err)
	}
	for _, scope := range []string{"s", "s~api", "other"} {
		if _, err := MarkReviewed(home, scope, p.ID, ""); err == nil {
			t.Fatalf("cross-scope review: %s", scope)
		}
	}
	got, err = MarkReviewed(home, "s~web", next.ID, p.ID)
	if err != nil || got.Package != next.ID {
		t.Fatalf("advance: %+v %v", got, err)
	}
	unchanged, err := Read(home, "s~web", p.ID)
	if err != nil || !reflect.DeepEqual(p, unchanged) {
		t.Fatal("review modified immutable package", err)
	}
}

func TestCorruptReviewFailsClosed(t *testing.T) {
	home, work, m := fixture(t)
	p, err := Publish(home, "s", work, "acme/app", 42, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewPath(home, "s"), []byte(`{"package":"../../escape"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Reviewed(home, "s"); err == nil {
		t.Fatal("read corrupt record")
	}
	if _, err := MarkReviewed(home, "s", p.ID, ""); err == nil {
		t.Fatal("overwrote corrupt record")
	}
}
