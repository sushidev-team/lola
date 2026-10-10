package scm

import (
	"context"
	"testing"
)

func TestMarkPRReadyArgs(t *testing.T) {
	bin, argsLog := fakeGh(t, "", 0)
	if err := (&Client{GhBin: bin}).MarkPRReady(context.Background(), "acme/nori", 7); err != nil {
		t.Fatal(err)
	}
	if args, want := loggedArgs(t, argsLog), "pr ready 7 --repo acme/nori"; args != want {
		t.Errorf("invoked %q, want %q", args, want)
	}
}

func TestClosePRKeepsBranch(t *testing.T) {
	bin, argsLog := fakeGh(t, "", 0)
	if err := (&Client{GhBin: bin}).ClosePR(context.Background(), "acme/nori", 8, "superseded by #7"); err != nil {
		t.Fatal(err)
	}
	if args, want := loggedArgs(t, argsLog), "pr close 8 --repo acme/nori --comment superseded by #7"; args != want {
		t.Errorf("invoked %q, want %q", args, want)
	}
}

func TestPRStateRejectsBadInput(t *testing.T) {
	bin, _ := fakeGh(t, "", 0)
	c := &Client{GhBin: bin}
	if err := c.MarkPRReady(context.Background(), "nori", 7); err == nil {
		t.Error("repo without owner accepted")
	}
	if err := c.ClosePR(context.Background(), "acme/nori", 0, ""); err == nil {
		t.Error("PR 0 accepted")
	}
	bin, _ = fakeGh(t, "", 1)
	if err := (&Client{GhBin: bin}).ClosePR(context.Background(), "acme/nori", 8, ""); err == nil {
		t.Error("gh failure must be an error")
	}
}
