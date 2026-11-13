# AGENTS.md

Guidance for coding agents (Claude Code, Codex, OpenCode) in this repository.
`CLAUDE.md` only imports this file. Rules here are the non-obvious ones:
things the code does not say on its own, or traps that already cost a debugging
session. The long-form rationale lives in code comments, `docs/` and git history.

## What lola is

A single Go binary that watches Linear for issues matching a filter (team →
project → cycle → workflow state → labels → assignee) and spawns its OWN
coding-agent session per match: git worktree + tmux session + agent. It then
observes the PR/CI via `gh` and reacts (re-prompt, notify, review, clean up).

- **Coding agent is pluggable:** `claude` (default) | `codex` | `opencode`, via
  `[defaults].agent` with a per-`[[project]].agent` override.
- **Three independent agent choices — don't confuse them:** (1) the per-session
  coding agent above; (2) lola-internal helpers: `[brain]` and `lola plan` default to
  `claude -p`; enabled `[ai.<provider>]` profiles follow the workspace default
  agent, while `[statusagent].agent` independently picks claude | codex | opencode;
  (3) review providers, which name their own agent per
  `[[review.provider]]` kind.
- **Roles:** `lola run` is the daemon; `lola` / `lola tui` is the Bubble Tea TUI;
  every other subcommand is a thin client on the unix socket `~/.lola/lola.sock`
  (newline-delimited JSON, `internal/protocol`). `desktop/` is the macOS app,
  `mobile/` the phone client (via `internal/remote`); both are clients too.
- **Daemon lifecycle** is TUI-managed by default (`internal/tui/daemonctl.go`:
  auto-spawn on open, `^r` restart re-execs the current binary, `^x` stop). With
  `[defaults].manage_daemon = false` launchd `KeepAlive` owns it instead — never both.
- `~/.lola/config.toml` is the single source of truth; UIs edit it, then send `reload`.
- The old AO (`ao spawn`) bridge is removed. Leftover `Source: "ao"` / `AOStatus`
  fields are back-compat only.

## Build / test

Use the Makefile. It sets a repo-local `GOCACHE` (`.gocache/`) and
`GOFLAGS=-mod=mod -buildvcs=false`; bare `go build`/`go test` fail in a sandbox.

```sh
make build | vet | test | check | tidy      # tidy = GOPROXY=off go mod tidy
GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go test ./internal/daemon -run TestDispatch -v
```

`hubproto/` is a nested Go module; `make test` and `make vet` also check it.

Go per `go.mod` (1.25). Deps: cobra, bubbletea + lipgloss, BurntSushi/toml;
everything else is stdlib + exec seams.

### `make build` never reaches the running daemon

The daemon runs from `$GOPATH/bin/lola` (TUI `^r`, the app's restart and a
hand-started `lola run` all resolve it from `PATH`). A change is live only after
`make build && GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go install .`
plus a daemon restart — it never hot-reloads its binary.

- `go install` writes a NEW inode; the old process keeps running old code.
  Check: `lsof -p <pid> | awk '$4=="txt"'` vs `stat -f '%i %N' $(command -v lola)`.
- A stale daemon answers `unknown cmd "<x>"` for a new command, but a missing
  *derivation* (new observer pass, new field) fails silently. Confirm the image
  first: `go tool nm $(command -v lola) | grep <symbol>`.

## Architecture map

`internal/daemon` composes leaf packages. Each leaf owns one external tool or
concern behind an **exec seam** (swappable func/interface), so tests never touch
real tmux/git/gh/claude. Leaves must not import upward (e.g. `scm` never imports
`state`; `agent`, `devtab`, `lolaenv`, `sshtab`, `proctree` are stdlib leaves so
daemon, runtime and UIs can share them).

**Core**
- `config` — `config.toml` schema, defaults, atomic 0600 save, STATIC validation
  only (no path/git checks). `Home()` honours `$LOLA_HOME`. Owns `[defaults]`
  inheritance (see invariants).
- `linear` — GraphQL client (`API` + `fake.go`), paginated, backoff on 429/5xx,
  IDs are UUIDs passed as variables.
- `runtime` (`Native`) — `Spawn` (worktree → symlinks → `post_create` → tmux
  agent), `Adopt` (re-adopt after restart, reports zombies, never kills), `Kill`.
- `worktree` — `~/.lola/worktrees/<project>/<session>/`; `Remove` refuses dirty
  (`ErrDirty`) unless forced; `DeleteBranch` for an already-gone checkout.
- `tmux` — lola's OWN server (`tmux -L lola`); targets use the `=` exact prefix.
- `hook` + hidden `lola hook <event>` — agent lifecycle hooks → daemon socket.
  On the agent's critical path: bounded 2s, always exits 0.
- `scm` — `gh` PR/CI FACTS only. `state` — the status vocabulary (see invariants).
- `session` — pure data: `Session` model + JSON snapshot `Store`.
- `protocol` — wire types. `remote` — TLS phone listener; `mdns` advertises it;
  `panebus` multiplexes one tmux attach per pane across subscribers.
- `tui` — TUI plus the socket client (`Send`/`Logs`) the CLI reuses. `main.go`
  is cobra wiring only.

**Agents, sessions, git**
- `agent` — kind enum, `Caps` (only claude has feed/send/stop/cards/paste),
  launch argv, READ-ONLY review argv (`ReviewArgs`), callback configs.
  `config.AgentForProject` resolves project → defaults → claude.
- `attention` — pane → activity/question classifier (agent-aware).
- `agentlog` (turn in flight?), `agentfeed` (Conversation reader, display-only),
  `agentoutput` (CLI telemetry vs untrusted text).
- `board` — `lola report <verb>` self-report; `claimaudit` — transcript audit of
  its `check … pass` claims. Both display-only.
- `checkpoint` — per-turn snapshots as refs. `gitdiff` — worktree vs merge-base.
  `diffanchor` — which PR diff lines can carry inline comments. `gitrepo` —
  local-git autofill (fails closed to zero values). `gitguard` — pins host git
  for sandboxed worktrees.
- `claudetrust` — the ONE writer of `~/.claude.json` (per-worktree trust key).
- `usage` (token scanners + ledger), `quota` (subscription limits, display-only),
  `sysload` (`[load]` hold).
- `planner` (`lola plan` decomposition), `bestof` (mix vocabulary + ranking),
  `statusagent` (opt-in status interpreter, display-only), `brain` (summarizer),
  `notify`, `secrets`, `doctor`, `timefmt` (keep in sync with
  `desktop/frontend/src/lib/time.ts`), `vtterm` (embedded PTY panel).
- `goal*` — autonomous goals (contracts, policy, isolated work stores, container
  checks, review, publication); see `docs/workspace-goals.md`. `knowledge`,
  `taskboard`, `comparison` — candidate lessons, shared requirement list,
  evaluation records.

**Dev servers & processes**
- `devtab` (`<id>-dev-<n>` naming), `devurl` (rank loopback URLs from pane text),
  `devforward` (publish one loopback server on the LAN while watched),
  `portproc` (lsof listeners + cwd, fails open), `portlease` (per-session port
  blocks), `portclash` (dead tab → port number only), `proctree` (process-group
  termination across the ppid tree), `lolaenv` (per-session env file contract).

**Review**
- `review` (cli family), `reviewagent` (agent family, `review.Client` shape),
  `reviewrun` (visible-pass hand-off), `reviewmd` (findings → GitHub Markdown),
  `reviewprofile`, `reviewpack`, `reviewverify`, `reviewcapture`.

**SSH** — `sshconn` (the only ssh argv builder), `sshstore`, `sshelement`
(agent claims), `sshterm` (shell bootstrap + parsers), `sshtab` (naming).
Contract: `docs/ssh-sessions.md`.

### Daemon files (`internal/daemon`)

`daemon.go` (struct + seams — every `func` field is a test hook), `dispatch.go`
(one tick), `slots.go` (slot reservation), `observer.go` (~30s read-only loop:
one `tmux ls`, gh facts → delivery axis, pane → agent axis),
`reactions.go`, `reconcile.go` (~5m orphan revert, lease pruning),
`writeback.go` (Linear), `review*.go`, `dev.go`/`devclash.go`, `mergequeue.go`,
`bestof.go`, `hookbridge.go`, `apps.go`/`stackgit.go` (stacks),
`talk*.go`/`timeline.go` (Conversation), `ssh*.go`, `goal*.go`, `state.go`
(seen store + in-flight).

## Invariants — read before changing daemon code

### Config
- **A `Project` field holds the RESOLVED value; `Inherits` records the source.**
  Inheritable keys: `match_labels`, `match_mode`, `on_sent_set_label`,
  `blocked_label_id`, `dedup_mode`, `priority_sort`, `symlinks`, `post_create`, `env`.
  - Mutating e.g. `p.MatchLabels` without clearing `p.Inherits.MatchLabels`
    silently discards the write on `Save`. Form layers go through an explicit override step.
  - The bitmap's zero value means "fully explicit". Never flip that polarity.
  - `fileProject` uses pointers: absent key = inherit, `key = []` = override to nothing.
  - `ResolveInheritance` is idempotent; Load/Validate/Save all call it.
  - NOT inheritable (zero = fall back at read time): `agent`, `concurrency_cap`,
    `branch_prefix`. Never inheritable: `group`, `dev_commands`, `port_range`,
    `ports_per_session`, `pre_remove` (an inherited one would hit the wrong repo).
- **`Name` is identity, `Label` is display.** `Name` is a path segment, session-id
  prefix and tmux name. Render `p.DisplayName()` / `cfg.DisplayNameFor(id)`, never
  a bare `p.Name`. `config.Slug` is the one label → id function (`SlugTyping` for
  live typing). Slug shape is a UI rule, not validation (legacy `"Okane"` must load);
  only re-slug a name a human typed. Rename is `cmd=renameProject`, idle-only.
- **Project panel arrangement is display-only.** `group`, `[[project]]` order and
  `[[group]]` `position` are read by nothing in daemon/runtime. Dangling groups
  are repaired to "" with a notice, never rejected. `SetProjectLayout` fails
  closed unless the payload is an exact permutation. The project form has no
  group field on purpose (`SaveProject` leaves `Group` untouched); keyboard
  equivalents `⌥↑/↓/→/←` must stay. The drag uses pointer events; its logic is in
  `desktop/frontend/src/lib/sidebarlayout.ts` (no DOM, tested).
- **New project starts at the folder; every derived field is fill-only.** One
  `gitrepo.Inspect` fills path/label/name/repo/default_branch. Never overwrite a
  human value (only empty or form-written `repoAuto`/`branchAuto`). Identity is
  suggested for NEW projects only. Drop a result whose path no longer matches.
  Opening an existing form must not make it dirty.
- **`[defaults]` labels must be WORKSPACE labels — a UI rule, not validation.**
  Scope is unknowable offline; do not reinstate a cross-team check in `Validate`.

### Dispatch & slots
- **Health-gate every dispatch.** tmux/git/agent unresolvable or project missing →
  skip the tick, set `LastError`, mutate nothing.
- **Dispatch order is load-bearing:** mark in-flight + write seen BEFORE spawning;
  upsert the session immediately so the next `Budget` counts it.
- **Holds never touch sessions:** `dispatchHold` (spend/load), `dependencyHold`
  (Linear blockers not done/merged, or open sub-issues). Skip, write no seen entry.
  A load value the OS will not report holds nothing (fail open).
- **`state.HoldsSlot(agent, delivery)` is the one slot classification.** A session
  holds a slot while its agent is alive and its PR is not parked on a human
  (`review_pending`, `approved`) or terminal. A live pre-PR agent holds a slot
  whatever it is doing, idle included — excluding idle lets dispatch blow past the cap.
- **Caps race:** every spawn takes `reserveSlots` (project cap + `global_cap`
  against live sessions + in-flight spawns, one lock). best-of/goal attempts use
  `noProjectCap`. Human opens check no cap but count.
- **Best-of** (`bestof.go`): N attempts (`-b<k>` suffix) on one issue. Charge the
  whole mix or nothing; dedup the issue once; hold PR-open write-back until the
  pick. The pick stamps `BestOfWinner` FIRST, closes losers' PRs (branches kept)
  and kills losers without freeing the in-flight claim or calling
  `clearLabelDispatch`. Reconcile checks every attempt's branch for an open PR.
- **Plans** are created only by `cmd=planApply`; sub-issue placement
  (`subIssuePlacement`) mirrors `linear.BuildIssueFilter` — change both together.
  A parent that already has sub-issues is refused.
- **Dead filter refs are reported:** a zero-match tick resolves the filter's
  labels/states (`filterrefs.go`); an unmatchable one becomes `LastError`. Fails open.
- **Fail closed on unknowns:** reconcile skips the orphan revert whenever the
  open-PR check cannot answer.

### Status axes (`internal/state`)
- A session is `AgentState` (hooks + pane + tmux) × `DeliveryState` (gh facts).
  PR facts never mask the agent axis. Write via `SetAgentState`/`SetDelivery`
  only — never write `Status` directly.
- Consumers take the PAIR and live in `display.go`: `DisplayFor` (primary pill:
  working | idle | needs_you | gone | shell | orphaned; unknown → working),
  `Attention` (a predicate, not a state), `HoldsSlot`, `Present`, `SortRank`,
  `KanbanKeyFor`. Nothing may classify by the rollup string.
- `Rollup` survives only as the wire shim for `SessionInfo.status` (16 legacy
  words, read by `mobile/`). Do not delete it. `state.Notable` is the one
  string-keyed classifier left (recorded transitions). `FromLegacy` backfills old snapshots.
- `desktop/frontend/src/lib/theme.ts` mirrors the vocabulary;
  `desktop/state_parity_test.go` pins vocabularies and column keys but NOT column
  membership. Re-read the other port when you touch either.

### Typing into an agent
- **Every send goes through the idle gate and a fresh pane proof.** Use
  `typeToAgent` (holds `sendGate` shared), never `d.sendKeys`. A non-idle session
  defers, never forces. Payloads are sanitized and never run as commands.
- An adoption-carried gate is unverified and must pass `ensurePromptVerified`.
- Review hand-offs use the wider `handoffDeliverable` gate (`AtPrompt`, idle
  notification, or pane-reconciled `AgentIdle`; never `InputPermission`), but
  ALWAYS require `paneWaitingNow` → `ActivityWaiting` before a byte is typed — a
  hook verdict says nothing about now (a modal may cover the prompt).
  `flushReviewHandoffs` delivers ONE item per pass; the Stop hook flushes async.
- `resolveConflict` uses the same gate + proof but REFUSES instead of deferring,
  and stamps `LastReactedStatus`.
- Diff-viewer feedback (`cmd=feedback`) and `cmd=broadcast` queue
  (`PendingFeedback`/`PendingBroadcast`) and DEFER, never refuse; a failed send re-queues.
- Human presses use `lockHumanPrompt`, queued/automated sends `lockAutomationPrompt`;
  both demand `ActivityWaiting` + `ComposerEmptyAt` (cursor-based). The one
  exception is `lockHumanMessage`: a human's submitted Conversation message may
  reach a supported agent mid-turn, but still needs fresh proof of an empty
  composer. Dialogs, drafts and unknown screens stay blocked; slash commands and
  Stop & send keep the resting-prompt gate. Every path
  records a `typedNote`. Claude messages >800 bytes go by `load-buffer` +
  `paste-buffer -p` (`send-keys -l` corrupts >1KB); other agents stay typed.
  Always `send-keys -l --` with a trailing `;` escaped.
- `agentChoose` sends one key, no Enter, after re-reading the pane against the
  hash the human saw. `/config`-type screens classify as waiting, so never rely
  on `Classify` alone in a composer path.
- `agentStop` needs a working pane, debounces (two Escs open Rewind), holds
  automation 30s and the human queue until the human speaks. `escIfWorking` is the one Esc.

### Pane classification (`attention.Classify`)
It mirrors claude-code's rendering, so a rendering change silently disables
every send path. Re-verify against a live pane.
- Caret padding is U+00A0; `stripANSI` folds Unicode `Zs` to space.
- The composer is drawn mid-turn too. Only the live status line (gerund +
  ellipsis + running timer) means working: `hasLiveWorkingCue` runs before the
  waiting cue. Frozen meters/spinners lose to a resting prompt.
- Modals (full-width `▔` rule, `modalOverlayRe`) → `ActivityBlocked`, checked
  first; mapped to `AgentWaitingInput` + `InputDialog`. Not the `Esc to cancel`
  footer (AskUserQuestion shares it). Never add a Blocked case that types.
  Prevention via `modalSkills` in `hook.SettingsJSON` — keep both halves.

### tmux and terminals
- **`tmux.ScrollPane` picks the scrollback:** alt-screen programs (agents) get an
  SGR wheel sequence; plain shells get copy mode. Neither needs `[tmux].mouse`.
  Both UIs call it (`TermService.Scroll`, TUI `forwardWheel`); don't re-derive.
  Unanswerable pane → copy mode. The app's wheel handler always returns false.
- Copy mode outlives the scroll: `TermService.Write`, `Detach` and `tmux.SendKeys`
  cancel it before input, or keys become copy-mode commands.
- Shift+Enter = `ESC CR`, encoded by both surfaces (`LiveTerminal.svelte`
  swallows the event; TUI on `tea.ModShift`).
- `(*tmux.Client).ConfigureServer` is the one place for global options; called on
  every `NewSession`, retried because a cold server cannot be configured.
  `history-limit` applies at pane creation. Build clients via `config.TmuxClient`.
  `mouse` is written in both states. Verify new options against a real tmux.

### Teardown, dev sessions, ports
- **Teardown = tmux sessions + worktree + branch**, each fail-closed. Kill the
  agent AND aux sessions (`-shell-N`, `-review`, `-dev-N`, `-ssh-N`; regex
  anchored at both ends) via `tmux.KillSessionTree` (signals every process group
  in the pane's ppid tree; agent Bash commands leave the group). Aux sweep is
  best-effort. Delete the branch only if `Session.OwnsBranch()`. A dirty worktree
  keeps checkout and branch. `pre_remove` runs only on a clean worktree, bounded
  and best-effort, never for sandboxed sessions. Call `sshCloseSession` before
  `runtime.Kill`.
- **The ACTIVE session is derived from tmux, never remembered.**
  `DevActive`/`DevTabs`/`DevURLs`/clash are caches overwritten by
  `reconcileDevTabs`. Liveness is `#{pane_dead}` (tabs use `remain-on-exit`); a
  failed probe changes nothing. `cmd=dev` is a MOVE: stop the old holder's tabs
  (discovered by listing) first, then `sweepPortSquatters` — only inside
  `~/.lola/worktrees/<project>/`, never a group owning a live pane, fail closed.
- `lolaenv.CommandLine` only `exec`s a SIMPLE command (exec before `cd x && y`
  runs nothing under bash 3.2). Classifier stays conservative.
- Dev URLs: fast watch on activation (`startDevURLWatch`) + observer fallback
  (`scanDevURLs`). Both keep refining after the first address (a sidecar may
  print before the app settles its port), bounded by `devURLAttempts`; opened via `cmd=openURL` (http(s) only), never `window.open`.
- Port clash: read a dead tab once per death, lsof only when the pane named a
  port, kill only as a human's dialog answer re-verified at click time.
- `port_range` = multi-active: per-session lease (`portlease`, keyed by session id,
  idempotent) exposed as `LOLA_PORT*` via `p.Env`; lola values win. Leasing is
  best-effort at spawn, required at activation. Released on worktree removal,
  rollback, or `pruneLeases`.

### Reviews
- **A review reports, never writes — enforced by argv** (`agent.ReviewArgs`:
  claude headless defaults, codex `--sandbox read-only`, opencode without
  `--auto`). For codex/opencode the prompt is positional and LAST; the diff goes on stdin.
- **Vendors are config.** Seven kinds in three families (agent / cli / watch);
  dispatch switches on family (`config.ReviewAgentFor`/`IsCLIKind`/`IsWatchKind`),
  per-kind seams live in `d.passRuns`. One provider per kind. A kind that names
  nothing (or is unknown) is disabled, never silently CodeRabbit. `bot-watch`
  has no author default. Both UIs generate editors from `config.ReviewProviderKinds()`.
- **A pass never runs on the observe loop:** `reviewworker.go` drains a queue.
  Agent-family timeout default is 900s. Non-answers release the once-per-PR guard
  (`noteReviewOutcome`, up to `reviewMaxAttempts`).
- Visible passes run in `<id>-review`; the daemon never parses the pane — the
  child writes `~/.lola/cache/review/<session>/` mapped back to the same `Err*`
  sentinels. Anything failing degrades to direct exec.
- stderr: classify quota/auth only on failure, keep a TAIL buffer, use phrases
  (not bare `auth`). `review` and `reviewagent` carry independent copies — fix both.
- **GitHub output** (`reviewmd`): CSS is stripped, so structure only (alert, emoji,
  `<details>`, `<kbd>` chips inside one blockquote, every line `>`-prefixed). The
  FORMAT block in the review prompt is a contract (`TestReviewInstructionPinsTheGradedShape`).
  Grade vocab is whitelisted. Unparseable → verbatim. Self-bounds to 15KB.
  Links only for `owner/name` + plain `path:line`.
- **Inline reviews** (`reviewinline.go`): the endpoint is atomic, so anchor only
  lines `diffanchor` confirms (snap ≤3 lines, stated in the body). Degrade to the
  plain comment, except on transient gh errors (retry inline). Tally counts all
  findings. `neutralizeBotTriggers`/`neutralizeWatchedBots` on every body.
- Close-the-loop notes: `inlineThreadNote` (asserts threads, derived from
  `InlineReviewPRs[kind] == PR.Number`) vs `prThreadNote` (conditional). Both
  appended AFTER untrusted text.
- A forced review must also `unstampGithubSettled` before exec (not `InlineReviewPRs`);
  releases only clear guards pointing at that PR.
- **Review gate:** `lola/review/<kind>` commit status (lola cannot REQUEST_CHANGES
  on its own login's PR). New head → re-review when the agent is idle, capped by
  `reviewGateRounds`. Verify clean checkout + HEAD matches PR head around the pass.
  Findings never go in a status. `SetCommitStatus` refuses contexts outside `lola/`.

### Reactions, CI, write-back
- Reactions dispatch on `s.Delivery`, never the rollup (merged → dead-pane check
  → ci_failed → merge_conflict → changes_requested → approved → closed).
  `resetReactionGuards` has no agent-axis special case.
- CI is judged by the base branch's REQUIRED checks (`requiredchecks.go`); any
  unknown keeps the all-checks verdict; `lola/*` contexts are never CI. The merge
  queue reads `reviewGatesOpen` for the gate.
- Merge queue: only the head PR per repo acts; merge pinned with
  `--match-head-commit`; every unknown holds.
- Fire once per transition: persisted one-shot guards (`LastReactedStatus`, `WB*Done`, per-PR review guards).

### Checkpoints, sandbox, stacks, context

- Docker defaults to Lola’s versioned image (edge for development); its Linux
  Lola binary is extracted into a protected cache when `sandbox.lola_bin` is
  unset. Per-project toolchains build on the default image; custom images
  supply their own tools. API-key billing requires `sandbox.use_api_key`.
  Desktop sandbox login is handled by `SandboxLoginService`.
- **Checkpoints are refs** (`refs/lola/checkpoints/<session>/<seq>`), recorded via
  a temporary index (`GIT_INDEX_FILE`), `--no-gpg-sign`, fixed identity; never
  `git stash create`. Baseline recorded by runtime before first launch. `Restore`
  moves files only (HEAD never moves), records a before-snapshot, refuses
  `ErrWouldClobber`, holds `sendGate` exclusively. `.lola/` excluded. Forks cut
  at the checkpoint via `worktree.CreateAt`.
- **Sandbox (`docker`): the mount list IS the isolation.** git `config`/`hooks`
  re-mounted read-only; mount sources come from things the agent cannot write.
  Every host git exec on a sandboxed worktree goes through `gitguard.Apply`;
  unpinned records are `gitguard.Deny`'d. Hooks dial `LOLA_HOOK_ADDR`; the bridge
  admits only `hookEvent`/`agentReport` with an HMAC token — never widen it.
  `docker rm -f` on kill and before every launch. No host commands in a sandboxed
  worktree after the agent starts. Project is passed BY NAME, not inferred from the id.
  `TestSandboxContainsTheAgent` (`LOLA_SANDBOX_IT=1`).
- **Stack sessions** (`kind = "stack"`): one agent in a folder of per-app worktrees.
  Apps chosen once at spawn (`config.SelectApps`). PR state lives on `session.App`;
  act on it through LANE views (`<id>~<app>`), never write lane fields on the
  session. The session's own PR fields are a derived aggregate (`Reaggregate`).
  Prefix agent messages with `laneHeader`. No best-of, fork or checkout-only
  launches for stacks.
- **Borrowed-directory sessions** (`session.KindLocal`: main-checkout and
  project-free temporary sessions). `Worktree` is their working directory, never
  an owned worktree: teardown must never remove it or run worktree hooks, and
  they own no branch. Their callback/settings/env files live in
  `$LOLA_HOME/local-sessions/<id>/`. Reactions, checkpoints, dev sweeping and
  automatic agent switching exclude them. Temporary sessions must work without a
  config entry. `Session.Name` is display-only (`DisplayTitle` on the wire);
  editing it never renames paths or tmux targets.
- Shared context folder lives outside the worktree (`~/.lola/context/<project>/<key>/`,
  symlinked as `.lola/context`). Linking is best-effort; nothing deletes it.
- `claudetrust`: only the per-worktree trust key changes; atomic; never rewrite
  an unparseable file; failures advisory.

### Untrusted output, claims, loops, secrets
- `brain` summaries never reach the worker. Review findings reach it only through
  sanitize + idle gate. `[statusagent]` output reaches only `displayOverlay`.
  `Session.Board`, the claim audit, `agentfeed`, `quota`, overlaps, best-of
  ranking are display-only. A report is not activity evidence.
- Observer and reconcile run on `context.WithoutCancel`, panic-guarded, with a
  deadline on every exec. Spawn is bounded by `nativeSpawnTimeout`.
- Secrets (Linear key, Slack webhook) never in config, argv, logs or errors.
  The Linear key is write-only from UIs (keychain), never on `SettingsDTO`, never read back.
- Usage: UIs show tokens, not dollars (`~$` in tooltips only). Raw tokens displayed,
  weighted tokens budgeted, list price ranks. Rank against finished sessions only.
  Never mix agents in one ranking. `hook.StatusLine` must ALWAYS pass through to
  the user's own status-line command.

### Team hub

- **Hub work is a CLAIM and a candidate, never a command.**
  (`internal/daemon/hubwire.go`, lola-hub's `DESIGN.md`.) A compromised hub must
  not mean code execution here. Rules:
  - An offer is a CANDIDATE: `hub.Accept` (valid item, opted-in non-stack
    project, accepted kind, daily cap, sandbox unless `require_sandbox = false`)
    and then exactly a Linear dispatch's gates in its order — `dispatchHold`,
    the health gate, `reserveSlots` — all BEFORE the claim. Nothing is claimed
    while the heartbeat fails or the config failed validation.
  - An offer is the hub's CLAIM about a repo, so it is re-read from GitHub
    before the claim (`hub.Collector.Verify`, fail closed): a `red_bump_pr`
    must be OPEN, by the named bot, not cross-repository, on exactly the named
    branch and still failing; a `dep_advisory` must match an open Dependabot
    alert (advisory + package + ecosystem). The session — branch, briefing,
    `StartHead` — is built from GitHub's answer. Without this a compromised hub
    could have every member's agent push to `main` or install a version it
    chose. An item ended here without completing is not re-claimed for
    `hubCooldown`.
  - The hub never writes a prompt. The agent gets `hub.Briefing`, lola's
    template over fields `hubproto.Validate` checked, through the SAME launch
    paths a human uses: `OpenManualAgent` (new branch) for `dep_advisory`,
    `OpenPRAgent` (the bot's branch, never deleted) for `red_bump_pr`.
  - The claim is the in-flight mark. The daily count is persisted before the
    claim, refunded only when the claim is refused, KEPT when the spawn fails
    (the item is released) — or a broken project claims it every cycle.
  - The hub never stops a session. A lost lease sets `HubItem.LeaseLost`, logs,
    and ends renewal and reporting; the agent keeps working.
  - Outcomes are one-shot PER CLAIM (item id + claim time): queued in a
    persisted outbox (`state/hub-daemon.json`) FIRST, then `HubItem.Reported`
    is stamped, with a settled list keyed the same way, so a crash or a down
    hub neither repeats nor loses one; a re-claim drops whatever an older claim
    left queued (a stale release would free the new lease). A lease whose
    outcome is still unsent keeps being renewed. Teardown (`dropSession`) only
    queues and nudges, it never waits on the network. A `red_bump_pr`
    completes only once the PR head moved past the one GitHub showed at claim
    AND is green; an unmoved, still-red one ends as refused / agent_gave_up,
    never not_reproducible. `HubItem` survives adoption.
  - Only what the protocol names leaves the machine: repo slugs, validated
    findings, item ids, a PR URL and a delivery word — never a session id, path
    or pane text. `hubStatus` is a read a phone may make.
  - **A verified team policy LOCKS every key it carries, in the daemon's
    MEMORY only.** (`internal/config/teampolicy.go`, `internal/daemon/
    hubpolicy.go`.) `overlayTeamPolicy` runs on every config the daemon puts
    live — startup and each reload — AFTER `Validate` and after
    `ResolveInheritance`, and calls `config.ApplyTeamPolicy` with the stored
    bundle once `hub.ApplicablePolicy` says yes: hub enabled, the bundle
    RE-VERIFIES against the current `trusted_policy_keys`, a joined identity
    for `[hub].url` names its org, `[hub].apply_policy` is not false. Every
    unknown applies nothing. Rules:
    - The overlay is never a file. `ApplyTeamPolicy` marks the Config and
      `Config.Save` refuses a marked one (`ErrTeamPolicyOverlay`), so a daemon
      path that writes config.toml must re-`Load` the file and edit that
      (`handleEnable` does, then mirrors the flag onto `d.cfg`). The TUI and
      the app never apply it; they read and write the file.
    - Each key maps to exactly its runtime field: `review_providers` →
      exactly those kinds enabled in the catalog (own entry or kind defaults;
      a kind that cannot run stays off with a notice; fallbacks to a disabled
      kind dropped; legacy tables folded in memory), `budget_daily_weighted_
      tokens` → `[budget].daily_tokens`, `merge_queue_enabled` →
      `[merge_queue].enabled` (false locks it off), `max_concurrency_hint` →
      a CEILING on `global_cap` (min, never a raise), `accept_kinds` →
      `[hub].accept_kinds`. `escalate_after` is the hub's; it is shown, never
      applied. Anything unmappable is a notice (`lola doctor`'s "hub policy"
      row), never a guess. The `Inherits` bitmap is untouched.
    - A newly fetched version takes effect through ONE reload
      (`hubPolicyReload`, once per version). The fetch runs every 5 minutes
      and on `cmd=hubSync`. `hubStatus.policy` carries `applied`,
      `appliedKeys` (team value + local value) and `applyNote`; the app reads
      them through `ConfigService.TeamPolicy` to show locked controls.
    - No Settings key may ever execute anything (hubproto's closed set).
  - **A person's hub command is a human act, and only a human's.**
    `protocol.HubHumanCommands` (join, leave, board, org, claim, release,
    sync) are denied by `internal/remote` as a list (a phone gets none of
    them, the board and org included: they carry the whole team's work and
    device names) and named by no sandbox hook-bridge allowlist. Rules:
    - `cmd=hubClaim` goes through `hubClaimItem`, the loop's path, with the
      human rules of every human open: the click is the opt-in (no
      `hub_chores`) and intent (no concurrency cap, no daily count, no
      cooldown), it still RESERVES a slot so a tick beside it counts it, and
      `require_sandbox`, the kind filter, `dispatchHold`, the health gate and
      the GitHub re-read all hold — a click does not make an item safer to run
      on the host. Several projects on the item's repo need `project`. A
      per-item in-flight set keeps the loop and a click from claiming one item
      twice.
    - `cmd=hubRelease` never stops a session: it sets `HubItem.Released`,
      which ends renewal and reporting exactly like a lost lease (`Open()`).
      A hub that cannot be reached gets the release through the outbox; a hub
      that refuses it changes nothing here.
    - The board/org views are display data: cached (20s / 60s) so a polling UI
      cannot flood the hub, refused whole on one bad row, and nothing on them
      is acted on — a claim re-finds the item (offers, then the open board)
      and re-reads it from GitHub.
    - `cmd=hubJoin` carries the enroll token: never logged, echoed or
      returned, scrubbed from an error that happens to contain it, and the
      enrollment is shielded from a client hanging up mid-request.

### SSH (contract: `docs/ssh-sessions.md`)
- `ssh -O check` exit 0 is the only proof of a connection; nothing read from a
  pane moves a state; no auto-reconnect. Keep `ProxyCommand=false`.
- `protocol/ssh.go`'s split is the trust boundary (`SSHAgentCommands` vs human
  commands; bridge and phones get no human commands). Production refuses
  unsandboxed agents unless `allow_unsandboxed` (`SSHAdmit`, `SSHDirectSandbox`).
- Agent commands run on exec channels, never in the human's shell. Human Run
  blocks are `Private` until `sshShareBlock`. Facts vs claims never mix.
- Prompt answers: memory only, via `tmux.PasteSecret`, never argv/log/store; stale
  cards must not type.
- Two daemon sentences are matched by the UIs ("changed since", "not waiting for
  approval"); rewording them in `sshhuman.go` changes both UIs.

### Desktop session view (design: `docs/unified-session.md`)
- One DOM, three container-size layouts (`.cv-*` in `app.css`); one
  `SessionEmbed` at two heights (CSS state, never `{#if}`).
- `lib/answerblocks.ts` builds elements as data (no `{@html}`), fails open, loses no word.
- Plates = lola witnessed it; cards = you can act on it; nothing else gets a box.
  State glyphs never appear in story rows.
- Terminal attaches only while on screen; no hidden/read-only clients.
- Ended sessions stay readable 14 days (`State.Ended`). Comments anchor only on the base diff.
- **Widgets are untrusted:** `sandbox="allow-scripts"` only, `data:` documents,
  a `MessageChannel` port is the only channel, `lola.ask`/`lola.run` only fill.
  Frames never move in the DOM (`DockArea.svelte`, append-only each).
  `desktop/runtimeguard.go` + `TestRuntimeGuardWasReviewedForThisWails` must be
  re-read on every Wails upgrade. Never claim "no network" in the UI.

### UI conventions
- `[ui].theme` paints both surfaces; `internal/tui/catppuccin.go` ports
  `catppuccin.ts`. TUI palette is `var`; every palette-derived style must be built
  in a `rebuild*Styles()` registered in `rebuildStyles()`. Keep `config.UIThemes`
  and TS `THEME_IDS` identical.
- App: every action is `<Button>`, every popover row `<MenuItem>`. Literal class
  maps only; `enabled:hover:`; recolour with Tailwind's trailing `!`; in-flight
  state lives in the store. Labels are Sentence case. Test by role
  (`getByRole("menuitem", { name })`).
- `text-xs` is the uppercase micro-label tier; use `text-sm` for small text.
- No OS-drawn form controls: `Checkbox.svelte`/`Select.svelte`; glyphs are sibling
  `<svg>`s, never pseudo-elements. `Controls.test.ts` enforces it.
- Destructive = shifted key + confirm (`x` stop / `X` remove; `D` dev toggle).
  One `confirm` store and `ConfirmDialog` in the app.
- TUI forms: call `rebase()` after every async fill; `ctrl+c` stays gated on
  `m.form == nil && m.settings == nil`.

## Testing

- **Never drive a UI with synthetic OS input** (no CGEvent, `osascript`,
  `cliclick`, Accessibility API, focus or pointer moves). Use, in order: vitest +
  @testing-library `fireEvent`; `xcrun simctl io <udid> screenshot`; launch deep
  links (`SIMCTL_CHILD_LOLA_DEV_LINK`, `mobile/src/lib/devlink.ts` — add a link
  target, never a tap); a browser harness at phone viewport. Otherwise say it is
  unverified.
- Inject fakes via `Daemon` seam fields and `linear/fake.go`; isolate state with
  `$LOLA_HOME = t.TempDir()`.
- Definition of done for daemon changes: `agent-rules.md`.

## Desktop app (`desktop/`)

**Lola**: Wails 3 + Svelte 5 runes + Tailwind v4 + xterm.js. A package inside
this Go module (to reuse `internal/`), a client of the daemon socket, driving
`tmux -L lola` directly for terminals. Bound services: Talk, SSH, Daemon, Term,
Config, Context, Doctor, Linear, Update. One project form with tabs (TUI too).
Needs the `wails3` CLI. See `desktop/README.md`.

- `wails3 task build` rebuilds only `bin/Lola`; the `.app` is made by
  `wails3 task package`. Iterate with `wails3 dev`.
- The Dock name comes from the `.app` directory name: `APP_NAME: "Lola"` in
  `desktop/Taskfile.yml`, `CFBundleExecutable` in the Info plists and `APP_PATH`
  in `.github/workflows/build.yml` must agree. Never change `CFBundleIdentifier`.
- WKWebView does not stretch a flex child in a flex column — use grid. Verify in the real `.app`.
- The app bundles the CLI at `Contents/Resources/bin/lola`; resolution is
  `$LOLA_BIN` → `PATH` → bundled (PATH first for the dev loop). `InstallCLI`
  symlinks only. Never vendor tmux.
- `ensurePATH` probes the login shell and reads a sentinel, not raw output.
- Bare keys belong to the frontend (bail on `isChord`); ⌘ chords belong to the
  macOS Session menu (`desktop/main.go`). Avoid Edit-menu chords and ⌘⌫; list new
  ones in `HelpOverlay.svelte`.
- Terminal links: `WebLinksAddon` + `linkHandler` → `store.openURL` → daemon. Never `window.open`.
- Icon is icns-only. Do not re-add `CFBundleIconName` / `Assets.car` (macOS 26 shrinks the tile).
- Self-update needs the repo PUBLIC (anonymous releases API). Keep `available`
  and `installable` separate (the DMG lands after the release). Manual checks pass
  `force`. Update prefs live in `~/.lola/desktop-update.json`.
- Releases are release-please; `build.yml` is called via `uses:`, not a tag trigger.
  goreleaser appends artifacts without touching notes.

## Reference docs

`README.md` (commands, full config reference), `config.example.toml`,
`agent-rules.md` (build spec), `SPEC.md` / `PLAN.md`, `docs/unified-session.md`,
`docs/ssh-sessions.md`, `docs/workspace-goals.md`.
