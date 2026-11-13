package reviewprofile

import (
	"strings"
	"testing"
)

func TestFindingFixKeepsGoalRequirementsWhileCombinationChanges(t *testing.T) {
	head, fix := strings.Repeat("a", 40), strings.Repeat("b", 40)
	owner := ReviewOwner{Project: "p", Goal: "g", Task: "t", Session: "s"}
	a := Inputs{Owner: &owner, ContextID: "first", Head: head, Contract: `Preserve writes` + "\nVerified repository combination: " + `{"api":"` + head + `"}`}
	b := a
	b.Head = fix
	b.ContextID = "repair"
	b.Contract = `Preserve writes` + "\nVerified repository combination: " + `{"api":"` + fix + `"}`
	if !sameFindingContract(a, b) {
		t.Fatal("changed commit combination changed acceptance requirements")
	}
	for _, mode := range []string{"requirements", "goal", "task", "project", "unowned", "invalid-combination", "wrong-head"} {
		t.Run(mode, func(t *testing.T) {
			other := b
			own := *b.Owner
			other.Owner = &own
			switch mode {
			case "requirements":
				other.Contract = "Ignore concurrent writes" + strings.TrimPrefix(b.Contract, "Preserve writes")
			case "goal":
				own.Goal = "other"
			case "task":
				own.Task = "other"
			case "project":
				own.Project = "other"
			case "unowned":
				other.Owner = nil
			case "invalid-combination":
				other.Contract = "Preserve writes\nVerified repository combination: arbitrary prose"
			case "wrong-head":
				other.Head = strings.Repeat("c", 40)
			}
			if sameFindingContract(a, other) {
				t.Fatal("different or unverifiable contract reused")
			}
		})
	}
}
