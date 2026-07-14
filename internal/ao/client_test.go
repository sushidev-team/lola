package ao

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeBin installs a shell script standing in for the ao binary. It echoes
// the canned stdout and appends its argv to <dir>/args.log.
func fakeBin(t *testing.T, stdout string) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "ao")
	argsLog = filepath.Join(dir, "args.log")
	script := "#!/bin/sh\necho \"$@\" >> " + argsLog + "\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

func loggedArgs(t *testing.T, argsLog string) string {
	t.Helper()
	b, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// Fixture mirrors the verified output of AO's bundled CLI:
// `ao session ls --json` wraps sessions in data[] and uses projectId/issueId.
const sessionLsFixture = `{
  "data": [
    {"id": "nori-app-2", "projectId": "nori-app", "issueId": "NORI-12", "status": "working", "isTerminated": false},
    {"id": "nori-app-1", "projectId": "nori-app", "status": "terminated", "isTerminated": true}
  ],
  "meta": {"hiddenTerminatedCount": 0}
}`

func TestLiveSessionsParsesRealShapeAndDropsTerminated(t *testing.T) {
	bin, argsLog := fakeBin(t, sessionLsFixture)
	c := &Client{Bin: bin}

	got, err := c.LiveSessions(context.Background())
	if err != nil {
		t.Fatalf("LiveSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 live session (terminated dropped), got %d: %+v", len(got), got)
	}
	s := got[0]
	if s.ID != "nori-app-2" || s.Project != "nori-app" || s.IssueID != "NORI-12" || s.Status != "working" {
		t.Errorf("session = %+v, want id/projectId/issueId/status mapped", s)
	}
	if args := loggedArgs(t, argsLog); args != "session ls --json" {
		t.Errorf("invoked %q, want %q", args, "session ls --json")
	}
}

func TestSpawnUsesFlagForm(t *testing.T) {
	bin, argsLog := fakeBin(t, "")
	c := &Client{Bin: bin}

	// Empty prompt: --prompt must be omitted entirely.
	if err := c.Spawn(context.Background(), "nori-app", "NORI-12", ""); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Current AO rejects the old positional `spawn <project> <issue>` form.
	if args := loggedArgs(t, argsLog); args != "spawn --project nori-app --issue NORI-12" {
		t.Errorf("invoked %q, want flag form without --prompt", args)
	}
}

func TestSpawnPassesPromptFlag(t *testing.T) {
	bin, argsLog := fakeBin(t, "")
	c := &Client{Bin: bin}

	prompt := "Linear issue NORI-12: fix login"
	if err := c.Spawn(context.Background(), "nori-app", "NORI-12", prompt); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	want := "spawn --project nori-app --issue NORI-12 --prompt " + prompt
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked %q, want %q", args, want)
	}
}

func TestProjectsParsesProjectsEnvelope(t *testing.T) {
	fixture := `{
  "projects": [
    {"id": "nori-app", "path": "/repos/nori-app"},
    {"id": "backend"}
  ]
}`
	bin, argsLog := fakeBin(t, fixture)
	c := &Client{Bin: bin}

	got, err := c.Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	want := []string{"nori-app", "backend"}
	if !slices.Equal(got, want) {
		t.Errorf("Projects = %v, want %v", got, want)
	}
	if args := loggedArgs(t, argsLog); args != "project ls --json" {
		t.Errorf("invoked %q, want %q", args, "project ls --json")
	}
}

// The project-ls envelope is unverified against a real AO build; if it turns
// out to use data[] like session ls, Projects must still yield the IDs
// instead of silently parsing an empty registry.
func TestProjectsAcceptsDataEnvelope(t *testing.T) {
	fixture := `{
  "data": [
    {"id": "nori-app"}
  ],
  "meta": {}
}`
	bin, _ := fakeBin(t, fixture)
	c := &Client{Bin: bin}

	got, err := c.Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if want := []string{"nori-app"}; !slices.Equal(got, want) {
		t.Errorf("Projects = %v, want %v", got, want)
	}
}

func TestCountLiveFiltersProjectAndStatus(t *testing.T) {
	sessions := []SessionState{
		{Project: "nori-app", Status: "working"},
		{Project: "nori-app", Status: "review_pending"}, // parked: not counted
		{Project: "other", Status: "working"},           // other project
	}
	counting := map[string]bool{"working": true}
	if n := CountLive(sessions, "nori-app", counting); n != 1 {
		t.Errorf("CountLive = %d, want 1", n)
	}
}
