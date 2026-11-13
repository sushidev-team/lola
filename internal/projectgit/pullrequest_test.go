package projectgit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type prFixture struct {
	mu                              sync.Mutex
	dir, base, head, remoteHead     string
	selection                       PRSelection
	posts                           []string
	payload                         map[string]any
	existing, failLabels, failUsers bool
	pushes                          int
}

func newPRFixture(t *testing.T) *prFixture {
	dir := repo(t)
	write(t, dir, "file.txt", "base\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "Initial")
	base := testGit(t, dir, "rev-parse", "HEAD")
	testGit(t, dir, "checkout", "-b", "feature/topic")
	write(t, dir, "file.txt", "base\ncommitted change\n")
	testGit(t, dir, "commit", "-am", "feat: add a committed change")
	head := testGit(t, dir, "rev-parse", "HEAD")
	testGit(t, dir, "remote", "add", "origin", "https://github.com/acme/repo.git")
	return &prFixture{dir: dir, base: base, head: head, remoteHead: head, selection: PRSelection{Remote: "origin", SourceRef: "refs/heads/feature/topic", SourceSHA: head, BaseRepo: "acme/repo", Base: "main"}}
}
func (f *prFixture) github(ctx context.Context, input string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) < 8 || args[1] != "--hostname" || args[2] != "github.com" {
		return nil, errors.New("unexpected GitHub invocation")
	}
	method, endpoint := args[4], args[7]
	if method == "POST" {
		f.posts = append(f.posts, endpoint)
		if strings.HasSuffix(endpoint, "/pulls") {
			_ = json.Unmarshal([]byte(input), &f.payload)
			return []byte(`{"number":42,"html_url":"https://github.com/acme/repo/pull/42"}`), nil
		}
		if f.failLabels && strings.HasSuffix(endpoint, "/labels") {
			return nil, errors.New("not permitted")
		}
		return []byte(`{}`), nil
	}
	switch {
	case endpoint == "repos/acme/repo":
		return []byte(`{"full_name":"acme/repo","default_branch":"main"}`), nil
	case endpoint == "repos/acme/repo/branches/main":
		return []byte(fmt.Sprintf(`{"name":"main","commit":{"sha":%q}}`, f.base)), nil
	case endpoint == "repos/acme/repo/branches/feature%2Ftopic":
		if f.remoteHead == "" {
			return nil, errors.New("GitHub: Not Found (HTTP 404)")
		}
		return []byte(fmt.Sprintf(`{"name":"feature/topic","commit":{"sha":%q}}`, f.remoteHead)), nil
	case strings.HasPrefix(endpoint, "repos/acme/repo/pulls?"):
		if f.existing {
			return []byte(`[{"number":12,"html_url":"https://github.com/acme/repo/pull/12"}]`), nil
		}
		return []byte(`[]`), nil
	case strings.Contains(endpoint, "/branches?"):
		return []byte(`[[{"name":"main"},{"name":"release"}]]`), nil
	case strings.Contains(endpoint, "/collaborators?"):
		if f.failUsers {
			return nil, errors.New("forbidden")
		}
		return []byte(`[[{"login":"reviewer","permissions":{"push":true}},{"login":"reader","permissions":{"push":false}}]]`), nil
	case strings.Contains(endpoint, "/assignees?"):
		return []byte(`[[{"login":"owner"}]]`), nil
	case strings.Contains(endpoint, "/labels?"):
		return []byte(`[[{"name":"feature"}]]`), nil
	case strings.Contains(endpoint, "/teams?"):
		return []byte(`[[{"name":"Engineering","slug":"engineering"}]]`), nil
	}
	return nil, fmt.Errorf("unexpected endpoint: %s", endpoint)
}
func (f *prFixture) client() PullRequestClient { return PullRequestClient{Run: f.github} }
func (f *prFixture) request() PRCreateRequest {
	return PRCreateRequest{Selection: f.selection, Title: "feat: add a committed change", Body: "## TL;DR\nA useful change.", Reviewers: []string{"reviewer", "team:engineering"}, Assignees: []string{"owner"}, Labels: []string{"feature"}}
}
func TestPROptionsAndCommittedPreview(t *testing.T) {
	f := newPRFixture(t)
	ctx := context.Background()
	p := f.client()
	options, err := p.Options(ctx, Client{}, f.dir, "origin", "")
	if err != nil || options.BaseRepo != "acme/repo" || len(options.Branches) != 2 || len(options.Reviewers) != 2 {
		t.Fatalf("%+v %v", options, err)
	}
	f.failUsers = true
	options, err = p.Options(ctx, Client{}, f.dir, "origin", "")
	if err != nil || len(options.Notices) != 1 {
		t.Fatalf("optional metadata blocked form: %+v %v", options, err)
	}
	write(t, f.dir, "file.txt", "unstaged private content\n")
	write(t, f.dir, "private.txt", "untracked private content\n")
	preview, err := p.Preview(ctx, Client{}, f.dir, f.selection)
	if err != nil || preview.NeedsPush || preview.Commits != 1 || preview.Files != 1 || !strings.Contains(preview.Context, "committed change") || strings.Contains(preview.Context, "private content") {
		t.Fatalf("%+v %v", preview, err)
	}
	if len(f.posts) > 0 {
		t.Fatal("preview published something")
	}
}
func TestPRCreateKeepsResultWhenMetadataFailsAndDoesNotDuplicate(t *testing.T) {
	f := newPRFixture(t)
	f.failLabels = true
	p := f.client()
	result, err := p.Create(context.Background(), Client{}, f.dir, f.request())
	if err != nil || result.Number != 42 || len(result.Warnings) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if f.payload["head"] != "acme:feature/topic" || f.payload["base"] != "main" || f.payload["draft"] != false || f.payload["title"] != "feat: add a committed change" {
		t.Fatalf("wrong payload: %+v", f.payload)
	}
	if len(f.posts) != 4 {
		t.Fatalf("missing metadata: %v", f.posts)
	}
	f.existing = true
	before := len(f.posts)
	result, err = p.Create(context.Background(), Client{}, f.dir, f.request())
	if err != nil || !result.Existing || result.Number != 12 || len(f.posts) != before {
		t.Fatalf("duplicate created: %+v %v", result, err)
	}
}
func TestPRPublishingIsExplicitPinnedAndNeverForcePushes(t *testing.T) {
	f := newPRFixture(t)
	f.remoteHead = ""
	p := f.client()
	r := f.request()
	ctx := context.Background()
	if _, err := p.Create(ctx, Client{}, f.dir, r); err == nil || !strings.Contains(err.Error(), "push this branch") {
		t.Fatalf("%v", err)
	}
	if len(f.posts) != 0 {
		t.Fatal("created before publishing consent")
	}
	r.Push = true
	c := Client{run: func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
		for _, arg := range args {
			if arg == "push" {
				f.pushes++
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "--force") || !strings.Contains(joined, "refs/heads/feature/topic:refs/heads/feature/topic") || !strings.Contains(joined, "--no-follow-tags") {
					t.Fatalf("unsafe push: %s", joined)
				}
				f.remoteHead = f.head
				return nil, nil
			}
		}
		return runGit(ctx, dir, input, args...)
	}}
	result, err := p.Create(ctx, c, f.dir, r)
	if err != nil || result.Number != 42 || f.pushes != 1 {
		t.Fatalf("%+v %v pushes=%d", result, err, f.pushes)
	}
	f.remoteHead = ""
	testGit(t, f.dir, "remote", "set-url", "--push", "origin", "https://github.com/other/elsewhere.git")
	if _, err = p.Create(ctx, c, f.dir, r); err == nil || !strings.Contains(err.Error(), "push destination") {
		t.Fatalf("%v", err)
	}
	if f.pushes != 1 {
		t.Fatal("published to a different destination")
	}
}
func TestPRRejectsStaleAndUnapprovedRequests(t *testing.T) {
	for _, kind := range []string{"stale", "repository", "same-branch", "draft-reviewers", "unknown-remote", "remote-stale"} {
		t.Run(kind, func(t *testing.T) {
			f := newPRFixture(t)
			r := f.request()
			switch kind {
			case "stale":
				r.Selection.SourceSHA = f.base
			case "repository":
				r.Selection.BaseRepo = "unrelated/project"
			case "same-branch":
				r.Selection.Base = "feature/topic"
			case "draft-reviewers":
				r.Draft = true
			case "unknown-remote":
				r.Selection.Remote = "--upload-pack=bad"
			case "remote-stale":
				r.Selection.SourceRef = "refs/remotes/origin/feature/topic"
				testGit(t, f.dir, "update-ref", r.Selection.SourceRef, f.head)
				f.remoteHead = f.base
			}
			if _, err := f.client().Create(context.Background(), Client{}, f.dir, r); err == nil {
				t.Fatal("request unexpectedly accepted")
			}
			if len(f.posts) > 0 {
				t.Fatal("rejected request reached publication")
			}
		})
	}
}

func TestPRForkDefaultsToParentAndRecoversAmbiguousCreation(t *testing.T) {
	f := newPRFixture(t)
	p := PullRequestClient{Run: func(ctx context.Context, input string, args ...string) ([]byte, error) {
		method, endpoint := args[4], args[7]
		if method == "GET" && endpoint == "repos/acme/repo" {
			return []byte(`{"full_name":"acme/repo","default_branch":"main","parent":{"full_name":"upstream/repo"}}`), nil
		}
		if method == "GET" && endpoint == "repos/upstream/repo" {
			return []byte(`{"full_name":"upstream/repo","default_branch":"main"}`), nil
		}
		if method == "POST" && endpoint == "repos/upstream/repo/pulls" {
			f.existing = true
			return nil, errors.New("connection interrupted after sending request")
		}
		forwarded := append([]string(nil), args...)
		forwarded[7] = strings.Replace(endpoint, "repos/upstream/repo/", "repos/acme/repo/", 1)
		return f.github(ctx, input, forwarded...)
	}}
	options, err := p.Options(context.Background(), Client{}, f.dir, "origin", "")
	if err != nil || options.BaseRepo != "upstream/repo" {
		t.Fatalf("wrong fork target: %+v %v", options, err)
	}
	request := f.request()
	request.Selection.BaseRepo = options.BaseRepo
	result, err := p.Create(context.Background(), Client{}, f.dir, request)
	if err != nil || result.Number != 12 || result.URL != "https://github.com/upstream/repo/pull/12" || len(result.Warnings) != 1 {
		t.Fatalf("ambiguous create lost PR: %+v %v", result, err)
	}
	if len(f.posts) != 0 {
		t.Fatal("retried creation or metadata after ambiguous response")
	}
}
