package ao

import (
	"context"
	"encoding/json"
	"os/exec"
)

type SessionState struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Status  string `json:"status"`
}

type Client struct{ Bin string } // absolute path; launchd has no PATH

func (c *Client) Reachable(ctx context.Context) bool {
	return exec.CommandContext(ctx, c.Bin, "list", "--json").Run() == nil
}

func (c *Client) LiveSessions(ctx context.Context) ([]SessionState, error) {
	out, err := exec.CommandContext(ctx, c.Bin, "list", "--json").Output()
	if err != nil {
		return nil, err
	}
	var s []SessionState
	return s, json.Unmarshal(out, &s)
}

// Spawn uses the Linear IDENTIFIER (FE-231), never the UUID.
func (c *Client) Spawn(ctx context.Context, project, identifier string) error {
	return exec.CommandContext(ctx, c.Bin, "spawn", project, identifier).Run()
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
