package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/gitrepo"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func reviewLessonProposal(f reviewprofile.Finding, profile, head string) knowledge.Proposal {
	topic := "Review: " + f.Requirement
	if len(topic) > 160 {
		topic = topic[:160]
	}
	return knowledge.Proposal{Topic: topic, Summary: f.Requirement + ": " + f.Fix, Rationale: f.Reproduction, Evidence: []string{f.Evidence, "review " + profile + " at " + head}, Tags: []string{profile}}
}

func findingProof(f reviewprofile.FindingReport) (*reviewprofile.Record, string) {
	if f.State != "verified" || len(f.Events) == 0 {
		return nil, ""
	}
	e := f.Events[len(f.Events)-1]
	if e.State != "verified" || e.Verification == nil || e.Verification.Inputs.Invocation == nil {
		return nil, ""
	}
	return e.Verification, e.FixHead
}

func (d *Daemon) queueReviewPromotion(owner *reviewprofile.ReviewOwner) {
	if owner == nil || d.knowledge == nil || !d.beginConnWork() {
		return
	}
	go func() {
		defer d.connWg.Done()
		d.promoteReviewKnowledge(context.Background(), owner.Project)
	}()
}

// The registry event is an immutable snapshot of independent verification;
// changing provider settings later does not rewrite historical evidence.
func (d *Daemon) reviewProofCurrent(l knowledge.Lesson) (current, available bool) {
	p := l.ReviewProof
	if p == nil || p.ReplacedByHuman {
		return true, true
	}
	if d.profileReviews == nil || d.knowledge == nil {
		return false, false
	}
	var origin knowledge.Lesson
	for _, item := range d.knowledge.List() {
		if item.ID == p.Source.ID {
			origin = item
			break
		}
	}
	if origin.ID == "" {
		return false, false
	}
	if origin.Version != p.Source.Version || origin.Status != knowledge.Candidate {
		return false, true
	}
	for _, f := range d.profileReviews.Findings() {
		if f.ID != p.FindingID {
			continue
		}
		r, head := findingProof(f)
		if r == nil || f.Revision != p.FindingRevision || r.Key != p.VerificationKey || head != p.FixCommit {
			return false, true
		}
		current, ok := d.profileReviews.Get(r.Key)
		if !ok {
			return false, false
		}
		return current.State == "pass" && current.Attempt == r.Attempt && reflect.DeepEqual(current.Result, r.Result), true
	}
	return false, false
}

func (d *Daemon) promoteReviewKnowledge(ctx context.Context, project string) []knowledge.Maintenance {
	if d.knowledge == nil || d.profileReviews == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	findings := d.profileReviews.Findings()
	lessons := d.knowledge.List()
	derived := map[knowledge.Reference]bool{}
	for _, l := range lessons {
		if l.ReviewProof != nil {
			derived[l.ReviewProof.Source] = true
		}
	}
	var reports []knowledge.Maintenance
	for _, l := range lessons {
		if ctx.Err() != nil {
			break
		}
		if (project != "" && l.Scope.Project != project) || l.Status != knowledge.Candidate || l.Source.Kind != "review" || l.Policy || l.ReviewReason != "" || derived[knowledge.Reference{ID: l.ID, Version: l.Version}] {
			continue
		}
		for _, f := range findings {
			if f.Inputs.Owner == nil || f.Inputs.Owner.Project != l.Scope.Project || f.Inputs.Owner.Goal != l.Scope.GoalID || f.Inputs.Owner.Session != l.Source.Session || l.Source.ID != fmt.Sprintf("%s:%d", f.ReviewKey, f.Index) || l.Source.Commit != f.Inputs.Head || !reflect.DeepEqual(l.Proposal, reviewLessonProposal(f.Finding, f.Profile, f.Inputs.Head)) {
				continue
			}
			verification, fix := findingProof(f)
			if verification == nil {
				continue
			}
			report := knowledge.Maintenance{ID: l.ID, Version: l.Version, State: "waiting"}
			promoted, err := d.promoteReviewLesson(ctx, l, f, *verification, fix)
			if err != nil {
				report.Reason = err.Error()
			} else {
				report.State, report.LessonID = "promoted", promoted.ID
			}
			reports = append(reports, report)
			break
		}
	}
	return reports
}

func (d *Daemon) promoteReviewLesson(ctx context.Context, l knowledge.Lesson, f reviewprofile.FindingReport, verification reviewprofile.Record, fix string) (knowledge.Lesson, error) {
	var none knowledge.Lesson
	// Repository routing is configured authority, never a model-supplied path.
	d.mu.Lock()
	var members []string
	p := d.cfg.ProjectByName(l.Scope.Project)
	isStack := p != nil && p.IsStack()
	for _, m := range d.cfg.Projects {
		if p != nil && !m.IsStack() && m.Repo == f.Inputs.Repo && ((isStack && m.Stack == p.Name) || (!isStack && m.Name == p.Name)) {
			members = append(members, m.Name)
		}
	}
	d.mu.Unlock()
	if len(members) != 1 {
		return none, fmt.Errorf("review repository is not uniquely configured in this project")
	}
	member := members[0]
	source, branch, err := d.knowledgeRepository(l.Scope.Project, member)
	if err != nil {
		return none, err
	}
	proofSource := source
	if l.Scope.GoalID != "" {
		if d.goals == nil {
			return none, fmt.Errorf("goal delivery is unavailable")
		}
		g, ok := d.goals.Get(l.Scope.GoalID)
		if !ok || g.Project != l.Scope.Project || g.State != "accepted" {
			return none, fmt.Errorf("goal delivery is not yet accepted")
		}
		m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
		repo, err := m.Repository(ctx, g.ID, member)
		if err != nil || repo.Source != source {
			return none, fmt.Errorf("accepted goal repository evidence is unavailable")
		}
		proofSource = repo.GitDir
	}
	paths := slices.Clone(f.Inputs.Paths)
	if len(paths) == 0 {
		paths = []string{"."}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	var targets []knowledge.CodeTarget
	for _, path := range paths {
		targets = append(targets, knowledge.CodeTarget{Member: member, Path: path})
	}
	if err := knowledge.ValidateTargets(targets); err != nil {
		return none, err
	}
	var refs []knowledge.CodeReference
	var scoped []string
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return none, err
		}
		accepted, err := d.readKnowledgeCode(ctx, source, branch, target.Path)
		if err != nil || !accepted.Exists {
			return none, fmt.Errorf("accepted target code is unavailable")
		}
		reader := (gitrepo.CodeReader{}).ReadCommit
		if d.knowledgeProofRead != nil {
			reader = d.knowledgeProofRead
		}
		verified, err := reader(ctx, proofSource, fix, target.Path)
		if err != nil || !verified.Exists || verified.Digest != accepted.Digest {
			return none, fmt.Errorf("accepted code does not match the independently verified fix")
		}
		refs = append(refs, knowledge.CodeReference{CodeTarget: target, Source: source, Branch: branch, Commit: accepted.Commit, Digest: accepted.Digest})
		if isStack {
			if target.Path == "." {
				scoped = append(scoped, member)
			} else {
				scoped = append(scoped, member+"/"+target.Path)
			}
		} else if target.Path != "." {
			scoped = append(scoped, target.Path)
		}
	}
	proof := knowledge.ReviewProof{Source: knowledge.Reference{ID: l.ID, Version: l.Version}, FindingID: f.ID, FindingRevision: f.Revision, VerificationKey: verification.Key, FixCommit: fix}
	// Human decisions use this lock too. Curation cannot change between the
	// final evidence check and writing its derived guidance.
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	if current, available := d.reviewProofCurrent(knowledge.Lesson{ReviewProof: &proof}); !current || !available {
		return none, fmt.Errorf("review verification changed during promotion")
	}
	return d.knowledge.PromoteReview(proof, scoped, refs)
}
