package main

import (
	"context"
	"fmt"
	"github.com/sushidev-team/lola/internal/projectgit"
	"testing"
)

func TestProjectPRServicesAuthorizeBeforeGitHub(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	calls := 0
	s := &ProjectGitService{pullRequests: projectgit.PullRequestClient{Run: func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("should not reach GitHub")
	}}}
	selection := projectgit.PRSelection{Remote: "origin", SourceRef: "refs/heads/main", SourceSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BaseRepo: "acme/repo", Base: "main"}
	if _, err := s.PullRequestOptions("missing", "", "origin", ""); err == nil {
		t.Fatal("unknown project allowed")
	}
	if _, err := s.PreviewPullRequest("demo", t.TempDir(), selection); err == nil {
		t.Fatal("foreign checkout allowed")
	}
	if _, err := s.CreatePullRequest("missing", "", projectgit.PRCreateRequest{Selection: selection, Title: "test"}); err == nil {
		t.Fatal("unknown project created PR")
	}
	if _, err := s.DraftPullRequest("demo", t.TempDir(), selection, "", ""); err == nil {
		t.Fatal("foreign checkout reached draft")
	}
	if calls != 0 {
		t.Fatal("unauthorized request reached GitHub")
	}
}
