package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewpack"
	"github.com/sushidev-team/lola/internal/reviewverify"
)

func (d *Daemon) handleReviewVerification(ctx context.Context, req protocol.Request) protocol.Response {
	fail := func(err error) protocol.Response { return protocol.Response{Error: err.Error()} }
	var a protocol.ReviewVerifyArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return fail(err)
	}
	p, err := reviewpack.Read(d.home, req.Session, a.Package)
	if err != nil {
		return fail(err)
	}
	if p.Session != req.Session {
		return fail(errors.New("select the package's repository"))
	}
	d.mu.Lock()
	docker := "docker"
	if d.cfg != nil {
		docker = d.cfg.Sandbox.DockerBin()
	}
	d.mu.Unlock()
	r := reviewverify.Runner{Home: d.home, Docker: docker, Run: d.reviewCheckRun}
	d.reviewPackageMu.Lock()
	defer d.reviewPackageMu.Unlock()
	key := p.ID + ":" + a.ID
	if req.Cmd == protocol.CmdReviewChecks {
		all, err := r.List(p)
		if err != nil {
			return fail(err)
		}
		for i := range all {
			if !all[i].Stopped && !d.reviewJobs[p.ID+":"+all[i].ID] {
				all[i].State = "interrupted"
			}
		}
		return dataResponse(struct {
			Checks []reviewverify.Record `json:"checks"`
		}{all})
	}
	if req.Cmd == protocol.CmdReviewRecover {
		if d.reviewJobs[key] {
			return fail(errors.New("check is still running; wait for its bounded timeout"))
		}
		all, err := r.List(p)
		if err != nil {
			return fail(err)
		}
		for _, v := range all {
			if v.ID == a.ID {
				v, err = r.Recover(ctx, v)
				if err != nil {
					return fail(err)
				}
				return dataResponse(v)
			}
		}
		return fail(errors.New("verification not found"))
	}
	s, ok := d.sessions.Get(p.Session)
	if !ok || s.Worktree == "" || s.Repo != p.Repo || s.GoalManaged() || s.Sandbox != "" && s.Sandbox != "none" {
		return fail(errors.New("independent review checks require the original host checkout; goal and sandbox sessions use their isolated verification workflow"))
	}
	if len(d.reviewJobs) >= 2 && !d.reviewJobs[key] {
		return fail(errors.New("two review checks are already running; try again after one finishes"))
	}
	v, fresh, err := r.Reserve(p, a.ID, a.Spec)
	if err != nil {
		return fail(err)
	}
	if !fresh {
		return dataResponse(v)
	}
	if !d.beginConnWork() {
		v.State, v.Stopped, v.Error = "error", true, "daemon is shutting down"
		_ = r.Save(v)
		return fail(errors.New(v.Error))
	}
	if d.reviewJobs == nil {
		d.reviewJobs = map[string]bool{}
	}
	d.reviewJobs[key] = true
	go func() {
		defer d.connWg.Done()
		defer func() { d.reviewPackageMu.Lock(); delete(d.reviewJobs, key); d.reviewPackageMu.Unlock() }()
		if err := r.Execute(ctx, v, s.Worktree); err != nil {
			d.logf("", "review check %s: %v", v.ID, err)
		}
	}()
	return dataResponse(v)
}
