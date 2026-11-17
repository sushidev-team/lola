package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/internal/gitrepo"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewcapture"
	"github.com/sushidev-team/lola/internal/reviewpack"
)

// Capture is deliberately on the local human transport, never the agent bridge.
// It executes the project's installed browser helper only after an explicit action.
func (d *Daemon) handleReviewCapture(ctx context.Context, req protocol.Request) protocol.Response {
	fail := func(err error) protocol.Response { return protocol.Response{Error: err.Error()} }
	var a protocol.ReviewCaptureArgs
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
	if a.Screen < -1 || a.Screen >= len(p.Screens) || a.Screen == -1 && len(p.Screens) >= 4 {
		return fail(errors.New("select a screenshot to update; a package holds at most four screens"))
	}
	opts := reviewcapture.Options{URL: a.URL, Title: a.Title, Selector: a.Selector, Width: a.Width, Height: a.Height, Channel: a.Channel, Destination: "capture.png"}
	if err := opts.Validate(); err != nil {
		return fail(err)
	}
	s, ok := d.sessions.Get(p.Session)
	if !ok || s.Worktree == "" || s.Repo != p.Repo || s.GoalManaged() || s.Sandbox != "" && s.Sandbox != "none" || s.Location == "ssh" {
		return fail(errors.New("capture requires the original local host checkout"))
	}
	if !d.beginConnWork() {
		return fail(errors.New("daemon is shutting down"))
	}
	defer d.connWg.Done()
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	checkRevision := func() error {
		current, exists := d.sessions.Get(p.Session)
		if !exists || current.Worktree != s.Worktree || current.Repo != s.Repo {
			return errors.New("session changed; refresh review before capturing")
		}
		if current.PR != nil && (p.PR > 0 && current.PR.Number != p.PR || current.PR.HeadSHA != "" && current.PR.HeadSHA != p.Head || current.PR.BaseSHA != "" && current.PR.BaseSHA != p.Base) {
			return errors.New("review package is stale; prepare material for the current PR first")
		}
		head, err := (gitrepo.CodeReader{}).Head(ctx, s.Worktree)
		if err != nil {
			return err
		}
		if head != p.Head {
			return errors.New("checkout revision differs from this package; prepare current review material first")
		}
		return nil
	}
	if err := checkRevision(); err != nil {
		return fail(err)
	}
	d.reviewPackageMu.Lock()
	if len(d.reviewCapturing) >= 2 || d.reviewCapturing[p.Session] {
		d.reviewPackageMu.Unlock()
		return fail(errors.New("capture is busy; wait for the current captures to finish"))
	}
	if d.reviewCapturing == nil {
		d.reviewCapturing = map[string]bool{}
	}
	d.reviewCapturing[p.Session] = true
	d.reviewPackageMu.Unlock()
	defer func() { d.reviewPackageMu.Lock(); delete(d.reviewCapturing, p.Session); d.reviewPackageMu.Unlock() }()
	stage, err := os.MkdirTemp("", "lola-review-capture-package-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(stage)
	for _, artifact := range p.Artifacts {
		data, _, err := reviewpack.Image(d.home, p.Session, p.ID, artifact.ID)
		if err != nil {
			return fail(err)
		}
		if err := os.WriteFile(filepath.Join(stage, artifact.ID), data, 0600); err != nil {
			return fail(err)
		}
	}
	screen, err := reviewcapture.CaptureInto(ctx, s.Worktree, stage, opts, d.reviewCaptureRun)
	if err != nil {
		return fail(err)
	}
	if err := checkRevision(); err != nil {
		return fail(err)
	}
	screen.Caption = "Captured from the live page in a fresh browser context. The page's running code and uncommitted changes are not independently verified."
	manifest := p.Manifest
	manifest.Screens = append([]reviewpack.Screen{}, p.Screens...)
	if a.Screen == -1 {
		manifest.Screens = append(manifest.Screens, screen)
	} else {
		screen.Before = manifest.Screens[a.Screen].Before
		manifest.Screens[a.Screen] = screen
	}
	d.reviewPackageMu.Lock()
	defer d.reviewPackageMu.Unlock()
	captured, err := reviewpack.Publish(d.home, p.Session, stage, p.Repo, p.PR, manifest)
	if err != nil {
		return fail(err)
	}
	return dataResponse(captured)
}
