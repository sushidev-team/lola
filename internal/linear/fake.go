package linear

import (
	"context"
	"slices"
	"sync"

	"github.com/sushidev-team/lola/internal/config"
)

var _ API = (*Fake)(nil)

// Call records one API invocation for order-sensitive assertions
// (seen-before-spawn ordering, identifier-vs-UUID usage).
type Call struct {
	Method string
	Args   []any
}

// Fake is an in-memory implementation of API for tests. All methods are
// safe for concurrent use — the daemon calls the client from goroutines.
//
// Errors are injected per method name via Errs (e.g. Errs["SetIssueLabels"]);
// an injected error is returned on every call until the entry is removed.
// Every invocation is appended to Calls in order, including failed ones.
type Fake struct {
	mu sync.Mutex

	// Fixtures, one per getter.
	ViewerUser        User
	TeamList          []Team
	ProjectsByTeam    map[string][]Project
	ActiveCycleByTeam map[string]*Cycle
	CyclesByTeam      map[string][]Cycle
	StatesByTeam      map[string][]State
	LabelsByTeam      map[string][]Label
	MembersByTeam     map[string][]User

	// MatchingIssues fixture: IssuesFunc wins when non-nil, else the
	// static Issues slice is returned.
	Issues     []Issue
	IssuesFunc func(p config.Poll, activeCycleID, viewerID string) ([]Issue, error)

	// LabelIDsByIssue (keyed by issue UUID) backs IssueLabelIDs and is
	// updated in place by SetIssueLabels, so tests observe the delta.
	LabelIDsByIssue map[string][]string

	// CommentsByIssue records successful CreateComment bodies per issue UUID
	// (in order) so tests can assert one-comment-per-transition.
	CommentsByIssue map[string][]string

	// StateByIssue records the last stateId set via SetIssueState per issue
	// UUID, so tests can assert the exact transition target.
	StateByIssue map[string]string

	// Errs injects an error per method name.
	Errs map[string]error

	// Calls is the ordered invocation log. Read it via CallLog/CallNames
	// while goroutines may still be running.
	Calls []Call
}

// record appends the call and returns any injected error. Caller must hold mu.
func (f *Fake) record(method string, args ...any) error {
	f.Calls = append(f.Calls, Call{Method: method, Args: args})
	return f.Errs[method]
}

// CallLog returns a copy of the ordered invocation log.
func (f *Fake) CallLog() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Calls)
}

// CallNames returns just the method names, in call order.
func (f *Fake) CallNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, len(f.Calls))
	for i, c := range f.Calls {
		names[i] = c.Method
	}
	return names
}

func (f *Fake) Viewer(ctx context.Context) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Viewer"); err != nil {
		return User{}, err
	}
	return f.ViewerUser, nil
}

func (f *Fake) Teams(ctx context.Context) ([]Team, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Teams"); err != nil {
		return nil, err
	}
	return slices.Clone(f.TeamList), nil
}

func (f *Fake) Projects(ctx context.Context, teamID string) ([]Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Projects", teamID); err != nil {
		return nil, err
	}
	return slices.Clone(f.ProjectsByTeam[teamID]), nil
}

func (f *Fake) Cycles(ctx context.Context, teamID string) (*Cycle, []Cycle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Cycles", teamID); err != nil {
		return nil, nil, err
	}
	var active *Cycle
	if c := f.ActiveCycleByTeam[teamID]; c != nil {
		cp := *c
		active = &cp
	}
	return active, slices.Clone(f.CyclesByTeam[teamID]), nil
}

func (f *Fake) States(ctx context.Context, teamID string) ([]State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("States", teamID); err != nil {
		return nil, err
	}
	return slices.Clone(f.StatesByTeam[teamID]), nil
}

func (f *Fake) Labels(ctx context.Context, teamID string) ([]Label, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Labels", teamID); err != nil {
		return nil, err
	}
	return slices.Clone(f.LabelsByTeam[teamID]), nil
}

func (f *Fake) Members(ctx context.Context, teamID string) ([]User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Members", teamID); err != nil {
		return nil, err
	}
	return slices.Clone(f.MembersByTeam[teamID]), nil
}

func (f *Fake) MatchingIssues(ctx context.Context, p config.Poll, activeCycleID, viewerID string) ([]Issue, error) {
	f.mu.Lock()
	err := f.record("MatchingIssues", p, activeCycleID, viewerID)
	fn := f.IssuesFunc
	static := slices.Clone(f.Issues)
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	// Invoke the user func outside the lock so it may call back into the fake.
	if fn != nil {
		return fn(p, activeCycleID, viewerID)
	}
	return static, nil
}

func (f *Fake) IssueLabelIDs(ctx context.Context, issueUUID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("IssueLabelIDs", issueUUID); err != nil {
		return nil, err
	}
	return slices.Clone(f.LabelIDsByIssue[issueUUID]), nil
}

func (f *Fake) SetIssueLabels(ctx context.Context, issueUUID string, labelIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("SetIssueLabels", issueUUID, slices.Clone(labelIDs)); err != nil {
		return err
	}
	if f.LabelIDsByIssue == nil {
		f.LabelIDsByIssue = map[string][]string{}
	}
	f.LabelIDsByIssue[issueUUID] = slices.Clone(labelIDs)
	return nil
}

func (f *Fake) CreateComment(ctx context.Context, issueUUID, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("CreateComment", issueUUID, body); err != nil {
		return err
	}
	if f.CommentsByIssue == nil {
		f.CommentsByIssue = map[string][]string{}
	}
	f.CommentsByIssue[issueUUID] = append(f.CommentsByIssue[issueUUID], body)
	return nil
}

func (f *Fake) SetIssueState(ctx context.Context, issueUUID, stateID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("SetIssueState", issueUUID, stateID); err != nil {
		return err
	}
	if f.StateByIssue == nil {
		f.StateByIssue = map[string]string{}
	}
	f.StateByIssue[issueUUID] = stateID
	return nil
}
