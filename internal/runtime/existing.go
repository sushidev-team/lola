package runtime

import (
	"context"
	"errors"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// OpenExisting runs on an existing branch. Like Open and OpenPRAgent, it uses
// the non-owning pr kind: the branch survives launch failure and teardown.
func (n *Native) OpenExisting(ctx context.Context, p config.Project, id, branch, prompt, agentOverride string, useAgent bool) (session.Session, error) {
	if err := needsCheckout(p, "OpenExisting"); err != nil {
		return session.Session{}, err
	}
	if id == "" || branch == "" {
		return session.Session{}, errors.New("runtime: open existing: session id and branch required")
	}
	dir, err := n.WT.CheckoutExisting(ctx, p, id, branch)
	if err != nil {
		return session.Session{}, err
	}
	if !useAgent {
		return n.finishShellLaunch(ctx, p, id, dir, branch, session.KindPR)
	}
	kind := n.resolveKind(p.Name, agentOverride)
	pin, err := n.finishAgentLaunch(ctx, p, id, dir, branch, kind, false, id, prompt)
	if err != nil {
		return session.Session{}, err
	}
	return withAgentState(session.Session{
		ID: id, Source: "native", Kind: session.KindPR, Project: p.Name,
		Title: "manual: " + branch, Branch: branch, Repo: p.Repo,
		Worktree: dir, TmuxName: id, Agent: string(kind), ContextKey: id,
		Sandbox: n.SandboxFor(p.Name), GitPin: pin,
	}, state.AgentStarting), nil
}
