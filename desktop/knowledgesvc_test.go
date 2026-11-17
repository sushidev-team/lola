package main

import (
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestKnowledgeService(t *testing.T) {
	f := startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		var a struct {
			Scope     knowledge.Scope        `json:"scope"`
			CaptureID string                 `json:"capture_id"`
			ID        string                 `json:"id"`
			Version   int                    `json:"version"`
			Evidence  string                 `json:"evidence"`
			Code      []knowledge.CodeTarget `json:"code_targets"`
		}
		if err := json.Unmarshal(req.Args, &a); err != nil {
			t.Fatal(err)
		}
		if a.Scope.Project != "nori" {
			t.Fatal("lost project")
		}
		switch req.Cmd {
		case "knowledgeList":
			return protocol.Response{OK: true, Data: json.RawMessage(`[]`)}
		case "knowledgeExport":
			return protocol.Response{OK: true, Data: json.RawMessage(`{"LEARNINGS.md":"# Lessons"}`)}
		case "knowledgeCapture", "knowledgeCorrect":
			if a.CaptureID != "capture-id" {
				t.Fatal("lost capture identity")
			}
		case "knowledgeAccept", "knowledgeDispute":
			if a.ID != "lesson" || a.Version != 9 || a.Scope.GoalID != "goal" || a.Evidence != "Verified evidence" {
				t.Fatal("changed decision", a)
			}
		default:
			t.Fatal(req.Cmd)
		}
		return protocol.Response{Error: "uncertain reply"}
	})
	s := &DaemonService{}
	if _, err := s.KnowledgeList("nori"); err != nil {
		t.Fatal(err)
	}
	capture := knowledge.CaptureRequest{Scope: knowledge.Scope{Project: "nori"}, CaptureID: "capture-id"}
	for _, correct := range []bool{false, true} {
		if _, err := s.KnowledgeCapture(capture, correct); err == nil {
			t.Fatal("lost error")
		}
	}
	scope := knowledge.Scope{Project: "nori", GoalID: "goal"}
	if _, err := s.KnowledgeAccept(knowledge.Acceptance{Scope: scope, ID: "lesson", Version: 9, Evidence: "Verified evidence"}); err == nil {
		t.Fatal("lost error")
	}
	if err := s.KnowledgeDispute(scope, "lesson", 9, "Verified evidence"); err == nil {
		t.Fatal("lost error")
	}
	if files, err := s.KnowledgeExport("nori"); err != nil || files["LEARNINGS.md"] != "# Lessons" {
		t.Fatal(files, err)
	}
	if len(f.requests()) != 6 {
		t.Fatal("unexpected retry")
	}
}
