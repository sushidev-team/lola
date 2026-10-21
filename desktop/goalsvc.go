package main

import (
	"encoding/json"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
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

// GoalPropose makes one explicit bounded call. The caller retains the request ID
// before sending, and recovers uncertain responses through GoalProposal.
func (s *DaemonService) GoalPropose(request goalplan.Request) (goalplan.Record, error) {
	args, err := json.Marshal(request)
	var out goalplan.Record
	if err != nil {
		return out, err
	}
	err = call(protocol.Request{Cmd: "goalPropose", Args: args}, longTimeout, &out)
	return out, err
}

func (s *DaemonService) GoalProposals(project string) ([]goalplan.Summary, error) {
	args, _ := json.Marshal(map[string]string{"project": project})
	out := []goalplan.Summary{}
	err := call(protocol.Request{Cmd: "goalProposals", Args: args}, shortTimeout, &out)
	return out, err
}

func (s *DaemonService) GoalProposal(id string) (goalplan.Record, error) {
	args, _ := json.Marshal(map[string]string{"id": id})
	var out goalplan.Record
	err := call(protocol.Request{Cmd: "goalProposal", Args: args}, shortTimeout, &out)
	return out, err
}

func (s *DaemonService) GoalAcceptPlan(id string, revision uint64) (string, error) {
	args, _ := json.Marshal(struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
	}{id, revision})
	var out goal.Goal
	err := call(protocol.Request{Cmd: "goalAcceptPlan", Args: args}, longTimeout, &out)
	return out.ID, err
}

// GoalControl preserves the revision the human actually reviewed. In particular
// a stale request is never retried automatically against the latest revision.
func (s *DaemonService) GoalControl(id string, revision uint64, action, reason string) error {
	args, _ := json.Marshal(goal.ControlRequest{ID: id, Revision: revision, Action: action, Reason: reason})
	return call(protocol.Request{Cmd: "goalControl", Args: args}, longTimeout, nil)
}

// GoalPublish projects the reviewed goal into its existing Linear project only.
// An uncertain response is not retried here; the UI must reload and review again.
func (s *DaemonService) GoalPublish(id string, revision uint64) error {
	args, _ := json.Marshal(struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
	}{id, revision})
	return call(protocol.Request{Cmd: "goalPublishReviewed", Args: args}, longTimeout, nil)
}

// GoalStartOptions suggests finite limits and configured repositories without
// starting an agent or issuing any remote write.
func (s *DaemonService) GoalStartOptions(id string) (goal.StartOptions, error) {
	args, _ := json.Marshal(protocol.GoalArgs{ID: id})
	var out goal.StartOptions
	err := call(protocol.Request{Cmd: "goalStartOptions", Args: args}, shortTimeout, &out)
	return out, err
}

func (s *DaemonService) GoalStart(authorization goal.Authorization) error {
	args, err := json.Marshal(authorization)
	if err != nil {
		return err
	}
	return call(protocol.Request{Cmd: "goalStart", Args: args}, longTimeout, nil)
}
