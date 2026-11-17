package scm

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestChecksStateAmong(t *testing.T) {
	checks := []Check{
		{Name: "Laravel Tests (PHP 8.5)", Bucket: "pass"},
		{Name: "vitest-storybook", Bucket: "fail"}, // shadow mode, not required
		{Name: "lola/review/codex-session", Bucket: "fail"},
	}
	cases := []struct {
		name     string
		checks   []Check
		required []string
		want     string
	}{
		{"all checks: a red non-required job fails", checks, nil, "fail"},
		{"required only: the shadow job is ignored", checks, []string{"Laravel Tests (PHP 8.5)"}, "pass"},
		{"required but not reported is pending", checks, []string{"Laravel Tests (PHP 8.5)", "Prettier format check"}, "pending"},
		{"a required lola status is not CI", checks, []string{"Laravel Tests (PHP 8.5)", "lola/review/codex-session"}, "pass"},
		{"lola statuses never fail CI", []Check{{Name: "lola/review/x", Bucket: "fail"}}, nil, "none"},
		{"nothing", nil, nil, "none"},
		{"required list against no checks is pending", nil, []string{"test"}, "pending"},
		{"only a review is required: shadow jobs are not CI", checks, []string{"lola/review/codex-session"}, "none"},
	}
	for _, c := range cases {
		if got := ChecksStateAmong(c.checks, c.required); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestApplyRequiredLeavesLegacyFactsAlone(t *testing.T) {
	pr := &PR{ChecksState: "fail"} // persisted before Checks existed
	pr.ApplyRequired([]string{"test"})
	if pr.ChecksState != "fail" {
		t.Fatalf("ChecksState = %q, want the stored verdict kept", pr.ChecksState)
	}
	pr = &PR{ChecksState: "fail", Checks: []Check{{Name: "test", Bucket: "pass"}, {Name: "other", Bucket: "fail"}}}
	pr.ApplyRequired(nil)
	if pr.ChecksState != "fail" {
		t.Fatalf("nil required must keep the all-checks verdict, got %q", pr.ChecksState)
	}
	pr.ApplyRequired([]string{"test"})
	if pr.ChecksState != "pass" {
		t.Fatalf("ChecksState = %q, want pass", pr.ChecksState)
	}
}

func TestFreshPRWithoutChecksWaitsForRequiredChecks(t *testing.T) {
	bin, _ := fakeGh(t, `[{"number":7,"state":"OPEN","statusCheckRollup":[]}]`, 0)
	pr, err := (&Client{GhBin: bin}).PRForBranch(context.Background(), "acme/nori", "topic")
	if err != nil {
		t.Fatal(err)
	}
	pr.ApplyRequired([]string{"test"})
	if pr.ChecksState != "pending" {
		t.Fatalf("unreported required check = %q, want pending", pr.ChecksState)
	}
}

func TestRequiredChecksEscapesBranch(t *testing.T) {
	bin, log := fakeGh(t, `[]`, 0)
	if _, err := (&Client{GhBin: bin}).RequiredChecks(context.Background(), "acme/nori", "release/next"); err != nil {
		t.Fatal(err)
	}
	if args := loggedArgs(t, log); args != "api repos/acme/nori/rules/branches/release%2Fnext" {
		t.Fatalf("invoked %q", args)
	}
}

func TestRequiredChecksParsesActiveRulesets(t *testing.T) {
	bin, argsLog := fakeGh(t, `[
	  {"type":"deletion"},
	  {"type":"required_status_checks","parameters":{"required_status_checks":[
	    {"context":"Laravel Tests (PHP 8.5)"},{"context":"Build test assets","integration_id":15368}]}},
	  {"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"Build test assets"}]}}
	]`, 0)
	got, err := (&Client{GhBin: bin}).RequiredChecks(context.Background(), "acme/nori", "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Laravel Tests (PHP 8.5)", "Build test assets"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if args := loggedArgs(t, argsLog); args != "api repos/acme/nori/rules/branches/main" {
		t.Fatalf("invoked %q", args)
	}
}

func TestRequiredChecksRefusesBadInput(t *testing.T) {
	c := &Client{GhBin: "/nonexistent"}
	for _, br := range []string{"", "../x", "-x", "a b"} {
		if _, err := c.RequiredChecks(context.Background(), "acme/nori", br); err == nil {
			t.Errorf("branch %q accepted", br)
		}
	}
}

func TestSetCommitStatusArgsAndGuards(t *testing.T) {
	bin, argsLog := fakeGh(t, `{}`, 0)
	c := &Client{GhBin: bin}
	sha := "0123456789abcdef0123456789abcdef01234567"
	long := strings.Repeat("é", 200)
	if err := c.SetCommitStatus(context.Background(), "acme/nori", sha, StatusFailure, "lola/review/codex-session", long, "https://github.com/acme/nori/pull/1"); err != nil {
		t.Fatal(err)
	}
	args := loggedArgs(t, argsLog)
	for _, want := range []string{"api -X POST repos/acme/nori/statuses/" + sha, "state=failure", "context=lola/review/codex-session", "target_url=https://github.com/acme/nori/pull/1"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	for _, bad := range []struct{ state, ctx, sha string }{
		{"weird", "lola/review/x", sha},
		{StatusSuccess, "Laravel Tests (PHP 8.5)", sha}, // never someone else's context
		{StatusSuccess, "lola/review/x", "not-a-sha"},
	} {
		if err := c.SetCommitStatus(context.Background(), "acme/nori", bad.sha, bad.state, bad.ctx, "d", ""); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestFailingChecksAmongSkipsUnrequiredAndLola(t *testing.T) {
	bin, _ := fakeGh(t, `[
	  {"name":"test","state":"FAILURE","bucket":"fail","link":"","workflow":"CI"},
	  {"name":"vitest-storybook","state":"FAILURE","bucket":"fail","link":"","workflow":"CI"},
	  {"name":"lola/review/codex-session","state":"FAILURE","bucket":"fail","link":"","workflow":""}
	]`, 1)
	out, err := (&Client{GhBin: bin}).FailingChecksAmong(context.Background(), "acme/nori", 7, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 failing check(s)") || strings.Contains(out, "storybook") || strings.Contains(out, "lola/") {
		t.Fatalf("summary = %q", out)
	}
}
