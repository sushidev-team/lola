package doctor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// tripped writes a plausible breaker report and returns it.
func tripped(t *testing.T, reason string) StatusAgentBreaker {
	t.Helper()
	b := StatusAgentBreaker{
		Failures:  5,
		Reason:    reason,
		TrippedAt: time.Now().Add(-time.Minute),
		RetryAt:   time.Now().Add(29 * time.Minute),
	}
	if err := ReportStatusAgentBreaker(b); err != nil {
		t.Fatalf("report: %v", err)
	}
	return b
}

// A reported breaker reads back and becomes a non-critical warning naming the
// reason — the whole point: the tripped interpreter stops being silent.
func TestStatusAgentBreakerReportsAsAWarning(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	tripped(t, "statusagent: interpreter exited nonzero (exit status 1): You must run `claude` to review the updated terms.")

	got, ok := StatusAgentBreakerReport()
	if !ok {
		t.Fatal("a report just written did not read back")
	}
	if got.PID != os.Getpid() {
		t.Fatalf("pid %d, want the writer's %d", got.PID, os.Getpid())
	}

	res, ok := statusAgentResult()
	if !ok {
		t.Fatal("no result for a tripped breaker")
	}
	if res.OK || res.Critical {
		t.Fatalf("want a non-critical warning, got OK=%v Critical=%v", res.OK, res.Critical)
	}
	for _, want := range []string{"disabled after 5 consecutive failures", "updated terms", "re-arms"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("detail %q missing %q", res.Detail, want)
		}
	}
}

// Nothing to report: no file at all, a cleared file, and a file whose writing
// daemon is gone all add no row — this check may only ever surface a warning a
// LIVE daemon put there on purpose.
func TestStatusAgentBreakerSilentWhenNotTripped(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())

	if _, ok := statusAgentResult(); ok {
		t.Fatal("a fresh home reported a breaker")
	}
	// Clearing an absent breadcrumb is success, not an error.
	if err := ClearStatusAgentBreaker(); err != nil {
		t.Fatalf("clear on absent file: %v", err)
	}

	tripped(t, "boom")
	if _, ok := statusAgentResult(); !ok {
		t.Fatal("tripped breaker not reported")
	}
	if err := ClearStatusAgentBreaker(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok := statusAgentResult(); ok {
		t.Fatal("cleared breaker still reported")
	}

	// A daemon that died while tripped must not make the next one look broken.
	tripped(t, "boom")
	orig := processAlive
	t.Cleanup(func() { processAlive = orig })
	processAlive = func(int) bool { return false }
	if _, ok := statusAgentResult(); ok {
		t.Fatal("a breaker from a dead daemon must be ignored")
	}
}

// Garbage in the breadcrumb is ignored rather than reported: the file is
// derived state, so an unreadable one may only cost a warning.
func TestStatusAgentBreakerIgnoresGarbage(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	tripped(t, "boom")
	path, err := breakerPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := statusAgentResult(); ok {
		t.Fatal("unparsable breadcrumb reported")
	}
	// A report with no trip time is not a tripped breaker either.
	if err := ReportStatusAgentBreaker(StatusAgentBreaker{Failures: 5, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := statusAgentResult(); ok {
		t.Fatal("a report with no trip time reported")
	}
}

// The row rides the full Report, which is what `lola doctor` and the app's
// overlay actually render — and only when there is something to say.
func TestCheckCarriesTheStatusAgentRow(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	pathWith(t, "tmux", "git", "claude") // the critical trio, so OK() is about us

	has := func(rep Report) bool {
		for _, res := range rep.Results {
			if res.Name == checkStatusAgent {
				return true
			}
		}
		return false
	}
	if has(Check(context.Background(), nil)) {
		t.Fatal("healthy machine grew a status-interpreter row")
	}
	tripped(t, "boom")
	rep := Check(context.Background(), nil)
	if !has(rep) {
		t.Fatal("tripped breaker missing from the report")
	}
	// Non-critical: a display-only feature must never fail the report.
	if res := result(t, rep, checkStatusAgent); res.Critical {
		t.Fatal("the status-interpreter row must not be critical")
	}
	if !rep.OK() {
		t.Fatalf("a tripped status interpreter must not fail the report: %+v", rep.Results)
	}
}
