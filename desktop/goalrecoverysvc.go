package main

import (
	"encoding/json"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

// GoalRecovery reads a local snapshot. It never launches work or contacts Linear.
func (s *DaemonService) GoalRecovery(id string) (goal.RecoveryView, error) {
	args, _ := json.Marshal(protocol.GoalArgs{ID: id})
	var out goal.RecoveryView
	err := call(protocol.Request{Cmd: "goalRecovery", Args: args}, shortTimeout, &out)
	return out, err
}

// GoalSyncBrief reads Linear and retains the observed brief in local history.
// A successful refresh is not acceptance of that brief or authority to resume.
func (s *DaemonService) GoalSyncBrief(id string) error {
	args, _ := json.Marshal(protocol.GoalArgs{ID: id})
	return call(protocol.Request{Cmd: "goalSyncBrief", Args: args}, longTimeout, nil)
}

func (s *DaemonService) GoalAcceptBrief(request goal.BriefAcceptance) error {
	args, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return call(protocol.Request{Cmd: "goalAcceptBrief", Args: args}, longTimeout, nil)
}

func (s *DaemonService) GoalDismissProgress(request goal.ProgressResolution) error {
	args, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return call(protocol.Request{Cmd: "goalDismissProgress", Args: args}, longTimeout, nil)
}

func (s *DaemonService) GoalDeliveryEvidence(request goal.DeliveryEvidenceRequest) error {
	args, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return call(protocol.Request{Cmd: "goalDeliveryEvidence", Args: args}, longTimeout, nil)
}
