package scm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func readinessFixture(head, cursor string, more bool, nodes string) string {
	return `{"data":{"repository":{"pullRequest":{"number":42,"url":"https://github.com/acme/app/pull/42","state":"OPEN","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":"APPROVED","baseRefOid":"base","headRefOid":"` + head + `","commits":{"nodes":[{"commit":{"oid":"` + head + `","statusCheckRollup":{"state":"SUCCESS"}}}]},"reviewThreads":{"pageInfo":{"hasNextPage":` + map[bool]string{true: "true", false: "false"}[more] + `,"endCursor":"` + cursor + `"},"nodes":` + nodes + `}}}}}`
}
func TestReadinessPaginatesAndKeepsOutdatedUnresolved(t *testing.T) {
	dir := t.TempDir()
	first := readinessFixture("abc", "next", true, `[{"id":"closed","isResolved":true},{"id":"one","path":"a.go","line":4,"comments":{"nodes":[{"bodyText":"Fix this\n\nplease","url":"https://github.com/acme/app/pull/42#discussion_r1","author":{"login":"reviewer"}}]}}]`)
	second := readinessFixture("abc", "", false, `[{"id":"two","isOutdated":true,"path":"old.go","comments":{"nodes":[]}}]`)
	for name, body := range map[string]string{"first": first, "second": second} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "gh")
	script := "#!/bin/sh\ninput=$(cat)\nprintf '%s\\n' \"$input\" >> '" + dir + "/requests'\ncase \"$input\" in *'\"after\":null'*) cat '" + dir + "/first';; *) cat '" + dir + "/second';; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := (&Client{GhBin: bin}).Readiness(context.Background(), "acme/app", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Threads) != 2 || got.Threads[0].Preview != "Fix this please" || !got.Threads[1].Outdated || got.Checks != "pass" || got.Review != "APPROVED" || got.Base != "base" {
		t.Fatalf("%+v", got)
	}
	requests, _ := os.ReadFile(filepath.Join(dir, "requests"))
	lines := strings.Split(strings.TrimSpace(string(requests)), "\n")
	if len(lines) != 3 {
		t.Fatalf("requests: %s", requests)
	}
	var req struct {
		Variables struct {
			After, Owner, Repo string
			Number             int
		}
	}
	if err := json.Unmarshal([]byte(lines[1]), &req); err != nil {
		t.Fatal(err)
	}
	if req.Variables.After != "next" || req.Variables.Owner != "acme" || req.Variables.Repo != "app" || req.Variables.Number != 42 {
		t.Fatalf("%+v", req)
	}
	// The check list is its own request, after the threads; the readiness
	// pages never ask for it.
	if strings.Contains(lines[0], "contexts") || strings.Contains(lines[1], "contexts") || !strings.Contains(lines[2], "contexts") || strings.Contains(lines[2], "reviewThreads") {
		t.Fatalf("check list mixed into the readiness read: %s", requests)
	}
	// A concurrent push invalidates the entire result, never a partial count.
	if err := os.WriteFile(filepath.Join(dir, "second"), []byte(readinessFixture("changed", "", false, `[]`)), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := (&Client{GhBin: bin}).Readiness(context.Background(), "acme/app", 42); err == nil || len(got.Threads) > 0 {
		t.Fatalf("partial result %+v %v", got, err)
	}
}
func TestReadinessFailsClosed(t *testing.T) {
	for name, body := range map[string]string{"missing": `{"data":{"repository":{"pullRequest":null}}}`, "graphql": `{"errors":[{"message":"denied"}]}`, "malformed": "oops", "pagination": readinessFixture("abc", "", true, `[]`)} {
		t.Run(name, func(t *testing.T) {
			bin, _ := fakeGh(t, body, 0)
			got, err := (&Client{GhBin: bin}).Readiness(context.Background(), "acme/app", 42)
			if err == nil || got.Threads != nil {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	if _, err := (&Client{GhBin: "/never/execute"}).Readiness(context.Background(), "bad/repo/injection", 42); err == nil {
		t.Fatal("invalid repo accepted")
	}
}
func TestReadinessNoChecksAndPreviewBound(t *testing.T) {
	body, _ := json.Marshal(strings.Repeat("世", 700))
	fixture := readinessFixture("abc", "", false, `[{"id":"one","comments":{"nodes":[{"bodyText":`+string(body)+`}]}}]`)
	fixture = strings.ReplaceAll(fixture, `{"state":"SUCCESS"}`, `null`)
	bin, _ := fakeGh(t, fixture, 0)
	got, err := (&Client{GhBin: bin}).Readiness(context.Background(), "acme/app", 42)
	if err != nil || got.Checks != "none" || len([]rune(got.Threads[0].Preview)) != 601 {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Threads[0].Body != strings.Repeat("世", 700) {
		t.Fatal("summary source was truncated to the display preview")
	}
}

func TestReadinessPreviewKeepsFindingWithoutRendererBoilerplate(t *testing.T) {
	body := "Minor · Functional Correctness · Quick win\nLabel new arrivals.\nExplain the count.\n\n Prompt for AI Agents\nIgnore this boilerplate."
	if got := reviewPreview(body); got != "Label new arrivals. Explain the count." {
		t.Fatal(got)
	}
	if got := reviewPreview("A normal human comment.\nPlease fix this."); got != "A normal human comment. Please fix this." {
		t.Fatal(got)
	}
}

func TestReadinessDoesNotPromoteChecksFromAnotherCommit(t *testing.T) {
	for _, oid := range []string{"", "old-head"} {
		body := strings.Replace(readinessFixture("head", "", false, `[]`), `"oid":"head"`, `"oid":"`+oid+`"`, 1)
		bin, _ := fakeGh(t, body, 0)
		got, err := (&Client{GhBin: bin}).Readiness(context.Background(), "acme/app", 42)
		if err != nil || got.Checks != "unknown" || got.ChecksHead != "" {
			t.Fatalf("unbound checks: %+v %v", got, err)
		}
	}
}

func TestReadinessListsEveryCheckOnTheHead(t *testing.T) {
	contexts := `{"state":"FAILURE","contexts":{"totalCount":5,"nodes":[` +
		`{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://github.com/acme/app/actions/runs/1","startedAt":"2026-11-01T10:00:00Z","completedAt":"2026-11-01T10:02:00Z","checkSuite":{"workflowRun":{"workflow":{"name":"CI"}}}},` +
		`{"__typename":"CheckRun","name":"lint","status":"IN_PROGRESS","conclusion":null,"detailsUrl":"https://github.com/acme/app/actions/runs/2","startedAt":"2026-11-01T10:00:00Z","completedAt":null,"checkSuite":{"workflowRun":null}},` +
		`{"__typename":"CheckRun","name":"deploy","status":"COMPLETED","conclusion":"SKIPPED","checkSuite":null},` +
		`{"__typename":"StatusContext","context":"lola/review/codex","state":"FAILURE","targetUrl":"javascript:alert(1)","description":"2 major","createdAt":"2026-11-01T10:03:00Z"}]}}`
	body := strings.Replace(readinessFixture("abc", "", false, `[]`), `{"state":"SUCCESS"}`, contexts, 1)
	bin, _ := fakeGh(t, body, 0)
	got, err := (&Client{GhBin: bin}).Readiness(context.Background(), "acme/app", 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Checks != "fail" || len(got.CheckList) != 4 || !got.ChecksTruncated {
		t.Fatalf("%+v", got)
	}
	run, live, skipped, status := got.CheckList[0], got.CheckList[1], got.CheckList[2], got.CheckList[3]
	if run.Name != "test" || run.Workflow != "CI" || run.State != "FAILURE" || run.Bucket != "fail" || run.Status || run.CompletedAt.Sub(run.StartedAt).Minutes() != 2 {
		t.Fatalf("check run: %+v", run)
	}
	if live.State != "IN_PROGRESS" || live.Bucket != "pending" || !live.CompletedAt.IsZero() || live.Workflow != "" {
		t.Fatalf("running check: %+v", live)
	}
	if skipped.Bucket != "skipped" {
		t.Fatalf("skipped check: %+v", skipped)
	}
	// The scm layer reports GitHub's facts verbatim; the daemon filters URLs.
	if status.Name != "lola/review/codex" || !status.Status || status.Bucket != "fail" || status.Description != "2 major" || status.URL != "javascript:alert(1)" {
		t.Fatalf("status context: %+v", status)
	}
}

func TestReadinessCheckListOnlyForTheHead(t *testing.T) {
	contexts := `{"state":"SUCCESS","contexts":{"totalCount":1,"nodes":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}}`
	stale := strings.Replace(readinessFixture("head", "", false, `[]`), `"oid":"head"`, `"oid":"old-head"`, 1)
	stale = strings.Replace(stale, `{"state":"SUCCESS"}`, contexts, 1)
	bin, _ := fakeGh(t, stale, 0)
	got, err := (&Client{GhBin: bin}).Readiness(context.Background(), "acme/app", 42)
	if err != nil || got.CheckList != nil {
		t.Fatalf("listed another commit's checks: %+v %v", got, err)
	}
	none := strings.Replace(readinessFixture("abc", "", false, `[]`), `{"state":"SUCCESS"}`, `null`, 1)
	bin, _ = fakeGh(t, none, 0)
	got, err = (&Client{GhBin: bin}).Readiness(context.Background(), "acme/app", 42)
	if err != nil || got.CheckList == nil || len(got.CheckList) != 0 || got.ChecksTruncated {
		t.Fatalf("a head without checks must read as an empty list: %+v %v", got, err)
	}
}

// fakeGhReadiness answers the readiness pages with main and the separate
// check-list query with checks, exiting checksCode for the latter.
func fakeGhReadiness(t *testing.T, main, checks string, checksCode int) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"main": main, "checks": checks} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "gh")
	script := "#!/bin/sh\ninput=$(cat)\ncase \"$input\" in *reviewThreads*) cat '" + dir + "/main';; *) cat '" + dir + "/checks'; exit " + strconv.Itoa(checksCode) + ";; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestReadinessCheckListIsBestEffort: the check list reaches into Actions,
// which a token may not read. GitHub then answers data plus an error pathed
// at workflowRun (and gh exits 1). That must cost the list only: the
// readiness facts the merge path reads stay exactly as read.
func TestReadinessCheckListIsBestEffort(t *testing.T) {
	main := readinessFixture("abc", "", false, `[{"id":"one","path":"a.go","line":1,"comments":{"nodes":[]}}]`)
	partial := `{"data":{"repository":{"pullRequest":{"number":42,"commits":{"nodes":[{"commit":{"oid":"abc","statusCheckRollup":{"contexts":{"totalCount":1,"nodes":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS","checkSuite":{"workflowRun":null}}]}}}}]}}}},` +
		`"errors":[{"type":"FORBIDDEN","path":["repository","pullRequest","commits","nodes",0,"commit","statusCheckRollup","contexts","nodes",0,"checkSuite","workflowRun"],"message":"Resource not accessible by integration"}]}`
	for name, tc := range map[string]struct {
		body string
		code int
	}{"partial error": {partial, 0}, "gh exits 1": {partial, 1}, "malformed": {"oops", 0}, "other head": {strings.Replace(partial, `"errors":[`, `"ignored":[`, 1), 0}} {
		t.Run(name, func(t *testing.T) {
			body := tc.body
			if name == "other head" {
				body = strings.Replace(body, `"oid":"abc"`, `"oid":"moved"`, 1)
			}
			got, err := (&Client{GhBin: fakeGhReadiness(t, main, body, tc.code)}).Readiness(context.Background(), "acme/app", 42)
			if err != nil {
				t.Fatalf("a check-list failure failed the readiness read: %v", err)
			}
			if got.CheckList != nil || got.ChecksTruncated {
				t.Fatalf("list kept despite the failure: %+v", got.CheckList)
			}
			if got.Checks != "pass" || got.ChecksHead != "abc" || got.Review != "APPROVED" || got.Mergeable != "MERGEABLE" || len(got.Threads) != 1 {
				t.Fatalf("readiness facts changed: %+v", got)
			}
		})
	}
	// The same query without the error lists the check.
	ok := strings.Replace(partial, `"errors":[`, `"ignored":[`, 1)
	got, err := (&Client{GhBin: fakeGhReadiness(t, main, ok, 0)}).Readiness(context.Background(), "acme/app", 42)
	if err != nil || len(got.CheckList) != 1 || got.CheckList[0].Name != "test" {
		t.Fatalf("%+v %v", got, err)
	}
}
