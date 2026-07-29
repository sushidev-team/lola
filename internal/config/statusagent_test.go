package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadToml(t *testing.T, body string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

const statusAgentBaseToml = `
[defaults]
global_cap = 2

[[project]]
name = "p1"
path = "/tmp/p1"
`

// An absent [statusagent] table resolves to the zero config: disabled, zero
// behavior change, and Save omits the table entirely.
func TestStatusAgentAbsentIsDisabled(t *testing.T) {
	c := loadToml(t, statusAgentBaseToml)
	if c.StatusAgent != (StatusAgentConfig{}) {
		t.Fatalf("absent table = %+v, want zero", c.StatusAgent)
	}
	out := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Save(out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), "statusagent") {
		t.Errorf("Save wrote a [statusagent] table for the zero config:\n%s", data)
	}
}

// `enabled = true` alone resolves the full recommended defaults.
func TestStatusAgentEnabledAloneResolvesDefaults(t *testing.T) {
	c := loadToml(t, statusAgentBaseToml+"\n[statusagent]\nenabled = true\n")
	sa := c.StatusAgent
	if !sa.Enabled || sa.Model != DefaultStatusAgentModel ||
		sa.TimeoutSeconds != DefaultStatusAgentTimeoutSeconds ||
		sa.MinIntervalSeconds != DefaultStatusAgentMinIntervalSeconds ||
		sa.MaxPerCycle != DefaultStatusAgentMaxPerCycle ||
		sa.MinConfidence != DefaultStatusAgentMinConfidence ||
		sa.IncludeTranscript {
		t.Fatalf("enabled-alone = %+v, want defaults (model sonnet, 60s, 120s, 2, 0.5, transcript off)", sa)
	}
}

// Explicit zeros survive a save/load round-trip: model = "" (claude's own
// default, distinct from the unset-key "sonnet") and confidence 0.
func TestStatusAgentRoundTripPreservesExplicitZeros(t *testing.T) {
	c := loadToml(t, statusAgentBaseToml+`
[statusagent]
enabled = true
bin = "/opt/bin/claude"
model = ""
timeout_seconds = 30
min_interval_seconds = 0
max_per_cycle = 5
min_confidence = 0.0
include_transcript = true
`)
	sa := c.StatusAgent
	if sa.Model != "" || sa.Bin != "/opt/bin/claude" || sa.MinIntervalSeconds != 0 ||
		sa.MinConfidence != 0 || !sa.IncludeTranscript || sa.TimeoutSeconds != 30 || sa.MaxPerCycle != 5 {
		t.Fatalf("explicit values mangled on load: %+v", sa)
	}
	out := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Save(out); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if c2.StatusAgent != sa {
		t.Fatalf("round-trip changed the table:\nbefore %+v\nafter  %+v", sa, c2.StatusAgent)
	}
}

func TestStatusAgentValidation(t *testing.T) {
	bad := []string{
		"[statusagent]\ntimeout_seconds = -1\n",
		"[statusagent]\nmin_interval_seconds = -5\n",
		"[statusagent]\nmax_per_cycle = -1\n",
		"[statusagent]\nmin_confidence = 1.5\n",
		"[statusagent]\nmin_confidence = -0.1\n",
	}
	for _, b := range bad {
		c := loadToml(t, statusAgentBaseToml+b)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "statusagent") {
			t.Errorf("Validate(%q) = %v, want statusagent error", b, err)
		}
	}
	good := loadToml(t, statusAgentBaseToml+"[statusagent]\nenabled = true\n")
	if err := good.Validate(); err != nil {
		t.Errorf("Validate(enabled) = %v, want nil", err)
	}
}
