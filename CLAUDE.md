# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What lola is

`lola` is a single Go binary that watches Linear for issues matching a filter
(team → project → cycle → workflow state → labels → assignee) and spawns its
**own** coding-agent session for each match: a git worktree, a tmux session, and
Claude Code running inside it. It then observes the resulting PR/CI via `gh` and
can react (re-prompt the agent, notify, clean up).

The coding agent is **pluggable** — `claude` (default) | `codex` | `opencode`,
set via `[defaults].agent` with a per-`[[project]].agent` override — with full
lifecycle-callback parity. Beware the **two distinct** uses of "claude": (1) the
pluggable coding agent spawned per issue (above), versus (2) lola-internal
helpers that always shell `claude -p` regardless of that setting — the `[brain]`
summarizer (`internal/brain`), `[review]`, and `[coderabbit]`. Those are NOT the
coding agent and never change with the `agent` choice.

One binary, two roles:
- `lola run` — the daemon (launchd `KeepAlive` keeps it alive)
- `lola` / `lola tui` — the Bubble Tea TUI client
- every other subcommand is a thin socket client that talks to the daemon over
  the unix socket `~/.lola/lola.sock` (newline-delimited JSON, `internal/protocol`)

Config (`~/.lola/config.toml`) is the single source of truth; the TUI edits it,
then sends `reload`. History: through P0–P2 lola was a thin trigger into a
separate Agent Orchestrator (AO) via an `ao spawn` bridge; that bridge is
**removed** — lola is native-only now. Some code/comments still carry `Source:
"ao"` / `AOStatus` fields for back-compat, and `agent-rules.md` marks every rule
that changed with **[changed from AO bridge]**.

## Build / test

Use the Makefile — it sets a repo-local `GOCACHE` (`.gocache/`) and
`GOFLAGS=-mod=mod -buildvcs=false` so builds work in sandboxed shells that can
only write inside the repo. Do not run bare `go build`/`go test` in a sandbox;
they try to write the global build cache and VCS stat cache and fail.

```sh
make build          # -> ./lola
make vet
make test           # go test ./...
make check          # build + vet + test
make tidy           # GOPROXY=off go mod tidy (deps already pinned in go.mod)
```

Run a single test:
```sh
go test ./internal/daemon -run TestDispatch -v      # inside Makefile env
GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go test ./internal/daemon -run TestDispatch -v
```

Go 1.24+ (repo builds under 1.26). Deps: `cobra` (CLI), `bubbletea` + `lipgloss`
(TUI), `BurntSushi/toml` (config). Everything else is stdlib + exec seams.

## Architecture map

The daemon (`internal/daemon`) is the heart; it composes the leaf packages,
each of which owns exactly one external tool or concern behind an **exec seam**
(a swappable function/interface) so tests never touch the real tmux/git/gh/claude:

- `internal/config` — owns `config.toml`: schema, defaults, atomic
  (temp+rename, 0600) persistence, and **static** validation only. `Home()`
  honors `$LOLA_HOME` (every runtime path derives from it; tests set it).
  Path-exists / is-a-git-repo checks live in the runtime layer, NOT here.
- `internal/linear` — Linear GraphQL client (`API` interface + `fake.go` for
  tests). Paginated queries, exponential backoff on 429/5xx, filter built from
  the poll's mode fields. All IDs are Linear **UUIDs** passed as variables.
- `internal/runtime` (`Native`) — the session launcher: `Spawn` (worktree →
  symlinks → `post_create` → tmux `claude --settings`), `Adopt` (re-adopt
  survivors after a restart; reports zombies, never kills), `Kill`. Composes
  `worktree` + `tmux` + `hook`; talks to git/tmux/claude only through them.
- `internal/worktree` — per-session git worktrees under
  `~/.lola/worktrees/<project>/<session>/`. `Remove` refuses a dirty worktree
  (`ErrDirty`) unless forced, and guards the project's main checkout.
- `internal/tmux` — thin tmux CLI adapter on lola's **own** server
  (`tmux -L lola`), isolated from the user's default tmux. Session targets use
  the `=` exact-match prefix.
- `internal/hook` + `lola hook <event>` (hidden subcommand) — the callback path
  from Claude Code lifecycle hooks back into the daemon. `hook.SettingsJSON`
  generates per-session `--settings` wiring Stop/Notification/SessionEnd/
  PostToolUse/UserPromptSubmit to `lola hook`, which posts to the socket. This
  path is on the agent's critical path: bounded 2s, always exits 0 — a broken
  lola must never wedge or fail an agent's turn.
- `internal/scm` — GitHub PR/CI observation via `gh`. `DeriveStatus` is the ONE
  deterministic status derivation used everywhere (caps, reactions, reconcile,
  TUI).
- `internal/session` — pure data: the `Session` model + JSON snapshot `Store`
  (atomic temp+rename). No exec. Holds derived `Status`, PR state, and the
  persisted one-shot guards for reactions (P3) and write-back (P4).
- `internal/agent` — the pluggable coding-agent leaf (stdlib + regexp only; must
  NOT import config/session/hook/runtime/attention): the `claude`|`codex`|
  `opencode` kind enum, per-kind launch argv (`LaunchArgs`), the callback-config
  bodies (codex `config.toml`, opencode plugin JS), and `ParseCodexNotify`.
  `internal/runtime` writes the right callback artifact at spawn; the health-gate
  checks the resolved binary; `config.AgentForProject` resolves
  project→defaults→`claude`. `internal/attention` imports it for agent-aware
  pane classification.
- `internal/secrets` / `internal/notify` / `internal/brain` / `internal/review`
  / `internal/attention` / `internal/doctor` — Linear key resolution
  (keychain→env), best-effort desktop/Slack notify, opt-in headless-claude
  summarizer, opt-in CodeRabbit QA pass, pane→answerable-question heuristic
  parser (agent-aware), structured health checks.
- `internal/tui` — the interactive poll manager + sessions view, AND the plain
  socket client (`Send`/`Logs`) reused by the CLI subcommands.
- `main.go` — cobra wiring only; each subcommand marshals a `protocol.Request`
  and calls `tui.Send`, except `run` (daemon) and `tui` (TUI).

### Daemon internals (`internal/daemon`, split by concern)

- `daemon.go` — the `Daemon` struct and its many exec seams (see the struct's
  field comments — every `func(...)` field is a test injection point), worker
  goroutine management, reload diffing.
- `dispatch.go` — one tick: health-gate → resolve key/cycle → query → drop
  in-flight/dedup → sort by `priority_sort` → take `Budget(pollCap, globalCap,
  liveCounted)` → per issue: **mark in-flight+seen FIRST, then spawn**, then
  (label mode, success only) re-read labels fresh and flip.
- `observer.go` — read-only ~30s loop merging native sessions with `gh` PR
  state into the `session.Store` snapshot; the `sessions` socket command serves
  the cache (a client request never execs gh/tmux). Contains the
  anti-false-working guard (`staleWorkingThreshold`).
- `reactions.go` — P3 engine acting on derived status changes.
- `reconcile.go` — ~5m pass reverting orphaned issues (labeled-sent but no
  counted session and no open PR after `orphanTimeout`).
- `writeback.go` — P4 Linear state transitions + comments.
- `state.go` — the per-poll `seen` store and in-flight set.

## Non-obvious invariants (read before changing daemon code)

- **Health-gate every dispatch.** If `tmux`/`git`/`claude` aren't all resolvable
  or the poll's `[[project]]` doesn't resolve: skip the tick, record `lastError`
  in status, and mutate **nothing** (no seen, no labels, no in-flight).
- **Dispatch order is load-bearing.** Record in-flight + write seen *before*
  spawning, so a crash mid-spawn can't double-dispatch. Upsert the session into
  the store immediately so the next `Budget` call counts it.
- **`liveCounted` comes from the session store snapshot**, never a local
  counter. Only slot-occupying derived statuses count (`working`, `needs_input`,
  `draft`, `ci_failed`, `changes_requested`, `ci_pending`); parked-for-review
  and terminal statuses don't, so held PRs don't stall pickup.
- **Fail CLOSED on unknowns.** The reconcile orphan-revert skips whenever the
  open-PR check can't answer (no repo, gh error) — better a stuck label than
  lost work.
- **Send-keys safety (reactions/review).** Typing into a live agent mid-turn
  corrupts it. Every path that types goes through the `AtPrompt` idle gate
  (consumed atomically via `Store.Update`); a non-idle session has its reaction
  **deferred**, never forced. Payloads are sanitized (control chars stripped)
  and are **never** run as a command.
- **Fire once per transition.** Reactions and write-backs use persisted
  one-shot guards (`LastReactedStatus`, `WB*Done`, review's per-PR guard) so
  they don't re-fire on every 30s observer cycle.
- **Untrusted output stays out of the control loop.** `brain` summaries and
  `review` findings are derived from attacker-influenceable context (PR diffs,
  CI logs, pane text). They may go to a human (notify + Linear comment) but the
  brain summary must **never** be fed back to the worker agent; review findings
  reach the worker only through the sanitize + idle gate.
- **Shutdown-shielded loops.** The observer and reconcile loops run on
  `context.WithoutCancel` and are panic-guarded, with a per-exec deadline on
  every gh/tmux call so a wedged external process can't hang graceful shutdown
  at `d.wg.Wait()`. Spawn is bounded by `nativeSpawnTimeout` for the same
  reason. Preserve these when adding an exec call to those paths.
- **Secret discipline.** The Linear key and Slack webhook URL never live in
  `config.toml`, never appear in argv, a log line, or a returned error. Follow
  the existing pattern (resolve from keychain/env by *name*; sanitize
  `*url.Error`) when touching those packages.

## Testing conventions

- 46 `_test.go` files; the daemon package is the densest. Inject fakes via the
  `Daemon` struct's seam fields and `linear.API` / `fake.go`. Use `$LOLA_HOME`
  (a `t.TempDir()`) to isolate all runtime state.
- Definition of done for a daemon change (per `agent-rules.md`): cover filter
  construction per mode, pagination, `Budget` math, both dedup modes incl. seen
  pruning, cross-poll dedup, labelIds delta, identifier-vs-UUID usage, and the
  native lifecycle (spawn+rollback, adopt classification, store-driven
  `liveCounted`, fail-closed reconcile revert).

## Reference docs

- `README.md` — user-facing: full command list, config reference (every
  `[section]` and key), runtime layout, launchd install, secrets.
- `config.example.toml` — complete commented config.
- `agent-rules.md` — the build spec / rule list (with AO-bridge deltas).
- `SPEC.md` / `PLAN.md` — original spec and phased roadmap (P0–P9).
