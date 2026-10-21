package config

import "fmt"

func (c *Config) validateGoalMode() []error {
	var errs []error
	for _, p := range c.Projects {
		if !p.GoalMode {
			continue
		}
		if p.Stack != "" {
			errs = append(errs, fmt.Errorf("project %q: enable goal_mode on the owning stack, not a member", p.Name))
		}
		if !p.Sandboxed() {
			errs = append(errs, fmt.Errorf("project %q: goal_mode requires sandbox = docker for isolated workers", p.Name))
		}
		if p.TeamID == "" || p.ProjectID == "" {
			errs = append(errs, fmt.Errorf("project %q: goal_mode requires an existing Linear team and project", p.Name))
		}
	}
	return errs
}
