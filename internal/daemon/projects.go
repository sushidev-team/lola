package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// projectsData builds the reply for cmd=projects: every configured [[project]]
// decorated with live status, joined from the config, the per-poll status
// tracker, and the observer's session snapshot. Like sessionsData it serves
// in-memory state and never execs a subprocess — the only filesystem touches are
// a cheap agent-binary LookPath (via runtimeHealth) and an os.Stat path probe.
//
// Agent health is resolved PER PROJECT (its override → [defaults].agent →
// claude), not the default agent, so a project whose agent differs from the
// default reports its own readiness and the TUI gates that project's spawn verbs
// correctly.
func (d *Daemon) projectsData(_ context.Context) protocol.ProjectsData {
	d.mu.Lock()
	health := d.runtimeHealth
	cfgErr := d.cfgErr
	type projMeta struct {
		p         config.Project
		agentKind string
		agentBin  string
		polls     []string
		enabled   int
	}
	metas := make([]projMeta, 0, len(d.cfg.Projects))
	for _, pr := range d.cfg.Projects {
		kind := d.cfg.AgentForProject(pr.Name)
		m := projMeta{p: pr, agentKind: kind, agentBin: agent.Parse(kind).Binary()}
		// A project has at most one polling config (its own). The status tracker
		// is keyed by project name, so m.polls holds the project's own name when
		// it polls.
		if pr.Polls() {
			m.polls = []string{pr.Name}
			if pr.Enabled {
				m.enabled = 1
			}
		}
		metas = append(metas, m)
	}
	d.mu.Unlock()

	snap := d.sessions.Snapshot() // exec-free
	out := make([]protocol.ProjectInfo, 0, len(metas))
	for _, m := range metas {
		info := protocol.ProjectInfo{
			Name:           m.p.Name,
			Path:           m.p.Path,
			Repo:           m.p.Repo,
			DefaultBranch:  m.p.DefaultBranch,
			Agent:          m.agentKind,
			AgentBin:       m.agentBin,
			RepoConfigured: m.p.Repo != "",
			PollCount:      len(m.polls),
			PollsEnabled:   m.enabled,
			Polls:          m.polls,
			PathOK:         projectPathOK(m.p.Path),
		}
		if health != nil {
			if err := health(m.agentBin); err != nil {
				info.AgentErr = err.Error()
			} else {
				info.AgentOK = true
			}
		}
		// Newest LastRun / first LastError across the project's polls; a held
		// config surfaces its error on every project.
		for _, pn := range m.polls {
			ps := d.status.get(pn)
			if ps.LastRun.After(info.LastRun) {
				info.LastRun = ps.LastRun
			}
			if ps.LastError != "" && info.LastError == "" {
				info.LastError = ps.LastError
			}
		}
		if cfgErr != "" && info.LastError == "" {
			info.LastError = cfgErr
		}
		// Session rollups from the snapshot store (never re-derived, never execs).
		for _, s := range snap {
			if s.Source != "native" || s.Project != m.p.Name {
				continue
			}
			info.Sessions++
			if nativeCountingStatuses[s.Status] {
				info.LiveCounted++
			}
			switch s.Status {
			case "needs_input":
				info.NeedsYou++
			case "ci_failed":
				info.CIRed++
			}
			if s.PR != nil && strings.EqualFold(s.PR.State, "OPEN") {
				info.OpenPRs++
			}
		}
		out = append(out, info)
	}
	return protocol.ProjectsData{Projects: out}
}

// projectPathOK reports whether path exists, is a directory, and holds a .git
// entry (a git checkout). A worktree/submodule .git is a file, not a directory,
// so this only checks for its presence. Empty path is not OK. It is a pure
// filesystem probe (os.Stat only), safe on the exec-free projects path.
func projectPathOK(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return false
	}
	return true
}
