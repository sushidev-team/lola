package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func TestGoalWorkerReceivesAcceptedCompatibilityAndMergeOrder(t *testing.T) {
	n, g, _ := goalFixture(t)
	g.Execution.Policy.PublishPR = true
	g.Execution.Policy.Members[0].Repo = "owner/api"
	g.Execution.Policy.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Continue supporting the old client during rollout."}, MergeOrder: []string{"api"}}
	g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
	s, err := n.SpawnGoal(context.Background(), g, "attempt1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(s.Worktree), "control", lolaDir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), g.Execution.Policy.RolloutContract()) || !strings.Contains(string(b), "Do not push, create pull requests, merge") {
		t.Fatal("worker lost reviewed rollout or manual-delivery boundary")
	}
}
