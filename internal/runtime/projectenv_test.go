package runtime

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
)

func TestExpandProjectEnvFillsEveryPlaceholder(t *testing.T) {
	p := config.Project{Name: "nori-app", Env: map[string]string{
		"REDIS_QUEUE": "{{{.Session}}}",
		"ISSUE":       "{{.Issue}}",
		"BRANCH":      "{{.Branch}}",
		"PROJECT":     "{{.Project}}",
		"WORKTREE":    "{{.Worktree}}",
	}}

	got := expandProjectEnv(p, EnvVars{
		Session:  "lola-nori-app-nor-366",
		Issue:    "NOR-366",
		Branch:   "feature/nor-366-period-export",
		Project:  "nori-app",
		Worktree: "/wt/nori-app/lola-nori-app-nor-366",
	}).Env

	want := map[string]string{
		"REDIS_QUEUE": "{lola-nori-app-nor-366}",
		"ISSUE":       "NOR-366",
		"BRANCH":      "feature/nor-366-period-export",
		"PROJECT":     "nori-app",
		"WORKTREE":    "/wt/nori-app/lola-nori-app-nor-366",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
}

// The whole point: two worktrees of one project must not resolve to the same
// value, or they keep sharing the backing service the value names.
func TestExpandProjectEnvIsDistinctPerSession(t *testing.T) {
	p := config.Project{Name: "nori-app", Env: map[string]string{"REDIS_QUEUE": "{{{.Session}}}"}}

	a := expandProjectEnv(p, EnvVars{Session: "lola-nori-app-nor-366"}).Env["REDIS_QUEUE"]
	b := expandProjectEnv(p, EnvVars{Session: "lola-nori-app-nor-364"}).Env["REDIS_QUEUE"]

	if a == b {
		t.Fatalf("both sessions resolved to %q", a)
	}
}

// A PR or manual session has no Linear issue. {{.Issue}} renders empty rather
// than leaking the literal placeholder into a sourced shell file.
func TestExpandProjectEnvIssueIsEmptyWithoutAnIssue(t *testing.T) {
	p := config.Project{Name: "x", Env: map[string]string{"Q": "q-{{.Issue}}"}}

	got := expandProjectEnv(p, EnvVars{Session: "lola-x-open-review"}).Env["Q"]

	if got != "q-" {
		t.Errorf("Q = %q, want %q", got, "q-")
	}
}

// Callers pass config.Project by value but the Env map is shared with the
// loaded config; rendering must not write through to it.
func TestExpandProjectEnvDoesNotMutateTheSource(t *testing.T) {
	src := map[string]string{"Q": "{{.Session}}"}
	p := config.Project{Name: "x", Env: src}

	_ = expandProjectEnv(p, EnvVars{Session: "one"})
	out := expandProjectEnv(p, EnvVars{Session: "two"}).Env

	if src["Q"] != "{{.Session}}" {
		t.Errorf("source map was rewritten to %q", src["Q"])
	}
	if out["Q"] != "two" {
		t.Errorf("second render = %q, want two", out["Q"])
	}
}

func TestExpandProjectEnvLeavesPlainValuesAndEmptyMapsAlone(t *testing.T) {
	plain := expandProjectEnv(
		config.Project{Name: "x", Env: map[string]string{"APP_ENV": "local"}},
		EnvVars{Session: "s"},
	).Env
	if plain["APP_ENV"] != "local" {
		t.Errorf("APP_ENV = %q, want local", plain["APP_ENV"])
	}

	if env := expandProjectEnv(config.Project{Name: "x"}, EnvVars{Session: "s"}).Env; env != nil {
		t.Errorf("nil Env became %v", env)
	}
}

// Names are shell-parsed on the left of an assignment, so they are validated
// identifiers and must never be templated — only values are rendered.
func TestExpandProjectEnvNeverRendersNames(t *testing.T) {
	p := config.Project{Name: "x", Env: map[string]string{"Q_{{.Issue}}": "v"}}

	got := expandProjectEnv(p, EnvVars{Issue: "NOR-1"}).Env

	if _, ok := got["Q_{{.Issue}}"]; !ok {
		t.Errorf("name was rewritten: %v", got)
	}
}

// The rendered value is what reaches .lola/env, single-quoted like any other.
func TestEnvFileWritesTheRenderedValue(t *testing.T) {
	n := &Native{}
	p := expandProjectEnv(
		config.Project{Name: "nori-app", Env: map[string]string{"REDIS_QUEUE": "{{{.Session}}}"}},
		EnvVars{Session: "lola-nori-app-nor-366"},
	)

	got := string(n.envFile(p, "lola-nori-app-nor-366", "/wt", agent.Claude))

	if !strings.Contains(got, "REDIS_QUEUE='{lola-nori-app-nor-366}'\n") {
		t.Errorf("envFile did not carry the rendered value:\n%s", got)
	}
}
