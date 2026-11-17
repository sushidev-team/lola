package projectgit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRemoteDisplayMetadataNeverContainsCredentials(t *testing.T) {
	cases := []struct {
		raw               string
		host, owner, repo string
	}{
		{"https://secret:token@github.com/team/project.git", "github.com", "team", "team/project"},
		{"git@github.com:person/repository.git", "github.com", "person", "person/repository"},
		{"ssh://git@github.com:22/Team/Repo.git", "github.com", "Team", "Team/Repo"},
		{"https://token@gitlab.com/team/project.git", "gitlab.com", "", ""},
		{"git@github.company:team/project.git", "github.company", "", ""},
		{"https://github.com/team/project.git?token=secret", "github.com", "", ""},
		{"https://github.com/evil%2Fname/project.git", "github.com", "", ""},
		{"/tmp/local-repository", "", "", ""},
	}
	for _, tc := range cases {
		r := remoteMetadata("origin", tc.raw)
		if r.Host != tc.host || r.Owner != tc.owner || r.Repo != tc.repo {
			t.Errorf("%q: %+v", tc.raw, r)
		}
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "token") {
			t.Fatalf("credentials escaped: %s", raw)
		}
		if tc.owner != "" && r.AvatarURL != "https://github.com/"+tc.owner+".png?size=40" {
			t.Errorf("avatar=%q", r.AvatarURL)
		}
	}
}

func TestRemotesReadActualGitConfigAndKeepFirstFetchURL(t *testing.T) {
	dir := repo(t)
	testGit(t, dir, "remote", "add", "origin", "https://user:secret@github.com/team/project.git")
	testGit(t, dir, "config", "--add", "remote.origin.url", "https://github.com/other/repo.git")
	testGit(t, dir, "remote", "add", "other", "git@gitlab.com:team/project.git")
	refs := (Client{}).Remotes(context.Background(), dir)
	if len(refs) != 2 || refs[0].Repo != "team/project" || refs[1].Host != "gitlab.com" {
		t.Fatalf("remotes=%+v", refs)
	}
	snap, err := (Client{}).Snapshot(context.Background(), dir, "", 200)
	if err != nil || len(snap.Remotes) != 2 {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
}

func TestIdentitiesBatchByCommitCacheAndRejectForeignAvatars(t *testing.T) {
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	calls := 0
	now := time.Unix(10, 0)
	client := IdentityClient{now: func() time.Time { return now }, run: func(_ context.Context, body []byte) ([]byte, error) {
		calls++
		var request struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.Variables["owner"] != "team" || request.Variables["name"] != "repo" || !strings.Contains(request.Query, `object(oid:"`+a+`")`) {
			t.Fatalf("query=%s", body)
		}
		return []byte(`{"data":{"repository":{"c0":{"author":{"avatarUrl":"https://avatars.githubusercontent.com/u/1?s=40","user":{"login":"developer","avatarUrl":"https://avatars.githubusercontent.com/u/1?s=40"}}},"c1":{"author":{"avatarUrl":"https://tracking.example/avatar","user":{"login":"bad/name","avatarUrl":"https://avatars.githubusercontent.com.evil/u/2"}}},"c2":null}}}`), nil
	}}
	result := client.Lookup(context.Background(), "team/repo", []string{a, b, c, a})
	if len(result.Authors) != 1 || result.Authors[a].Login != "developer" || calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
	client.Lookup(context.Background(), "team/repo", []string{a, b, c})
	if calls != 1 {
		t.Fatal("cached commits refetched")
	}
	now = now.Add(3 * time.Minute)
	client.run = func(_ context.Context, body []byte) ([]byte, error) {
		calls++
		if strings.Contains(string(body), a) {
			t.Fatal("positive cached author refetched")
		}
		return nil, errors.New("offline")
	}
	client.Lookup(context.Background(), "team/repo", []string{a, b, c})
	if calls != 2 {
		t.Fatal("negative cache never retried")
	}
}

func TestIdentityLimitsAndFailureStopNetworkWork(t *testing.T) {
	shas := []string{}
	for i := 0; i < 200; i++ {
		shas = append(shas, fmt.Sprintf("%040x", i))
	}
	calls := 0
	client := IdentityClient{run: func(_ context.Context, _ []byte) ([]byte, error) {
		calls++
		return nil, errors.New("authentication unavailable")
	}}
	client.Lookup(context.Background(), "team/repo", shas)
	client.Lookup(context.Background(), "team/repo", shas)
	if calls != 1 {
		t.Fatalf("failed requests retried: %d", calls)
	}
	for _, bad := range [][]string{{"--all"}, {"main"}, append(shas, strings.Repeat("a", 40))} {
		if ValidIdentitySHAs(bad) {
			t.Fatal("accepted invalid hashes")
		}
		client.Lookup(context.Background(), "team/repo", bad)
	}
	client.Lookup(context.Background(), "owner/repo/extra", shas)
	if calls != 1 {
		t.Fatal("invalid requests reached gh")
	}
}

func TestIdentityLookupBatchesAndKeepsGitHubBots(t *testing.T) {
	shas := []string{}
	for i := 0; i < 101; i++ {
		shas = append(shas, fmt.Sprintf("%040x", i))
	}
	calls := 0
	client := IdentityClient{run: func(_ context.Context, body []byte) ([]byte, error) {
		calls++
		if strings.Contains(string(body), "c100:") {
			t.Fatal("more than 100 aliases in a request")
		}
		return []byte(`{"data":{"repository":{"c0":{"author":{"user":{"login":"dependabot[bot]","avatarUrl":"https://avatars.githubusercontent.com/in/123?s=40"}}}}}}`), nil
	}}
	result := client.Lookup(context.Background(), "team/repo", shas)
	if calls != 2 || len(result.Authors) != 2 || result.Authors[shas[100]].Login != "dependabot[bot]" {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
}

func TestIdentityLookupKeepsPartialGraphQLData(t *testing.T) {
	sha := strings.Repeat("a", 40)
	client := IdentityClient{run: func(context.Context, []byte) ([]byte, error) {
		return []byte(`{"data":{"repository":{"c0":{"author":{"user":{"login":"developer","avatarUrl":"https://avatars.githubusercontent.com/u/1"}}},"c1":null}},"errors":[{"message":"A local commit is not published"}]}`), errors.New("partial GraphQL response")
	}}
	result := client.Lookup(context.Background(), "team/repo", []string{sha, strings.Repeat("b", 40)})
	if len(result.Authors) != 1 || result.Authors[sha].Login != "developer" {
		t.Fatalf("partial result=%+v", result)
	}
}
