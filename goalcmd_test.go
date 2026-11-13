package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func TestGoalAcceptDeliveryRequiresReviewedRevisionAndEvidence(t *testing.T) {
	for _, args := range [][]string{{"accept-delivery", "goal"}, {"accept-delivery", "goal", "--revision", "3"}, {"accept-delivery", "goal", "--reason", "Tested the delivered flow"}} {
		cmd := goalCmd()
		cmd.SetArgs(args)
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "required flag") {
			t.Fatalf("missing acceptance authority reached daemon: %v", err)
		}
	}
}

func TestGoalStartRequiresReviewedStackRolloutBeforeSocketCall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOLA_HOME", dir) // no live daemon can be reached by this fixture
	a := goal.Authorization{ID: "goal", Revision: 3, Policy: goalpolicy.Policy{
		Members: []goalpolicy.Member{{ID: "api", Project: "api", Source: "/private/api", Repo: "owner/api", Target: "main"}, {ID: "web", Project: "web", Source: "/private/web", Repo: "owner/web", Target: "main"}},
		Checks:  []goalpolicy.Check{{ID: "tests", Command: "true", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 60}},
		Limits:  goalpolicy.Limits{Concurrency: 1, MaxTasks: 10, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 10, RuntimeMinutes: 60, AttemptMinutes: 15, Tokens: 100000}, PublishPR: true,
	}}
	b, _ := json.Marshal(a)
	path := filepath.Join(dir, "authorization.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := goalCmd()
	cmd.SetArgs([]string{"start", path})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "policy.rollout") {
		t.Fatal("stack authorization did not require reviewed rollout locally", err)
	}
}
