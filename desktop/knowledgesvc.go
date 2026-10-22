package main

import (
	"encoding/json"
	"errors"

	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

func (s *DaemonService) KnowledgeList(project string) ([]knowledge.Lesson, error) {
	out := []knowledge.Lesson{}
	if project == "" {
		return out, errors.New("project required")
	}
	args, _ := json.Marshal(map[string]any{"scope": knowledge.Scope{Project: project}})
	err := call(protocol.Request{Cmd: "knowledgeList", Args: args}, shortTimeout, &out)
	return out, err
}
func (s *DaemonService) KnowledgeCapture(request knowledge.CaptureRequest, correction bool) (knowledge.Lesson, error) {
	var out knowledge.Lesson
	if request.Scope.Project == "" || request.CaptureID == "" {
		return out, errors.New("project and retained capture identity required")
	}
	args, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	command := "knowledgeCapture"
	if correction {
		command = "knowledgeCorrect"
	}
	err = call(protocol.Request{Cmd: command, Args: args}, shortTimeout, &out)
	return out, err
}
func (s *DaemonService) KnowledgeAccept(request knowledge.Acceptance) (knowledge.Lesson, error) {
	var out knowledge.Lesson
	if request.Scope.Project == "" {
		return out, errors.New("project required")
	}
	args, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	err = call(protocol.Request{Cmd: "knowledgeAccept", Args: args}, longTimeout, &out)
	return out, err
}
func (s *DaemonService) KnowledgeDispute(scope knowledge.Scope, id string, version int, reason string) error {
	if scope.Project == "" {
		return errors.New("project required")
	}
	args, _ := json.Marshal(struct {
		Scope    knowledge.Scope `json:"scope"`
		ID       string          `json:"id"`
		Version  int             `json:"version"`
		Evidence string          `json:"evidence"`
	}{scope, id, version, reason})
	return call(protocol.Request{Cmd: "knowledgeDispute", Args: args}, shortTimeout, nil)
}

// Export produces reviewable file content; it never writes a repository file.
func (s *DaemonService) KnowledgeExport(project string) (map[string]string, error) {
	out := map[string]string{}
	if project == "" {
		return out, errors.New("project required")
	}
	args, _ := json.Marshal(map[string]any{"scope": knowledge.Scope{Project: project}})
	err := call(protocol.Request{Cmd: "knowledgeExport", Args: args}, longTimeout, &out)
	return out, err
}
