package scm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	if len(lines) != 2 {
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
