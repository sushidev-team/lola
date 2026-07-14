package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validPoll() Poll {
	return Poll{
		Name:              "frontend",
		Enabled:           true,
		TeamID:            "team-uuid",
		ProjectID:         "proj-uuid",
		CycleMode:         "active",
		StateIDs:          []string{"state-1", "state-2"},
		MatchLabels:       []string{"label-1"},
		MatchMode:         "any",
		AssigneeMode:      "me",
		AOProject:         "frontend",
		ConcurrencyCap:    2,
		PrioritySort:      []string{"priority", "createdAt"},
		DedupMode:         "label",
		OnSentSetLabel:    "label-sent",
		OnSentRemoveLabel: "label-1",
	}
}

func TestHomeEnvOverride(t *testing.T) {
	t.Setenv("AOP_HOME", "/tmp/aop-test-home")
	h, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if h != "/tmp/aop-test-home" {
		t.Fatalf("Home() = %q, want AOP_HOME override", h)
	}

	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/tmp/aop-test-home", "config.toml") {
		t.Fatalf("DefaultPath() = %q", p)
	}

	t.Setenv("AOP_HOME", "")
	h, err = Home()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(h, string(filepath.Separator)+".aop") {
		t.Fatalf("Home() without AOP_HOME = %q, want ~/.aop", h)
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
	if !reflect.DeepEqual(c.AO.CountingStates, DefaultCountingStates) {
		t.Errorf("counting_states = %v, want default %v", c.AO.CountingStates, DefaultCountingStates)
	}
}

// Omitted [ao].counting_states must default to working/in_progress —
// otherwise liveCounted is always 0 and the global cap never binds.
func TestLoadDefaultsCountingStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n\n[ao]\nbin = \"/usr/local/bin/ao\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.AO.CountingStates, []string{"working", "in_progress"}) {
		t.Errorf("counting_states = %v, want [working in_progress]", c.AO.CountingStates)
	}

	// An explicit list is kept as-is.
	body += "counting_states = [\"working\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.AO.CountingStates, []string{"working"}) {
		t.Errorf("explicit counting_states = %v, want [working]", c.AO.CountingStates)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.toml")

	orig := &Config{}
	orig.Defaults.PollInterval = 90 * time.Second
	orig.Defaults.ConcurrencyCap = 3
	orig.Defaults.GlobalCap = 5
	orig.Linear = LinearConfig{APIKeyKeychain: "aop-linear", APIKeyEnv: "LINEAR_API_KEY", Endpoint: DefaultEndpoint}
	orig.AO = AOConfig{Bin: "/opt/homebrew/bin/ao", ConfigPath: "/etc/agent-orchestrator.yaml", CountingStates: []string{"working", "in_progress"}}
	orig.Polls = []Poll{validPoll()}

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

func TestLoadClampAndTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
poll_interval = "10s"
concurrency_cap = 1
global_cap = 4

[ao]
bin = "~/bin/ao"
config_path = "~/ao/agent-orchestrator.yaml"
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
	if want := filepath.Join(home, "bin", "ao"); c.AO.Bin != want {
		t.Errorf("ao.bin = %q, want %q", c.AO.Bin, want)
	}
	if want := filepath.Join(home, "ao", "agent-orchestrator.yaml"); c.AO.ConfigPath != want {
		t.Errorf("ao.config_path = %q, want %q", c.AO.ConfigPath, want)
	}
	if c.Linear.Endpoint != DefaultEndpoint {
		t.Errorf("endpoint = %q, want default", c.Linear.Endpoint)
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

		{"label mode needs set label", func(c *Config) { c.Polls[0].OnSentSetLabel = "" }, "on_sent_set_label"},
		{"label mode needs remove label", func(c *Config) { c.Polls[0].OnSentRemoveLabel = "" }, "on_sent_remove_label"},
		{"label mode needs match labels", func(c *Config) { c.Polls[0].MatchLabels = nil }, "requires match_labels"},
		{"label mode remove must be a match label", func(c *Config) { c.Polls[0].OnSentRemoveLabel = "label-unrelated" }, "on_sent_remove_label must be one of match_labels"},
		{"label mode any with multiple match labels rejected", func(c *Config) {
			c.Polls[0].MatchMode = "any"
			c.Polls[0].MatchLabels = []string{"label-1", "label-2"}
		}, "exactly one match label"},
		{"label mode all with multiple match labels ok", func(c *Config) {
			c.Polls[0].MatchMode = "all"
			c.Polls[0].MatchLabels = []string{"label-1", "label-2"}
		}, ""},
		{"seen mode multiple any labels ok", func(c *Config) {
			c.Polls[0].DedupMode = "seen"
			c.Polls[0].OnSentSetLabel = ""
			c.Polls[0].OnSentRemoveLabel = ""
			c.Polls[0].MatchMode = "any"
			c.Polls[0].MatchLabels = []string{"label-1", "label-2"}
		}, ""},
		{"seen mode needs no labels", func(c *Config) {
			c.Polls[0].DedupMode = "seen"
			c.Polls[0].OnSentSetLabel = ""
			c.Polls[0].OnSentRemoveLabel = ""
		}, ""},
		{"bad dedup_mode enum", func(c *Config) { c.Polls[0].DedupMode = "both" }, "dedup_mode"},
		{"empty dedup_mode rejected", func(c *Config) { c.Polls[0].DedupMode = "" }, "dedup_mode"},

		{"cap zero without default", func(c *Config) { c.Polls[0].ConcurrencyCap = 0 }, "concurrency_cap"},
		{"cap negative without default", func(c *Config) { c.Polls[0].ConcurrencyCap = -2 }, "concurrency_cap"},
		{"cap zero with default ok", func(c *Config) { c.Polls[0].ConcurrencyCap = 0; c.Defaults.ConcurrencyCap = 2 }, ""},
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

func TestAOProjects(t *testing.T) {
	// The fixture has nested keys under each project, a decoy nested
	// "projects:" key inside another block, comments, and quoted names.
	got, err := AOProjects(filepath.Join("testdata", "agent-orchestrator.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"frontend", "backend", "infra"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AOProjects = %v, want %v", got, want)
	}

	if _, err := AOProjects(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing ao config must error")
	}
}

func TestAOProjectsWithoutProjectsBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-orchestrator.yaml")
	if err := os.WriteFile(path, []byte("dashboard:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := AOProjects(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("AOProjects = %v, want none", got)
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
