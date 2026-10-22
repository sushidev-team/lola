package knowledge

import "time"

// CaptureRequest identifies a single operator submission before it is sent.
// Reusing CaptureID can recover a lost reply, but cannot replace its contents.
type CaptureRequest struct {
	Scope     Scope    `json:"scope"`
	Proposal  Proposal `json:"proposal"`
	CaptureID string   `json:"capture_id"`
}

// Acceptance is the human-reviewed lesson version, evidence and explicit
// conflicting lessons to supersede. Scope prevents a stale project form from
// applying its decision to a lesson in another project or goal.
type Acceptance struct {
	Scope       Scope        `json:"scope"`
	ID          string       `json:"id"`
	Version     int          `json:"version"`
	Evidence    string       `json:"evidence"`
	Supersedes  []string     `json:"supersedes"`
	CodeTargets []CodeTarget `json:"code_targets,omitempty"`
	Expires     time.Time    `json:"expires,omitempty"`
}
