// Package escalate turns escalated work items into ONE Linear issue per
// finding (or per fail group).
//
// "One" survives crashes, cancels, store failures and reopened items: every
// attempt is recorded BEFORE Linear is called (Tries, NextTry), and the issue
// is created under a client-supplied id derived from the org and the
// finding's dedup key (hubproto.EscalationIssueID), not from the hub item id.
// Every attempt first looks that id up, so an issue whose creation was never
// recorded, or that an earlier episode of the same finding filed, is found
// instead of created twice. Items failed with one fail group
// (hubproto.FailRequest.Group) share one issue, keyed by hubproto.GroupKey.
//
// Pending escalations are sent most severe first, then oldest first. A
// pending escalation for an item that is already done is dropped. When every
// item of a sent issue is done, the issue gets a comment and is closed.
//
// Each org's escalations are sent with that org's OWN Linear key (store.Org.
// LinearKey, sealed; opened with the hub's secret.Box only for the call) to
// that org's own team (store.Org.LinearTeam); the hub-wide team is a fallback
// only while the hub holds a single org. One org's key never files another
// org's issue. Each org may file at most MaxPerOrgPerDay issues a day.
//
// A key is never logged and never put in argv; it is stored only sealed.
// Issue text is built from VALIDATED item fields only, re-validated here. Escalation fails open: an issue that cannot
// be created stays pending and is retried with backoff; nothing else in the
// hub waits on it.
package escalate

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// EnvKey names the environment variable an OLDER hub read its one Linear key
// from. It is only adopted into a single-org hub at startup now
// (admin.AdoptLegacyEnv); every org holds its own key.
const EnvKey = "LOLA_HUB_LINEAR_KEY"

// EnvTeam names the environment variable holding the Linear team id (the
// --linear-team flag wins).
const EnvTeam = "LOLA_HUB_LINEAR_TEAM"

// DefaultEndpoint is Linear's GraphQL endpoint.
const DefaultEndpoint = "https://api.linear.app/graphql"

// ClosedMark is the Escalation.Err value of an issue the hub commented on
// and closed after its items were done. It is a hub constant like every
// other Err class, never Linear's text.
const ClosedMark = store.EscalationClosed

// Issue is what gets created.
type Issue struct {
	Title string
	Body  string
}

// Creator creates and finds Linear issues.
type Creator interface {
	// CreateIssue creates iss in teamID under the client-supplied issue id
	// and returns the issue's identifier (e.g. "ENG-12").
	CreateIssue(ctx context.Context, teamID, issueID string, iss Issue) (string, error)
	// FindIssue looks an issue up by id. found=false with a nil error means
	// Linear answered and has no such issue.
	FindIssue(ctx context.Context, issueID string) (identifier string, found bool, err error)
}

// Ref is a created or found issue: its identifier and, when the Creator
// knows it, its URL.
type Ref struct {
	Identifier string
	URL        string
	// Closed: Linear reports the issue completed or canceled (a found issue
	// only). An escalation that lands on a closed issue reopens it.
	Closed bool
}

// Linker is an optional Creator extension that also answers the issue URL.
type Linker interface {
	CreateIssueRef(ctx context.Context, teamID, issueID string, iss Issue) (Ref, error)
	FindIssueRef(ctx context.Context, issueID string) (Ref, bool, error)
}

// Closer is an optional Creator extension: comment on an issue (by its
// identifier) and move it to its team's first completed state.
type Closer interface {
	CloseIssue(ctx context.Context, identifier, comment string) error
}

// Reopener is an optional Creator extension: comment on a closed issue (by
// its identifier) and move it back to its team's first unstarted state. A
// finding that escalates again after its issue was closed lands on that same
// issue (the id is stable), so the issue must open again or nobody is told.
type Reopener interface {
	ReopenIssue(ctx context.Context, identifier, comment string) error
}

// issueNamespace is the UUIDv5 namespace of the hub's FIRST escalation ids,
// derived from the item id. New issues use hubproto.EscalationIssueID; this
// one is kept so a retry still finds an issue an older hub may have created.
var issueNamespace = [16]byte{0x6b, 0x0e, 0x3c, 0x51, 0x9a, 0x2d, 0x4f, 0x7e, 0x8c, 0x11, 0xd4, 0x52, 0xa0, 0x37, 0xe9, 0x6c}

// IssueID is the LEGACY issue id (a UUIDv5 of the hub item id). It is only
// looked up on a retry, never created under.
func IssueID(itemID string) string {
	h := sha1.New()
	h.Write(issueNamespace[:])
	h.Write([]byte(itemID))
	u := h.Sum(nil)[:16]
	u[6] = (u[6] & 0x0f) | 0x50 // version 5
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	x := hex.EncodeToString(u)
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

// Err classes stored on the item. A class, never a response body or a key.
var (
	ErrAuth    = errors.New("linear: unauthorized")
	ErrHTTP    = errors.New("linear: http error")
	ErrGraphQL = errors.New("linear: graphql error")
	ErrNetwork = errors.New("linear: network error")
	ErrNoState = errors.New("linear: no completed state")
	ErrNoTeam  = errors.New("linear: no such team")
)

// Linear is the GraphQL Creator.
type Linear struct {
	Key      string // an org's key, opened for this call; never logged
	Endpoint string // "" means DefaultEndpoint
	Client   *http.Client
}

const (
	issueCreate   = `mutation($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { identifier url } } }`
	issueFind     = `query($id: ID!) { issues(filter: { id: { eq: $id } }) { nodes { identifier url state { type } } } }`
	commentCreate = `mutation($input: CommentCreateInput!) { commentCreate(input: $input) { success } }`
	issueStates   = `query($id: String!) { issue(id: $id) { team { states(filter: { type: { eq: "completed" } }) { nodes { id position } } } } }`
	issueUpdate   = `mutation($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success } }`
	teamByKey     = `query($key: String!) { teams(filter: { key: { eq: $key } }) { nodes { id } } }`
	teamList      = `query { teams(first: 100) { nodes { id key name } } }`
	openStates    = `query($id: String!) { issue(id: $id) { team { states(filter: { type: { in: ["unstarted", "backlog"] } }) { nodes { id position type } } } } }`
)

type issueNode struct {
	Identifier string `json:"identifier"`
	URL        string `json:"url"`
	State      struct {
		Type string `json:"type"`
	} `json:"state"`
}

// CreateIssue implements Creator.
func (l *Linear) CreateIssue(ctx context.Context, teamID, issueID string, iss Issue) (string, error) {
	r, err := l.CreateIssueRef(ctx, teamID, issueID, iss)
	return r.Identifier, err
}

// FindIssue implements Creator.
func (l *Linear) FindIssue(ctx context.Context, issueID string) (string, bool, error) {
	r, found, err := l.FindIssueRef(ctx, issueID)
	return r.Identifier, found, err
}

// CreateIssueRef implements Linker.
func (l *Linear) CreateIssueRef(ctx context.Context, teamID, issueID string, iss Issue) (Ref, error) {
	raw, err := l.post(ctx, issueCreate, map[string]any{"input": map[string]any{
		"id": issueID, "teamId": teamID, "title": iss.Title, "description": iss.Body,
	}})
	if err != nil {
		return Ref{}, err
	}
	var out struct {
		Data struct {
			IssueCreate struct {
				Success bool      `json:"success"`
				Issue   issueNode `json:"issue"`
			} `json:"issueCreate"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) > 0 || !out.Data.IssueCreate.Success {
		return Ref{}, ErrGraphQL
	}
	return checkRef(out.Data.IssueCreate.Issue)
}

// FindIssueRef implements Linker.
func (l *Linear) FindIssueRef(ctx context.Context, issueID string) (Ref, bool, error) {
	raw, err := l.post(ctx, issueFind, map[string]any{"id": issueID})
	if err != nil {
		return Ref{}, false, err
	}
	var out struct {
		Data struct {
			Issues struct {
				Nodes []issueNode `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) > 0 {
		return Ref{}, false, ErrGraphQL
	}
	if len(out.Data.Issues.Nodes) == 0 {
		return Ref{}, false, nil
	}
	n := out.Data.Issues.Nodes[0]
	r, err := checkRef(n)
	r.Closed = n.State.Type == "completed" || n.State.Type == "canceled"
	return r, err == nil, err
}

// CloseIssue implements Closer: one comment, then the team's completed
// state with the lowest position.
func (l *Linear) CloseIssue(ctx context.Context, identifier, comment string) error {
	if !hubproto.ValidIssueIdentifier(identifier) {
		return ErrGraphQL
	}
	if err := l.mutate(ctx, commentCreate, map[string]any{"input": map[string]any{"issueId": identifier, "body": comment}}, "commentCreate"); err != nil {
		return err
	}
	raw, err := l.post(ctx, issueStates, map[string]any{"id": identifier})
	if err != nil {
		return err
	}
	var out struct {
		Data struct {
			Issue struct {
				Team struct {
					States struct {
						Nodes []struct {
							ID       string  `json:"id"`
							Position float64 `json:"position"`
						} `json:"nodes"`
					} `json:"states"`
				} `json:"team"`
			} `json:"issue"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) > 0 {
		return ErrGraphQL
	}
	nodes := out.Data.Issue.Team.States.Nodes
	if len(nodes) == 0 {
		return ErrNoState
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Position < nodes[j].Position })
	if !linearIDRe.MatchString(nodes[0].ID) {
		return ErrGraphQL
	}
	return l.mutate(ctx, issueUpdate, map[string]any{"id": identifier, "input": map[string]any{"stateId": nodes[0].ID}}, "issueUpdate")
}

// ReopenIssue implements Reopener: one comment, then the team's unstarted
// state with the lowest position (a backlog state when the team has no
// unstarted one).
func (l *Linear) ReopenIssue(ctx context.Context, identifier, comment string) error {
	if !hubproto.ValidIssueIdentifier(identifier) {
		return ErrGraphQL
	}
	if err := l.mutate(ctx, commentCreate, map[string]any{"input": map[string]any{"issueId": identifier, "body": comment}}, "commentCreate"); err != nil {
		return err
	}
	raw, err := l.post(ctx, openStates, map[string]any{"id": identifier})
	if err != nil {
		return err
	}
	var out struct {
		Data struct {
			Issue struct {
				Team struct {
					States struct {
						Nodes []struct {
							ID       string  `json:"id"`
							Position float64 `json:"position"`
							Type     string  `json:"type"`
						} `json:"nodes"`
					} `json:"states"`
				} `json:"team"`
			} `json:"issue"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) > 0 {
		return ErrGraphQL
	}
	nodes := out.Data.Issue.Team.States.Nodes
	if len(nodes) == 0 {
		return ErrNoState
	}
	// Unstarted before backlog, then by position.
	sort.SliceStable(nodes, func(i, j int) bool {
		if (nodes[i].Type == "unstarted") != (nodes[j].Type == "unstarted") {
			return nodes[i].Type == "unstarted"
		}
		return nodes[i].Position < nodes[j].Position
	})
	if !linearIDRe.MatchString(nodes[0].ID) {
		return ErrGraphQL
	}
	return l.mutate(ctx, issueUpdate, map[string]any{"id": identifier, "input": map[string]any{"stateId": nodes[0].ID}}, "issueUpdate")
}

// linearIDRe bounds an id Linear hands back before the hub sends it again.
var linearIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// teamKeyRe is a Linear team KEY ("ENG"): what `init --linear-team` resolves
// through the API; anything else is taken as an id.
var teamKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,9}$`)

// IsTeamKey reports whether s reads as a Linear team key rather than an id.
func IsTeamKey(s string) bool { return teamKeyRe.MatchString(s) }

// TeamID resolves a team KEY ("ENG") to the team's id.
func (l *Linear) TeamID(ctx context.Context, key string) (string, error) {
	if !IsTeamKey(key) {
		return "", ErrNoTeam
	}
	raw, err := l.post(ctx, teamByKey, map[string]any{"key": key})
	if err != nil {
		return "", err
	}
	var out struct {
		Data struct {
			Teams struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"teams"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) > 0 {
		return "", ErrGraphQL
	}
	if len(out.Data.Teams.Nodes) != 1 || !linearIDRe.MatchString(out.Data.Teams.Nodes[0].ID) {
		return "", ErrNoTeam
	}
	return out.Data.Teams.Nodes[0].ID, nil
}

// Teams lists the teams of the workspace the key belongs to (at most
// hubproto.MaxLinearTeams), by key. It doubles as the check that Linear
// accepts the key. A team whose id or key does not validate is left out; a
// name that does not validate is sent empty. Nothing else of Linear's answer
// leaves this function.
func (l *Linear) Teams(ctx context.Context) ([]hubproto.LinearTeam, error) {
	raw, err := l.post(ctx, teamList, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			Teams struct {
				Nodes []hubproto.LinearTeam `json:"nodes"`
			} `json:"teams"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) > 0 {
		return nil, ErrGraphQL
	}
	teams := []hubproto.LinearTeam{}
	for _, t := range out.Data.Teams.Nodes {
		if !hubproto.ValidLinearTeamID(t.ID) || !hubproto.ValidLinearTeamKey(t.Key) || len(teams) == hubproto.MaxLinearTeams {
			continue
		}
		if !hubproto.ValidLinearTeamName(t.Name) {
			t.Name = ""
		}
		teams = append(teams, t)
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].Key < teams[j].Key })
	return teams, nil
}

// mutate sends a mutation whose answer is {data: {<field>: {success}}}.
func (l *Linear) mutate(ctx context.Context, q string, vars map[string]any, field string) error {
	raw, err := l.post(ctx, q, vars)
	if err != nil {
		return err
	}
	var out struct {
		Data   map[string]struct{ Success bool } `json:"data"`
		Errors []json.RawMessage                 `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Errors) > 0 || !out.Data[field].Success {
		return ErrGraphQL
	}
	return nil
}

// checkRef keeps an identifier only when it looks like one, and a URL only
// when it is a plain https://linear.app link: both reach the console and the
// board.
func checkRef(n issueNode) (Ref, error) {
	if !hubproto.ValidIssueIdentifier(n.Identifier) {
		return Ref{}, ErrGraphQL
	}
	r := Ref{Identifier: n.Identifier}
	if hubproto.ValidateLinearURL(n.URL) == nil {
		r.URL = n.URL
	}
	return r, nil
}

// post sends one GraphQL request and returns the 200 body.
func (l *Linear) post(ctx context.Context, query string, vars map[string]any) ([]byte, error) {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return nil, err
	}
	ep := l.Endpoint
	if ep == "" {
		ep = DefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(payload))
	if err != nil {
		return nil, ErrNetwork
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", l.Key)
	c := l.Client
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		// *url.Error carries the URL only, but keep it a class anyway.
		return nil, ErrNetwork
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrAuth
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: status %d", ErrHTTP, resp.StatusCode)
	}
	return raw, nil
}

// lastPR is the newest valid PR URL an attempt or the outcome recorded ("" =
// none), with its delivery when the outcome carries it.
func lastPR(w *store.WorkItem) (string, hubproto.Delivery) {
	if o := w.Outcome; o != nil && hubproto.ValidatePRURL(o.PRURL) == nil && o.Delivery.Valid() {
		return o.PRURL, o.Delivery
	}
	for i := len(w.History) - 1; i >= 0; i-- {
		if u := w.History[i].PRURL; u != "" && hubproto.ValidatePRURL(u) == nil {
			return u, ""
		}
	}
	return "", ""
}

// BuildIssue renders an item's issue from validated fields only. An item that
// does not validate gets a generic text naming the hub item id and nothing
// else.
func BuildIssue(w *store.WorkItem) Issue {
	it := w.Item
	it.ID = ""
	why := "it failed " + itoa(w.Attempts) + " automated attempts"
	if w.Escalation != nil && w.Escalation.Reason == "critical" {
		why = "its severity is critical (it is also offered to members' agents)"
	}
	if hubproto.Validate(it) != nil {
		return Issue{
			Title: "lola-hub: chore " + safeID(w.ID) + " needs a person",
			Body:  "A lola-hub chore needs a person. Its fields did not validate, so they are not shown; inspect it with `lola-hub admin items list`.",
		}
	}
	var b strings.Builder
	title := itemTitle(it)
	switch it.Kind {
	case hubproto.KindDepAdvisory:
		d := it.DepAdvisory
		fmt.Fprintf(&b, "A dependency advisory needs a person because %s.\n\n", why)
		fmt.Fprintf(&b, "- Repository: `%s`\n- Ecosystem: `%s`\n- Package: `%s`\n- Vulnerable: `%s`\n", d.Repo, d.Ecosystem, d.Package, d.Vulnerable)
		if d.Fixed != "" {
			fmt.Fprintf(&b, "- Fixed in: `%s`\n", d.Fixed)
		}
		fmt.Fprintf(&b, "- Advisory: `%s`\n- Severity: `%s`\n", d.Advisory, d.Severity)
	case hubproto.KindRedBumpPR:
		r := it.RedBumpPR
		fmt.Fprintf(&b, "A dependency-bump PR with failing checks needs a person because %s.\n\n", why)
		fmt.Fprintf(&b, "- PR: https://github.com/%s/pull/%d\n- Branch: `%s`\n", r.Repo, r.PR, r.Branch)
		if r.Package != "" {
			fmt.Fprintf(&b, "- Package: `%s`\n", r.Package)
		}
		fmt.Fprintf(&b, "- Author: `%s`\n", r.Author)
	}
	if u, del := lastPR(w); u != "" {
		if del != "" {
			fmt.Fprintf(&b, "- Last attempt PR: %s (%s)\n", u, del)
		} else {
			fmt.Fprintf(&b, "- Last attempt PR: %s\n", u)
		}
	}
	if w.LastFail.Valid() {
		fmt.Fprintf(&b, "- Last failure: `%s`\n", w.LastFail)
	}
	fmt.Fprintf(&b, "\nlola-hub item `%s`, reported by %d member(s).\n", safeID(w.ID), w.Reporters)
	return Issue{Title: title, Body: b.String()}
}

// itemTitle is a one-line summary of a VALIDATED item.
func itemTitle(it hubproto.Item) string {
	switch it.Kind {
	case hubproto.KindDepAdvisory:
		d := it.DepAdvisory
		return fmt.Sprintf("Security: %s in %s (%s, %s)", d.Advisory, d.Repo, d.Package, d.Severity)
	case hubproto.KindRedBumpPR:
		r := it.RedBumpPR
		if r.Package == "" {
			return fmt.Sprintf("Red dependency bump: %s#%d (grouped)", r.Repo, r.PR)
		}
		return fmt.Sprintf("Red dependency bump: %s#%d (%s)", r.Repo, r.PR, r.Package)
	}
	return "lola-hub chore"
}

// BuildGroupIssue renders ONE issue for items failed under one fail group.
// Each item contributes a line from its validated fields; an item that does
// not validate contributes its hub id only.
func BuildGroupIssue(group string, items []*store.WorkItem) Issue {
	if len(items) == 1 {
		return BuildIssue(items[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d lola-hub chores failed together", len(items))
	if hubproto.ValidFailGroup(group) {
		fmt.Fprintf(&b, " (group `%s`)", group)
	}
	b.WriteString(" and need a person.\n\n")
	repos := map[string]bool{}
	for _, w := range items {
		it := w.Item
		it.ID = ""
		if hubproto.Validate(it) != nil {
			fmt.Fprintf(&b, "- lola-hub item `%s` (fields did not validate)\n", safeID(w.ID))
			continue
		}
		repos[it.Repo()] = true
		fmt.Fprintf(&b, "- %s, %s attempt(s), item `%s`", itemTitle(it), itoa(w.Attempts), safeID(w.ID))
		if u, _ := lastPR(w); u != "" {
			fmt.Fprintf(&b, ", last PR %s", u)
		}
		if w.LastFail.Valid() {
			fmt.Fprintf(&b, ", last failure `%s`", w.LastFail)
		}
		b.WriteString("\n")
	}
	title := fmt.Sprintf("lola-hub: %d chores failed together", len(items))
	if len(repos) == 1 {
		for r := range repos {
			title += " in " + r
		}
	}
	return Issue{Title: title, Body: b.String()}
}

// reopenComment is the comment posted when an escalation lands on an issue
// that was closed: hub text and validated item ids only.
func reopenComment(ids []string) string {
	var b strings.Builder
	b.WriteString("lola-hub: this chore escalated again after the issue was closed, so the issue is open again.\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "\n- item `%s`", safeID(id))
	}
	b.WriteString("\n")
	return b.String()
}

// closeComment is the comment posted before an issue is closed: validated
// outcome fields only.
func closeComment(items []*store.WorkItem) string {
	var b strings.Builder
	b.WriteString("lola-hub: this chore is done, so the issue is closed.\n")
	for _, w := range items {
		fmt.Fprintf(&b, "\n- item `%s`", safeID(w.ID))
		if u, del := lastPR(w); u != "" {
			fmt.Fprintf(&b, ": %s", u)
			if del != "" {
				fmt.Fprintf(&b, " (%s)", del)
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}

func safeID(id string) string {
	if hubproto.ValidItemID(id) {
		return id
	}
	return "(invalid id)"
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// Backoff bounds.
const (
	MinBackoff = time.Minute
	MaxBackoff = time.Hour
)

func backoff(tries int) time.Duration {
	d := MinBackoff
	for i := 1; i < tries && d < MaxBackoff; i++ {
		d *= 2
	}
	if d > MaxBackoff {
		d = MaxBackoff
	}
	return d
}

// CloseRetry is how long a failed close waits before it is tried again.
const CloseRetry = 15 * time.Minute

// DefaultMaxPerOrgPerDay caps the Linear issues one org may file in 24h.
const DefaultMaxPerOrgPerDay = 20

// Escalator drains pending escalations.
type Escalator struct {
	Store store.Store
	// Creator, when set, sends EVERY org's escalations (tests). Left nil,
	// each org's issues are sent with its own sealed key, opened with Box.
	Creator Creator
	// Box opens the orgs' sealed Linear keys. nil (and no Creator) disables
	// sending: items stay pending.
	Box *secret.Box
	// Open builds the Creator for an opened key (nil = the Linear API).
	Open func(key string) Creator
	// Team is the hub-wide Linear team id. It applies only while the hub
	// holds exactly one org; with several, each org needs its own
	// store.Org.LinearTeam or its escalations stay pending.
	Team            string
	MaxPerOrgPerDay int // 0 = DefaultMaxPerOrgPerDay
	Now             func() time.Time
	Logf            func(format string, args ...any)
}

func (e *Escalator) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Escalator) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

func (e *Escalator) perDay() int {
	if e.MaxPerOrgPerDay > 0 {
		return e.MaxPerOrgPerDay
	}
	return DefaultMaxPerOrgPerDay
}

// Enabled reports whether escalations can be sent at all. Whether an org's
// escalations go out also depends on its key and its team.
func (e *Escalator) Enabled() bool { return e.Creator != nil || e.Box != nil }

// creator is the Creator for org's issues, or nil when the org has no key
// the hub can open.
func (e *Escalator) creator(d *store.Data, org string) Creator {
	if e.Creator != nil {
		return e.Creator
	}
	o := d.Orgs[org]
	if o == nil || o.LinearKey == "" {
		return nil
	}
	key, err := e.Box.Open(org, secret.FieldLinearKey, o.LinearKey)
	if err != nil || !hubproto.ValidLinearKey(key) {
		return nil
	}
	if e.Open != nil {
		return e.Open(key)
	}
	return &Linear{Key: key}
}

// KeyReady reports whether org's escalations have a key to go out with.
func KeyReady(d *store.Data, org string, box *secret.Box) bool {
	o := d.Orgs[org]
	return o != nil && o.LinearKey != "" && box != nil
}

// TeamFor is the Linear team of an org's escalations ("" = none): the org's
// own, or the hub-wide one while the hub holds a single org.
func TeamFor(d *store.Data, org, hubTeam string) string {
	o := d.Orgs[org]
	if o == nil {
		return ""
	}
	if o.LinearTeam != "" {
		return o.LinearTeam
	}
	if len(d.Orgs) == 1 {
		return hubTeam
	}
	return ""
}

// Waiting counts org's pending escalations ("" = every org).
func Waiting(d *store.Data, org string) int {
	n := 0
	for _, w := range d.Items {
		if (org == "" || w.Org == org) && w.Escalation != nil && w.Escalation.Pending {
			n++
		}
	}
	return n
}

// severityRank orders sends: critical first. A red bump PR has no severity
// and ranks with moderate, as offers do.
func severityRank(s hubproto.Severity) int {
	switch s {
	case hubproto.SeverityCritical:
		return 4
	case hubproto.SeverityHigh:
		return 3
	case hubproto.SeverityLow:
		return 1
	}
	return 2
}

// escalationKey is the key an item's issue is filed under: its fail group's
// when it escalated by attempts with a valid group, else its dedup key.
func escalationKey(w *store.WorkItem) (key, group string) {
	if w.Escalation != nil && w.Escalation.Reason == "attempts" && hubproto.ValidFailGroup(w.FailGroup) {
		return hubproto.GroupKey(w.FailGroup), w.FailGroup
	}
	return w.Key, ""
}

type job struct {
	creator               Creator
	org, team, key, group string
	ids                   []string
	issueID               string
	iss                   Issue
	rank                  int
	age                   time.Time
}

// RunOnce sends every due pending escalation and closes issues whose items
// are done. It returns how many issues were sent and how many sends failed.
// The Linear calls run outside the store transactions.
func (e *Escalator) RunOnce(ctx context.Context) (sent, failed int) {
	if !e.Enabled() {
		return 0, 0
	}
	e.dropDone()
	now := e.now()
	var jobs []*job
	noTeam, noKey, capped := map[string]int{}, map[string]int{}, map[string]int{}
	_ = e.Store.View(func(d *store.Data) error {
		budget := map[string]int{}
		for _, w := range d.Items {
			if x := w.Escalation; x != nil && !x.SentAt.IsZero() && now.Sub(x.SentAt) < 24*time.Hour {
				budget[w.Org]++
			}
		}
		// Items of one org and key form one job (one issue).
		byKey := map[string]*job{}
		members := map[*job][]*store.WorkItem{}
		creators := map[string]Creator{}
		for _, w := range d.Items {
			x := w.Escalation
			if x == nil || !x.Pending || now.Before(x.NextTry) {
				continue
			}
			c, seen := creators[w.Org]
			if !seen {
				c = e.creator(d, w.Org)
				creators[w.Org] = c
			}
			if c == nil {
				noKey[w.Org]++
				continue
			}
			team := TeamFor(d, w.Org, e.Team)
			if team == "" {
				noTeam[w.Org]++
				continue
			}
			key, group := escalationKey(w)
			j := byKey[w.Org+"\x00"+key]
			if j == nil {
				j = &job{creator: c, org: w.Org, team: team, key: key, group: group, age: x.CreatedAt}
				byKey[w.Org+"\x00"+key] = j
				jobs = append(jobs, j)
			}
			members[j] = append(members[j], w)
			if r := severityRank(w.Item.Severity()); r > j.rank {
				j.rank = r
			}
			if x.CreatedAt.Before(j.age) {
				j.age = x.CreatedAt
			}
		}
		// Most severe first, then oldest: the daily cap holds back the
		// least urgent.
		sort.Slice(jobs, func(a, b int) bool {
			if jobs[a].rank != jobs[b].rank {
				return jobs[a].rank > jobs[b].rank
			}
			if !jobs[a].age.Equal(jobs[b].age) {
				return jobs[a].age.Before(jobs[b].age)
			}
			return jobs[a].key < jobs[b].key
		})
		kept := jobs[:0]
		for _, j := range jobs {
			if budget[j.org] >= e.perDay() {
				capped[j.org] += len(members[j])
				continue
			}
			budget[j.org]++
			ws := members[j]
			sort.Slice(ws, func(a, b int) bool { return ws[a].ID < ws[b].ID })
			for _, w := range ws {
				j.ids = append(j.ids, w.ID)
			}
			if j.group != "" {
				j.iss = BuildGroupIssue(j.group, ws)
			} else {
				j.iss = BuildIssue(ws[0])
			}
			j.issueID = hubproto.EscalationIssueID(j.org, j.key)
			kept = append(kept, j)
		}
		jobs = kept
		return nil
	})
	for org, n := range noKey {
		e.logf("escalate: org %s has %d pending escalation(s) and no usable Linear key", org, n)
	}
	for org, n := range noTeam {
		e.logf("escalate: org %s has %d pending escalation(s) and no Linear team (admin org linear)", org, n)
	}
	for org, n := range capped {
		e.logf("escalate: org %s reached %d issues in 24h; %d escalation(s) wait", org, e.perDay(), n)
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		if e.send(ctx, j) {
			sent++
		} else {
			failed++
		}
	}
	e.closeDone(ctx)
	return
}

// dropDone drops pending escalations of items that are already done: nobody
// needs to be asked about a chore a member's agent finished. It writes only
// when there is something to drop.
func (e *Escalator) dropDone() {
	due := false
	_ = e.Store.View(func(d *store.Data) error {
		for _, w := range d.Items {
			if w.State == hubproto.StateDone && w.Escalation != nil && w.Escalation.Pending {
				due = true
				return nil
			}
		}
		return nil
	})
	if !due {
		return
	}
	at := e.now().UTC()
	err := e.Store.Update(func(d *store.Data) error {
		for _, w := range d.Items {
			if w.State == hubproto.StateDone && w.Escalation != nil && w.Escalation.Pending {
				w.Escalation = nil
				d.Log(store.Event{At: at, Org: w.Org, Kind: "escalation_dropped", Item: w.ID, Detail: "item done"})
			}
		}
		return nil
	})
	if err != nil {
		e.logf("escalate: drop escalations of done items: %v", err)
	}
}

// send makes one attempt for j and reports whether the issue now exists.
func (e *Escalator) send(ctx context.Context, j *job) bool {
	// Record the attempt FIRST, so a crash, a cancel or a store that cannot
	// record the result never turns into an immediate second attempt.
	at := e.now()
	retry := false
	var live []string
	err := e.Store.Update(func(d *store.Data) error {
		for _, id := range j.ids {
			w := d.Items[id]
			if w == nil || w.Escalation == nil || !w.Escalation.Pending || at.Before(w.Escalation.NextTry) {
				continue
			}
			x := w.Escalation
			retry = retry || x.Tries > 0
			x.Tries++
			x.NextTry = at.Add(backoff(x.Tries)).UTC()
			live = append(live, id)
		}
		if len(live) == 0 {
			return errSkip
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, errSkip) {
			e.logf("escalate: record attempt for %s: %v (not sent)", j.ids[0], err)
		}
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// The stable id may already name an issue: an unrecorded earlier
	// create, an earlier episode of the same finding, or the group's issue
	// another item already filed. On a retry, an older hub's per-item id
	// is looked up too.
	ids := []string{}
	if j.issueID != "" {
		ids = append(ids, j.issueID)
	}
	if retry {
		for _, id := range live {
			ids = append(ids, IssueID(id))
		}
	}
	var ref Ref
	found := false
	for _, id := range ids {
		if ref, found, err = find(cctx, j.creator, id); err != nil || found {
			break
		}
	}
	if err == nil && !found {
		if j.issueID == "" {
			err = ErrGraphQL // an invalid org or key: never file under a guessed id
		} else {
			ref, err = create(cctx, j.creator, j.team, j.issueID, j.iss)
		}
	}
	if err == nil && found && ref.Closed {
		// The stable id names an issue that was closed: an earlier episode
		// of this finding (resolved, re-reported, escalated again) or a
		// purged and re-created item. Marking it sent would tell nobody, so
		// it is reopened; its id is taken for good, so a new one cannot be
		// filed under it.
		if r, ok := j.creator.(Reopener); ok {
			err = r.ReopenIssue(cctx, ref.Identifier, reopenComment(live))
		} else {
			e.logf("escalate: item %s: issue %s is closed and this Linear client cannot reopen it", j.ids[0], ref.Identifier)
		}
	}
	done := e.now().UTC()
	uerr := e.Store.Update(func(d *store.Data) error {
		changed := false
		// The org's settings status: the class of the latest send, "" once
		// one worked.
		if o := d.Orgs[j.org]; o != nil {
			class := ""
			if err != nil {
				class = errClass(err)
			}
			if o.LinearErr != class {
				o.LinearErr, changed = class, true
			}
		}
		for _, id := range live {
			w := d.Items[id]
			if w == nil || w.Escalation == nil || !w.Escalation.Pending {
				continue
			}
			changed = true
			x := w.Escalation
			if err != nil {
				x.Err = errClass(err)
				d.Log(store.Event{At: done, Org: w.Org, Kind: "escalation_failed", Item: w.ID, Detail: x.Err})
				continue
			}
			x.Pending, x.Err, x.LinearID, x.URL, x.SentAt = false, "", ref.Identifier, ref.URL, done
			d.Log(store.Event{At: done, Org: w.Org, Kind: "escalated", Item: w.ID, Detail: ref.Identifier})
		}
		if !changed {
			return errSkip
		}
		return nil
	})
	if uerr != nil && !errors.Is(uerr, errSkip) {
		// The next retry looks the issue up before creating one.
		e.logf("escalate: record result for %s: %v", j.ids[0], uerr)
	}
	if err != nil {
		e.logf("escalate: item %s: %s (will retry)", j.ids[0], errClass(err))
		return false
	}
	return true
}

func find(ctx context.Context, c Creator, id string) (Ref, bool, error) {
	if l, ok := c.(Linker); ok {
		return l.FindIssueRef(ctx, id)
	}
	ident, found, err := c.FindIssue(ctx, id)
	if err != nil || !found {
		return Ref{}, found, err
	}
	r, err := checkRef(issueNode{Identifier: ident})
	return r, err == nil, err
}

func create(ctx context.Context, c Creator, team, id string, iss Issue) (Ref, error) {
	if l, ok := c.(Linker); ok {
		return l.CreateIssueRef(ctx, team, id, iss)
	}
	ident, err := c.CreateIssue(ctx, team, id, iss)
	if err != nil {
		return Ref{}, err
	}
	return checkRef(issueNode{Identifier: ident})
}

// closeDone comments on and closes every sent issue whose items are all
// done (or failed: closed as no longer reproducible), when the org's Creator
// can.
func (e *Escalator) closeDone(ctx context.Context) {
	now := e.now()
	type closeJob struct {
		cl                  Closer
		org, ident, comment string
		ids                 []string
	}
	var jobs []closeJob
	_ = e.Store.View(func(d *store.Data) error {
		groups := map[string][]*store.WorkItem{}
		var order []string
		for _, w := range d.Items {
			x := w.Escalation
			if x == nil || x.Pending || x.LinearID == "" || x.Err == ClosedMark {
				continue
			}
			k := w.Org + "\x00" + x.LinearID
			if groups[k] == nil {
				order = append(order, k)
			}
			groups[k] = append(groups[k], w)
		}
		sort.Strings(order)
		for _, k := range order {
			ws := groups[k]
			anyDone, due := false, true
			for _, w := range ws {
				switch w.State {
				case hubproto.StateDone:
					anyDone = true
				case hubproto.StateFailed:
				default:
					due = false
				}
				if now.Before(w.Escalation.NextTry) && w.Escalation.Err != "" {
					due = false // a failed close waits for its retry
				}
			}
			if !due || !anyDone {
				continue
			}
			cl, ok := e.creator(d, ws[0].Org).(Closer)
			if !ok {
				continue
			}
			sort.Slice(ws, func(a, b int) bool { return ws[a].ID < ws[b].ID })
			cj := closeJob{cl: cl, org: ws[0].Org, ident: ws[0].Escalation.LinearID, comment: closeComment(ws)}
			for _, w := range ws {
				cj.ids = append(cj.ids, w.ID)
			}
			jobs = append(jobs, cj)
		}
		return nil
	})
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := j.cl.CloseIssue(cctx, j.ident, j.comment)
		cancel()
		at := e.now().UTC()
		uerr := e.Store.Update(func(d *store.Data) error {
			for _, id := range j.ids {
				w := d.Items[id]
				if w == nil || w.Escalation == nil || w.Escalation.LinearID != j.ident {
					continue
				}
				if err != nil {
					w.Escalation.Err = errClass(err)
					w.Escalation.NextTry = at.Add(CloseRetry)
					continue
				}
				w.Escalation.Err = ClosedMark
				d.Log(store.Event{At: at, Org: w.Org, Kind: "escalation_closed", Item: w.ID, Detail: j.ident})
			}
			return nil
		})
		if uerr != nil {
			e.logf("escalate: record close of %s: %v", j.ident, uerr)
		}
		if err != nil {
			e.logf("escalate: close %s: %s (will retry)", j.ident, errClass(err))
		}
	}
}

var errSkip = errors.New("skip")

func errClass(err error) string {
	for _, c := range []error{ErrAuth, ErrHTTP, ErrGraphQL, ErrNetwork, ErrNoState, ErrNoTeam} {
		if errors.Is(err, c) {
			return c.Error()
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "linear: timeout"
	}
	return "linear: error"
}

// Run calls RunOnce every interval until ctx ends. It never panics out.
func (e *Escalator) Run(ctx context.Context, interval time.Duration) {
	if !e.Enabled() {
		e.logf("escalate: disabled (no %s, so no org's Linear key can be opened); escalations stay pending", secret.EnvKey)
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					e.logf("escalate: panic recovered: %v", r)
				}
			}()
			e.RunOnce(ctx)
		}()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
