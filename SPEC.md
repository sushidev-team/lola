# lola — Linear → Agent Orchestrator poller

Single Go binary that watches Linear for issues matching a filter (team → project → active cycle → workflow state → labels → assignee) and dispatches them into a running Agent Orchestrator (AO) instance via `ao spawn`. Launchd-managed daemon (`lola run`) + TUI (`lola`). `lola` ONLY triggers — it never touches git, worktrees, PRs, or CI (that's AO's job).

## Architecture

- One binary, two roles: `lola run` = daemon; `lola` / `lola tui` = TUI client.
- Config is source of truth (`~/.lola/config.toml`); TUI only edits it, then signals reload.
- launchd owns liveness (must be a LaunchAgent for tmux/GUI context); daemon owns scheduling + live pause/resume.
- Daemon ↔ TUI over unix socket `~/.lola/lola.sock` (newline-delimited JSON).

## Filters (per poll)

- Team (required)
- Project (optional)
- Cycle: none | active | pinned
- Workflow state(s) (by ID)
- Labels (trigger tags) with `match_mode = any | all`
- Assignee: anyone | me (Linear `viewer`) | specific user

## Runtime layout (`~/.lola/`)

- `config.toml` (0600, no secrets)
- `lola.sock` (0600)
- `daemon.log`
- `state/<poll>.seen`
- `cache/linear-<team>.json`

## Commands

- `lola` / `lola tui` — open TUI (list + create/edit/delete/pause)
- `lola run` — start daemon (launchd calls this)
- `lola stop` — graceful shutdown
- `lola status` — table: poll, enabled, last run, last spawn, running, error
- `lola enable/disable <poll>` — live pause/resume
- `lola poll <poll> --once [--dry-run]` — one tick now; dry-run prints matches, no side effects
- `lola reload` — re-read config
- `lola logs [poll]` — tail log

## Dispatch flow (per tick, per enabled poll)

1. Precheck AO reachable (`ao session ls --json`). If down: skip, set lastError, do NOT mutate seen/labels.
2. Resolve API key (Keychain &gt; env).
3. If cycle_mode=active: resolve `team.activeCycle.id` NOW (never cache across ticks).
4. Run issues query, paginated (first:100 + pageInfo until done), filter built dynamically.
5. Cross-poll dedup: drop IDs in daemon-global in-flight set.
6. Mode dedup — label: trigger label already flipped away; seen = short-TTL race guard. seen: drop seen IDs, prune seen entries that no longer match (lets reopened tickets re-queue).
7. Sort by priority_sort (deterministic when capped).
8. budget = min(poll.concurrency_cap, global_cap − liveCounted); liveCounted = AO sessions whose state ∈ counting_states (excludes review/blocked so held PRs don't stall pickup).
9. Per issue (not batched unless AO returns per-ID results): (a) mark in-flight + write seen FIRST; (b) `ao spawn --project <ao_project> --issue <IDENTIFIER> --prompt <context>` (FE-231, not UUID; current AO rejects positional args; the prompt carries identifier + title + a fetch-full-issue-from-Linear instruction so agents don't start blind); (c) on success + label mode: re-read current labelIds FRESH, new = (current − remove_label) + set_label, issueUpdate with UUID; (d) optional Slack "picked up".
10. Log matched/spawned/capped-out/errors; update status.

Identifier vs UUID: `ao spawn` uses identifier (`FE-231`); `issueUpdate` uses UUID (`id`). Query fetches both.

## Reconciliation pass (periodic, ~5 min)

Issues labeled set_label with no counted AO session and no open PR after orphan_timeout (default 15m) → revert to trigger label (or raise agent-blocked) and clear seen so it re-queues.

## GraphQL cascade (TUI edit form)

- viewer (for assignee=me): `{ viewer { id name email } }`
- ① teams: `{ teams { nodes { id key name } } }`
- ② projects: `team(id:$t){ projects{ nodes{ id name state } } }`
- ③ cycles: `team(id:$t){ activeCycle{ id number name endsAt } cycles(first:20){ nodes{ id number name } } }`
- ④ states: `team(id:$t){ states{ nodes{ id name type position } } }`
- ⑤ labels (handle groups via parent): `team(id:$t){ labels{ nodes{ id name color parent{ id name } } } }`
- ⑥ members (assignee=user): `team(id:$t){ members{ nodes{ id name email active } } }`

Per-tick issues query (paginated, conditional filter parts): team.id.eq; project.id.eq (if set); cycle.id.eq (if cycle_mode != none); [state.id.in](http://state.id.in) (from state_ids); [labels.some.id.in](http://labels.some.id.in) (match_mode=any) or AND of some-conditions (all); assignee.id.eq ([me→viewer.id](http://xn--meviewer-bh6d.id), user→assignee_user_id, anyone→omit). Node fields: id identifier title branchName priority createdAt labels{nodes{id}}; pageInfo{hasNextPage endCursor}.

Label transition (no add-label mutation; re-read first, send full array): `issueUpdate(id:$id, input:{labelIds:$labelIds}){ success }`.

## launchd (LaunchAgent, not Daemon)

- Inject PATH (include /opt/homebrew/bin), HOME, WorkingDirectory. `ao`/`tmux`/`gh` must be on PATH or absolute.
- RunAtLoad + KeepAlive true. Logs to ~/.lola/daemon.log.

## Config key points

- [ao]: bin (absolute), config_path, counting_states = ["working","no_signal","needs_input","draft","ci_failed","changes_requested"] (AO's real slot-occupying statuses; parked-for-review pr_open/review_pending/approved/mergeable and dead merged/idle/terminated stay uncounted).
- Per poll: cycle_mode, state_ids, match_labels + match_mode, assignee_mode + assignee_user_id, ao_project (must exist in agent-orchestrator.yaml), concurrency_cap, priority_sort, dedup_mode (label|seen), on_sent.set_label / remove_label.

## Go module layout

main.go; internal/config (config, validate, atomic writes via temp+rename); internal/linear (client, iface, queries, mutations, types, fake); internal/ao (client: spawn, session ls --json, Reachable); internal/daemon (daemon, dispatch, reconcile, server, state, inflight); internal/tui (app, list, form, client); internal/secrets (keychain_darwin, env).

## Build rules / gotchas

- Labels & cycles are team-scoped; handle label groups (parent).
- No issueAddLabel — always read-modify-write full labelIds fresh before mutation.
- Rate limit: min 30s poll; exponential backoff on 429/5xx.
- Cap counting must query AO live, never local counter.
- seen is a safety belt (label mode) or authoritative+pruned (seen mode) — never unbounded.
- Surface "AO not running" / "Linear auth failed" in status; never fail silently.
- Validate on save/enable: ao_project exists, IDs resolve, caps &gt; 0, pinned cycle has cycle_id, label mode has set/remove labels.
- config.toml + lola.sock mode 0600; never log API key.
- Slack (optional) fires only lola-owned "picked up" event; AO owns PR/CI notifications.
- Testing seam: linear.API interface + fake.go fixtures; unit test filter construction, pagination, budget math, both dedup modes incl. pruning, cross-poll dedup, labelIds delta, identifier-vs-UUID.

## Context

- AO = one instance/dashboard, many projects; each project = one repo. No native multi-repo project → decompose into repo-scoped Linear issues (approach A).
- AO natively handles worktrees, PRs, CI-fix loop, review-comment loop, escalation, hold-for-review, delete-on-merge. Only missing piece = the Linear trigger, which is lola.
- Subscription note: for unattended runs prefer ANTHROPIC_API_KEY; Max/OAuth outside first-party Claude Code is a ToS gray zone.
