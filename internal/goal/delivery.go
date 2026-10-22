package goal

import (
	"errors"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
)

type DeliveryMember struct {
	Repo   string `json:"repo"`
	Target string `json:"target"`
	Head   string `json:"head"`
	State  string `json:"state"`
	Number int    `json:"number,omitempty"`
	URL    string `json:"url,omitempty"`
	Draft  bool   `json:"draft"`
}
type Delivery struct {
	Operation string                    `json:"operation"`
	State     string                    `json:"state"` // publishing, published, merged, closed, error
	Members   map[string]DeliveryMember `json:"members"`
	Error     string                    `json:"error,omitempty"`
	CheckedAt time.Time                 `json:"checked_at"`
}
type DeliveryEvidenceRequest struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	URL      string `json:"url"`
}

func ValidateEvidenceURL(raw string) error {
	if len(raw) > 2000 || strings.ContainsAny(raw, "\r\n<>\\") {
		return errors.New("invalid shared evidence URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("shared evidence must be an HTTPS artifact URL without credentials")
	}
	return nil
}

// ObserveDelivery accepts daemon-observed PR facts, never a worker's claim.
// Only an exact verified head merged into every target completes delivery.
func (g *Goal) ObserveDelivery(d Delivery) error {
	e := g.Execution
	if e == nil || e.Final == nil || e.Final.Receipt == "" || d.Operation != e.Final.ID || !e.Policy.PublishPR || d.CheckedAt.IsZero() {
		return errors.New("delivery does not belong to a verified publishable goal")
	}
	if !slices.Contains([]string{"publishing", "published", "merged", "closed", "error"}, d.State) {
		return errors.New("invalid delivery state")
	}
	all, err := g.validateDelivery(d)
	if err != nil {
		return err
	}
	if d.State == "merged" && !all {
		return errors.New("merged delivery lacks complete target-branch observations")
	}
	if all && g.State == "ready" && d.State == "merged" && !e.Policy.RequireHumanAcceptance {
		g.State = "accepted"
	}
	e.Delivery = &d
	return nil
}

// HumanAcceptance records the operator's decision about an exact delivered
// revision. It cannot replace verification or authorize a merge/deployment.
type HumanAcceptance struct {
	Revision     uint64            `json:"revision"`
	Operation    string            `json:"operation"`
	Receipt      string            `json:"receipt"`
	PolicyDigest string            `json:"policy_digest"`
	Manifest     map[string]string `json:"manifest"`
	Evidence     string            `json:"evidence"`
	AcceptedAt   time.Time         `json:"accepted_at"`
}

func (g Goal) Delivered() bool {
	e := g.Execution
	if e == nil || !e.Policy.PublishPR || e.Final == nil || e.Final.Receipt == "" || e.Delivery == nil || e.Delivery.State != "merged" || e.Delivery.CheckedAt.IsZero() {
		return false
	}
	all, err := g.validateDelivery(*e.Delivery)
	return err == nil && all
}

func (g *Goal) acceptDelivery(evidence string, now time.Time) error {
	e := g.Execution
	if e == nil || !e.Policy.RequireHumanAcceptance || g.State != "ready" || g.Revision == 0 || g.SyncError != "" || g.PendingBrief != "" || !g.Delivered() {
		return errors.New("human acceptance requires a synchronized, verified goal with all final deliveries observed merged")
	}
	if strings.TrimSpace(evidence) == "" || len(evidence) > 2000 || strings.ContainsRune(evidence, '\x00') {
		return errors.New("record acceptance evidence in the reason (1–2000 bytes)")
	}
	if now.Before(e.Delivery.CheckedAt) {
		return errors.New("acceptance cannot precede delivery observation")
	}
	e.HumanAcceptance = &HumanAcceptance{Revision: g.Revision, Operation: e.Final.ID, Receipt: e.Final.Receipt, PolicyDigest: e.PolicyDigest, Manifest: maps.Clone(e.Manifest), Evidence: evidence, AcceptedAt: now}
	g.State = "accepted"
	return nil
}

func (g Goal) validateHumanAcceptance() error {
	e := g.Execution
	if e == nil {
		return nil
	}
	a := e.HumanAcceptance
	if a == nil {
		if e.Policy.RequireHumanAcceptance && g.State == "accepted" {
			return errors.New("goal requires a separate human delivery acceptance")
		}
		return nil
	}
	if !e.Policy.RequireHumanAcceptance || g.State != "accepted" || !g.Delivered() || a.Revision == 0 || a.Revision >= g.Revision || a.Operation != e.Final.ID || a.Receipt != e.Final.Receipt || a.PolicyDigest != e.PolicyDigest || !maps.Equal(a.Manifest, e.Manifest) || a.AcceptedAt.IsZero() || a.AcceptedAt.Before(e.Delivery.CheckedAt) || strings.TrimSpace(a.Evidence) == "" || len(a.Evidence) > 2000 || strings.ContainsRune(a.Evidence, '\x00') {
		return errors.New("invalid human acceptance receipt or changed delivered evidence")
	}
	return nil
}

func (g Goal) validateDelivery(d Delivery) (bool, error) {
	e := g.Execution
	if e == nil || e.Final == nil || d.Operation != e.Final.ID || d.Members == nil {
		return false, errors.New("invalid delivery identity")
	}
	if !slices.Contains([]string{"publishing", "published", "merged", "closed", "error"}, d.State) {
		return false, errors.New("invalid delivery state")
	}
	all, changed := len(d.Members) == len(e.Policy.Members), false
	for id, m := range d.Members {
		known := false
		for _, p := range e.Policy.Members {
			if p.ID == id && p.Repo == m.Repo && p.Target == m.Target && e.Manifest[id] == m.Head {
				known = true
			}
		}
		if !known || !slices.Contains([]string{"pending", "unchanged", "published", "merged", "closed"}, m.State) {
			return false, errors.New("delivery member differs from accepted scope or verified head")
		}
		if m.State != "unchanged" {
			changed = true
		}
		if m.State != "merged" && m.State != "unchanged" {
			all = false
		}
		if slices.Contains([]string{"published", "merged", "closed"}, m.State) && (m.Number < 1 || m.URL == "") {
			return false, errors.New("observed delivery member lacks a PR")
		}
	}
	return all && changed, nil
}
