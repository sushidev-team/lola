package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitrepo"
	"github.com/sushidev-team/lola/internal/knowledge"
)

// Resolve against the scope's configured repositories, never paths supplied by
// a worker or the saved reference. A removed/reassigned member fails closed.
func (d *Daemon) knowledgeRepository(project, member string) (string, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.cfg.ProjectByName(project)
	m := d.cfg.ProjectByName(member)
	if p == nil || m == nil || m.IsStack() || (p.IsStack() && m.Stack != p.Name) || (!p.IsStack() && m.Name != p.Name) || m.Path == "" {
		return "", "", fmt.Errorf("code reference is outside the configured lesson scope")
	}
	branch := m.DefaultBranch
	if branch == "" {
		branch = config.DefaultBranchName
	}
	source, err := filepath.Abs(m.Path)
	return source, branch, err
}

func (d *Daemon) readKnowledgeCode(ctx context.Context, source, branch, path string) (gitrepo.CodeIdentity, error) {
	if err := ctx.Err(); err != nil {
		return gitrepo.CodeIdentity{}, err
	}
	if d.knowledgeCodeRead != nil {
		return d.knowledgeCodeRead(ctx, source, branch, path)
	}
	return (gitrepo.CodeReader{}).Read(ctx, source, branch, path)
}

func (d *Daemon) acceptKnowledge(ctx context.Context, a knowledgeArgs) (knowledge.Lesson, error) {
	var lesson knowledge.Lesson
	for _, l := range d.knowledge.List() {
		if l.ID == a.ID && l.Version == a.Version {
			lesson = l
			break
		}
	}
	if lesson.ID == "" {
		return lesson, fmt.Errorf("lesson changed; reload before accepting")
	}
	targets := a.CodeTargets
	if targets == nil {
		for _, r := range lesson.Code {
			targets = append(targets, r.CodeTarget)
		}
	}
	if len(lesson.Code) > 0 && len(targets) == 0 {
		return lesson, fmt.Errorf("existing code references cannot be silently removed")
	}
	if err := knowledge.ValidateTargets(targets); err != nil {
		return lesson, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var refs []knowledge.CodeReference
	for _, target := range targets {
		source, branch, err := d.knowledgeRepository(lesson.Scope.Project, target.Member)
		if err != nil {
			return lesson, err
		}
		code, err := d.readKnowledgeCode(ctx, source, branch, target.Path)
		if err != nil || !code.Exists {
			return lesson, fmt.Errorf("cannot verify committed code for %s:%s", target.Member, target.Path)
		}
		refs = append(refs, knowledge.CodeReference{CodeTarget: target, Source: source, Branch: branch, Commit: code.Commit, Digest: code.Digest})
	}
	return d.knowledge.AcceptWithCode(a.ID, a.Version, a.Evidence, a.Supersedes, a.Expires, refs)
}

// Only proven changes demote durable lessons. Unavailable evidence is excluded
// for this read, and a subsequent successful read can restore its availability.
// One deadline covers the whole batch, including exports and explicit maintenance.
func (d *Daemon) maintainKnowledge(ctx context.Context, lessons []knowledge.Lesson) ([]knowledge.Lesson, []knowledge.Maintenance) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var valid []knowledge.Lesson
	var reports []knowledge.Maintenance
	for _, l := range lessons {
		r := knowledge.Maintenance{ID: l.ID, Version: l.Version, State: "current"}
		switch {
		case l.Status != knowledge.Active:
			r.State = "inactive"
		case !l.ExpiresAt.IsZero() && !time.Now().Before(l.ExpiresAt):
			r.State, r.Reason = "expired", "lesson expired"
		case len(l.Code) == 0:
			r.State = "untracked"
		default:
			for _, ref := range l.Code {
				source, branch, err := d.knowledgeRepository(l.Scope.Project, ref.Member)
				if err != nil {
					r.State, r.Reason = "unavailable", "configured repository is unavailable"
					break
				}
				if source != ref.Source || branch != ref.Branch {
					r.State, r.Reason = "changed", "referenced code changed"
					break
				}
				code, err := d.readKnowledgeCode(ctx, source, branch, ref.Path)
				if err != nil {
					r.State, r.Reason = "unavailable", "committed code could not be checked"
					break
				}
				if !code.Exists || code.Digest != ref.Digest {
					r.State, r.Reason = "changed", "referenced code changed"
					break
				}
			}
		}
		if r.State == "changed" || r.State == "expired" {
			if err := d.knowledge.ReviewCode(l.ID, l.Version, r.Reason); err != nil {
				r.State, r.Reason = "unavailable", "lesson changed or maintenance could not be saved"
			} else {
				r.Version++
			}
		}
		if r.State == "current" || r.State == "untracked" {
			valid = append(valid, l)
		}
		reports = append(reports, r)
	}
	// A curation decision made while Git was being read wins over this snapshot.
	current := map[string]knowledge.Lesson{}
	for _, l := range d.knowledge.List() {
		current[l.ID] = l
	}
	kept := valid[:0]
	for _, l := range valid {
		if now := current[l.ID]; now.Version == l.Version && now.Status == knowledge.Active {
			kept = append(kept, l)
		}
	}
	return kept, reports
}

func (d *Daemon) selectKnowledge(ctx context.Context, q knowledge.Query) knowledge.Bundle {
	if d.knowledge == nil {
		return knowledge.Bundle{}
	}
	// Bound candidates before IO. Do not refill from unvalidated lessons.
	selected := knowledge.Select(d.knowledge.List(), q, time.Now())
	valid, _ := d.maintainKnowledge(ctx, selected.Lessons)
	return knowledge.Select(valid, q, time.Now())
}
