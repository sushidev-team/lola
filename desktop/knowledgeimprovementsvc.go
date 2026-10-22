package main

import (
	"encoding/json"
	"errors"

	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

// Improvement reads are local and on demand; no maintenance or external calls.
func (s *DaemonService) KnowledgeImprovementCatalog(scope knowledge.Scope, after string) (knowledge.ImprovementCatalog, error) {
	out := knowledge.ImprovementCatalog{Records: []knowledge.ImprovementRecord{}}
	err := callImprovement("knowledgeImprovementCatalog", scope, "", "", after, &out)
	return out, err
}
func (s *DaemonService) KnowledgeImprovement(scope knowledge.Scope, id string) (knowledge.Improvement, error) {
	var out knowledge.Improvement
	if id == "" {
		return out, errors.New("improvement identity required")
	}
	err := callImprovement("knowledgeImprovement", scope, id, "", "", &out)
	return out, err
}
func (s *DaemonService) KnowledgeImprovementSources(scope knowledge.Scope, id, revision, after string) (knowledge.ImprovementSources, error) {
	var out knowledge.ImprovementSources
	if id == "" || revision == "" {
		return out, errors.New("improvement identity and reviewed revision required")
	}
	err := callImprovement("knowledgeImprovementSources", scope, id, revision, after, &out)
	return out, err
}
func (s *DaemonService) KnowledgeImprovementDecisions(scope knowledge.Scope, id string) ([]knowledge.ImprovementDecision, error) {
	out := []knowledge.ImprovementDecision{}
	if id == "" {
		return out, errors.New("improvement identity required")
	}
	err := callImprovement("knowledgeImprovementDecisions", scope, id, "", "", &out)
	return out, err
}
func (s *DaemonService) KnowledgeVersion(scope knowledge.Scope, id string, version int) (knowledge.Lesson, error) {
	var out knowledge.Lesson
	if scope.Project == "" || id == "" || version < 1 {
		return out, errors.New("project and exact lesson version required")
	}
	args, _ := json.Marshal(struct {
		Scope   knowledge.Scope `json:"scope"`
		ID      string          `json:"id"`
		Version int             `json:"version"`
	}{scope, id, version})
	err := call(protocol.Request{Cmd: "knowledgeVersion", Args: args}, shortTimeout, &out)
	return out, err
}
func (s *DaemonService) KnowledgeDecideImprovement(request knowledge.ImprovementTriage) (knowledge.Improvement, error) {
	var out knowledge.Improvement
	if request.Scope.Project == "" || request.ID == "" || request.Revision == "" {
		return out, errors.New("project, improvement identity and reviewed revision required")
	}
	args, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	err = call(protocol.Request{Cmd: "knowledgeDecideImprovement", Args: args}, shortTimeout, &out)
	return out, err
}

// Export previews files for normal task review; it never installs policy or writes a checkout.
func (s *DaemonService) KnowledgeExportImprovement(scope knowledge.Scope, id, revision string) (map[string]string, error) {
	out := map[string]string{}
	if id == "" || revision == "" {
		return out, errors.New("improvement identity and reviewed revision required")
	}
	err := callImprovement("knowledgeExportImprovement", scope, id, revision, "", &out)
	return out, err
}
func callImprovement(command string, scope knowledge.Scope, id, revision, after string, out any) error {
	if scope.Project == "" {
		return errors.New("project required")
	}
	args, _ := json.Marshal(struct {
		Scope    knowledge.Scope `json:"scope"`
		ID       string          `json:"id,omitempty"`
		Revision string          `json:"revision,omitempty"`
		After    string          `json:"after,omitempty"`
	}{scope, id, revision, after})
	return call(protocol.Request{Cmd: command, Args: args}, shortTimeout, out)
}
