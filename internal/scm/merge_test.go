package scm

import (
	"context"
	"testing"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func TestBehindByArgsAndParse(t *testing.T) {
	bin, argsLog := fakeGh(t, "3", 0)
	c := &Client{GhBin: bin}
	n, err := c.BehindBy(context.Background(), "acme/nori", "main", testSHA)
	if err != nil || n != 3 {
		t.Fatalf("BehindBy = %d, %v; want 3", n, err)
	}
	want := "api repos/acme/nori/compare/main..." + testSHA + " --jq .behind_by"
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked %q, want %q", args, want)
	}
}

// "Could not tell" must never read as "up to date": a gh failure and an
// unparsable answer are both errors.
func TestBehindByFailsClosed(t *testing.T) {
	bin, _ := fakeGh(t, "", 1)
	if _, err := (&Client{GhBin: bin}).BehindBy(context.Background(), "acme/nori", "main", testSHA); err == nil {
		t.Error("gh failure must be an error")
	}
	bin, _ = fakeGh(t, "null", 0)
	if _, err := (&Client{GhBin: bin}).BehindBy(context.Background(), "acme/nori", "main", testSHA); err == nil {
		t.Error("an unparsable behind_by must be an error")
	}
}

func TestBehindByRejectsHostileInput(t *testing.T) {
	bin, _ := fakeGh(t, "0", 0)
	c := &Client{GhBin: bin}
	for _, tc := range []struct{ repo, base, sha string }{
		{"acme/nori", "main..x", testSHA},
		{"acme/nori", "-main", testSHA},
		{"acme/nori", "main?x=1", testSHA},
		{"acme/nori", "main", "HEAD"},
		{"nori", "main", testSHA},
	} {
		if _, err := c.BehindBy(context.Background(), tc.repo, tc.base, tc.sha); err == nil {
			t.Errorf("BehindBy(%q, %q, %q) accepted", tc.repo, tc.base, tc.sha)
		}
	}
}

func TestMergePRPinsTheHead(t *testing.T) {
	bin, argsLog := fakeGh(t, "", 0)
	c := &Client{GhBin: bin}
	if err := c.MergePR(context.Background(), "acme/nori", 42, "squash", testSHA); err != nil {
		t.Fatal(err)
	}
	want := "pr merge 42 --repo acme/nori --squash --match-head-commit " + testSHA
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked %q, want %q", args, want)
	}
	if err := c.MergePR(context.Background(), "acme/nori", 42, "admin", testSHA); err == nil {
		t.Error("an unknown merge method must be refused")
	}
	if err := c.MergePR(context.Background(), "acme/nori", 42, "merge", ""); err == nil {
		t.Error("an unpinned merge must be refused")
	}
}
