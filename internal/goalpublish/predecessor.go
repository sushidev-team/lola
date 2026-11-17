package goalpublish

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// predecessor carries branch/PR ownership across final-verification revisions.
// Its head must be an ancestor of the newly independently verified head. A
// remote head outside these two exact revisions never receives a push.
func (p *Publisher) predecessor(ctx context.Context, want Request, digest string) (Record, error) {
	return p.predecessorChain(ctx, want, digest, want.Policy.Limits.TargetRefreshes, map[string]bool{want.Operation: true})
}

func (p *Publisher) predecessorChain(ctx context.Context, want Request, digest string, remaining int, seen map[string]bool) (Record, error) {
	if want.PreviousOperation == "" {
		return Record{}, nil
	}
	if seen[want.PreviousOperation] || len(want.Targets) != len(want.Policy.Members) || remaining <= 0 {
		return Record{}, errors.New("publication predecessor requires an authorized refreshed target combination")
	}
	seen[want.PreviousOperation] = true
	dir, err := p.Workspaces.DeliveryDirectory(want.Goal, want.PreviousOperation)
	if err != nil {
		return Record{}, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "publication.json"))
	if err != nil {
		return Record{}, err
	}
	var old Record
	if err := json.Unmarshal(b, &old); err != nil {
		return old, err
	}
	oldDigest, err := old.Request.Policy.Digest()
	if err != nil || old.Version != 1 || old.CreatedAt.IsZero() || old.Request.Goal != want.Goal || old.Request.Operation != want.PreviousOperation || oldDigest != digest || len(old.Members) != len(want.Policy.Members) || len(old.Request.Manifest) != len(old.Members) {
		return old, errors.New("publication predecessor lost its accepted identity")
	}
	parent, err := p.predecessorChain(ctx, old.Request, digest, remaining-1, seen)
	if err != nil {
		return old, err
	}
	if len(old.Request.Targets) > 0 && len(old.Request.Targets) != len(old.Members) {
		return old, errors.New("predecessor lost target combination")
	}
	for _, m := range want.Policy.Members {
		member, ok := old.Members[m.ID]
		if !ok || member.Repo != m.Repo || member.Target != m.Target || member.Head != old.Request.Manifest[m.ID] || !slices.Contains([]string{"pending", "unchanged", "published", "merged", "closed"}, member.State) || old.Complete && member.State == "pending" || member.EvidenceComment < 0 || member.Number < 0 {
			return old, errors.New("publication predecessor member is inconsistent")
		}
		if slices.Contains([]string{"published", "merged", "closed"}, member.State) && (!member.Pushed || member.Number < 1 || member.URL == "") {
			return old, errors.New("predecessor lost its PR acknowledgment")
		}
		expectedMarker := ""
		if old.Request.PreviousOperation != "" {
			expectedMarker = parent.Members[m.ID].MarkerOperation
			if expectedMarker == "" {
				expectedMarker = old.Request.PreviousOperation
			}
		}
		if member.MarkerOperation != expectedMarker {
			return old, errors.New("predecessor changed its original PR ownership marker")
		}
		r, err := p.Workspaces.Repository(ctx, want.Goal, m.ID)
		if err != nil {
			return old, err
		}
		target := r.Start
		if len(old.Request.Targets) > 0 {
			target = old.Request.Targets[m.ID]
			if !exactCommit.MatchString(target) {
				return old, errors.New("predecessor target is not an exact commit")
			}
		}
		if member.State == "unchanged" {
			same, err := p.Workspaces.TreesEqual(ctx, want.Goal, m.ID, member.Head, target)
			if err != nil {
				return old, err
			}
			if !same || member.Number != 0 || member.Pushed {
				return old, errors.New("predecessor unchanged member has inconsistent delivery")
			}
		}
		if err := p.Workspaces.IsAncestor(ctx, want.Goal, m.ID, member.Head, want.Manifest[m.ID]); err != nil {
			return old, errors.New("refreshed publication does not preserve the previous verified head")
		}
	}
	return old, nil
}
