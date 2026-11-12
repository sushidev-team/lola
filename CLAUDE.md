# CLAUDE.md

Guidance for Claude Code working in this repository.

## What lola is

A single Go binary that watches Linear for issues matching a filter (team →
project → cycle → workflow state → labels → assignee) and spawns its own
coding-agent session per match: a git worktree, a tmux session, the agent inside.
It then observes the PR/CI via `gh` and reacts (re-prompt, notify, clean up).

- **Coding agent is pluggable**: `claude` (default) | `codex` | `opencode`, via
  `[defaults].agent` with per-`[[project]].agent` override, full lifecycle-callback parity.
- **Review is pluggable**: `[[review.provider]]` has seven kinds in three families
  (agent / cli / bot-watch).
- **Three distinct "claude"s**: (1) the per-issue coding agent; (2) lola-internal
  helpers that always shell `claude -p` (`[brain]`, `[statusagent]`); (3) a review
  provider names its own agent and follows neither.

One binary, two roles:
- `lola run` — the daemon. TUI-managed by default: the TUI spawns a detached
  `lola run` if the socket is dead; `^r`/`^x` restart/stop (restart re-execs the
  current binary). Owned by `internal/tui/daemonctl.go`. `[defaults].manage_daemon
  = false` hands lifecycle to launchd `KeepAlive` — never both.
- `lola` / `lola tui` — Bubble Tea TUI.
- Every other subcommand is a thin client over `~/.lola/lola.sock`
  (newline-delimited JSON, `internal/protocol`).

`~/.lola/config.toml` is the single source of truth; UIs edit it, then send
`reload`. The old AO bridge (`ao spawn`) is removed; leftover `Source: "ao"` /
`AOStatus` fields are back-compat. `agent-rules.md` marks changed rules **[changed
from AO bridge]**.

## Build / test

Use the Makefile — it sets repo-local `GOCACHE` (`.gocache/`) and
`GOFLAGS=-mod=mod -buildvcs=false`. Bare `go build`/`go test` fail in a sandbox.

```sh
make build | vet | test | check | tidy      # tidy = GOPROXY=off go mod tidy
GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go test ./internal/daemon -run TestDispatch -v
```

Go 1.24+ (builds under 1.26). Deps: cobra, bubbletea + lipgloss, BurntSushi/toml;
everything else stdlib + exec seams.

### Getting a change into the running daemon

The daemon runs from `$GOPATH/bin/lola` (TUI `^r`, the app's restart and a
hand-started `lola run` all resolve it from `PATH`), and never hot-reloads. So:

```sh
make build && GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go install .
```

then restart the daemon. Traps:
- `go install` writes a new inode; the old process keeps running old code. Check
  `lsof -p <pid> | awk '$4=="txt"'` against `stat -f '%i %N' $(command -v lola)`.
- An old daemon answers `unknown cmd "<x>"` for a new command, but a missing
  *derivation* fails silently. Confirm the image has it: `go tool nm $(command -v
  lola) | grep <symbol>`, or `go tool objdump -s '<caller>' ... | grep <callee>`.

## Architecture map

`internal/daemon` composes leaf packages; each leaf owns one external tool or
concern behind an **exec seam**, so tests never touch real tmux/git/gh/claude.

- `config` — `config.toml` schema, defaults, atomic 0600 save, **static**
  validation only (path/git checks belong to runtime). `Home()` honors
  `$LOLA_HOME`. Owns the `[defaults]` → `[[project]]` inheritance layer.
- `linear` — GraphQL client (`API` + `fake.go`), pagination, backoff on 429/5xx;
  IDs are UUIDs passed as variables.
- `runtime` (`Native`) — `Spawn` (worktree → symlinks → `post_create` → tmux agent),
  `Adopt` (re-adopt survivors; reports zombies, never kills), `Kill`.
- `worktree` — worktrees under `~/.lola/worktrees/<project>/<session>/`. `Remove`
  refuses dirty (`ErrDirty`) unless forced, guards the main checkout;
  `DeleteBranch` prunes stale registrations first.
- `tmux` — adapter on lola's own server (`tmux -L lola`); targets use `=` exact match.
- `hook` + hidden `lola hook <event>` — Claude Code lifecycle hooks → daemon.
  `hook.SettingsJSON` writes per-session `--settings`. On the agent's critical
  path: bounded 2s, always exits 0.
- `scm` — PR/CI **facts** via `gh`; must never import `state`.
- `state` — the status vocabulary: `AgentState` × `DeliveryState` axes,
  `DeriveDelivery` (UNKNOWN-mergeable hysteresis), and in `display.go` every
  consumer table (`DisplayFor`, `Attention`, `HoldsSlot`, `Present`, `SortRank`,
  `KanbanKeyFor`). `Rollup` is the legacy wire shim; `Notable` the one
  string-keyed classifier left.
- `session` — pure data: `Session` + JSON `Store` (atomic). Rollup `Status` is
  written only by `SetAgentState`/`SetDelivery`.
- `agent` — coding-agent leaf (stdlib + regexp; must NOT import
  config/session/hook/runtime/attention): kind enum, `Caps` (feed, send, stop,
  cards, cursor, paste — claude only), `LaunchArgs`, `ReviewArgs`/
  `ReviewStreamArgs`, callback configs, `ParseCodexNotify`.
  `config.AgentForProject` resolves project → defaults → `claude`.
- `reviewagent` — agent-family review pass (read-only, headless), same signature
  as `review.Client`. Not `brain`: its findings may reach the worker; brain's never.
- `review` / `brain` / `attention` / `doctor` / `secrets` / `notify` — CodeRabbit
  QA pass, opt-in summarizer, pane→question parser (agent-aware), health checks,
  Linear key (keychain→env), best-effort notify.
- `reviewmd` — renders findings to GitHub Markdown (`Render`, `RenderInline`).
- `diffanchor` — which `(path, line)` of a diff can carry an inline comment
  (RIGHT side, `Nearest`). Fails toward fewer anchors.
- `gitdiff` — worktree vs merge-base (`origin/<base>` first), incl. untracked,
  capped per file; `Between` for checkpoints. Behind `cmd=diff`.
- `checkpoint` — per-turn snapshots on `refs/lola/checkpoints/<session>/<seq>`.
- `gitrepo` — local git only: `Detect` (owner/name from upstream then origin),
  `Branches`, `Inspect`. **Fails closed** (zero value on any unknown).
- `gitguard` — pins host git for sandboxed worktrees (see sandbox invariant).
- `board` — `lola report <verb>` vocabulary + `Apply`; parsed daemon-side.
- `claimaudit` — scans Claude's JSONL for test/lint/build runs vs board claims.
- `planner` — `lola plan` decomposition + `Validate` (steps depend only on earlier
  steps → acyclic). Never touches Linear.
- `bestof` — mix vocabulary `<agent>[@<model>][:<count>]` (`ParseMix`) + `Suggest`.
- `statusagent` — opt-in `claude -p` status interpreter; display-only.
- `agentfeed` — Conversation reader over the agent's log; display-only.
- `devtab` / `sshtab` / `lolaenv` — naming conventions as stdlib leaves
  (`<id>-dev-<n>`, `<id>-ssh-N`) shared by daemon, runtime and UIs.
- `proctree` — the one place lola signals processes it did not start: `ps` +
  whole-group termination (TERM → grace → KILL → settle).
- `devurl` — ranks loopback http(s) URLs a dev command printed, by cue text.
- `portclash` — reads a dead dev tab for "port in use"; returns only a port int.
- `portproc` — lsof: listening ports + owner cwd. Fails open.
- `portlease` — per-session port blocks (`~/.lola/state/ports.json`).
- `claudetrust` — the one writer of Claude's global config: per-worktree
  `hasTrustDialogAccepted` (`trustAgentDir`/`untrustAgentDir`). Touches only that
  key, atomic, never rewrites an unparseable file, advisory. Off with
  `[defaults].trust_worktrees = false`; skips sandboxed and non-lola folders.
- `usage` / `quota` / `sysload` — token usage (claude/codex/opencode scanners,
  `Ledger` in `~/.lola/state/usage.json`), subscription limits, load average +
  free memory (-1 = unknown).
- `sshconn` / `sshstore` / `sshelement` / `sshterm` — ssh argv + exec, the
  session's ssh record under `~/.lola/state/ssh/<id>/`, the agent's claim
  vocabulary, shell bootstrap/marker parsing/`DetectPrompt`.
- `tui` — TUI + the socket client (`Send`/`Logs`) the CLI reuses.
- `main.go` — cobra wiring only.

### Daemon files (`internal/daemon`)

- `daemon.go` — `Daemon` struct; every `func(...)` field is a test seam.
- `dispatch.go` — tick: health gate → resolve key/cycle → query → drop
  in-flight/dedup → sort → `Budget(pollCap − projectLive, globalCap, liveCounted)`
  → reserve slots → **mark in-flight + seen, then spawn** → (label mode) flip labels.
- `observer.go` — ~30s read-only loop: one `tmux ls`, gh facts → delivery axis
  (failed fetches → `PRStale`), pane classification → agent axis
  (`agentReconcile`). `sessions` serves the cache; clients never cause execs.
  Has the anti-false-working guard (`staleWorkingThreshold`).
- `reactions.go` (P3, `ensurePromptVerified`), `writeback.go` (P4),
  `reconcile.go` (~5m orphan revert), `state.go` (seen + in-flight),
  `statusagentwire.go`, `dev.go`, `overlap.go` (`Session.Overlaps`, display-only), `mergequeue.go`,
  `bestof.go` (`cmd=bestOf`/`bestOfCompare`/`bestOfPick`, `spawnIssue`),
  `hookbridge.go`, `checkpoints.go`, `apps.go` (`observeApps`/`applyApps`,
  `devPlan`, `withLanes`) / `stackgit.go` (stacks), `talk*.go`/`timeline.go`
  (`agentSay`, `agentStop`, `agentChoose`, `talkState`, `endedSessions`;
  human-side only, `SSHHumanCommands`).

## Invariants (read before changing daemon code)

### Config

- **A `Project` field holds the RESOLVED value; `Inherits` says where it came
  from.** Inheritable keys: `match_labels`, `match_mode`, `on_sent_set_label`,
  `blocked_label_id`, `dedup_mode`, `priority_sort`, `symlinks`, `post_create`,
  `env`. `Load` resolves them into the plain field and records the source in
  `config.ProjectInherits`.
  - `Save` writes a key only when overridden. Mutating `p.MatchLabels` without
    clearing `p.Inherits.MatchLabels` silently discards the write.
  - Bitmap zero value = fully explicit. Never flip that polarity.
  - `fileProject` uses pointers: absent = inherit, `[]` = override to nothing.
  - `ResolveInheritance` is idempotent; `Load`/`Validate`/`Save` all call it.
  - `agent`, `concurrency_cap`, `branch_prefix` resolve at read time
    (`AgentForProject`, `EffectiveCap`, `BranchPrefixForProject`), not in the bitmap.
  - Never inheritable (per-repo): `group`, `dev_commands`, `port_range`,
    `ports_per_session`, `pre_remove`.
- **`Name` is identity, `Label` is display.** `Name` is a path segment, the
  session-id prefix and the tmux name. Render `p.DisplayName()` /
  `cfg.DisplayNameFor(id)`, never bare `p.Name`. `config.Slug` is the one
  label→id path (`SlugTyping` for live typing). Slug shape is a UI rule, not
  validation; the TUI re-slugs only a human-typed name (`idEdited`). Renames go
  through `cmd=renameProject`, idle-only — do not extend to live sessions.
- **The project panel arrangement is display-only.** `[[project]].group`, the
  `[[project]]` array order and `[[group]].position`. Nothing in
  daemon/runtime/dispatch reads them.
  - A dangling `group` is repaired to "" with a notice (`sanitizeGroups`), never rejected.
  - `ConfigService.SetProjectLayout` fails closed unless the payload is an exact
    permutation of configured projects and groups.
  - The project form has no group field; filing is by drag or `⌥↑/⌥↓/⌥→/⌥←`.
    `SaveProject` leaves `Project.Group` untouched.
  - Drag uses pointer events, not HTML5 DnD; all decisions live in DOM-free
    `desktop/frontend/src/lib/sidebarlayout.ts` (`buildRows`/`toLayout` round-trip).
- **New project starts at the folder; derived fields are fill-only.** Picker
  (`ConfigService.PickFolder` / `internal/tui/dirpicker.go`) → one
  `gitrepo.Inspect` fills `path` (checkout root), `label`/`name`
  (`config.LabelFromPath` → `Slug`), `repo`, `default_branch`.
  - Never overwrite a human value — only empty or self-filled
    (`repoAuto`/`branchAuto`); typing clears those flags.
  - Identity is suggested for NEW projects only.
  - Drop an answer whose path no longer matches the form.
  - On open, inspect without filling (an untouched form must not be dirty).
- **`[defaults]` label keys must be workspace labels — a UI rule, not
  validation.** UIs offer only `linear.WorkspaceLabels`. Do not add a
  team-scope check to `Validate`.

### Dispatch and slots

- **Health-gate every dispatch.** Missing `tmux`/`git`/agent binary or an
  unresolvable project: skip the tick, set `lastError`, mutate nothing.
- **Dispatch order is load-bearing.** In-flight + seen before spawn; upsert the
  session immediately so the next `Budget` counts it.
- **Spend and load HOLD dispatch, never touch a session.** `dispatchHold`
  (`usage.go`) has the health-gate contract (`dispatch held: <why>`).
  - UIs show tokens, not dollars; list-price `~$` only in tooltips. Absent usage
    = unknown → blank, never `0`. Budgets display as a percentage.
  - Three measures: raw tokens (displayed), weighted tokens (`Totals.Weighted`,
    what budgets count), list-price `CostUSD` (ranking only).
  - Codex: date-keyed logs attributed by `session_meta` cwd; logs outside
    `~/.lola/worktrees` are never read past line one; no price → ranked on its
    own `Scale`. Opencode: `sqlite3 -readonly`, query on stdin, worktree
    sessions only, numbers only, messages replaced by id. Never rank agents together.
  - Rank only against finished sessions. Ledger replaces each (day, source) total.
  - Hook `TranscriptPath` trusted only directly under the claude projects root.
  - `[brain]`/`[statusagent]` run with cwd `~/.lola/helpers` (`helperSource`).
  - Unknown load holds nothing (fail open).
  - Subscription limits are display-only. `hook.SettingsJSON` owns every lola
    claude session's status line, and `hook.StatusLine` must ALWAYS pass through
    to the user's own command (local → project → user settings, same stdin,
    bounded, group killed), never to itself.
- **`state.HoldsSlot(agent, delivery)` is the one slot classification;
  `liveCounted` = store snapshot + slot ledger.** A session holds a slot while
  its agent is alive (not dead/exited/shell/orphaned) AND its PR is neither
  parked on a human (`review_pending`, `approved`) nor terminal. A live pre-PR
  agent holds a slot whatever it is doing, idle included — otherwise an idle
  session holds nothing forever and dispatch spawns past the cap.
- **`concurrency_cap` bounds what a project runs.** The tick subtracts the
  project's own slot holders (a stack's under the stack name). Every spawn first
  takes `reserveSlots` (`slots.go`), checking both caps against live + in-flight
  spawns under one lock. Best-of/goal attempts use `noProjectCap`. Human opens
  (ticket, PR, manual, fork, revive) check no cap but do count.
- **Dependencies hold dispatch, never write.** `dependencyHold` skips an issue
  with an open "blocked by" (not completed/canceled and not MERGED in the store)
  or open sub-issues. No seen entry, so it re-qualifies when blockers land.
- **Best-of: N sessions on one issue; only the pick ends it** (`bestof.go`).
  Attempts get `-b<k>` suffixes (`runtime.SpawnWith`; `issueFromSessionID` strips
  it like `-r<n>`) and carry `BestOf/Attempt/Attempts/Model` (model args prepended,
  repeated by revive).
  - Every attempt counts: dispatch charges `len(mix)` and spawns only if the whole
    group fits; `cmd=bestOf` refuses past `global_cap`; both honor `dispatchHold`;
    every distinct agent passes the health gate before any mutation.
  - The issue is deduped once (one claim, one seen, one label flip — its
    `RemovedLabels` recorded on every attempt — one write-back).
  - `InBestOfRace` holds PR-open write-back until the pick.
  - Pick stamps `BestOfWinner` FIRST, readies the kept PR, closes the others
    (branch kept), tears losers down via dirty-safe `runtime.Kill` without freeing
    the claim or calling `clearLabelDispatch` (which also bails while a sibling exists).
  - Reconcile checks every recorded branch (`nativeSessionsForIssue`).
  - Compare-view tallies (`ReviewTallies`) and ★ (`bestof.Suggest`) are display-only.
- **A plan is created only by a human's `--apply`, and is placed, not routed.**
  `cmd=planApply` re-validates and creates sub-issues placed as the inverse of the
  poll filter (`subIssuePlacement` mirrors `linear.BuildIssueFilter` — change them
  together). Normal ticks pick them up. A parent with any sub-issue is refused;
  partial creation is reported by identifier.
- **A dead filter reference is reported.** A zero-match tick resolves the poll's
  labels/states/`on_sent_set_label` (`linear.FilterRefs`, cached `refTTL`) and
  reports dead ones as `LastError` (`filterrefs.go`). Fails open; never changes dispatch.
- **Fail closed on unknowns.** Reconcile's orphan revert skips when the open-PR
  check cannot answer.

### Status axes

- **Two axes, never collapsed for display.** `AgentState` (hooks + pane +
  transcript + tmux) and `DeliveryState` (gh facts). Writing `Status` directly is a bug.
  - Primary pill = agent axis via `state.DisplayFor`: working | idle | needs_you
    | gone | shell | orphaned (`starting`→working, `exited`/`dead`→gone; unknown →
    working, like `KanbanFallbackKey`). PR facts never mask it. Secondary chip = delivery axis.
  - "Needs a human" is the predicate `state.Attention` over both, not a state.
    (The old single word was ~90% the agent's own 60s idle nudge and flapped.)
  - `Rollup` survives only for `protocol.SessionInfo.status`: `mobile/` reads the
    historical 16 words via the shared `desktop/frontend/src/lib`. Do not delete
    it. Nothing classifies by it except `events.go`'s `state.Notable` on recorded
    transitions.
  - `state.FromLegacy` backfills pre-axis snapshots (hence `handoffDeliverable`
    still admits `AgentWaitingInput + InputIdleNotify`).
  - `theme.ts` mirrors it; `desktop/state_parity_test.go` pins `AllDisplays`,
    `AllStatuses` and kanban keys/titles but NOT column membership — re-read the
    other port of `KanbanKeyFor` when touching either.

### Typing into an agent

- **Send-keys safety.** Typing mid-turn corrupts the agent. Every typing path
  goes through `typeToAgent` (never `d.sendKeys` directly), behind an idle gate
  consumed atomically via `Store.Update`; non-idle → defer, never force.
  Payloads sanitized, never executed. Adopted gates are unverified
  (`AtPromptVerified=false`) and must pass `ensurePromptVerified`; ambiguity defers.
  - Review hand-off gate `handoffDeliverable` = `AtPrompt` OR idle notification
    (`AgentWaitingInput` + `InputIdleNotify`) OR observer-parked `AgentIdle`.
    `InputPermission` stays excluded.
  - That gate is only a candidate filter: `handoffPromptProof` → `paneWaitingNow`
    must capture the pane and see `ActivityWaiting` before one byte is typed —
    never short-circuit on `AtPromptVerified` (Claude can raise a modal after Stop).
  - Stop hook flushes immediately (`flushHandoffsOnStop`, async, drain-group).
    `flushReviewHandoffs` stops after ONE delivery per pass. Delivery sets
    `AgentWorking`, closing the gate.
  - `cmd=resolveConflict` uses the same gate + proof but REFUSES instead of
    deferring, ignores `LastReactedStatus` as entry but stamps it, and sends
    lola's own text naming `default_branch` (a merge).
- **`attention.Classify` mirrors Claude Code's rendering — re-verify against a
  live pane.** A missed detail silently disables every send path.
  - The caret is padded with U+00A0; `stripANSI` folds Unicode `Zs` to space.
  - The composer is drawn even mid-turn. Only the live status line discriminates
    (`✻ Harmonizing… (5m 58s · …)` vs finished `✻ Cogitated for 24m 46s` →
    `hasLiveWorkingCue`, checked before the
    waiting cue). Weak cues (frozen meter, spinner, codex `Working 4m`) lose to a
    resting prompt.
  - The frame is plain `─` rules; `boxBorderRe` never fires for claude.
- **A modal is not a prompt.** `ActivityBlocked` is returned before every other
  cue, keyed on the full-width `▔` rule (`modalOverlayRe`) — not `Esc to cancel`,
  which AskUserQuestion also shows. `agentReconcile` maps it to
  `AgentWaitingInput` + `InputDialog`. Never add a Blocked case that types.
  Prevention: `modalSkills` writes `skillOverrides: {"auto-mode-setup": "off"}`
  into per-session settings. Keep both halves.
- **Human vs machine sends take different locks; both prove.**
  `lockHumanPrompt` (a press now) vs `lockAutomationPrompt` (anything queued).
  Both hold `sendGate` through proof and delivery and demand `ActivityWaiting` +
  `ComposerEmptyAt` (claude reads the cursor). `typeToAgentLocked` takes a
  `typedNote` so every send leaves a row. Claude messages > 800 bytes go via
  `load-buffer` + `paste-buffer -p` (`send-keys -l` breaks ~1 KB, fails 16 KB
  silently); other agents stay `SendKeysTyped` (`agent.Caps.Paste`, `sendFor`).
  Typed text is `send-keys -l --` with a trailing `;` escaped.
- **A choice key is one key, no Enter, pinned to a hash of the lines a human
  read.** `agentChoose` re-reads the pane under the gate before pressing. The
  card clears on pane re-read (denials fire no hook). `/config`-type screens
  classify as waiting, so composers must not rely on `Classify` alone.
- **Stop means stop, for a while.** `agentStop` needs a working pane on a fresh
  capture and debounces (two Escs at rest open Rewind). Holds the human queue
  until the human speaks and automation for 30s; reconciles the axis itself.
  `escIfWorking` is the one Esc (taking the wheel uses it with only the automation
  hold). Stop & send keeps the hold if the message didn't go; a vim composer left
  INSERT → message stays queued.

### tmux and terminals

- **Two scrollbacks; `tmux.ScrollPane` picks.** Full-screen agents use the
  alternate screen (no tmux history): if the pane is in mode or
  `#{mouse_any_flag}` is set, send an SGR wheel sequence via `send-keys -l`;
  otherwise `copy-mode -e` + `send-keys -X scroll-up`. Unanswerable → copy mode.
  - Neither needs `[tmux].mouse`. App (`LiveTerminal.svelte` →
    `TermService.Scroll`) and TUI (`forwardWheel`) both call it; never re-derive routing.
  - The app's wheel handler always returns false (xterm's fallback turns wheel
    into arrow keys).
  - Copy mode persists: `TermService.Write` exits it before typing, `Detach` on
    teardown, `tmux.SendKeys` before every daemon send. `capture-pane` is unaffected.
- **Shift+Enter is a line break (`ESC CR`), encoded by each surface.**
  `LiveTerminal.svelte` writes `\x1b\r` and swallows the event;
  `internal/tui/terminal.go` emits it on `tea.ModShift` (reachable only with a
  CSI-u outer terminal).
- **`(*tmux.Client).ConfigureServer` is the one place for global (`-g`) options.**
  `NewSession` calls it, and again after create if the first failed (cold server).
  `desktop/termsvc.go` `Shell` repeats this. `history-limit` applies at pane
  creation, so every session must agree; every session-creating caller builds its
  client via `config.TmuxClient`. `mouse` is written in both states. Verify a new
  option against real tmux (dropped options like `alternate-scroll` spam advisories).

### Teardown and dev servers

- **Teardown is three things** (`runtime.Kill`):
  1. The agent's tmux session AND aux sessions (`-shell-N`, `-review`, `-dev-N`,
     `-ssh-N`). Callers run `sshCloseSession` first (`ssh -O exit` before the tab).
     Every session goes through `tmux.KillSessionTree` (pane group + every group in
     its ppid tree, one shared grace window; degrades to `KillSession`). Aux sweep is
     best-effort. Match `parent + ^(-shell-\d+|-review|-dev-\d+|-ssh-\d+)$`,
     anchored both ends.
  2. The worktree, dirty-safe. Dirty keeps checkout AND branch.
  3. The local branch only if `Session.OwnsBranch()`. A missing worktree still
     deletes the branch (`worktree.DeleteBranch`).
  - `pre_remove` runs between tmux teardown and removal, only on a clean (or
    forced) worktree; bounded (`preRemoveTimeout`), each command in its own group,
    best-effort, then removal is forced. Skipped for sandboxed sessions.
- **The ACTIVE dev session is derived from tmux, never remembered** (`dev.go`).
  One session per project runs `dev_commands`; `cmd=dev` is a MOVE that kills the
  previous holder's tabs first.
  - `DevActive`/`DevTabs` are a cache; `reconcileDevTabs` overwrites them each cycle.
  - `lolaenv.CommandLine` `exec`s only a simple command (conservative
    `commandSeparators`/`shellWordBreakers`); `exec cd x && y` silently does nothing.
  - Tabs have `remain-on-exit`; liveness is `#{pane_dead}` (`tmux.DeadPanes`). A
    failed probe changes nothing.
  - Stopping lists tabs (not config count) and uses `KillSessionTree`.
  - Activation also runs `sweepPortSquatters`: anything listening with cwd under
    `~/.lola/worktrees/<project>/` (via `portproc`) is killed — never the project's
    own checkout, never a group owning a live tmux pane or the tmux server. Fails
    closed without lsof/ps/pane list.
  - Dev URLs: `scanDevURLs` (observer fallback, bounded by `devURLAttempts`) and
    `startDevURLWatch` (fast poll on the cancellable context, stops at first URL or
    tab change). `markDev` clears URLs on any change. Opened via `cmd=openURL`
    (http(s) only), never `window.open`.
  - Port clash (`devclash.go`, `cmd=devFreePort`): read a dead tab once per death
    (`devClashChecked`) with `portclash`; lsof only if a port was named; unresolved
    holder records nothing. The kill re-verifies session + port + pid still holding
    it, refuses groups owning a live pane, no ps/tmux = no kill. `markDev` drops it.
  - **`port_range` makes it multi-active.** Each session leases a block
    (`portlease`, in `runtime.sessionProject`) exposed as `LOLA_PORT` /
    `LOLA_PORT_1..N` via `p.Env` (`withSessionEnv`); `LOLA_SESSION_SLUG` and
    `LOLA_DB_NAME` (`runtime.DBName`) always. lola's values beat `[project.env]`.
    Registry keyed by session id, `Acquire` idempotent. Lease best-effort at spawn,
    required at activation (`handleDev` refuses). Multi-active takes over nothing
    and sweeps only its own worktree. Released on worktree removal and spawn
    rollback; `pruneLeases` (reconcile, `orphanTimeout` grace) catches the rest.

### Review

- **A visible review pass runs in `<sessionID>-review`; the pane is display only**
  (`reviewvisible.go`). The hidden `lola review-run` child writes findings +
  outcome to `~/.lola/cache/review/<session>/` (`reviewrun`); `Status.Err()` maps
  back to the same `Err*` sentinels as a direct exec. Claude uses
  `--output-format stream-json --verbose` rendered by `reviewagent/stream.go`.
  The pane holds after the pass; `runtime.IsAuxSession` covers it, Adopt drops it
  only when its parent is live. Everything degrades to direct exec.
- **A review pass never runs on the observe loop.** `queueReviewProviders` queues;
  `reviewworker.go` drains one at a time. Agent-family default timeout is 900s
  (`DefaultClaudeReviewTimeoutSeconds`). The per-PR guard is stamped before exec;
  non-answers (timeout/quota/unavailable) release it via `noteReviewOutcome` up to
  `reviewMaxAttempts`. Findings/clean/auth/exit-error are final.
- **A review reports, never writes — enforced by argv** (`agent.ReviewArgs`):
  claude headless defaults, codex `--sandbox read-only`, opencode without `--auto`.
  The prompt is positional and LAST for codex/opencode; the diff goes on stdin,
  never argv. Output reaches the child as pipes, never a TTY.
- **Every review vendor is a config slot.** Kinds: agent
  (`claude-session`|`codex-session`|`opencode-session`), cli
  (`coderabbit-cli`|`custom-cli`), watch (`coderabbit-watch`|`bot-watch`). One
  provider per kind.
  - Dispatch switches on family (`config.ReviewAgentFor`/`IsCLIKind`/
    `IsWatchKind`); per-kind exec seams live in `d.passRuns`. The daemon may name
    only `coderabbit-cli`, `coderabbit-watch`, `kindClaudeSession`.
  - `applyKindDefaults` runs before explicit keys. `bot-watch` has no author
    default; enabled without `author` (or `custom-cli` without `command`) is a
    validation error, and `setReviewProvidersLocked` disables it (never silently
    run CodeRabbit). Unknown kinds fail closed too.
  - Labels per provider (`labelsFor`); author sanitized via `botDisplayName`.
  - `neutralizeWatchedBots` defuses `@coderabbit*` always plus every enabled
    watch author, on every body.
  - Both UIs generate editors from `config.ReviewProviderKinds()`.
- **stderr is narration: classifiers read its TAIL, only on failure.** Same three
  rules in both `review` and `reviewagent` (independent copies — fix both): quota
  scan of stderr only on process failure; stderr kept by a `tailBuffer` (stdout
  keeps head); auth cues are phrases, never bare `auth`/`login`.
- **The review FORMAT block is a contract with `reviewmd`.** Prompt asks for
  `**Grade:** impact=… confidence=… effort=…`, `**Gist:**`, `**Fix:**`,
  `**Detail:**`. Renderer: Gist, Fix, `<kbd>` grade chips in one blockquote
  (every line `>`-prefixed), Detail and unknown fields in a nested `<details>`.
  Grades whitelisted (`gradeVocab`, order `gradeOrder`). Pinned by
  `TestReviewInstructionPinsTheGradedShape`. Fold is presentation only; worker,
  notify and Linear get raw text. Unparseable bodies post verbatim under `###`.
  Links only for `owner/name` repo, URL-safe ref, plain `path:line`. Summary
  HTML-escaped with `<code>` rebuilt. Self-bounded to `reviewmd.MaxBytes` (15KB)
  under `scm.postCommentMaxBytes` (16KB); drops detail before findings.
- **The github sink is the only one that reshapes findings.** GitHub strips
  CSS/style, so the design uses only alert callouts (`[!CAUTION]`/`[!WARNING]`/
  `[!NOTE]`, from worst severity), emoji, bold, code, links, `<details>`.
  `postGithubSink` is its caller. Don't style `<details>`.
- **Inline shape = plain comment + anchors** (`github_inline = true`,
  `reviewinline.go`). One `event: COMMENT` review, one thread per finding.
  - Atomic endpoint: fetch `scm.PRReviewTarget` first; anchor only what
    `diffanchor` confirms; the rest stays in the summary.
  - Degrades to `postGithubSink` on nil seams, unreadable diff, nothing
    anchorable, 403/422. Transient gh failure: leave the settle guard unstamped and retry inline.
  - Snap up to `inlineAnchorWindow` (3) lines; a snapped thread states the real location.
  - Summary tally counts every finding and how many weren't anchored.
  - `neutralizeBotTriggers` on every body.
- **Closing threads is part of the pipeline.** Every feedback hand-off ends with
  lola's own instruction (fix, commit, push, then reply + resolve addressed
  threads), appended after untrusted text, derived from repo slug + PR only.
  `inlineThreadNote` asserts threads exist (only when
  `InlineReviewPRs[kind] == PR.Number`); `prThreadNote` is conditional (watch
  bots, inline fallback, `changes_requested`). Both share `threadWorkflow`.
  `ci_failed`/`merge_conflict` stay silent.
- **A forced review releases every per-PR one-shot.** `cmd=review` bypasses
  `ReviewedPRs[kind]` via `runReviewChain`, but `PostedGitHubPRs[kind]` must be
  released too: `handleReviewProvider` calls `unstampGithubSettled` before exec,
  else the sink no-ops. Keep
  `InlineReviewPRs[kind]`. Releases only clear guards pointing at that PR number.
- **A review becomes a requirable check via a commit status** (`reviewgate.go`,
  `github_status`), since GitHub refuses REQUEST_CHANGES on the operator's own PR.
  `lola/review/<kind>`: pending → success / failure (blocker, major, unparseable
  severity) / error (didn't run, gave up).
  - New head → re-review once the agent isn't working (`headReviewDue`); releases the settle guard.
  - Rounds capped per PR (`reviewGateRounds`), then one `error` naming `lola
    review`. Forced reviews uncapped.
  - Lola's own texts only; `SetCommitStatus` refuses contexts outside `lola/`.
  - Verify clean checkout + HEAD == PR head before and after a gated pass; never
    publish success for unreviewed code.
  - Merge queue reads `reviewGatesOpen`. `ReviewGates` is control state.

### Reactions, CI, write-back

- **Reactions dispatch off the delivery axis, never the rollup.** Order: merged →
  dead pane → ci_failed → merge_conflict → changes_requested → approved → closed;
  each re-checks under the store lock. `resetReactionGuards` has no agent-axis
  special case. The dead-pane check is load-bearing (`SetAgentState` does not close
  `AtPrompt` when a pane dies); `merged` stays above it. `reactingLabel` reads the same axis.
- **CI = required checks; lola's statuses are never CI** (`requiredchecks.go`,
  `scm.ChecksStateAmong`). Re-judge `scm.PR.Checks` against base-branch active
  rulesets (`RequiredChecks`, cached). Unknown → stricter all-checks verdict;
  unreported required = pending; classic protection not read; prompt narrowed via
  `FailingChecksAmong`; contexts under `scm.LolaStatusPrefix` excluded.
- **Fire once per transition**: `LastReactedStatus`, `WB*Done`, per-PR review guards.
- **Merge queue** (`[merge_queue]`, `mergequeue.go`): per repo only the head (oldest approved,
  non-failing lola PR) acts — sync via `typeAtRestingPrompt` if behind, else merge
  with `--match-head-commit` after a fresh re-read. Unknowns hold.
  `MergeQueueGuard` is one-shot per head commit.
- **Diff-viewer feedback defers, never refuses or forces** (`cmd=feedback`,
  `feedback.go`). Kept apart from `PendingHandoffs` (keyed by provider kind).
  Appended to `Session.PendingFeedback`, delivered via the hand-off gate + pane
  proof by request, Stop hook or observer (before provider stashes; counts as the
  pass's one delivery). Failed send re-queues. `cmd=broadcast` uses
  `PendingBroadcast` the same way, flushed after feedback.

### Checkpoints, sandbox, stacks, context

- **A checkpoint is a ref, never a branch commit; recording never touches a real
  index.** Only restore (own index) and fork `Apply` (new worktree) write one.
  Stop hook records per turn (`recordCheckpointAsync`); the runtime records the
  `start` baseline before a new session's first launch (`recordBaseline`; spawn
  and fork only); `user_prompt` records one if missing.
  - `Snapshot`: temporary `GIT_INDEX_FILE` from a copy → `write-tree` +
    `commit-tree --no-gpg-sign` (fixed identity) + `update-ref`. Not `git stash create`.
  - Refs in the common store, session id in the name (`validSession`); the wire
    names only `Seq`. New worktree prunes stale refs; teardown prunes after removal,
    keeps them for a kept dirty worktree.
  - `Restore` records a "before restore" checkpoint first, `read-tree --reset -u`,
    resets the index to HEAD; HEAD never moves. Refuses `ErrWouldClobber` for
    untracked-ignored paths.
  - Restore holds `sendGate` exclusively (every `typeToAgent` holds it shared),
    re-reads the axis, needs a resting pane for a live agent. Queued sends wait.
    `ckptLock` serializes checkpoint ops.
  - `.lola/` excluded.
  - Fork (`runtime.ForkAgent`) uses `worktree.CreateAt` at the checkpoint HEAD,
    lays the tree as uncommitted work, inherits `ContextKey`; manual kind, no `Issue`.
- **Sandbox isolation is the mount list** (`[[project]].sandbox = "docker"`,
  `runtime/sandbox.go`). `launchLine` wraps the launch in `docker run -it` in the
  same pane. Project passed BY NAME (session ids are ambiguous).
  - Mounts: worktree, each repo's git common dir with `config` and `hooks`
    re-mounted READ-ONLY (never drop), claude transcript slug dir, Linux lola (ro),
    symlink targets (ro). Sources come from main checkouts, never the worktree's
    `.git` pointer; context folder only if it still points inside `~/.lola/context/<project>/`.
  - Host git never follows a sandboxed worktree's metadata: `gitguard` pins admin
    dir + store at creation (`Session.GitPin`/`App.GitPin`); every host git exec
    (`checkpoint`, `gitdiff`, `worktree`, `reviewagent`) goes through
    `gitguard.Apply`. `config.worktree` is ro in the container; pinned `worktree
    remove` uses `--force` after its own dirty check. Unpinned sandboxed records
    are `gitguard.Deny`'d (`syncGitPins`). New host git calls must use
    `gitguard.Apply`. `TestSandboxContainsTheAgent` (`LOLA_SANDBOX_IT=1`).
  - Hooks dial `LOLA_HOOK_ADDR` (unix sockets don't survive VM bind mounts).
    `hookbridge.go` admits only `hookEvent`/`agentReport` with
    `hook.TokenFor(secret, req.Session)` (HMAC, `~/.lola/state/hook-secret`).
    Never widen the allowlist.
  - `runtime.Kill` runs `docker rm -f <id>`; every launch line starts with it.
  - Health gate checks the docker CLI (`sandboxedBin`). `pre_remove` and any host
    command in a sandboxed worktree after launch are escapes — skip them.
- **A STACK session is one agent in a folder; PR concerns run per app on a lane.**
  `kind = "stack"` has no checkout; members are projects with `stack = "<name>"`
  (`config/stack.go`). Session folder `~/.lola/worktrees/<stack>/<id>/` holds one
  worktree per app (`runtime/stack.go`).
  - Apps chosen once at spawn (`config.SelectApps`); no apps → skip without seen.
    Afterwards the folders are the truth (`stackPresent`, `discoverApps`).
  - Each app is set up as its own project with its own lease (`LeaseKey`,
    `session.BaseID`) and slug/DB `<id>-<app>`.
  - PR state lives on `session.App`; act through a LANE VIEW `<id>~<app>`
    (`Store.Get`/`Store.Update` fold PR fields into the app, everything else into
    the session). Never write lane fields on the session record; never consume a
    lane gate without the session's `sendGate`.
  - Session PR fields are a derived aggregate (`Reaggregate`,
    `state.AggregateDelivery`, `FocusApp`) for write-back/slots/list/cleanup only.
    Untouched apps (`App.Touched`) don't count.
  - Text to the agent about one app starts with `laneHeader`.
  - Teardown (`removeStack`): each app dirty-safe; no-PR app with commits
    (`worktree.Ahead`) kept; any refusal keeps the folder. Merged cleanup waits for `AppsSettled`.
  - Local-git views present one tree with `<app>/<path>`; checkpoints
    `RecordAlways` in every app; restore all-or-nothing.
  - No best-of, fork or checkout-only launches (`needsCheckout`).
- **The shared context folder lives outside the worktree**:
  `~/.lola/context/<project>/<key>/` symlinked as `.lola/context`. Key =
  `ContextKey` → lowercased issue → session id (`runtime.ContextKey`). Best-effort;
  never deleted automatically; `renameProject` moves it.

### Trust boundaries

- **Untrusted output stays out of the control loop.** Brain summaries and review
  findings come from attacker-influenceable context. Brain never reaches the
  worker; findings only via sanitize + idle gate. `[statusagent]` output reaches
  only wire display fields (`displayOverlay` is the one reader).
- **`Session.Board` (agent self-report) is a claim, display-only.** Taught by
  the section `withReportBriefing` appends to `.lola/prompt.md`.
  `handleAgentReport` writes only `Board` (no axis, freshness, gate, guard);
  `boardInfo` is the one reader. Not activity evidence; blockers feed neither
  `Attention` nor triage. `agentReport` is in remote `deniedCommands`,
  rate-limited; CLI exits 0 if the daemon is down.
  - Claim audit (`claimaudit.go`) is display-only too: one scanner advance per
    cycle for claude sessions with a matching `check … pass`; `sessionsData` judges
    from cache. Decode only command lines + `is_error`, never render transcript
    text. Warn only on a fully read transcript. Exit status counts only when it is
    the line's own (`claimaudit.Hit` handles `&&`, `||`, pipes, here-docs).
    Evicted deciding runs → abstain.
- **Shutdown-shielded loops.** Observer and reconcile run on
  `context.WithoutCancel`, panic-guarded, every gh/tmux exec deadlined; spawn
  bounded by `nativeSpawnTimeout`.
- **Secret discipline.** Linear key and Slack webhook never in `config.toml`,
  argv, logs or errors; resolve by name (keychain/env), sanitize `*url.Error`. The
  Linear key's write path (`ConfigService.SetLinearKey`, TUI `sfSecret`) goes
  straight to the keychain, never through `SettingsDTO`/a form commit; nothing
  reads it back (`LinearKeyStatus`/`linearKeyHelp` report only the source); both
  surfaces mask and clear it; on keychain failure leave `api_key_env` and say so.

### Conversation and session view (desktop)

See `docs/unified-session.md`.

- **The Conversation is a display-only projection; everything from an agent's log
  is a claim.** `agentfeed` may read assistant text, tool names/summaries, and
  tool output on request, sanitized. Nothing it returns reaches control state
  (`TestControlLoopNeverImportsAgentfeed`). Reads only a UUID base name under the session's slug
  dir, no-follow, never a hook-supplied path. Only store-recorded sends wear the
  "you" fill; only exec-channel blocks wear a plate or exit code. `feedHealth`
  degradation is always visible; `partial` compares hook vs feed counts.
- **A plate = lola witnessed it; a card = you can act on it; nothing else gets a
  box.** Rail marks (`S/RailMark`) are neutral kind markers, never state; only an
  unanswered failure tick has colour. State glyphs (`◉ ◐ ● ○ ✕ $`) never appear in
  story rows. `S/RailFoot` is the agent-axis footer. Delivery axis in the top bar.
- **One DOM, three layouts** (`.cv-*` container queries: lanes / pane / stack <
  1040px). Prose max 560px. `lib/answerblocks.ts` builds elements as data, no
  `{@html}`, fails open (a test holds no word is lost). A person's message is never
  parsed; log rows never wear green.
- **One session view; opening only changes its height.** The band is the same
  `SessionEmbed`; its wrapper is a CSS state, never `{#if}`. Unopened it takes the
  keyboard only from a click.
- **Terminal attached only while on screen.** One xterm per tmux session; no
  hidden/read-only clients. Selection settles before attaching. `defaultView` opens
  the terminal for agentless, orphaned, goal-managed sessions and unreadable logs
  (unless a host is connected), only at open; later degradations show a notice.
- **A session's story outlives it.** `dropSession` records `State.Ended`
  (best-of losers via `recordEndedAs`); 14-day retention from `EndedAt`;
  `EndedView.svelte` reads store + transcript. Respawned ids get a
  `session_started` divider; old queued words read as recalled.
- **Changes: one place, two entries** (`ChangesView.svelte`): `DIFF` vs base and
  `CHECKPOINTS` per turn. Comments anchor only on the base diff. `Changes +84 −12`
  comes from `lib/changes.svelte.ts` (one `cmd=diff` on arrival and at rest) —
  no new `SessionInfo` field.
- `FirstOpen.svelte`: one global line (`lola.session.intro`) saying the terminal
  is still there; not consumed while a terminal covers the view.

### SSH sessions

Contract: `docs/ssh-sessions.md`; implementation `internal/daemon/ssh*.go`.

- **The agent holds nothing; a command is an exec channel, never keystrokes.**
  The daemon owns a real ssh master in a `<id>-ssh-N` tab where the human authenticated.
  - `ssh -O check` exit 0 is the only proof of connection. Pane text never moves
    state (read only for the handback preview and sign-in prompt text). No
    auto-reconnect; only human-side `sshGo` enters `connecting`.
  - `ProxyCommand=false` in `sshconn.CommandArgs`/`ReopenArgs` makes commands fail
    closed — never remove.
  - `protocol/ssh.go`'s split is the trust boundary: `hookBridgeCmds` gains exactly
    `SSHAgentCommands`; `internal/remote` denies both lists. Real for sandboxed
    agents only; production refuses unsandboxed agents unless `allow_unsandboxed`.
  - `config.SSHHost.SSHAdmit` / `config.SSHDirectSandbox` are the production rule,
    read on what the session recorded, at every decision incl. `sshApprove`. A
    human running a proposal is exempt. Direct sessions can be sandboxed
    (`Native.sandboxScratchLine`).
  - Facts (command, cwd, exit, timing, output) vs claims (`why`, `scope`,
    elements: display-only, sanitized, rate-limited). Proposals run only via
    `sshProposal` from a human, pinned to the revision read. No client-side
    "read-only" parsing.
  - Wheel and connection re-checked at dequeue. Taking the wheel refuses what
    waits, ends grants, leaves in-flight commands stoppable. Local stops may leave
    remote processes; say so.
  - The store's queue is the truth for text to the agent (`Session.PendingSSH` is
    its join); delivered via `sshAgent*` replies or `deliverQueued`.
  - `sshCloseConn` runs `-O exit` before killing the tab; graceful daemon stop
    closes no master; a command running then reads `lost`.
  - UIs compile against `protocol/ssh.go`. Only "changed since" and "not waiting
    for approval" map to friendlier copy (reword them in `sshhuman.go` = both UIs).
  - `<id>-ssh-N` is never discovered: refused by `TermService.CloseShell`, omitted
    from `Shells`/phone panes, mounted only by `SSHTerminalPane` (name from
    `SSHConnInfo.Tab`). Daemon down → app keeps last sessions, phone keeps ssh ones
    (`store.retained`, `retainAll`).
- **One story per session; a human's command is private until shared.**
  `view.scope` filters and targets Run (default All). Terminal reached via strip or
  ⌘\\. Composer: ↩ asks the agent, ⌘↩ runs on the host; double ⌘ swaps, and the
  swapped field must look different (tier colour). Unswapped ↩ never runs. Under
  All with several live hosts Run is refused; with no host ⌘↩ keeps the draft.
  `cmd=sshHumanRun` records `Private` blocks, invisible to the agent until
  `cmd=sshShareBlock`. Both human-only.
- **A host is one connection; removing it removes its commands, not your words.**
  Connect always adds a connection (except answering an agent's request).
  Per-connection `view.commandDraft`; session `view.agentDraft`.
  - Under All a Say hands back nothing and leaves `TalkConn`; with a host in scope
    it hands that host back and sets `TalkConn`.
  - New connections reuse a running master (`sshSharedMaster`); last user
    (`State.SharesMaster`) does `-O exit`.
  - `cmd=sshTrash` (`Store.Trash`) removes the connection's blocks, output,
    elements, events, handback and queued text; keeps human `say` items
    (unlabelled) and session-wide items. Agent gets one line.
  - The agent never names a tab: `ConnForHost` prefers `State.TalkConn`, then newest
    open; no evidence → session-wide (`Conn == ""`).
- **One shell for the human, one folder for everyone.** `sshterm` bash hooks via
  fd (nothing written to host); `sshtap.go` reads via `pipe-pane` + FIFO. Run goes
  into the shell only when tracked and at a prompt, else exec channel.
  - Markers are display material (nonce is learnable): may create a private block
    and a validated folder, never a decision.
  - The agent shares the folder only; never route agent commands into the shell.
  - `Elevated` is a label, never a check.
  - FIFO opened blocking (Go can't poll it on darwin); stop wakes it with a byte.
  - lola closes idle masters itself (`sshIdleExpired`) since `-O check` resets
    ControlPersist.
- **A shell's question comes to the timeline; its answer is the one secret on any
  lola path** (`sshprompt.go`, `SSHPromptCard.svelte`).
  - The question is untrusted: show the exact line and asker, no lock/logo, yes/no
    never takes focus, no state follows from detection.
  - Pending prompt is memory only (`sshPrompts`); never set `Prompt` on a stored `Conn`.
  - The answer never touches argv, logs, store, drafts or replies: `tmux.PasteSecret`
    (stdin `load-buffer`, `paste-buffer -d`), never `SendText`. Don't lower the Wails
    log level.
  - Stale cards don't type: claim id, stage live, pane shows the same line.
  - `cmd=sshPromptAnswer` human-only, no CLI verb.
  - The tap discards the first line after an answer; `AnswerEchoTab` withholds that
    tab's handback. That is the whole echo defence — say so plainly.
  - Sign-in reads the pane on every host only in `connecting`/`authenticating`.
    Unknown wording → "Waiting for input?" + terminal button.

### Widgets (desktop)

- **A widget is untrusted code.** `SSHWidget.svelte` keeps `sandbox="allow-scripts"`
  alone (never `allow-same-origin`). `lola.ask`/`lola.run` only fill the composer,
  never send/run, and never fill or move focus while a prompt card is open or an
  input is focused. A per-session click gates running.
  On a Wails upgrade re-check the native `external` handler and
  `desktop/runtimeguard.go` (requires POST + `x-wails-client-id`, no `Origin:
  null`); `TestRuntimeGuardWasReviewedForThisWails` fails until reviewed.
- **Navigation escapes the CSP.** Never claim "no network". The SDK's
  `MessageChannel` port (sent in `boot`) is the only channel; a navigated page
  can't answer the ping (`WIDGET_PING_MS`) and is closed. Never post data via
  `contentWindow.postMessage`. A live page leaking via 204 remains open (needs a
  native rule, untested).
- **Widgets must never fail silently.** Keep the `window.webkit` hide in its own
  `try`/script. Frame is a `data:` document (not `srcdoc`, which mutes errors). SDK
  head has no line breaks. Error text is untrusted: `cmd=sshWidgetStatus` sends it
  capped/quoted to the agent only; the panel shows `WIDGET_FAULTS`; the store keeps
  a fingerprint. `WidgetAdvice` is advice. Guide examples are ```` ```html name ````
  fences; `TestWidgetGuideExamplesDrawNoAdvice`.
- **Frames never move in the DOM.** `DockArea.svelte` keeps all frames in one
  append/filter-only each (`docklayout.liveFrames`) positioned over panels; never
  wrap or reorder it.
  - `lib/docklayout.ts` is the DOM-free layout tree; operations return normalized
    trees (same object if unchanged); random-sequence test pins no tab lost/duplicated.
  - Previews apply the move; non-fitting moves come back `refused` ("No room").
  - `resizeSplit` pushes through neighbours at minimum.
  - Frames lose pointer events from the press of a tab/sash; tabs capture the
    pointer only once dragging.
  - Background tabs use `visibility`, start on first show; `active` pauses frames
    and refuses `lola.ask`.
  - A new run leaves the layout up with no frame running (escape from a blocking page).
  - `syncDock` drops tabs only after a real timeline answer; new tabs open behind
    with a dot.
  - Session switch unmounts (pages keep state in lola data). Too-small window → drawer.

### UI conventions

- **`[ui].theme` paints both surfaces.** `internal/tui/catppuccin.go` ports
  `desktop/frontend/src/lib/catppuccin.ts`. The TUI palette in `theme.go` is a
  `var` seeded with the old navy values. Every palette-derived lipgloss style
  must be declared bare and built in a `rebuildXStyles()` called by
  `rebuildStyles()`, or it keeps old colours. `applyTheme` runs on load, reload
  and save. Keep `config.UIThemes` and TS `THEME_IDS` identical.
- **Every app action is `<Button>`; every popover row is `<MenuItem>`**
  (`desktop/frontend/src/lib/components/Button.svelte`: sizes `xs/sm/md`, variants
  `ghost`/`accent`/`secondary`/`primary`/`danger`/`danger-solid`, plus `selected`,
  `icon`, `block`, `loading`). No hand-rolled button classes.
  - `loading` disables but overrides the fade with `!`; in-flight flags live in the
    store, not the button.
  - Classes are literals in module-level maps (Tailwind scans text).
  - Use `enabled:hover:`, never bare `hover:`.
  - Override a variant colour with trailing `!` (`text-warn!`).
  - Hand-rolled on purpose (commented in place): `role="tab"` strips
    (`Tabs.svelte`, `ViewStrip.svelte`), theme swatches, `[defaults]` inherit chip,
    card-shaped rows, machine tab chips (`ViewStrip.svelte`, `DockPanel.svelte`).
  - Labels in Sentence case. Tests use `getByRole("menuitem", { name })`.
- **`text-xs` is the uppercase micro-label tier** (12px/600/0.06em). Use
  `text-sm` for small reading text. Scale is documented atop `app.css`.
- **No OS-drawn form controls.** Use `Checkbox.svelte` / `Select.svelte`; number
  steppers hidden in `app.css`. Tick and caret are sibling `<svg>`s in
  `currentColor`, never `::after` (WebKit drops it). `class` lands on the wrapper.
  Native on purpose: the `<select>` popup and textarea grabber. `Controls.test.ts`
  fails on raw `type="checkbox"` / `<select`.
- **Destructive = shifted key, and it confirms.** Project lists: `x` stops polling,
  `X` removes the project; `n`/`a` open the new-project form. Dev toggle is `D` on
  both surfaces. The app routes every irreversible action through
  `lib/confirm.svelte.ts` + one `ConfirmDialog`.
- **TUI config forms guard unsaved edits.** `formModel`/`settingsForm` keep a
  `baseline`; dirty `esc` arms `confirmDiscard`. Call `rebase()` after every async
  fill. `ctrl+c` at the top of `rootModel.Update` is gated on `m.form == nil &&
  m.settings == nil` — keep the gate.

## Testing

- **NEVER drive a UI with synthetic OS input**: no CGEvent, `osascript`
  clicks/keys, `cliclick`, Accessibility driving, or anything that focuses windows
  or moves the pointer. It steals the user's focus and is unreliable. Instead, in order:
  1. Component tests (vitest + @testing-library `fireEvent`) in `mobile/` or
     `desktop/frontend/`.
  2. `xcrun simctl io <udid> screenshot`, then read the image.
  3. Launch-env deep links: `SIMCTL_CHILD_LOLA_DEV_LINK=... xcrun simctl launch`
     (`mobile/src/lib/devlink`). Add a link target if needed, never a tap.
  4. A browser harness at phone viewport against `npm run dev`.
  Otherwise say it's unverified and leave it for a human.
- Inject fakes via `Daemon` seam fields and `linear.API`/`fake.go`; isolate state
  with `$LOLA_HOME = t.TempDir()`.
- Daemon definition of done (`agent-rules.md`): filter construction per mode,
  pagination, `Budget` math, both dedup modes incl. seen pruning, cross-poll
  dedup, labelIds delta, identifier-vs-UUID, native lifecycle (spawn + rollback,
  adopt classification, store-driven `liveCounted`, fail-closed reconcile revert).

## Desktop app (`desktop/`)

**Lola**: Wails 3 + Svelte 5 runes + Tailwind v4 + xterm.js. A package inside
this module (to reuse `internal/*`), a client of the daemon socket (never embeds
it), driving `tmux -L lola` directly for terminals. Services: `TalkService`
(only `lib/talk.svelte.ts` imports it), `SSHService` (human side, no
`sshAgent*`), `DaemonService`, `TermService`, `ConfigService`, `DoctorService`,
`LinearService`, `UpdateService`. One project form with tabs (TUI:
`internal/tui/form.go`). Needs `wails3` CLI (`go install
github.com/wailsapp/wails/v3/cmd/wails3@latest`). See `desktop/README.md`.

Gotchas:
- **`wails3 task build` updates only `bin/Lola`; the `.app` is made by `wails3
  task package`.** Iterate with `wails3 dev`.
- **Dock/Finder name comes from the `.app` directory name.** `desktop/Taskfile.yml`
  `APP_NAME: "Lola"` is load-bearing; keep `build/darwin/Info*.plist`
  `CFBundleExecutable` and `.github/workflows/build.yml` `APP_PATH` in lockstep. Dev
  bundle is `bin/dev/Lola.app`. Never change `CFBundleIdentifier`
  (`dev.sushi.lola.desktop`). Everything else stays lowercase `lola`.
- **WKWebView doesn't stretch a flex child in a flex column.** Use grid or explicit
  width; verify in the real `.app`.
- **The app ships the CLI** at `Contents/Resources/bin/lola` (`build:cli`; path
  pinned against `bundledRelPath`). `desktop/lolabin.go` resolves `$LOLA_BIN` →
  `PATH` → bundled (PATH first so `go install` wins). `DaemonService.CLIInfo` +
  doctor flag version skew. `InstallCLI` symlinks, refusing anything not a symlink
  into a `.app`. Only `lola` is vendored — never tmux.
- **`ensurePATH` probes the login shell** (`$SHELL -l -c`, bounded, reads a
  sentinel, falls back to a static list).
- **The daemon never hot-reloads** — see "Getting a change into the running
  daemon". The store uses `Promise.allSettled` so one unknown command doesn't
  blank the UI.
- **Bare keys are the frontend's; ⌘ chords are the macOS menu's.** `App.svelte`
  `onKey` bails on `isChord` (`lib/keys.ts`: meta/ctrl/alt, not shift). Chords live
  in the Session menu (`installAppMenu`/`newSessionMenu` in `desktop/main.go`) →
  `app:session-action`; they work even while a terminal has focus. New chords:
  Cmd-based, no Edit-menu chords or ⌘⌫, no duplicates (Force Reload is ⌥⌘R), list
  in `HelpOverlay.svelte`.
- Fonts: bundled JetBrains Mono; xterm re-fits on `document.fonts.ready`.
- **Terminal links**: `WebLinksAddon` + `term.options.linkHandler` (OSC 8) →
  `store.openURL` → daemon `cmd=openURL` (http(s) only). Never `window.open`. With
  `[tmux].mouse = true` clicks go to tmux.
- **App icon is icns-only** — never re-add `CFBundleIconName`/`Assets.car` (Tahoe
  shrinks it, wails#4163). `build/darwin/make-icns.sh` builds from
  `darwin/appicon-rounded.png`; master is `build/appicon.svg`; `build/appicon.icon/`
  is unused.
- **Self-update assumes a PUBLIC repo.** `UpdateService` (`desktop/updatesvc.go`,
  `desktop/internal/update`) anonymously reads
  `/repos/sushidev-team/lola/releases/latest`, mounts the universal DMG,
  `ditto`-stages and swaps after quit. `main.version` via `-ldflags` (non-semver
  `dev` = always offer). Cadence/skip in `~/.lola/desktop-update.json`. The DMG lands
  after the release: keep `available` (newer version) apart from `installable`
  (has `downloadURL`). `Checker` caches 1h; manual checks pass `force`; opening
  the overlay always re-checks.
- **Releases are release-please.** Merging the release PR tags + creates the
  release, then calls reusable `build.yml` via `uses:` (tags don't trigger
  workflows). goreleaser: `changelog.disable` + `release.mode: append`. Version in
  `.release-please-manifest.json`; config also bumps `desktop/build/config.yml`.

## Reference docs

- `README.md` — commands, config reference, runtime layout, launchd, secrets.
- `config.example.toml` — commented config.
- `agent-rules.md` — build spec / rules (with AO-bridge deltas).
- `SPEC.md` / `PLAN.md` — original spec and roadmap (P0–P9).
- `docs/unified-session.md` — desktop session view design; change it with the view.
- `docs/ssh-sessions.md` — engineering contract for hosts in a session.
