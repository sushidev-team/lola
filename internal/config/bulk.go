package config

import (
	"fmt"
	"slices"
)

// BulkPatch is one settings change applied to many [[project]] tables at once.
// A nil field leaves that setting alone; a non-nil one is an explicit override
// (so MatchLabels = &[]string{} means "match no labels", not "inherit").
//
// Label settings are REFERENCES (a Linear label UUID, a name, or "group/name"):
// the same label name has a different UUID in every team, so each project's
// refs are resolved against its own team by the LabelResolver ApplyBulk is
// given. Config itself never talks to Linear.
type BulkPatch struct {
	Agent          *string
	MatchLabels    *[]string
	MatchMode      *string
	OnSentSetLabel *string
	BlockedLabel   *string
}

// Empty reports whether the patch changes nothing.
func (b BulkPatch) Empty() bool {
	return b.Agent == nil && b.MatchLabels == nil && b.MatchMode == nil &&
		b.OnSentSetLabel == nil && b.BlockedLabel == nil
}

// LabelResolver turns one label reference into the label UUID valid for the
// project (its team). An empty ref stays empty without being asked.
type LabelResolver func(p Project, ref string) (string, error)

// BulkChange reports what a bulk apply did (or would do) to one project.
type BulkChange struct {
	Project string   `json:"project"`
	Changed []string `json:"changed,omitempty"` // TOML keys whose value differs now
	Error   string   `json:"error,omitempty"`
}

// ApplyBulk applies patch to the named projects (every project when names is
// empty) in memory and reports per project. Label values go through resolve.
// A project that reports an Error is left untouched; callers refuse to save
// when any project errored, so a bulk change lands whole or not at all.
//
// Writes go through the Inherits bitmap: an explicit override clears the
// inherit bit, otherwise Save would silently drop the value (see Project).
func ApplyBulk(cfg *Config, names []string, patch BulkPatch, resolve LabelResolver) ([]BulkChange, error) {
	if patch.Empty() {
		return nil, fmt.Errorf("bulk: nothing to change")
	}
	if patch.Agent != nil && *patch.Agent != "" && !validBulkAgent(*patch.Agent) {
		return nil, fmt.Errorf("bulk: unknown agent %q (claude|codex|opencode)", *patch.Agent)
	}
	if patch.MatchMode != nil && *patch.MatchMode != "any" && *patch.MatchMode != "all" {
		return nil, fmt.Errorf("bulk: match mode must be any or all, got %q", *patch.MatchMode)
	}
	for _, n := range names {
		if cfg.ProjectByName(n) == nil {
			return nil, fmt.Errorf("bulk: project %q not found", n)
		}
	}
	var out []BulkChange
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if len(names) > 0 && !slices.Contains(names, p.Name) {
			continue
		}
		ch := BulkChange{Project: p.Name}
		if err := applyBulkOne(p, patch, resolve, &ch); err != nil {
			ch.Error = err.Error()
		}
		out = append(out, ch)
	}
	return out, nil
}

func validBulkAgent(s string) bool { return s == "claude" || s == "codex" || s == "opencode" }

func applyBulkOne(p *Project, patch BulkPatch, resolve LabelResolver, ch *BulkChange) error {
	work := *p
	work.MatchLabels = slices.Clone(p.MatchLabels)
	var changed []string
	if patch.Agent != nil && work.Agent != *patch.Agent {
		work.Agent = *patch.Agent
		changed = append(changed, "agent")
	}
	if patch.MatchMode != nil && (work.MatchMode != *patch.MatchMode || work.Inherits.MatchMode) {
		work.MatchMode = *patch.MatchMode
		work.Inherits.MatchMode = false
		changed = append(changed, "match_mode")
	}
	one := func(ref string) (string, error) {
		if ref == "" {
			return "", nil
		}
		return resolve(*p, ref)
	}
	if patch.MatchLabels != nil {
		ids := []string{} // non-nil: nil would mean "inherit" to ResolveInheritance
		for _, ref := range *patch.MatchLabels {
			id, err := one(ref)
			if err != nil {
				return err
			}
			if id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if !slices.Equal(ids, work.MatchLabels) || work.Inherits.MatchLabels || work.MatchLabels == nil {
			work.MatchLabels = ids
			work.Inherits.MatchLabels = false
			changed = append(changed, "match_labels")
		}
	}
	if patch.OnSentSetLabel != nil {
		id, err := one(*patch.OnSentSetLabel)
		if err != nil {
			return err
		}
		if id != work.OnSentSetLabel || work.Inherits.OnSentSetLabel {
			work.OnSentSetLabel = id
			work.Inherits.OnSentSetLabel = false
			changed = append(changed, "on_sent_set_label")
		}
	}
	if patch.BlockedLabel != nil {
		id, err := one(*patch.BlockedLabel)
		if err != nil {
			return err
		}
		if id != work.BlockedLabelID || work.Inherits.BlockedLabelID {
			work.BlockedLabelID = id
			work.Inherits.BlockedLabelID = false
			changed = append(changed, "blocked_label_id")
		}
	}
	ch.Changed = changed
	if len(changed) > 0 {
		*p = work
	}
	return nil
}
