package session

import (
	"strings"
	"testing"
)

func TestGoalIdentityDoesNotCaptureAProjectNamedGoal(t *testing.T) {
	ordinary := Session{ID: "lola-goal-eng-123", Kind: KindLinear, IssueUUID: "issue"}
	if ordinary.GoalManaged() || !ordinary.LinearBound() || !ordinary.OwnsBranch() {
		t.Fatal("ordinary project named goal was reclassified")
	}
	lost := Session{ID: "lola-goal-" + strings.Repeat("a", 32), IssueUUID: "issue"}
	if !lost.GoalManaged() || lost.LinearBound() || lost.OwnsBranch() {
		t.Fatal("goal identity lost its delivery guard")
	}
}
