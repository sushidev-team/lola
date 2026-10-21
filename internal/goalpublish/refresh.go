package goalpublish

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
)

var ErrTargetChanged = errors.New("delivery target changed; refresh and reverify the goal before publication")

var exactCommit = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

// CaptureTargets reads configured delivery branches only. The caller must
// durably reserve this entire map before fetching or staging a refresh.
func (p *Publisher) CaptureTargets(ctx context.Context, goal string, policy goalpolicy.Policy) (map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	branch, err := p.refreshPlacement(ctx, goal, policy)
	if err != nil {
		return nil, err
	}
	targets := make(map[string]string, len(policy.Members))
	for _, member := range policy.Members {
		refs, err := p.refs(ctx, member.Repo, branch, member.Target)
		if err != nil {
			return nil, err
		}
		if refs.Default == branch || !exactCommit.MatchString(refs.Base) {
			return nil, errors.New("cannot establish an exact safe delivery target")
		}
		targets[member.ID] = refs.Base
	}
	return targets, nil
}

// FetchTarget imports one reserved exact commit into a disposable daemon-owned
// store. It never fetches into the source checkout or authoritative goal repo.
// A lost response is safe to retry: object presence is the acknowledgment.
func (p *Publisher) FetchTarget(ctx context.Context, goal, operation string, policy goalpolicy.Policy, member, base, target string) (goalwork.TargetStore, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Guard == nil || !exactCommit.MatchString(base) || !exactCommit.MatchString(target) {
		return goalwork.TargetStore{}, errors.New("target fetch requires exact commits and a live authority guard")
	}
	if _, err := p.refreshPlacement(ctx, goal, policy); err != nil {
		return goalwork.TargetStore{}, err
	}
	var repo string
	for _, m := range policy.Members {
		if m.ID == member {
			repo = m.Repo
		}
	}
	if repo == "" {
		return goalwork.TargetStore{}, errors.New("target fetch member is outside accepted policy")
	}
	var store goalwork.TargetStore
	err := p.effect(ctx, func() error {
		var err error
		store, err = p.Workspaces.PrepareTarget(ctx, goal, operation, member, base)
		return err
	})
	if err != nil {
		return store, err
	}
	if err := p.Workspaces.TargetPresent(ctx, store, target); err == nil {
		return store, nil
	}
	err = p.effect(ctx, func() error {
		args := append(p.httpsGitArgs(), "-c", "fetch.fsckObjects=true", "-c", "transfer.fsckObjects=true", "-c", "gc.auto=0", "-c", "maintenance.auto=false",
			"fetch", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--", "https://github.com/"+repo+".git", target)
		bin := p.GitBin
		if bin == "" {
			bin = "git"
		}
		_, err := p.command(ctx, store.GitDir, bin, args, nil)
		return err
	})
	if err != nil {
		return store, err
	}
	return store, p.Workspaces.TargetPresent(ctx, store, target)
}

func (p *Publisher) refreshPlacement(ctx context.Context, goal string, policy goalpolicy.Policy) (string, error) {
	if p.Workspaces == nil {
		return "", errors.New("target refresh requires owned goal workspaces")
	}
	if err := policy.Validate(); err != nil {
		return "", err
	}
	branch, err := goalwork.GoalBranch(goal)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, member := range policy.Members {
		_, name, _ := strings.Cut(member.Repo, "/")
		if len(member.Repo) > 255 || !repositoryName.MatchString(member.Repo) || name == "." || name == ".." || seen[strings.ToLower(member.Repo)] || member.Target == branch {
			return "", errors.New("target refresh requires distinct configured GitHub repositories")
		}
		seen[strings.ToLower(member.Repo)] = true
		r, err := p.Workspaces.Repository(ctx, goal, member.ID)
		if err != nil {
			return "", err
		}
		if r.Source != member.Source || r.Target != member.Target || r.Branch != branch {
			return "", errors.New("target refresh placement differs from the owned repository")
		}
	}
	return branch, nil
}
