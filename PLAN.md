# Lola — Implementation Plan

**Lola** (command: `lola`) — agent runner & orchestrator, homage to *Lola rennt*:
run, observe the outcome, and if it isn't good enough, run again.

Lola grows out of the existing `aop` codebase (Linear poller, daemon, socket
protocol, TUI, config discipline, test suite) into a full replacement for
Agent Orchestrator: **deterministic Go core** for lifecycle (spawn, observe,
react, cleanup), tmux as the session runtime, Claude Code as the only
harness, GitHub as the only SCM, Linear as a first-class tracker with
write-back. An LLM "brain" is layered on top only where judgment is needed —
never in the control loop.

Strangler pattern: every tier ships something independently useful, and the
existing aop→AO pipeline keeps working until the native runtime replaces it.

---

## P0 — Rename + close the gaps in the running pipeline (now)

Get one real ticket flowing end-to-end while the rest is built. Everything
here is small and pays off immediately regardless of later tiers.

1. **Rename aop → lola.** Binary/root command `lola`, module path, runtime
   dir `~/.lola/`, socket `lola.sock`, `LOLA_HOME` env override, launchd
   label `com.user.lola`, docs. Repo rename (`ao-puller` → `lola`) on GitHub.
   *(S)*
2. **Spawn with context: `--prompt`.** Pass Linear issue title + "fetch full
   details via linearis" instruction alongside `--issue` so agents don't
   start blind (current AO build resolves GitHub issues only). Falls away
   once the native runtime builds prompts itself (P2.12), but needed for the
   AO bridge. *(S)*
3. **Per-poll `repo` (owner/name) in config** → reconcile runs
   `gh pr list --repo <repo> --head <branch>`. Without it the PR check fails
   closed under launchd and orphan reverts never fire. *(S)*
4. **Go live:** Linear key in Keychain, trigger/sent labels in Linear, first
   poll via `lola tui`, launchd plist installed, one real dispatch observed.
   Operational learning feeds every later tier. *(S, mostly ops)*

## P1 — Sessions: read-only observability (strangler v1)

See everything before controlling anything. Zero risk to the running
pipeline; instant TUI value even while AO still spawns.

5. **Session model + store.** `internal/session`: ID, project, issue
   identifier, branch, worktree path, tmux target, status, timestamps.
   JSON state files under `~/.lola/state/` (sqlite only if query needs
   grow later). *(S)*
6. **tmux adapter.** `internal/tmux`: list sessions, `capture-pane -e`
   (rendered screen), `send-keys`, attach target helpers, exists/alive
   checks. Sessions survive lola restarts by design — tmux server owns
   them. *(S/M)*
7. **PR/CI observer.** `internal/scm`: poll `gh pr view --json
   state,reviews,statusCheckRollup,mergeable` per session branch; derive
   pr_open / ci_failed / changes_requested / approved / mergeable / merged.
   Deterministic status derivation = the single source for caps, reactions,
   reconcile. *(M)*
8. **TUI sessions view.** Second tab: session list (status, issue, PR,
   checks, age), live preview pane via capture-pane polling, `enter` =
   attach (`tea.ExecProcess` suspend/resume), kill with confirm. *(M)*

## P2 — Native runtime: Lola spawns her own runners (replaces AO spawn)

9. **Worktree manager.** `git worktree add` per session under
   `~/.lola/worktrees/<project>/<session>/`, branch `lola/<issue>-<n>`,
   park-on-PR, cleanup-on-merge policy, orphan sweep integration. *(M)*
10. **Project registry in config.toml.** `[[project]]`: name, path, repo,
    default_branch, post_create commands, symlinks (`.env` etc.), env
    forwarding. Validation on save/enable (path exists, is git repo).
    Replaces `[ao]` + AOProjects. *(S/M)*
11. **postCreate + symlinks executor.** Run in worktree before agent start;
    failure blocks the session with a clear status, never a half-started
    agent. *(S)*
12. **Claude Code launcher.** Spawn `claude` in a fresh tmux session inside
    the worktree; prompt assembled from the full Linear issue (title,
    description, comments — we have the API); inject session env + hooks
    config. *(M)*
13. **State detection via hooks.** Claude Code Stop/Notification hooks POST
    to `~/.lola/lola.sock` → working / idle / needs_input / done. Fallback
    liveness: tmux pane alive + last-output age (the `no_signal`
    equivalent). Never scrape screen content for state. *(M — the hard one;
    budget iteration)*
14. **Runtime switch per poll:** `runtime = "ao" | "native"`. Dispatch flow,
    caps and dedup stay identical; only the spawn/count backend changes.
    AO bridge (`internal/ao`) survives until native is trusted, then dies.
    *(S)*
15. **Session adoption on restart.** Daemon scans tmux + state files on
    start, re-adopts live sessions, reconciles zombies (state file without
    tmux session and vice versa). *(M)*

## P3 — Reaction engine: Lola runs again (replaces AO reactions)

The movie part. All reactions configurable per project: `auto`, `retries`,
`escalate_after`, `message` template.

16. **ci-failed → send-to-agent.** Fetch failing check logs (`gh run view
    --log-failed`), `tmux send-keys` into the session with a recovery
    prompt; N retries then escalate. *(M)*
17. **changes-requested → send-to-agent.** Review comments (incl. inline)
    formatted into the session; re-request review on push. *(M)*
18. **merge-conflict → send-to-agent** (rebase instruction), detect via
    observer `mergeable`. *(S)*
19. **approved-and-green → notify + park.** Worktree stays for human review;
    never auto-merge. **merged → cleanup**: remove worktree + branch,
    archive session record, free the slot. *(S/M)*
20. **Notifier.** Desktop (osascript/terminal-notifier) + Slack webhook;
    priority routing (urgent/action/info); needs_input and escalations are
    urgent. *(S/M)*

## P4 — Linear write-back: the differentiator AO doesn't have

21. **Lifecycle comments + state transitions.** Configurable mapping per
    poll: spawn → "In Progress" + comment session link; PR open → "In
    Review" + PR link comment; merged → "Done". *(M)*
22. **Escalation to Linear.** Stuck/blocked session → `agent-blocked` label
    + comment with reason; reconcile reverts integrate with it. *(S)*
23. **State-based dedup mode.** With transitions owned by Lola, workflow
    state replaces label-flip dedup (cleaner than labels; keep label mode
    for teams that want it). *(M)*

## P5 — Orchestrator brain: LLM only where judgment lives

Headless `claude -p` calls from the daemon; deterministic core untouched.
Each is optional and independently switchable.

24. **Ticket triage/decomposition.** Multi-repo ticket → repo-scoped
    sub-issues (SPEC "approach A"), priority suggestion, done as Linear
    write-back with human confirm in TUI. *(M/L)*
25. **Escalation summarizer.** Stuck session transcript → 5-line summary in
    the notification/Linear comment instead of "agent stuck". *(S/M)*
26. **Retry-vs-escalate judgment** on ambiguous CI failures (flaky test vs
    real break) before burning retries. *(M)*

## P6 — Ops polish

27. **`lola doctor`.** Checks: tmux/gh/claude on PATH + versions, keychain
    key readable, launchd loaded, socket healthy, project paths valid. *(S)*
28. **CI + release.** GitHub Actions (build/vet/test + `-race`), goreleaser,
    versioned releases; maybe brew tap. *(S/M)*
29. **TUI polish.** Session history view, log viewer with follow, metrics
    (spawns/day, time-to-PR, retry rates), theming. *(ongoing)*

---

## Decisions locked in (from prior analysis)

- **tmux, not zmx/own-PTY:** need send-keys + rendered capture-pane +
  attach + survive-daemon-restart; tmux ships all four. LaunchAgent (user
  GUI context) already required.
- **Deterministic orchestrator, LLM on top:** the control loop never asks a
  model what to do; agents are consulted for triage/summaries/judgment only.
- **Claude-Code-only, GitHub-only, Linear-first:** cuts AO's harness/plugin/
  dashboard surface entirely; that's what makes this feasible solo.
- **State detection via hooks, not screen-scraping.**
- **Never auto-merge.** approved+green parks the worktree and notifies.

## Effort ballpark

P0 ≈ a day incl. going live. P1+P2 ≈ the original aop build ×2 (workflow-
assisted: days, not weeks). P3 ≈ another aop. P4 small, P5/P6 incremental.
The long tail lives in 13/15/16 (state detection, adoption, CI feedback) —
ship them behind the runtime flag and iterate while AO still carries
production.
