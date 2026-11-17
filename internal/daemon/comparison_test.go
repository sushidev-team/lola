package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/comparison"
	"github.com/sushidev-team/lola/internal/protocol"
)

func TestComparisonCommandsAreLocalAndRejectInvalidEvidence(t *testing.T) {
	d := &Daemon{} // no stores, integrations or runtime: these commands must stay pure
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := d.handle(ctx, protocol.Request{Cmd: "comparisonTemplate"})
	if !r.OK {
		t.Fatal(r)
	}
	var b comparison.Bundle
	if err := json.Unmarshal(r.Data, &b); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	if d.handle(ctx, protocol.Request{Cmd: "comparisonReport", Args: raw}).OK {
		t.Fatal("unfilled template reported")
	}
	b.Pairs[0] = comparison.Pair{ID: "one", Task: "Preserve focus", Acceptance: []string{"Focus returns"}, Repositories: []comparison.Repository{{Name: "test/repo", Base: strings.Repeat("a", 40)}}, Limits: comparison.Limits{AgentCalls: 4, RuntimeSeconds: 600, Tokens: 10000}, Intervention: "One reviewed lesson"}
	raw, _ = json.Marshal(b)
	r = d.handle(ctx, protocol.Request{Cmd: "comparisonPrepare", Args: raw})
	if !r.OK {
		t.Fatal(r)
	}
	var draft comparison.Bundle
	_ = json.Unmarshal(r.Data, &draft)
	if len(draft.Runs) != 2 {
		t.Fatal(draft)
	}
	r = d.handle(ctx, protocol.Request{Cmd: "comparisonReport", Args: raw})
	if !r.OK {
		t.Fatal(r)
	}
	var report comparison.Report
	_ = json.Unmarshal(r.Data, &report)
	if report.PlannedPairs != 1 || report.MatchedBudgetPairs != 0 || len(report.Pairs[0].Limitations) != 2 {
		t.Fatal(report)
	}
	if d.handle(ctx, protocol.Request{Cmd: "comparisonReport", Args: json.RawMessage(`{"version":100}`)}).OK {
		t.Fatal("unknown schema accepted")
	}
}
