package config

import (
	"fmt"

	"github.com/sushidev-team/lola/internal/agent"
)

// The [statusagent] table configures the status INTERPRETER: an opt-in,
// bounded headless agent pass (internal/statusagent) that reads one session's
// observed material (pane tail, recent events, PR facts, optionally the
// agent's transcript tail) and judges what the agent is actually doing — a
// display overlay for the session list, nothing more. Its output is untrusted
// (the material is attacker-influenceable) and reaches ONLY the wire's
// display fields: never Session.Status, never the dispatch budget, never
// reactions/write-back/answer gating, never send-keys.
//
// The table is entirely optional and defaults to DISABLED: an absent
// [statusagent] table means Enabled=false with zero behavior change. The
// daemon owns scheduling (per-session debounce, input-hash skip, per-cycle
// cap, overlay expiry — constants in internal/daemon/statusagentwire.go);
// this package only holds the schema, defaults, and static validation.

const (
	// DefaultStatusAgentModel is the --model passed when [statusagent].model is
	// unset (table present, key omitted): the small/fast tier suits a pass that
	// runs per session on status churn. Other agents use their own default.
	// An explicit model = "" always means the agent's own default model.
	DefaultStatusAgentModel = "sonnet"
	// DefaultStatusAgentTimeoutSeconds caps one interpretation call.
	DefaultStatusAgentTimeoutSeconds = 60
	// DefaultStatusAgentMinIntervalSeconds is the per-session debounce: at most
	// one interpretation attempt per session per this many seconds.
	DefaultStatusAgentMinIntervalSeconds = 120
	// DefaultStatusAgentMaxPerCycle caps how many interpretations one 30s
	// observe cycle may queue, bounding worst-case spend while sessions churn.
	DefaultStatusAgentMaxPerCycle = 2
	// DefaultStatusAgentMinConfidence is the floor under which an
	// interpretation is discarded and the deterministic display stands.
	DefaultStatusAgentMinConfidence = 0.5
)

// StatusAgentConfig is the [statusagent] table.
//
//   - Enabled gates the whole feature; false (the default, and the value for
//     an absent table) means the daemon never execs the interpreter.
//   - Agent selects claude (also the empty default), codex, or opencode.
//   - Bin overrides the selected agent executable; "" resolves it via PATH (launchd
//     installs should pin an absolute path).
//   - Model is passed as --model when non-empty; explicitly "" uses the agent's
//     default model (the unset-key default is "sonnet" for Claude only).
//   - TimeoutSeconds bounds each call; MinIntervalSeconds debounces per
//     session; MaxPerCycle caps queueing per observe cycle; MinConfidence
//     drops low-confidence judgements. All must be >= 0 (confidence in [0,1]).
//   - IncludeTranscript additionally feeds the tail of the agent's own
//     transcript file into the context (more signal, more tokens; default
//     off).
type StatusAgentConfig struct {
	Enabled            bool    `toml:"enabled"`
	Agent              string  `toml:"agent"`
	Bin                string  `toml:"bin"`
	Model              string  `toml:"model"`
	TimeoutSeconds     int     `toml:"timeout_seconds"`
	MinIntervalSeconds int     `toml:"min_interval_seconds"`
	MaxPerCycle        int     `toml:"max_per_cycle"`
	MinConfidence      float64 `toml:"min_confidence"`
	IncludeTranscript  bool    `toml:"include_transcript"`
}

// --- on-disk mirror --------------------------------------------------------
//
// Pointer-per-field (the [brain] pattern) so load can tell an ABSENT key
// (take the default) from an explicit zero the operator wants preserved —
// which matters here: model = "" genuinely means "claude's default model",
// distinct from the unset-key default "sonnet".

type fileStatusAgentConfig struct {
	Enabled            *bool    `toml:"enabled,omitempty"`
	Agent              *string  `toml:"agent,omitempty"`
	Bin                *string  `toml:"bin,omitempty"`
	Model              *string  `toml:"model,omitempty"`
	TimeoutSeconds     *int     `toml:"timeout_seconds,omitempty"`
	MinIntervalSeconds *int     `toml:"min_interval_seconds,omitempty"`
	MaxPerCycle        *int     `toml:"max_per_cycle,omitempty"`
	MinConfidence      *float64 `toml:"min_confidence,omitempty"`
	IncludeTranscript  *bool    `toml:"include_transcript,omitempty"`
}

// resolveStatusAgent materializes the [statusagent] table. A nil (absent)
// mirror yields the zero StatusAgentConfig — disabled, zero behavior change.
// A present table overlays each explicitly-set key onto the defaults, so
// `enabled = true` alone resolves the full recommended configuration.
func resolveStatusAgent(fs *fileStatusAgentConfig) StatusAgentConfig {
	if fs == nil {
		return StatusAgentConfig{}
	}
	c := StatusAgentConfig{
		Model:              DefaultStatusAgentModel,
		TimeoutSeconds:     DefaultStatusAgentTimeoutSeconds,
		MinIntervalSeconds: DefaultStatusAgentMinIntervalSeconds,
		MaxPerCycle:        DefaultStatusAgentMaxPerCycle,
		MinConfidence:      DefaultStatusAgentMinConfidence,
	}
	if fs.Agent != nil {
		c.Agent = *fs.Agent
	}
	if agent.Parse(c.Agent) != agent.Claude {
		c.Model = ""
	}
	if fs.Enabled != nil {
		c.Enabled = *fs.Enabled
	}
	if fs.Bin != nil {
		c.Bin = *fs.Bin
	}
	if fs.Model != nil {
		c.Model = *fs.Model
	}
	if fs.TimeoutSeconds != nil {
		c.TimeoutSeconds = *fs.TimeoutSeconds
	}
	if fs.MinIntervalSeconds != nil {
		c.MinIntervalSeconds = *fs.MinIntervalSeconds
	}
	if fs.MaxPerCycle != nil {
		c.MaxPerCycle = *fs.MaxPerCycle
	}
	if fs.MinConfidence != nil {
		c.MinConfidence = *fs.MinConfidence
	}
	if fs.IncludeTranscript != nil {
		c.IncludeTranscript = *fs.IncludeTranscript
	}
	return c
}

// statusAgentFile builds the on-disk mirror for Save. A zero (unconfigured)
// table returns nil so [statusagent] is omitted entirely; otherwise every
// field is written explicitly so the round-trip is exact and an operator's
// explicit false/0/"" survives.
func statusAgentFile(c StatusAgentConfig) *fileStatusAgentConfig {
	if c == (StatusAgentConfig{}) {
		return nil
	}
	return &fileStatusAgentConfig{
		Enabled:            &c.Enabled,
		Agent:              &c.Agent,
		Bin:                &c.Bin,
		Model:              &c.Model,
		TimeoutSeconds:     &c.TimeoutSeconds,
		MinIntervalSeconds: &c.MinIntervalSeconds,
		MaxPerCycle:        &c.MaxPerCycle,
		MinConfidence:      &c.MinConfidence,
		IncludeTranscript:  &c.IncludeTranscript,
	}
}

// validateStatusAgent applies the static rules ([statusagent] needs nothing
// external, so this is complete validation).
func (c *Config) validateStatusAgent() []error {
	var errs []error
	sa := c.StatusAgent
	if sa.Agent != "" && !agent.Valid(sa.Agent) {
		errs = append(errs, fmt.Errorf("statusagent.agent must be claude|codex|opencode (empty uses claude), got %q", sa.Agent))
	}
	if sa.TimeoutSeconds < 0 {
		errs = append(errs, fmt.Errorf("statusagent.timeout_seconds must be >= 0, got %d", sa.TimeoutSeconds))
	}
	if sa.MinIntervalSeconds < 0 {
		errs = append(errs, fmt.Errorf("statusagent.min_interval_seconds must be >= 0, got %d", sa.MinIntervalSeconds))
	}
	if sa.MaxPerCycle < 0 {
		errs = append(errs, fmt.Errorf("statusagent.max_per_cycle must be >= 0, got %d", sa.MaxPerCycle))
	}
	if sa.MinConfidence < 0 || sa.MinConfidence > 1 {
		errs = append(errs, fmt.Errorf("statusagent.min_confidence must be within [0, 1], got %g", sa.MinConfidence))
	}
	return errs
}
