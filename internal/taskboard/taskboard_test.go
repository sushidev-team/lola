package taskboard

import (
	"github.com/sushidev-team/lola/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestSharedRequirements(t *testing.T) {
	now := time.Now()
	tasks, _, err := Apply(nil, []string{"add", "login", "requirement", "Keyboard\n access"}, "user", now)
	if err != nil || tasks[0].Text != "Keyboard access" {
		t.Fatalf("add: %+v %v", tasks, err)
	}
	before := tasks[0]
	changed, _, err := Apply(tasks, []string{"state", "login", "doing"}, "agent", now)
	if err != nil || changed[0].State != "doing" || tasks[0] != before {
		t.Fatalf("state or alias: %+v %v", changed, err)
	}
	for _, args := range [][]string{{"edit", "login", "different scope"}, {"remove", "login"}} {
		if _, _, err := Apply(tasks, args, "agent", now); err == nil {
			t.Fatalf("agent changed user's requirement: %v", args)
		}
	}
	if _, id, err := Apply(tasks, []string{"add", "login", "requirement", "Keyboard access"}, "user", now); err != nil || id != "" {
		t.Fatalf("retry: %s %v", id, err)
	}
	if _, _, err := Apply(tasks, []string{"add", "login", "requirement", "Different"}, "user", now); err == nil {
		t.Fatal("ID conflict accepted")
	}
}
func TestBounds(t *testing.T) {
	for _, args := range [][]string{{"add", "bad id", "task", "x"}, {"add", "id", "other", "x"}, {"add", "id", "task", strings.Repeat("x", MaxText+1)}, {"add", "id", "task", "\n"}, {"state", "missing", "done"}} {
		if _, _, err := Apply(nil, args, "agent", time.Now()); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	full := make([]protocol.SessionTask, MaxTasks)
	if _, _, err := Apply(full, []string{"add", "new", "task", "x"}, "agent", time.Now()); err == nil {
		t.Fatal("unbounded list")
	}
}

func TestRequirementAttachment(t *testing.T) {
	now := time.Now()
	tasks, _, _ := Apply(nil, []string{"add", "work", "task", "Ship"}, "user", now)
	tasks, _, err := Apply(tasks, []string{"add", "criteria", "requirement", "--task", "work", "Accessible"}, "agent", now)
	if err != nil || tasks[1].ParentID != "work" {
		t.Fatalf("linked add: %+v %v", tasks, err)
	}
	if _, _, err := Apply(tasks, []string{"attach", "work", "criteria"}, "agent", now); err == nil {
		t.Fatal("task was attached to requirement")
	}
	if _, _, err := Apply(tasks, []string{"attach", "criteria", "missing"}, "agent", now); err == nil {
		t.Fatal("missing parent allowed")
	}
	detached, _, err := Apply(tasks, []string{"remove", "work"}, "user", now)
	if err != nil || len(detached) != 1 || detached[0].ParentID != "" || tasks[1].ParentID != "work" {
		t.Fatalf("removal lost requirement or mutated source: %+v %v", detached, err)
	}
}

func TestTaskCompletionRequiresVerifiedCriteria(t *testing.T) {
	now := time.Now()
	original := []protocol.SessionTask{{ID: "work", Kind: "task", State: "done", Author: "user"}}
	next, _, err := Apply(original, []string{"add", "criterion", "requirement", "--task", "work", "Keyboard access"}, "agent", now)
	if err != nil || next[0].State != "todo" || original[0].State != "done" {
		t.Fatalf("new scope did not reopen safely: %+v %v", next, err)
	}
	if _, _, err := Apply(next, []string{"state", "work", "done"}, "agent", now); err == nil {
		t.Fatal("completed unmet requirements")
	}
	next, _, _ = Apply(next, []string{"state", "criterion", "done"}, "agent", now)
	next, _, err = Apply(next, []string{"state", "work", "done"}, "agent", now)
	if err != nil || next[0].State != "done" {
		t.Fatalf("verified completion: %+v %v", next, err)
	}
	for _, args := range [][]string{{"state", "criterion", "todo"}, {"edit", "criterion", "Also support tab navigation"}} {
		reopened, _, err := Apply(next, args, "agent", now)
		if err != nil || reopened[0].State != "todo" || reopened[1].State != "todo" {
			t.Fatalf("scope change not reopened: %+v %v", reopened, err)
		}
	}
}

func TestAgentsOrganizeRequirementsWithoutDiscardingThem(t *testing.T) {
	now := time.Now()
	items := []protocol.SessionTask{{ID: "first", Kind: "task", State: "todo", Author: "agent"}, {ID: "second", Kind: "task", State: "done", Author: "agent"}, {ID: "criterion", Kind: "requirement", ParentID: "first", Text: "Keep user scope", State: "todo", Author: "user"}}
	for _, args := range [][]string{{"add", "new", "requirement", "Unattached"}, {"attach", "criterion", "none"}, {"remove", "first"}} {
		if _, _, err := Apply(items, args, "agent", now); err == nil {
			t.Fatalf("accepted disorganized operation: %v", args)
		}
	}
	next, _, err := Apply(items, []string{"attach", "criterion", "second"}, "agent", now)
	if err != nil || next[2].ParentID != "second" || next[2].Text != items[2].Text || next[2].Author != "user" || next[1].State != "todo" {
		t.Fatalf("move: %+v %v", next, err)
	}
	if _, _, err := Apply(next, []string{"remove", "first"}, "agent", now); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalRequirementsAreExplicitAndCanBeReorganized(t *testing.T) {
	now := time.Now()
	items, _, err := Apply(nil, []string{"add", "accessibility", "requirement", "--global", "Keyboard access throughout"}, "agent", now)
	if err != nil || items[0].Scope != "global" || items[0].ParentID != "" {
		t.Fatalf("global: %+v %v", items, err)
	}
	if _, _, err := Apply(nil, []string{"add", "bad", "task", "--global", "Not a requirement"}, "agent", now); err == nil {
		t.Fatal("global task accepted")
	}
	items, _, _ = Apply(items, []string{"add", "work", "task", "Keyboard support"}, "agent", now)
	items, _, err = Apply(items, []string{"attach", "accessibility", "work"}, "agent", now)
	if err != nil || items[0].Scope != "" || items[0].ParentID != "work" {
		t.Fatalf("attached: %+v %v", items, err)
	}
	items, _, err = Apply(items, []string{"attach", "accessibility", "--global"}, "agent", now)
	if err != nil || items[0].Scope != "global" || items[0].ParentID != "" {
		t.Fatalf("global again: %+v %v", items, err)
	}
}

func TestManualChecksAndFeedback(t *testing.T) {
	now := time.Now()
	items := []protocol.SessionTask{
		{ID: "work", Kind: "task", State: "doing"},
		{ID: "browser", Kind: "requirement", ParentID: "work", Text: "Check the shortcut in the browser", State: "todo", Author: "user"},
	}
	next, _, err := Apply(items, []string{"state", "browser", "checking"}, "agent", now)
	if err != nil || next[1].State != "checking" || items[1].State != "todo" {
		t.Fatalf("checking: %+v %v", next, err)
	}
	if _, _, err := Apply(next, []string{"state", "work", "done"}, "agent", now); err == nil {
		t.Fatal("completed task before human check")
	}
	next, _, err = Apply(next, []string{"feedback", "browser", "Shortcut still scrolls\nthe page"}, "agent", now)
	if err != nil || next[1].Feedback != "Shortcut still scrolls the page" || next[1].State != "todo" || next[1].Text != items[1].Text || next[1].Author != "user" {
		t.Fatalf("feedback changed scope: %+v %v", next, err)
	}
	for _, args := range [][]string{{"feedback", "missing", "problem"}, {"feedback", "browser", ""}, {"feedback", "browser", strings.Repeat("x", MaxText+1)}} {
		if _, _, err := Apply(next, args, "agent", now); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	next, _, _ = Apply(next, []string{"state", "browser", "checking"}, "agent", now)
	next, _, _ = Apply(next, []string{"state", "browser", "done"}, "user", now)
	next, _, err = Apply(next, []string{"state", "work", "done"}, "agent", now)
	if err != nil || next[0].State != "done" {
		t.Fatalf("confirmation: %+v %v", next, err)
	}
	// Repeated feedback after another check is a new failure, not a no-op.
	next, _, err = Apply(next, []string{"feedback", "browser", "Shortcut still scrolls the page"}, "agent", now)
	if err != nil || next[0].State != "todo" || next[1].State != "todo" {
		t.Fatalf("reopen: %+v %v", next, err)
	}
}
