package scm

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeGh installs a shell script standing in for the gh binary (pattern:
// internal/tmux fake-bin helper). It appends its argv to <dir>/args.log,
// echoes the canned stdout, and exits with the given code.
func fakeGh(t *testing.T, stdout string, exitCode int) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "gh")
	argsLog = filepath.Join(dir, "args.log")
	script := "#!/bin/sh\necho \"$@\" >> " + argsLog + "\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if exitCode != 0 {
		script += "echo boom >&2\nexit " + strconv.Itoa(exitCode) + "\n"
	}
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

// Fixture mirrors gh 2.x `pr list --json`: a top-level array; CheckRun
// entries carry status/conclusion, StatusContext entries carry state.
const prListFixture = `[
  {
    "number": 42,
    "url": "https://github.com/acme/nori/pull/42",
    "state": "OPEN",
    "isDraft": false,
    "mergeable": "MERGEABLE",
    "reviewDecision": "APPROVED",
    "statusCheckRollup": [
      {"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"},
      {"__typename": "StatusContext", "state": "SUCCESS"}
    ]
  }
]`

func TestPRForBranchArgsAndParse(t *testing.T) {
	bin, argsLog := fakeGh(t, prListFixture, 0)
	c := &Client{GhBin: bin}

	pr, err := c.PRForBranch(context.Background(), "acme/nori", "lola/NORI-12-1")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	want := "pr list --repo acme/nori --head lola/NORI-12-1 --state all --limit 1 " +
		"--json number,url,state,isDraft,mergeable,reviewDecision,statusCheckRollup"
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked %q, want %q", args, want)
	}
	if pr == nil {
		t.Fatal("PRForBranch = nil, want PR")
	}
	exp := PR{
		Number:         42,
		URL:            "https://github.com/acme/nori/pull/42",
		State:          "OPEN",
		IsDraft:        false,
		Mergeable:      "MERGEABLE",
		ReviewDecision: "APPROVED",
		ChecksState:    "pass",
	}
	if *pr != exp {
		t.Errorf("PR = %+v, want %+v", *pr, exp)
	}
}

// No PR for the branch: gh exits 0 with an empty array → (nil, nil), never an
// error (errors mean "could not check" and must stay distinguishable).
func TestPRForBranchNoPR(t *testing.T) {
	bin, _ := fakeGh(t, `[]`, 0)
	c := &Client{GhBin: bin}

	pr, err := c.PRForBranch(context.Background(), "acme/nori", "lola/NORI-13-1")
	if err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	if pr != nil {
		t.Errorf("PRForBranch = %+v, want nil for empty array", pr)
	}
}

func TestPRForBranchGhFailure(t *testing.T) {
	bin, _ := fakeGh(t, "", 1)
	c := &Client{GhBin: bin}

	pr, err := c.PRForBranch(context.Background(), "acme/nori", "b")
	if err == nil {
		t.Fatalf("PRForBranch = %+v, want error on gh exit 1", pr)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q should surface gh stderr", err)
	}
}

func TestPRForBranchBadJSON(t *testing.T) {
	bin, _ := fakeGh(t, `not json`, 0)
	c := &Client{GhBin: bin}

	if _, err := c.PRForBranch(context.Background(), "acme/nori", "b"); err == nil {
		t.Fatal("want error on unparseable gh output")
	}
}

// Empty GhBin resolves gh via LookPath: prepend the fake dir to PATH.
func TestPRForBranchLookPathDefault(t *testing.T) {
	bin, argsLog := fakeGh(t, `[]`, 0)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := &Client{}

	if _, err := c.PRForBranch(context.Background(), "acme/nori", "b"); err != nil {
		t.Fatalf("PRForBranch: %v", err)
	}
	if args := loggedArgs(t, argsLog); !strings.HasPrefix(args, "pr list ") {
		t.Errorf("fake gh not invoked via PATH, args log: %q", args)
	}
}

// End-to-end rollup shapes through the fake gh: ChecksState derivation from
// the two statusCheckRollup entry types.
func TestPRForBranchChecksStateVariants(t *testing.T) {
	mk := func(rollup string) string {
		return `[{"number":1,"url":"u","state":"OPEN","isDraft":false,"mergeable":"UNKNOWN","reviewDecision":"","statusCheckRollup":` + rollup + `}]`
	}
	cases := []struct {
		name   string
		rollup string
		want   string
	}{
		{"empty rollup", `[]`, "none"},
		{"null rollup", `null`, "none"},
		{"all success", `[{"status":"COMPLETED","conclusion":"SUCCESS"},{"state":"SUCCESS"}]`, "pass"},
		{"neutral and skipped are success-ish", `[{"status":"COMPLETED","conclusion":"NEUTRAL"},{"status":"COMPLETED","conclusion":"SKIPPED"}]`, "pass"},
		{"checkrun failure wins over success", `[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]`, "fail"},
		{"statuscontext error is fail", `[{"state":"ERROR"}]`, "fail"},
		{"fail outranks pending", `[{"status":"IN_PROGRESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]`, "fail"},
		{"in-progress checkrun pending", `[{"status":"IN_PROGRESS"},{"status":"COMPLETED","conclusion":"SUCCESS"}]`, "pending"},
		{"queued checkrun pending", `[{"status":"QUEUED"}]`, "pending"},
		{"statuscontext pending", `[{"state":"PENDING"},{"state":"SUCCESS"}]`, "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, _ := fakeGh(t, mk(tc.rollup), 0)
			c := &Client{GhBin: bin}
			pr, err := c.PRForBranch(context.Background(), "acme/nori", "b")
			if err != nil {
				t.Fatalf("PRForBranch: %v", err)
			}
			if pr.ChecksState != tc.want {
				t.Errorf("ChecksState = %q, want %q", pr.ChecksState, tc.want)
			}
		})
	}
}

// Extra failure conclusions beyond the two the spec names: terminal-bad
// CheckRun conclusions must never read as pass.
func TestChecksStateTerminalBadConclusions(t *testing.T) {
	for _, conc := range []string{"TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE"} {
		got := checksState([]rollupEntry{{Status: "COMPLETED", Conclusion: conc}})
		if got != "fail" {
			t.Errorf("checksState(%s) = %q, want fail", conc, got)
		}
	}
}

func TestDeriveStatus(t *testing.T) {
	open := func(draft bool, review, checks string) *PR {
		return &PR{State: "OPEN", IsDraft: draft, ReviewDecision: review, ChecksState: checks}
	}
	conflicting := func(draft bool, review, checks string) *PR {
		pr := open(draft, review, checks)
		pr.Mergeable = "CONFLICTING"
		return pr
	}
	cases := []struct {
		name  string
		alive bool
		pr    *PR
		want  string
	}{
		{"no PR, session alive", true, nil, "working"},
		{"no PR, session dead", false, nil, "no_pr"},
		{"merged wins over everything", false, &PR{State: "MERGED", IsDraft: true, ChecksState: "fail"}, "merged"},
		{"closed wins over draft/checks", true, &PR{State: "CLOSED", IsDraft: true, ChecksState: "fail"}, "closed"},
		{"draft wins over failing checks", true, &PR{State: "OPEN", IsDraft: true, ChecksState: "fail"}, "draft"},
		{"ci failed wins over changes requested", true, open(false, "CHANGES_REQUESTED", "fail"), "ci_failed"},
		{"changes requested", true, open(false, "CHANGES_REQUESTED", "pass"), "changes_requested"},
		{"changes requested with pending checks", true, open(false, "CHANGES_REQUESTED", "pending"), "changes_requested"},
		{"approved and green", true, open(false, "APPROVED", "pass"), "approved"},
		{"approved but checks pending", true, open(false, "APPROVED", "pending"), "ci_pending"},
		{"approved but no checks", true, open(false, "APPROVED", "none"), "review_pending"},
		{"ci pending, review required", true, open(false, "REVIEW_REQUIRED", "pending"), "ci_pending"},
		{"green, review required", true, open(false, "REVIEW_REQUIRED", "pass"), "review_pending"},
		{"green, no review requirement", true, open(false, "", "pass"), "review_pending"},
		{"no checks, no review decision", false, open(false, "", "none"), "review_pending"},
		{"merge conflict", true, conflicting(false, "", "pass"), "merge_conflict"},
		{"merge conflict with pending checks", true, conflicting(false, "", "pending"), "merge_conflict"},
		{"ci failed wins over merge conflict", true, conflicting(false, "", "fail"), "ci_failed"},
		{"merge conflict wins over changes requested", true, conflicting(false, "CHANGES_REQUESTED", "pass"), "merge_conflict"},
		{"conflicting PR must never read approved", true, conflicting(false, "APPROVED", "pass"), "merge_conflict"},
		{"draft wins over merge conflict", true, conflicting(true, "", "pass"), "draft"},
		{"merged wins over merge conflict", false, &PR{State: "MERGED", Mergeable: "CONFLICTING"}, "merged"},
		{"mergeable unknown is not a conflict", true, &PR{State: "OPEN", Mergeable: "UNKNOWN", ChecksState: "pass"}, "review_pending"},
		{"mergeable clean is not a conflict", true, &PR{State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pass"}, "review_pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveStatus(tc.alive, tc.pr); got != tc.want {
				t.Errorf("DeriveStatus(%v, %+v) = %q, want %q", tc.alive, tc.pr, got, tc.want)
			}
		})
	}
}
