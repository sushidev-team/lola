// Package ao shells out to the Agent Orchestrator CLI. Command shapes are
// verified against AO's bundled CLI (`ao session ls --json`, flag-only
// `ao spawn`); the legacy `ao list --json` / positional-spawn forms are
// rejected by current AO builds.
package ao

import (
	"context"
	"encoding/json"
	"os/exec"
)

// SessionState is one element of `ao session ls --json`'s data[] array.
// Without --all the listing already excludes orchestrator-role sessions, so
// everything here is a worker. IssueID carries the Linear identifier passed
// to spawn (e.g. FE-231); session IDs themselves are AO-internal
// (<sessionPrefix>-<n>) and never match Linear identifiers.
type SessionState struct {
	ID           string `json:"id"`
	Project      string `json:"projectId"`
	IssueID      string `json:"issueId"`
	Status       string `json:"status"`
	IsTerminated bool   `json:"isTerminated"`
}

type Client struct{ Bin string } // absolute path; launchd has no PATH

func (c *Client) Reachable(ctx context.Context) bool {
	return exec.CommandContext(ctx, c.Bin, "session", "ls", "--json").Run() == nil
}

func (c *Client) LiveSessions(ctx context.Context) ([]SessionState, error) {
	out, err := exec.CommandContext(ctx, c.Bin, "session", "ls", "--json").Output()
	if err != nil {
		return nil, err
	}
	var env struct {
		Data []SessionState `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, err
	}
	live := make([]SessionState, 0, len(env.Data))
	for _, s := range env.Data {
		if !s.IsTerminated {
			live = append(live, s)
		}
	}
	return live, nil
}

// Spawn uses the Linear IDENTIFIER (FE-231), never the UUID. A non-empty
// prompt is passed via --prompt so the agent starts with issue context (AO's
// own issue resolution is GitHub-only).
func (c *Client) Spawn(ctx context.Context, project, identifier, prompt string) error {
	args := []string{"spawn", "--project", project, "--issue", identifier}
	if prompt != "" {
		args = append(args, "--prompt", prompt)
	}
	return exec.CommandContext(ctx, c.Bin, args...).Run()
}

// Projects returns the registered AO project IDs (`ao project ls --json`).
// Desktop AO builds keep the registry in SQLite, so this — not a yaml file —
// is the authoritative source for ao_project validation. Unlike `session ls`,
// this envelope is unverified against a real AO build, so both a projects[]
// and a session-ls-style data[] key are accepted; callers must treat an
// empty result as "registry unavailable" and fall back to the yaml scan
// rather than as an authoritative empty registry.
func (c *Client) Projects(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, c.Bin, "project", "ls", "--json").Output()
	if err != nil {
		return nil, err
	}
	type entry struct {
		ID string `json:"id"`
	}
	var env struct {
		Projects []entry `json:"projects"`
		Data     []entry `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, err
	}
	entries := env.Projects
	if len(entries) == 0 {
		entries = env.Data
	}
	ids := make([]string, 0, len(entries))
	for _, p := range entries {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

// CountLive counts only sessions whose Status is in countingStates.
func CountLive(sessions []SessionState, project string, countingStates map[string]bool) int {
	n := 0
	for _, s := range sessions {
		if s.Project == project && countingStates[s.Status] {
			n++
		}
	}
	return n
}
