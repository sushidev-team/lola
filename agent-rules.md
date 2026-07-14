# Build instructions for `lola`

Implement ONLY what the lola Spec v2 defines. `lola` triggers AO; it must never touch git, worktrees, PRs, or CI. Config is the single source of truth; the TUI edits it then sends `reload`. Language: Go (latest stable), Cobra for CLI, Bubble Tea + Lipgloss for TUI. One binary: `lola run` = daemon, `lola`/`lola tui` = client, other subcommands talk to the socket.

## Environment / launchd (critical)

- Ship as a LaunchAgent (in ~/Library/LaunchAgents), NOT a LaunchDaemon — AO's tmux runtime needs your user/GUI context.
- launchd has no login-shell PATH. Resolve `ao`/`tmux`/`gh` via absolute paths or the PATH injected in the plist. `[ao].bin` is absolute.
- Gate every dispatch on `ao.Reachable(ctx)`. If AO is down: skip tick, record lastError, and DO NOT mutate seen or labels.

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

- label mode: the flipped trigger label is primary dedup. seen is a short-TTL race guard only.
- seen mode: seen is authoritative AND must be pruned — if a seen ID no longer matches the filter, forget it so reopened tickets re-queue. Unbounded seen is a bug.
- Cross-poll: maintain a daemon-global in-flight set keyed by issue UUID; never spawn the same issue from two polls in one cycle. `--dry-run` reports overlaps.

## Dispatch ordering (per issue, not batched unless AO returns per-ID results)

1. Mark in-flight (global set) + write seen FIRST.
2. Spawn with the IDENTIFIER (FE-231), not the UUID.
3. Only on confirmed success + label mode: re-read current labelIds FRESH (avoid read-modify-write race), compute (current − remove_label) + set_label, then issueUpdate with the UUID.
4. If label write fails: log, do not re-spawn (seen guards it).

## Caps

- budget = min(poll.concurrency_cap, global_cap − liveCounted). liveCounted counts ONLY AO sessions whose state ∈ ao.counting_states (excludes review/blocked so held PRs don't stall pickup).
- liveCounted MUST come from `ao session ls --json`, never a local counter.
- When capped, sort by priority_sort (priority then createdAt) for deterministic selection.

## Reconciliation

- Periodic pass (~5 min): issues labeled set_label with no counted AO session and no open PR after orphan_timeout (default 15m) → revert label (or set agent-blocked) and clear seen so it re-queues.

## Safety / robustness

- config.toml and lola.sock are mode 0600. Config writes are temp-file + rename (atomic).
- Respect min 30s poll interval; exponential backoff on 429/5xx.
- Validate on save/enable: ao_project exists in agent-orchestrator.yaml; labels/states/cycle/user IDs resolve; caps &gt; 0; pinned cycle has cycle_id; label mode has set/remove labels.
- Surface "AO not running" and "Linear auth failed" in `status`; never fail silently.
- Slack (if enabled) fires only the lola-owned "picked up" event; AO owns PR/CI notifications.

## Daemon

- One goroutine per enabled poll with its own ticker; `reload` re-diffs config and starts/stops goroutines without dropping unaffected ones.
- Unix socket at ~/.lola/lola.sock, newline-delimited JSON per the protocol.
- `status` reports aoRunning (can we exec `ao`?) and linearOk (last auth ok?).
- Graceful shutdown on SIGTERM/`stop`: finish in-flight tick, close socket, exit 0.

## TUI cascading form

- Fetch each level only after the prior selection: team → projects → cycle info → states → labels → members.
- Populate the ao_project dropdown from agent-orchestrator.yaml ([ao].config_path). Refuse to save/enable a poll whose ao_project is not found.
- Cache Linear metadata per team in cache/linear-.json; provide a manual "refresh" key.
- Show validation errors inline (unresolved label/state/user ID, missing ao_project, cap ≤ 0).

## Testing (definition of done)

- Use the linear.API interface + fake.go fixtures. Unit tests MUST cover: filter construction per mode, pagination, budget math, both dedup modes incl. seen pruning, cross-poll dedup, labelIds delta computation, identifier-vs-UUID usage.
- Integration: `lola poll <n> --once --dry-run` prints correct matches (incl. assignee=me) against a real team; creating a poll via the cascade writes valid config.toml; the launchd instance survives sleep/wake and a manual `kill` (KeepAlive restarts it); enabling a poll with a bad ao_project is rejected with a clear message.
