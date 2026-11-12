package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitrepo"
)

// HubProjectDTO is one project as the Team hub settings show it: which repo it
// offers the hub and whether it takes chores. Stacks are left out — a stack
// has no repository of its own; its member projects are listed instead.
type HubProjectDTO struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Repo is the configured GitHub "owner/name" ("" = none).
	Repo string `json:"repo"`
	// SuggestedRepo is what the checkout's remote says, offered when Repo is
	// empty or differs. Read from local git only; "" when unknown.
	SuggestedRepo string `json:"suggestedRepo"`
	HubChores     bool   `json:"hubChores"`
	Sandboxed     bool   `json:"sandboxed"`
	// Stack is the stack this project is a member of ("" = standalone).
	Stack string `json:"stack"`
}

// HubProjectPatchDTO changes one project's hub-facing keys. A nil field is
// left as it is.
type HubProjectPatchDTO struct {
	Name      string  `json:"name"`
	Repo      *string `json:"repo"`
	HubChores *bool   `json:"hubChores"`
	Sandboxed *bool   `json:"sandboxed"`
}

// inspectRepo is the seam over gitrepo.Inspect (tests never shell git).
var inspectRepo = func(ctx context.Context, dir string) string {
	return gitrepo.Inspect(ctx, dir).Repo
}

// HubProjects lists the projects that can take team chores, in config order.
func (s *ConfigService) HubProjects() ([]HubProjectDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out := []HubProjectDTO{}
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.IsStack() {
			continue
		}
		d := HubProjectDTO{
			Name:      p.Name,
			Label:     p.DisplayName(),
			Repo:      p.Repo,
			HubChores: p.HubChores,
			Sandboxed: p.Sandboxed(),
			Stack:     p.Stack,
		}
		if p.Path != "" && ctx.Err() == nil {
			if r := inspectRepo(ctx, p.Path); r != "" && r != p.Repo {
				d.SuggestedRepo = r
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// SaveHubProject writes a project's repo, chore opt-in and Docker sandbox
// in one config save. Turning chores on needs an owner/name repo — the same
// rule Validate enforces — and the error says so in the UI's words.
func (s *ConfigService) SaveHubProject(patch HubProjectPatchDTO) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	p := cfg.ProjectByName(patch.Name)
	if p == nil {
		return errors.New("project not found: " + patch.Name)
	}
	if p.IsStack() {
		return errors.New("a stack has no repository of its own; set it on the member projects")
	}
	if patch.Repo != nil {
		repo := strings.TrimSpace(*patch.Repo)
		if repo != "" && hubproto.ValidateRepo(repo) != nil {
			return errors.New("enter the repository as owner/name")
		}
		p.Repo = repo
	}
	if patch.HubChores != nil {
		p.HubChores = *patch.HubChores
	}
	if patch.Sandboxed != nil {
		if *patch.Sandboxed {
			p.Sandbox = config.SandboxDocker
		} else if p.Sandbox == config.SandboxDocker {
			p.Sandbox = ""
		}
	}
	if p.HubChores && hubproto.ValidateRepo(p.Repo) != nil {
		return errors.New("set the GitHub repository first")
	}
	return saveConfig(cfg, path)
}
