package secret

import (
	"errors"
	"strings"
	"testing"
)

func box(t *testing.T) *Box {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(k)
	if err != nil || b == nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func TestSealOpen(t *testing.T) {
	b := box(t)
	const plain = "lin_api_abcdefghijklmnopqrstuvwxyz"
	s1, err := b.Seal("acme", FieldLinearKey, plain)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := b.Seal("acme", FieldLinearKey, plain)
	if s1 == s2 {
		t.Error("two seals of one value are identical (nonce reuse)")
	}
	if strings.Contains(s1, plain) {
		t.Error("the sealed value contains the plaintext")
	}
	got, err := b.Open("acme", FieldLinearKey, s1)
	if err != nil || got != plain {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

// A sealed value is bound to its org and its field.
func TestOpenRefusesMovedValue(t *testing.T) {
	b := box(t)
	s, _ := b.Seal("acme", FieldLinearKey, "x")
	if _, err := b.Open("other", FieldLinearKey, s); !errors.Is(err, ErrCorrupt) {
		t.Errorf("other org: %v", err)
	}
	if _, err := b.Open("acme", FieldDigestWebhook, s); !errors.Is(err, ErrCorrupt) {
		t.Errorf("other field: %v", err)
	}
	if _, err := b.Open("acme", FieldLinearKey, s[:len(s)-2]+"AA"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("tampered: %v", err)
	}
	if _, err := b.Open("acme", FieldLinearKey, "junk"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("junk: %v", err)
	}
}

func TestOtherKey(t *testing.T) {
	s, _ := box(t).Seal("acme", FieldLinearKey, "x")
	if _, err := box(t).Open("acme", FieldLinearKey, s); !errors.Is(err, ErrOtherKey) {
		t.Fatalf("err = %v, want ErrOtherKey", err)
	}
}

func TestNoKey(t *testing.T) {
	b, err := New("  ")
	if err != nil || b != nil {
		t.Fatalf("New(\"\") = %v, %v", b, err)
	}
	if _, err := b.Seal("acme", FieldLinearKey, "x"); !errors.Is(err, ErrNoKey) {
		t.Errorf("Seal: %v", err)
	}
	if _, err := b.Open("acme", FieldLinearKey, "x"); !errors.Is(err, ErrNoKey) {
		t.Errorf("Open: %v", err)
	}
}

func TestBadKey(t *testing.T) {
	for _, k := range []string{"correct horse battery staple", "c2hvcnQ=", "!!!"} {
		if _, err := New(k); !errors.Is(err, ErrBadKey) {
			t.Errorf("New(%q) = %v, want ErrBadKey", k, err)
		}
	}
}
