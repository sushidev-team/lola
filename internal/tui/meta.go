// Per-team Linear metadata blob: fetched via the GraphQL cascade, cached at
// Home()/cache/linear-<teamID>.json, force-refreshed with the 'r' key.
package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/you/aop/internal/config"
	"github.com/you/aop/internal/linear"
	"github.com/you/aop/internal/secrets"
)

type teamMeta struct {
	FetchedAt   time.Time        `json:"fetchedAt"`
	Viewer      linear.User      `json:"viewer"`
	Teams       []linear.Team    `json:"teams"`
	Projects    []linear.Project `json:"projects"`
	ActiveCycle *linear.Cycle    `json:"activeCycle,omitempty"`
	Cycles      []linear.Cycle   `json:"cycles"`
	States      []linear.State   `json:"states"`
	Labels      []linear.Label   `json:"labels"`
	Members     []linear.User    `json:"members"`
}

func cachePath(teamID string) (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cache", "linear-"+teamID+".json"), nil
}

func loadTeamCache(teamID string) (*teamMeta, error) {
	path, err := cachePath(teamID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m teamMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func saveTeamCache(teamID string, m *teamMeta) error {
	path, err := cachePath(teamID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// newLinearAPI builds a client from the configured endpoint and the API key
// resolved via Keychain/env. The key itself is never logged or displayed.
func newLinearAPI(cfg *config.Config) (linear.API, error) {
	key, err := secrets.LinearAPIKey(cfg.Linear.APIKeyKeychain, cfg.Linear.APIKeyEnv)
	if err != nil {
		return nil, err
	}
	return linear.New(cfg.Linear.Endpoint, key), nil
}

type teamsMsg struct {
	teams []linear.Team
	err   error
}

type metaMsg struct {
	teamID    string
	meta      *teamMeta
	fromCache bool
	err       error
}

func fetchTeamsCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		api, err := newLinearAPI(cfg)
		if err != nil {
			return teamsMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		teams, err := api.Teams(ctx)
		return teamsMsg{teams: teams, err: err}
	}
}

// loadMetaCmd returns the cached blob for teamID unless force is set, in
// which case (or on cache miss) it runs the full cascade and rewrites the
// cache.
func loadMetaCmd(cfg *config.Config, teamID string, force bool) tea.Cmd {
	return func() tea.Msg {
		if !force {
			if m, err := loadTeamCache(teamID); err == nil {
				return metaMsg{teamID: teamID, meta: m, fromCache: true}
			}
		}
		api, err := newLinearAPI(cfg)
		if err != nil {
			return metaMsg{teamID: teamID, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		m := &teamMeta{FetchedAt: time.Now()}
		if m.Viewer, err = api.Viewer(ctx); err != nil {
			return metaMsg{teamID: teamID, err: err}
		}
		if m.Teams, err = api.Teams(ctx); err != nil {
			return metaMsg{teamID: teamID, err: err}
		}
		if m.Projects, err = api.Projects(ctx, teamID); err != nil {
			return metaMsg{teamID: teamID, err: err}
		}
		if m.ActiveCycle, m.Cycles, err = api.Cycles(ctx, teamID); err != nil {
			return metaMsg{teamID: teamID, err: err}
		}
		if m.States, err = api.States(ctx, teamID); err != nil {
			return metaMsg{teamID: teamID, err: err}
		}
		if m.Labels, err = api.Labels(ctx, teamID); err != nil {
			return metaMsg{teamID: teamID, err: err}
		}
		if m.Members, err = api.Members(ctx, teamID); err != nil {
			return metaMsg{teamID: teamID, err: err}
		}
		_ = saveTeamCache(teamID, m) // best-effort; stale cache beats no data
		return metaMsg{teamID: teamID, meta: m}
	}
}

// labelDisplay renders group labels as "parent / child".
func labelDisplay(l linear.Label) string {
	if l.Parent != nil {
		return l.Parent.Name + " / " + l.Name
	}
	return l.Name
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}
