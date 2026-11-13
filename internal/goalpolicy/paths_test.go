package goalpolicy

import "testing"

func TestScopeContainmentAndOverlap(t *testing.T) {
	all := Scope{All: true}
	read := Scope{}
	src := Scope{Paths: []string{"src"}}
	api := Scope{Paths: []string{"src/api"}}
	web := Scope{Paths: []string{"src/web"}}
	if !api.Within(src) || src.Within(api) || !src.Within(all) || all.Within(src) || !read.Within(api) {
		t.Fatal("scope containment")
	}
	if !src.Overlaps(api) || api.Overlaps(web) || all.Overlaps(read) || !all.Overlaps(web) || !api.Overlaps(Scope{Paths: []string{"SRC/API/handler.go"}}) {
		t.Fatal("scope overlap")
	}
	if src.Contains("src-sibling/file") || !src.Contains("src/file") || read.Contains("file") {
		t.Fatal("path boundary")
	}
}
func TestOwnedPathsRejectEscapesAndControlDirectories(t *testing.T) {
	for _, p := range []string{".", "..", "../x", "/x", "src/../x", "src//x", "src/", "src\\x", ".git/config", "src/.GIT/config", ".lola/config", "src/.LoLa/file", "line\nfile", "\x00"} {
		if err := ValidatePaths([]string{p}); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
	if err := ValidatePaths([]string{"src", "src"}); err == nil {
		t.Fatal("duplicates")
	}
	if err := ValidatePaths([]string{"src/api", "tests/invitations_test.go"}); err != nil {
		t.Fatal(err)
	}
	if err := (Scope{All: true, Paths: []string{"src"}}).Validate(); err == nil {
		t.Fatal("ambiguous scope")
	}
}
