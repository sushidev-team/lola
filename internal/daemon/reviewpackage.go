package daemon

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewpack"
)

func (d *Daemon) handleReviewPackage(req protocol.Request) protocol.Response {
	fail := func(err error) protocol.Response { return protocol.Response{Error: err.Error()} }
	switch req.Cmd {
	case protocol.CmdReviewPublish:
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
		return dataResponse(p)
	case protocol.CmdReviewPackages:
		// Retained packages remain readable after a session leaves the live store.
		packages, err := reviewpack.List(d.home, req.Session)
		if err != nil {
			return fail(err)
		}
		// JSON projection keeps the protocol package independent of storage.
		return dataResponse(struct {
			Packages []reviewpack.Package `json:"packages"`
		}{Packages: packages})
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
