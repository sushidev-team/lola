package daemon

import (
	"errors"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func (d *Daemon) goalStartOptions(id string) (goal.StartOptions, error) {
	var out goal.StartOptions
	g, ok := d.goals.Get(id)
	if !ok {
		return out, errors.New("unknown goal")
	}
	if g.Execution != nil || g.State != "planned" || g.SyncError != "" || g.PendingBrief != "" {
		return out, errors.New("publish and reconcile the goal before authorizing execution")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.cfg.ProjectByName(g.Project)
	if p == nil || p.InStack() || !p.GoalMode || !p.Sandboxed() {
		return out, errors.New("enable autonomous goals and Docker workspace in project or stack Execution settings")
	}
	wanted := map[string]bool{}
	for _, task := range g.Tasks {
		for _, member := range task.Apps {
			wanted[member] = true
		}
	}
	if !p.IsStack() {
		wanted[p.Name] = true
	}
	members := []goalpolicy.Member{}
	for _, member := range d.cfg.Projects {
		if !wanted[member.Name] {
			continue
		}
		if member.IsStack() || (p.IsStack() && member.Stack != p.Name) || (!p.IsStack() && member.Name != p.Name) {
			return out, errors.New("goal routing no longer matches project configuration")
		}
		target := member.DefaultBranch
		if target == "" {
			target = config.DefaultBranchName
		}
		members = append(members, goalpolicy.Member{ID: member.Name, Project: member.Name, Source: member.Path, Repo: member.Repo, Target: target})
	}
	if len(members) != len(wanted) || len(members) == 0 {
		return out, errors.New("a goal repository is no longer configured")
	}
	out = goal.StartOptions{Goal: g.Overview(time.Now().UTC()), Authorization: goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: goalpolicy.Policy{Members: members, Checks: []goalpolicy.Check{}, Profiles: []string{}, Limits: goal.SuggestedLimits(len(g.Tasks))}}, Profiles: []goal.StartProfile{}}
	for _, profile := range d.cfg.ReviewProfiles {
		if !profile.Enabled || len(profile.Projects) > 0 && !slices.Contains(profile.Projects, g.Project) {
			continue
		}
		out.Profiles = append(out.Profiles, goal.StartProfile{ID: profile.ID, Kind: profile.Kind, Required: profile.Required, Artifacts: profile.RequiredArtifacts()})
		if profile.Required {
			out.Authorization.Policy.Profiles = append(out.Authorization.Policy.Profiles, profile.ID)
		}
	}
	return out, nil
}
