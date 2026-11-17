package daemon

// A direct session, and the glance on cmd=sessions.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshelement"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// sshOpener is the runtime half of a direct session. It stays out of NativeAPI, so
// the many fakes that have no use for it need no stub.
type sshOpener interface {
	OpenSSH(ctx context.Context, sp runtime.SSHOpenSpec) (session.Session, error)
}

// handleSSHOpen starts an agent-assisted session on a host: an ordinary local session
// plus one connection. Starting it IS the go for connecting, and command approval
// still applies. The production rule is config.SSHDirectSandbox's, in one place.
func (d *Daemon) handleSSHOpen(ctx context.Context, a protocol.SSHOpenArgs) (protocol.SSHOpenData, error) {
	cfg := d.sshConfig()
	h := cfg.SSHHostByName(a.Host)
	if h == nil {
		return protocol.SSHOpenData{}, fmt.Errorf("no host named %q", sshClean(a.Host, 64))
	}
	if err := cfg.SSHHostError(*h); err != nil {
		return protocol.SSHOpenData{}, fmt.Errorf("host %s has an invalid configuration: %w", h.ID(), err)
	}
	sandbox, err := cfg.SSHDirectSandbox(*h)
	if err != nil {
		return protocol.SSHOpenData{}, err
	}
	if err := validateSessionName(a.Name); err != nil {
		return protocol.SSHOpenData{}, err
	}
	wheel := protocol.SSHWheelAgent
	switch a.Wheel {
	case "", protocol.SSHWheelAgent:
	case protocol.SSHWheelHuman:
		wheel = protocol.SSHWheelHuman
	default:
		return protocol.SSHOpenData{}, fmt.Errorf("wheel must be %s or %s", protocol.SSHWheelAgent, protocol.SSHWheelHuman)
	}
	kind := sshDirectAgent(cfg, *h)
	if a.AgentKind != "" {
		if !agent.Valid(a.AgentKind) {
			return protocol.SSHOpenData{}, errors.New("unknown coding agent")
		}
		kind = a.AgentKind
	}
	// The gate is tmux, ssh, and what launches the agent: docker when sandboxed (the
	// agent is in the image), its binary otherwise. No git: there is no checkout.
	bin := agent.Parse(kind).Binary()
	if sandbox == config.SandboxDocker {
		bin = cfg.Sandbox.DockerBin()
	}
	for _, b := range []string{d.ssh.client.Binary(), bin} {
		if _, err := d.ssh.lookPath(b); err != nil {
			return protocol.SSHOpenData{}, fmt.Errorf("runtime not ready: %s is not on PATH", b)
		}
	}
	tm := d.sshTmuxClient()
	if tm == nil || !tm.Available() {
		return protocol.SSHOpenData{}, errors.New("runtime not ready: tmux is not available")
	}
	d.mu.Lock()
	nat := d.native
	d.mu.Unlock()
	opener, ok := nat.(sshOpener)
	if !ok {
		return protocol.SSHOpenData{}, errors.New("native runtime unavailable")
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return protocol.SSHOpenData{}, err
	}
	id := "lola-local-" + hex.EncodeToString(suffix[:])
	connID, err := sshNewConnID()
	if err != nil {
		return protocol.SSHOpenData{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	defer cancel()
	s, err := opener.OpenSSH(cctx, runtime.SSHOpenSpec{
		ID: id, Kind: kind, Host: *h, Sandbox: sandbox, Brief: sshelement.Para(a.Brief, protocol.SSHMaxSayBytes),
	})
	if err != nil {
		return protocol.SSHOpenData{}, err
	}
	s.Name = strings.TrimSpace(a.Name)
	d.sessions.Upsert(s)
	rollback := func(cause error) (protocol.SSHOpenData, error) {
		_ = nat.Kill(cctx, s, true, false)
		d.sessions.Delete(id)
		return protocol.SSHOpenData{}, cause
	}
	if err := d.sessions.Save(); err != nil {
		return rollback(fmt.Errorf("persist session: %w", err))
	}
	d.recordSessionEvent("", s)
	store, err := d.sshStore(id, true)
	if err != nil {
		return rollback(err)
	}
	now := d.ssh.now()
	c := d.sshNewConn(connID, *h, s, protocol.SSHEntryDirect, "", "", now)
	c.State = protocol.SSHConnRequested // sshConnect moves it on at once
	c.Wheel = wheel
	if err := store.Update(func(st *sshstore.State) error {
		st.Conns = append(st.Conns, c)
		return nil
	}); err != nil {
		return rollback(fmt.Errorf("record the connection: %w", err))
	}
	d.sshIndexConn(connID, id)
	info, err := d.sshConnect(cctx, store, connID, *h)
	if err != nil {
		d.logf("", "ssh: %s: connect: %v", id, err)
	}
	warning := protocol.SSHDataWarning
	if sandbox != config.SandboxDocker {
		warning += " " + protocol.SSHUnsandboxedNote
	}
	out := protocol.SSHOpenData{Session: id, Conn: info, Warning: warning}
	if sandbox == config.SandboxDocker {
		out.Sandbox = config.SandboxDocker
	}
	return out, nil
}

// sshGlance is what rides on cmd=sessions: from memory, no exec. NeedsHuman comes
// from daemon facts only. Nothing an agent posted can raise it, so a looping agent
// cannot cry wolf with questions.
func (d *Daemon) sshGlance(id string) *protocol.SSHSessionInfo {
	d.ssh.mu.Lock()
	store := d.ssh.stores[id]
	d.ssh.mu.Unlock()
	if store == nil {
		return nil
	}
	var out *protocol.SSHSessionInfo
	store.View(func(st *sshstore.State) {
		if len(st.Conns) == 0 && len(st.Items) == 0 {
			return
		}
		g := &protocol.SSHSessionInfo{Connections: []protocol.SSHConnInfo{}, LastSeq: st.Seq}
		var prompt, auth, approval, goFor string
		for _, c := range st.Conns {
			info := st.ConnInfo(c)
			d.ssh.prompts.fill(&info)
			g.Connections = append(g.Connections, info)
			if info.Prompt != nil && prompt == "" {
				prompt = sshDisplay(c) + " is asking for input."
			}
			switch c.State {
			case protocol.SSHConnAuthenticating:
				if auth == "" {
					auth = "Sign in to " + sshDisplay(c) + "."
				}
			case protocol.SSHConnRequested:
				if goFor == "" {
					goFor = sshDisplay(c) + " is waiting for your go."
				}
			}
		}
		for _, it := range st.Items {
			switch it.Kind {
			case protocol.SSHItemBlock:
				switch it.Block.Status {
				case protocol.SSHBlockPendingApproval:
					if g.NextApproval == nil {
						b := *it.Block
						g.NextApproval = &b
						approval = "A command on " + it.Block.Host + " is waiting for your approval."
					}
				case protocol.SSHBlockRunning:
					g.RunningCommand = sshClean(it.Block.Command, 120)
				}
			case protocol.SSHItemElement:
				if it.Element.Kind == string(sshelement.KindSummary) {
					g.Headline = sshelement.Line(it.Element.Title, sshelement.MaxTitleRunes)
				}
				if it.Element.Outcome == nil && (it.Element.Kind == string(sshelement.KindQuestion) || it.Element.Kind == string(sshelement.KindProposal)) {
					g.OpenItems++
				}
			}
		}
		for _, why := range []string{prompt, auth, approval, goFor} {
			if why != "" {
				g.NeedsHuman, g.NeedsHumanWhy = true, why
				break
			}
		}
		out = g
	})
	return out
}
