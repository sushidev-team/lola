package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/projectgit"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshelement"
	"github.com/sushidev-team/lola/internal/sshstore"
)

func validateSessionName(name string) error {
	if utf8.RuneCountInString(name) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return errors.New("session name must be at most 100 characters without control characters")
	}
	return nil
}

func isLocalShell(s session.Session) bool {
	return s.Kind == session.KindLocal && s.IsAgentless() &&
		(s.Location == "temporary" || s.Location == "main" || s.Location == "checkout")
}

// A PTY EOF can mean a detached client, not an exited shell. Retire only a
// borrowed-directory shell whose primary and auxiliary tmux sessions are ALL
// gone. The command keeps its original name for existing clients.
func (d *Daemon) handleCloseExitedTemporary(ctx context.Context, id string) (protocol.KillData, error) {
	s, ok := d.sessions.Get(id)
	if !ok {
		return protocol.KillData{Removed: true}, nil
	}
	if !isLocalShell(s) {
		return protocol.KillData{}, errors.New("session is not a local shell")
	}
	d.mu.Lock()
	nat, list := d.native, d.listTmuxSessions
	d.mu.Unlock()
	if nat == nil || list == nil {
		return protocol.KillData{}, errors.New("cannot verify local shell liveness")
	}
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	defer cancel()
	live, err := list(cctx)
	if err != nil {
		return protocol.KillData{}, err
	}
	parent := paneTarget(s)
	for _, pane := range live {
		if pane.Name == parent || strings.HasPrefix(pane.Name, parent+"-") {
			return protocol.KillData{Removed: false}, nil
		}
	}
	d.sessions.Delete(id)
	if err := d.sessions.Save(); err != nil {
		d.sessions.Upsert(s)
		return protocol.KillData{}, err
	}
	// Never call Kill from a PTY-exit notification: even if another tab appeared
	// after the listing, there is no process termination on this path.
	if err := runtime.RemoveLocalArtifacts(s.ID); err != nil {
		d.logf("", "local shell %s: remove launch artifacts: %v", id, err)
	}
	return protocol.KillData{Removed: true}, nil
}

func (d *Daemon) handleRenameSession(id, name string) error {
	name = strings.TrimSpace(name)
	if err := validateSessionName(name); err != nil {
		return err
	}
	if _, ok := d.sessions.Get(id); !ok {
		return fmt.Errorf("unknown session %s", id)
	}
	d.sessions.Update(id, func(s *session.Session) bool { s.Name = name; return true })
	return d.sessions.Save()
}

// directoryPrompter is the runtime half of a prompt on a borrowed directory. It stays out of
// NativeAPI for the same reason sshOpener does.
type directoryPrompter interface {
	OpenDirectoryPrompt(ctx context.Context, p config.Project, id, dir, location, kind string, useAgent bool, prompt string) (session.Session, error)
	LocalFirstTurn(id, prompt string) string
}

type directoryProfilePrompter interface {
	OpenDirectoryProfile(ctx context.Context, p config.Project, id, dir, location, kind string, useAgent bool, prompt, model, effort string) (session.Session, error)
	LocalFirstTurn(id, prompt string) string
}

// noteInitial writes a session's first row: the brief a human gave it. It is created
// delivered, because it was, and so that no client offers to recall it. typed is the text
// lola gave the agent as its first turn, when the log will carry a record of that.
func (d *Daemon) noteInitial(id, prompt, typed string) {
	text := strings.TrimSpace(humanText(prompt))
	if text == "" {
		return
	}
	if len(text) > protocol.SSHMaxSayBytes {
		text = sshelement.Para(text, protocol.SSHMaxSayBytes) // a row, and the agent has the whole brief
	}
	now := time.Now()
	ev := timelineEvent{Session: id, Human: &protocol.SSHHumanMessage{
		Origin: protocol.SSHHumanInitial, Text: text, Delivered: true, DeliveredAt: now,
	}}
	if typed != "" {
		ev.Sent = &sshstore.SentBatch{Hash: sentHash(typed), At: now}
	}
	d.emitSystem(ev)
}

// A borrowed directory never becomes a worktree owned by Lola. No checkout,
// setup hooks, branch mutations or implicit project configuration occur here.
func (d *Daemon) handleOpenDirectory(ctx context.Context, a protocol.OpenManualArgs) (protocol.OpenData, error) {
	if a.Location != "main" && a.Location != "checkout" && a.Location != "temporary" {
		return protocol.OpenData{}, errors.New("unknown session location")
	}
	if err := validateSessionName(a.Name); err != nil {
		return protocol.OpenData{}, err
	}
	if a.AIAction != "" && (a.AIAction != "resolve" || a.Location != "checkout" || !a.Agent) {
		return protocol.OpenData{}, errors.New("invalid AI action for this session")
	}
	if a.Branch != "" || a.Base != "" {
		return protocol.OpenData{}, errors.New("branch options require a new worktree")
	}
	if a.Location == "temporary" && (a.Agent || a.Project != "") {
		return protocol.OpenData{}, errors.New("temporary sessions are project-free shells")
	}
	d.mu.Lock()
	nat, health := d.native, d.runtimeHealth
	var p config.Project
	configured := d.cfg.ProjectByName(a.Project)
	if configured != nil {
		p = *configured
	}
	kind := d.cfg.AgentForProject(a.Project)
	profile, useProfile := d.cfg.AIProfile(kind, a.AIAction)
	dir := d.cfg.Defaults.TemporaryDirectory
	home, validateCheckout := d.home, d.validateLocalCheckout
	d.mu.Unlock()
	if nat == nil {
		return protocol.OpenData{}, errors.New("native runtime unavailable")
	}
	if a.Location == "main" || a.Location == "checkout" {
		if configured == nil || p.IsStack() || p.Path == "" {
			return protocol.OpenData{}, fmt.Errorf("unknown project %q", a.Project)
		}
		if a.Location == "main" && a.Path != "" {
			return protocol.OpenData{}, errors.New("main checkout uses the configured project path")
		}
		dir = p.Path
		if a.Location == "checkout" {
			if a.Path == "" {
				return protocol.OpenData{}, errors.New("checkout path required")
			}
			dir = a.Path
		}
	} else if a.Path != "" {
		dir = a.Path
	}
	if dir == "" {
		dir = "~"
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return protocol.OpenData{}, err
		}
		dir = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/"))
	}
	if !filepath.IsAbs(dir) {
		return protocol.OpenData{}, errors.New("session directory must be an absolute path or start with ~/")
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return protocol.OpenData{}, fmt.Errorf("session directory: %w", err)
	}
	if a.Location == "checkout" {
		if validateCheckout == nil {
			validateCheckout = validateBorrowedCheckout
		}
		checkCtx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		err := validateCheckout(checkCtx, p.Path, dir, home)
		cancel()
		if err != nil {
			return protocol.OpenData{}, err
		}
	}
	if a.AgentKind != "" {
		if !agent.Valid(a.AgentKind) {
			return protocol.OpenData{}, errors.New("unknown coding agent")
		}
		kind = a.AgentKind
		d.mu.Lock()
		profile, useProfile = d.cfg.AIProfile(kind, a.AIAction)
		d.mu.Unlock()
	}
	bin := "tmux"
	if a.Agent {
		bin = agent.Parse(kind).Binary()
	}
	if err := health(bin); err != nil {
		return protocol.OpenData{}, fmt.Errorf("runtime not ready: %w", err)
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return protocol.OpenData{}, err
	}
	id := "lola-local-" + hex.EncodeToString(suffix[:])
	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	defer cancel()
	prompt := ""
	if a.Agent {
		prompt = strings.TrimSpace(humanText(a.Prompt))
		if len(prompt) > protocol.SSHMaxSayBytes {
			return protocol.OpenData{}, fmt.Errorf("this brief is %d bytes and the limit is %d, so the session was not started", len(prompt), protocol.SSHMaxSayBytes)
		}
	}
	var s session.Session
	typed := ""
	if a.AIAction != "" && useProfile {
		opener, ok := nat.(directoryProfilePrompter)
		if !ok {
			return protocol.OpenData{}, errors.New("runtime cannot apply the selected AI profile")
		}
		s, err = opener.OpenDirectoryProfile(cctx, p, id, dir, a.Location, kind, a.Agent, prompt, profile.Model, profile.Effort)
		typed = opener.LocalFirstTurn(id, prompt)
	} else if opener, ok := nat.(directoryPrompter); ok && prompt != "" {
		s, err = opener.OpenDirectoryPrompt(cctx, p, id, dir, a.Location, kind, a.Agent, prompt)
		typed = opener.LocalFirstTurn(id, prompt)
	} else {
		prompt = "" // a runtime that cannot pass it on: no row for words nobody was given
		s, err = nat.OpenDirectory(cctx, p, id, dir, a.Location, kind, a.Agent)
	}
	if err != nil {
		return protocol.OpenData{}, err
	}
	s.Name = strings.TrimSpace(a.Name)
	d.sessions.Upsert(s)
	if err := d.sessions.Save(); err != nil {
		_ = nat.Kill(cctx, s, false, false)
		d.sessions.Delete(id)
		return protocol.OpenData{}, fmt.Errorf("persist session: %w", err)
	}
	d.recordSessionEvent("", s)
	d.noteInitial(id, prompt, typed)
	return protocol.OpenData{SessionID: id, Worktree: dir, Message: "Session opened"}, nil
}

// Only a registered, non-isolated checkout can be borrowed. Managed directories
// must use their existing session; a second host agent must never enter a sandbox.
func validateBorrowedCheckout(ctx context.Context, root, dir, home string) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if gitguard.Pinned(root) || gitguard.Pinned(dir) {
		return errors.New("isolated checkouts must use their existing session")
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	if localPathWithin(root, home) || localPathWithin(dir, home) {
		return errors.New("managed checkouts must use their existing session")
	}
	trees, err := (projectgit.Client{}).Worktrees(ctx, root)
	if err != nil {
		return err
	}
	return registeredBorrowedCheckout(dir, trees)
}
func localPathWithin(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}
func registeredBorrowedCheckout(dir string, trees []projectgit.Worktree) error {
	for _, tree := range trees {
		path, err := filepath.EvalSymlinks(tree.Path)
		if err == nil && path == dir && !tree.Locked && !tree.Prunable {
			return nil
		}
	}
	return errors.New("directory is not an available checkout of this project")
}
