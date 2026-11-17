package config

import (
	"fmt"

	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func reviewProfileEntries(fr *fileReviewConfig) []reviewprofile.Profile {
	if fr == nil {
		return nil
	}
	return fr.Profile
}

func (c *Config) validateReviewProfiles() []error {
	var errs []error
	ids := map[string]bool{}
	providers := map[string]ReviewProvider{}
	for _, p := range c.EffectiveReviewProviders() {
		providers[string(p.Provider)] = p
	}
	for _, p := range c.ReviewProfiles {
		if err := p.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("review profile %q: %w", p.ID, err))
		}
		if ids[p.ID] {
			errs = append(errs, fmt.Errorf("duplicate review profile %q", p.ID))
		}
		ids[p.ID] = true
		provider, ok := providers[p.Provider]
		_, agentProvider := ReviewAgentFor(p.Provider)
		if p.Enabled && (!ok || !provider.Enabled || !agentProvider) {
			errs = append(errs, fmt.Errorf("review profile %q requires an enabled agent-family provider (structured rubric execution)", p.ID))
		}
		for _, project := range p.Projects {
			if c.ProjectByName(project) == nil {
				errs = append(errs, fmt.Errorf("review profile %q names unknown project %q", p.ID, project))
			}
		}
	}
	return errs
}
