package scm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ReviewThread is an unresolved, resolvable GitHub review conversation, not an
// issue comment (which has no resolved state). Preview is plain text only.
type ReviewThread struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Outdated bool   `json:"outdated"`
	Author   string `json:"author"`
	Preview  string `json:"preview"`
	// Body retains the source before preview truncation, for display summaries.
	Body string `json:"-"`
	URL  string `json:"url"`
}

// PRReadiness contains facts, not a promise that GitHub will permit a merge.
type PRReadiness struct {
	Number     int            `json:"number"`
	URL        string         `json:"url"`
	State      string         `json:"state"`
	Draft      bool           `json:"draft"`
	Mergeable  string         `json:"mergeable"`
	MergeState string         `json:"mergeState"`
	Review     string         `json:"review"`
	Checks     string         `json:"checks"`
	Head       string         `json:"head"`
	Base       string         `json:"base"`
	ChecksHead string         `json:"checksHead"`
	Threads    []ReviewThread `json:"threads"`
	// BaseRef is the branch the PR merges into, so a caller can look up which
	// checks that branch requires.
	BaseRef string `json:"baseRef"`
	// CheckList is every check and commit status GitHub reports for
	// ChecksHead, in GitHub's order. It is nil when the rollup could not be
	// bound to the PR head, so a list is never shown for another commit, and
	// when its own best-effort read failed (readinessChecks): the list is
	// display-only and never fails the readiness read.
	// ChecksTruncated says GitHub holds more entries than were read.
	CheckList       []ReadinessCheck `json:"checkList"`
	ChecksTruncated bool             `json:"checksTruncated"`
}

// ReadinessCheck is one entry of the head commit's check rollup, as GitHub
// reported it. Every string is GitHub's (or a third party's) and untrusted:
// the caller clips it and opens URL only when it is https.
type ReadinessCheck struct {
	Name        string    `json:"name"`     // CheckRun job name, or a StatusContext's context
	Workflow    string    `json:"workflow"` // Actions workflow name; "" for a status or a non-Actions app
	State       string    `json:"state"`    // GitHub's word: a CheckRun status until COMPLETED, then its conclusion
	Bucket      string    `json:"bucket"`   // pass | fail | pending | skipped
	URL         string    `json:"url"`
	Description string    `json:"description"` // StatusContext description only
	Status      bool      `json:"status"`      // a commit status (StatusContext), not a CheckRun
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
}

// readinessBucket is checkBucket plus "skipped", which a display separates
// from a pass. The CI verdict still counts a skipped check as passing.
func readinessBucket(state string) string {
	switch strings.ToUpper(state) {
	case "SKIPPED", "NEUTRAL":
		return "skipped"
	}
	return checkBucket(state)
}

var feedbackRepoRE = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

const readinessQuery = `query($owner:String!,$repo:String!,$number:Int!,$after:String){repository(owner:$owner,name:$repo){pullRequest(number:$number){number url state isDraft mergeable mergeStateStatus reviewDecision headRefOid baseRefOid baseRefName commits(last:1){nodes{commit{oid statusCheckRollup{state}}}} reviewThreads(first:100,after:$after){pageInfo{hasNextPage endCursor} nodes{id isResolved isOutdated path line comments(first:1){nodes{bodyText url author{login}}}}}}}}`

// readinessChecksQuery reads the head's per-check list on its own. The list is
// display-only and reaches into Actions (checkSuite.workflowRun), which a token
// without Actions access cannot read, so it must never share a query with the
// facts the merge path depends on.
const readinessChecksQuery = `query($owner:String!,$repo:String!,$number:Int!){repository(owner:$owner,name:$repo){pullRequest(number:$number){number commits(last:1){nodes{commit{oid statusCheckRollup{contexts(first:100){totalCount nodes{__typename ... on CheckRun{name status conclusion detailsUrl startedAt completedAt checkSuite{workflowRun{workflow{name}}}} ... on StatusContext{context state targetUrl description createdAt}}}}}}}}}}`

// Readiness paginates all review threads. Partial results and unknown PRs are
// errors: neither may look like zero unresolved feedback. Every exec is bounded
// by runStdin and the caller's deadline; the page cap also bounds total output.
func (c *Client) Readiness(ctx context.Context, repo string, number int) (PRReadiness, error) {
	var result PRReadiness
	if !feedbackRepoRE.MatchString(repo) || number <= 0 {
		return result, fmt.Errorf("invalid pull request")
	}
	owner, name, _ := strings.Cut(repo, "/")
	var after *string
	for page := 0; page < 20; page++ {
		payload, _ := json.Marshal(map[string]any{"query": readinessQuery, "variables": map[string]any{"owner": owner, "repo": name, "number": number, "after": after}})
		out, stderr, err := c.runStdin(ctx, payload, "api", "graphql", "--input", "-")
		if err != nil {
			return PRReadiness{}, ghError("read pull request", err, stderr)
		}
		var response struct {
			Errors []json.RawMessage `json:"errors"`
			Data   struct {
				Repository struct {
					PullRequest *struct {
						Number                                                                                       int
						URL, State, Mergeable, MergeStateStatus, ReviewDecision, HeadRefOid, BaseRefOid, BaseRefName string
						IsDraft                                                                                      bool
						Commits                                                                                      struct {
							Nodes []struct {
								Commit struct {
									OID               string
									StatusCheckRollup *struct{ State string }
								}
							}
						}
						ReviewThreads struct {
							PageInfo struct {
								HasNextPage bool
								EndCursor   string
							}
							Nodes []struct {
								ID, Path               string
								Line                   int
								IsResolved, IsOutdated bool
								Comments               struct {
									Nodes []struct {
										BodyText, URL string
										Author        struct{ Login string }
									}
								}
							}
						}
					}
				}
			}
		}
		if err := json.Unmarshal(out, &response); err != nil {
			return PRReadiness{}, fmt.Errorf("read pull request: invalid response: %w", err)
		}
		pr := response.Data.Repository.PullRequest
		if len(response.Errors) > 0 || pr == nil || pr.Number != number {
			return PRReadiness{}, fmt.Errorf("pull request unavailable")
		}
		if page == 0 {
			result = PRReadiness{Number: pr.Number, URL: pr.URL, State: pr.State, Draft: pr.IsDraft, Mergeable: pr.Mergeable, MergeState: pr.MergeStateStatus, Review: pr.ReviewDecision, Head: pr.HeadRefOid, Base: pr.BaseRefOid, BaseRef: pr.BaseRefName, Checks: "none", Threads: []ReviewThread{}}
			// Never associate a rollup with the PR head by position alone.
			if len(pr.Commits.Nodes) != 1 || pr.Commits.Nodes[0].Commit.OID == "" || pr.Commits.Nodes[0].Commit.OID != pr.HeadRefOid {
				result.Checks = "unknown"
			} else {
				result.ChecksHead = pr.Commits.Nodes[0].Commit.OID
			}
			if result.ChecksHead != "" && pr.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
				switch pr.Commits.Nodes[0].Commit.StatusCheckRollup.State {
				case "SUCCESS":
					result.Checks = "pass"
				case "FAILURE", "ERROR":
					result.Checks = "fail"
				case "PENDING", "EXPECTED":
					result.Checks = "pending"
				default:
					result.Checks = "unknown"
				}
			} else if result.ChecksHead != "" {
				result.CheckList = []ReadinessCheck{} // the head has no checks at all
			}
		} else if pr.HeadRefOid != result.Head || pr.BaseRefOid != result.Base {
			return PRReadiness{}, fmt.Errorf("PR changed while loading; refresh to try again")
		}
		for _, t := range pr.ReviewThreads.Nodes {
			if t.IsResolved {
				continue
			}
			thread := ReviewThread{ID: t.ID, Path: t.Path, Line: t.Line, Outdated: t.IsOutdated}
			if len(t.Comments.Nodes) > 0 {
				cm := t.Comments.Nodes[0]
				thread.Author = cm.Author.Login
				thread.URL = cm.URL
				thread.Body = cm.BodyText
				preview := []rune(reviewPreview(cm.BodyText))
				if len(preview) > 600 {
					preview = append(preview[:600], '…')
				}
				thread.Preview = string(preview)
			}
			result.Threads = append(result.Threads, thread)
		}
		info := pr.ReviewThreads.PageInfo
		if !info.HasNextPage {
			if result.ChecksHead != "" && result.CheckList == nil {
				result.CheckList, result.ChecksTruncated = c.readinessChecks(ctx, owner, name, number, result.ChecksHead)
			}
			return result, nil
		}
		if info.EndCursor == "" || (after != nil && info.EndCursor == *after) {
			return PRReadiness{}, fmt.Errorf("incomplete review thread pagination")
		}
		after = &info.EndCursor
	}
	return PRReadiness{}, fmt.Errorf("too many review threads to load completely; open GitHub")
}

// readinessChecks reads the per-check list of head, best-effort: any failure
// (exec, a GraphQL error even on one node, a decode error, a head that moved
// since the readiness read) answers nil, which leaves the rest of the
// readiness answer exactly as it was read.
func (c *Client) readinessChecks(ctx context.Context, owner, repo string, number int, head string) ([]ReadinessCheck, bool) {
	payload, _ := json.Marshal(map[string]any{"query": readinessChecksQuery, "variables": map[string]any{"owner": owner, "repo": repo, "number": number}})
	out, _, err := c.runStdin(ctx, payload, "api", "graphql", "--input", "-")
	if err != nil {
		return nil, false
	}
	var response struct {
		Errors []json.RawMessage `json:"errors"`
		Data   struct {
			Repository struct {
				PullRequest *struct {
					Number  int
					Commits struct {
						Nodes []struct {
							Commit struct {
								OID               string
								StatusCheckRollup *struct {
									Contexts *struct {
										TotalCount int
										Nodes      []rollupContext
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if json.Unmarshal(out, &response) != nil || len(response.Errors) > 0 {
		return nil, false
	}
	pr := response.Data.Repository.PullRequest
	if pr == nil || pr.Number != number || len(pr.Commits.Nodes) != 1 || pr.Commits.Nodes[0].Commit.OID != head {
		return nil, false
	}
	rollup := pr.Commits.Nodes[0].Commit.StatusCheckRollup
	if rollup == nil {
		return []ReadinessCheck{}, false // the head has no checks at all
	}
	if rollup.Contexts == nil {
		return nil, false
	}
	list := make([]ReadinessCheck, 0, len(rollup.Contexts.Nodes))
	for _, n := range rollup.Contexts.Nodes {
		list = append(list, n.check())
	}
	return list, rollup.Contexts.TotalCount > len(rollup.Contexts.Nodes)
}

// rollupContext decodes both node shapes of a rollup's contexts: a CheckRun
// (name, status, conclusion) and a StatusContext (context, state).
type rollupContext struct {
	Typename    string `json:"__typename"`
	Name        string
	Status      string
	Conclusion  string
	DetailsURL  string `json:"detailsUrl"`
	StartedAt   time.Time
	CompletedAt time.Time
	CheckSuite  *struct {
		WorkflowRun *struct {
			Workflow *struct{ Name string }
		}
	}
	Context     string
	State       string
	TargetURL   string `json:"targetUrl"`
	Description string
	CreatedAt   time.Time
}

func (n rollupContext) check() ReadinessCheck {
	if n.Typename == "StatusContext" {
		return ReadinessCheck{Name: n.Context, State: n.State, Bucket: readinessBucket(n.State), URL: n.TargetURL, Description: n.Description, Status: true, StartedAt: n.CreatedAt}
	}
	state := n.Conclusion // a CheckRun reports its status until COMPLETED, then its conclusion
	if n.Status != "" && n.Status != "COMPLETED" {
		state = n.Status
	}
	c := ReadinessCheck{Name: n.Name, State: state, Bucket: readinessBucket(state), URL: n.DetailsURL, StartedAt: n.StartedAt, CompletedAt: n.CompletedAt}
	if n.CheckSuite != nil && n.CheckSuite.WorkflowRun != nil && n.CheckSuite.WorkflowRun.Workflow != nil {
		c.Workflow = n.CheckSuite.WorkflowRun.Workflow.Name
	}
	return c
}

// Lola's review renderer puts severity/category metadata above the actual
// finding and an agent prompt below it. Keep the finding in compact previews;
// the original conversation remains available on GitHub.
var reviewMetadataLine = regexp.MustCompile(`^(Critical|Major|Minor|Trivial) · [^\n]+$`)

func reviewPreview(body string) string {
	body = strings.TrimSpace(body)
	first, rest, found := strings.Cut(body, "\n")
	if found && reviewMetadataLine.MatchString(first) && strings.TrimSpace(rest) != "" {
		body = rest
	}
	if before, _, found := strings.Cut(body, "\n Prompt for AI Agents"); found && strings.TrimSpace(before) != "" {
		body = before
	}
	return strings.Join(strings.Fields(body), " ")
}
