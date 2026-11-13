package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/board"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewpack"
	"github.com/sushidev-team/lola/internal/session"
)

func (d *Daemon) handleReviewPackageFeedback(ctx context.Context, req protocol.Request) protocol.Response {
	var a protocol.ReviewFeedbackArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return protocol.Response{Error: err.Error()}
	}
	if strings.TrimSpace(a.Note) == "" || len(a.Note) > 48000 || len(a.Section) > 500 {
		return protocol.Response{Error: "feedback requires a note up to 48000 bytes and section up to 500 bytes"}
	}
	p, err := reviewpack.Read(d.home, req.Session, a.Package)
	if err != nil {
		return protocol.Response{Error: err.Error()}
	}
	if p.Session != req.Session {
		return protocol.Response{Error: "select the package's repository"}
	}
	note := fmt.Sprintf("Review package %s\nRepository: %s; PR #%d\nBase: %s\nHead: %s\nSection: %s\n\n%s", p.ID, p.Repo, p.PR, p.Base, p.Head, a.Section, a.Note)
	data, err := d.handleFeedback(ctx, protocol.FeedbackArgs{Session: p.Session, Note: note})
	if err != nil {
		return protocol.Response{Error: err.Error()}
	}
	return dataResponse(data)
}

func (d *Daemon) handleReviewPackage(req protocol.Request) protocol.Response {
	fail := func(err error) protocol.Response { return protocol.Response{Error: err.Error()} }
	switch req.Cmd {
	case protocol.CmdReviewPublish, protocol.CmdReviewComplete:
		var args protocol.ReviewPublishArgs
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return fail(err)
		}
		manifest, err := reviewpack.Decode(args.Manifest)
		if err != nil {
			return fail(err)
		}
		id := req.Session
		if args.App != "" {
			if strings.ContainsAny(args.App, "~/\\") {
				return protocol.Response{Error: "invalid stack member"}
			}
			id += "~" + args.App
		}
		s, ok := d.sessions.Get(id)
		if !ok || s.Worktree == "" || (s.Stack && s.Lane == "") {
			return protocol.Response{Error: "review publication requires a session checkout; stacks must select --app"}
		}
		if !d.reports.allow(req.Session, time.Now()) {
			return fail(errReportRateLimited)
		}
		pr := 0
		if s.PR != nil {
			pr = s.PR.Number
		}
		d.reviewPackageMu.Lock()
		defer d.reviewPackageMu.Unlock()
		p, err := reviewpack.Publish(d.home, id, s.Worktree, s.Repo, pr, manifest)
		if err != nil {
			return fail(err)
		}
		// A stack shares its progress board across members. Completing one
		// repository must not claim the whole multi-repository task is done.
		if req.Cmd == protocol.CmdReviewComplete && s.Lane == "" {
			_, ok := d.sessions.Update(id, func(current *session.Session) bool {
				next, err := board.Apply(current.Board, []string{"phase", "done"}, time.Now())
				if err != nil {
					return false
				}
				current.Board = next
				return true
			})
			if !ok {
				return protocol.Response{Error: "material retained, but session ended before completion report"}
			}
			if err := d.sessions.Save(); err != nil {
				return fail(fmt.Errorf("material retained; completion report persistence: %w", err))
			}
		}
		return dataResponse(p)
	case protocol.CmdReviewPackages:
		// Retained packages remain readable after a session leaves the live store.
		packages, err := reviewpack.List(d.home, req.Session)
		if err != nil {
			return fail(err)
		}
		// A retained stack root can contain several members after live session
		// cleanup. Keep their inspection baselines separate in that view too.
		reviewed := map[string]*reviewpack.Review{}
		seen := map[string]bool{}
		for _, p := range packages {
			if seen[p.Session] {
				continue
			}
			seen[p.Session] = true
			r, err := reviewpack.Reviewed(d.home, p.Session)
			if err != nil {
				return fail(err)
			}
			if r != nil {
				reviewed[p.Session] = r
			}
		}
		// JSON projection keeps the protocol package independent of storage.
		return dataResponse(struct {
			ReviewTracking bool                          `json:"reviewTracking"`
			Packages       []reviewpack.Package          `json:"packages"`
			Reviewed       map[string]*reviewpack.Review `json:"reviewed,omitempty"`
		}{Packages: packages, Reviewed: reviewed, ReviewTracking: true})
	case protocol.CmdReviewMark:
		var args protocol.ReviewMarkArgs
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return fail(err)
		}
		d.reviewPackageMu.Lock()
		defer d.reviewPackageMu.Unlock()
		reviewed, err := reviewpack.MarkReviewed(d.home, req.Session, args.Package, args.Previous)
		if err != nil {
			return fail(err)
		}
		return dataResponse(reviewed)
	case protocol.CmdReviewImage:
		var args protocol.ReviewImageArgs
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return fail(err)
		}
		data, mime, err := reviewpack.Image(d.home, req.Session, args.Package, args.Artifact)
		if err != nil {
			return fail(err)
		}
		return dataResponse(protocol.ReviewImageData{DataURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
	}
	return protocol.Response{Error: "unknown review package command"}
}

func (d *Daemon) handleReviewDiff(ctx context.Context, req protocol.Request) protocol.Response {
	fail := func(err error) protocol.Response { return protocol.Response{Error: err.Error()} }
	var args protocol.ReviewDiffArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return fail(err)
	}
	from, err := reviewpack.Read(d.home, req.Session, args.From)
	if err != nil {
		return fail(err)
	}
	to, err := reviewpack.Read(d.home, req.Session, args.To)
	if err != nil {
		return fail(err)
	}
	if from.Session != req.Session || to.Session != req.Session || from.Repo != to.Repo || from.PR != to.PR {
		return protocol.Response{Error: "review packages belong to different repositories or pull requests"}
	}
	s, ok := d.sessions.Get(req.Session)
	if !ok || s.Worktree == "" || s.Repo != to.Repo || s.GoalManaged() || d.commitDiff == nil {
		return protocol.Response{Error: "Commit comparison requires the original live checkout. Retained write-ups and screenshots remain available."}
	}
	cctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	res, err := d.commitDiff(cctx, s.Worktree, from.Head, to.Head)
	if err != nil {
		return fail(err)
	}
	return dataResponse(diffData(s.ID, from.Head, from.Head, res))
}
