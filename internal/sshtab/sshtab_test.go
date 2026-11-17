package sshtab

import "testing"

func TestNameAndIndexRoundTrip(t *testing.T) {
	if got, want := Name("lola-local-abc", 2), "lola-local-abc-ssh-2"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got := Index("lola-local-abc", Name("lola-local-abc", 12)); got != 12 {
		t.Fatalf("Index = %d", got)
	}
}

func TestIsMatchesOnlyANumberedTab(t *testing.T) {
	for _, n := range []string{"lola-x-ssh-1", "lola-x-ssh-40"} {
		if !Is(n) {
			t.Errorf("Is(%q) = false", n)
		}
	}
	// A session whose branch ends in "-ssh" is a session of its own.
	for _, n := range []string{"lola-x", "lola-open-ssh", "lola-x-ssh-", "lola-x-ssh-0", "lola-x-ssh-x", "lola-x-ssh-1a", "lola-x-shell-1"} {
		if Is(n) {
			t.Errorf("Is(%q) = true", n)
		}
	}
}

func TestIndexBindsATabToItsExactParent(t *testing.T) {
	if got := Index("lola-fe-4", "lola-fe-42-ssh-1"); got != 0 {
		t.Errorf("across sessions = %d", got)
	}
	for _, n := range []string{"lola-fe-42", "lola-fe-42-shell-1", "lola-fe-42-ssh-", "lola-fe-42-ssh-0", "lola-fe-42-ssh-01", "lola-fe-42-ssh-1x", "lola-fe-42-ssh-1-ssh-2"} {
		if got := Index("lola-fe-42", n); got != 0 {
			t.Errorf("Index(%q) = %d", n, got)
		}
	}
}

func TestParentNamesTheSessionTheShapeImplies(t *testing.T) {
	if p, ok := Parent("lola-x-1-ssh-2"); !ok || p != "lola-x-1" {
		t.Fatalf("Parent = %q %v", p, ok)
	}
	for _, n := range []string{"lola-x-1", "lola-x-ssh-", "lola-x-ssh-0", "-ssh-1"} {
		if p, ok := Parent(n); ok {
			t.Errorf("Parent(%q) = %q", n, p)
		}
	}
}
