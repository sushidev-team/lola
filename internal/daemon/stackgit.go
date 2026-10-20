package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sushidev-team/lola/internal/checkpoint"
	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// The local-git views of a STACK session (diff viewer, overlap detection,
// turn checkpoints). The session folder is not a repository; each app is. So
// every one of these runs per app and presents the result as ONE tree rooted
// at the session folder: a path is "<app>/<path in the app>", exactly the
// path the agent sees from its working directory. That is what keeps the
// clients unchanged — the diff viewer lists the apps as top-level folders,
// and a diff-feedback anchor ("nori-website/src/app.ts:12") points the agent
// at the right file without any further translation.

// presentApps is the subset of a stack session's apps whose worktree is on
// disk (a teardown in progress removes them one by one).
func presentApps(s session.Session) []session.App {
	var out []session.App
	for _, a := range s.Apps {
		if a.Worktree == "" {
			continue
		}
		if _, err := os.Stat(a.Worktree); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// prefixFiles roots an app's diff files at its folder in the session.
func prefixFiles(app string, files []gitdiff.File) []gitdiff.File {
	out := make([]gitdiff.File, 0, len(files))
	for _, f := range files {
		f.Path = app + "/" + f.Path
		if f.OldPath != "" {
			f.OldPath = app + "/" + f.OldPath
		}
		out = append(out, f)
	}
	return out
}

// stackDiff is handleDiff for a stack session: each app against its own
// base, merged into one result. An app that cannot be diffed (no merge-base
// yet, git failure) is named in Base rather than failing the whole view.
func (d *Daemon) stackDiff(ctx context.Context, s session.Session) (protocol.DiffData, error) {
	var res gitdiff.Result
	var bases, failed []string
	for _, a := range presentApps(s) {
		r, err := d.worktreeDiff(ctx, a.Worktree, appBase(a))
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", a.Name, err))
			continue
		}
		bases = append(bases, a.Name+": "+r.Base)
		res.Files = append(res.Files, prefixFiles(a.Name, r.Files)...)
		res.Truncated = res.Truncated || r.Truncated
	}
	if len(bases) == 0 && len(failed) > 0 {
		return protocol.DiffData{}, fmt.Errorf("diff %s: %s", s.ID, strings.Join(failed, "; "))
	}
	base := strings.Join(bases, ", ")
	if len(failed) > 0 {
		base += " — not shown: " + strings.Join(failed, "; ")
	}
	return diffData(s.ID, base, "", res), nil
}

// stackChangedFiles is the overlap detector's file set for a stack session:
// every app's changed paths, rooted at the session folder. nil when no app
// could be read.
func (d *Daemon) stackChangedFiles(ctx context.Context, s session.Session) map[string]bool {
	var set map[string]bool
	for _, a := range presentApps(s) {
		app := s
		app.ID, app.Worktree = session.LaneID(s.ID, a.Name), a.Worktree
		files := d.sessionChangedFiles(ctx, app, appBase(a))
		if files == nil {
			continue
		}
		if set == nil {
			set = map[string]bool{}
		}
		for p := range files {
			set[a.Name+"/"+p] = true
		}
	}
	return set
}

// Turn checkpoints of a stack session. Every app records one checkpoint per
// turn, ALWAYS (checkpoint.RecordAlways — even an untouched app), with the
// same label, so checkpoint #N is the same moment in every app and the
// clients keep addressing a restore point by one number. The first present
// app is the timeline the list is read from. A restore is all-or-nothing:
// every app must hold #N under the same label before anything is touched,
// and an app that fails mid-way rolls the ones already restored back to the
// state they had (their safety checkpoint).

// errStackCheckpointGap: the apps disagree about a checkpoint, so it cannot
// name one moment for all of them (a record failed in one app). Fail closed.
var errStackCheckpointGap = errors.New("this checkpoint was not recorded in every app")

// stackRecordCheckpoint is recordCheckpoint's stack body. The caller holds
// the session's checkpoint lock.
func (d *Daemon) stackRecordCheckpoint(ctx context.Context, s session.Session, event string) {
	apps := presentApps(s)
	if len(apps) == 0 {
		return
	}
	list, err := d.checkpoints.List(ctx, apps[0].Worktree, s.ID)
	if err != nil {
		d.logf("", "checkpoint: %s: %v", s.ID, err)
		return
	}
	label := ckptLabelStart
	if event == "user_prompt" {
		if len(list) > 0 {
			return
		}
	} else {
		turns := 0
		for _, c := range list {
			if strings.HasPrefix(c.Label, ckptLabelTurn) {
				turns++
			}
		}
		label = fmt.Sprintf("%s%d", ckptLabelTurn, turns+1)
	}
	seq := 0
	for _, a := range apps {
		cp, err := d.checkpoints.RecordAlways(ctx, a.Worktree, s.ID, label)
		if err != nil {
			d.logf("", "checkpoint: %s app %s: %v", s.ID, a.Name, err)
			continue
		}
		seq = cp.Seq
	}
	if seq > 0 {
		d.logf("", "checkpoint: %s #%d (%s) in %d app(s)", s.ID, seq, label, len(apps))
	}
}

// stackCheckpoint is checkpoint #seq in every present app, or
// errStackCheckpointGap when one lacks it or recorded a different moment.
func (d *Daemon) stackCheckpoint(ctx context.Context, s session.Session, seq int) ([]session.App, []checkpoint.Checkpoint, []checkpoint.Checkpoint, error) {
	apps := presentApps(s)
	if len(apps) == 0 {
		return nil, nil, nil, fmt.Errorf("session %s has no app checkouts", s.ID)
	}
	cps := make([]checkpoint.Checkpoint, len(apps))
	prevs := make([]checkpoint.Checkpoint, len(apps))
	for i, a := range apps {
		cp, prev, err := d.checkpoints.Get(ctx, a.Worktree, s.ID, seq)
		if err != nil {
			if errors.Is(err, checkpoint.ErrNotFound) && i > 0 {
				return nil, nil, nil, fmt.Errorf("checkpoint #%d: %w (missing in %s)", seq, errStackCheckpointGap, a.Name)
			}
			return nil, nil, nil, fmt.Errorf("%s: %w", a.Name, err)
		}
		if i > 0 && cp.Label != cps[0].Label {
			return nil, nil, nil, fmt.Errorf("checkpoint #%d: %w (%s has %q, %s has %q)", seq, errStackCheckpointGap, apps[0].Name, cps[0].Label, a.Name, cp.Label)
		}
		cps[i], prevs[i] = cp, prev
	}
	return apps, cps, prevs, nil
}

// stackCheckpointDiff is what one turn changed across the apps.
func (d *Daemon) stackCheckpointDiff(ctx context.Context, s session.Session, seq int) (protocol.DiffData, error) {
	apps, cps, prevs, err := d.stackCheckpoint(ctx, s, seq)
	if err != nil {
		return protocol.DiffData{}, err
	}
	var res gitdiff.Result
	base := "its HEAD"
	if prevs[0].SHA != "" {
		base = fmt.Sprintf("checkpoint #%d", prevs[0].Seq)
	}
	for i, a := range apps {
		from := cps[i].Head
		if prevs[i].SHA != "" {
			from = prevs[i].SHA
		}
		r, err := d.commitDiff(ctx, a.Worktree, from, cps[i].SHA)
		if err != nil {
			return protocol.DiffData{}, fmt.Errorf("checkpoint #%d in %s: %w", seq, a.Name, err)
		}
		res.Files = append(res.Files, prefixFiles(a.Name, r.Files)...)
		res.Truncated = res.Truncated || r.Truncated
	}
	return diffData(s.ID, base, "", res), nil
}

// stackRestore restores checkpoint #seq in every app, all or nothing, and
// returns the safety checkpoint's number (the same in every app). The caller
// holds the checkpoint lock and the send gate and has proven the agent idle.
func (d *Daemon) stackRestore(ctx context.Context, s session.Session, seq int) (int, error) {
	apps, _, _, err := d.stackCheckpoint(ctx, s, seq)
	if err != nil {
		return 0, err
	}
	safeties := make([]int, 0, len(apps))
	for i, a := range apps {
		safety, err := d.checkpoints.Restore(ctx, a.Worktree, s.ID, seq)
		if err == nil {
			safeties = append(safeties, safety.Seq)
			continue
		}
		// Put the apps already restored back the way they were.
		var undo []string
		for j := range i {
			if _, uerr := d.checkpoints.Restore(ctx, apps[j].Worktree, s.ID, safeties[j]); uerr != nil {
				undo = append(undo, fmt.Sprintf("%s (%v)", apps[j].Name, uerr))
			}
		}
		if len(undo) > 0 {
			return 0, fmt.Errorf("restore #%d failed in %s: %w — and undoing the apps already restored failed too: %s", seq, a.Name, err, strings.Join(undo, "; "))
		}
		return 0, fmt.Errorf("restore #%d failed in %s, nothing was changed: %w", seq, a.Name, err)
	}
	return safeties[0], nil
}
