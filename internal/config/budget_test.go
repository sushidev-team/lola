package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBudgetAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 3

[budget]
daily_tokens = 250_000_000
notify = true

[load]
max_load_per_cpu = 1.5
min_free_memory_percent = 10

[[project]]
name = "p"
path = "/tmp/p"
daily_budget_tokens = 7_000_000
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Budget != (BudgetConfig{DailyTokens: 250_000_000, Notify: true}) || c.Load != (LoadConfig{MaxLoadPerCPU: 1.5, MinFreeMemoryPercent: 10}) {
		t.Fatalf("loaded budget=%+v load=%+v", c.Budget, c.Load)
	}
	if got := c.ProjectBudget("p"); got != 7_000_000 {
		t.Fatalf("project budget = %v", got)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Budget != c.Budget || back.Load != c.Load || back.ProjectBudget("p") != 7_000_000 {
		t.Fatalf("round trip lost values: %+v %+v", back.Budget, back.Load)
	}
}

func TestZeroLimitsAreNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, _ := Load(path)
	c.Defaults.GlobalCap = 1
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if s := string(data); strings.Contains(s, "[budget]") || strings.Contains(s, "[load]") || strings.Contains(s, "daily_budget_tokens") {
		t.Fatalf("a fresh config must persist no limit tables:\n%s", s)
	}
}

func TestValidateLimits(t *testing.T) {
	c := &Config{Defaults: Defaults{GlobalCap: 1}}
	c.Budget.DailyTokens = -1
	c.Load.MinFreeMemoryPercent = 100
	c.Projects = []Project{{Name: "p", Path: "/p", DailyBudgetTokens: -2}}
	err := c.Validate()
	for _, want := range []string{"budget.daily_tokens", "min_free_memory_percent", "daily_budget_tokens"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("validate = %v, want it to mention %s", err, want)
		}
	}
}
