package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/session"
)

type profileRun func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error)

type profileArgs struct {
	Session  string `json:"session"`
	Profile  string `json:"profile"`
	Contract string `json:"contract,omitempty"`
	Retry    bool   `json:"retry,omitempty"`
}

func profileBusyKey(session, profile string) string { return session + "|profile:" + profile }

func (d *Daemon) enqueueProfile(a profileArgs) error {
	key := profileBusyKey(a.Session, a.Profile)
	d.reviewMu.Lock()
	if d.reviewBusy == nil {
		d.reviewBusy = map[string]bool{}
	}
	if d.reviewBusy[key] {
		d.reviewMu.Unlock()
		return errors.New("specialist review already queued or running")
	}
	if d.reviewCh == nil {
		d.reviewMu.Unlock()
		return errors.New("review worker unavailable")
	}
	d.reviewBusy[key] = true
	ch := d.reviewCh
	d.reviewMu.Unlock()
	select {
	case ch <- reviewJob{ID: a.Session, Profile: &a}:
		return nil
	default:
		d.clearReviewBusy(key)
		return errors.New("review queue is full; retry on a later cycle")
	}
}

func (d *Daemon) queueProfileReviews(s session.Session) {
	if d.profileReviews == nil || s.GoalManaged() || s.Source != "native" || !agentSettled(s) || s.PR == nil || !isReviewablePROpen(s.PR) || s.PR.BaseSHA == "" {
		return
	}
	d.mu.Lock()
	profiles := slices.Clone(d.cfg.ReviewProfiles)
	d.mu.Unlock()
	records := d.profileReviews.List()
	for _, r := range records {
		d.queueSpecialistHandoff(s, r)
	}
	for _, p := range profiles {
		if !p.Enabled || !p.OnPROpen || (len(p.Projects) > 0 && !slices.Contains(p.Projects, s.Project)) {
			continue
		}
		seen := slices.ContainsFunc(records, func(r reviewprofile.Record) bool {
			return r.State != "stale" && (r.Inputs.Owner == nil || r.Inputs.Owner.Session == s.ID) && r.Profile == p.ID && r.Inputs.Repo == d.repoForSession(s) && r.Inputs.Head == s.PR.HeadSHA && r.Inputs.Base == s.PR.BaseSHA && r.Key == p.Key(r.Inputs)
		})
		if !seen {
			_ = d.enqueueProfile(profileArgs{Session: s.ID, Profile: p.ID})
		}
	}
}

func (d *Daemon) handleProfile(ctx context.Context, req protocol.Request) protocol.Response {
	if req.Cmd == "reviewProfiles" {
		d.mu.Lock()
		profiles := slices.Clone(d.cfg.ReviewProfiles)
		d.mu.Unlock()
		return dataResponse(profiles)
	}
	if d.profileReviews == nil {
		return protocol.Response{Error: fmt.Sprintf("specialist review store unavailable: %v", d.profileErr)}
	}
	if req.Cmd == "profileResults" {
		return dataResponse(d.profileReviews.List())
	}
	if req.Cmd == "profileFindings" {
		return dataResponse(d.profileReviews.Findings())
	}
	if req.Cmd == "profileCalibration" {
		return dataResponse(d.profileReviews.Calibration())
	}
	if req.Cmd == "profileDecideFinding" {
		var decision reviewprofile.FindingDecision
		if err := json.Unmarshal(req.Args, &decision); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		d.goalMu.Lock()
		defer d.goalMu.Unlock()
		report, err := d.profileReviews.Decide(decision)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		if report.Inputs.Owner != nil && report.Inputs.Owner.Goal != "" && reviewprofile.BlockingDispute(report) && d.goals != nil {
			if g, ok := d.goals.Get(report.Inputs.Owner.Goal); ok {
				if err := d.holdGoalDispute(g, report.ID); err != nil {
					return protocol.Response{Error: err.Error()}
				}
			}
		}
		return dataResponse(report)
	}
	var a profileArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return protocol.Response{Error: err.Error()}
	}
	if _, _, err := d.profileFor(a.Profile); err != nil {
		return protocol.Response{Error: err.Error()}
	}
	if _, ok := d.sessions.Get(a.Session); !ok {
		return protocol.Response{Error: "unknown session"}
	}
	if len(a.Contract) > 16000 {
		return protocol.Response{Error: "contract exceeds 16 KiB"}
	}
	if err := d.enqueueProfile(a); err != nil {
		return protocol.Response{Error: err.Error()}
	}
	return dataResponse(map[string]any{"queued": true, "session": a.Session, "profile": a.Profile})
}

func (d *Daemon) profileFor(id string) (reviewprofile.Profile, config.ReviewProvider, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.cfg.ReviewProfiles {
		if p.ID != id {
			continue
		}
		if err := p.Validate(); err != nil {
			return p, config.ReviewProvider{}, err
		}
		if !p.Enabled {
			return p, config.ReviewProvider{}, fmt.Errorf("profile %s is disabled", id)
		}
		for _, provider := range d.cfg.EffectiveReviewProviders() {
			if string(provider.Provider) == p.Provider && provider.Enabled {
				if _, ok := config.ReviewAgentFor(p.Provider); ok {
					return p, provider, nil
				}
			}
		}
		return p, config.ReviewProvider{}, errors.New("profile requires an enabled agent provider")
	}
	return reviewprofile.Profile{}, config.ReviewProvider{}, fmt.Errorf("unknown review profile %q", id)
}

func (d *Daemon) runProfile(ctx context.Context, a profileArgs) (reviewprofile.Record, error) {
	if d.profileReviews == nil {
		return reviewprofile.Record{}, errors.New("specialist review store unavailable")
	}
	p, provider, err := d.profileFor(a.Profile)
	if err != nil {
		return reviewprofile.Record{}, err
	}
	s, ok := d.sessions.Get(a.Session)
	if !ok || s.GoalManaged() || s.Source != "native" || s.PR == nil || !isReviewablePROpen(s.PR) || !agentSettled(s) || (s.Stack && s.Lane == "") {
		return reviewprofile.Record{}, errors.New("specialist review requires a settled session with an open PR (select a stack lane)")
	}
	if s.PR.HeadSHA == "" || s.PR.BaseSHA == "" {
		return reviewprofile.Record{}, errors.New("specialist review is waiting for exact PR head and base commits")
	}
	dir := s.Worktree
	if dir == "" {
		dir = filepath.Join(d.home, "worktrees", s.Project, s.ID)
	}
	in := reviewprofile.Inputs{Owner: &reviewprofile.ReviewOwner{Project: s.Project, Session: s.ID}, Repo: d.repoForSession(s), Head: s.PR.HeadSHA, Base: s.PR.BaseSHA, Contract: a.Contract}
	checkCtx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	in.Paths, err = d.profilePaths(checkCtx, dir, in.Head, in.Base)
	cancel()
	if err != nil {
		return reviewprofile.Record{}, err
	}
	if len(p.Projects) > 0 && !slices.Contains(p.Projects, s.Project) {
		return reviewprofile.Record{}, errors.New("specialist profile is not enabled for this project")
	}
	if !p.Applies(s.Project, in.Paths) {
		in.Contract = "Not applicable: inspected changed paths are outside the configured profile scope."
		if old, ok := d.profileReviews.Get(p.Key(in)); ok {
			return old, nil
		}
		r, err := d.profileReviews.Begin(p, in, false)
		if err != nil {
			return r, err
		}
		r.State = "not_applicable"
		return r, d.profileReviews.Finish(r)
	}
	if in.Contract == "" {
		if s.IssueUUID == "" {
			return reviewprofile.Record{}, errors.New("provide an acceptance contract for this non-Linear session")
		}
		api, err := d.ensureLinear()
		if err != nil {
			return reviewprofile.Record{}, err
		}
		readCtx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		issue, err := api.IssueDetail(readCtx, s.IssueUUID)
		cancel()
		if err != nil {
			return reviewprofile.Record{}, err
		}
		if strings.TrimSpace(issue.Description) == "" {
			return reviewprofile.Record{}, errors.New("issue has no acceptance contract; add it before specialist review")
		}
		in.Contract = issue.Title + "\n\n" + issue.Description
		in.ContractIssueID = s.IssueUUID
	}
	if len(in.Contract) > 16000 {
		return reviewprofile.Record{}, errors.New("acceptance contract exceeds 16 KiB")
	}
	if d.knowledge != nil {
		in.Knowledge = d.selectKnowledge(ctx, knowledge.Query{Project: s.Project, Paths: in.Paths, Text: in.Contract, MaxBytes: 4000}).Markdown()
	}
	if previous, exists := d.profileReviews.Get(p.Key(in)); exists && !a.Retry && previous.State != "stale" {
		return previous, nil
	}
	if hold := d.dispatchHold(ctx, s.Project, time.Now()); hold != "" {
		return reviewprofile.Record{}, fmt.Errorf("specialist review held: %s", hold)
	}
	r, err := d.profileReviews.Begin(p, in, a.Retry)
	if err != nil {
		return r, err
	}
	finish := func(state string, cause error) (reviewprofile.Record, error) {
		r.State = state
		if cause != nil {
			r.Error = cause.Error()
		}
		if err := d.profileReviews.Finish(r); err != nil {
			return r, errors.Join(cause, err)
		}
		if state == "pass" || state == "findings" {
			d.captureProfileFindings(s, r)
			d.queueSpecialistHandoff(s, r)
		}
		return r, nil // incomplete/error is a durable result, not an apparent clean run
	}
	if err := p.CheckInputs(in); err != nil {
		return finish("incomplete", err)
	}
	run := d.profileRun
	if run == nil {
		agentKind, _ := config.ReviewAgentFor(p.Provider)
		client := buildAgentReview(agentKind, provider)
		if client == nil {
			return finish("error", errors.New("specialist provider unavailable"))
		}
		if p.TimeoutSeconds > 0 && (client.Timeout == 0 || time.Duration(p.TimeoutSeconds)*time.Second < client.Timeout) {
			client.Timeout = time.Duration(p.TimeoutSeconds) * time.Second
		}
		run = client.ReviewProfile
	}
	if err := d.profileInputsCurrent(ctx, s, in, dir); err != nil {
		return finish("error", err)
	}
	r.Result, err = run(ctx, dir, p, in)
	if err != nil {
		return finish("incomplete", err)
	}
	// Validate seam output too: a buggy provider adapter cannot certify a partial result.
	b, _ := json.Marshal(r.Result)
	r.Result, err = reviewprofile.Parse(string(b), in, p)
	if err != nil {
		return finish("incomplete", err)
	}
	if err := d.profileInputsCurrent(ctx, s, in, dir); err != nil {
		return finish("error", err)
	}
	current, _, err := d.profileFor(p.ID)
	if err != nil || current.Key(in) != p.Key(in) {
		return finish("error", errors.New("profile changed during review"))
	}
	state := "pass"
	for _, f := range r.Result.Findings {
		if f.Severity == "major" || f.Severity == "blocker" {
			state = "findings"
		}
	}
	return finish(state, nil)
}

func (d *Daemon) captureProfileFindings(s session.Session, r reviewprofile.Record) {
	if d.knowledge == nil {
		return
	}
	for i, f := range r.Result.Findings {
		topic := "Review: " + f.Requirement
		if len(topic) > 160 {
			topic = topic[:160]
		}
		proposal := knowledge.Proposal{Topic: topic, Summary: f.Requirement + ": " + f.Fix,
			Rationale: f.Reproduction, Evidence: []string{f.Evidence, "review " + r.Profile + " at " + r.Inputs.Head}, Tags: []string{r.Profile}}
		_, err := d.knowledge.Capture(knowledge.Scope{Project: s.Project, GoalID: s.GoalID}, proposal,
			knowledge.Source{Kind: "review", ID: fmt.Sprintf("%s:%d", r.Key, i), Session: s.ID, Commit: r.Inputs.Head}, false)
		if err != nil {
			d.logf(s.Project, "review lesson candidate omitted: %v", err)
		}
	}
}

func (d *Daemon) profileInputsCurrent(ctx context.Context, s session.Session, in reviewprofile.Inputs, dir string) error {
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	defer cancel()
	if err := d.profileContractCurrent(cctx, in); err != nil {
		return err
	}
	if err := d.verifyReviewHead(cctx, dir, in.Head); err != nil {
		return err
	}
	fresh, err := d.prForBranch(cctx, in.Repo, s.Branch)
	if err != nil {
		return err
	}
	if fresh == nil || fresh.Number != s.PR.Number || fresh.State != "OPEN" || fresh.HeadSHA != in.Head || fresh.BaseSHA != in.Base {
		return errors.New("specialist head or base changed during review")
	}
	return nil
}

// Required profile gates are local acceptance facts. Missing stores, unknown
// base commits, incomplete reviews and a changed rubric all fail closed.
func (d *Daemon) profileGatesOpen(s session.Session) bool {
	if d.sessionDisputeHold(s) {
		return false
	}
	d.mu.Lock()
	profiles := slices.Clone(d.cfg.ReviewProfiles)
	d.mu.Unlock()
	for _, p := range profiles {
		if !p.Enabled || !p.Required || (len(p.Projects) > 0 && !slices.Contains(p.Projects, s.Project)) {
			continue
		}
		if d.profileReviews == nil || s.PR == nil || s.PR.BaseSHA == "" {
			return false
		}
		passed := false
		for _, r := range d.profileReviews.List() {
			if (r.Inputs.Owner != nil && (r.Inputs.Owner.Project != s.Project || r.Inputs.Owner.Session != s.ID)) || r.Profile != p.ID || r.Inputs.Repo != d.repoForSession(s) || r.Inputs.Head != s.PR.HeadSHA || r.Inputs.Base != s.PR.BaseSHA || r.Key != p.Key(r.Inputs) {
				continue
			}
			// Path-scoped profiles need an inspected path manifest even when the
			// paths show that this profile does not apply; unknowns never pass.
			if (r.State == "pass" && p.Applies(s.Project, r.Inputs.Paths)) || (r.State == "not_applicable" && !p.Applies(s.Project, r.Inputs.Paths)) {
				passed = true
			}
		}
		if !passed {
			return false
		}
	}
	return true
}

var errProfileContractChanged = errors.New("specialist acceptance contract changed in Linear")

func (d *Daemon) profileContractCurrent(ctx context.Context, in reviewprofile.Inputs) error {
	if in.ContractIssueID == "" {
		return nil
	} // explicit operator-provided contract
	api, err := d.ensureLinear()
	if err != nil {
		return err
	}
	issue, err := api.IssueDetail(ctx, in.ContractIssueID)
	if err != nil {
		return err
	}
	if issue.Title+"\n\n"+issue.Description != in.Contract {
		return errProfileContractChanged
	}
	return nil
}

// Recheck human-authored contracts immediately before a merge. The observer's
// cheap gate reads stored evidence; a changed brief must invalidate that cache.
func (d *Daemon) profileContractsCurrent(ctx context.Context, s session.Session) bool {
	d.mu.Lock()
	profiles := slices.Clone(d.cfg.ReviewProfiles)
	d.mu.Unlock()
	for _, p := range profiles {
		if !p.Enabled || !p.Required || (len(p.Projects) > 0 && !slices.Contains(p.Projects, s.Project)) {
			continue
		}
		if d.profileReviews == nil || s.PR == nil {
			return false
		}
		for _, r := range d.profileReviews.List() {
			if (r.Inputs.Owner != nil && (r.Inputs.Owner.Project != s.Project || r.Inputs.Owner.Session != s.ID)) || r.Profile != p.ID || r.State != "pass" || r.Inputs.Repo != d.repoForSession(s) || r.Inputs.Head != s.PR.HeadSHA || r.Inputs.Base != s.PR.BaseSHA || r.Key != p.Key(r.Inputs) {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			err := d.profileContractCurrent(cctx, r.Inputs)
			cancel()
			if err != nil {
				if errors.Is(err, errProfileContractChanged) {
					if saveErr := d.profileReviews.Invalidate(r.Key, err.Error()); saveErr != nil {
						d.logf("", "invalidate specialist result: %v", saveErr)
					}
				}
				return false
			}
		}
	}
	return true
}
