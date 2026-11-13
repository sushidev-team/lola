package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestKnowledgeImprovementViews(t *testing.T) {
	d, _, _ := goalFixture(t)
	scope := knowledge.Scope{Project: "p1", GoalID: "goal"}
	p := knowledge.Proposal{Topic: "Recovery", Summary: "Persist reservations before starting work."}
	for _, id := range []string{"one", "two"} {
		if _, err := d.knowledge.Capture(scope, p, knowledge.Source{Kind: "worker", ID: id, Session: id}, false); err != nil {
			t.Fatal(err)
		}
	}
	proposal := d.knowledge.Improvements(scope, "").Proposals[0]
	d.knowledge.DecideImprovement(scope, proposal.ID, proposal.Revision, "tracked", "Track with the normal issue workflow.")
	before := d.knowledge.List()
	history := d.knowledge.ImprovementHistory(scope, proposal.ID)
	call := func(command string, args knowledgeArgs) protocol.Response {
		raw, _ := json.Marshal(args)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return d.handle(ctx, protocol.Request{Cmd: command, Args: raw})
	}
	args := knowledgeArgs{Scope: scope, ID: proposal.ID, Revision: proposal.Revision}
	for _, cmd := range []string{"knowledgeImprovementCatalog", "knowledgeImprovementSources", "knowledgeImprovementDecisions"} {
		r := call(cmd, args)
		if !r.OK {
			t.Fatal(cmd, r.Error)
		}
		switch cmd {
		case "knowledgeImprovementCatalog":
			var out knowledge.ImprovementCatalog
			json.Unmarshal(r.Data, &out)
			if out.Total != 1 || out.Records[0].ID != proposal.ID || !out.Records[0].Available {
				t.Fatal(out)
			}
		case "knowledgeImprovementSources":
			var out knowledge.ImprovementSources
			json.Unmarshal(r.Data, &out)
			if out.Total != 2 || out.Revision != proposal.Revision || out.Scope.GoalID != "goal" {
				t.Fatal(out)
			}
		case "knowledgeImprovementDecisions":
			var out []knowledge.ImprovementDecision
			json.Unmarshal(r.Data, &out)
			if len(out) != 1 || out[0].SourceVersions != nil || out[0].Evidence != history[0].Evidence {
				t.Fatal(out)
			}
		}
		missing := args
		missing.Scope.Project = ""
		if call(cmd, missing).OK {
			t.Fatal("missing scope accepted", cmd)
		}
	}
	args.Scope.GoalID = "other"
	if call("knowledgeImprovementSources", args).OK {
		t.Fatal("foreign source receipt exposed")
	}
	if !reflect.DeepEqual(before, d.knowledge.List()) || !reflect.DeepEqual(history, d.knowledge.ImprovementHistory(scope, proposal.ID)) || len(d.goals.List()) != 0 {
		t.Fatal("inspection mutated state")
	}
	// Read commands do not reserve or launch work, even with a canceled context.
}
