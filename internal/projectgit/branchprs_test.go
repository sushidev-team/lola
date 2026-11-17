package projectgit

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBranchPRsIncludesForkParentAndMatchesSourceRepository(t *testing.T) {
	var endpoints []string
	p := PullRequestClient{Run: func(_ context.Context, input string, args ...string) ([]byte, error) {
		if args[4] != "GET" || input != "" {
			t.Fatal("listing must be read-only")
		}
		endpoint := args[7]
		endpoints = append(endpoints, endpoint)
		switch endpoint {
		case "repos/me/fork":
			return []byte(`{"parent":{"full_name":"team/repo"}}`), nil
		case "repos/me/fork/pulls?state=open&per_page=100":
			return []byte(`[]`), nil
		case "repos/team/repo/pulls?state=open&per_page=100":
			if !strings.Contains(strings.Join(args, " "), "--paginate --slurp") {
				t.Fatal("missing pagination")
			}
			return []byte(`[[{"number":7,"title":"feat: first","head":{"ref":"topic","repo":{"full_name":"ME/FORK"}},"base":{"ref":"main"}},{"number":8,"head":{"ref":"topic","repo":{"full_name":"other/fork"}}},{"number":9,"head":{"ref":"deleted","repo":null}}],[{"number":10,"title":"Draft","draft":true,"html_url":"https://untrusted.test","head":{"ref":"second","repo":{"full_name":"me/fork"}},"base":{"ref":"develop"}}]]`), nil
		default:
			t.Fatalf("unexpected endpoint %q", endpoint)
			return nil, nil
		}
	}}
	prs, err := p.BranchPRs(context.Background(), []Remote{{Host: "github.com", Repo: "me/fork"}, {Host: "github.com", Repo: "ME/FORK"}, {Host: "gitlab.com", Repo: "team/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 3 || len(prs) != 2 || prs[0].Number != 7 || prs[0].HeadRepo != "ME/FORK" || prs[1].Number != 10 || !prs[1].Draft || prs[1].Base != "develop" || prs[1].URL != "https://github.com/team/repo/pull/10" {
		t.Fatalf("%+v; calls=%v", prs, endpoints)
	}
}

func TestBranchPRsSkipsUnsupportedRemotesAndReportsFailures(t *testing.T) {
	want := errors.New("offline")
	calls := 0
	p := PullRequestClient{Run: func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, want }}
	prs, err := p.BranchPRs(context.Background(), []Remote{{Host: "gitlab.com", Repo: "team/repo"}, {Host: "github.com", Repo: "../bad/path"}})
	if err != nil || calls != 0 || !reflect.DeepEqual(prs, []BranchPR{}) {
		t.Fatalf("%v %v calls=%d", prs, err, calls)
	}
	if _, err = p.BranchPRs(context.Background(), []Remote{{Host: "github.com", Repo: "team/repo"}}); err == nil {
		t.Fatal("failure hidden as no PRs")
	}
}
