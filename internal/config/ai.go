package config

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
)

// AI profiles are opt-in per backend. Absent entries preserve legacy action
// settings; the settings provider selector only edits these independent entries.
type AIModel struct {
	Model  string `toml:"model" json:"model"`
	Effort string `toml:"effort,omitempty" json:"effort"`
}
type AIProvider struct {
	Enabled  bool               `toml:"enabled" json:"enabled"`
	Profiles map[string]AIModel `toml:"profiles" json:"profiles"`
	Actions  map[string]string  `toml:"actions" json:"actions"`
}

func DefaultAIProvider(provider string) AIProvider {
	p := AIProvider{Profiles: map[string]AIModel{"fast": {}, "balanced": {}, "thorough": {}}, Actions: map[string]string{"draft": "fast", "resolve": "thorough", "summary": "balanced", "plan": "thorough"}}
	if provider == "claude" {
		p.Profiles = map[string]AIModel{"fast": {Model: "haiku"}, "balanced": {Model: "sonnet"}, "thorough": {Model: "opus"}}
	}
	return p
}

func (c *Config) AIProfile(provider, action string) (AIModel, bool) {
	p, ok := c.AI[provider]
	if !ok || !p.Enabled {
		return AIModel{}, false
	}
	defaults := DefaultAIProvider(provider)
	name := p.Actions[action]
	if name == "" {
		name = defaults.Actions[action]
	}
	if name == "" {
		return AIModel{}, false
	}
	model, ok := p.Profiles[name]
	if !ok {
		model = defaults.Profiles[name]
	}
	return model, true
}

// CommitInstructionsFor leaves repository style automatic when no preference is set.
// A project preference replaces the workspace preference, like AgentForProject.
func (c *Config) CommitInstructionsFor(project string) string {
	if p := c.ProjectByName(project); p != nil && strings.TrimSpace(p.CommitInstructions) != "" {
		return p.CommitInstructions
	}
	return c.Defaults.CommitInstructions
}

func (c *Config) validateAI() []error {
	var errs []error
	if len(c.Defaults.CommitInstructions) > 4000 {
		errs = append(errs, fmt.Errorf("defaults.commit_instructions must be at most 4000 bytes"))
	}
	for _, p := range c.Projects {
		if len(p.CommitInstructions) > 4000 {
			errs = append(errs, fmt.Errorf("project %s: commit_instructions must be at most 4000 bytes", p.Name))
		}
	}
	for provider, p := range c.AI {
		if !agent.Valid(provider) {
			errs = append(errs, fmt.Errorf("ai: unknown provider %q", provider))
			continue
		}
		defaults := DefaultAIProvider(provider)
		for name, model := range p.Profiles {
			if _, ok := defaults.Profiles[name]; !ok {
				errs = append(errs, fmt.Errorf("ai.%s: unknown profile %q", provider, name))
			}
			if model.Model != "" && !agent.ValidModel(model.Model) {
				errs = append(errs, fmt.Errorf("ai.%s.%s: invalid model", provider, name))
			}
			if !agent.ValidEffort(agent.Parse(provider), model.Effort) {
				errs = append(errs, fmt.Errorf("ai.%s.%s: invalid effort", provider, name))
			}
		}
		for action, profile := range p.Actions {
			if _, ok := defaults.Actions[action]; !ok {
				errs = append(errs, fmt.Errorf("ai.%s: unknown action %q", provider, action))
			}
			if _, ok := defaults.Profiles[profile]; !ok {
				errs = append(errs, fmt.Errorf("ai.%s.%s: unknown profile %q", provider, action, profile))
			}
		}
	}
	return errs
}
