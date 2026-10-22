package main

import (
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestImprovementBridge(t *testing.T) {
	scope := knowledge.Scope{Project: "nori", GoalID: "goal", Paths: []string{"src"}}
	f := startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		var args struct {
			Scope    knowledge.Scope `json:"scope"`
			ID       string          `json:"id"`
			Revision string          `json:"revision"`
			After    string          `json:"after"`
			Version  int             `json:"version"`
			Decision string          `json:"decision"`
			Evidence string          `json:"evidence"`
		}
		if err := json.Unmarshal(req.Args, &args); err != nil {
			t.Fatal(err)
		}
		if args.Scope.Project != "nori" || args.Scope.GoalID != "goal" || len(args.Scope.Paths) != 1 || args.Scope.Paths[0] != "src" {
			t.Fatal("lost scope", args)
		}
		switch req.Cmd {
		case "knowledgeImprovementCatalog":
			if args.After != "page-after" || args.ID != "" {
				t.Fatal(args)
			}
			return protocol.Response{OK: true, Data: json.RawMessage(`{"records":[],"total":0}`)}
		case "knowledgeImprovement":
			if args.ID != "proposal" {
				t.Fatal(args)
			}
			return protocol.Response{OK: true, Data: json.RawMessage(`{"id":"proposal","revision":"reviewed"}`)}
		case "knowledgeImprovementSources":
			if args.ID != "proposal" || args.Revision != "reviewed" || args.After != "source-after" {
				t.Fatal(args)
			}
			return protocol.Response{OK: true, Data: json.RawMessage(`{"id":"proposal","revision":"reviewed","references":[],"total":0}`)}
		case "knowledgeImprovementDecisions":
			if args.ID != "proposal" {
				t.Fatal(args)
			}
			return protocol.Response{OK: true, Data: json.RawMessage(`[]`)}
		case "knowledgeVersion":
			if args.ID != "lesson" || args.Version != 3 {
				t.Fatal(args)
			}
			return protocol.Response{OK: true, Data: json.RawMessage(`{"id":"lesson","version":3}`)}
		case "knowledgeDecideImprovement":
			if args.ID != "proposal" || args.Revision != "reviewed" || args.Decision != "tracked" || args.Evidence != "Recorded in the normal issue workflow." {
				t.Fatal(args)
			}
			return protocol.Response{Error: "uncertain decision reply"}
		case "knowledgeExportImprovement":
			if args.ID != "proposal" || args.Revision != "reviewed" {
				t.Fatal(args)
			}
			return protocol.Response{OK: true, Data: json.RawMessage(`{"IMPROVEMENT.md":"# Review before implementation"}`)}
		default:
			t.Fatal(req.Cmd)
		}
		return protocol.Response{}
	})
	s := &DaemonService{}
	if _, err := s.KnowledgeImprovementCatalog(scope, "page-after"); err != nil {
		t.Fatal(err)
	}
	if p, err := s.KnowledgeImprovement(scope, "proposal"); err != nil || p.Revision != "reviewed" {
		t.Fatal(p, err)
	}
	if _, err := s.KnowledgeImprovementSources(scope, "proposal", "reviewed", "source-after"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KnowledgeImprovementDecisions(scope, "proposal"); err != nil {
		t.Fatal(err)
	}
	if l, err := s.KnowledgeVersion(scope, "lesson", 3); err != nil || l.Version != 3 {
		t.Fatal(l, err)
	}
	if _, err := s.KnowledgeDecideImprovement(knowledge.ImprovementTriage{Scope: scope, ID: "proposal", Revision: "reviewed", Decision: "tracked", Evidence: "Recorded in the normal issue workflow."}); err == nil || err.Error() != "uncertain decision reply" {
		t.Fatal("lost mutation uncertainty", err)
	}
	if files, err := s.KnowledgeExportImprovement(scope, "proposal", "reviewed"); err != nil || files["IMPROVEMENT.md"] == "" {
		t.Fatal(files, err)
	}
	if len(f.requests()) != 7 {
		t.Fatal("automatic retries or extra effects", len(f.requests()))
	}
	if _, err := s.KnowledgeImprovementCatalog(knowledge.Scope{}, ""); err == nil {
		t.Fatal("missing scope")
	}
	if _, err := s.KnowledgeImprovement(scope, ""); err == nil {
		t.Fatal("missing identity")
	}
	if _, err := s.KnowledgeImprovementSources(scope, "proposal", "", ""); err == nil {
		t.Fatal("missing source revision")
	}
	if _, err := s.KnowledgeImprovementDecisions(scope, ""); err == nil {
		t.Fatal("missing history identity")
	}
	if _, err := s.KnowledgeVersion(scope, "lesson", 0); err == nil {
		t.Fatal("unversioned source")
	}
	if _, err := s.KnowledgeDecideImprovement(knowledge.ImprovementTriage{Scope: scope, ID: "proposal"}); err == nil {
		t.Fatal("unreviewed decision")
	}
	if _, err := s.KnowledgeExportImprovement(scope, "proposal", ""); err == nil {
		t.Fatal("unreviewed export")
	}
	if len(f.requests()) != 7 {
		t.Fatal("invalid request reached daemon")
	}
}
