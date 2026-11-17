package remote

import (
	"encoding/json"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Team members (docs/team-sessions.md). A member is a teammate's daemon that an
// owner pinned by key. It is NOT a phone: it holds no tier, it connects only
// through the hub relay on a rendezvous the owner published for a share, and
// the one thing it may do is attach to the panes of sessions currently shared
// with it. The policy lives in MemberGate, answered by the daemon from the live
// session store on EVERY frame, so un-sharing takes effect at once.

// Share levels as the gate answers them ("" = no access).
const (
	LevelNone = ""
	LevelView = protocol.ShareLevelView
	LevelType = protocol.ShareLevelType
)

// MemberGate is the daemon's per-frame policy for member peers.
type MemberGate interface {
	// KnownRV reports whether rv is the rendezvous of a currently published
	// share. A member connection on any other rv is refused.
	KnownRV(rv string) bool
	// PaneAccess returns the level the member holds on pane: LevelNone unless
	// pane is the agent pane of a session shared with this member.
	PaneAccess(memberID, pane string) string
	// Roster answers CmdShareSessions for the member; attached is the live
	// subscription list the server holds.
	Roster(memberID string, attached []Attachment) (protocol.ShareSessionsData, error)
}

// Attachment is one live member subscription.
type Attachment struct {
	MemberID string
	Name     string
	Pane     string
	Since    time.Time
}

// MemberAttachments lists the live member subscriptions, for presence.
func (s *Server) MemberAttachments() []Attachment {
	var out []Attachment
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		if c.authed && c.peer.Member {
			conns = append(conns, c)
		}
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.mu.Lock()
		for pane, sub := range c.subs {
			out = append(out, Attachment{MemberID: c.peer.DeviceID, Name: c.peer.Label, Pane: pane, Since: sub.since})
		}
		c.mu.Unlock()
	}
	return out
}

// RecheckMembers ends every member subscription the gate no longer allows and
// every connection whose member entry is gone. The daemon calls it on each
// change of share state and of the member registry.
func (s *Server) RecheckMembers() int {
	gate, reg := s.opts.MemberGate, s.opts.Members
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		if c.authed && c.peer.Member {
			conns = append(conns, c)
		}
	}
	s.mu.Unlock()
	n := 0
	for _, c := range conns {
		if reg == nil || gate == nil {
			c.shutdown()
			n++
			continue
		}
		if d, ok := reg.Get(c.peer.DeviceID); !ok || d.Epoch != c.peer.Epoch {
			c.shutdown()
			n++
			continue
		}
		c.mu.Lock()
		var stale []*subscription
		for pane, sub := range c.subs {
			if gate.PaneAccess(c.peer.DeviceID, pane) == LevelNone {
				stale = append(stale, sub)
				delete(c.subs, pane)
			}
		}
		c.mu.Unlock()
		for _, sub := range stale {
			sub.close()
			c.refusePane("", sub.pane, protocol.CodeUnknownPane, "this session is no longer shared with you")
			n++
		}
	}
	return n
}

// authenticateMember answers a client certificate the phone registry did not
// know. A member is accepted only on a relayed stream whose rendezvous is a
// published share's, so a LAN peer, or a relay peer on a phone's rv, never
// reaches the member path.
func (a *deviceAuthorizer) authenticateMember(hs *Handshake, h [32]byte) (Peer, bool, error) {
	if a.members == nil || a.gate == nil {
		return Peer{}, false, nil
	}
	dev, ok := a.members.Lookup(h)
	if !ok {
		return Peer{}, false, nil
	}
	if hs.Via != ViaRelay || !a.gate.KnownRV(hs.RelayRV) {
		return Peer{}, true, a.rvMismatch(hs, dev.ID)
	}
	a.members.Touch(dev.ID)
	return Peer{DeviceID: dev.ID, Label: dev.Name, Epoch: dev.Epoch, Via: via(hs), Member: true}, true, nil
}

// memberRegistryLive reports whether the peer's member entry still stands.
func (a *deviceAuthorizer) memberRegistryLive(peer Peer) bool {
	if a.members == nil {
		return false
	}
	d, ok := a.members.Get(peer.DeviceID)
	return ok && d.Epoch == peer.Epoch
}

// admitMember is the per-frame gate for a member peer, in place of the tier
// path. Anything it does not name is refused; a refusal costs a strike.
func (c *conn) admitMember(f *protocol.Frame) (verdict, error) {
	if !c.srv.memberLive(c.peer) {
		c.srv.opts.logf()("remote: member=%s closed: entry gone", logSafe(c.peer.DeviceID))
		return verdict{}, errRefused
	}
	deny := func() (verdict, error) {
		c.srv.opts.logf()("remote: member=%s denied type=%s cmd=%s", logSafe(c.peer.DeviceID), logSafe(f.Type), logSafe(f.Cmd))
		return verdict{protocol.CodeDenied, "not permitted for this device"}, c.strike()
	}
	gate := c.srv.opts.MemberGate
	if gate == nil {
		return deny()
	}
	switch f.Type {
	case protocol.FrameReq:
		if f.Cmd != protocol.CmdShareSessions {
			return deny()
		}
	case protocol.FrameSub, protocol.FrameUnsub:
		if f.Type == protocol.FrameSub && gate.PaneAccess(c.peer.DeviceID, f.Pane) == LevelNone {
			return verdict{protocol.CodeUnknownPane, "pane is not available"}, c.strike()
		}
	case protocol.FramePTY:
		var in protocol.PTYInputPayload
		if err := json.Unmarshal(f.Payload, &in); err != nil {
			return deny()
		}
		lvl := gate.PaneAccess(c.peer.DeviceID, f.Pane)
		switch in.Action {
		case protocol.PTYActionWrite:
			if lvl != LevelType {
				return deny()
			}
		case protocol.PTYActionScroll, protocol.PTYActionResize:
			if lvl == LevelNone {
				return deny()
			}
		default:
			return deny()
		}
	case protocol.FrameErr:
	default:
		return deny()
	}
	return verdict{}, nil
}

// memberLive reports whether the member entry the peer authenticated under
// still stands.
func (s *Server) memberLive(peer Peer) bool {
	if s.opts.Members == nil {
		return false
	}
	d, ok := s.opts.Members.Get(peer.DeviceID)
	return ok && d.Epoch == peer.Epoch
}

// handleMemberReq answers the one command a member may send, on the reader
// goroutine: it only reads the gate and the live subscription list.
func (c *conn) handleMemberReq(f *protocol.Frame) error {
	v, err := c.admitMember(f)
	if v.refused() {
		c.refuse(f.ID, v.code, v.msg)
		return err
	}
	if err != nil {
		return err
	}
	data, rerr := c.srv.opts.MemberGate.Roster(c.peer.DeviceID, c.srv.MemberAttachments())
	if rerr != nil {
		c.refuse(f.ID, protocol.CodeInternal, "roster unavailable")
		return nil
	}
	data.Host = c.srv.opts.Host
	raw, jerr := json.Marshal(data)
	if jerr != nil {
		c.refuse(f.ID, protocol.CodeInternal, "roster unavailable")
		return nil
	}
	c.respondWatch(f.ID, protocol.Response{OK: true, Data: raw})
	return nil
}
