package runtime

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/portlease"
)

func leaseRegistry(t *testing.T) *portlease.Registry {
	t.Helper()
	r, err := portlease.Open(filepath.Join(t.TempDir(), "ports.json"))
	if err != nil {
		t.Fatal(err)
	}
	r.InUse = func(int) bool { return false }
	return r
}

func TestDBNameIsAStableIdentifier(t *testing.T) {
	if got := DBName("lola-nori-eng-42"); got != "lola_nori_eng_42" {
		t.Errorf("DBName = %q", got)
	}
	if got := DBName("42-x"); got != "lola_42_x" {
		t.Errorf("leading digit: %q", got)
	}
	long := "lola-" + strings.Repeat("a", 80)
	a, b := DBName(long), DBName(long+"b")
	if len(a) != dbNameMax || len(b) != dbNameMax || a == b {
		t.Errorf("long names must cap at %d and stay distinct: %q / %q", dbNameMax, a, b)
	}
	if DBName(long) != a {
		t.Error("DBName must be deterministic")
	}
}

func TestWithSessionEnvInjectsLolaVarsAndPlaceholders(t *testing.T) {
	p := config.Project{Name: "nori", Env: map[string]string{
		"APP_PORT":  "{{.Port}}",
		"VITE_PORT": "{{.Port2}}",
		"DB":        "{{.DBName}}",
		"HOST":      "{{.Slug}}.test",
		"LOLA_PORT": "8000", // a pinned value would collide; lola's wins
	}}
	got := withSessionEnv(p, EnvVars{Session: "lola-nori-eng-42", Ports: []int{4100, 4101}}).Env
	want := map[string]string{
		"APP_PORT": "4100", "VITE_PORT": "4101", "DB": "lola_nori_eng_42", "HOST": "lola-nori-eng-42.test",
		"LOLA_PORT": "4100", "LOLA_PORT_1": "4100", "LOLA_PORT_2": "4101",
		"LOLA_DB_NAME": "lola_nori_eng_42", "LOLA_SESSION_SLUG": "lola-nori-eng-42",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	if p.Env["LOLA_PORT"] != "8000" {
		t.Error("the caller's Env map was mutated")
	}

	// No lease: no port vars, and port placeholders render empty.
	bare := withSessionEnv(p, EnvVars{Session: "lola-nori-eng-42"}).Env
	if _, ok := bare["LOLA_PORT_1"]; ok || bare["APP_PORT"] != "" || bare["LOLA_PORT"] != "8000" {
		t.Errorf("without a lease: %v", bare)
	}
}

func TestSpawnLeasesPortsIntoEnvAndPostCreate(t *testing.T) {
	f := newFixture(t, "", "")
	f.n.Ports = leaseRegistry(t)
	f.p.PortRange = "4100-4199"
	f.p.PortsPerSession = 2
	f.p.PostCreate = []string{`printf '%s %s %s' "$LOLA_PORT" "$LOLA_PORT_2" "$LOLA_DB_NAME" > seen`}

	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	seen, err := os.ReadFile(filepath.Join(s.Worktree, "seen"))
	if err != nil {
		t.Fatal(err)
	}
	if string(seen) != "4100 4101 lola_nori_eng_42" {
		t.Errorf("post_create saw %q", seen)
	}
	env, err := os.ReadFile(filepath.Join(s.Worktree, ".lola", "env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"LOLA_PORT=4100\n", "LOLA_PORT_2=4101\n", "LOLA_DB_NAME=lola_nori_eng_42\n"} {
		if !strings.Contains(string(env), line) {
			t.Errorf(".lola/env missing %s:\n%s", line, env)
		}
	}
	if l, ok := f.n.Ports.Get(s.ID); !ok || !slices.Equal(l.Ports, []int{4100, 4101}) {
		t.Errorf("lease = %+v, %v", l, ok)
	}
}

func TestSpawnRollbackReleasesTheLease(t *testing.T) {
	f := newFixture(t, "", "")
	f.n.Ports = leaseRegistry(t)
	f.p.PortRange = "4100-4199"
	f.p.PostCreate = []string{"exit 3"}
	if _, err := f.n.Spawn(context.Background(), f.p, issueENG42(), ""); err == nil {
		t.Fatal("want a prepare failure")
	}
	if all := f.n.Ports.All(); len(all) != 0 {
		t.Errorf("a rolled-back spawn kept its lease: %+v", all)
	}
}

func TestKillRunsPreRemoveWithSessionEnvAndReleases(t *testing.T) {
	f := newFixture(t, "", "")
	f.n.Ports = leaseRegistry(t)
	out := filepath.Join(t.TempDir(), "hook.out")
	p := f.p
	p.PreRemove = []string{
		"exit 7", // a failing hook neither stops the next one nor the teardown
		`printf '%s %s %s' "$PWD" "$LOLA_PORT" "$LOLA_DB_NAME" > ` + out,
	}
	f.n.Cfg = &config.Config{Projects: []config.Project{p}}
	var logs []string
	f.n.Logf = func(format string, args ...any) { logs = append(logs, format) }

	s := killFixtureSession()
	if _, err := f.n.Ports.Acquire(s.ID, "nori", 4100, 4199, 1); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.root, "nori", s.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.n.Kill(context.Background(), s, true, false); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("pre_remove did not run: %v", err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if string(got) != real+" 4100 lola_nori_eng_42" && string(got) != dir+" 4100 lola_nori_eng_42" {
		t.Errorf("pre_remove saw %q", got)
	}
	if !slices.ContainsFunc(logs, func(l string) bool { return strings.Contains(l, "pre_remove") }) {
		t.Errorf("a failing pre_remove command must be logged: %v", logs)
	}
	if _, ok := f.n.Ports.Get(s.ID); ok {
		t.Error("the lease must be released once the worktree is gone")
	}
}

func TestKillSkipsPreRemoveForADirtyWorktree(t *testing.T) {
	f := newFixture(t, `*"status --porcelain"*)
  echo " M main.go"
  exit 0
  ;;`, "")
	f.n.Ports = leaseRegistry(t)
	out := filepath.Join(t.TempDir(), "hook.out")
	p := f.p
	p.PreRemove = []string{"touch " + out}
	f.n.Cfg = &config.Config{Projects: []config.Project{p}}

	s := killFixtureSession()
	if _, err := f.n.Ports.Acquire(s.ID, "nori", 4100, 4199, 1); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "nori", s.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.n.Kill(context.Background(), s, true, false); err == nil {
		t.Fatal("a dirty worktree must be refused")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("pre_remove ran for a worktree that is being KEPT")
	}
	if _, ok := f.n.Ports.Get(s.ID); !ok {
		t.Error("a kept worktree keeps its lease")
	}
}
