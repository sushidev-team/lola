package main

import (
	"encoding/json"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

// GoalOverview reads only durable local records. The project selector is an
// identity, never a display label. Empty selects every configured project.
func (s *DaemonService) GoalOverview(project string) ([]goal.Overview, error) {
	args, _ := json.Marshal(map[string]string{"project": project})
	out := []goal.Overview{}
	err := call(protocol.Request{Cmd: "goalOverview", Args: args}, shortTimeout, &out)
	return out, err
}

// GoalControl preserves the revision the human actually reviewed. In particular
// a stale request is never retried automatically against the latest revision.
func (s *DaemonService) GoalControl(id string, revision uint64, action, reason string) error {
	args, _ := json.Marshal(goal.ControlRequest{ID: id, Revision: revision, Action: action, Reason: reason})
	return call(protocol.Request{Cmd: "goalControl", Args: args}, longTimeout, nil)
}
