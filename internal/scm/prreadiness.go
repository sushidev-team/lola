package scm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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
	URL      string `json:"url"`
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
	Threads    []ReviewThread `json:"threads"`
}

var feedbackRepoRE = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

const readinessQuery = `query($owner:String!,$repo:String!,$number:Int!,$after:String){repository(owner:$owner,name:$repo){pullRequest(number:$number){number url state isDraft mergeable mergeStateStatus reviewDecision headRefOid commits(last:1){nodes{commit{statusCheckRollup{state}}}} reviewThreads(first:100,after:$after){pageInfo{hasNextPage endCursor} nodes{id isResolved isOutdated path line comments(first:1){nodes{bodyText url author{login}}}}}}}}`

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
						Number                                                              int
						URL, State, Mergeable, MergeStateStatus, ReviewDecision, HeadRefOid string
						IsDraft                                                             bool
						Commits                                                             struct {
							Nodes []struct {
								Commit struct{ StatusCheckRollup *struct{ State string } }
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
			result = PRReadiness{Number: pr.Number, URL: pr.URL, State: pr.State, Draft: pr.IsDraft, Mergeable: pr.Mergeable, MergeState: pr.MergeStateStatus, Review: pr.ReviewDecision, Head: pr.HeadRefOid, Checks: "none", Threads: []ReviewThread{}}
			if len(pr.Commits.Nodes) > 0 && pr.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
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
			}
		} else if pr.HeadRefOid != result.Head {
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
			return result, nil
		}
		if info.EndCursor == "" || (after != nil && info.EndCursor == *after) {
			return PRReadiness{}, fmt.Errorf("incomplete review thread pagination")
		}
		after = &info.EndCursor
	}
	return PRReadiness{}, fmt.Errorf("too many review threads to load completely; open GitHub")
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
