package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestBestOfRoundTrip(t *testing.T) {
	c, path := writeCfg(t, `
[best_of]
label = "label-bestof"
agents = ["claude:2", "codex@gpt-5-codex"]
`)
	want := BestOfConfig{Label: "label-bestof", Agents: []string{"claude:2", "codex@gpt-5-codex"}}
	if !reflect.DeepEqual(c.BestOf, want) {
		t.Fatalf("loaded %+v, want %+v", c.BestOf, want)
	}
	mix, err := c.BestOfMix()
	if err != nil || len(mix) != 3 || mix[2].Model != "gpt-5-codex" {
		t.Fatalf("BestOfMix = %+v, %v", mix, err)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c2.BestOf, want) {
		t.Fatalf("after save/load %+v, want %+v", c2.BestOf, want)
	}
}

// A config that never mentioned [best_of] must not grow the table on save, and
// its mix is the default.
func TestBestOfAbsent(t *testing.T) {
	c, path := writeCfg(t, "")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "best_of") {
		t.Fatalf("absent table written on save:\n%s", raw)
	}
	mix, err := c.BestOfMix()
	if err != nil || len(mix) != 2 || mix[0].Agent != "claude" {
		t.Fatalf("default mix = %+v, %v", mix, err)
	}
}

func TestBestOfValidate(t *testing.T) {
	c := &Config{BestOf: BestOfConfig{Agents: []string{"gpt:2"}}}
	errs := c.validateBestOf()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "best_of.agents") {
		t.Fatalf("validateBestOf = %v", errs)
	}
	c.BestOf.Agents = []string{"claude", "opencode@anthropic/claude-sonnet-4"}
	if errs := c.validateBestOf(); errs != nil {
		t.Fatalf("valid mix rejected: %v", errs)
	}
}
