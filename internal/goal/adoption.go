package goal

import (
	"errors"
	"reflect"
	"regexp"
)

var issueUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Adoption pins existing placement without moving the issue under the goal.
// Title, description and routing remain in the task contract. Membership is a
// separately reserved related-issue link, never a replacement parent/milestone.
type Adoption struct {
	ParentID    string  `json:"parent_id,omitempty"`
	MilestoneID string  `json:"milestone_id,omitempty"`
	Membership  Binding `json:"membership"`
}

func (g Goal) validateAdoptions() error {
	for _, t := range g.Tasks {
		if t.ExistingIssueID == "" {
			if t.Adoption != nil {
				return errors.New("adoption lacks an explicit existing issue")
			}
			continue
		}
		if t.Adoption == nil || t.Issue.ID != t.ExistingIssueID || !t.Issue.Confirmed || t.Adoption.Membership.ID == "" || t.DecomposedFrom != "" {
			return errors.New("existing issue lacks its captured adoption binding")
		}
		if g.State == "planned" || g.Execution != nil {
			if !t.Adoption.Membership.Confirmed {
				return errors.New("adoption membership is not published")
			}
		}
	}
	return nil
}

func preserveAdoptions(old, next Goal) error {
	for _, a := range old.Tasks {
		if a.Adoption == nil {
			continue
		}
		found := false
		for _, b := range next.Tasks {
			if b.ID != a.ID {
				continue
			}
			found = true
			if b.Adoption == nil || a.Issue.ID != b.Issue.ID || !reflect.DeepEqual(a.TaskDraft, b.TaskDraft) || a.Adoption.ParentID != b.Adoption.ParentID || a.Adoption.MilestoneID != b.Adoption.MilestoneID || a.Adoption.Membership.ID != b.Adoption.Membership.ID || (a.Adoption.Membership.Confirmed && !b.Adoption.Membership.Confirmed) {
				return errors.New("adopted issue contract and membership must be retained")
			}
			if len(a.Relations) != len(b.Relations) {
				return errors.New("adopted dependency bindings must be retained")
			}
			for i, relation := range a.Relations {
				if relation.ID != b.Relations[i].ID || (relation.Confirmed && !b.Relations[i].Confirmed) {
					return errors.New("adopted dependency identity cannot be replaced")
				}
			}
		}
		if !found {
			return errors.New("adopted issue reservation must be retained")
		}
	}
	return nil
}
