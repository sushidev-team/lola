# lola — Linear-triggered native agent orchestrator

Single Go binary that watches Linear for issues matching a filter (team →
project → active cycle → workflow state → labels → assignee) and, for each
match, spawns its **own** coding-agent session: a per-issue git worktree, a
tmux session, and Claude Code running inside it. Launchd-managed daemon
(`lola run`) + TUI (`lola`). lola owns the whole run — worktree, agent, and
observation of the resulting PR/CI — not just the trigger.

> **Design note.** This spec was originally written for an AO-bridge design in
> which `lola` only triggered a separate Agent Orchestrator (AO) via `ao spawn`
> and never touched git/worktrees/PRs/CI. That bridge (the `[ao]` table, the
> per-poll `runtime`/`ao_project` keys, `internal/ao`) was removed; lola is
> **native-only**. Sections below are updated to the native runtime, and the
> places where the design changed are marked **[changed from AO bridge]**.

## Architecture

- One binary, two roles: `lola run` = daemon; `lola` / `lola tui` = TUI client.
- Config is source of truth (`~/.lola/config.toml`); TUI only edits it, then
  signals reload.
- launchd owns liveness (must be a LaunchAgent for the tmux/GUI context lola's
  own sessions run in); daemon owns scheduling + live pause/resume.
- Daemon ↔ TUI over unix socket `~/.lola/lola.sock` (newline-delimited JSON).

## Filters (per poll)

- Team (required)
- Project (optional, Linear project)
- Cycle: none | active | pinned
- Workflow state(s) (by ID)
- Labels (trigger tags) with `match_mode = any | all`
- Assignee: anyone | me (Linear `viewer`) | specific user

## Runtime layout (`~/.lola/`)

- `config.toml` (0600, no secrets)
- `lola.sock` (0600)
- `daemon.log`
- `state/<poll>.seen`
- `state/sessions.json` (native session store)
- `worktrees/<project>/<session>/` (per-session git worktrees)
- `cache/linear-<team>.json`

## Commands

- `lola` / `lola tui` — open TUI (list + create/edit/delete/pause; sessions tab)
- `lola run` — start daemon (launchd calls this)
- `lola stop` — graceful shutdown
- `lola status` — table: poll, enabled, last run, last spawn, running, error
- `lola enable/disable <poll>` — live pause/resume
- `lola poll <poll> --once [--dry-run]` — one tick now; dry-run prints matches, no side effects
- `lola reload` — re-read config
- `lola logs [poll]` — tail log
- `lola hook <event>` — hidden internal callback; Claude Code lifecycle hooks invoke it, it posts to the socket. Never run by hand.

## Dispatch flow (per tick, per enabled poll)

1. **[changed from AO bridge]** Precheck the **native runtime** is healthy:
   `tmux` available, `git` and `claude` resolvable (LookPath only, nothing
   exec'd), and the poll's `[[project]]` resolves. If unhealthy: skip, set
   lastError, do NOT mutate seen/labels/in-flight. (Was: precheck AO reachable
   via `ao session ls --json`.)
2. Resolve API key (Keychain > env).
3. If cycle_mode=active: resolve `team.activeCycle.id` NOW (never cache across ticks).
4. Run issues query, paginated (first:100 + pageInfo until done), filter built dynamically.
5. Cross-poll dedup: drop IDs in daemon-global in-flight set.
6. Mode dedup — label: trigger label already flipped away; seen = short-TTL race guard. seen: drop seen IDs, prune seen entries that no longer match (lets reopened tickets re-queue).
7. Sort by priority_sort (deterministic when capped).
8. **[changed from AO bridge]** budget = min(poll.concurrency_cap, global_cap − liveCounted); liveCounted = **native** sessions in the store whose derived status occupies a slot (`working`, `needs_input`, `draft`, `ci_failed`, `changes_requested`, `ci_pending`). Parked-for-review states (approved, review_pending, no_pr) and terminal ones (merged, dead, session_ended) don't count, so held PRs never stall pickup. (Was: AO sessions from `ao session ls --json` in `counting_states`.)
9. **[changed from AO bridge]** Per issue (up to budget): (a) mark in-flight + write seen FIRST; (b) spawn the native session — `git worktree add` under `~/.lola/worktrees/<project>/<sessionID>` on branch `issue.branchName` (fallback `lola/<identifier>`), run project symlinks + `post_create`, write `.lola/{prompt.md,settings.json}`, then `tmux new-session` running `claude --settings .lola/settings.json` (identifier FE-231, not UUID; the prompt points the agent at `.lola/prompt.md`, which carries identifier + title + a fetch-full-issue-from-Linear instruction so agents don't start blind); the returned session is upserted into the store immediately so the next budget computation counts it; (c) on success + label mode: re-read current labelIds FRESH, new = (current − all match_labels) + set_label, issueUpdate with UUID. (Was: `ao spawn --project <ao_project> --issue <IDENTIFIER> --prompt <context>`.)
10. Log matched/spawned/capped-out/errors; update status.

Whole-spawn deadline: worktree + `post_create` + tmux are bounded by
`nativeSpawnTimeout` (10 min) — user-supplied `post_create` runs in here and
the shielded tick context alone could never abort it.

Identifier vs UUID: the session name / branch and the agent prompt use the
identifier (`FE-231`); `issueUpdate` uses the UUID (`id`). Query fetches both.

## Native runtime **[new section — replaces the AO bridge]**

The runner lives in these packages, composed by `internal/runtime`:

- `internal/worktree` — `git worktree add`/`remove` per session, branch
  creation, symlink + `post_create` preparation, `List`/`BranchExists`.
  Destructive-op discipline: `Remove` is force=false, so a dirty worktree
  refuses (`ErrDirty`) and is kept for inspection; the main checkout is guarded.
- `internal/tmux` — session control (`new-session`, `has`, `list`,
  `kill-session`, `capture-pane`, `send-keys`, availability probe).
- `internal/hook` — generates the per-session Claude Code `settings.json`
  wiring the Stop / Notification / SessionEnd / PostToolUse hooks to
  `lola hook <event>`, and the `Post` client that ships an event over the
  socket. `LOLA_SESSION` is exported in the pane so the hook can attribute it.
- `internal/scm` — PR/CI observation: `gh pr list --repo <repo> --head
  <branch> --json …` → `DeriveStatus` yields `working / no_pr / draft /
  ci_pending / ci_failed / merge_conflict / changes_requested / approved /
  review_pending / merged / closed`.
- `internal/session` — the session model + JSON store (`state/sessions.json`):
  ID, source (`native`), project, issue identifier + UUID, branch, worktree
  path, tmux target, derived status, PR facts, timestamps.
- `internal/runtime` — `Spawn` (worktree → prepare → hooks → tmux → claude,
  with best-effort rollback of partial spawns), `Adopt` (restart-recovery scan
  pairing live `lola-*` tmux sessions with worktree dirs), `Kill`, `Alive`.

Session naming: `lola-<project>-<identifier-lowercased>` is both the tmux
session name and the worktree dir basename; a re-queued issue whose prior
worktree/branch is kept for inspection gets a `-r<n>` suffix.

Observer loop (~30s, read-only): merges each native session in the store with
fresh tmux liveness (`runtime.Alive`) and PR state (scm), writing the derived
status back via an atomic read-modify-write so a concurrently-arriving hook
event is never clobbered. A dead pane whose PR is not merged → `dead`. P2 never
auto-kills; reacting to CI/review state is P3 (future work).

## Reconciliation pass (periodic, ~5 min)

**[changed from AO bridge]** Issues still carrying set_label with **no counted
native session** (no live pane for that identifier) and no open PR after
orphan_timeout (default 15m) → revert labels (remove set_label, restore all
match_labels) and clear seen + in-flight so they re-queue. The open-PR check runs `gh pr list --repo <repo>
--head <branch>`, preferring the session record's branch/repo and falling back
to the poll's `repo` (or its project's, via `PollRepo`); a check that cannot
answer fails CLOSED (no revert). The dead session's worktree is kept for
inspection, never removed by reconcile. (Was: no counted AO session.)

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

- **[changed from AO bridge]** Inject PATH (include /opt/homebrew/bin), HOME,
  WorkingDirectory. `tmux`/`git`/`gh`/`claude` must be on PATH or absolute (was:
  `ao`/`tmux`/`gh`). lola's own tmux sessions need the user/GUI context.
- RunAtLoad + KeepAlive true. Logs to ~/.lola/daemon.log.

## Config key points

- **[changed from AO bridge]** No `[ao]` table, no per-poll `runtime` /
  `ao_project`. `[[project]]` (one per repo) is the runtime registry: name,
  path (absolute), repo (owner/name), default_branch, post_create, symlinks,
  env. `[defaults]`: poll_interval, concurrency_cap, global_cap.
- Per poll: team_id, project_id, cycle_mode (+cycle_id), state_ids,
  match_labels + match_mode, assignee_mode + assignee_user_id, **project (a
  `[[project]]` name — required)**, repo (optional; falls back to the project's
  repo for PR checks), concurrency_cap, priority_sort, dedup_mode (label|seen),
  on_sent_set_label. (History: a per-poll on_sent_remove_label existed in the
  AO-bridge era; it was retired — Lola now removes all match_labels on the flip.)

## Go module layout

**[changed from AO bridge]** main.go; internal/config (config, validate, atomic
writes via temp+rename); internal/linear (client, iface, queries, mutations,
types, fake); internal/runtime (native spawn/adopt/kill/alive);
internal/worktree (worktree lifecycle); internal/tmux (session control);
internal/hook (Claude Code hook settings + socket callback); internal/scm
(gh-based PR/CI observation); internal/session (session model + JSON store);
internal/daemon (daemon, dispatch, observer, reconcile, status, server, state,
inflight); internal/protocol (socket request/response types); internal/tui
(app, list, form, sessions, client); internal/secrets (keychain_darwin, env).
(Was: internal/ao — removed.)

## Build rules / gotchas

- Labels & cycles are team-scoped; handle label groups (parent).
- No issueAddLabel — always read-modify-write full labelIds fresh before mutation.
- Rate limit: min 30s poll; exponential backoff on 429/5xx.
- **[changed from AO bridge]** Cap counting comes from the native session store
  (slot-occupying statuses), never a local counter (was: query AO live).
- seen is a safety belt (label mode) or authoritative+pruned (seen mode) — never unbounded.
- **[changed from AO bridge]** Surface "runtime unavailable" (missing
  tmux/git/claude or unknown project) / "Linear auth failed" in status; never
  fail silently (was: "AO not running").
- **[changed from AO bridge]** Validate on save/enable: project references a
  defined `[[project]]`; IDs resolve; caps > 0; pinned cycle has cycle_id;
  label mode has a set label and non-empty match_labels (and set ∉ match_labels). Path-exists /
  is-git-repo checks live in the runtime layer, not config load. (Was: validate
  `ao_project` exists in agent-orchestrator.yaml.)
- config.toml + lola.sock mode 0600; never log API key.
- **[changed from AO bridge]** Slack (optional, future) fires the lola-owned
  "picked up" event; PR/CI observation is lola's own via `gh` — the reaction
  engine that notifies/acts on it is P3.
- Testing seam: linear.API interface + fake.go fixtures; unit test filter construction, pagination, budget math, both dedup modes incl. pruning, cross-poll dedup, labelIds delta, identifier-vs-UUID, and the native spawn/adopt/reconcile paths.

## Context

- **[changed from AO bridge]** lola now owns the worktree/agent/PR-observation
  loop natively (tmux + Claude Code + `gh`), so there is no external
  orchestrator instance. A multi-repo Linear ticket still has no native
  multi-repo project → decompose into repo-scoped issues, one `[[project]]`
  each (approach A). The historical AO context: AO = one instance/dashboard,
  many projects, one repo per project; lola supplied only the Linear trigger.
- **[changed from AO bridge]** lola natively handles worktree create + park +
  cleanup and observes PR/CI; the CI-fix / review-comment / escalation reaction
  loops are the P3 roadmap, not shipped. (Was: AO handled all of these; lola
  only triggered.)
- Subscription note: for unattended runs prefer ANTHROPIC_API_KEY; Max/OAuth outside first-party Claude Code is a ToS gray zone.
