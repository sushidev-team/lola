package daemon

// The agent side of ssh. Every handler here acts only as the session named by the
// request, which the bridge has already tied to a token. Refusals are answers with
// OK true, written for the agent to read, because a refusal is something to act on
// and not a transport failure.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshelement"
	"github.com/sushidev-team/lola/internal/sshstore"
)

func (d *Daemon) sshAgentSession(id string) (session.Session, error) {
	if id == "" {
		return session.Session{}, errors.New("no session ($LOLA_SESSION unset)")
	}
	s, ok := d.sessions.Get(id)
	if !ok {
		return session.Session{}, fmt.Errorf("unknown session %s", id)
	}
	return s, nil
}

// sshAgentName is how a human reads this agent's name in a sentence lola writes.
func sshAgentName(s session.Session) string {
	switch s.Agent {
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	}
	return "Claude"
}

func refusal(code, msg string) *protocol.SSHRefusal {
	return &protocol.SSHRefusal{Code: code, Message: msg}
}

func sshUnknownHost(cfg *config.Config, name string) *protocol.SSHRefusal {
	names := make([]string, 0, len(cfg.SSHHosts))
	for _, h := range cfg.SSHHosts {
		names = append(names, h.ID())
	}
	slices.Sort(names)
	if len(names) == 0 {
		return refusal(protocol.SSHRefuseUnknownHost, "No hosts are configured. A human adds them as [[ssh_host]] in lola's config.")
	}
	return refusal(protocol.SSHRefuseUnknownHost, fmt.Sprintf("No host named %q. Configured hosts: %s.", sshClean(name, 64), strings.Join(names, ", ")))
}

func (d *Daemon) sshNotePoll(session string) {
	d.ssh.mu.Lock()
	d.ssh.lastPoll[session] = d.ssh.now()
	d.ssh.mu.Unlock()
}

// handleSSHAgentHosts reports this session's standing per host. The CLI polls it
// to wait for a decision on a request.
func (d *Daemon) handleSSHAgentHosts(id string, a protocol.SSHAgentHostsArgs) (protocol.SSHAgentHostsData, error) {
	s, err := d.sshAgentSession(id)
	if err != nil {
		return protocol.SSHAgentHostsData{}, err
	}
	cfg := d.sshConfig()
	store, err := d.sshStore(s.ID, false)
	if err != nil {
		return protocol.SSHAgentHostsData{}, err
	}
	d.sshNotePoll(s.ID)
	out := protocol.SSHAgentHostsData{Hosts: []protocol.SSHAgentHost{}}
	for _, h := range cfg.SSHHosts {
		if a.Host != "" && a.Host != h.ID() {
			continue
		}
		row := protocol.SSHAgentHost{Name: h.ID(), Tier: h.EffectiveTier(), User: h.User, Allowed: true}
		if err := cfg.SSHHostError(h); err != nil {
			row.Allowed, row.Refused = false, "This host's configuration is invalid: "+err.Error()
		} else if err := h.SSHAdmit(s.Sandbox != ""); err != nil {
			row.Allowed, row.Refused = false, err.Error()
		}
		if store != nil {
			store.View(func(st *sshstore.State) {
				if c := st.ConnForHost(h.ID()); c != nil {
					row.Conn, row.State, row.Wheel, row.Reason = c.ID, c.State, c.Wheel, c.DenyReason
				}
			})
		}
		out.Hosts = append(out.Hosts, row)
	}
	out.Inbox = d.sshInbox(s.ID)
	return out, nil
}

// sshNewConn builds a connection record from config.
func (d *Daemon) sshNewConn(id string, h config.SSHHost, s session.Session, entry, why, scope string, now time.Time) *sshstore.Conn {
	c := &sshstore.Conn{Port: h.Port, IdentityFile: h.IdentityFile}
	c.ID, c.Session, c.Host, c.Label, c.Alias = id, s.ID, h.ID(), h.DisplayName(), h.Alias
	c.Tier, c.User, c.Entry, c.Sandboxed = h.EffectiveTier(), h.User, entry, s.Sandbox != ""
	c.Wheel, c.WheelSince = protocol.SSHWheelAgent, now
	c.Why, c.Scope = why, scope
	c.RequestedAt = now
	c.GrantMaxMinutes = h.EffectiveGrantMaxMinutes()
	c.IdleLingerSeconds = h.EffectiveControlPersist()
	return c
}

// handleSSHAgentRequest asks for a connection. Nothing connects until a human's go,
// and asking again while a request is open returns the same one. After a denial or
// a close a request makes a new connection, so a new reason gets seen.
func (d *Daemon) handleSSHAgentRequest(id string, a protocol.SSHAgentRequestArgs) (protocol.SSHAgentRequestData, error) {
	s, err := d.sshAgentSession(id)
	if err != nil {
		return protocol.SSHAgentRequestData{}, err
	}
	reply := func(r *protocol.SSHRefusal) (protocol.SSHAgentRequestData, error) {
		return protocol.SSHAgentRequestData{Refusal: r, Inbox: d.sshInbox(s.ID)}, nil
	}
	cfg := d.sshConfig()
	h := cfg.SSHHostByName(a.Host)
	if h == nil {
		return reply(sshUnknownHost(cfg, a.Host))
	}
	if err := cfg.SSHHostError(*h); err != nil {
		return reply(refusal(protocol.SSHRefuseUnknownHost, "This host's configuration is invalid: "+err.Error()))
	}
	if err := h.SSHAdmit(s.Sandbox != ""); err != nil {
		return reply(refusal(protocol.SSHRefuseUnsandboxed, err.Error()))
	}
	why := sshClean(a.Why, sshWhyRunes)
	if why == "" {
		return reply(refusal(protocol.SSHRefuseBadCommand, "--why is required. Say what you need this host for: it is shown to the human."))
	}
	if !d.ssh.runLimit.allow(s.ID, d.ssh.now()) {
		return reply(refusal(protocol.SSHRefuseRateLimited, "Too many requests. Wait for the human instead of retrying."))
	}
	store, err := d.sshStore(s.ID, true)
	if err != nil {
		return protocol.SSHAgentRequestData{}, err
	}
	connID, err := sshNewConnID()
	if err != nil {
		return protocol.SSHAgentRequestData{}, err
	}
	now := d.ssh.now()
	var out protocol.SSHAgentRequestData
	created := false
	err = store.Update(func(st *sshstore.State) error {
		if c := st.ConnForHost(h.ID()); c != nil && c.State != protocol.SSHConnDenied {
			out.Conn, out.State = c.ID, c.State
			return nil
		}
		c := d.sshNewConn(connID, *h, s, protocol.SSHEntryRequest, why, sshClean(a.Scope, sshWhyRunes), now)
		c.State = protocol.SSHConnRequested
		st.Conns = append(st.Conns, c)
		st.AddEvent(protocol.SSHSystemEvent{
			Kind: protocol.SSHEventRequested,
			Text: fmt.Sprintf("%s asked to connect to %s.", sshAgentName(s), h.DisplayName()),
			Conn: connID,
		}, now)
		out.Conn, out.State = connID, c.State
		created = true
		return nil
	})
	if err != nil {
		return protocol.SSHAgentRequestData{}, fmt.Errorf("record the request: %w", err)
	}
	if created {
		d.sshIndexConn(connID, s.ID)
		d.logf("", "ssh: %s asked for %s (conn %s)", s.ID, h.ID(), connID)
		d.sshNotify(notify.Urgent, "Connection requested: "+h.DisplayName(), issueLabel(s)+": waiting for your go.")
	}
	out.Inbox = d.sshInbox(s.ID)
	return out, nil
}

// handleSSHAgentRun is the decision order of docs/ssh-sessions.md section 2.3: one
// function, every step fail-closed. Nothing from the agent reaches ssh but the
// command, and that only after a human or a grant allowed it.
func (d *Daemon) handleSSHAgentRun(id string, a protocol.SSHAgentRunArgs) (protocol.SSHAgentRunData, error) {
	s, err := d.sshAgentSession(id)
	if err != nil {
		return protocol.SSHAgentRunData{}, err
	}
	reply := func(b *protocol.SSHBlock, r *protocol.SSHRefusal) (protocol.SSHAgentRunData, error) {
		return protocol.SSHAgentRunData{Block: b, Refusal: r, Inbox: d.sshInbox(s.ID)}, nil
	}
	cfg := d.sshConfig()
	// 1. The catalog.
	h := cfg.SSHHostByName(a.Host)
	if h == nil {
		return reply(nil, sshUnknownHost(cfg, a.Host))
	}
	if err := cfg.SSHHostError(*h); err != nil {
		return reply(nil, refusal(protocol.SSHRefuseUnknownHost, "This host's configuration is invalid: "+err.Error()))
	}
	// 2. The production rule, on what was RECORDED at spawn.
	if err := h.SSHAdmit(s.Sandbox != ""); err != nil {
		return reply(nil, refusal(protocol.SSHRefuseUnsandboxed, err.Error()))
	}
	// 3. The request itself.
	why := sshClean(a.Why, sshWhyRunes)
	if why == "" {
		return reply(nil, refusal(protocol.SSHRefuseBadCommand, "--why is required. It is shown to the human as your reason."))
	}
	if err := sshelement.ValidateCommand(a.Command); err != nil {
		return reply(nil, refusal(protocol.SSHRefuseBadCommand, err.Error()))
	}
	if err := sshelement.ValidateCwd(a.Cwd); err != nil {
		return reply(nil, refusal(protocol.SSHRefuseBadCommand, err.Error()))
	}
	// 4. The rate limit.
	if !d.ssh.runLimit.allow(s.ID, d.ssh.now()) {
		return reply(nil, refusal(protocol.SSHRefuseRateLimited, "Too many commands this minute. Slow down."))
	}
	// 5. A connection for (session, host).
	store, err := d.sshStore(s.ID, false)
	if err != nil {
		return protocol.SSHAgentRunData{}, err
	}
	noConn := refusal(protocol.SSHRefuseNoConnection, fmt.Sprintf("No connection to %s. Ask for one first: lola ssh request %s --why ...", h.ID(), h.ID()))
	if store == nil {
		return reply(nil, noConn)
	}
	cwd := a.Cwd
	if cwd == "" {
		if c := sshAgentConn(store, h.ID()); c != nil {
			cwd = c.Cwd
		}
	}
	if cwd == "" {
		cwd = h.DefaultCwd
	}
	now := d.ssh.now()
	var block *protocol.SSHBlock
	var rf *protocol.SSHRefusal
	var connID string
	notifyApproval := false
	uerr := store.Update(func(st *sshstore.State) error {
		c := st.ConnForHost(h.ID())
		if c == nil {
			rf = noConn
			return nil
		}
		connID = c.ID
		b := protocol.SSHBlock{
			Conn: c.ID, Session: s.ID, Host: h.ID(), Tier: h.EffectiveTier(), User: sshBlockUser(c),
			Command: a.Command, Cwd: cwd, Why: why, Actor: protocol.SSHActorAgent,
			TimeoutSeconds: h.CommandTimeout(a.TimeoutSeconds), CreatedAt: now,
		}
		refuse := func(code, detail, msg string) {
			b.ID = st.NewBlockID()
			b.Status, b.RefusalCode, b.StatusDetail, b.EndedAt = protocol.SSHBlockRefused, code, detail, now
			block = st.AddBlock(b, now)
			store.Prune(st)
			rf = refusal(code, msg)
		}
		switch c.State {
		case protocol.SSHConnRequested:
			refuse(protocol.SSHRefuseAwaitingGo, "Still waiting for your go.", "The human has not given the go for "+h.ID()+" yet. Nothing has connected.")
			return nil
		case protocol.SSHConnDenied:
			msg := "The human denied the connection to " + h.ID() + "."
			if c.DenyReason != "" {
				msg += " Their reason: " + c.DenyReason
			}
			rf = refusal(protocol.SSHRefuseConnDenied, msg)
			return nil
		case protocol.SSHConnConnected:
		default:
			refuse(protocol.SSHRefuseNotConnected, "The connection was not up.", "The connection to "+h.ID()+" is "+c.State+". Only a human can reconnect it.")
			return nil
		}
		// 6. The wheel.
		if c.Wheel == protocol.SSHWheelHuman {
			refuse(protocol.SSHRefuseHumanControl, "You had the wheel.", "The human has control of "+h.ID()+". Wait for the handback. Do not retry.")
			return nil
		}
		// 7. The queue.
		open := 0
		for _, it := range st.Items {
			if it.Kind == protocol.SSHItemBlock && it.Block.Conn == c.ID &&
				(it.Block.Status == protocol.SSHBlockPendingApproval || it.Block.Status == protocol.SSHBlockQueued) {
				open++
			}
		}
		if open >= sshMaxOpen {
			rf = refusal(protocol.SSHRefuseTooManyQueued, fmt.Sprintf("%d commands are already waiting on %s. Wait for them.", open, h.ID()))
			return nil
		}
		// 8. A live grant, else a human. A grant is re-read here, so one the current
		// tier does not allow never covers anything.
		b.ID = st.NewBlockID()
		if c.Grant != nil && now.Before(c.Grant.ExpiresAt) && h.EffectiveGrantMaxMinutes() > 0 {
			b.Status = protocol.SSHBlockQueued
			b.Approval = protocol.SSHApprovalInfo{Mode: protocol.SSHApprovalGrant, Granted: true, At: now, GrantID: c.Grant.ID}
		} else {
			b.Status = protocol.SSHBlockPendingApproval
			b.Approval = protocol.SSHApprovalInfo{Mode: protocol.SSHApprovalPerCommand}
			b.ApprovalExpiresAt = now.Add(time.Duration(h.EffectiveApprovalTimeout()) * time.Second)
			notifyApproval = true
		}
		block = st.AddBlock(b, now)
		store.Prune(st)
		return nil
	})
	if uerr != nil {
		// An unrecorded command breaks the audit guarantee, so it does not run.
		d.logf("", "ssh: %s: cannot record a command: %v", s.ID, uerr)
		if block != nil {
			refused := block.ID
			store.Volatile(func(st *sshstore.State) bool {
				if it := st.Item(refused); it != nil && it.Block != nil {
					it.Block.Status, it.Block.RefusalCode = protocol.SSHBlockRefused, protocol.SSHRefuseStorage
					it.Block.StatusDetail = "lola could not record this command, so it did not run."
					it.Block.EndedAt = now
					st.Touch(it)
				}
				return true
			})
		}
		return reply(nil, refusal(protocol.SSHRefuseStorage, "lola could not record this command, so it did not run."))
	}
	var out *protocol.SSHBlock
	if block != nil {
		out = sshBlockCopy(store, block.ID)
	}
	if notifyApproval {
		// Host, tier and block only. The command is agent-written and may hold a
		// credential, and this can land in a Slack channel. It belongs on the card.
		body := h.EffectiveTier()
		if out != nil {
			body += " · command " + out.ID
		}
		d.sshNotify(notify.Urgent, "Approval needed: "+h.DisplayName(), body+". Open Lola to read it.")
	}
	if rf == nil && out != nil && out.Status == protocol.SSHBlockQueued {
		d.sshDispatch(store, connID)
		out = sshBlockCopy(store, out.ID)
	}
	return reply(out, rf)
}

// sshBlockUser is what a block records as its user: what ssh -G resolved, else what
// config names.
func sshBlockUser(c *sshstore.Conn) string {
	if c.ResolvedUser != "" {
		return c.ResolvedUser
	}
	return c.User
}

func sshBlockCopy(store *sshstore.Store, id string) *protocol.SSHBlock {
	var out *protocol.SSHBlock
	store.View(func(st *sshstore.State) {
		if b := st.Block(id); b != nil {
			cp := *b
			out = &cp
		}
	})
	return out
}

// handleSSHAgentPoll is a block's state and its output past the given offsets. The
// lookup is per session, so another session's block reads as unknown.
func (d *Daemon) handleSSHAgentPoll(id string, a protocol.SSHAgentPollArgs) (protocol.SSHAgentPollData, error) {
	s, err := d.sshAgentSession(id)
	if err != nil {
		return protocol.SSHAgentPollData{}, err
	}
	store, err := d.sshStore(s.ID, false)
	if err != nil {
		return protocol.SSHAgentPollData{}, err
	}
	unknown := fmt.Errorf("unknown block %s", sshClean(a.Block, 32))
	if store == nil {
		return protocol.SSHAgentPollData{}, unknown
	}
	b := sshBlockCopy(store, a.Block)
	if b == nil || b.Private {
		return protocol.SSHAgentPollData{}, unknown
	}
	d.sshNotePoll(s.ID)
	terminal := protocol.SSHBlockTerminal(b.Status)
	out := protocol.SSHAgentPollData{Block: *b, StdoutOffset: a.StdoutOffset, StderrOffset: a.StderrOffset}
	var soEnd, seEnd int64
	if !b.OutputPruned {
		so, sn, se, err := store.ReadPoll(b.ID, protocol.SSHStreamStdout, a.StdoutOffset, protocol.SSHMaxPollBytes, b.DroppedBytes, terminal)
		if err != nil {
			return protocol.SSHAgentPollData{}, err
		}
		eo, en, ee, err := store.ReadPoll(b.ID, protocol.SSHStreamStderr, a.StderrOffset, protocol.SSHMaxPollBytes, b.DroppedBytes, terminal)
		if err != nil {
			return protocol.SSHAgentPollData{}, err
		}
		out.Stdout, out.StdoutOffset, soEnd = string(so), sn, se
		out.Stderr, out.StderrOffset, seEnd = string(eo), en, ee
	}
	out.Done = terminal && out.StdoutOffset >= soEnd && out.StderrOffset >= seEnd
	out.Inbox = d.sshInbox(s.ID)
	return out, nil
}

// handleSSHAgentCancel lets an agent stop its own block. It never drains the inbox:
// a CLI on its way out would lose the words.
func (d *Daemon) handleSSHAgentCancel(id string, a protocol.SSHAgentCancelArgs) (protocol.SSHBlock, error) {
	s, err := d.sshAgentSession(id)
	if err != nil {
		return protocol.SSHBlock{}, err
	}
	store, err := d.sshStore(s.ID, false)
	if err != nil {
		return protocol.SSHBlock{}, err
	}
	if store == nil {
		return protocol.SSHBlock{}, fmt.Errorf("unknown block %s", sshClean(a.Block, 32))
	}
	// Its own block only. A command the human chose to run is not the agent's to stop.
	if b := sshBlockCopy(store, a.Block); b == nil || b.Actor != protocol.SSHActorAgent {
		return protocol.SSHBlock{}, fmt.Errorf("unknown block %s", sshClean(a.Block, 32))
	}
	return d.sshStopBlock(store, a.Block), nil
}

// handleSSHAgentPost publishes one element. It is a CLAIM: display-only, and nothing
// in it reaches a state machine, a grant, a notification or NeedsHuman.
func (d *Daemon) handleSSHAgentPost(id string, a protocol.SSHAgentPostArgs) (protocol.SSHAgentPostData, error) {
	s, err := d.sshAgentSession(id)
	if err != nil {
		return protocol.SSHAgentPostData{}, err
	}
	now := d.ssh.now()
	if !d.ssh.postLimit.allow(s.ID, now) {
		return protocol.SSHAgentPostData{}, errors.New("lola ssh post: rate limited. Post on meaningful changes only")
	}
	p, err := sshelement.Parse(a.Argv)
	if err != nil {
		return protocol.SSHAgentPostData{}, err
	}
	cfg := d.sshConfig()
	if p.Host != "" && cfg.SSHHostByName(p.Host) == nil {
		return protocol.SSHAgentPostData{}, fmt.Errorf("lola ssh post: no host named %q", p.Host)
	}
	store, err := d.sshStore(s.ID, true)
	if err != nil {
		return protocol.SSHAgentPostData{}, err
	}
	var out protocol.SSHAgentPostData
	err = store.Update(func(st *sshstore.State) error {
		// Evidence is checked against this session's own record, and the facts copied
		// here are lola's. An unknown block rejects the post, so the agent can correct
		// itself and a human never finds a link going nowhere.
		if err := sshelement.Resolve(&p.Element, func(block string) (sshelement.BlockFact, bool) {
			b := st.Block(block)
			if b == nil || b.Private {
				return sshelement.BlockFact{}, false
			}
			return sshelement.BlockFact{Status: b.Status, ExitCode: b.ExitCode}, true
		}); err != nil {
			return err
		}
		conn := sshPostConn(st, p.Host, p.Element.Evidence)
		var e sshelement.Element
		prevEl := st.ElementByRef(string(p.Element.Kind), p.Element.Ref)
		if prevEl == nil && p.Element.Kind == sshelement.KindWidget {
			n := 0
			for _, it := range st.Items {
				if it.Element != nil && it.Element.Kind == string(sshelement.KindWidget) {
					n++
				}
			}
			if n >= sshelement.MaxWidgets {
				return fmt.Errorf("lola ssh post: this session already has %d widgets. Update one with --ref", n)
			}
		}
		if prev := prevEl; prev != nil {
			merged, err := p.Merge(sshElementFromWire(*prev), now)
			if err != nil {
				return err
			}
			e = merged
			if p.Host != "" {
				e.Conn = conn
			}
		} else {
			created, err := p.New(st.NewElementID(), now)
			if err != nil {
				return err
			}
			e = created
			e.Conn = conn
		}
		st.PutElement(sshElementToWire(e), now)
		store.Prune(st)
		out.ID, out.Revision = e.ID, e.Revision
		return nil
	})
	if err != nil {
		return protocol.SSHAgentPostData{}, err
	}
	out.Inbox = d.sshInbox(s.ID)
	return out, nil
}

// sshPostConn attaches a post to a connection: the named host's, else the one its evidence
// ran on. With neither it belongs to the whole session, because narration must not die with
// a tab. TalkConn still routes ConnForHost, which is about commands and not about posts.
func sshPostConn(st *sshstore.State, host string, evidence []sshelement.Evidence) string {
	if host != "" {
		if c := st.ConnForHost(host); c != nil {
			return c.ID
		}
		return ""
	}
	for _, e := range evidence {
		if b := st.Block(e.Block); b != nil && b.Conn != "" {
			return b.Conn
		}
	}
	return ""
}

func sshElementToWire(e sshelement.Element) protocol.SSHElement {
	out := protocol.SSHElement{
		ID: e.ID, Ref: e.Ref, Kind: string(e.Kind), Conn: e.Conn, Title: e.Title, Body: e.Body,
		State: e.State, Severity: e.Severity, Command: e.Command, Cwd: e.Cwd, Choices: e.Choices,
		HTML: e.HTML, Data: e.Data, Height: e.Height,
		Revision: e.Revision, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
	for _, s := range e.Steps {
		out.Steps = append(out.Steps, protocol.SSHElementStep{Text: s.Text, State: s.State})
	}
	for _, m := range e.Metrics {
		out.Metrics = append(out.Metrics, protocol.SSHElementMetric{Name: m.Name, Value: m.Value, Unit: m.Unit})
	}
	for _, v := range e.Evidence {
		out.Evidence = append(out.Evidence, protocol.SSHElementEvidence{Block: v.Block, Verified: v.Verified, Status: v.Status, ExitCode: v.ExitCode})
	}
	if e.Outcome != nil {
		out.Outcome = &protocol.SSHElementOutcome{Text: e.Outcome.Text, Block: e.Outcome.Block, At: e.Outcome.At}
	}
	return out
}

func sshElementFromWire(e protocol.SSHElement) sshelement.Element {
	out := sshelement.Element{
		ID: e.ID, Ref: e.Ref, Kind: sshelement.Kind(e.Kind), Conn: e.Conn, Title: e.Title, Body: e.Body,
		State: e.State, Severity: e.Severity, Command: e.Command, Cwd: e.Cwd, Choices: e.Choices,
		HTML: e.HTML, Data: e.Data, Height: e.Height,
		Revision: e.Revision, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
	for _, s := range e.Steps {
		out.Steps = append(out.Steps, sshelement.Step{Text: s.Text, State: s.State})
	}
	for _, m := range e.Metrics {
		out.Metrics = append(out.Metrics, sshelement.Metric{Name: m.Name, Value: m.Value, Unit: m.Unit})
	}
	for _, v := range e.Evidence {
		out.Evidence = append(out.Evidence, sshelement.Evidence{Block: v.Block, Verified: v.Verified, Status: v.Status, ExitCode: v.ExitCode})
	}
	if e.Outcome != nil {
		out.Outcome = &sshelement.Outcome{Text: e.Outcome.Text, Block: e.Outcome.Block, At: e.Outcome.At}
	}
	return out
}

// sshAgentConn is a host's current connection, for the folder a command inherits.
func sshAgentConn(store *sshstore.Store, host string) *sshstore.Conn {
	if store == nil {
		return nil
	}
	var out *sshstore.Conn
	store.View(func(st *sshstore.State) {
		if c := st.ConnForHost(host); c != nil {
			k := *c
			out = &k
		}
	})
	return out
}
