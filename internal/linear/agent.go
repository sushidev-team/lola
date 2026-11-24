package linear

import (
	"context"
	"fmt"
)

// The Linear AGENT surface: the Agent Session API that lets lola act as a
// first-class Linear agent (assign / delegate / @mention). Every call here runs
// against an OAuth APP-ACTOR token (NewBearer), never the personal API key the
// poll loop uses — the app token's viewer IS the agent, which is what scopes
// `agentSessions` to the sessions opened on lola.
//
// Everything a session or an activity carries is written by humans in the
// workspace, so it is DATA: the daemon routes on ids only and hands prompt text
// to the agent through the same sanitize + idle gate a `lola answer` takes.

// AgentSession is one Agent Session as lola needs it: the trigger (a NEW
// session is a dispatch request) and the issue it is about.
type AgentSession struct {
	ID        string
	Status    string // pending|active|stopping|complete|awaitingInput|error|stale
	CreatedAt string
	URL       string
	Issue     *Issue // nil for a session not on an issue (lola ignores those)
	// Who is involved, as Linear user ids (empty when unknown). Routing data only:
	// they pick which daemon acts, they never gate a send or a write.
	CreatorID  string // the human who mentioned or delegated
	AssigneeID string
	// An app user (lola itself or another agent) is never recorded here: only a
	// human can own a session, so a delegate that is some other agent cannot hide
	// the assignee from every daemon.
	DelegateID string // the app user itself when a human delegated to lola
	// MentionBody is the text of the comment the human wrote when they mentioned
	// lola ("@lola go ahead"); empty for a plain delegation. UNTRUSTED text.
	MentionBody string
}

// AgentPrompt is one HUMAN message in a session (a `prompt` activity): a reply
// to an elicitation, a follow-up instruction, or a signal such as `stop`.
type AgentPrompt struct {
	ID        string
	Body      string
	Signal    string // "" | stop | continue | auth | select
	UserID    string
	CreatedAt string
}

// AgentActivity is one activity lola emits into a session. Content is the
// JSON object the API takes verbatim ({type, body} for thought / response /
// elicitation / error, {type, action, parameter, result} for an action).
type AgentActivity struct {
	Content        map[string]any
	Ephemeral      bool
	Signal         string         // "" | select | auth
	SignalMetadata map[string]any // e.g. {"options":[{"value":"Approve"}]} for select
}

// PlanStep is one entry of the session-level checklist (`agentSession.plan`).
type PlanStep struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending|inProgress|completed|canceled
}

// ExternalURL is one link shown on the session (the PR, typically).
type ExternalURL struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// AgentAPI is the agent-token surface the daemon's agent loop consumes. A
// separate interface from API on purpose: it authenticates as a different
// principal, and a fake for one must never be mistaken for the other.
type AgentAPI interface {
	AgentViewer(ctx context.Context) (User, error)
	AgentSessions(ctx context.Context) ([]AgentSession, error)
	AgentPrompts(ctx context.Context, sessionID, afterISO string) ([]AgentPrompt, error)
	CreateAgentActivity(ctx context.Context, sessionID string, a AgentActivity) error
	UpdateAgentPlan(ctx context.Context, sessionID string, plan []PlanStep) error
	SetAgentExternalURLs(ctx context.Context, sessionID string, urls []ExternalURL) error
}

// Thought / Action / Response / Elicitation / ErrorActivity build the content
// shapes Linear documents for each activity type.
func Thought(body string, ephemeral bool) AgentActivity {
	return AgentActivity{Content: map[string]any{"type": "thought", "body": body}, Ephemeral: ephemeral}
}

func Action(action, parameter, result string) AgentActivity {
	c := map[string]any{"type": "action", "action": action, "parameter": parameter}
	if result != "" {
		c["result"] = result
	}
	return AgentActivity{Content: c}
}

func Response(body string) AgentActivity {
	return AgentActivity{Content: map[string]any{"type": "response", "body": body}}
}

func ErrorActivity(body string) AgentActivity {
	return AgentActivity{Content: map[string]any{"type": "error", "body": body}}
}

// Elicitation asks the human something; with options it carries the `select`
// signal so Linear renders them as choices. The human's pick comes back as a
// prompt activity whose body is the option's value.
func Elicitation(body string, options ...string) AgentActivity {
	a := AgentActivity{Content: map[string]any{"type": "elicitation", "body": body}}
	if len(options) > 0 {
		opts := make([]map[string]any, 0, len(options))
		for _, o := range options {
			opts = append(opts, map[string]any{"value": o})
		}
		a.Signal = "select"
		a.SignalMetadata = map[string]any{"options": opts}
	}
	return a
}

// AgentViewer returns the app user the token acts as (the agent's own id —
// used to ignore lola's own activities and to recognise a delegation to it).
func (c *Client) AgentViewer(ctx context.Context) (User, error) {
	return c.Viewer(ctx)
}

// agentMaxPages bounds one cycle's session reads (50 per page). Sessions come
// newest first, so the pages walked cover every recently active session —
// enough for a queued delegation to be reconsidered for days — without an
// unbounded scan of the agent's whole history on every poll.
const agentMaxPages = 6

// AgentSessions returns this agent's sessions, most recently updated first,
// following the connection cursor for up to agentMaxPages pages. Pending
// sessions are the dispatch triggers; queued ones are reconsidered from here.
func (c *Client) AgentSessions(ctx context.Context) ([]AgentSession, error) {
	const q = `query($after: String){ agentSessions(first:50, after:$after, orderBy:updatedAt){
		nodes{ id status createdAt url
			creator{ id app } sourceComment{ body }
			issue{ id identifier title branchName priority createdAt
				team{ id } project{ id } assignee{ id app } delegate{ id app } } }
		pageInfo{ hasNextPage endCursor } } }`
	var (
		out   []AgentSession
		after any
	)
	for page := 0; page < agentMaxPages; page++ {
		var r struct {
			AgentSessions struct {
				Nodes []struct {
					ID, Status, CreatedAt string
					URL                   *string
					Creator               *struct {
						ID  string
						App bool
					}
					SourceComment *struct{ Body string }
					Issue         *struct {
						ID, Identifier, Title, BranchName, CreatedAt string
						Priority                                     float64
						Team                                         *struct{ ID string }
						Project                                      *struct{ ID string }
						Assignee, Delegate                           *struct {
							ID  string
							App bool
						}
					}
				}
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
			}
		}
		if err := c.do(ctx, q, map[string]any{"after": after}, &r); err != nil {
			return nil, err
		}
		for _, n := range r.AgentSessions.Nodes {
			s := AgentSession{ID: n.ID, Status: n.Status, CreatedAt: n.CreatedAt}
			if n.URL != nil {
				s.URL = *n.URL
			}
			if n.Creator != nil && !n.Creator.App {
				s.CreatorID = n.Creator.ID
			}
			if n.SourceComment != nil {
				s.MentionBody = n.SourceComment.Body
			}
			if n.Issue != nil {
				is := &Issue{ID: n.Issue.ID, Identifier: n.Issue.Identifier, Title: n.Issue.Title,
					BranchName: n.Issue.BranchName, Priority: n.Issue.Priority, CreatedAt: n.Issue.CreatedAt}
				if n.Issue.Team != nil {
					is.TeamID = n.Issue.Team.ID
				}
				if n.Issue.Project != nil {
					is.ProjectID = n.Issue.Project.ID
				}
				if n.Issue.Assignee != nil && !n.Issue.Assignee.App {
					s.AssigneeID = n.Issue.Assignee.ID
				}
				if n.Issue.Delegate != nil && !n.Issue.Delegate.App {
					s.DelegateID = n.Issue.Delegate.ID
				}
				s.Issue = is
			}
			out = append(out, s)
		}
		if !r.AgentSessions.PageInfo.HasNextPage || r.AgentSessions.PageInfo.EndCursor == "" {
			break
		}
		after = r.AgentSessions.PageInfo.EndCursor
	}
	return out, nil
}

// AgentPrompts returns the human prompt activities of one session created
// strictly after afterISO (all of them when it is empty), oldest first. It
// follows the cursor to the end, so the caller's watermark never advances past
// a prompt it was not shown.
func (c *Client) AgentPrompts(ctx context.Context, sessionID, afterISO string) ([]AgentPrompt, error) {
	const q = `query($f: AgentActivityFilter, $after: String){ agentActivities(filter:$f, first:50, after:$after, orderBy:createdAt){
		nodes{ id createdAt signal user{ id }
			content{ ... on AgentActivityPromptContent { body } } }
		pageInfo{ hasNextPage endCursor } } }`
	f := map[string]any{
		"agentSessionId": map[string]any{"eq": sessionID},
		"type":           map[string]any{"eq": "prompt"},
	}
	if afterISO != "" {
		f["createdAt"] = map[string]any{"gt": afterISO}
	}
	var (
		out   []AgentPrompt
		after any
	)
	for page := 0; ; page++ {
		if page == agentMaxPages*4 {
			return nil, fmt.Errorf("agent prompts: more than %d pages since the watermark", page)
		}
		var r struct {
			AgentActivities struct {
				Nodes []struct {
					ID, CreatedAt string
					Signal        *string
					User          *struct{ ID string }
					Content       struct{ Body string }
				}
				PageInfo struct {
					HasNextPage bool
					EndCursor   string
				}
			}
		}
		if err := c.do(ctx, q, map[string]any{"f": f, "after": after}, &r); err != nil {
			return nil, err
		}
		for _, n := range r.AgentActivities.Nodes {
			p := AgentPrompt{ID: n.ID, Body: n.Content.Body, CreatedAt: n.CreatedAt}
			if n.Signal != nil {
				p.Signal = *n.Signal
			}
			if n.User != nil {
				p.UserID = n.User.ID
			}
			out = append(out, p)
		}
		if !r.AgentActivities.PageInfo.HasNextPage || r.AgentActivities.PageInfo.EndCursor == "" {
			break
		}
		after = r.AgentActivities.PageInfo.EndCursor
	}
	// Deliver in the order the human wrote them, whichever direction the API
	// paginated.
	sortPromptsOldestFirst(out)
	return out, nil
}

// sortPromptsOldestFirst orders by createdAt (RFC3339 compares lexically) so
// the result does not depend on which direction the API paginated.
func sortPromptsOldestFirst(ps []AgentPrompt) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j].CreatedAt < ps[j-1].CreatedAt; j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

// CreateAgentActivity emits one activity into a session.
func (c *Client) CreateAgentActivity(ctx context.Context, sessionID string, a AgentActivity) error {
	const m = `mutation($in: AgentActivityCreateInput!){ agentActivityCreate(input:$in){ success } }`
	in := map[string]any{"agentSessionId": sessionID, "content": a.Content}
	if a.Ephemeral {
		in["ephemeral"] = true
	}
	if a.Signal != "" {
		in["signal"] = a.Signal
	}
	if a.SignalMetadata != nil {
		in["signalMetadata"] = a.SignalMetadata
	}
	var r struct{ AgentActivityCreate struct{ Success bool } }
	if err := c.do(ctx, m, map[string]any{"in": in}, &r); err != nil {
		return err
	}
	if !r.AgentActivityCreate.Success {
		return fmt.Errorf("agentActivityCreate: success=false")
	}
	return nil
}

// UpdateAgentPlan REPLACES the session's checklist (the API takes the whole
// array; it cannot patch one entry).
func (c *Client) UpdateAgentPlan(ctx context.Context, sessionID string, plan []PlanStep) error {
	return c.updateAgentSession(ctx, sessionID, map[string]any{"plan": plan})
}

// SetAgentExternalURLs REPLACES the session's external links.
func (c *Client) SetAgentExternalURLs(ctx context.Context, sessionID string, urls []ExternalURL) error {
	return c.updateAgentSession(ctx, sessionID, map[string]any{"externalUrls": urls})
}

func (c *Client) updateAgentSession(ctx context.Context, sessionID string, in map[string]any) error {
	const m = `mutation($id: String!, $in: AgentSessionUpdateInput!){ agentSessionUpdate(id:$id, input:$in){ success } }`
	var r struct{ AgentSessionUpdate struct{ Success bool } }
	if err := c.do(ctx, m, map[string]any{"id": sessionID, "in": in}, &r); err != nil {
		return err
	}
	if !r.AgentSessionUpdate.Success {
		return fmt.Errorf("agentSessionUpdate: success=false")
	}
	return nil
}

// Owner is the ONE human a session belongs to, by the shared routing order:
// the delegate, else the assignee, else the creator. The app user itself
// (appUserID) is skipped: delegating to lola sets the delegate to lola, which
// names no person. Every daemon computes the same answer from the same data, so
// at most one of them matches its own user id. "" means no human is known.
func (s AgentSession) Owner(appUserID string) string {
	for _, id := range []string{s.DelegateID, s.AssigneeID, s.CreatorID} {
		if id != "" && id != appUserID {
			return id
		}
	}
	return ""
}
