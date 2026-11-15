package daemon

import (
	"slices"

	"github.com/sushidev-team/lola/internal/protocol"
)

// handleSessionTimeline serves cmd=sessionTimeline: the Conversation timeline a
// phone may read. Same data and paging as sshTimeline, but every value is
// rebuilt by publicTimeline, so nothing the human keeps private (typed
// commands, shell prompt cards, handback transcripts) or that only the desktop
// can render safely (agent-authored widget documents) leaves the machine.
func (d *Daemon) handleSessionTimeline(a protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error) {
	data, err := d.handleSSHTimeline(protocol.SSHTimelineArgs{Session: a.Session, Conn: a.Conn, After: a.After, Limit: a.Limit})
	if err != nil {
		return protocol.SSHTimelineData{}, err
	}
	return publicTimeline(data), nil
}

// publicTimeline builds a NEW value by ALLOWLIST copy: a field added to these
// types later is absent from the result until it is added here
// (TestPublicTimelineIsAnAllowlist pins the kept set by reflection). The
// timeline:<session> watch topic must serve this output, never
// handleSSHTimeline's.
func publicTimeline(in protocol.SSHTimelineData) protocol.SSHTimelineData {
	out := protocol.SSHTimelineData{
		Session:     in.Session,
		Connections: make([]protocol.SSHConnInfo, 0, len(in.Connections)),
		Items:       make([]protocol.SSHTimelineItem, 0, len(in.Items)),
		LastSeq:     in.LastSeq,
		More:        in.More,
	}
	for _, c := range in.Connections {
		out.Connections = append(out.Connections, publicConn(c))
	}
	for _, it := range in.Items {
		// A command the human typed is theirs until shared. Dropping the row
		// leaves a Seq gap, which is fine: clients page by LastSeq.
		if it.Block != nil && it.Block.Private {
			continue
		}
		out.Items = append(out.Items, publicItem(it))
	}
	return out
}

func publicConn(c protocol.SSHConnInfo) protocol.SSHConnInfo {
	return protocol.SSHConnInfo{
		ID:          c.ID,
		Session:     c.Session,
		Alias:       c.Alias,
		Label:       c.Label,
		Tier:        c.Tier,
		Entry:       c.Entry,
		Sandboxed:   c.Sandboxed,
		State:       c.State,
		Wheel:       c.Wheel,
		WheelSince:  c.WheelSince,
		Why:         c.Why,
		Scope:       c.Scope,
		RequestedAt: c.RequestedAt,
	}
}

func publicItem(it protocol.SSHTimelineItem) protocol.SSHTimelineItem {
	out := protocol.SSHTimelineItem{
		ID:    it.ID,
		Kind:  it.Kind,
		Order: it.Order,
		Seq:   it.Seq,
		At:    it.At,
	}
	if it.Block != nil {
		b := publicBlock(*it.Block)
		out.Block = &b
	}
	if it.Element != nil {
		e := publicElement(*it.Element)
		out.Element = &e
	}
	if it.System != nil {
		s := *it.System
		out.System = &s
	}
	if it.Human != nil {
		h := publicHuman(*it.Human)
		out.Human = &h
	}
	return out
}

// publicBlock keeps everything but the host the command ran on, the user and
// the directory: those name the machine, not the story.
func publicBlock(b protocol.SSHBlock) protocol.SSHBlock {
	out := protocol.SSHBlock{
		ID:                b.ID,
		Conn:              b.Conn,
		Session:           b.Session,
		Tier:              b.Tier,
		Command:           b.Command,
		Why:               b.Why,
		Actor:             b.Actor,
		Status:            b.Status,
		Approval:          b.Approval,
		StatusDetail:      b.StatusDetail,
		RefusalCode:       b.RefusalCode,
		TimeoutSeconds:    b.TimeoutSeconds,
		ApprovalExpiresAt: b.ApprovalExpiresAt,
		CreatedAt:         b.CreatedAt,
		StartedAt:         b.StartedAt,
		EndedAt:           b.EndedAt,
		DurationMs:        b.DurationMs,
		StdoutBytes:       b.StdoutBytes,
		StderrBytes:       b.StderrBytes,
		DroppedBytes:      b.DroppedBytes,
		Preview:           b.Preview,
		PreviewStream:     b.PreviewStream,
		Binary:            b.Binary,
		OutputPruned:      b.OutputPruned,
		ProposalElement:   b.ProposalElement,
		Origin:            b.Origin,
		Elevated:          b.Elevated,
	}
	// Private is not copied: a private block never reaches here.
	out.ExitCode = cloneIntPtr(b.ExitCode)
	return out
}

// publicElement drops the agent-authored widget document (HTML, Data) and the
// directory: a phone renders an element's text fields only.
func publicElement(e protocol.SSHElement) protocol.SSHElement {
	out := protocol.SSHElement{
		ID:        e.ID,
		Ref:       e.Ref,
		Kind:      e.Kind,
		Conn:      e.Conn,
		Title:     e.Title,
		Body:      e.Body,
		State:     e.State,
		Severity:  e.Severity,
		Steps:     slices.Clone(e.Steps),
		Metrics:   slices.Clone(e.Metrics),
		Command:   e.Command,
		Choices:   slices.Clone(e.Choices),
		Height:    e.Height,
		Revision:  e.Revision,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
	if e.Evidence != nil {
		out.Evidence = make([]protocol.SSHElementEvidence, len(e.Evidence))
		for i, ev := range e.Evidence {
			ev.ExitCode = cloneIntPtr(ev.ExitCode)
			out.Evidence[i] = ev
		}
	}
	if e.Outcome != nil {
		o := *e.Outcome
		out.Outcome = &o
	}
	return out
}

// publicHuman drops the handback transcript: exactly what the human's terminal
// showed, which the human shared with the agent and not with a phone.
func publicHuman(h protocol.SSHHumanMessage) protocol.SSHHumanMessage {
	return protocol.SSHHumanMessage{
		Origin:      h.Origin,
		Conn:        h.Conn,
		Text:        h.Text,
		Delivered:   h.Delivered,
		DeliveredAt: h.DeliveredAt,
		Via:         h.Via,
		Recalled:    h.Recalled,
		Returned:    h.Returned,
	}
}

func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
