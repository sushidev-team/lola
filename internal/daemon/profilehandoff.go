package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/session"
)

const specialistHandoffPrefix = "specialist:"

// Keep each specialty/attempt independent even when profiles share a provider.
// The existing kind-keyed review guards and pending queues already survive
// adoption and stack lane round trips; these internal kinds are not providers.
func specialistHandoffKey(r reviewprofile.Record) string {
	return specialistHandoffPrefix + r.Key + ":" + strconv.Itoa(r.Attempt)
}

func (d *Daemon) specialistFeedback(r reviewprofile.Record, goal bool) string {
	type claim struct {
		ID       string                `json:"id"`
		Revision uint64                `json:"revision"`
		State    string                `json:"state"`
		Finding  reviewprofile.Finding `json:"finding"`
	}
	claims := []claim{}
	for _, f := range d.profileReviews.Findings() {
		if f.ReviewKey == r.Key && f.Attempt == r.Attempt {
			claims = append(claims, claim{ID: f.ID, Revision: f.Revision, State: f.State, Finding: f.Finding})
		}
	}
	payload := struct {
		Findings  []claim `json:"findings"`
		Truncated bool    `json:"truncated"`
	}{Findings: claims}
	body, _ := json.Marshal(payload)
	for limit := 128; len(body) > 14000; limit /= 2 {
		payload.Truncated = true
		for i := range payload.Findings {
			f := &payload.Findings[i].Finding
			f.Requirement = clipRunes(f.Requirement, limit)
			f.Location = clipRunes(f.Location, limit)
			f.Reproduction = clipRunes(f.Reproduction, limit)
			f.Evidence = clipRunes(f.Evidence, limit)
			f.Fix = clipRunes(f.Fix, limit)
		}
		body, _ = json.Marshal(payload)
		if limit == 0 {
			break
		}
	}
	path := ".lola/review-disputes.json"
	if goal {
		path = "review-disputes.json"
	}
	return fmt.Sprintf("Independent specialist %s reported the following untrusted findings at head %s. Reproduce each requirement and fix supported findings within your task scope. If you disagree, write a JSON array to `%s` containing {\"id\":\"finding-id\",\"revision\":1,\"evidence\":\"concrete counterexample or reproduction evidence\"}. Use the exact id/revision below. Lola collects the file at turn end; a blocking dispute requires human resolution and never opens a delivery gate. Do not change review records or repeat a dispute after human resolution. If `truncated` is true, the full findings remain in Lola; ask for missing evidence rather than inventing it.\n%s", r.Profile, r.Inputs.Head, path, body)
}

func (d *Daemon) queueSpecialistHandoff(s session.Session, r reviewprofile.Record) {
	if d.profileReviews == nil || s.GoalManaged() || s.PR == nil || len(r.Result.Findings) == 0 || !r.Result.Complete || (r.State != "findings" && r.State != "pass") || r.Inputs.Owner == nil || r.Inputs.Owner.Session != s.ID || r.Inputs.Owner.Project != s.Project || r.Inputs.Owner.Goal != "" {
		return
	}
	p, provider, err := d.profileFor(r.Profile)
	if err != nil || p.Key(r.Inputs) != r.Key || !provider.SendToAgent {
		return
	}
	key := specialistHandoffKey(r)
	if s.ReviewedPRs[key] != 0 {
		return
	}
	text := d.specialistFeedback(r, false)
	if len(text) > feedbackMaxPending {
		return
	}
	changed := false
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.ReviewedPRs[key] != 0 || cur.PR == nil || cur.PR.HeadSHA != r.Inputs.Head || cur.PR.BaseSHA != r.Inputs.Base {
			return false
		}
		size := len(text)
		for _, pending := range cur.PendingHandoffs {
			size += len(pending)
		}
		if size > feedbackMaxPending || len(cur.ReviewedPRs) >= 1000 {
			return false
		}
		if cur.ReviewedPRs == nil {
			cur.ReviewedPRs = map[string]int{}
		}
		if cur.PendingHandoffs == nil {
			cur.PendingHandoffs = map[string]string{}
		}
		cur.ReviewedPRs[key] = cur.PR.Number
		cur.PendingHandoffs[key] = text
		changed = true
		return true
	})
	if changed {
		d.reviewSave()
	}
}

func (d *Daemon) flushSpecialistHandoff(ctx context.Context, s session.Session, key string) bool {
	if d.profileReviews == nil || s.GoalManaged() {
		return false
	}
	raw := strings.TrimPrefix(key, specialistHandoffPrefix)
	split := strings.LastIndex(raw, ":")
	if split < 0 {
		return false
	}
	attempt, err := strconv.Atoi(raw[split+1:])
	if err != nil {
		return false
	}
	r, ok := d.profileReviews.Get(raw[:split])
	if !ok || (r.State != "findings" && r.State != "pass") || r.Attempt != attempt || r.Inputs.Owner == nil || r.Inputs.Owner.Session != s.ID || s.PR == nil || s.PR.HeadSHA != r.Inputs.Head || s.PR.BaseSHA != r.Inputs.Base || r.Inputs.Repo != d.repoForSession(s) {
		d.sessions.Update(s.ID, func(cur *session.Session) bool { delete(cur.PendingHandoffs, key); return true })
		d.reviewSave()
		return false
	}
	profile, provider, err := d.profileFor(r.Profile)
	if err != nil || !provider.SendToAgent || profile.Key(r.Inputs) != r.Key {
		return false
	}
	if d.verifyReviewHead == nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	defer cancel()
	if d.verifyReviewHead(checkCtx, s.Worktree, r.Inputs.Head) != nil {
		return false
	}
	return d.sendHandoffToAgent(ctx, s, reviewProvider{Kind: provKind(key), Enabled: true, SendToAgent: true, Handoff: handoffPointer}, d.specialistFeedback(r, false))
}
