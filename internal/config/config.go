// Package config owns ~/.aop/config.toml: schema, defaults, atomic
// persistence, and static validation. All runtime paths derive from Home(),
// which honors the $AOP_HOME override that tests rely on.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultEndpoint is used when [linear].endpoint is unset.
	DefaultEndpoint = "https://api.linear.app/graphql"
	// DefaultPollInterval is used when [defaults].poll_interval is unset.
	DefaultPollInterval = 60 * time.Second
	// MinPollInterval is the Linear rate-limit floor; configured intervals
	// are clamped up to it, never rejected.
	MinPollInterval = 30 * time.Second
)

// DefaultCountingStates is used when [ao].counting_states is unset. Without
// it liveCounted would always be 0 and the global cap would never bind.
var DefaultCountingStates = []string{"working", "in_progress"}

type Poll struct {
	Name              string   `toml:"name"`
	Enabled           bool     `toml:"enabled"`
	TeamID            string   `toml:"team_id"`
	ProjectID         string   `toml:"project_id"`
	CycleMode         string   `toml:"cycle_mode"` // none|active|pinned
	CycleID           string   `toml:"cycle_id"`
	StateIDs          []string `toml:"state_ids"`
	MatchLabels       []string `toml:"match_labels"`
	MatchMode         string   `toml:"match_mode"`    // any|all
	AssigneeMode      string   `toml:"assignee_mode"` // anyone|me|user
	AssigneeUserID    string   `toml:"assignee_user_id"`
	AOProject         string   `toml:"ao_project"`
	ConcurrencyCap    int      `toml:"concurrency_cap"`
	PrioritySort      []string `toml:"priority_sort"`
	DedupMode         string   `toml:"dedup_mode"` // label|seen
	OnSentSetLabel    string   `toml:"on_sent_set_label"`
	OnSentRemoveLabel string   `toml:"on_sent_remove_label"`
}

// Defaults is the [defaults] table. PollInterval is a plain time.Duration in
// memory; on disk it is a string like "60s" (see fileDefaults/Duration).
type Defaults struct {
	PollInterval   time.Duration `toml:"-"`
	ConcurrencyCap int           `toml:"concurrency_cap"`
	GlobalCap      int           `toml:"global_cap"`
}

// LinearConfig is the [linear] table. It intentionally has no api_key field:
// secrets never live in config.toml.
type LinearConfig struct {
	APIKeyKeychain string `toml:"api_key_keychain"`
	APIKeyEnv      string `toml:"api_key_env"`
	Endpoint       string `toml:"endpoint"`
}

// AOConfig is the [ao] table.
type AOConfig struct {
	Bin            string   `toml:"bin"`
	ConfigPath     string   `toml:"config_path"`
	CountingStates []string `toml:"counting_states"`
}

type Config struct {
	Defaults Defaults     `toml:"defaults"`
	Linear   LinearConfig `toml:"linear"`
	AO       AOConfig     `toml:"ao"`
	Polls    []Poll       `toml:"poll"`
}

// Duration is a time.Duration that TOML-round-trips as a Go duration string
// (e.g. "60s"); BurntSushi/toml has no native duration type.
type Duration time.Duration

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// fileConfig / fileDefaults mirror Config for (de)serialization only, so
// Config can expose PollInterval as a plain time.Duration.
type fileConfig struct {
	Defaults fileDefaults `toml:"defaults"`
	Linear   LinearConfig `toml:"linear"`
	AO       AOConfig     `toml:"ao"`
	Polls    []Poll       `toml:"poll"`
}

type fileDefaults struct {
	PollInterval   Duration `toml:"poll_interval"`
	ConcurrencyCap int      `toml:"concurrency_cap"`
	GlobalCap      int      `toml:"global_cap"`
}

func (fc *fileConfig) config() *Config {
	return &Config{
		Defaults: Defaults{
			PollInterval:   time.Duration(fc.Defaults.PollInterval),
			ConcurrencyCap: fc.Defaults.ConcurrencyCap,
			GlobalCap:      fc.Defaults.GlobalCap,
		},
		Linear: fc.Linear,
		AO:     fc.AO,
		Polls:  fc.Polls,
	}
}

func (c *Config) file() *fileConfig {
	return &fileConfig{
		Defaults: fileDefaults{
			PollInterval:   Duration(c.Defaults.PollInterval),
			ConcurrencyCap: c.Defaults.ConcurrencyCap,
			GlobalCap:      c.Defaults.GlobalCap,
		},
		Linear: c.Linear,
		AO:     c.AO,
		Polls:  c.Polls,
	}
}

// Home returns the aop runtime directory: $AOP_HOME if set, else ~/.aop.
func Home() (string, error) {
	if h := os.Getenv("AOP_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aop"), nil
}

// DefaultPath returns Home()/config.toml.
func DefaultPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config.toml"), nil
}

// Load reads and parses the TOML config at path. A missing file is not an
// error: it yields a zero-value config with defaults applied. Leading ~ in
// [ao].bin and [ao].config_path is expanded; [linear].endpoint and
// [defaults].poll_interval get defaults, and the interval is clamped to
// MinPollInterval.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		c := &Config{}
		c.applyDefaults()
		return c, nil
	}
	if err != nil {
		return nil, err
	}

	var fc fileConfig
	if err := toml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c := fc.config()

	if c.AO.Bin, err = expandTilde(c.AO.Bin); err != nil {
		return nil, fmt.Errorf("expand ao.bin: %w", err)
	}
	if c.AO.ConfigPath, err = expandTilde(c.AO.ConfigPath); err != nil {
		return nil, fmt.Errorf("expand ao.config_path: %w", err)
	}

	c.applyDefaults()
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.Linear.Endpoint == "" {
		c.Linear.Endpoint = DefaultEndpoint
	}
	if c.Defaults.PollInterval == 0 {
		c.Defaults.PollInterval = DefaultPollInterval
	}
	if c.Defaults.PollInterval < MinPollInterval {
		c.Defaults.PollInterval = MinPollInterval
	}
	if len(c.AO.CountingStates) == 0 {
		c.AO.CountingStates = append([]string(nil), DefaultCountingStates...)
	}
}

// Save writes the config atomically: parents are created 0700, the TOML is
// written to a temp file in the destination directory (so the rename cannot
// cross filesystems), then renamed into place with final mode 0600.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := toml.NewEncoder(tmp).Encode(c.file()); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	name := tmp.Name()
	tmp = nil // written and closed; disarm the cleanup deferral
	return os.Rename(name, path)
}

// expandTilde expands a leading "~" or "~/" to the current user's home
// directory. "~user" forms are not supported and pass through unchanged.
func expandTilde(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}
