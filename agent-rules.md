# Build instructions for `lola`

Implement ONLY what the lola Spec defines. lola spawns and observes its **own**
native agent sessions — one git worktree + one tmux session running Claude Code
per matched Linear issue — and tracks the resulting PR/CI via `gh`. Config is
the single source of truth; the TUI edits it then sends `reload`. Language: Go
(latest stable), Cobra for CLI, Bubble Tea + Lipgloss for TUI. One binary:
`lola run` = daemon, `lola`/`lola tui` = client, other subcommands talk to the
socket.

> **History.** Through P2, lola only *triggered* a separate Agent Orchestrator
> (AO) via `ao spawn` and touched no git/worktrees/PRs/CI. That bridge was
> removed; lola is native-only. Rules that changed are marked
> **[changed from AO bridge]**.

## Environment / launchd (critical)

- Ship as a LaunchAgent (in ~/Library/LaunchAgents), NOT a LaunchDaemon — lola's
  own tmux sessions need your user/GUI context.
- **[changed from AO bridge]** launchd has no login-shell PATH. Resolve
  `tmux`/`git`/`gh`/`claude` via absolute paths or the PATH injected in the
  plist (was: `ao`/`tmux`/`gh`).
- **[changed from AO bridge]** Gate every dispatch on native runtime health:
  `tmux` available, `git` and the **configured coding agent's** binary
  (`claude`|`codex`|`opencode`, resolved per poll; default `claude`) resolvable,
  and the poll's `[[project]]` resolves. If unhealthy: skip tick, record
  lastError, and DO NOT mutate seen, labels, or in-flight. (Was: gate on
  `ao.Reachable(ctx)`.)

## Cycle handling

- If cycle_mode=active, resolve `team.activeCycle.id` at the START of each tick and filter by that UUID. Never cache the active cycle across ticks (handles rollover automatically).

## Query correctness

- Always paginate issues (first:100 + pageInfo until done). Silently missing issues is a bug.
- Populate workflow states, labels (incl. group `parent`), and team members via the cascade. Filter states by ID, not by literal name.
- assignee_mode: me → use [viewer.id](http://viewer.id); user → assignee_user_id; anyone → omit assignee filter.
- match_mode: any → [labels.some.id.in](http://labels.some.id.in)[...]; all → AND of per-label some conditions.
- Use variables, not string interpolation, for all IDs.

## Secrets

- No secrets in config.toml. Read the Linear key from macOS Keychain (`security find-generic-password -s <name> -w`) or an env var. Never log the key.

## Dedup (two explicit modes; do not mix)

- label mode: the flip removing all trigger labels is primary dedup. seen is a short-TTL race guard only. `on_sent_set_label` must NOT be one of `match_labels`, or the issue re-matches right after the flip and respawns forever.
- seen mode: seen is authoritative AND must be pruned — if a seen ID no longer matches the filter, forget it so reopened tickets re-queue. Unbounded seen is a bug.
- Cross-poll: maintain a daemon-global in-flight set keyed by issue UUID; never spawn the same issue from two polls in one cycle. `--dry-run` reports overlaps.

## Dispatch ordering (per issue)

1. Mark in-flight (global set) + write seen FIRST.
2. **[changed from AO bridge]** Spawn the native session with the IDENTIFIER
   (FE-231), not the UUID: `git worktree add` → symlinks + `post_create` →
   write `.lola/{prompt.md,settings.json}` → `tmux new-session` running
   `claude --settings .lola/settings.json`. Upsert the returned session into
   the store immediately so the next budget computation counts it. Bound the
   whole spawn with a deadline (worktree + user `post_create` + tmux). Roll back
   partial spawns best-effort (kill tmux if it came up; remove the worktree only
   when clean). (Was: `ao spawn --project <ao_project> --issue <IDENTIFIER>`.)
3. Only on confirmed success + label mode: re-read current labelIds FRESH (avoid read-modify-write race), compute (current − all match_labels) + set_label, then issueUpdate with the UUID.
4. If label write fails: log, do not re-spawn (seen guards it).

## Coding agent (pluggable)

- The coding agent spawned per issue is configurable: `claude` (default,
  behavior unchanged) | `codex` (OpenAI Codex CLI) | `opencode` (sst/opencode).
  Set globally via `[defaults].agent`, overridable per repo via
  `[[project]].agent`; empty/unknown resolves to `claude`. Resolution
  (`AgentForProject`): the project's `agent` if set, else `[defaults].agent`,
  else `claude` — and it is NEVER written back into config.toml. `internal/agent`
  is a stdlib-only leaf owning the kind enum, per-kind launch argv, and the
  callback-config bodies.
- Full lifecycle-callback parity across all three. claude: generated
  `.lola/settings.json` hooks → `lola hook`. codex: `notify` key in
  `$CODEX_HOME/config.toml` (`CODEX_HOME=<worktree>/.lola/codex`, with a
  best-effort symlink of the user's `~/.codex/auth.json`) →
  `lola hook codex-notify '<json>'`. opencode: an in-process plugin at
  `<worktree>/.opencode/plugins/lola-hook.js` shelling `lola hook <event>` on
  `session.idle`/`permission.asked`/`tool.execute.after`. All three normalize to
  the same event names (`stop` / `notification` / `session_end` / `tool_use` /
  `user_prompt`), so dispatch/observer/reaction logic stays agent-agnostic.
- codex/opencode run UNATTENDED like the claude session (`codex
  --ask-for-approval never --sandbox workspace-write`; `opencode --auto`).
  Callback artifacts stay under git-excluded dirs: `.lola/` (claude, codex) and
  `.opencode/` (opencode). `LOLA_SESSION` in the pane attributes every event.
- Pane classification (`internal/attention`) is agent-aware — a shared cue set
  plus per-kind cues — so screen-scraping backstops the callbacks for every
  agent; `k == Claude` behavior stays byte-identical to today.
- Provider auth is inherited from the daemon/pane env (`ANTHROPIC_API_KEY` /
  `OPENAI_API_KEY`) or an existing CLI login (`codex login`, `opencode auth`);
  never stored in config.toml.
- `[brain]`, `[review]`, and `[coderabbit]` are lola-INTERNAL helpers that
  always shell `claude -p` regardless of the coding-agent choice — they are NOT
  the pluggable coding agent and must not follow the `agent` setting.

## Caps

- **[changed from AO bridge]** budget = min(poll.concurrency_cap, global_cap −
  liveCounted). liveCounted counts ONLY **native** sessions in the store whose
  derived status occupies a slot: `working`, `needs_input`, `draft`,
  `ci_failed`, `changes_requested`, `ci_pending`. Parked-for-review (approved,
  review_pending, no_pr) and terminal (merged, dead, session_ended) don't
  count, so held PRs don't stall pickup. (Was: AO sessions in
  `ao.counting_states`.)
- **[changed from AO bridge]** liveCounted MUST come from the native session
  store snapshot, never a local counter or `ao session ls --json`.
- When capped, sort by priority_sort (priority then createdAt) for deterministic selection.

## Reconciliation

- **[changed from AO bridge]** Periodic pass (~5 min): issues labeled set_label
  with no counted **native** session (no live pane for that identifier) and no
  open PR after orphan_timeout (default 15m) → revert label and clear seen +
  in-flight so it re-queues. Prefer the session record's branch/repo for the
  open-PR check (`gh pr list --repo <repo> --head <branch>`), falling back to
  the poll's repo or its project's; a check that cannot answer fails CLOSED
  (no revert). Keep the dead session's worktree for inspection. (Was: no counted
  AO session; may set agent-blocked.)

## Safety / robustness

- config.toml and lola.sock are mode 0600. Config writes are temp-file + rename (atomic).
- Respect min 30s poll interval; exponential backoff on 429/5xx.
- **[changed from AO bridge]** Validate on save/enable: the poll's `project`
  references a defined `[[project]]`; labels/states/cycle/user IDs resolve; caps
  > 0; pinned cycle has cycle_id; label mode has a set label and non-empty
  match_labels (and set ∉ match_labels). Path-exists / is-git-repo checks live in the runtime layer, not
  config load. (Was: `ao_project` exists in agent-orchestrator.yaml.)
- **[changed from AO bridge]** Surface "runtime unavailable" (missing
  tmux/git/claude or unknown project) and "Linear auth failed" in `status`;
  never fail silently (was: "AO not running").
- **[changed from AO bridge]** The observer tracks PR/CI via `gh` (scm.PRForBranch
  → DeriveStatus). Acting on that state — CI-fix, review-comment, escalation,
  Slack/desktop notifications — is P3 (future work), not shipped. (Was: Slack
  fires only the lola-owned "picked up" event; AO owns PR/CI notifications.)

## Daemon

- One goroutine per enabled poll with its own ticker; `reload` re-diffs config and starts/stops goroutines without dropping unaffected ones.
- A read-only observer loop (~30s) and a reconcile loop (~5m) run alongside; both are panic-guarded and shielded from the shutdown cancel, with per-exec deadlines so a wedged `gh`/`tmux` can't hang graceful shutdown.
- On startup, adopt surviving sessions: scan tmux + worktree dirs, re-adopt live ones, flag zombies (worktree without pane, pane without worktree) — Adopt only reports, the daemon decides.
- Unix socket at ~/.lola/lola.sock, newline-delimited JSON per the protocol; it also serves the hidden `hookEvent` from `lola hook <event>`.
- **[changed from AO bridge]** `status` reports runtimeOk (are tmux/git/claude
  resolvable NOW?) and linearOk (last auth ok?) (was: aoRunning).
- Graceful shutdown on SIGTERM/`stop`: finish in-flight tick, close socket, exit 0.

## TUI cascading form

- Fetch each level only after the prior selection: team → projects → cycle info → states → labels → members.
- **[changed from AO bridge]** Populate the `project` dropdown from the
  `[[project]]` entries in config.toml. Refuse to save/enable a poll whose
  `project` is empty or names no defined `[[project]]`. (Was: populate
  `ao_project` from agent-orchestrator.yaml.)
- Cache Linear metadata per team in cache/linear-.json; provide a manual "refresh" key.
- **[changed from AO bridge]** Show validation errors inline (unresolved
  label/state/user ID, undefined project reference, cap ≤ 0).
- Second tab: session list (status, issue, PR, checks, age) with a live
  capture-pane preview and `enter` to attach.

## Testing (definition of done)

- Use the linear.API interface + fake.go fixtures. Unit tests MUST cover: filter construction per mode, pagination, budget math, both dedup modes incl. seen pruning, cross-poll dedup, labelIds delta computation, identifier-vs-UUID usage.
- **[changed from AO bridge]** Cover the native runtime: spawn (worktree +
  prepare + hooks + tmux, with rollback), adopt (re-adopt / dead / orphaned
  classification), the store-driven liveCounted, and the reconcile orphan
  revert (fail-closed PR check).
- **[coding agent]** Unit-test the `internal/agent` leaf (Valid/Parse/Binary/
  LaunchArgs, the codex `config.toml` + opencode plugin bodies, ParseCodexNotify
  event mapping), config resolution (`AgentForProject`), and the agent-aware
  attention cues (claude byte-identical, plus focused codex/opencode cue tests).
  codex/opencode **end-to-end** run-verification requires those binaries
  installed and is NOT exercised by the Go test suite.
- **[changed from AO bridge]** Integration: `lola poll <n> --once --dry-run`
  prints correct matches against a real team; creating a poll via the cascade
  writes valid config.toml; the launchd instance survives sleep/wake and a
  manual `kill` (KeepAlive restarts it); enabling a poll whose `project` is
  undefined is rejected with a clear message. (Was: a bad `ao_project` is
  rejected.)
