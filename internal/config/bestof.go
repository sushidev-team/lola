package config

import (
	"fmt"
	"slices"

	"github.com/sushidev-team/lola/internal/bestof"
)

// The [best_of] table configures best-of-N dispatch: an issue a poll matches
// that ALSO carries `label` is spawned as several attempts — the `agents` mix —
// on sibling branches instead of one, and a human later keeps one of them
// (`lola bestof pick`). See internal/daemon/bestof.go.
//
// `label` is a Linear WORKSPACE label UUID like every [defaults] label (it
// applies to every project's poll), and like those that is a UI rule, not a
// validation one. An absent table, or one without a label, means the label
// trigger is off; `lola bestof start` still works and falls back to `agents`
// when given no mix of its own.

// BestOfConfig is the [best_of] table.
//
//   - Label is the trigger label's UUID ("" = no label trigger).
//   - Agents is the default mix, one `<agent>[@<model>][:<count>]` entry each
//     (internal/bestof). Empty means DefaultBestOfAgents.
type BestOfConfig struct {
	Label  string   `toml:"label"`
	Agents []string `toml:"agents"`
}

// DefaultBestOfAgents is the mix used when [best_of].agents is empty: two
// claude attempts, the one mix that needs no second agent installed.
var DefaultBestOfAgents = []string{"claude:2"}

type fileBestOfConfig struct {
	Label  *string   `toml:"label,omitempty"`
	Agents *[]string `toml:"agents,omitempty"`
}

func resolveBestOf(f *fileBestOfConfig) BestOfConfig {
	var b BestOfConfig
	if f == nil {
		return b
	}
	if f.Label != nil {
		b.Label = *f.Label
	}
	if f.Agents != nil {
		b.Agents = slices.Clone(*f.Agents)
	}
	return b
}

// bestOfFile builds the on-disk mirror; the zero table is omitted so a config
// that never mentioned [best_of] does not grow one on save.
func bestOfFile(b BestOfConfig) *fileBestOfConfig {
	if b.Label == "" && len(b.Agents) == 0 {
		return nil
	}
	f := &fileBestOfConfig{}
	if b.Label != "" {
		f.Label = &b.Label
	}
	if len(b.Agents) > 0 {
		a := slices.Clone(b.Agents)
		f.Agents = &a
	}
	return f
}

// BestOfMix is the effective default mix, expanded into attempts.
func (c *Config) BestOfMix() ([]bestof.Attempt, error) {
	entries := c.BestOf.Agents
	if len(entries) == 0 {
		entries = DefaultBestOfAgents
	}
	return bestof.ParseMix(entries)
}

func (c *Config) validateBestOf() []error {
	if len(c.BestOf.Agents) == 0 {
		return nil
	}
	if _, err := bestof.ParseMix(c.BestOf.Agents); err != nil {
		return []error{fmt.Errorf("best_of.agents: %w", err)}
	}
	return nil
}
