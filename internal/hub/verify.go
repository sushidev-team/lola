package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/sushidev-team/lola/hubproto"
)

// An offer is the hub's CLAIM about a repository, and a hub can be wrong or
// compromised. Before a daemon claims one it re-reads the facts from GitHub
// with the member's own gh, and the session is built from GITHUB'S answer,
// never the hub's fields:
//
//   - red_bump_pr: the PR must be open, authored by the bot the item names,
//     from a branch in the same repository, on exactly the branch the item
//     names, and still failing. Otherwise an offer naming `main` or a
//     teammate's branch would have every member's agent push there.
//   - dep_advisory: an OPEN Dependabot alert must match advisory, package and
//     ecosystem, and the briefing's range and fixed version come from that
//     alert. Otherwise a hub could name any package or version — a typosquat,
//     a malicious release — and the agent would install it.
//
// Every unknown (gh error, a field missing, a mismatch) refuses the offer.

// shaRe is a full git commit id (SHA-1 or SHA-256).
var shaRe = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ErrNotConfirmed is returned by Verify when GitHub does not confirm an offer.
var ErrNotConfirmed = errors.New("hub: GitHub does not confirm the offered item")

// Verified is an offer as GitHub confirms it.
type Verified struct {
	// Item is the offer rebuilt from GitHub's facts (same ID and kind).
	Item hubproto.Item
	// Head is the PR head commit at verification (red_bump_pr only): the
	// agent's work counts only once the head moved past it.
	Head string
}

func notConfirmed(why string) error { return fmt.Errorf("%w: %s", ErrNotConfirmed, why) }

// Verify re-reads an offered item from GitHub. It fails closed.
func (c *Collector) Verify(ctx context.Context, it hubproto.Item) (Verified, error) {
	if err := hubproto.Validate(it); err != nil {
		return Verified{}, notConfirmed(err.Error())
	}
	switch it.Kind {
	case hubproto.KindRedBumpPR:
		return c.verifyRedBump(ctx, it)
	case hubproto.KindDepAdvisory:
		return c.verifyAdvisory(ctx, it)
	}
	return Verified{}, notConfirmed("unknown kind")
}

type ghPRView struct {
	State             string `json:"state"`
	HeadRefName       string `json:"headRefName"`
	HeadRefOid        string `json:"headRefOid"`
	IsCrossRepository *bool  `json:"isCrossRepository"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
	StatusCheckRollup []struct {
		Name       string `json:"name"`
		Context    string `json:"context"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
}

func (c *Collector) verifyRedBump(ctx context.Context, it hubproto.Item) (Verified, error) {
	r := *it.RedBumpPR
	out, err := c.gh(ctx, "pr", "view", strconv.Itoa(r.PR), "--repo", r.Repo,
		"--json", "state,author,headRefName,headRefOid,isCrossRepository,statusCheckRollup")
	if err != nil {
		return Verified{}, err
	}
	var pr ghPRView
	if err := json.Unmarshal(out, &pr); err != nil {
		return Verified{}, notConfirmed("pull request JSON does not decode")
	}
	switch {
	case pr.State != "OPEN":
		return Verified{}, notConfirmed("the pull request is not open")
	case pr.IsCrossRepository == nil || *pr.IsCrossRepository:
		return Verified{}, notConfirmed("the pull request's branch is not in the repository")
	case botAuthor(pr.Author.Login) == "" || botAuthor(pr.Author.Login) != r.Author:
		return Verified{}, notConfirmed("the pull request is not by " + r.Author)
	case pr.HeadRefName != r.Branch:
		return Verified{}, notConfirmed("the pull request's branch is not the offered one")
	case !shaRe.MatchString(pr.HeadRefOid):
		return Verified{}, notConfirmed("the pull request has no head commit")
	}
	if !prFailing(ghPR{StatusCheckRollup: pr.StatusCheckRollup}) {
		return Verified{}, notConfirmed("the pull request's checks do not fail")
	}
	v := it
	v.RedBumpPR = &hubproto.RedBumpPR{Repo: r.Repo, PR: r.PR, Branch: pr.HeadRefName, Package: r.Package, Author: botAuthor(pr.Author.Login)}
	if err := hubproto.Validate(v); err != nil {
		return Verified{}, notConfirmed(err.Error())
	}
	return Verified{Item: v, Head: pr.HeadRefOid}, nil
}

func (c *Collector) verifyAdvisory(ctx context.Context, it hubproto.Item) (Verified, error) {
	d := *it.DepAdvisory
	alerts, err := c.alerts(ctx, d.Repo)
	if err != nil {
		return Verified{}, err
	}
	for _, a := range alerts {
		g := a.DepAdvisory
		if g.Advisory != d.Advisory || g.Package != d.Package || g.Ecosystem != d.Ecosystem {
			continue
		}
		v := a
		v.ID = it.ID
		if err := hubproto.Validate(v); err != nil {
			return Verified{}, notConfirmed(err.Error())
		}
		return Verified{Item: v}, nil
	}
	return Verified{}, notConfirmed("no open Dependabot alert matches")
}
