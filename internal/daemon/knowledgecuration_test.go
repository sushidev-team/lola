package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestKnowledgeCaptureIdentityAndScopedCuration(t *testing.T) {
	d, _, _ := goalFixture(t)
	send := func(command string, args any) protocol.Response {
		t.Helper()
		b, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		return d.handleKnowledge(context.Background(), protocol.Request{Cmd: command, Args: b})
	}
	req := knowledge.CaptureRequest{Scope: knowledge.Scope{Project: "p1"}, CaptureID: "6cb0ae02-b741-4bb4-9b08-9e72007456cb", Proposal: knowledge.Proposal{Topic: "Shutdown", Summary: "Drain writes before stopping workers.", Evidence: []string{"Observed shutdown regression"}}}
	first := send("knowledgeCapture", req)
	if !first.OK {
		t.Fatal(first.Error)
	}
	var l knowledge.Lesson
	_ = json.Unmarshal(first.Data, &l)
	for range 2 {
		resp := send("knowledgeCapture", req)
		if !resp.OK || len(d.knowledge.List()) != 1 {
			t.Fatal("lost response retry duplicated capture", resp)
		}
	}
	changed := req
	changed.Proposal.Summary = "Stop workers before draining writes."
	if resp := send("knowledgeCapture", changed); resp.OK {
		t.Fatal("retained identity changed contents")
	}
	if resp := send("knowledgeCorrect", req); resp.OK {
		t.Fatal("retained identity changed authority")
	}
	bad := req
	bad.CaptureID = "not an ID"
	if resp := send("knowledgeCapture", bad); resp.OK {
		t.Fatal("unbounded identity accepted")
	}
	for _, scope := range []knowledge.Scope{{Project: "p2"}, {Project: "p1", GoalID: "another-goal"}} {
		decision := knowledge.Acceptance{Scope: scope, ID: l.ID, Version: l.Version, Evidence: "Verified shutdown with regression check"}
		if resp := send("knowledgeAccept", decision); resp.OK {
			t.Fatal("cross-scope accept")
		}
		if resp := send("knowledgeDispute", decision); resp.OK {
			t.Fatal("cross-scope dispute")
		}
	}
	decision := knowledge.Acceptance{Scope: l.Scope, ID: l.ID, Version: l.Version, Evidence: "Verified shutdown with regression check"}
	if resp := send("knowledgeAccept", decision); !resp.OK {
		t.Fatal(resp.Error)
	}
	if resp := send("knowledgeDispute", decision); resp.OK {
		t.Fatal("stale decision accepted")
	}
	// Retry returns the current curated record rather than reviving the candidate.
	recovered := send("knowledgeCapture", req)
	var current knowledge.Lesson
	_ = json.Unmarshal(recovered.Data, &current)
	if !recovered.OK || current.Status != knowledge.Active || current.Version != l.Version+1 {
		t.Fatal("recovery rewrote curation", recovered)
	}
	// Old CLI requests without a scope continue to work.
	if resp := send("knowledgeDispute", map[string]any{"id": l.ID, "version": current.Version, "evidence": "New regression contradicts the claim"}); !resp.OK {
		t.Fatal(resp.Error)
	}
}
