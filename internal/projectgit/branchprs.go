package projectgit

import (
	"context"
	"fmt"
	"strings"
)

// BranchPR is display metadata for an open pull request, including drafts.
// HeadRepo + Head identify the source; equal branch names across forks do not.
type BranchPR struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Repo     string `json:"repo"`
	HeadRepo string `json:"headRepo"`
	Head     string `json:"head"`
	Base     string `json:"base"`
	Draft    bool   `json:"draft"`
}

func (p PullRequestClient) BranchPRs(ctx context.Context, remotes []Remote) ([]BranchPR, error) {
	sources, targets := []string{}, []string{}
	for _, remote := range remotes {
		if remote.Host != "github.com" || !validGitHubRepo(remote.Repo) || containsPR(sources, remote.Repo) {
			continue
		}
		sources = append(sources, remote.Repo)
		targets = append(targets, remote.Repo)
	}
	// Include the parent so a fork's branches find PRs opened upstream even
	// when the parent has not been added as a local remote.
	for _, repo := range sources {
		var info prRepo
		if err := p.api(ctx, "GET", "repos/"+repo, nil, &info, false); err != nil {
			return nil, err
		}
		if info.Parent != nil && validGitHubRepo(info.Parent.FullName) && !containsPR(targets, info.Parent.FullName) {
			targets = append(targets, info.Parent.FullName)
		}
	}
	result := []BranchPR{}
	for _, repo := range targets {
		var pages [][]struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Draft  bool   `json:"draft"`
			Head   struct {
				Ref  string  `json:"ref"`
				Repo *prRepo `json:"repo"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		}
		if err := p.api(ctx, "GET", "repos/"+repo+"/pulls?state=open&per_page=100", nil, &pages, true); err != nil {
			return nil, err
		}
		for _, page := range pages {
			for _, pr := range page {
				if pr.Number <= 0 || pr.Head.Repo == nil || !containsPR(sources, pr.Head.Repo.FullName) || strings.TrimSpace(pr.Head.Ref) == "" {
					continue
				}
				result = append(result, BranchPR{Number: pr.Number, Title: clipDraftText(pr.Title, 500), URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, pr.Number), Repo: repo, HeadRepo: pr.Head.Repo.FullName, Head: pr.Head.Ref, Base: pr.Base.Ref, Draft: pr.Draft})
			}
		}
	}
	return result, nil
}
