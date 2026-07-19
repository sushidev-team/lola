package main

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/secrets"
)

// LinearService exposes the Linear metadata the poll editor needs to offer real
// cascading pickers (team → project/cycle/state/label/assignee) instead of raw
// UUIDs. It resolves the API key the same way the daemon/TUI do — from the
// keychain or an env var, by *name* (secrets.LinearAPIKey) — so the key never
// touches config, argv, or a log line. Per-team metadata is cached in memory;
// pass refresh=true to bypass it.
type LinearService struct {
	mu    sync.Mutex
	cache map[string]LinearTeamMeta
}

func NewLinearService() *LinearService {
	return &LinearService{cache: map[string]LinearTeamMeta{}}
}

// LinearOption is one selectable {id,label} — the id is the Linear UUID stored
// in config, the label is what the picker shows.
type LinearOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type LinearTeam struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type LinearTeamMeta struct {
	Projects      []LinearOption `json:"projects"`
	Cycles        []LinearOption `json:"cycles"`
	ActiveCycleID string         `json:"activeCycleId"`
	States        []LinearOption `json:"states"`
	Labels        []LinearOption `json:"labels"`
	Members       []LinearOption `json:"members"`
}

func linearClient() (linear.API, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return linearClientFor(cfg)
}

func linearClientFor(cfg *config.Config) (linear.API, error) {
	key, err := secrets.LinearAPIKey(cfg.Linear.APIKeyKeychain, cfg.Linear.APIKeyEnv)
	if err != nil {
		return nil, err
	}
	return linear.New(cfg.Linear.Endpoint, key), nil
}

// Teams lists the workspace's teams for the top-level picker.
func (s *LinearService) Teams() ([]LinearTeam, error) {
	c, err := linearClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ts, err := c.Teams(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LinearTeam, 0, len(ts))
	for _, t := range ts {
		out = append(out, LinearTeam{ID: t.ID, Key: t.Key, Name: t.Name})
	}
	return out, nil
}

// TeamMeta fetches everything the poll form's dependent pickers need for a team.
func (s *LinearService) TeamMeta(teamID string, refresh bool) (LinearTeamMeta, error) {
	if teamID == "" {
		return LinearTeamMeta{}, nil
	}
	if !refresh {
		s.mu.Lock()
		if m, ok := s.cache[teamID]; ok {
			s.mu.Unlock()
			return m, nil
		}
		s.mu.Unlock()
	}

	c, err := linearClient()
	if err != nil {
		return LinearTeamMeta{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	projects, err := c.Projects(ctx, teamID)
	if err != nil {
		return LinearTeamMeta{}, err
	}
	active, cycles, err := c.Cycles(ctx, teamID)
	if err != nil {
		return LinearTeamMeta{}, err
	}
	states, err := c.States(ctx, teamID)
	if err != nil {
		return LinearTeamMeta{}, err
	}
	labels, err := c.Labels(ctx, teamID)
	if err != nil {
		return LinearTeamMeta{}, err
	}
	members, err := c.Members(ctx, teamID)
	if err != nil {
		return LinearTeamMeta{}, err
	}

	meta := LinearTeamMeta{}
	for _, p := range projects {
		meta.Projects = append(meta.Projects, LinearOption{ID: p.ID, Label: p.Name})
	}
	for _, cy := range cycles {
		meta.Cycles = append(meta.Cycles, LinearOption{ID: cy.ID, Label: cycleLabel(cy)})
	}
	if active != nil {
		meta.ActiveCycleID = active.ID
	}
	for _, st := range states {
		meta.States = append(meta.States, LinearOption{ID: st.ID, Label: st.Name})
	}
	for _, l := range labels {
		meta.Labels = append(meta.Labels, LinearOption{ID: l.ID, Label: labelDisplay(l)})
	}
	for _, u := range members {
		if !u.Active {
			continue
		}
		meta.Members = append(meta.Members, LinearOption{ID: u.ID, Label: memberLabel(u)})
	}

	s.mu.Lock()
	s.cache[teamID] = meta
	s.mu.Unlock()
	return meta, nil
}

func cycleLabel(c linear.Cycle) string {
	if c.Name == "" {
		return "Cycle " + strconv.Itoa(c.Number)
	}
	return "Cycle " + strconv.Itoa(c.Number) + " — " + c.Name
}

// labelDisplay mirrors internal/tui: a child label shows "Parent / Child".
func labelDisplay(l linear.Label) string {
	if l.Parent != nil && l.Parent.Name != "" {
		return l.Parent.Name + " / " + l.Name
	}
	return l.Name
}

func memberLabel(u linear.User) string {
	if u.Email != "" {
		return u.Name + " (" + u.Email + ")"
	}
	return u.Name
}
