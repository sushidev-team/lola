package linear

import (
	"context"

	"github.com/sushidev-team/lola/internal/config"
)

type API interface {
	Viewer(ctx context.Context) (User, error)
	Teams(ctx context.Context) ([]Team, error)
	Projects(ctx context.Context, teamID string) ([]Project, error)
	Cycles(ctx context.Context, teamID string) (active *Cycle, all []Cycle, err error)
	States(ctx context.Context, teamID string) ([]State, error)
	Labels(ctx context.Context, teamID string) ([]Label, error)
	Members(ctx context.Context, teamID string) ([]User, error)
	MatchingIssues(ctx context.Context, p config.Poll, activeCycleID, viewerID string) ([]Issue, error)
	IssueLabelIDs(ctx context.Context, issueUUID string) ([]string, error)
	SetIssueLabels(ctx context.Context, issueUUID string, labelIDs []string) error
	CreateComment(ctx context.Context, issueUUID, body string) error
	SetIssueState(ctx context.Context, issueUUID, stateID string) error
}
