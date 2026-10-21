package goalpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
)

type Member struct {
	ID      string   `json:"id"`
	Project string   `json:"project"`
	Source  string   `json:"source"`
	Repo    string   `json:"repo"`
	Target  string   `json:"target"`
	Paths   []string `json:"paths,omitempty"`
}

type Limits struct {
	Concurrency     int     `json:"concurrency"`
	MaxTasks        int     `json:"max_tasks"`
	AttemptsPerTask int     `json:"attempts_per_task"`
	ReviewRounds    int     `json:"review_rounds"`
	AgentRuns       int     `json:"agent_runs"` // workers + coordinator + reviewers
	RuntimeMinutes  int     `json:"runtime_minutes"`
	AttemptMinutes  int     `json:"attempt_minutes"`
	Tokens          int64   `json:"tokens"` // observed usage; delayed reports are not an exact hard cap
	SpendUSD        float64 `json:"spend_usd,omitempty"`
	TargetRefreshes int     `json:"target_refreshes,omitempty"` // 0 disables automatic refresh; at most 3
}

type Policy struct {
	Members   []Member `json:"members"`
	Checks    []Check  `json:"checks"`
	Profiles  []string `json:"profiles"`
	Limits    Limits   `json:"limits"`
	PublishPR bool     `json:"publish_pr"`       // goal branches and draft PRs only; never merge
	Replan    bool     `json:"replan,omitempty"` // bounded coordinator decomposition after conclusive failure
}

func (p Policy) Validate() error {
	if len(p.Members) < 1 || len(p.Members) > 50 || len(p.Checks) < 1 || len(p.Checks) > 50 {
		return errors.New("goal policy requires 1–50 members and acceptance checks")
	}
	l := p.Limits
	if l.TargetRefreshes < 0 || l.TargetRefreshes > 3 {
		return errors.New("target refresh limit must be between 0 (disabled) and 3")
	}
	if l.Concurrency < 1 || l.Concurrency > 8 || l.MaxTasks < 1 || l.MaxTasks > 50 || l.AttemptsPerTask < 1 || l.AttemptsPerTask > 5 || l.ReviewRounds < 1 || l.ReviewRounds > 3 || l.AgentRuns < 1 || l.AgentRuns > 1000 || l.RuntimeMinutes < 1 || l.RuntimeMinutes > 10080 || l.AttemptMinutes < 1 || l.AttemptMinutes > l.RuntimeMinutes || l.Tokens < 1 || l.SpendUSD < 0 || math.IsNaN(l.SpendUSD) || math.IsInf(l.SpendUSD, 0) {
		return errors.New("goal policy requires finite positive concurrency, task, attempt, review, agent-run, runtime and token limits")
	}
	members := map[string]bool{}
	sources := map[string]bool{}
	for _, m := range p.Members {
		if !identifier.MatchString(m.ID) || m.Project == "" || members[m.ID] || !filepath.IsAbs(m.Source) || sources[filepath.Clean(m.Source)] || m.Target == "" || strings.HasPrefix(m.Target, "-") || strings.HasPrefix(m.Target, "refs/") || strings.ContainsAny(m.Target, "\x00\r\n") {
			return errors.New("policy members require distinct identities and absolute checkouts, with explicit target branches")
		}
		if p.PublishPR && (len(strings.Split(m.Repo, "/")) != 2 || strings.HasPrefix(m.Repo, "/") || strings.HasSuffix(m.Repo, "/") || strings.ContainsAny(m.Repo, " \t\r\n:")) {
			return errors.New("publishing requires an explicit GitHub owner/repository for every member")
		}
		members[m.ID] = true
		sources[filepath.Clean(m.Source)] = true
		if err := ValidatePaths(m.Paths); err != nil {
			return err
		}
	}
	checks := map[string]bool{}
	for _, c := range p.Checks {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("check %s: %w", c.ID, err)
		}
		if checks[c.ID] || (c.Member != "" && !members[c.Member]) {
			return errors.New("check identities must be unique and members must belong to the policy")
		}
		checks[c.ID] = true
	}
	profiles := map[string]bool{}
	for _, id := range p.Profiles {
		if !identifier.MatchString(id) || profiles[id] {
			return errors.New("required reviewer identities must be unique")
		}
		profiles[id] = true
	}
	return nil
}

func (p Policy) Digest() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
