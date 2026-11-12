package projectgit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PullRequestClient owns GitHub calls. Run is an exec seam, never a bound input.
type PullRequestClient struct {
	Run func(context.Context, string, ...string) ([]byte, error)
}
type PRChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type PROptions struct {
	SourceRepo    string     `json:"sourceRepo"`
	Repositories  []string   `json:"repositories"`
	BaseRepo      string     `json:"baseRepo"`
	DefaultBranch string     `json:"defaultBranch"`
	Branches      []string   `json:"branches"`
	Reviewers     []PRChoice `json:"reviewers"`
	Assignees     []PRChoice `json:"assignees"`
	Labels        []PRChoice `json:"labels"`
	Notices       []string   `json:"notices"`
}
type PRSelection struct {
	Remote    string `json:"remote"`
	SourceRef string `json:"sourceRef"`
	SourceSHA string `json:"sourceSHA"`
	BaseRepo  string `json:"baseRepo"`
	Base      string `json:"base"`
}
type PRPreview struct {
	SourceRepo string `json:"sourceRepo"`
	Head       string `json:"head"`
	BaseSHA    string `json:"baseSHA"`
	NeedsPush  bool   `json:"needsPush"`
	Commits    int    `json:"commits"`
	Files      int    `json:"files"`
	Subject    string `json:"subject"`
	// Context is bounded server-side and used only by the explicit AI action.
	Context string `json:"-"`
}
type PRCreateRequest struct {
	Selection PRSelection `json:"selection"`
	Title     string      `json:"title"`
	Body      string      `json:"body"`
	Draft     bool        `json:"draft"`
	Push      bool        `json:"push"`
	Reviewers []string    `json:"reviewers"`
	Assignees []string    `json:"assignees"`
	Labels    []string    `json:"labels"`
}
type PRResult struct {
	Number   int      `json:"number"`
	URL      string   `json:"url"`
	Existing bool     `json:"existing"`
	Warnings []string `json:"warnings"`
}
type prRepo struct {
	FullName      string  `json:"full_name"`
	DefaultBranch string  `json:"default_branch"`
	Parent        *prRepo `json:"parent"`
}
type prBranch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

func (p PullRequestClient) api(ctx context.Context, method, endpoint string, body any, result any, pages bool) error {
	args := []string{"api", "--hostname", "github.com", "--method", method, "-H", "Accept: application/vnd.github+json", endpoint}
	input := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = string(raw)
		args = append(args, "--input", "-")
	}
	if pages {
		args = append(args, "--paginate", "--slurp")
	}
	run := p.Run
	if run == nil {
		run = func(ctx context.Context, input string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "gh", args...)
			cmd.Stdin = strings.NewReader(input)
			cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_PAGER=cat")
			cmd.WaitDelay = 2 * time.Second
			var out, stderr boundedBuffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				if errors.Is(err, exec.ErrNotFound) {
					return nil, errors.New("install and sign in to GitHub CLI (gh) to create pull requests")
				}
				if ctx.Err() != nil {
					return nil, errors.New("GitHub request timed out; try again")
				}
				return nil, fmt.Errorf("GitHub: %s", clipDraftText(strings.TrimSpace(stderr.String()), 2000))
			}
			return out.Bytes(), nil
		}
	}
	raw, err := run(ctx, input, args...)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	if err = json.Unmarshal(raw, result); err != nil {
		return errors.New("GitHub returned an unreadable response")
	}
	return nil
}
func prRemote(c Client, ctx context.Context, dir, name string) (Remote, error) {
	for _, r := range c.Remotes(ctx, dir) {
		if r.Name == name {
			if r.Host != "github.com" || r.Repo == "" {
				return r, errors.New("pull request creation currently supports GitHub remotes")
			}
			return r, nil
		}
	}
	return Remote{}, errors.New("choose an existing GitHub remote")
}
func (p PullRequestClient) repositories(ctx context.Context, c Client, dir, remote, target string) (Remote, []string, string, error) {
	r, err := prRemote(c, ctx, dir, remote)
	if err != nil {
		return r, nil, "", err
	}
	var repo prRepo
	if err = p.api(ctx, "GET", "repos/"+r.Repo, nil, &repo, false); err != nil {
		return r, nil, "", err
	}
	allowed := []string{r.Repo}
	if repo.Parent != nil && validGitHubRepo(repo.Parent.FullName) {
		allowed = append(allowed, repo.Parent.FullName)
	}
	for _, item := range c.Remotes(ctx, dir) {
		if item.Host == "github.com" && validGitHubRepo(item.Repo) && !containsPR(allowed, item.Repo) {
			allowed = append(allowed, item.Repo)
		}
	}
	if target == "" {
		target = r.Repo
		if repo.Parent != nil && containsPR(allowed, repo.Parent.FullName) {
			target = repo.Parent.FullName
		}
	}
	if !containsPR(allowed, target) {
		return r, nil, "", errors.New("choose a configured GitHub repository or the source fork's parent")
	}
	return r, allowed, target, nil
}
func containsPR(values []string, value string) bool {
	for _, v := range values {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}
func (p PullRequestClient) Options(ctx context.Context, c Client, dir, remote, target string) (PROptions, error) {
	r, repos, target, err := p.repositories(ctx, c, dir, remote, target)
	out := PROptions{SourceRepo: r.Repo, Repositories: repos, BaseRepo: target}
	if err != nil {
		return out, err
	}
	var info prRepo
	if err = p.api(ctx, "GET", "repos/"+target, nil, &info, false); err != nil {
		return out, err
	}
	out.DefaultBranch = info.DefaultBranch
	// Optional metadata must not hide the branch/title form when permissions are limited.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var branchErr error
	for _, kind := range []string{"branches", "collaborators", "assignees", "labels", "teams"} {
		wg.Add(1)
		go func(kind string) {
			defer wg.Done()
			var pages [][]struct {
				Name        string `json:"name"`
				Login       string `json:"login"`
				Slug        string `json:"slug"`
				Permissions struct {
					Push     bool `json:"push"`
					Admin    bool `json:"admin"`
					Maintain bool `json:"maintain"`
				} `json:"permissions"`
			}
			err := p.api(ctx, "GET", "repos/"+target+"/"+kind+"?per_page=100", nil, &pages, true)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if kind == "branches" {
					branchErr = err
				} else {
					out.Notices = append(out.Notices, "Could not load "+kind+"; you can add them on GitHub later.")
				}
				return
			}
			for _, page := range pages {
				for _, item := range page {
					switch kind {
					case "branches":
						out.Branches = append(out.Branches, item.Name)
					case "collaborators":
						if item.Permissions.Push || item.Permissions.Admin || item.Permissions.Maintain {
							out.Reviewers = append(out.Reviewers, PRChoice{item.Login, item.Login})
						}
					case "assignees":
						out.Assignees = append(out.Assignees, PRChoice{item.Login, item.Login})
					case "labels":
						out.Labels = append(out.Labels, PRChoice{item.Name, item.Name})
					case "teams":
						if item.Slug != "" {
							out.Reviewers = append(out.Reviewers, PRChoice{"team:" + item.Slug, item.Name + " (team)"})
						}
					}
				}
			}
		}(kind)
	}
	wg.Wait()
	return out, branchErr
}
func (c Client) prSource(ctx context.Context, dir string, s PRSelection) (Remote, string, error) {
	r, err := prRemote(c, ctx, dir, s.Remote)
	if err != nil {
		return r, "", err
	}
	local := strings.HasPrefix(s.SourceRef, "refs/heads/")
	if !local && !strings.HasPrefix(s.SourceRef, "refs/remotes/"+r.Name+"/") {
		return r, "", errors.New("choose a local branch or a branch from the source remote")
	}
	if _, err = c.branchRef(ctx, dir, s.SourceRef, s.SourceSHA, local); err != nil {
		return r, "", err
	}
	head := strings.TrimPrefix(s.SourceRef, "refs/heads/")
	if local {
		upstream, _ := c.text(ctx, dir, "for-each-ref", "--format=%(upstream)", s.SourceRef)
		if strings.HasPrefix(upstream, "refs/remotes/"+r.Name+"/") {
			head = strings.TrimPrefix(upstream, "refs/remotes/"+r.Name+"/")
		}
	} else {
		head = strings.TrimPrefix(s.SourceRef, "refs/remotes/"+r.Name+"/")
	}
	if head == "HEAD" || strings.HasPrefix(head, "-") {
		return r, "", errors.New("choose a branch tip")
	}
	if _, err = c.git(ctx, dir, "", "check-ref-format", "refs/heads/"+head); err != nil {
		return r, "", errors.New("invalid source branch")
	}
	return r, head, nil
}
func (p PullRequestClient) Preview(ctx context.Context, c Client, dir string, s PRSelection) (PRPreview, error) {
	if !validGitHubRepo(s.BaseRepo) {
		return PRPreview{}, errors.New("choose a destination repository")
	}
	r, head, err := c.prSource(ctx, dir, s)
	out := PRPreview{SourceRepo: r.Repo, Head: head}
	if err != nil {
		return out, err
	}
	if _, _, _, err = p.repositories(ctx, c, dir, s.Remote, s.BaseRepo); err != nil {
		return out, err
	}
	if s.Base == "" || strings.HasPrefix(s.Base, "-") {
		return out, errors.New("choose a base branch")
	}
	if _, err = c.git(ctx, dir, "", "check-ref-format", "refs/heads/"+s.Base); err != nil {
		return out, errors.New("invalid base branch")
	}
	if strings.EqualFold(r.Repo, s.BaseRepo) && head == s.Base {
		return out, errors.New("source and base must be different branches")
	}
	var base prBranch
	if err = p.api(ctx, "GET", "repos/"+s.BaseRepo+"/branches/"+url.PathEscape(s.Base), nil, &base, false); err != nil {
		return out, err
	}
	if !identitySHA.MatchString(base.Commit.SHA) {
		return out, errors.New("base branch has no valid commit")
	}
	out.BaseSHA = base.Commit.SHA
	var remote prBranch
	err = p.api(ctx, "GET", "repos/"+r.Repo+"/branches/"+url.PathEscape(head), nil, &remote, false)
	if err != nil && !strings.Contains(err.Error(), "HTTP 404") {
		return out, err
	}
	out.NeedsPush = err != nil || remote.Commit.SHA != s.SourceSHA
	if out.NeedsPush && !strings.HasPrefix(s.SourceRef, "refs/heads/") {
		return out, errors.New("source remote branch changed; fetch it and reopen the form")
	}
	// Local comparison can include not-yet-published commits without reading working files.
	mergeBase, err := c.text(ctx, dir, "merge-base", base.Commit.SHA, s.SourceSHA)
	if err != nil {
		return out, errors.New("fetch the destination repository before comparing these branches")
	}
	count, err := c.text(ctx, dir, "rev-list", "--count", mergeBase+".."+s.SourceSHA)
	if err != nil {
		return out, err
	}
	out.Commits, _ = strconv.Atoi(count)
	diff, err := c.Diff(ctx, dir, mergeBase, s.SourceSHA, "commit", "")
	if err != nil {
		return out, err
	}
	out.Files = len(diff.Files)
	out.Subject, _ = c.text(ctx, dir, "show", "-s", "--format=%s", s.SourceSHA, "--")
	subjects, _ := c.text(ctx, dir, "log", "-30", "--format=%s", mergeBase+".."+s.SourceSHA, "--")
	var text strings.Builder
	fmt.Fprintf(&text, "From %q branch %q to %q branch %q. %d commits, %d changed files.\nCommit subjects:\n%s\n", r.Repo, head, s.BaseRepo, s.Base, out.Commits, out.Files, clipDraftText(subjects, 2000))
	for _, f := range diff.Files {
		entry := fmt.Sprintf("\n%s %q (+%d -%d)\n%s\n", f.Status, f.Path, f.Additions, f.Deletions, clipDraftText(f.Patch, min(1600, 6000/max(1, len(diff.Files)))))
		if text.Len()+len(entry) > 10000 {
			break
		}
		text.WriteString(entry)
	}
	out.Context = text.String()
	return out, nil
}
func (p PullRequestClient) existing(ctx context.Context, repo, owner, head, base string) (PRResult, error) {
	var list []struct {
		Number int    `json:"number"`
		URL    string `json:"html_url"`
	}
	query := url.Values{"state": {"open"}, "head": {owner + ":" + head}, "base": {base}, "per_page": {"1"}}
	err := p.api(ctx, "GET", "repos/"+repo+"/pulls?"+query.Encode(), nil, &list, false)
	if err != nil {
		return PRResult{}, err
	}
	if len(list) > 0 {
		return PRResult{Number: list[0].Number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, list[0].Number), Existing: true}, nil
	}
	return PRResult{}, nil
}
func (p PullRequestClient) Create(ctx context.Context, c Client, dir string, r PRCreateRequest) (PRResult, error) {
	out := PRResult{}
	r.Title = strings.TrimSpace(r.Title)
	if r.Title == "" || len(r.Title) > 256 || strings.ContainsAny(r.Title, "\r\n\x00") || len(r.Body) > 65000 || strings.ContainsRune(r.Body, 0) {
		return out, errors.New("enter a title under 256 characters and a description under 65,000 characters")
	}
	if r.Draft && len(r.Reviewers) > 0 {
		return out, errors.New("reviewers can be requested after a draft is marked ready")
	}
	for _, list := range [][]string{r.Reviewers, r.Assignees, r.Labels} {
		if len(list) > 100 {
			return out, errors.New("too many metadata selections")
		}
		for _, value := range list {
			if value == "" || len(value) > 100 || strings.ContainsAny(value, "\r\n\x00") {
				return out, errors.New("invalid metadata selection")
			}
		}
	}
	preview, err := p.Preview(ctx, c, dir, r.Selection)
	if err != nil {
		return out, err
	}
	if preview.Commits == 0 {
		return out, errors.New("there are no commits to propose against this base")
	}
	source, head, err := c.prSource(ctx, dir, r.Selection)
	if err != nil {
		return out, err
	}
	existing, err := p.existing(ctx, r.Selection.BaseRepo, source.Owner, head, r.Selection.Base)
	if err != nil {
		return out, err
	}
	if existing.Number > 0 {
		return existing, nil
	}
	if preview.NeedsPush {
		if !r.Push {
			return out, errors.New("push this branch before creating the pull request")
		}
		// A different push URL must not publish to a repository absent from the form.
		pushURL, err := c.text(ctx, dir, "remote", "get-url", "--push", "--all", r.Selection.Remote)
		if err != nil {
			return out, err
		}
		urls := strings.Split(pushURL, "\n")
		if len(urls) != 1 || remoteMetadata(source.Name, urls[0]).Repo != source.Repo || remoteMetadata(source.Name, urls[0]).Host != "github.com" {
			return out, errors.New("the remote's push destination differs from the selected GitHub repository")
		}
		if err = c.publishBranch(ctx, dir, BranchRequest{Source: r.Selection.SourceRef, SourceSHA: r.Selection.SourceSHA, Remote: r.Selection.Remote, RemoteBranch: head}); err != nil {
			return out, err
		}
	}
	// Verify both the live local tip and GitHub head immediately before publication.
	current, currentHead, err := c.prSource(ctx, dir, r.Selection)
	if err != nil {
		return out, err
	}
	if current.Repo != source.Repo || currentHead != head {
		return out, errors.New("source destination changed; reopen the pull request form")
	}
	var branch prBranch
	if err = p.api(ctx, "GET", "repos/"+source.Repo+"/branches/"+url.PathEscape(head), nil, &branch, false); err != nil {
		return out, err
	}
	if branch.Commit.SHA != r.Selection.SourceSHA {
		return out, errors.New("GitHub's branch tip differs from the selected commit; fetch and try again")
	}
	var created struct {
		Number int    `json:"number"`
		URL    string `json:"html_url"`
	}
	payload := map[string]any{"title": r.Title, "body": r.Body, "head": source.Owner + ":" + head, "head_repo": strings.Split(source.Repo, "/")[1], "base": r.Selection.Base, "draft": r.Draft}
	if err = p.api(ctx, "POST", "repos/"+r.Selection.BaseRepo+"/pulls", payload, &created, false); err != nil {
		if found, e := p.existing(ctx, r.Selection.BaseRepo, source.Owner, head, r.Selection.Base); e == nil && found.Number > 0 {
			found.Warnings = []string{"GitHub created the pull request, but its metadata may not have been applied."}
			return found, nil
		}
		return out, err
	}
	if created.Number <= 0 {
		return out, errors.New("GitHub did not return a pull request number; check GitHub before retrying")
	}
	out = PRResult{Number: created.Number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", r.Selection.BaseRepo, created.Number)}
	endpoint := "repos/" + r.Selection.BaseRepo + "/issues/" + strconv.Itoa(out.Number)
	for _, item := range []struct {
		name   string
		values []string
	}{{"assignees", r.Assignees}, {"labels", r.Labels}} {
		if len(item.values) > 0 {
			if err = p.api(ctx, "POST", endpoint+"/"+item.name, map[string]any{item.name: item.values}, nil, false); err != nil {
				out.Warnings = append(out.Warnings, "Pull request created, but "+item.name+" could not be added: "+err.Error())
			}
		}
	}
	if len(r.Reviewers) > 0 {
		users, teams := []string{}, []string{}
		for _, name := range r.Reviewers {
			if strings.HasPrefix(name, "team:") {
				teams = append(teams, strings.TrimPrefix(name, "team:"))
			} else {
				users = append(users, name)
			}
		}
		if err = p.api(ctx, "POST", "repos/"+r.Selection.BaseRepo+"/pulls/"+strconv.Itoa(out.Number)+"/requested_reviewers", map[string]any{"reviewers": users, "team_reviewers": teams}, nil, false); err != nil {
			out.Warnings = append(out.Warnings, "Pull request created, but reviewers could not be requested: "+err.Error())
		}
	}
	return out, nil
}

func (c Client) CheckPRSource(ctx context.Context, dir string, selection PRSelection) error {
	_, _, err := c.prSource(ctx, dir, selection)
	return err
}
