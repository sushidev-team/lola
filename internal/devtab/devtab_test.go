package devtab

import "testing"

func TestNameIsTheOneBasedTabOfItsSession(t *testing.T) {
	if got, want := Name("lola-app-eng-42", 1), "lola-app-eng-42-dev-1"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
	if got, want := Name("lola-app-eng-42", 3), "lola-app-eng-42-dev-3"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
}

func TestIsMatchesOnlyANumberedDevTab(t *testing.T) {
	for _, name := range []string{"lola-app-eng-42-dev-1", "lola-x-dev-12"} {
		if !Is(name) {
			t.Errorf("Is(%q) = false, want true", name)
		}
	}
	// A session whose BRANCH ends in "-dev" is a session of its own; treating it
	// as an auxiliary tab would drop it from adoption and sweep it on a teardown
	// it has nothing to do with.
	for _, name := range []string{"lola-app-eng-42", "lola-app-open-dev", "lola-app-dev", "lola-app-dev-", "lola-app-dev-x"} {
		if Is(name) {
			t.Errorf("Is(%q) = true, want false", name)
		}
	}
}

func TestIndexBindsATabToItsExactParent(t *testing.T) {
	if got := Index("lola-fe-42", "lola-fe-42-dev-2"); got != 2 {
		t.Errorf("Index = %d, want 2", got)
	}
	// The prefix trap: "lola-fe-4" IS a prefix of "lola-fe-42-dev-1", and a loose
	// match would make one session's toggle kill a sibling's dev server.
	if got := Index("lola-fe-4", "lola-fe-42-dev-1"); got != 0 {
		t.Errorf("Index across sessions = %d, want 0", got)
	}
	for _, name := range []string{"lola-fe-42", "lola-fe-42-shell-1", "lola-fe-42-review", "lola-fe-42-dev-", "lola-fe-42-dev-0", "lola-fe-42-dev-x"} {
		if got := Index("lola-fe-42", name); got != 0 {
			t.Errorf("Index(%q) = %d, want 0", name, got)
		}
	}
}
