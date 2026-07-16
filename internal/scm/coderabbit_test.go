package scm

// Tests for the [coderabbit] PR-comment WATCH fetch (coderabbit.go): author
// filtering, the `since` watermark, the returned `latest` watermark, empty/
// non-matching handling, size bounding, and gh-error surfacing. The fake gh
// (fakeGhRouter, from reaction_test.go) dispatches the "pr_view" route.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// crFixture: two coderabbit items (a comment and a COMMENTED review), one human
// comment, one empty-body coderabbit review, and one non-coderabbit review — so
// author filtering, empty-body skipping, and timestamp ordering are all exercised.
const crFixture = `{
  "comments": [
    {"author":{"login":"coderabbitai[bot]"},"body":"Summary: the walkthrough","createdAt":"2024-01-02T10:00:00Z"},
    {"author":{"login":"humandev"},"body":"looks good to me","createdAt":"2024-01-03T10:00:00Z"}
  ],
  "reviews": [
    {"author":{"login":"coderabbitai[bot]"},"body":"Actionable: fix the nil deref","state":"COMMENTED","submittedAt":"2024-01-04T10:00:00Z"},
    {"author":{"login":"coderabbitai[bot]"},"body":"","state":"COMMENTED","submittedAt":"2024-01-05T10:00:00Z"},
    {"author":{"login":"reviewer"},"body":"please change this","state":"CHANGES_REQUESTED","submittedAt":"2024-01-06T10:00:00Z"}
  ]
}`

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestCodeRabbitCommentsFiltersAndWatermarks(t *testing.T) {
	bin, argsLog := fakeGhRouter(t, map[string]ghResp{"pr_view": {out: crFixture, code: 0}})
	c := &Client{GhBin: bin}

	since := mustTime(t, "2024-01-01T00:00:00Z")
	text, latest, err := c.CodeRabbitComments(context.Background(), "acme/nori", 7, since, "coderabbitai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both coderabbit items surface; the human comment, the empty-body review, and
	// the non-coderabbit review do not.
	for _, want := range []string{"Summary: the walkthrough", "fix the nil deref", "[comment]", "[review COMMENTED]"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q\n%s", want, text)
		}
	}
	for _, no := range []string{"looks good to me", "please change this"} {
		if strings.Contains(text, no) {
			t.Errorf("text must not include %q\n%s", no, text)
		}
	}
	// Oldest-first: the 1/2 comment precedes the 1/4 review.
	if i, j := strings.Index(text, "walkthrough"), strings.Index(text, "nil deref"); i < 0 || j < 0 || i > j {
		t.Errorf("items not oldest-first (comment before review): %s", text)
	}
	// latest = newest matched item (the 1/4 review), NOT the empty-body 1/5.
	if want := mustTime(t, "2024-01-04T10:00:00Z"); !latest.Equal(want) {
		t.Errorf("latest = %v, want %v", latest, want)
	}
	// The one gh call was `pr view … --json comments,reviews`.
	if log := loggedArgs(t, argsLog); !strings.Contains(log, "pr view 7") || !strings.Contains(log, "comments,reviews") {
		t.Errorf("unexpected gh argv: %q", log)
	}
}

func TestCodeRabbitCommentsRespectsSince(t *testing.T) {
	bin, _ := fakeGhRouter(t, map[string]ghResp{"pr_view": {out: crFixture, code: 0}})
	c := &Client{GhBin: bin}

	// Watermark past the comment but before the review: only the review is new.
	since := mustTime(t, "2024-01-03T00:00:00Z")
	text, latest, err := c.CodeRabbitComments(context.Background(), "acme/nori", 7, since, "coderabbitai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(text, "walkthrough") {
		t.Errorf("comment older than `since` must not surface: %s", text)
	}
	if !strings.Contains(text, "nil deref") {
		t.Errorf("review newer than `since` must surface: %s", text)
	}
	if want := mustTime(t, "2024-01-04T10:00:00Z"); !latest.Equal(want) {
		t.Errorf("latest = %v, want %v", latest, want)
	}
}

func TestCodeRabbitCommentsNothingNew(t *testing.T) {
	bin, _ := fakeGhRouter(t, map[string]ghResp{"pr_view": {out: crFixture, code: 0}})
	c := &Client{GhBin: bin}

	// Watermark at (== not after) the newest matched item: nothing strictly newer.
	since := mustTime(t, "2024-01-04T10:00:00Z")
	text, latest, err := c.CodeRabbitComments(context.Background(), "acme/nori", 7, since, "coderabbitai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("no item is newer than `since`; want empty text, got %q", text)
	}
	if !latest.Equal(since) {
		t.Errorf("latest must not move below/beyond the newest item: got %v want %v", latest, since)
	}
}

func TestCodeRabbitCommentsCustomAuthor(t *testing.T) {
	bin, _ := fakeGhRouter(t, map[string]ghResp{"pr_view": {out: crFixture, code: 0}})
	c := &Client{GhBin: bin}

	// A different bot: only the "reviewer" CHANGES_REQUESTED review matches.
	since := mustTime(t, "2024-01-01T00:00:00Z")
	text, _, err := c.CodeRabbitComments(context.Background(), "acme/nori", 7, since, "reviewer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "please change this") || strings.Contains(text, "walkthrough") {
		t.Errorf("author filter should isolate 'reviewer': %s", text)
	}
}

func TestCodeRabbitCommentsGhErrorIsDistinct(t *testing.T) {
	bin, _ := fakeGhRouter(t, map[string]ghResp{"pr_view": {out: "", err: "boom", code: 1}})
	c := &Client{GhBin: bin}

	since := mustTime(t, "2024-01-01T00:00:00Z")
	text, latest, err := c.CodeRabbitComments(context.Background(), "acme/nori", 7, since, "coderabbitai")
	if err == nil {
		t.Fatal("a gh failure must surface as an error, not empty text")
	}
	if text != "" {
		t.Errorf("error path must return empty text, got %q", text)
	}
	if !latest.Equal(since) {
		t.Errorf("error path must not advance the watermark: got %v want %v", latest, since)
	}
}

func TestCodeRabbitCommentsSizeBound(t *testing.T) {
	huge := strings.Repeat("x", reactionMaxBytes*2)
	fixture := `{"comments":[{"author":{"login":"coderabbitai[bot]"},"body":"` + huge + `","createdAt":"2024-02-01T00:00:00Z"}],"reviews":[]}`
	bin, _ := fakeGhRouter(t, map[string]ghResp{"pr_view": {out: fixture, code: 0}})
	c := &Client{GhBin: bin}

	text, _, err := c.CodeRabbitComments(context.Background(), "acme/nori", 7, time.Time{}, "coderabbitai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(text) > reactionMaxBytes {
		t.Errorf("text = %d bytes, want <= %d", len(text), reactionMaxBytes)
	}
}
