package protocol

import "time"

// SessionTask is shared session scope, separate from the agent's replaceable plan.
// State is a claim, never a lifecycle or dispatch input.
type SessionTask struct {
	ID           string    `json:"id"`
	ParentID     string    `json:"parentId,omitempty"`
	Scope        string    `json:"scope,omitempty"` // "global" applies across the session; empty + no parent awaits organization.
	Kind         string    `json:"kind"`
	Text         string    `json:"text"`
	State        string    `json:"state"`
	Feedback     string    `json:"feedback,omitempty"` // Latest user feedback; the conversation keeps the full history.
	Author       string    `json:"author"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Notification string    `json:"notification,omitempty"`
	SendError    string    `json:"sendError,omitempty"`
}
