package reviewprofile

import (
	"reflect"
	"slices"
	"strings"
)

// ReviewOwner comes from the daemon's session/goal, never model output. Old
// records without it remain inspectable but cannot grant worker authority.
type ReviewOwner struct {
	Project string `json:"project"`
	Session string `json:"session,omitempty"`
	Goal    string `json:"goal,omitempty"`
	Task    string `json:"task,omitempty"`
}

// DisputeActor is constructed by the daemon from a live reserved attempt. The
// task ancestry receipt allows a replacement task to dispute its parent's review.
type DisputeActor struct {
	ReviewOwner
	AllowedTasks []string `json:"allowed_tasks,omitempty"`
}

type WorkerDispute struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Evidence string `json:"evidence"`
}

func validOwnerText(s string) bool {
	return s != "" && len(s) <= 240 && !strings.ContainsAny(s, "\x00\n\r\t")
}
func workerOwnsFinding(actor DisputeActor, owner *ReviewOwner) bool {
	if owner == nil || !validOwnerText(actor.Project) || !validOwnerText(actor.Session) || actor.Project != owner.Project || actor.Goal != owner.Goal {
		return false
	}
	if actor.Goal == "" {
		return actor.Task == "" && len(actor.AllowedTasks) == 0 && actor.Session == owner.Session
	}
	if !validOwnerText(actor.Goal) || !validOwnerText(actor.Task) || len(actor.AllowedTasks) == 0 || len(actor.AllowedTasks) > 1000 {
		return false
	}
	for _, task := range actor.AllowedTasks {
		if !validOwnerText(task) {
			return false
		}
	}
	return slices.Contains(actor.AllowedTasks, actor.Task) && slices.Contains(actor.AllowedTasks, owner.Task)
}
func sameDisputeActor(a, b *DisputeActor) bool { return reflect.DeepEqual(a, b) }

// DisputeWorker can only report a disagreement, once per finding. It cannot
// dismiss a requirement, declare a fix verified, or alter any review verdict.
func (s *Store) DisputeWorker(actor DisputeActor, a WorkerDispute) (FindingReport, error) {
	actor.AllowedTasks = slices.Clone(actor.AllowedTasks)
	return s.decide(FindingDecision{ID: a.ID, Revision: a.Revision, State: "disputed", Evidence: a.Evidence}, &actor)
}

func BlockingDispute(f FindingReport) bool {
	return f.State == "disputed" && (f.Finding.Severity == "major" || f.Finding.Severity == "blocker")
}
