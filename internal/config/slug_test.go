package config

import "testing"

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Nori App", "nori-app"},
		{"nori-app", "nori-app"},
		{"  Nori   App  ", "nori-app"},
		{"Okane", "okane"},
		{"my_project.v2", "my_project.v2"},
		{"Ünïcødé Ãpp", "n-c-d-pp"}, // non-ASCII collapses; never leaks into a path
		{"日本語", ""},                // ...and an all-non-ASCII label yields no id at all
		{"a/b", "a-b"},              // a separator can never survive: it would escape the segment
		{"../etc", "etc"},
		{"...", ""},
		{"", ""},
		{"   ", ""},
		{"---", ""},
	}
	for _, tc := range tests {
		if got := Slug(tc.in); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every non-empty Slug output must be a valid, stable id: slugging it again
// changes nothing, and IsSlug accepts it.
func TestSlugIsIdempotentAndSelfConsistent(t *testing.T) {
	for _, in := range []string{"Nori App", "a/b", "../etc", "my_project.v2", "Okane"} {
		s := Slug(in)
		if s == "" {
			continue
		}
		if again := Slug(s); again != s {
			t.Errorf("Slug(Slug(%q)) = %q, want %q", in, again, s)
		}
		if !IsSlug(s) {
			t.Errorf("IsSlug(Slug(%q)) = false for %q", in, s)
		}
	}
}

func TestIsSlugRejectsNonCanonical(t *testing.T) {
	for _, in := range []string{"", "Okane", "Nori App", "a/b", "-lead", "trail-", "..", "."} {
		if IsSlug(in) {
			t.Errorf("IsSlug(%q) = true, want false", in)
		}
	}
}

// SlugTyping must NOT trim the edges: trimming mid-typing makes a hyphenated id
// impossible to enter, because the separator vanishes the moment it is typed.
func TestSlugTypingKeepsTrailingSeparator(t *testing.T) {
	if got := SlugTyping("nori-"); got != "nori-" {
		t.Errorf("SlugTyping(%q) = %q, want the trailing hyphen kept", "nori-", got)
	}
	if got := SlugTyping("Nori A"); got != "nori-a" {
		t.Errorf("SlugTyping(%q) = %q, want nori-a", "Nori A", got)
	}
	// Typing a hyphenated name one keystroke at a time must converge on the slug.
	typed := ""
	for _, r := range "Nori App" {
		typed = SlugTyping(typed + string(r))
	}
	if typed != "nori-app" {
		t.Errorf("incremental typing produced %q, want nori-app", typed)
	}
}

func TestDisplayNameFallsBackToName(t *testing.T) {
	if got := (Project{Name: "nori-app"}).DisplayName(); got != "nori-app" {
		t.Errorf("DisplayName with no label = %q, want the name", got)
	}
	if got := (Project{Name: "nori-app", Label: "Nori App"}).DisplayName(); got != "Nori App" {
		t.Errorf("DisplayName = %q, want the label", got)
	}
	// A whitespace-only label is not a label.
	if got := (Project{Name: "nori-app", Label: "   "}).DisplayName(); got != "nori-app" {
		t.Errorf("DisplayName with a blank label = %q, want the name", got)
	}
}

func TestDisplayNameForUnknownProject(t *testing.T) {
	c := &Config{Projects: []Project{{Name: "nori-app", Label: "Nori App"}}}
	if got := c.DisplayNameFor("nori-app"); got != "Nori App" {
		t.Errorf("DisplayNameFor = %q, want Nori App", got)
	}
	// A session whose project was removed from config still renders something.
	if got := c.DisplayNameFor("gone"); got != "gone" {
		t.Errorf("DisplayNameFor(unknown) = %q, want the id back", got)
	}
}
