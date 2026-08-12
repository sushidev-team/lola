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
- `internal/gitrepo` — resolves a checkout's GitHub `owner/name` from its git
  remotes (upstream, then origin) so the project forms can prefill
  `[[project]].repo`. Local git only — no network, no `gh`. Deliberately NOT in
  `internal/scm` (gh-only) or `internal/config` (never execs). **Fails closed**:
  every unknown returns `""`, because an empty repo merely disables the open-PR
  check while a wrong one would make `gh pr list --repo` answer about someone
  else's repository.
- `internal/secrets` / `internal/notify` / `internal/brain` / `internal/review`
  / `internal/attention` / `internal/doctor` — Linear key resolution
  (keychain→env), best-effort desktop/Slack notify, opt-in headless-claude
  summarizer, opt-in CodeRabbit QA pass, pane→answerable-question heuristic
  parser (agent-aware), structured health checks.
- `internal/reviewmd` — presentation-only leaf (stdlib): renders a provider's
  plain findings into the GitHub Markdown posted on the PR. One caller
  (`postGithubSink`); see the invariant below.
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
  what closes the wider gate against a re-send. The gate admits a THIRD state,
  and that one needs live evidence: a session the observer's pane reconcile
  parked on `AgentIdle` (both of its paths close `AtPrompt` and neither opens a
  notification, so such a session was permanently unreachable) qualifies only
  once `handoffPromptProof` → `paneWaitingNow` captures the pane and classifies
  it `ActivityWaiting`. It never short-circuits on `AtPromptVerified` — a hook
  verdict from before the pane went quiet is not evidence about now — and any
  capture failure or non-waiting classification defers.
- **Teardown is THREE things, and each has its own fail-closed rule.**
  `runtime.Kill` (the merged-cleanup path and `lola kill`) takes down: (1) the
  agent's tmux session AND its auxiliary sessions — `<id>-shell-N` tabs and the
  `<id>-review` pane are SEPARATE tmux sessions, so killing only the agent left
  them running against a worktree about to be deleted; (2) the worktree, dirty-
  safe (`ErrDirty` unless forced); (3) the local branch, but only when
  `Session.OwnsBranch()` (a `pr` session's Branch is UPSTREAM — deleting it
  destroys someone else's ref). Rules that hold it together:
  - The aux sweep is BEST-EFFORT: a tmux that cannot answer logs and continues,
    because these are display surfaces and the caller retries the whole cleanup
    on error — a stuck shell tab must never block a worktree removal forever.
  - Matching is `parent + ^(-shell-\d+|-review)$`, anchored at BOTH ends:
    `lola-fe-42` is a prefix of `lola-fe-420-shell-1`, and a loose suffix test
    made one session's teardown kill a live sibling's tab.
  - A missing worktree directory no longer ends teardown early — it deletes the
    branch anyway (`worktree.DeleteBranch`), because a session whose checkout
    was already gone otherwise left its branch behind forever.
  - A DIRTY worktree keeps both the checkout and its branch. That is the whole
    gate: uncommitted work is the one thing teardown never discards.
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
  `*url.Error`) when touching those packages.
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
  square glyph buttons and `block` for full-width rows. Do not hand-roll
  `rounded … px-… hover:text-…` at a call site again. Consequences:
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
- **Destructive actions confirm, and the key that does the destructive thing is
  the SHIFTED one.** On both project lists (TUI home and the cockpit rail) `x`
  stops polling (reversible) and `X` removes the `[[project]]` from config; `n`
  and `a` both open the new-project form on both. They previously disagreed —
  same key, reversible on one screen and destructive on the other. In the desktop
  app every irreversible action routes through the single `confirm` store
  (`desktop/frontend/src/lib/confirm.svelte.ts`) and its one `ConfirmDialog`, so
  a shortcut and a button ask the same way; don't add a second bespoke dialog.
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
- **The daemon does not hot-reload its own binary.** After `make build`, a
  still-running `lola run` keeps the old code — a daemon predating a command
  answers `unknown cmd "<x>"` (e.g. `projects`). Restart it (TUI `^r`, the app's
  restart button, or stop+respawn) to pick up the new binary. The desktop store
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
  `desktop` job in `.github/workflows/release.yml`) is the checker's "current"
  version; a non-semver value (`dev`) means "always offer the release". Update
  cadence/skip live in `~/.lola/desktop-update.json`, NOT `config.toml` — the
  daemon and TUI never read them. The `desktop` job in `.github/workflows/build.yml`
  needs the Apple signing secrets (same names as rize) or it fails while the CLI
  release still succeeds; a notarized DMG is what keeps Gatekeeper quiet on the
  auto-installed swap.
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
