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
  `mobile/` the phone client (via `internal/remote`, directly or through the hub relay); both are clients too.
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

`hub/` is the team hub (the server daemons enrol with), a SEPARATE Go module with
its own rules: read `hub/AGENTS.md` and `hub/DESIGN.md` before touching it. It
builds with `cd hub && make check`, imports nothing from this module except
`hubproto`, deploys from `main` through `.github/workflows/hub.yml`, and is
excluded from lola's CI, releases and sandbox image. A wire change goes in
`hubproto/` and updates the daemon and the hub in the SAME pull request, and
stays backward compatible: installed daemons and the deployed hub run different
versions.

Go per `go.mod` (1.26). Deps: cobra, bubbletea + lipgloss, BurntSushi/toml;
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
- `protocol` — wire types. `remote` — the phone listener and relay acceptor (one TLS config,
  one frame loop for both): mutual TLS 1.3, device lookup, pairing, capability tiers, event
  push; `devices` — the paired-phone registry (`~/.lola/devices.json`); `eventbox` —
  per-connection event mailbox (coalescing, resnapshot, stream end; transport-free); `qr` —
  byte-mode QR for `lola pair`; `mdns` advertises the listener; `panebus` multiplexes one
  tmux attach per pane across subscribers.
- `tui` — TUI plus the socket client (`Send`/`Logs`) the CLI reuses. `main.go`
  is cobra wiring only.

**Agents, sessions, git**
- `agent` — kind enum, `Caps` (claude has feed/send/stop/cards/paste; codex has all but cards and is paste-only; opencode none),
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
- `askMerge` (desktop only, tier `never`) is the branch graph's "Ask agent to
  merge": the app never merges into a sandboxed session's checkout, so it asks
  that session's agent through `typeAtRestingPrompt` with the human lock, and
  REFUSES like `resolveConflict`. The branch name is the one outside value in
  the instruction: `validMergeRef` keeps it to a plain branch-name alphabet. No
  fetch is named (the human picked the host's ref) and stacks are refused.
- Diff-viewer feedback (`cmd=feedback`) and `cmd=broadcast` queue
  (`PendingFeedback`/`PendingBroadcast`) and DEFER, never refuse; a failed send re-queues.
- Human presses use `lockHumanPrompt`, queued/automated sends `lockAutomationPrompt`;
  both demand `ActivityWaiting` + `ComposerEmptyAt` (cursor-based). The one
  exception is `lockHumanMessage`: a human's submitted Conversation message may
  reach a supported agent mid-turn, but still needs fresh proof of an empty
  composer. Dialogs, drafts and unknown screens stay blocked; slash commands and
  Stop & send keep the resting-prompt gate. Every path
  records a `typedNote`. Claude messages >800 bytes go by `load-buffer` +
  `paste-buffer -p` (`send-keys -l` corrupts >1KB). Codex takes EVERY send as a paste
  (`PasteOnly`): it reads fast keystrokes as a paste burst and the Enter after them as a
  line break. opencode stays typed. `sendFor` also applies `agent.ComposerText` (a
  trailing space closes codex's `@`/`$` picker, which would otherwise eat the Enter).
  Always `send-keys -l --` with a trailing `;` escaped.
- `agentChoose` sends one key, no Enter, after re-reading the pane against the
  hash the human saw. `/config`-type screens classify as waiting, so never rely
  on `Classify` alone in a composer path.
- `agentStop` needs a working pane, debounces (two Escs open Rewind), holds
  automation 30s and the human queue until the human speaks. `escIfWorking` is the one Esc.

### Server push (contract: `docs/mobile/02-events.md`)
- **Remote envelope is v2 only** (`FrameVersionMin = FrameVersionCurrent = 2`),
  the pairing frame included. `event` frames carry the `id` of the `watch`
  that started the stream (≤ 64 bytes, `[A-Za-z0-9_-]`) and a per-stream
  `seq` from 1; a client drops other ids and re-watches on a gap. An `err`
  frame on a watch id ends that stream only.
- **Every pushed frame is authorized against the live registry**, not only
  inbound frames: the pump calls `AuthorizeFrame` before each event; a
  revoked peer is shut, a peer without `read` has its streams ended.
- **Pokes are free and blind.** `d.watch.Poke*` takes only `pokeMu`, never
  calls the store or a producer, and returns at once when nothing is watched.
  `session.Store.OnChange` and `sshstore.Store.SetNotify` are the two commit
  hooks; a new writer needs no poke of its own. Never call the store from
  inside them.
- **One lock order:** producer lock (`sessionsProjector.mu`, `feedPub.mu`,
  `timelinePub.mu`) → data locks (taken and released inside compute) →
  `topicState.mu` → `Box.mu` → leaves (`pokeMu`, `watchHub.mu`, which
  also guards `probed`). Compute, then install: nothing is computed, read or executed
  under `topicState.mu`. Snapshots are built only by producer runs on the
  hub's `sem`, never on the connection reader or the pump.
- **No watcher, no work.** Topic state exists only while watched; the hub
  ticker stops at zero; the pane is probed only for a watched `talk:<id>`
  (≤ 1/s whatever the hook rate, 15 s resting, parked when gone), at most 8
  sessions daemon-wide and 4 per device.
- **A probe is exactly the desktop's talkState call**, including its pane
  reconciliation of the agent axis. A watch therefore counts as a viewer. It
  never types, and it moves the agent axis only by the pane evidence
  `talkPaneState` already trusts.
- **Feed claims may drive only display-only pokes** (diff ticks); nothing on
  the push path feeds a gate, slot, reaction or write-back.
- **Snapshot, then deltas, under one lock; a slow link degrades, never
  blocks.** `internal/eventbox` coalesces per slot, resnapshots on overflow
  at most `ResnapshotLimit` times per minute, then ends the stream. At most
  `maxWatchingConns` connections hold a box (memory bound). Nothing
  oversize is ever resnapshotted: items over `EventItemMaxBytes` are
  `omitted`, an unsplittable value ends its stream.
- Sessions events use `remoteSessionInfo` (the `handleRemote` projection).
  `age`, `headlineAgo`, `board.updatedAgo`, `prObservedAt` and `Event.ago`
  are outside the change fingerprint (`sessionsVolatile`); add any new
  clock-derived field there. The timeline topic serves only
  `handleSessionTimeline` (`publicTimeline`) output.
- `attachImage` writes only through `os.Root` of the session's
  `.lola/images` (under `$LOLA_HOME`, never a `KindLocal` directory), runs no
  command, refuses a sandboxed worktree whose gitguard pin is lost, keeps the
  upload's descriptor and verifies the linked file is that inode, is capped
  per upload, per session, per device per day and by free disk, and returns
  the desktop's token (`<worktree>/.lola/images/paste-<16 hex><ext>`).
  `attachmentImage` roots at `.lola/images` and never serves a link.

### Pane classification (`attention.Classify`)
It mirrors claude-code's rendering, so a rendering change silently disables
every send path. Re-verify against a live pane.
- Codex (`attention/codex.go`, panes in `testdata/codex`, codex-cli 0.167.2): no box.
  The composer is the LAST row starting with `›`, above a ≤2-line footer; the same caret
  marks past messages and focused list rows (`› 1. …`), and shell mode swaps it for `!`.
  Empty = cursor shown at column 2 on that row holding only the dim placeholder. Every
  codex screen that is not the composer hides the cursor. Slash commands are refused
  for codex (its picker runs the focused row on Enter); it has no cards.
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
  print before the app settles its port), bounded by `devURLAttempts`; opened via `cmd=openURL` (http(s) only) on the Mac,
  never `window.open`; a phone opens them itself under the Phones rule.
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
  admits only `hookEvent`/`agentReport` plus the plan gate's in-pane `planGate`/`planSubmit`, with an HMAC token — never widen it beyond commands acting only on the token's own session.
  `docker rm -f` on kill and before every launch. No host commands in a sandboxed
  worktree after the agent starts. Project is passed BY NAME, not inferred from the id.
  `TestSandboxContainsTheAgent` (`LOLA_SANDBOX_IT=1`).
- **Stack sessions** (`kind = "stack"`): one agent in a folder of per-app worktrees.
  Apps are chosen at spawn (`config.SelectApps`, plus members a human ticked in
  the ticket picker: `OpenTicketArgs.Apps`) and only GROW afterwards, by a
  human's `cmd=addApps` (desktop/CLI only, tier `never`; `runtime.AddApps`
  prepares each app as the spawn does, all or nothing). For a SANDBOXED session
  the agent and container are stopped BEFORE the worktree exists and relaunched
  after (mounts are fixed at launch; no host command runs in a tree a live agent
  can write), so it is refused mid-turn. The agent is told through the broadcast
  queue. An agent's "needs <name>" report never adds anything.
  PR state lives on `session.App`;
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
  `Session.Board`, the claim audit, `quota`, overlaps, best-of
  ranking are display-only. `agentfeed` is display-only; its one non-display consumer is the
  push path's diff tick, itself display-only. A report is not activity evidence.
- Observer and reconcile run on `context.WithoutCancel`, panic-guarded, with a
  deadline on every exec. Spawn is bounded by `nativeSpawnTimeout`.
- **Native Linear agent** (`linearagent.go`, `linearwebhook.go`, `linear/agent.go`+`oauth.go`):
  a TRIGGER plus a MIRROR; Linear's data is never control. `[linear_agent]` polls
  the Agent Session API as the OAuth app actor (a separate principal from the API
  key; `linear.AgentAPI`). A new `pending` session dispatches through
  `handleOpenTicket` (claim → seen → spawn → write-back; an issue already running
  BINDS via `Session.AgentSessionID`); the ledger (`state/linear-agent.json`)
  records a session BEFORE its spawn. Routing is by the issue's TEAM (matching
  `project_id` wins); no match is an error activity, never a guess. The mirror
  emits one activity per CHANGE (watermark `Session.AgentMirror`), reads
  `Session.Board` (display sink) and feeds nothing back. A human reply is the plan
  verdict while a plan is pending, else QUEUED (`PendingNotices`) and typed only via
  `flushAgentNotices` (hand-off gate + pane proof, one per prompt). `stop` is
  acknowledged, never acted on. The webhook is a DOORBELL: verified HMAC+timestamp
  only wakes the poll, payload never parsed. The HUB can ring the same doorbell for laptops with no public address (`hubdoorbell.go`: a signed long-poll on `/v1/doorbell` over a per-org counter the hub bumps after verifying Linear's HMAC against a sealed per-org secret; the counter is only compared, never interpreted; runs only with `[hub]` and `[linear_agent]` both on, fails quiet, the 15s poll is the floor). Secrets resolve by Keychain service
  name, tokens refresh on expiry/401; OAuth errors name only status + a whitelisted word.
- **Plan gate** (`plangate.go`; CLI `lola plan-gate submit|approve|reject`, not
  `lola plan`): CONTROL state `Session.PlanGate` (planning → submitted → approved),
  set at spawn from the inheritable `[[project]].require_plan` and moved ONLY by
  `plangate.go`. A synchronous `PreToolUse` hook on `Edit|Write|MultiEdit|NotebookEdit`
  → `lola hook pre_tool_use` → `cmd=planGate` denies while `Blocks()`; wired into
  EVERY claude session, fails OPEN on an unreachable daemon. Sandbox reaches it via
  the hook bridge; stack sessions are gated too. Bash writes and codex/opencode are
  briefing-only. Verdicts arrive as queued notices, never typed mid-turn.
  `planSubmit`/`planGate` are on remote's denied floor; `planDecide` is audited.
- Secrets (Linear key, Slack webhook) never in config, argv, logs or errors.
  The Linear key is write-only from UIs (keychain), never on `SettingsDTO`, never read back.
- Usage: UIs show tokens, not dollars (`~$` in tooltips only). Raw tokens displayed,
  weighted tokens budgeted, list price ranks. Rank against finished sessions only.
  Never mix agents in one ranking. `hook.StatusLine` must ALWAYS pass through to
  the user's own status-line command.

### Team hub

- **Hub work is a CLAIM and a candidate, never a command.**
  (`internal/daemon/hubwire.go`, `hub/DESIGN.md`.) A compromised hub must
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
  - Only what the protocol names leaves the machine in plaintext: repo slugs,
    validated findings, item ids, a PR URL, a delivery word, rendezvous ids and
    bearer SHA-256 hashes — never a session id, path or pane text in the clear.
    Phone traffic crosses the hub only inside the end-to-end mutual TLS session
    (see **The hub relay is blind**). `hubStatus` is a read a phone may make.
  - **The hub relay is blind** (`hubproto/ws.go`, `hubproto/relay.go`,
    `internal/hub/relay.go`, `internal/daemon/hubrelay.go`,
    `internal/remote/relay.go`, `docs/mobile/03-hub-relay.md`). Rules:
    - The relayed stream IS the phone's mutual TLS 1.3 session, terminated by
      `remote.Server` (`ServeRelayed` → `tls.Server(raw, s.tlsConfig)`) and
      by the phone. Never terminate, inspect or frame-parse it anywhere else;
      never add a relay-only command path.
    - The daemon dials out only: a standing control channel
      (`/v1/relay/daemon`) and one accept leg per phone connection
      (`/v1/relay/accept?conn=`), both signed with `hubproto.Sign`. The hub
      never dials a daemon, and its `hello` values never steer the daemon.
    - Credentials: the daemon keeps and registers HASHES only (replace-all
      `POST /v1/relay/rendezvous`, every POST serialized). A phone's rv and
      bearer reach it only inside the inner TLS (`PairResult.Relay`,
      `relayCred`); the QR carries a single-use window rv and a random window
      token (`tok`) whose hash expires with the window. Never log an rv, a
      bearer, a token or a hash.
      Leaving, `relay = false`, a hub URL change and revoking the last phone
      all post the empty set to the hub that holds the entries.
    - An `incoming` is the hub's claim: the daemon dials only for an rv it
      registered, under its own rate limit and relay pool (4, at most 2 per
      rv, separate from the LAN pool). A relayed peer must present the client
      key bound to that rv, or pair through the open window's rv.
    - `WSConn` write deadlines govern data frames only; control frames carry
      their own short deadline, and a close never waits on a blocked writer.
      `remote`'s per-frame write deadline would otherwise kill an idle relayed
      phone at the first hub ping.
    - A 401 `unauthorized` on a relay dial is a revocation like any signed
      call (converted to `hub.APIError`); a 404 means an older hub
      (`unsupported`, recheck 6 h), never an error to retry hard.
    - The relay loop never blocks shutdown: every dial and read is bound to the
      run context and closes with `CloseNow`.
    - `[remote].relay` (default on) only matters with `[remote].enabled`;
      `bind = "off"` stops listening but not the relay. A relay-only daemon
      runs a listener-less `remote.Server`, rebuilt when the hub is joined or
      left. `RemoteConfig` stays pointer-free (`RelayOff`): `handleReload`
      compares it by value.
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
      tokens` → a CEILING on `[budget].daily_tokens` (never a raise), `merge_queue_enabled` →
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
    sync, stats, and the team commands createTeam, invite, teamSettings,
    revoke) are tier `never` in `internal/remote/tiers.go` (a phone gets none of
    them, the board and org included: they carry the whole team's work and
    device names) and named by no sandbox hook-bridge allowlist. Rules:
    - **The team admin key is not the device key.** `cmd=hubCreateTeam`
      (`hub.CreateTeam`) generates it on this machine and stores it in
      `state/hub-admin-key` (0600) as a record naming its hub and team; it is
      written PENDING before the one-time create code is sent and confirmed
      after the hub answered. It is never in `config.toml`, argv, a log line
      or a socket reply (`hub.AdminKey` renders its public half only). A
      leave keeps it; creating another team replaces it only with
      `replaceAdminKey`. Its PUBLIC half is pinned in `trusted_policy_keys`
      without a question, because it was generated here, not received.
    - The team-admin commands (`internal/daemon/hubadmin.go`: `hubInvite`,
      `hubTeamSettings`, `hubRevoke`) are device-signed AND countersigned
      with that key (`hubproto.SignAdmin`, `Client.rawSigned`). `hubAdmin()`
      fails closed before anything is sent: no identity, an admin key for
      another hub or team, or a device key that did not enroll. A Linear key,
      a digest webhook and a create code go IN only: never logged, scrubbed
      from every error (`hubScrub`), answered with fixed sentences. What
      comes back is status, validated whole (`OrgSettingsView.Validate`; a
      view of another team is refused). The invite is built HERE
      (`AdminKey.Invite`) from this machine's hub url and admin key; the hub
      supplies the token only, so it never chooses the key a member pins.
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
  - **The chore lifecycle is the hub's; the daemon reports facts**
    (`hubproto` items/frames/board). An opened PR is reported with `progress`
    (claimed → `in_review`, lease dropped, this device the owner); a
    `dep_advisory` completes on MERGE, and its `HubWork` stays open so
    `hubRepoBusy` keeps one PR per repo. A hub that answers 404 to `progress`
    gets the old complete-on-PR; a hub that predates `policy_version`,
    `pr_url` or `group` gets frames without them (it decodes strictly).
    - An advisory no published version fixes yet (`hubproto.Item.AwaitsFix`:
      an empty `fixed`) WAITS: the hub does not offer it, the loop refuses it
      before the claim (`hub.Accept`) and again on GitHub's answer, and the
      board shows it under "Waiting for a fix" (`HubBoardItem.NoFix`). A
      person's claim still takes it. No state or fail reason was added for it:
      a new state fails an older daemon's board validation whole.
    - A lost lease is re-claimed while the item is still open; only another
      device holding it leaves the work lost. `lease_lost` and a 4xx without
      a hubproto code are retried, never dropped.
    - Leases renew on their own timer (≤ TTL/3), and `[hub].poll_seconds` is
      capped at 900.
    - Every hub claim passes `sandboxLoginReady`. A failed spawn sends
      `FailSpawn` and cools the item down. A dead agent and a chore older
      than the age limit send their fail once; the session is never stopped.
    - Grace, cooldown, stale close, the 14-day reopen and escalation grouping
      are hub-side; the board only displays history, owner and member.
  - **A policy stops applying when the hub stops vouching.** `no_policy` marks
    the stored bundle withdrawn (it stays the rollback floor); a
    `401 unauthorized` on a signed call marks the device revoked
    (`state/hub-revoked.json`, key kept) and drops the overlay. Skew and replay
    are not revocations. The policy budget is a CEILING like the concurrency
    cap, never a raise.
  - **Joining spends a one-time secret; leaving lets go of everything.**
    (`internal/hub/join.go`, `invite.go`, `internal/daemon/hubcmds.go`.)
    The invite (`lola-hub:<url>#<token>[#<key_id>:<pub>]`, `hub.ParseInvite`)
    or bare token is read from stdin or `--token-file` only — no argv path.
    Everything that can fail runs before the token is sent; a failure after
    the hub accepted wraps `hub.ErrTokenUsed`. An invite's policy key is pinned
    (`hub.PinTrustedKey`) only after the person confirms its id + fingerprint
    or passes `--trust-key`; `cmd=hubJoin` never pins one, and no key ever
    comes from a hub response. `cmd=hubLeave` releases every live lease first
    (bounded), then clears outbox, cooldowns and open `HubWork` so no old item
    id reaches a later hub.
  - **Readiness is one list** (`doctor.HubReadiness`): joined, projects that
    take chores, docker, image, sandbox login, `gh auth status`, policy key
    pinned. `lola hub join`/`status`, `lola doctor` and the app all read it.
    Every `doctor.Result` carries a stable `Fix` id (`doctor.AllFixIDs`); no
    row carries a secret value.

### SSH (contract: `docs/ssh-sessions.md`)
- `ssh -O check` exit 0 is the only proof of a connection; nothing read from a
  pane moves a state; no auto-reconnect. Keep `ProxyCommand=false`.
- `protocol/ssh.go`'s split is the trust boundary (`SSHAgentCommands` vs `SSHHumanCommands`; the bridge and phones get no ssh human command —
  they are tier `never`; Conversation commands, including `sshRecall`/`sshAnswer`, are
  `protocol.TalkCommands`). Production refuses
  unsandboxed agents unless `allow_unsandboxed` (`SSHAdmit`, `SSHDirectSandbox`).
- Agent commands run on exec channels, never in the human's shell. Human Run
  blocks are `Private` until `sshShareBlock`. Facts vs claims never mix.
- Prompt answers: memory only, via `tmux.PasteSecret`, never argv/log/store; stale
  cards must not type.
- Two daemon sentences are matched by the UIs ("changed since", "not waiting for
  approval"); rewording them in `sshhuman.go` changes both UIs.

### Phones (`internal/remote`, `internal/devices`; spec `docs/mobile/01-pairing-and-tiers.md`)
- **A phone is a second HUMAN surface, identified by its key.** The listener is mutual TLS 1.3
  (`RequireAnyClientCert`, P-256 only); the client SPKI hash is looked up in
  `~/.lola/devices.json`. Nothing on the wire names a device. An unknown key fails the
  handshake unless a pairing window is open. No build tag gates the listener. A client counts
  a connection as authenticated only on the `ready` frame (TLS 1.3 finishes before the server
  judges the client certificate); post-handshake refusals send `not_paired`.
- **Pairing is local, one-shot, and the human types the code.** `protocol.LocalOnlyCommands`
  (pair*, devices, revokeDevice, setDeviceTiers) are tier `never`, refused again in
  `handleRemote`, absent from the hook bridge. The secret lives 5 min and is burned only by a
  VALID proof (a wrong one closes only that connection and never moves the pairing, so a
  stranger on the LAN cannot cancel pairings). The daemon never displays the 6-digit code:
  `pairConfirm` must carry the code the human read on the phone (`maxConfirmTries`). An
  unpaired connection may send exactly one frame: `remote.pair`. The payload, secret and
  relay `tok` are never logged. The pairing connection closes after its outcome; the phone
  reconnects as a paired device.
- **`internal/remote/tiers.go` is a closed allowlist.** Every command `d.handle` routes maps to
  exactly one of read / answer / control / destructive / shell / never
  (`TestEveryHandledCommandHasATier` parses `server.go`). A new command is unreachable until a
  human writes it into a tier. Unknown = never. `pty` write is `shell`. What the tiers mean on
  the Mac: `answer` can approve an agent's permission card, which runs what the agent proposed;
  `control` starts configured commands (`dev_commands`, `post_create`) inside trees the agent
  can write; `destructive` removes sessions (`kill`, `closeExitedTemporary`, `bestOfPick`);
  `shell` is raw terminal input and every command line or program the phone or the agent
  chose (`open`, `openManual{agent:false}`, `reviewPackageVerify`, `reviewPackageCapture`).
  Turning `shell` off is not a guarantee that no code runs. Anything that runs an arbitrary
  command line, or agent-writable code chosen by the request, is `shell`, never `control`.
  Phones get the desktop's Conversation (`talkState`/`agentFeed`/`sessionTimeline` read;
  `agentSay`/`agentChoose`/`agentStop`/`agentQueueRelease`/`agentSetting`/`attachImage`
  answer) and review workspace (`reviewPackages`/`Image`/`Diff`/`Checks` read;
  `reviewPackageMark`/`Feedback` answer; `reviewPackageRecover` control; `Verify`/`Capture`
  shell).
- **Desktop-only (tier `never`):** daemon lifecycle and config (`stop`, `reload`,
  `renameProject`), pairing and devices, every hub human command, every ssh command, the
  agent's own channels (`hookEvent`, `agentReport`, `reviewPackagePublish`/`Complete`), plans,
  goals, review profiles, knowledge, comparison and `openURL`.
- **`enable`/`disable` (one poll's `enabled`) are the only config writes a phone has; no other
  config write may be tiered.**
- **Requests are rebuilt, not forwarded** (`fields.go`): only each command's listed fields
  survive; `Force` is never carried; a phone opens worktrees only and needs `shell` for a
  shell; `agentSay.Conn` and `prs.refresh` are dropped. A new field on a reachable Args type
  fails `TestRemoteArgsFieldsAreReviewed` until reviewed.
- **Same handlers, same gates.** A phone send is a human send through `typeToAgent`,
  `sanitizeAgentText` (a leading `!` is defused on every surface), `lockHumanPrompt`/
  `lockHumanMessage` and `typedNote`. Nothing new types into an agent.
- **Revocation is immediate and survives restarts.** Every frame re-reads the device (id +
  epoch); `revokeDevice` also closes its live connections and drops its relay rendezvous at
  the hub (registry subscriber → `hubRelayDirty`); re-pairing a key bumps its epoch; tier
  edits apply on the next frame and close the device's live connections, so its next
  `ready` carries the new tiers. `devices.json` is 0600 and atomic; an unparseable one
  authenticates nobody and is never rewritten. A revoke that cannot rewrite it is journaled
  in `devices.revoked`; one that cannot write either keeps the listener and relay off for the
  run and fails `lola doctor`'s `phones` row.
- **Unauthenticated peers cannot crowd out paired phones.** Slots are taken at accept in a
  pre-auth pool capped globally and per source IP (relay has its own pool, spec 03); a
  connection moves to the authenticated pool only after `Authenticate`. Per device, the OLDEST
  connection is evicted when a new one authenticates past `maxConnsPerDevice`.
- **External APIs are budgeted per device.** Commands that call `gh` or Linear on the request
  path (`extCommands`) take an extra `extRate` bucket; `tickets` is cached like `prs`.
- **SSH stays on the desktop.** Phones read it only through `sessionTimeline`, an allowlist
  copy with no Private blocks, prompt cards, host/user/cwd, handback transcript or widget
  document; `sessions` strips `SSH`. Conversation commands are `protocol.TalkCommands`
  (including `sshRecall` and `sshAnswer`, which act on any session's Conversation store; an
  answer to an element scoped to an SSH connection is refused in `handleRemote`), not ssh
  commands.
- **Widgets never run on a phone.** `sessionTimeline` drops `SSHElement.HTML`/`Data`. Adding
  them back requires the phone to render widgets only in a sandboxed `data:` iframe that the
  native bridge is not injected into (`WKUserScript` `forMainFrameOnly`), proven by a spec 04
  test, and a change to this rule.
- **A phone opens URLs itself, and only http(s)** to a `dev_forward` address or the URL's own
  host; `openURL` (which opens a browser on the Mac) is tier `never`. Untrusted pane text never
  becomes a non-http(s) open on either surface.
- Every non-read remote command writes one `remote: audit` line naming the device, never the
  payload. Rate limits are per device, shared across its connections.

### Desktop session view (design: `docs/unified-session.md`)
- One DOM, three container-size layouts (`.cv-*` in `app.css`); one
  `SessionEmbed` at two heights (CSS state, never `{#if}`).
- `lib/answerblocks.ts` builds elements as data (no `{@html}`), fails open, loses no word.
- Plates = lola witnessed it; cards = you can act on it; nothing else gets a box.
  State glyphs never appear in story rows.
- Terminal attaches only while on screen, on both surfaces; no hidden/read-only clients.
- Ended sessions stay readable 14 days (`State.Ended`). Comments anchor only on the base diff.
- **Widgets are untrusted:** `sandbox="allow-scripts"` only, `data:` documents,
  a `MessageChannel` port is the only channel, `lola.ask`/`lola.run` only fill.
  Frames never move in the DOM (`DockArea.svelte`, append-only each).
  `desktop/runtimeguard.go` + `TestRuntimeGuardWasReviewedForThisWails` must be
  re-read on every Wails upgrade. Never claim "no network" in the UI. On the phone the same
  rule rests on `LolaTransportPlugin/BridgeGuard.swift` (main frame only) +
  `capacitorguard.test.ts`, re-read on every Capacitor upgrade.

### Phone client (`mobile/`, spec: `docs/mobile/04-client.md`)
- The phone runs the desktop's own Conversation, Changes and Review components
  through a shim (`mobile/src/wailsshim/`). `wailsshim/conformance.ts` makes
  `npm run check` fail when the shim drifts from the generated bindings: a new
  bound method needs a shim route (fwd, local, platform or absent) in the same
  change.
- Tiers have one source: `remote.CommandTiers()` → `mobile/src/wire/testdata/tiers.json`
  (`LOLA_WRITE_VECTORS=1 go test ./internal/remote -run TestTierGolden`) → `wire/tiers.ts`.
  The client never spells a tier by hand; it never sends a `never` command
  (`openURL` opens on the phone).
- Desktop stores take a pushed source (`$lib/pushsource.ts`). With none
  installed they poll exactly as before; only `mobile/src/main.ts` installs one.
  A refused topic reverts that store to polling. A desktop edit that adds a poll
  must add the matching push path or say why the phone does not need it.
- Secrets never cross the Capacitor bridge: the pairing secret, the QR's
  `tok`, every relay bearer and the client key stay in Swift and the Keychain.
  Capacitor logs every resolved payload and listener event in Debug builds.
  `relayCred` and every `lola-`-prefixed request id are native-only; the
  plugin refuses them from JS.
- Widgets never run on the phone: elements arrive without `html`/`data`, and
  `SSHWidget` renders a placeholder on a client that set `setWidgetsUnavailable`
  (`$lib/widgetsoff`, only `mobile/src/main.ts` does).
- The Capacitor `bridge` handler accepts messages from the app's main frame
  only (`LolaTransportPlugin/BridgeGuard.swift`), so a widget iframe cannot
  reach a plugin. `mobile/src/native/capacitorguard.test.ts` pins the Capacitor
  version it was read against; re-read the guard on every Capacitor upgrade.
- Only an authenticated signal ends a connection: a `not_paired` frame or a
  TLS alert after the pinned handshake completed. A wrong key, a TLS failure
  before `.ready`, an mDNS hint or a hub refusal fails one candidate only.
- The client re-sends a request after a connection change only if its tier is
  `read`; everything else rejects with `ConnectionClosedError`. A migration
  waits for in-flight non-read requests (`MIGRATE_DRAIN_MS`).
- Inbox Approve sends only an exact `Yes` choice, with the hash of the request
  text it rendered; never a "don't ask again" option.
- The phone's tier list hides controls; it never grants. The daemon gate is the
  truth, and a refusal is shown as the daemon's sentence.
- Screens are reached in tests by `lola-dev://open?route=…` or
  `preview.html?route=…&fixture=…`, never by synthetic taps. A screen or sheet
  no route reaches is a bug.

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
- Pairing in a simulator or E2E run is confirmed with `lola pair` (the code line on stdin,
  taken from the simulator's `lola-dev pair-code` log line, which only the Debug
  configuration compiles: `LOLA_DEV_LINK`), never by driving the desktop UI.
  `mobile/scripts/dev-link.sh --pair` does exactly that.
- Definition of done for daemon changes: `agent-rules.md`.

## Desktop app (`desktop/`)

**Lola**: Wails 3 + Svelte 5 runes + Tailwind v4 + xterm.js. A package inside
this Go module (to reuse `internal/`), a client of the daemon socket, driving
`tmux -L lola` directly for terminals. Bound services: Talk, SSH, Daemon, Term,
Config, Context, Doctor, Linear, Update, Phones. One project form with tabs (TUI too).
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
`docs/ssh-sessions.md`, `docs/workspace-goals.md`, `docs/mobile-rethink.md` and
`docs/mobile/` (phones, push, relay, client), `mobile/README.md` (operating the phone app).
`mobile/PLAN.md` is historical.
