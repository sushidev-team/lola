package store

import (
	"testing"
	"time"
)

func TestEnrollTokenStatusAndRevoke(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tok := &EnrollToken{Hash: HashToken("secret"), Org: "acme", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if got := tok.Status(now); got != TokenUnused {
		t.Fatalf("fresh: %s", got)
	}
	if got := tok.Status(now.Add(time.Hour)); got != TokenExpired {
		t.Fatalf("at expiry: %s", got)
	}
	if len(tok.ID()) != TokenIDLen || tok.ID() != tok.Hash[:TokenIDLen] {
		t.Fatalf("id %q", tok.ID())
	}
	if !tok.Revoke(now) || tok.Revoke(now) {
		t.Fatal("revoke must change an unused token once")
	}
	if got := tok.Status(now); got != TokenRevoked {
		t.Fatalf("revoked: %s", got)
	}
	// The expiry moved to the revoke moment, so the enroll check
	// (now.Before(ExpiresAt)) refuses it with no extra rule.
	if now.Before(tok.ExpiresAt) {
		t.Fatal("revoke left the token live")
	}
	used := &EnrollToken{Hash: "x", ExpiresAt: now.Add(time.Hour), UsedAt: &now}
	if used.Revoke(now) || used.Status(now) != TokenUsed {
		t.Fatal("a used token cannot be revoked")
	}
}

func TestTokenByID(t *testing.T) {
	d := NewData()
	a := &EnrollToken{Hash: "abcdef0123456789aa"}
	b := &EnrollToken{Hash: "abcdef0123456789bb"}
	d.Tokens[a.Hash], d.Tokens[b.Hash] = a, b
	if d.TokenByID("abcdef0123456789") != nil {
		t.Fatal("an ambiguous prefix must match nothing")
	}
	if d.TokenByID("abcdef0123456789aa") != a {
		t.Fatal("a full hash must match")
	}
	if d.TokenByID("abc") != nil {
		t.Fatal("a short id must match nothing")
	}
	c := &EnrollToken{Hash: "0123456789abcdef"}
	d.Tokens[c.Hash] = c
	if d.TokenByID(c.ID()) != c {
		t.Fatal("public id must match")
	}
}
