package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitrepo"
)

func newProjectDefaults() config.Project {
	return config.Project{
		DefaultBranch: config.DefaultBranchName,
		CycleMode:     "none",
		AssigneeMode:  "anyone",
		Inherits: config.ProjectInherits{
			Symlinks: true, PostCreate: true, Env: true,
			MatchLabels: true, MatchMode: true, OnSentSetLabel: true,
			BlockedLabelID: true, DedupMode: true, PrioritySort: true, AutoPRComments: true,
			RequirePlan: true,
		},
	}
}

func folderApp(info gitrepo.Info) StackFolderAppDTO {
	return StackFolderAppDTO{
		Path: info.Root, Label: config.LabelFromPath(info.Root),
		Repo: info.Repo, DefaultBranch: info.DefaultBranch,
	}
}

// ScanProjectFolder lists sibling checkouts and marks those already configured.
func (s *ConfigService) ScanProjectFolder(parent string) (StackFolderDTO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	infos, err := gitrepo.ScanAll(ctx, parent)
	if err != nil {
		return StackFolderDTO{}, err
	}
	cfg, _, err := loadConfig()
	if err != nil {
		return StackFolderDTO{}, err
	}
	out := StackFolderDTO{Path: parent, Apps: []StackFolderAppDTO{}}
	seen := map[string]bool{}
	for _, info := range infos {
		key := canonicalPath(info.Root)
		if seen[key] {
			continue
		}
		seen[key] = true
		app := folderApp(info)
		if p := cfg.ProjectAtPath(key, canonicalPath); p != nil {
			app.Project, app.Label, app.Blocked = p.Name, p.DisplayName(), "Already added"
		}
		out.Apps = append(out.Apps, app)
	}
	return out, nil
}

// AddProjectsFromFolder adds selected checkout roots in one config write.
// A repeated import skips configured paths and preserves their group and settings.
func (s *ConfigService) AddProjectsFromFolder(group string, paths []string) (int, error) {
	if len(paths) == 0 {
		return 0, errors.New("choose at least one project")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	apps := make([]StackFolderAppDTO, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return 0, errors.New("expected an absolute checkout path: " + path)
		}
		info := gitrepo.Inspect(ctx, path)
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if !info.IsRepo || canonicalPath(info.Root) != canonicalPath(path) {
			return 0, errors.New("checkout no longer available: " + path)
		}
		apps = append(apps, folderApp(info))
	}
	cfg, file, err := loadConfig()
	if err != nil {
		return 0, err
	}
	if group != "" && cfg.GroupByName(group) == nil {
		return 0, errors.New("group no longer exists: " + group)
	}
	added := 0
	for _, app := range apps {
		if cfg.ProjectAtPath(canonicalPath(app.Path), canonicalPath) != nil {
			continue
		}
		p := newProjectDefaults()
		p.Label, p.Path, p.Repo, p.Group = app.Label, app.Path, app.Repo, group
		base := config.Slug(app.Label)
		if base == "" {
			base = "project"
		}
		p.Name = base
		for n := 2; cfg.ProjectByName(p.Name) != nil; n++ {
			p.Name = base + "-" + strconv.Itoa(n)
		}
		if app.DefaultBranch != "" {
			p.DefaultBranch = app.DefaultBranch
		}
		cfg.Projects = append(cfg.Projects, p)
		added++
	}
	if added == 0 {
		return 0, nil
	}
	if g := cfg.GroupByName(group); g != nil {
		g.Collapsed = false
	}
	if err := saveConfig(cfg, file); err != nil {
		return 0, err
	}
	return added, nil
}
