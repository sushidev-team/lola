package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
)

var labelUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// handleBulkProjects applies one settings change to many projects. Like
// renameProject it rebases on the on-disk config (another client may have saved
// since), validates before writing, and saves only when every project resolved:
// a label name missing from one team refuses the whole change instead of
// leaving the projects half-configured. The daemon, not the client, resolves
// label names because only it holds the Linear key.
func (d *Daemon) handleBulkProjects(ctx context.Context, raw json.RawMessage) (protocol.BulkProjectsData, error) {
	var args protocol.BulkProjectsArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return protocol.BulkProjectsData{}, fmt.Errorf("bad bulkProjects args: %w", err)
		}
	}
	patch := config.BulkPatch{
		Agent: args.Agent, MatchLabels: args.MatchLabels, MatchMode: args.MatchMode,
		OnSentSetLabel: args.OnSentSetLabel, BlockedLabel: args.BlockedLabel,
	}
	path, err := config.DefaultPath()
	if err != nil {
		return protocol.BulkProjectsData{}, err
	}
	nc, err := config.Load(path)
	if err != nil {
		return protocol.BulkProjectsData{}, err
	}

	var lin linear.API
	teamLabels := map[string][]linear.Label{}
	resolve := func(p config.Project, ref string) (string, error) {
		if labelUUIDRe.MatchString(ref) {
			return ref, nil
		}
		if p.TeamID == "" {
			return "", fmt.Errorf("label %q is a name but the project has no team_id to look it up in", ref)
		}
		labels, ok := teamLabels[p.TeamID]
		if !ok {
			if lin == nil {
				if lin, err = d.ensureLinear(); err != nil {
					return "", fmt.Errorf("resolve label %q: %w", ref, err)
				}
			}
			if labels, err = lin.Labels(ctx, p.TeamID); err != nil {
				return "", fmt.Errorf("list labels of team %s: %w", p.TeamID, err)
			}
			teamLabels[p.TeamID] = labels
		}
		return matchLabelRef(labels, ref)
	}

	changes, err := config.ApplyBulk(nc, args.Projects, patch, resolve)
	if err != nil {
		return protocol.BulkProjectsData{}, err
	}
	data := protocol.BulkProjectsData{}
	failed := 0
	for _, c := range changes {
		data.Changes = append(data.Changes, protocol.BulkProjectChange{Project: c.Project, Changed: c.Changed, Error: c.Error})
		if c.Error != "" {
			failed++
		}
	}
	if failed > 0 {
		return data, fmt.Errorf("bulkProjects: %d project%s could not be changed; nothing saved", failed, plural(failed))
	}
	if args.DryRun {
		return data, nil
	}
	if err := nc.Validate(); err != nil {
		return data, fmt.Errorf("bulkProjects: config invalid after change: %w", err)
	}
	if err := nc.Save(path); err != nil {
		return data, fmt.Errorf("bulkProjects: save config: %w", err)
	}
	data.Applied = true
	if err := d.handleReload(ctx); err != nil {
		return data, fmt.Errorf("bulkProjects: saved but reload failed: %w", err)
	}
	return data, nil
}

// matchLabelRef finds ref among a team's labels by name or "group/name",
// case-insensitively. An ambiguous name is an error, never a guess.
func matchLabelRef(labels []linear.Label, ref string) (string, error) {
	want := strings.ToLower(strings.TrimSpace(ref))
	var hits []string
	for _, l := range labels {
		name := strings.ToLower(l.Name)
		full := name
		if l.Parent != nil {
			full = strings.ToLower(l.Parent.Name) + "/" + name
		}
		if want == full || (want == name && !strings.Contains(want, "/")) {
			hits = append(hits, l.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no label named %q in the project's team", ref)
	case 1:
		return hits[0], nil
	}
	return "", fmt.Errorf("label %q is ambiguous in the project's team (%d matches); use group/name or the id", ref, len(hits))
}
