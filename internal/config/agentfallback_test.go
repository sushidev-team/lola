package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The fallback chain resolves project → [defaults] → none, and a
// present-but-empty project key is a deliberate "no fallback here" override.
func TestFallbackChainForResolution(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
agent = "claude"
agent_fallback = ["codex", "opencode"]

[[project]]
name = "web"
path = "/tmp/web"

[[project]]
name = "api"
path = "/tmp/api"
agent_fallback = ["opencode"]

[[project]]
name = "cli"
path = "/tmp/cli"
agent_fallback = []
`)
	if got := c.FallbackChainFor("web"); !reflect.DeepEqual(got, []string{"codex", "opencode"}) {
		t.Errorf("web chain = %v, want the inherited [codex opencode]", got)
	}
	if got := c.FallbackChainFor("api"); !reflect.DeepEqual(got, []string{"opencode"}) {
		t.Errorf("api chain = %v, want the project override [opencode]", got)
	}
	if got := c.FallbackChainFor("cli"); len(got) != 0 {
		t.Errorf("cli chain = %v, want empty (override to nothing)", got)
	}
	if got := c.FallbackChainFor("nope"); !reflect.DeepEqual(got, []string{"codex", "opencode"}) {
		t.Errorf("unknown project chain = %v, want the [defaults] value", got)
	}
	if !c.ProjectByName("web").Inherits.AgentFallback {
		t.Error("web omits the key and must be marked inheriting")
	}
	if c.ProjectByName("api").Inherits.AgentFallback || c.ProjectByName("cli").Inherits.AgentFallback {
		t.Error("api/cli set the key and must not be marked inheriting")
	}
}

// An inherited chain is never frozen into the project's table on Save; an
// override is written and reloads to the same effective value.
func TestAgentFallbackSaveOmitsInheritedKey(t *testing.T) {
	c, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
agent_fallback = ["codex"]

[[project]]
name = "web"
path = "/tmp/web"

[[project]]
name = "api"
path = "/tmp/api"
agent_fallback = ["opencode"]
`)
	if err := c.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "[[project]]")
	if len(parts) != 3 {
		t.Fatalf("expected 2 project tables in saved file, got:\n%s", data)
	}
	if strings.Contains(parts[1], "agent_fallback") {
		t.Errorf("an inheriting project must not persist agent_fallback:\n%s", parts[1])
	}
	if !strings.Contains(parts[2], `agent_fallback = ["opencode"]`) {
		t.Errorf("an overriding project must persist its chain:\n%s", parts[2])
	}

	c2, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := c2.FallbackChainFor("web"); !reflect.DeepEqual(got, []string{"codex"}) {
		t.Errorf("after reload web chain = %v, want [codex]", got)
	}
	if got := c2.FallbackChainFor("api"); !reflect.DeepEqual(got, []string{"opencode"}) {
		t.Errorf("after reload api chain = %v, want [opencode]", got)
	}
}

// Unknown kinds and duplicates are rejected, on [defaults] and on a project's
// own chain; an inherited chain is reported once (against [defaults]).
func TestValidateAgentFallback(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
agent_fallback = ["codex", "codex", "nope"]

[[project]]
name = "web"
path = "/tmp/web"
agent_fallback = ["bogus"]

[[project]]
name = "api"
path = "/tmp/api"
`)
	err := c.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	msg := err.Error()
	for _, want := range []string{"duplicates", `"nope"`, `"bogus"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("validation error missing %q: %v", want, msg)
		}
	}
	if strings.Contains(msg, `"api"`) && strings.Contains(msg, "agent_fallback") && strings.Contains(msg, `"api".agent_fallback`) {
		t.Errorf("api inherits the chain and must not re-report it: %v", msg)
	}
}

// [reactions.agent_fallback] defaults to notify-only; an explicit auto = true
// loads and round-trips.
func TestReactionsAgentFallback(t *testing.T) {
	c, _ := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2
`)
	if c.Reactions.AgentFallback.Auto {
		t.Error("agent_fallback auto must default to false (notify-only)")
	}

	c2, path := writeCfg(t, `
[defaults]
global_cap = 4
concurrency_cap = 2

[reactions.agent_fallback]
auto = true
`)
	if !c2.Reactions.AgentFallback.Auto {
		t.Fatal("agent_fallback auto = true must load")
	}
	if err := c2.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	c3, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !c3.Reactions.AgentFallback.Auto {
		t.Error("agent_fallback auto = true must survive a save/load round-trip")
	}
}
