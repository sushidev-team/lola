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
- `lola run` — the daemon. Lifecycle is **TUI-managed by default**: the TUI
  silently spawns a detached `lola run` on open if the socket is dead, and
  `^r`/`^x` restart/stop it (restart re-execs the current binary, so the newest
  build comes up — the dev loop). `internal/tui/daemonctl.go` owns this. Set
  `[defaults].manage_daemon = false` to hand the lifecycle to launchd
  `KeepAlive` instead — the two owners must not both run.
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

### `make build` alone never reaches the running daemon

`make build` writes `./lola` in the repo. The daemon on this machine is started
from **`$GOPATH/bin/lola`** (the TUI's `^r` re-execs the current binary, the
app's restart button and a hand-started `lola run` resolve it from `PATH`), so a
change is only live after:

```sh
make build && GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go install .
```

...followed by a daemon restart — the daemon does not hot-reload its own binary.
Two traps that both cost a debugging session already:

- **Replacing the file does not change the running process.** `go install` writes
  a NEW inode; the old process keeps the one it started with, so `ls -l` on the
  binary says "new" while the daemon is still running last week's code. What the
  process is ACTUALLY executing:
  `lsof -p <pid> | awk '$4=="txt"'` — compare that inode with
  `stat -f '%i %N' $(command -v lola)`.
- **A feature can be missing without a single error line.** A daemon predating a
  command answers `unknown cmd "<x>"`, but one predating a *derivation* (dev
  URLs, a new observer pass) simply never writes the field and logs nothing.
  Before debugging the code, confirm the running image has it —
  `go tool nm $(command -v lola) | grep <symbol>`, or
  `go tool objdump -s '<caller>' $(command -v lola) | grep <callee>` to prove the
  call site is wired, not just linked in.

## Architecture map

The daemon (`internal/daemon`) is the heart; it composes the leaf packages,
each of which owns exactly one external tool or concern behind an **exec seam**
(a swappable function/interface) so tests never touch the real tmux/git/gh/claude:

- `internal/config` — owns `config.toml`: schema, defaults, atomic
  (temp+rename, 0600) persistence, and **static** validation only. `Home()`
  honors `$LOLA_HOME` (every runtime path derives from it; tests set it).
  Path-exists / is-a-git-repo checks live in the runtime layer, NOT here. Also
  owns the `[defaults]` → `[[project]]` **inheritance layer** — see the
  invariant below before touching `Project` or `Defaults`.
- `internal/linear` — Linear GraphQL client (`API` interface + `fake.go` for
  tests). Paginated queries, exponential backoff on 429/5xx, filter built from
  the poll's mode fields. All IDs are Linear **UUIDs** passed as variables.
- `internal/runtime` (`Native`) — the session launcher: `Spawn` (worktree →
  symlinks → `post_create` → tmux `claude --settings`), `Adopt` (re-adopt
  survivors after a restart; reports zombies, never kills), `Kill`. Composes
  `worktree` + `tmux` + `hook`; talks to git/tmux/claude only through them.
- `internal/worktree` — per-session git worktrees under
  `~/.lola/worktrees/<project>/<session>/`. `Remove` refuses a dirty worktree
  (`ErrDirty`) unless forced, and guards the project's main checkout;
  `DeleteBranch` is the branch-only half for a checkout that is already gone
  (it prunes stale worktree registrations first, or git refuses the delete).
- `internal/tmux` — thin tmux CLI adapter on lola's **own** server
  (`tmux -L lola`), isolated from the user's default tmux. Session targets use
  the `=` exact-match prefix.
- `internal/hook` + `lola hook <event>` (hidden subcommand) — the callback path
  from Claude Code lifecycle hooks back into the daemon. `hook.SettingsJSON`
  generates per-session `--settings` wiring Stop/Notification/SessionEnd/
  PostToolUse/UserPromptSubmit to `lola hook`, which posts to the socket. This
  path is on the agent's critical path: bounded 2s, always exits 0 — a broken
  lola must never wedge or fail an agent's turn.
- `internal/scm` — GitHub PR/CI observation via `gh`. Ships FACTS only (PR
  state, checks rollup, mergeability) — status derivation moved to
  `internal/state`, which sits above it (scm must never import state).
- `internal/state` — THE status vocabulary, single home. A session is two
  ORTHOGONAL axes: `AgentState` (what the agent itself does — hooks + pane +
  tmux liveness own it, PR facts never mask it) × `DeliveryState` (where the
  PR stands — gh facts only, via `DeriveDelivery` with UNKNOWN-mergeable
  hysteresis). `Rollup(a, d)` is the ONLY producer of the legacy one-string
  status; the consumer tables (`HoldsSlot`, `Present`, `Notable`,
  `NeedsAttention`, `SortRank`, `KanbanColumns`) are the only slot/attention
  classifications — the desktop's `theme.ts` mirrors them and
  `desktop/state_parity_test.go` pins the two byte-identical.
- `internal/session` — pure data: the `Session` model + JSON snapshot `Store`
  (atomic temp+rename). No exec. Holds the two axes + freshness stamps, the
  derived rollup `Status` (written ONLY by the `SetAgentState`/`SetDelivery`
  mutators), PR state, the persisted one-shot guards for reactions (P3) and
  write-back (P4), and the display-only `[statusagent]` overlay fields.
- `internal/statusagent` — the OPT-IN status interpreter: one bounded
  `claude -p` per interpretation (default `--model sonnet`) judging what an
  agent is ACTUALLY doing from pane/events/PR context. Output is parsed,
  whitelisted, clamped — and DISPLAY-ONLY (see the invariant below).
- `internal/agent` — the pluggable coding-agent leaf (stdlib + regexp only; must
  NOT import config/session/hook/runtime/attention): the `claude`|`codex`|
  `opencode` kind enum, per-kind launch argv (`LaunchArgs`), the callback-config
  bodies (codex `config.toml`, opencode plugin JS), and `ParseCodexNotify`.
  `internal/runtime` writes the right callback artifact at spawn; the health-gate
  checks the resolved binary; `config.AgentForProject` resolves
  project→defaults→`claude`. `internal/attention` imports it for agent-aware
  pane classification.
- `internal/devtab` — the naming convention for a session's DEV tabs
  (`<sessionID>-dev-<n>`, 1-based into `[[project]].dev_commands`). A stdlib leaf
  for the same reason `internal/lolaenv` is one: the daemon creates the tabs,
  `internal/runtime` must recognize them as auxiliary sessions, and both the TUI
  and the app discover them as terminal tabs — none of which may import the
  others.
- `internal/proctree` — the ONE place lola signals a process it did not start:
  the machine's process table (`ps`) plus whole-process-GROUP termination
  (SIGTERM → grace → SIGKILL → settle). It exists because a process group is not
  the whole story — a descendant that left the group (Claude Code's Bash tool
  puts every command in its own) is unreachable by a group kill and is exactly
  what keeps a port bound. Both `internal/tmux`'s `KillSessionTree` and the
  daemon's dev sweep use it, which is why it is a stdlib leaf and not part of
  either.
- `internal/devurl` — pure text: scores the LOCAL testing URLs a dev command
  printed into its pane and ranks the app's above the bundler's. It cannot be a
  lookup table (lola does not know what `dev_commands` runs or what port it
  settled on — the port MOVING is the whole point), so the CUE around the URL
  decides: "Server running on", a `[server]` label, "Local:". Only http(s) on a
  loopback host is ever returned, because the result is handed to an opener and
  pane text is untrusted.
- `internal/portproc` — one lsof question: which processes hold a LISTENING TCP
  port, and from which working directory. The directory is the point — lola
  cannot know what port `dev_commands` bind, so a stray dev server is found by
  where it runs, not by its port (see the ACTIVE-session invariant). Fails open:
  no lsof, or output it cannot parse, reports nothing rather than a guess.
- `internal/portclash` — pure text, the mirror image of `internal/devurl`: did a
  DEAD dev tab die because its port was taken, and which port was it? Every
  server words that failure differently (`bind: address already in use`,
  `EADDRINUSE`, `Port 9245 is in use`, `port is already allocated`), so the cue
  is the phrase plus the address beside it. The ONLY thing it ever returns is an
  integer in 1..65535 — the caller asks lsof about that port and offers a human a
  kill button, so a number is the whole safe surface to carry out of untrusted
  pane text. Fails closed on a wording it does not know or a message that names
  no port.
- `internal/gitrepo` — reads a checkout with LOCAL git only (no network, no
  `gh`): `Detect` resolves the GitHub `owner/name` from its remotes (upstream,
  then origin), `Branches` the fork-from candidates, and `Inspect` gathers root
  + repo + default branch + branches in ONE pass — the call behind the project
  forms' folder-pick autofill. Deliberately NOT in `internal/scm` (gh-only) or
  `internal/config` (never execs). **Fails closed**: every unknown returns the
  zero value, because an empty repo merely disables the open-PR check while a
  wrong one would make `gh pr list --repo` answer about someone else's
  repository.
- `internal/secrets` / `internal/notify` / `internal/brain` / `internal/review`
  / `internal/attention` / `internal/doctor` — Linear key resolution
  (keychain→env), best-effort desktop/Slack notify, opt-in headless-claude
  summarizer, opt-in CodeRabbit QA pass, pane→answerable-question heuristic
  parser (agent-aware), structured health checks.
- `internal/reviewmd` — presentation-only leaf (stdlib): renders a provider's
  plain findings into the GitHub Markdown posted on the PR — as one comment
  (`Render`) or split into anchored, resolvable review threads plus a summary
  (`RenderInline`). Two callers, both in `internal/daemon` (`postGithubSink` /
  `postGithubInline`); see the invariant below.
- `internal/diffanchor` — pure text leaf: which `(path, line)` pairs of a unified
  diff may carry a GitHub inline review comment (RIGHT side, added + context
  lines, `Nearest` for the bounded snap). It exists because the reviews endpoint
  is ATOMIC — one comment on a line outside the diff rejects the whole review —
  so the diff decides before anything is posted. It cannot live in `scm` (which
  never parses) or `reviewmd` (which never execs); it fails open toward FEWER
  anchors, never a wrong one.
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
- `observer.go` — read-only ~30s loop: ONE `tmux ls` per cycle (liveness +
  `#{session_activity}`, the sustain-only activity signal), gh PR facts onto
  the DELIVERY axis (failed fetches counted → `PRStale` on the wire, facts
  never invented), pane classification onto the AGENT axis pre- AND post-PR
  (`agentReconcile`) — into the `session.Store` snapshot; the `sessions`
  socket command serves the cache (a client request never execs gh/tmux).
  Contains the anti-false-working guard (`staleWorkingThreshold`).
- `reactions.go` — P3 engine acting on derived status changes. Also owns
  `ensurePromptVerified`, the pre-send pane check for an adoption-carried
  (unverified) AtPrompt gate.
- `statusagentwire.go` — the `[statusagent]` interpreter's worker/triggers/
  cost controls and `displayOverlay`, the overlay's ONE consumer.
- `dev.go` — the per-project ACTIVE session: `[[project]].dev_commands` running
  in `<id>-dev-N` tabs, plus the observer's `reconcileDevTabs` derivation. See
  the invariant below.
- `reconcile.go` — ~5m pass reverting orphaned issues (labeled-sent but no
  counted session and no open PR after `orphanTimeout`).
- `writeback.go` — P4 Linear state transitions + comments.
- `state.go` — the per-poll `seen` store and in-flight set.

## Non-obvious invariants (read before changing daemon code)

- **A `Project` field holds the RESOLVED value; `Inherits` says where it came
  from.** `[defaults]` carries a fallback for each inheritable `[[project]]` key
  (`match_labels`, `match_mode`, `on_sent_set_label`, `blocked_label_id`,
  `dedup_mode`, `priority_sort`, `symlinks`, `post_create`, `env`). Rather than
  making those fields pointers — which would have broken ~50 downstream reads in
  daemon/runtime/linear — `Load` RESOLVES them into the plain field and records
  the source in a `config.ProjectInherits` bitmap. So daemon code just reads
  `p.MatchLabels` and gets the effective value; only the config UIs consult
  `p.Inherits`. Consequences to preserve:
  - `Save` writes an inheritable key **only** when the project overrides it, so
    an inherited value is never frozen into the file. Mutating `p.MatchLabels`
    without clearing `p.Inherits.MatchLabels` **silently discards the write** —
    that is the trap. Both form layers go through an explicit override step.
  - The bitmap's **zero value means "fully explicit"**, matching a hand-built
    `config.Project` literal. Never flip that polarity: every construction site
    (tests, both UIs) would start silently inheriting.
  - The on-disk mirror (`fileProject`) uses **pointers** so an absent key
    ("inherit") stays distinct from `key = []` ("override to nothing"). A nil
    slice through that pointer is omitted, an empty non-nil slice is written.
  - `ResolveInheritance` is idempotent and canonicalizing; `Load`, `Validate`
    and `Save` all call it, which is what makes save/load an identity.
  - `agent` / `concurrency_cap` / `branch_prefix` are deliberately NOT in the
    bitmap: zero has always meant "fall back" for them and
    `AgentForProject` / `EffectiveCap` / `BranchPrefixForProject` already
    resolve project → `[defaults]` → hard default at read time.
- **A project has two names: `Name` is identity, `Label` is display.** `Name` is
  a path segment (`worktrees/<name>/`, `state/<name>.seen`) and the prefix of
  every session id — which is also the tmux session name — so ~11 call sites
  re-derive worktree paths from `cfg.ProjectByName(s.Project).Name` rather than
  reading `session.Worktree`. `Label` is free text nothing keys by. Consequences:
  - Render `p.DisplayName()` / `cfg.DisplayNameFor(id)` in UIs; use `Name` only
    for paths, tmux and protocol name fields. Never render a bare `p.Name`.
  - `config.Slug` is the ONE place a label becomes an id (`SlugTyping` is its
    non-trimming half, for live typing — trimming mid-keystroke makes a hyphen
    impossible to enter). `internal/runtime`'s own `slugify` is for git refs and
    stays independent.
  - Slug shape is a UI rule, NOT validation — pre-`label` configs hold names like
    `"Okane"` and must keep loading. The TUI form only canonicalizes a name a
    human actually typed (`idEdited`), because re-slugging an untouched legacy
    name would turn an ordinary save into a rename.
  - A `Name` change is `cmd=renameProject`, daemon-only and **idle-only**
    (`internal/daemon/renameproject.go`): it refuses while any session or
    worktree still carries the old name, then renames the config entry, carries
    the `.seen` file over and reloads. Do not "helpfully" extend it to live
    sessions without also moving worktrees + `git worktree repair` + tmux renames.
- **Adding a project starts at the FOLDER, and every derived field is
  FILL-ONLY.** The checkout is the one value nothing else can be derived
  without, so a new project opens straight into a picker — the native chooser in
  the app (`ConfigService.PickFolder`), `internal/tui/dirpicker.go` in the TUI
  (a listing marks git checkouts, and `enter` on one TAKES it rather than
  descending). One `gitrepo.Inspect` then fills `path` (the checkout ROOT, so
  picking a subdirectory still configures the repo), `label`/`name`
  (`config.LabelFromPath` → `Slug`, which round-trips back to the folder name),
  `repo` and `default_branch`. Rules:
  - A fill NEVER overwrites a human's value — only an empty field, or one this
    form itself wrote (`repoAuto`/`branchAuto`). Typing or pasting clears those
    flags, so a late answer cannot land on top of a decision.
  - Identity (`label`/`name`) is suggested for a NEW project ONLY: filling an
    existing project's empty label would write a `label` key nobody asked for on
    the next save, and its `default_branch` is a decision, not a placeholder.
  - The answer is matched against the CURRENT path before anything is applied; a
    result for a path the user has since changed is dropped, or the form would
    describe a repository it no longer names.
  - The app runs the pass on OPEN without filling (branch list + checkout status
    only) — an untouched form must not come up dirty; the TUI rebases its
    baseline for the same reason.
- **`[defaults]` label keys must be WORKSPACE labels, and that is a UI rule, not
  a validation one.** Linear has team labels (scoped to one team) and workspace
  labels (`IssueLabel.team == null`, valid everywhere). A `[defaults]` label is
  inherited by projects on any team, so only a workspace label is coherent —
  `linear.WorkspaceLabels` fetches exactly those and both settings screens offer
  only them. `Validate` does NOT check this: whether a UUID is team- or
  workspace-scoped is unknowable offline, and an earlier cross-team rejection
  here blocked the correct configuration. Do not reinstate it.
- **Health-gate every dispatch.** If `tmux`/`git`/`claude` aren't all resolvable
  or the poll's `[[project]]` doesn't resolve: skip the tick, record `lastError`
  in status, and mutate **nothing** (no seen, no labels, no in-flight).
- **Dispatch order is load-bearing.** Record in-flight + write seen *before*
  spawning, so a crash mid-spawn can't double-dispatch. Upsert the session into
  the store immediately so the next `Budget` call counts it.
- **Status is two axes; `state.Rollup` is its only producer.** `AgentState`
  (hooks + pane + tmux liveness) and `DeliveryState` (gh facts) live side by
  side on the `Session`; the rolled-up `Status` string is derived from them by
  the `SetAgentState`/`SetDelivery` mutators — writing `Status` directly is a
  bug (the axes and the rollup drift). Post-PR the delivery axis owns the
  rollup while the agent axis stays truthful underneath (that split is what
  killed the old hook↔observer status flap). `state.FromLegacy` backfills axes
  for pre-axis snapshots on load/Upsert, so legacy records keep working.
- **`liveCounted` comes from the session store snapshot**, never a local
  counter. Only slot-occupying rolled-up statuses count (`state.HoldsSlot`:
  `working`, `needs_input`, `draft`, `ci_failed`, `changes_requested`,
  `ci_pending`, `merge_conflict`); parked-for-review and terminal statuses
  don't, so held PRs don't stall pickup.
- **Fail CLOSED on unknowns.** The reconcile orphan-revert skips whenever the
  open-PR check can't answer (no repo, gh error) — better a stuck label than
  lost work.
- **Send-keys safety (reactions/review).** Typing into a live agent mid-turn
  corrupts it. Every path that types goes through the `AtPrompt` idle gate
  (consumed atomically via `Store.Update`); a non-idle session has its reaction
  **deferred**, never forced. Payloads are sanitized (control chars stripped)
  and are **never** run as a command. A gate carried across a daemon restart is
  UNVERIFIED (`AtPromptVerified=false` from adoption) and must pass
  `ensurePromptVerified` (live hook or a waiting pane) before the first send —
  ambiguity fails closed (defer). The **review hand-off** uses a deliberately
  wider gate, `handoffDeliverable`: `AtPrompt` **or** parked on an idle
  notification (`AgentWaitingInput` + `InputIdleNotify`), the same state
  `handleAnswer` types a human's reply into. `InputPermission` stays excluded —
  prose typed at a y/n approval answers the wrong question. Without the wider
  gate the feature was dead: findings deferred at PR-open (the worker has just
  pushed, so it is essentially always mid-turn) could only land in the sliver
  between the Stop hook and Claude Code's idle notification, which closes
  `AtPrompt` — the 30s observer cadence missed it every time and stashes piled
  up in `PendingHandoffs` unread. Two consequences: the Stop hook flushes
  immediately (`flushHandoffsOnStop`, async + drain-group registered, because a
  hook must never block a turn), and `flushReviewHandoffs` stops after ONE
  delivery per pass — an idle-notify delivery consumes no `AtPrompt`, so
  without that stop several kinds would type into the same prompt back-to-back.
  A delivered hand-off sets `AgentWorking` (as `handleAnswer` does), which is
  what closes the wider gate against a re-send. The gate admits a THIRD state:
  a session the observer's pane reconcile parked on `AgentIdle` (both of its
  paths close `AtPrompt` and neither opens a notification, so such a session was
  permanently unreachable). And whichever of the three admits it, the gate is
  only a candidate filter — `handoffPromptProof` → `paneWaitingNow` must capture
  the pane and classify it `ActivityWaiting` before ONE byte is typed. It never
  short-circuits on `AtPromptVerified`, for ANY of them: a hook verdict is
  evidence about the moment the hook fired, not about now, and that gap is where
  this broke. Claude Code ends a turn (Stop hook → `AtPrompt` +
  `AtPromptVerified`) and THEN covers the pane with a modal setup dialog; the
  short-circuit answered "verified" from the hook, the findings were typed into
  the dialog, the gate was consumed and the stash dropped — four PRs' reviews
  logged as `handed feedback to the worker` and read by nobody. A capture
  failure or any non-waiting classification (including a modal's
  `ActivityBlocked`) defers. One bounded tmux exec per delivery is the price.
- **A CARET is not proof of idleness, and the pane classifier is a claude-code
  RENDERING mirror — re-verify it against a live pane.** Every send-keys gate
  bottoms out in `attention.Classify`, so a rendering detail lola no longer
  recognizes silently disables the whole feature rather than erroring. Two such
  details are load-bearing today and both were learned from NOR-373, where a
  review deferred as "mid-turn" for 15 minutes until a human pasted it by hand:
  - The composer caret is padded with **U+00A0**, not a space (`❯ `), and
    Go's `\s` is ASCII-only. Every caret pattern missed it, `ActivityWaiting`
    became UNREACHABLE for claude sessions, and the review hand-off, the reaction
    engine and the answer path all failed closed forever. `stripANSI` therefore
    folds Unicode `Zs` to a plain space, once, for every downstream pattern.
  - The composer is drawn at **ALL times**, mid-turn included, and this build
    prints no `esc to interrupt` — so a resting caret no longer means the turn
    ended, and the LIVE status line is the only discriminator: gerund + ellipsis
    + a running timer (`✻ Harmonizing… (5m 58s · ↓ 17.9k tokens)`) while
    streaming, past tense without either (`✻ Cogitated for 24m 46s`) once
    finished. Hence `hasLiveWorkingCue`, checked BEFORE the waiting cue, while
    the weak cues (a frozen token meter, a leftover spinner frame, codex's
    `Working 4m 07s`) still lose to a resting prompt — a completed status line
    keeps those on screen, and reading them as activity is the old sticky
    false-`working` bug.
  Also note the frame is plain `─` RULES, no corners, so `boxBorderRe` never
  fires for claude and the `❯` caret carries the whole waiting classification.
- **A MODAL is not a prompt, and `attention` is the one place that knows.**
  Claude Code interrupts a session with keypress-driven overlays (the auto-mode
  setup wizard and its siblings). Typed prose is swallowed by the widget and the
  submit Enter answers the dialog, so a modal is the exact opposite of a resting
  composer — yet it draws a `❯` on its focused row, which read as a caret. Hence
  `attention.ActivityBlocked`, returned by `Classify` BEFORE every other cue
  (an overlay owns the screen; the status line behind it is frozen). Rules:
  - The cue is the full-width `▔` overlay RULE (`modalOverlayRe`), deliberately
    NOT the `Esc to cancel` footer — that footer also renders under the
    AskUserQuestion picker, a genuine answerable question that must keep
    classifying `ActivityWaiting` so it still surfaces as needs_input.
  - `agentReconcile` maps Blocked → `AgentWaitingInput` + `InputDialog`,
    regardless of the delivery axis: unlike a bare resting prompt post-PR this
    is not routine idling — nothing advances until a human presses a key, and
    the session holds a concurrency slot meanwhile.
  - Every send-keys path gets Blocked for free by failing closed on
    "not `ActivityWaiting`". Don't add a Blocked case that types.
  - PREVENTION is separate and lives in `hook.SettingsJSON`: `modalSkills`
    writes `skillOverrides: {"auto-mode-setup": "off"}` into the per-session
    `--settings` file (claude-code's flagSettings source is always merged, so
    the user's own settings stay untouched). Keep BOTH halves — Claude Code
    ships dialogs faster than that list can track them, and the classifier is
    what catches the ones it doesn't know.
- **Teardown is THREE things, and each has its own fail-closed rule.**
  `runtime.Kill` (the merged-cleanup path and `lola kill`) takes down: (1) the
  agent's tmux session AND its auxiliary sessions — `<id>-shell-N` tabs, the
  `<id>-review` pane and the `<id>-dev-N` dev tabs are SEPARATE tmux sessions, so
  killing only the agent left them running against a worktree about to be deleted
  (and, for a dev tab, a port still bound); (2) the worktree, dirty-
  safe (`ErrDirty` unless forced); (3) the local branch, but only when
  `Session.OwnsBranch()` (a `pr` session's Branch is UPSTREAM — deleting it
  destroys someone else's ref). Rules that hold it together:
  - EVERY session — the agent's own and each aux one — goes down through
    `tmux.KillSessionTree`, not `KillSession`. `kill-session` only hangs the pane
    process up, and anything that ignores SIGHUP — a dev tab's `php artisan
    serve`, a server started by hand in a shell tab — survives as an orphan of
    pid 1 still holding its port. It degrades to a plain `KillSession` whenever
    the pane pid cannot be resolved, so a tab is always torn down.
  - `KillSessionTree` signals the pane's process group **and every group its
    ppid TREE spans** (`internal/proctree`). The group alone is not enough:
    Claude Code's Bash tool puts each command it runs in its OWN process group
    (so it can time one out without touching the agent), so a `php artisan serve
    --port=8000` the AGENT started is invisible to a group kill of its own pane
    and outlives the whole session — holding the port against a worktree
    teardown is about to delete. All groups share ONE grace window (SIGTERM,
    then SIGKILL, then a short settle), because per-group grace would multiply
    the wait by a user waiting on a port.
  - The aux sweep is BEST-EFFORT: a tmux that cannot answer logs and continues,
    because these are display surfaces and the caller retries the whole cleanup
    on error — a stuck shell tab must never block a worktree removal forever.
  - Matching is `parent + ^(-shell-\d+|-review|-dev-\d+)$`, anchored at BOTH ends:
    `lola-fe-42` is a prefix of `lola-fe-420-shell-1`, and a loose suffix test
    made one session's teardown kill a live sibling's tab.
  - A missing worktree directory no longer ends teardown early — it deletes the
    branch anyway (`worktree.DeleteBranch`), because a session whose checkout
    was already gone otherwise left its branch behind forever.
  - A DIRTY worktree keeps both the checkout and its branch. That is the whole
    gate: uncommitted work is the one thing teardown never discards.
- **The ACTIVE session is DERIVED from tmux, never remembered.** Only one session
  per project may run `[[project]].dev_commands` (they bind ports), so `cmd=dev`
  is a MOVE: `internal/daemon/dev.go` kills the previous holder's `<id>-dev-N`
  tabs *before* starting its own, or "address already in use" is all the new tab
  ever says. Rules that hold it together:
  - `Session.DevActive`/`DevTabs` are a CACHE the toggle writes for an instant
    UI, and `reconcileDevTabs` (one per observe cycle) overwrites them from the
    tmux facts. Persisted intent would drift the moment a tab was closed, a
    command crashed, or the daemon restarted; derivation cannot.
  - A `dev_commands` entry is a SHELL LINE, and `lolaenv.CommandLine` only
    `exec`s it when it is a SIMPLE command. `exec` takes one command, so
    prefixing it onto `cd desktop && wails3 dev` binds it to `cd` — under macOS's
    /bin/sh (bash 3.2) that is a silent exit 0 with the real command never
    started, i.e. a dev tab that dies instantly and says nothing. A pipeline, an
    `&&` chain, a redirect or a builtin head therefore runs unprefixed; the
    wrapper `sh` waits for the command and exits with it, so `#{pane_dead}` still
    fires at the right moment and only the "pane pid IS the command" property is
    lost. The classifier is deliberately conservative (`commandSeparators`,
    `shellWordBreakers`): a false "not simple" costs an optimization, a false
    "simple" eats the command line.
  - The tabs carry `remain-on-exit`, so a crashed dev server keeps its pane and
    its error message — which means the session's EXISTENCE proves nothing and
    liveness is `#{pane_dead}` (`tmux.DeadPanes`, one `list-panes -a` per cycle,
    and only in a cycle whose `tmux ls` actually showed a dev tab).
  - A failed dead-pane probe changes NOTHING: a false "off" invites a restart
    that kills a healthy server, a false "on" hides one that is gone.
  - Stopping discovers by LISTING, not by the configured command count, so a tab
    left over from a longer `dev_commands` list is still torn down — and it goes
    through `KillSessionTree`, because `composer dev` spawns the process that
    actually holds the port and that process ignores SIGHUP. Killing only the
    tmux session left it orphaned on pid 1, so the session taking over started on
    8001 and the feature had moved the problem instead of solving it.
  - Killing the previous holder's TABS is not enough, so activation also SWEEPS
    (`sweepPortSquatters`). The agent working in a worktree starts servers of its
    own — `php artisan serve --port=8000`, to look at the page it just changed —
    and those are neither a tmux session nor part of any pane's process group
    (see the teardown invariant), so they outlive everything and the session
    taking over silently serves :8001 from the wrong checkout. lola cannot find
    such a process BY port (the port lives inside `composer dev`, not in config),
    so it finds it by WHERE it runs: `internal/portproc` (lsof) lists listening
    sockets with each owner's cwd, and anything listening from inside
    `~/.lola/worktrees/<project>/` goes down with its groups. Its rails, in
    order of how much damage each prevents: only that directory is ever swept
    (a server in the project's own checkout is the user's, not lola's); a group
    that owns a LIVE tmux pane — or the tmux server above it — is never
    signalled (every agent's cwd IS its worktree, so without this the sweep
    would kill the worker mid-turn); and it FAILS CLOSED, because no lsof, no
    `ps` and no pane list each cost the protect set.
  - The session's local ADDRESS is derived the same way and from the same place:
    `scanDevURLs` reads the dev tabs' scrollback once the tabs are up, ranks
    what it finds with `internal/devurl`, and puts it on `Session.DevURLs` →
    `SessionInfo.devUrls` → a clickable chip in the app / a `serves:` line in the
    TUI. Three rules keep it cheap and honest: it reads a pane only while
    nothing is known or right after the tab set changed (the address does not
    move on its own), it spends at most `devURLAttempts` reads per tab set so a
    `--watch` tab that never prints one stops costing a 2000-line capture every
    cycle, and `markDev` CLEARS the addresses on any change — a link to a server
    that is gone is worse than no link. The app opens it through the daemon
    (`cmd=openURL`, http(s)-only), never `window.open`: the address came out of
    terminal text.
  - The address is found by TWO readers, and the fast one is the toggle's. A
    server prints its address a second or two after its tab exists — the one
    moment where the observer's 30s cadence is far too slow, since a human is
    watching that tab come up — so activation starts `startDevURLWatch`: a
    background poll of its OWN tabs (short `devURLWatchLines` tail, every
    `devURLWatchEvery`, bounded by `devURLWatchWindow`) that stops at the first
    address. It re-reads the session record every pass and gives up the moment
    the tabs stop, change count or are taken over — the toggle is a MOVE, so a
    watch outlives its own tabs — and it runs on the SHUTDOWN-CANCELLABLE
    context, not a shielded one, because an aborted read costs only a link the
    observer finds a cycle later. `scanDevURLs` stays as that fallback: nothing
    depends on the watch succeeding.
  - A port the sweep may NOT reclaim becomes a QUESTION, never an action
    (`internal/daemon/devclash.go`, `cmd=devFreePort`). The sweep only touches
    `~/.lola/worktrees/<project>/`, so the common real-world clash — a
    `npm run dev` the human started in their own checkout — kills the dev tab and
    explains nothing: the command prints one line and exits, and `wails3 dev` or
    `vite` clears the screen on the way out, so the tab reads as "dead, no reason
    given". So a dead tab is read ONCE (`internal/portclash` → a port number,
    nothing else), lsof names the holder, and the result rides `SessionInfo`
    to a banner in the app / a `clash:` line + `F` in the TUI. Its rails:
    - Detection is one-shot per DEATH (`devClashChecked`, re-armed when the tab
      lives again): a dead pane never changes, so a second read learns nothing
      and would cost a capture plus an lsof every cycle forever.
    - lsof is asked only when the pane actually said a port was taken, and a
      holder that cannot be resolved records NOTHING — the finding's only use is
      offering to kill something.
    - The kill is a human's answer to a dialog, and the daemon re-verifies at
      that moment: session + port + pid must match the record AND that pid must
      STILL hold that port (pids are reused, and the gap to a click is
      unbounded). A group owning a live tmux pane is refused outright, as in the
      sweep, and no ps / no tmux means no kill.
    - The clash is DERIVED like `DevActive`/`DevURLs`: `markDev` drops it on any
      tab change, because it describes tabs that no longer exist.
  - `dev_commands` is deliberately NOT a `[defaults]` key (see the inheritance
    invariant): a dev command belongs to one repository, and an inherited one
    would start the wrong stack in every project that forgot to override it.
- **A review PASS runs in its own tmux session, and the pane is a DISPLAY.** With
  `visible = true` (the per-provider default) a pass runs as the hidden
  `lola review-run` inside `<sessionID>-review` on lola's tmux server, beside the
  worker and the `-shell-N` tabs (`internal/daemon/reviewvisible.go`). Rules that
  hold it together:
  - The daemon NEVER parses the pane (it wraps, scrolls, is overwritten). The
    child writes findings + an outcome class into
    `~/.lola/cache/review/<session>/` (`internal/reviewrun`), and
    `Status.Err()` maps that class back onto the SAME `Err*` sentinels a direct
    exec returns — so transports, fallback chain and retry budget cannot tell a
    visible pass from a direct one.
  - A visible claude pass uses `--output-format stream-json --verbose` and
    renders the events to plain lines (`internal/reviewclaude/stream.go`),
    because a plain `-p` review prints NOTHING until it finishes — a blank pane
    for ten minutes is not a progress display. The findings still come from the
    terminal `result` event, byte-identical to the plain pass.
  - The pane HOLDS after the pass (the child blocks forever) so the output stays
    readable; the next pass for that session replaces the whole tmux session and
    `lola kill` takes it down. Because it outlives its pass, adoption must not
    see it as an orphan: `runtime.IsAuxSession` covers it, and Adopt drops it
    only when its PARENT session is live beside it (a manual session on a branch
    ending in `-review` would otherwise vanish).
  - Everything degrades to the direct exec — no tmux, no session id, a tmux that
    refuses the session. A pane that cannot open must never cost a review.
- **The github sink is the ONLY review sink that reshapes the findings, and
  GitHub's sanitizer sets the whole design budget.** A comment body is
  sanitized server-side: CSS, `<style>` and the `style` attribute are stripped,
  so a PR comment can be STRUCTURED but never styled. `internal/reviewmd` (pure,
  dependency-free) spends the five things that survive — an ALERT callout
  (`> [!CAUTION]` / `[!WARNING]` / `[!NOTE]`, the only real colour available,
  its level DERIVED from the worst severity), emoji, bold, code spans and links
  — on a tally line over one COLLAPSED `<details>` per finding, each location
  linked to `blob/<session branch>/<path>#L<line>`. A `<details>` renders as a
  bare disclosure triangle with no box of its own; do not add markup trying to
  give it one. `postGithubSink` is its one caller; the worker hand-off, the
  notification and the Linear comment keep the raw text byte for byte.
- **The INLINE github shape is the plain comment plus anchors, and the plain
  comment stays the floor.** With `github_inline = true` (the default) the same
  findings go up as ONE `event: COMMENT` review — a summary body plus one
  anchored thread per finding (`internal/daemon/reviewinline.go`) — because only a
  review THREAD gets GitHub's reply box and "Resolve conversation" button, which
  is the entire point: the worker can close what it fixed. Its rails, in order of
  how much damage each prevents:
  - The endpoint is ATOMIC: one comment on a line outside the PR's diff rejects
    the WHOLE review with 422. So the diff is fetched first
    (`scm.PRReviewTarget` — head sha + unified diff in one call), `diffanchor`
    decides what may be anchored, and a finding whose line is not there stays in
    the summary body. NEVER post an anchor the diff did not confirm.
  - Everything degrades to `postGithubSink`'s plain comment — nil seams, an
    unreadable diff, nothing anchorable, 403/422. The ONE case that does not is a
    TRANSIENT gh failure: it leaves the settle guard unstamped and returns "done"
    so the next cycle retries the INLINE post, because silently flattening a
    review over a 502 would make the shape depend on the weather.
  - An anchor may SNAP up to `inlineAnchorWindow` (3) lines to reach the diff —
    the review instruction asks for the smallest line carrying the defect, which
    is routinely a context line just outside a hunk — and a snapped thread states
    the reported location in its own body. A comment sitting on a line it is not
    about, with nothing saying so, is worse than one in the summary.
  - The summary's tally counts EVERY finding, threads included, and names how many
    could not be anchored: the PR must never show fewer findings than the review
    produced.
  - `neutralizeBotTriggers` applies to EVERY body, thread bodies included — the
    "@coderabbitai in a finding must never start a new CodeRabbit run" guarantee
    is per-body, not per-post.
  - The worker instruction (`inlineThreadNote`) is DERIVED at send time from
    `Session.InlineReviewPRs[kind] == PR.Number`, not stashed: a hand-off is
    usually delivered long after the post that created the threads, often after a
    restart. It is lola's OWN text, appended AFTER the untrusted findings, and it
    is silent unless the threads exist for exactly this PR — a fallback comment
    must never tell an agent to resolve conversations that are not there.
- **The review instruction's FORMAT block is a CONTRACT with `reviewmd`, and its
  fields are split by AUDIENCE.** `reviewclaude`'s `-p` prompt asks for
  `**Grade:** impact=… confidence=… effort=…` (three fixed enums) + `**Gist:**`
  (one sentence) + `**Fix:** `(one sentence) + `**Detail:**` (≤4 sentences),
  because nobody reads four prose paragraphs per finding on a PR. The renderer
  puts Gist, then Fix, then the Grade chips (`<kbd>impact: high</kbd>` — `<kbd>`
  is the only allowlisted element GitHub draws as a bordered chip rather than as
  more code) inside ONE BLOCKQUOTE, and folds Detail — plus any field it does
  not know — behind a NESTED `<details>` outside it. The blockquote is
  load-bearing, not decoration: four flush blocks at equal weight read as debris
  between findings, and its left rail is the only containment a sanitized
  comment can express. Every line of the quote (including the blank ones, as a
  bare `>`) must carry the prefix or GitHub ends the quote mid-body.
  Consequences:
  - The grade vocabulary is a WHITELIST (`gradeVocab`), and unknown axes/values
    are dropped, not rendered: findings are model output and a chip reads as a
    fact. Chip order is fixed by `gradeOrder`, not by what the model wrote.
  - Renaming a field in the prompt without changing the renderer silently
    degrades every review to the pass-through path — hence
    `TestReviewInstructionPinsTheGradedShape` in `internal/reviewclaude`.
  - The FOLD is presentation, not redaction: the worker agent, notify and Linear
    still get every field raw, Detail included.
  - A body carrying neither Grade nor Gist (a `coderabbit-cli` pass, a
    pre-graded snapshot) is passed through verbatim, so old shapes still post.
  Rules: it FAILS OPEN — anything it cannot parse (a `coderabbit-cli` plain-text
  pass, a provider that ignored the format block) is posted verbatim under a
  plain `###` heading, so a formatter can never eat a review; and a location is
  linked ONLY when the repo is `owner/name`, the ref is URL-safe and the
  location is a plain `path:line` (a wrong link would point a reader at someone
  else's file). Its summary line is HTML-escaped with `<code>` spans rebuilt
  from the backticks (inline Markdown inside `<summary>` is not reliably
  rendered). It self-bounds to
  `reviewmd.MaxBytes` (15KB) UNDER `scm.postCommentMaxBytes` (16KB) so the
  head-clip there can never land mid-`<details>`; over budget it drops detail
  bodies, never findings.
- **A review PASS never runs on the observe loop.** A `claude-session` pass
  reads the PR's files, so a real PR takes 7–13 minutes; run inline it stalled
  tmux liveness, PR facts and reactions for every other session for that long,
  which is why its timeout could not simply be raised. The observer calls
  `queueReviewProviders` (watch shapes still poll inline — one bounded `gh`
  call) and `internal/daemon/reviewworker.go`'s single worker drains the queue
  one pass at a time on the cancellable run context. Two consequences:
  - `claude-session`'s default `timeout_seconds` is 900
    (`DefaultClaudeReviewTimeoutSeconds`), not the shared 300. At 300 every pass
    on a medium PR died on the deadline.
  - The once-per-PR guard is still stamped BEFORE the exec (crash safety), but
    an outcome that never ANSWERED (timeout / quota / nothing available) now
    releases it via `noteReviewOutcome` for up to `reviewMaxAttempts` tries per
    PR. Without that release a single timeout locked the PR out of review
    forever — the bug that made the feature look dead. A real answer (findings
    or clean) and a graceful skip (auth / exit error) stay final.
- **Fire once per transition.** Reactions and write-backs use persisted
  one-shot guards (`LastReactedStatus`, `WB*Done`, review's per-PR guard) so
  they don't re-fire on every 30s observer cycle.
- **Untrusted output stays out of the control loop.** `brain` summaries and
  `review` findings are derived from attacker-influenceable context (PR diffs,
  CI logs, pane text). They may go to a human (notify + Linear comment) but the
  brain summary must **never** be fed back to the worker agent; review findings
  reach the worker only through the sanitize + idle gate. The `[statusagent]`
  interpreter is stricter still: its parsed judgement reaches ONLY the wire's
  display fields (`displayOverlay` in `statusagentwire.go` is the one reader)
  — never `Status`, the axes, `AtPrompt`, dispatch counting, reactions,
  write-back, answer gating, or send-keys. Adding a reader of the overlay
  fields anywhere in the control loop breaks the design.
- **Shutdown-shielded loops.** The observer and reconcile loops run on
  `context.WithoutCancel` and are panic-guarded, with a per-exec deadline on
  every gh/tmux call so a wedged external process can't hang graceful shutdown
  at `d.wg.Wait()`. Spawn is bounded by `nativeSpawnTimeout` for the same
  reason. Preserve these when adding an exec call to those paths.
- **Secret discipline.** The Linear key and Slack webhook URL never live in
  `config.toml`, never appear in argv, a log line, or a returned error. Follow
  the existing pattern (resolve from keychain/env by *name*; sanitize
  `*url.Error`) when touching those packages. The Linear key is the one secret
  with a WRITE path in the settings UIs — it was previously settable only in the
  setup wizards, so a hand-written config could never gain one and rotating a
  key meant editing the Keychain by hand, while a keyless daemon fails every
  poll. Its rails:
  - Write-only, and NOT a form field. `ConfigService.SetLinearKey` (app) and the
    TUI's `sfSecret` field write straight to the keychain; the key is
    deliberately kept off `SettingsDTO` / the cfg write, for the reasons `[ui]`
    is (see the comment above `Themes`) plus one of its own — a whole-form
    commit would carry a secret through every unrelated save, and a validation
    failure on another tab would silently drop the key just typed.
  - Nothing reads a key back. `LinearKeyStatus` / `linearKeyHelp` resolve one
    only to learn WHETHER it resolves, and report the source's name.
  - Both surfaces mask it and clear the field after a successful store — the
    TUI's pane in particular is captured by lola's own attention parser.
  - A keychain failure still leaves a WORKING config (`api_key_env` by name) and
    says so, because the user then has to export it themselves.
- **`[ui].theme` paints BOTH surfaces, and the TUI palette is a `var` block.**
  `internal/tui/catppuccin.go` is a Go port of `desktop/frontend/src/lib/
  catppuccin.ts` — the same four flavors, the same contrast-walking token math —
  so the TUI and the app derive identical semantic colors from one identifier and
  `catppuccin-latte` genuinely lightens the TUI. Consequences:
  - `internal/tui/theme.go`'s palette is `var`, not `const`, because `applyTheme`
    repaints it. It is SEEDED with the historical navy values so a test that
    never calls `applyTheme` is unaffected.
  - A `var` reassignment does NOT update a `lipgloss.Style` built at init. Every
    package-level style derived from the palette must be declared bare and
    (re)built inside a `rebuildXStyles()` that `rebuildStyles()` calls — **adding
    a new palette-derived style without registering it there means it silently
    keeps the previous flavor's colors.**
  - `applyTheme` is called on load (`Run`) and on every reload/settings save, so
    the flavor applies without a restart. Unknown/empty id → the default flavor.
  - Both settings UIs write the key (TUI `S` → Appearance, app → Appearance); the
    app additionally live-previews. Keep the Go `config.UIThemes` list and the TS
    `THEME_IDS` list identical — `Validate` rejects anything outside the Go one.
- **Every action in the app is `<Button>`; every popover row is `<MenuItem>`.**
  `desktop/frontend/src/lib/components/Button.svelte` owns the whole ladder —
  sizes `xs`/`sm`/`md`, variants `ghost` (the default: transparent at rest, a
  `bg-sel` chip on hover, Linear's shape) / `accent` / `secondary` / `primary` /
  `danger` / `danger-solid`, plus `selected` for segmented controls, `icon` for
  square glyph buttons, `block` for full-width rows and `loading` for an action
  in flight. Do not hand-roll `rounded … px-… hover:text-…` at a call site
  again. Consequences:
  - `loading` DISABLES the button — these actions are not idempotent, and the
    dev toggle in particular stops another session's servers — but overrides the
    disabled fade with `!`, because a control that is working must not wear the
    40% of one that is dead. The in-flight flag belongs in the STORE, not in the
    button: the dev toggle has three triggers (row button, context menu, `D`)
    and a local flag would leave two of them looking inert. A call site that
    draws its own state glyph hides it while loading — the spinner takes that
    slot.
  - Every class in it is a LITERAL in the module-level maps. Tailwind scans
    source text, so a composed `` `bg-${x}` `` compiles to nothing.
  - Hover rules are `enabled:hover:`, never bare `hover:` — CSS still matches
    `:hover` on a disabled button, so a plain rule lights up a dead control.
  - **Recolouring a variant needs Tailwind's trailing `!`** (`class="text-warn!"`).
    A plain `text-warn` has the same specificity as the variant's `text-faint`
    and the winner is decided by Tailwind's order in the compiled sheet, not by
    the class attribute — the same trap applies to any width/border/gap override.
  - Five things stay hand-rolled ON PURPOSE, each commented where it lives: the
    `role="tab"` strip (`Tabs.svelte`), the theme swatches (drawn in their own
    flavor's colours), the `[defaults]` inherit chip (caption-sized, not a
    control), the card-shaped rows (project actions, kanban cards, nav rows), and
    the terminal tab chip (`SessionEmbed.svelte`) — one chip holding TWO buttons
    (label + close ×), so the wrapper paints the background and both buttons run
    `variant="bare"`; with the chip on the label, hovering the × dropped it.
  - Labels are **Sentence case** — "Open PR", "Trigger review", "CodeRabbit". The
    app was all-lowercase, which read as prose rather than as controls. Tests
    assert these strings; `getByRole("menuitem", { name })`, not `getByText`,
    because a MenuItem wraps its label beside an aria-hidden glyph.
- **No form control in the app is drawn by the OS.** A bare
  `<input type="checkbox">` and a bare `<select>` are painted by AppKit, so their
  box, tick, caret and focus ring follow the user's macOS version rather than
  this repo — two machines on the SAME build showed visibly different config
  forms (macOS 26's Liquid Glass controls against the older flat ones), which is
  a difference no screenshot can be debugged from. `Checkbox.svelte` and
  `Select.svelte` own those two; `input[type="number"]`'s stepper is killed in
  `app.css` (arrow keys still step). Rules:
  - The tick and the caret are real sibling `<svg>` elements in `currentColor`,
    never an `::after` on the input: WebKit does not reliably render
    pseudo-elements on form controls, so that version works in `wails3 dev`
    (Chrome) and disappears in the packaged app — the exact divergence these
    components exist to remove.
  - `class` on either component lands on the WRAPPER, not the control, so a
    row-level fade (`ghost()`'s `opacity-55`, `has-[:disabled]:opacity-40`) dims
    the tick/caret WITH the box instead of leaving it floating at full strength.
  - What stays native ON PURPOSE: the `<select>` popup (an AppKit menu outside
    the web view — re-drawing it means re-implementing keyboard nav, type-ahead
    and a11y) and the textarea resize grabber. `color-scheme`, written per flavor
    by `theme-runtime`, is the one lever over the popup and it is enough.
  - `Controls.test.ts` greps every `.svelte` file for a raw `type="checkbox"` /
    `<select` and fails on one, because a raw control looks perfectly fine on
    whichever macOS the author happened to be running.
- **Destructive actions confirm, and the key that does the destructive thing is
  the SHIFTED one.** On both project lists (TUI home and the cockpit rail) `x`
  stops polling (reversible) and `X` removes the `[[project]]` from config; `n`
  and `a` both open the new-project form on both. They previously disagreed —
  same key, reversible on one screen and destructive on the other. In the desktop
  app every irreversible action routes through the single `confirm` store
  (`desktop/frontend/src/lib/confirm.svelte.ts`) and its one `ConfirmDialog`, so
  a shortcut and a button ask the same way; don't add a second bespoke dialog.
  The dev toggle is `D` in BOTH surfaces for two reasons: bare `d` is already the
  doctor overlay in each, and activating a session stops another session's
  running processes — heavy enough for the shifted key even though it is not
  destructive.
- **Both TUI config forms guard unsaved edits, and the guard has a hole only a
  gate can close.** `formModel` and `settingsForm` each keep a `baseline`
  snapshot; `esc` on a dirty form arms `confirmDiscard` (y/n) instead of
  cancelling. Two things to preserve when touching them:
  - `rebase()` must be called after **every async fill** (repo auto-detection,
    Linear team/label loads). Those are not human edits, and without the rebase
    an untouched form starts claiming unsaved changes.
  - `ctrl+c` is handled at the TOP of `rootModel.Update`, *ahead* of the form
    routing, so the discard prompt can never see it. It is explicitly gated on
    `m.form == nil && m.settings == nil`. Removing that gate silently restores
    "reflexive ctrl+c throws away the whole form".

## Testing conventions

- 46 `_test.go` files; the daemon package is the densest. Inject fakes via the
  `Daemon` struct's seam fields and `linear.API` / `fake.go`. Use `$LOLA_HOME`
  (a `t.TempDir()`) to isolate all runtime state.
- Definition of done for a daemon change (per `agent-rules.md`): cover filter
  construction per mode, pagination, `Budget` math, both dedup modes incl. seen
  pruning, cross-poll dedup, labelIds delta, identifier-vs-UUID usage, and the
  native lifecycle (spawn+rollback, adopt classification, store-driven
  `liveCounted`, fail-closed reconcile revert).

## Desktop app (`desktop/`)

`desktop/` is **Lola**, the native macOS app (Wails 3 + Svelte 5 runes +
Tailwind v4 + xterm.js) that mirrors the TUI's flight-deck plus a live
terminal-grid overview. It is a **package inside this Go module** (not a separate
module) precisely so it can reuse `internal/protocol`, `internal/config`,
`internal/doctor`, `internal/linear`, `internal/secrets` — Go's `internal/` rule
forbids that from a sibling module. It is a **client of the same daemon socket**
the TUI uses; it never embeds the daemon, and it drives `tmux -L lola` directly
for terminal streaming. Six bound Wails services: `DaemonService` (every
protocol command + daemon start/stop/restart), `TermService` (capture-pane
snapshots for the grid + a live `tmux attach` PTY for the focused terminal),
`ConfigService` (read/write config.toml + first-run setup), `DoctorService`,
`LinearService` (team metadata for the cascading pickers), `UpdateService`
(GitHub-Releases self-update — see the update gotcha below). Note there is ONE
project form, not a project form plus a poll form: a project IS the poll unit,
so repository setup / filter / labels / write-back are TABS of a single overlay
(same in the TUI — `internal/tui/form.go`, which absorbed the old
`projectform.go`). Requires the
`wails3` CLI (`go install github.com/wailsapp/wails/v3/cmd/wails3@latest`), a
distinct binary from the v2 `wails`. See `desktop/README.md`.

**Gotchas (learned the hard way — don't rediscover them):**

- **`wails3 task build` only rebuilds the loose `bin/Lola`. The `.app`
  bundle is a copy made by `wails3 task package`.** So `open bin/Lola.app`
  after a `build` launches the *old* bundled binary — every source change looks
  like a no-op. **Iterate with `wails3 dev`** (live source, Web Inspector);
  `wails3 task package` when you want the `.app`.
- **The Dock/Finder/Cmd-Tab label comes from the `.app` DIRECTORY name, not the
  plist.** macOS honours `CFBundleDisplayName` only when it matches the on-disk
  bundle filename case-insensitively; otherwise the filename wins (which is why
  the Dock used to read `lola-desktop.dev` even though `CFBundleName` was
  `lola`). So `desktop/Taskfile.yml`'s `APP_NAME: "Lola"` is the load-bearing
  value: it names `bin/Lola`, `bin/Lola.app` and `bin/dev/Lola.app`, and
  `build/darwin/Info*.plist`'s `CFBundleExecutable` must stay in lockstep with it
  (the bundle task copies `bin/{{.APP_NAME}}` into `Contents/MacOS/`; a mismatch
  makes the bundle unlaunchable). `.github/workflows/build.yml`'s `APP_PATH` must
  match too — sign/notarize/staple/DMG all read it. The dev bundle is
  `bin/dev/Lola.app`, not `bin/Lola.dev.app`, for the same reason. Never touch
  `CFBundleIdentifier` (`dev.sushi.lola.desktop`) — changing it resets TCC grants
  and orphans Dock tiles. Everything else stays lowercase `lola`: the CLI, the
  socket, `~/.lola`, `tmux -L lola`, Go module paths, the DMG asset name.
- **WebKit ≠ Chrome for flex.** The production WKWebView does **not** stretch a
  `display:flex` child inside a flex **column** (it collapses to content width);
  Chrome does, so it looks fine in a browser and broken in the app. Use **CSS
  grid** for fill-the-parent layouts (grid cells stretch reliably), or an
  explicit width — never rely on `align-items:stretch` for a flex-container child
  in a column. Verify layout in the actual `.app`, not just Chrome.
- **The app SHIPS the CLI, and `desktop/lolabin.go` owns which one runs.** The
  app is a client: it starts a daemon by exec'ing `lola run` and cannot re-exec
  itself the way the TUI does. The DMG used to carry only the `.app`, so a fresh
  install died in the first-run wizard on "lola binary not found on PATH". The
  bundle now carries the CLI at `Contents/Resources/bin/lola`
  (`build/darwin/Taskfile.yml`'s `build:cli` → `create:app:bundle`; the path is
  pinned against `bundledRelPath` by a parity test). Resolution order is
  `$LOLA_BIN` → `PATH` → bundled, and **PATH stays ahead on purpose** — a
  developer's `go install` build is the dev loop below, and preferring the
  shipped copy would make `go install` look like a no-op. Consequences:
  - The bundled copy is the FLOOR, so the two can disagree in version;
    `DaemonService.CLIInfo` reports both and the doctor overlay flags the skew
    rather than leaving it to be debugged as a missing feature.
  - `InstallCLI` SYMLINKS (never copies) the bundled binary onto PATH, so the
    updater's bundle swap carries the CLI with it. It refuses to replace
    anything that is not a symlink into a `.app` — a hand-installed CLI is not
    ours to overwrite.
  - Only `lola` is vendored. `tmux` must NOT be: `tmux -L lola` is a
    client/server pair shared with the CLI, and mixing builds hits a
    protocol-version mismatch. `git`/`gh`/the coding agent are the user's own
    installs (auth, subscriptions) — hence the PATH work below.
- **`ensurePATH` probes the LOGIN SHELL, not a fixed list.** A Finder-launched
  `.app` inherits `/usr/bin:/bin:/usr/sbin:/sbin`, and the old two-entry
  Homebrew list could not find a `claude` installed through a version manager
  (mise/asdf/fnm/volta) or a `lola` in `~/go/bin`. It now runs `$SHELL -l -c`
  once at startup, bounded, reading its answer from a SENTINEL rather than from
  "the output" — login rc files print banners, and a PATH assembled from
  someone's shell greeting would be handed straight to exec. A failed probe
  falls back to the static list, which is a superset of the old behaviour.
- **The daemon does not hot-reload its own binary.** After `make build`, a
  still-running `lola run` keeps the old code — a daemon predating a command
  answers `unknown cmd "<x>"` (e.g. `projects`). Restart it (TUI `^r`, the app's
  restart button, or stop+respawn) to pick up the new binary — and note that the
  restart resolves `$GOPATH/bin/lola`, NOT the repo's `./lola`, so a `make build`
  without a `go install` restarts onto the same old code (see "`make build` alone
  never reaches the running daemon" above, including how to check what the
  process is really executing). The desktop store
  therefore uses `Promise.allSettled` so one unknown command can't blank the rest
  of the UI. (`setsid` is Linux-only; on macOS detach with `nohup … & disown`.)
- **Bare keys are the frontend's; ⌘ chords are the macOS menu's — never both.**
  Every shortcut in `App.svelte`'s `onKey` is an UNMODIFIED key, so it bails on
  `isChord` (`lib/keys.ts`: meta/ctrl/alt, never shift — `V`/`G`/`N`/`S`/`R`/`P`
  are real bindings). Without that, ⌘C ran a review instead of Copy and ⌘X asked
  to kill a session. Modifier shortcuts therefore live in the **Session menu**
  (`installAppMenu` / `newSessionMenu` in `desktop/main.go`), which emits
  `app:session-action` for the frontend to apply to its selection — the backend
  cannot know it. Two consequences: AppKit dispatches a menu accelerator BEFORE
  the WKWebView, so those chords work even while a live terminal holds the
  keyboard (a JS handler there never fires — xterm's textarea reads as "typing"),
  and a duplicate accelerator silently shadows, which is why `Force Reload` was
  moved off ⌘⇧R to ⌥⌘R. Adding one: keep it Cmd-based (Ctrl/Alt belong to
  tmux/zellij inside the pane), avoid every Edit-menu chord and ⌘⌫ (delete-to-
  line-start in a text field), and list it in `HelpOverlay.svelte`.
- Fonts: the terminals + mono UI use bundled **JetBrains Mono**
  (`@fontsource/jetbrains-mono`, imported in `main.ts`); xterm re-fits on
  `document.fonts.ready` so cell metrics match once it loads.
- **A clickable URL in a terminal is xterm's job, not the multiplexer's, and it
  must NOT use `window.open`.** xterm ships no link handling by default, which
  is why a printed `http://127.0.0.1:8000` was dead text — nothing to do with
  tmux (see the `multiplexer-choice` decision: lola stays on tmux). Both link
  kinds are wired in `LiveTerminal.svelte`: `WebLinksAddon` for plain-text URLs
  and `term.options.linkHandler` for OSC 8 hyperlinks. Both call
  `store.openURL`, which asks the DAEMON (`cmd=openURL`) — that is where the
  http(s)-only guard lives, and terminal text is untrusted (a log line can print
  `file://` or `javascript:`). `window.open` in a WKWebView would open the page
  *inside* the app, which is not a browser. Caveat worth knowing before
  debugging a "dead" link: with `[tmux].mouse = true` (off by default) tmux
  grabs mouse events, so clicks go to tmux and never reach xterm.
- **App icon is icns-only — do NOT re-add `CFBundleIconName` / `Assets.car`.**
  On macOS 26 (Tahoe) the Dock prefers the Liquid Glass `Assets.car` icon
  whenever `CFBundleIconName` is set, and Wails' generated `Assets.car` drops
  the art into Apple's inset icon-grid ([wails#4163](https://github.com/wailsapp/wails/issues/4163)),
  so the tile floats visibly smaller than neighboring icons. We deliberately
  ship **only** a full-bleed `icons.icns` (Tahoe masks it to the system radius
  and it fills the Dock slot): `build/Taskfile.yml`'s `generate:icons` omits
  `-iconcomposerinput`/`-macassetdir`, `build/darwin/make-icns.sh` rebuilds the
  icns from `darwin/appicon-rounded.png` with `sips`+`iconutil` (full-bleed
  squircle, no Wails "Big Sur tray"), and `CFBundleIconName` is stripped from
  both `Info.plist`s. `build/appicon.svg` is the canonical master (the figure
  is placed to fill the tile; the viewBox bounds the overflow). The unused
  `build/appicon.icon/` Icon Composer source is kept only in case Liquid Glass
  is revisited — re-enabling it reintroduces the float.
- **Self-update assumes a PUBLIC repo — no separate releases repo.**
  `UpdateService` (`desktop/updatesvc.go` + the pure `desktop/internal/update`
  leaf) checks `GET /repos/sushidev-team/lola/releases/latest` **anonymously**
  and installs the attached universal DMG by mounting it, `ditto`-staging the new
  bundle, and running a detached script that swaps the `.app` after the app quits.
  Anonymous only works because the repo is public — making it private again 404s
  the check (rize-reporting needs a `*-releases` mirror precisely because ITS
  source repo is private; lola must not copy that). The compiled `main.version`
  (default `"dev"`, injected via `-ldflags -X main.version=` in
  `build/darwin/Taskfile.yml`'s production branch, passed `VERSION=<tag>` by the
  `desktop` job in `.github/workflows/build.yml`) is the checker's "current"
  version; a non-semver value (`dev`) means "always offer the release". Update
  cadence/skip live in `~/.lola/desktop-update.json`, NOT `config.toml` — the
  daemon and TUI never read them. The `desktop` job in `.github/workflows/build.yml`
  needs the Apple signing secrets (same names as rize) or it fails while the CLI
  release still succeeds; a notarized DMG is what keeps Gatekeeper quiet on the
  auto-installed swap. Two rules follow from the DMG arriving AFTER the release:
  - **"A newer version exists" and "there is a build to install" are separate
    facts, and the UI must not merge them.** The release is published the moment
    release-please merges; its signed+notarized DMG is attached minutes later by
    the `desktop` job — and never, if that job fails. The store keeps
    `available` (newer version) apart from `installable` (`available` + a
    `downloadURL`), because folding the asset check into `available` told
    everyone on the previous version "✓ you're up to date" for the whole window
    — silently, and permanently after a failed signing job. Without a build,
    `UpdateOverlay` names the version and offers the release page.
  - **A manual check must be able to answer differently.** `Checker` caches the
    release for `CacheDuration` (1h) per app run, so "Check again" was a no-op
    against exactly the answer that goes stale first (the DMG landing on an
    already-published release). `CheckForUpdates(force)` clears that cache, every
    manual check passes `force`, and opening the overlay always re-checks rather
    than reusing what the launch auto-check saw.
- **Releases are release-please, not manual `v*` tags.** `.github/workflows/
  release-please.yml` maintains a release PR from Conventional Commits; merging
  it tags the repo + creates the GitHub Release, then calls the reusable
  `build.yml` (goreleaser CLI archives + the signed desktop DMG). A
  release-please tag does NOT fire a `push: tags` workflow (GitHub blocks that
  recursion), which is why `build.yml` is invoked via `uses:`, not a tag trigger.
  goreleaser runs `changelog.disable` + `release.mode: append` so it uploads
  artifacts onto the release-please-authored release WITHOUT clobbering its
  notes. Version lives in `.release-please-manifest.json`; `release-please-config.json`
  also bumps `desktop/build/config.yml`'s `info.version`.

## Reference docs

- `README.md` — user-facing: full command list, config reference (every
  `[section]` and key), runtime layout, launchd install, secrets.
- `config.example.toml` — complete commented config.
- `agent-rules.md` — the build spec / rule list (with AO-bridge deltas).
- `SPEC.md` / `PLAN.md` — original spec and phased roadmap (P0–P9).
