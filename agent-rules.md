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
- `[brain]` is a lola-INTERNAL helper that always shells `claude -p` regardless
  of the coding-agent choice — it is NOT the pluggable coding agent and must not
  follow the `agent` setting. The same is true of the REVIEW providers: an
  agent-family provider names its own agent (`codex-session` runs codex) and is
  configured per provider, so it too never follows the session's `agent` setting.
- **[changed]** The review helper generalized from the two hardcoded tables into
  a provider CATALOG (`[[review.provider]]`) with SEVEN kinds in three FAMILIES:
  CLI passes (`coderabbit-cli`, `custom-cli`), bot WATCHES (`coderabbit-watch`,
  `bot-watch`) and AGENT passes (`claude-session`, `codex-session`,
  `opencode-session`). Every family is a swappable slot — which agent reviews,
  which CLI reviews, and whose GitHub review is relayed are all config. The legacy
  `[review]`/`[coderabbit]` tables still work forever (synthesized into
  `coderabbit-cli`/`coderabbit-watch` providers); catalog + a non-empty legacy
  table is a HARD validation error, resolved one-way by
  `lola config migrate-review`.

## Review providers (flexible review)

- **[changed]** At most ONE provider per KIND (guards key by kind) — which is
  WHY every review agent gets its own kind rather than sharing one with an
  `agent =` field: two agents can then run as primary and fallback for the same
  session. Two execution SHAPES: PASS (the cli + agent families — exec, return
  findings synchronously, per-PR guard) and WATCH (the watch family — poll the
  PR, watermark guard). A
  provider runs per session only if enabled AND not referenced in any other
  enabled provider's `fallback` (a fallback-only provider runs ONLY when reached
  via a chain — prevents double-review/double-hand-off).
- **[changed]** Fire-once guards are KIND-KEYED maps in session state
  (`ReviewedPRs`/`ReviewWatermarks`/`PendingHandoffs`/`PostedGitHubPRs`), stamped
  BEFORE the exec (a pass chain stamps the PRIMARY kind so a fell-through fallback
  never re-fires), migrated idempotently from the old scalars (`migrateReviewState`,
  run on Store load AND in Adopt) and carried through Adopt. The synthesized legacy
  providers reuse the same fixed kind keys, so an upgraded/adopted session is NOT
  re-reviewed.
- **[changed]** Transports = a multiselect over `{lola, github, linear}`. `lola`
  is ALWAYS present (force-appended on resolve) and expands to the notify sink
  (gated by the `notify` bool) + the worker hand-off sink (gated by
  `send_to_agent`) — muting either independently is what preserves the legacy
  `notify=false` opt-out. `github`/`linear` are additive opt-in public sinks.
- **[changed]** Only the worker hand-off sink SANITIZES + idle-gates: it reuses
  the existing `AtPrompt` atomic idle-gate + `sanitizeAgentText` + defer-never-drop
  VERBATIM, never run as a command, pending stash keyed `PendingHandoffs[kind]`.
  notify/github/linear are HUMAN sinks — full untrusted findings verbatim, NO
  sanitize, NEVER re-fed into the control loop. Per-PROVIDER labels/preambles so
  a `codex-session`'s findings read "Codex review" and a `bot-watch`'s name the
  bot it watches — never mislabeled "CodeRabbit". With the catalog pluggable, WHO
  produced a finding is the one thing a reader cannot infer from the finding
  itself, so it is always stated. A watch's author reaches those labels and the
  worker's pane, so it is sanitized to login characters and clipped
  (`botDisplayName`) before it is interpolated into any text lola generates.
- **[changed]** The `github` sink is `gh pr comment <pr> --repo <repo> --body-file -`
  ONLY (body on STDIN, never argv) — a plain PR comment, never `gh pr review`
  (no approve/request-changes). PASS shapes only; validation FORBIDS `github` on
  a WATCH kind (its feedback is already on the PR — a self-feedback loop).
  Idempotent per PR via `PostedGitHubPRs[kind]`: a SUCCESS or a PERMANENT gh error
  (422/403) stamps the settle guard + logs once; a TRANSIENT error (5xx/timeout/
  missing repo) leaves it unstamped to retry next cycle. Empty body = skip.
  Fail-closed: missing repo / gh-not-authed = silent skip.
- **[changed]** lola NEVER triggers a new run of a review bot. The github sink
  runs every posted body through `neutralizeWatchedBots` (`reviewer.go`),
  inserting a zero-width space after the `@` of any `@coderabbit`/`@coderabbitai`
  mention AND of any bot lola has an ENABLED watch configured for (one
  case-insensitive alternation — GitHub logins are case-insensitive), so a
  findings body that happens to name the bot can't be parsed as a command and
  kick off a fresh run (which would also burn a review credit). The coderabbit
  pattern is unconditional so the historical guarantee holds with or without a
  watch; the configured half generalizes it with the catalog. Applies to the
  github sink ONLY — notify/linear/worker never reach the bot. This is what makes
  a WATCH-ONLY posture (a single watch provider: poll + relay the bot's own
  auto-review, never exec a review CLI, never post to the PR) safe even when
  paired with a github-posting agent/cli provider.
- **[changed]** Fallback (PASS-SHAPE ONLY): a provider that CAN'T answer
  (`ErrNotFound`/`ErrTimeout`/`ErrQuota`/binary-unavailable) advances to the next
  configured fallback kind; the result routes under the PRIMARY's transports.
  `ErrAuth`/`ErrExit` are a graceful skip that does NOT fall through (fail-closed:
  auth is an operator fix; a genuine failure must not burn the paid fallback).
  Each exec self-bounded by its own `timeout_seconds`; the whole cycle stays under
  the shared shutdown-abortable `reviewCycleCtx`. Chain exhausted / graceful skip
  leaves the guard SET and logs once, never errors per cycle, never blocks lifecycle.
- **[changed]** WATCH cannot fall back. A review bot posts "out of reviews" as an
  ordinary PR comment (non-empty, `err==nil`, classifier-undetectable), so a watch
  has no quota signal — validation forbids `fallback` on a watch kind. A
  quota->fallback chain requires a cli or agent provider (whose exit/stderr
  carries the quota signal). Agent-to-agent is the common case: `claude-session`
  with `fallback = ["codex-session"]` hands the review to codex on a usage limit.
- **[new]** An AGENT-family provider is one bounded, READ-ONLY headless review by
  the named agent: `internal/reviewagent` (the generalized former
  `internal/reviewclaude`) drives claude|codex|opencode behind ONE Client, and
  `internal/agent.ReviewArgs` owns each one's argv. The instruction, the diff on
  STDIN, the caps and the Err* sentinels are IDENTICAL across agents, so the
  chain cannot tell them apart. A review REPORTS; it never writes — each agent is
  launched in its most restrictive non-interactive posture (claude: headless
  defaults; codex: `--sandbox read-only`; opencode: NO `--auto`), the deliberate
  opposite of the unattended worker launch, so a prompt injection in the diff
  cannot turn the reviewer into a writer.
- **[new]** The VISIBLE pass narrates per agent: claude prints nothing until it
  finishes, so its visible run asks for `--output-format stream-json` and lola
  renders the events; codex and opencode already narrate on STDERR and have it
  teed to the pane. Both stream shapes still capture stdout under the SAME cap —
  teeing never widens a cap. Both reach the child as PIPES, never a TTY, which is
  what makes codex and opencode put their answer on stdout at all.
- **[new]** The two GENERIC kinds carry no tool of their own, so validation
  REJECTS an enabled `custom-cli` with no `command` and an enabled `bot-watch`
  with no `author` — an empty one would exec or poll nothing. The check is gated
  on `enabled`, so a half-written disabled entry never blocks a reload. The
  RUNTIME fails closed independently (`unconfiguredKindReason`), because
  `Validate` is not fatal at startup and both empty values fall back to
  CodeRabbit downstream: such a provider is disabled and named in the startup
  warning rather than silently running the wrong vendor.
- **[new]** An agent's STDERR is its NARRATION (codex/opencode print the whole
  review there), so the quota scan over stderr runs ONLY on a failed run, stderr
  is retained by a TAIL buffer (a CLI's fatal error is its last line; the head
  cap kept the prose and discarded the error), and the auth cues are PHRASES
  rather than the bare substrings `auth`/`login`. stdout keeps its HEAD — there
  the payload is the findings, most severe first — and its own shortness gate. Their
  defaults are per-kind and applied BEFORE the explicit keys overlay
  (`applyKindDefaults`): a `bot-watch` deliberately gets NO author default, or it
  would be silently identical to a `coderabbit-watch`.
- **[new]** `base_flag` (cli family) is how the base branch reaches an arbitrary
  review CLI: `<BaseFlag> <base>` appended to the argv, defaulting to `--base`,
  and an explicit EMPTY value appends nothing at all (a tool that takes no base
  argument). `internal/review` holds no default for it — config does — so only an
  explicit `base_flag = ""` reaches the client empty.
- **[new]** The daemon names ONLY the kinds it must match on (the two legacy
  guard keys and the `lola coderabbit` alias target). Everything else is driven by
  the config-side family predicates (`config.ReviewAgentFor` / `IsCLIKind` /
  `IsWatchKind`), and the per-kind exec seams live in ONE map (`d.passRuns`), so
  adding a review agent needs no daemon field, no seam switch case and no test
  hook. Both UIs generate their provider editors from `config.ReviewProviderKinds()`
  (the app via `ConfigService.ReviewKinds()`), so a new kind is offered by both
  with no UI edit; a frontend parity test pins its fixture against the Go list.
- **[changed]** Self-feedback guard (only when BOTH a github pass provider AND a
  watch are configured): the gh authed login is resolved ONCE (memoized
  `scm.Client.AuthedLogin`, `gh api user --jq .login`) and passed to the watch to
  drop lola-posted comments — ZERO new per-cycle gh exec. Resolution failure =
  fail-open (skip the filter; the default `author="coderabbitai"` won't match
  lola's login anyway). Combined with the github-off-watch validation, a
  lola-posted comment is never re-ingested by its own watch.

## Caps

- **[changed from AO bridge] [changed: two-axis status]** budget =
  min(poll.concurrency_cap, global_cap − liveCounted). liveCounted counts ONLY
  **native** sessions in the store whose rolled-up status occupies a slot
  (`state.HoldsSlot`): `working`, `needs_input`, `draft`, `ci_failed`,
  `changes_requested`, `ci_pending`, `merge_conflict` (the engine is actively
  re-prompting a conflicting agent to rebase, so its runner is busy).
  Parked-for-review (approved, review_pending) and terminal (merged, closed,
  dead, session_ended) don't count, so held PRs don't stall pickup. (Was: AO
  sessions in `ao.counting_states`; merge_conflict previously did not count.)
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
- **[changed from AO bridge] [changed: two-axis status]** The observer tracks
  PR/CI via `gh` (scm.PRForBranch → state.DeriveDelivery) onto the DELIVERY
  axis, and hooks/pane/tmux-activity onto the AGENT axis; `state.Rollup` is the
  ONLY producer of the one-string status (`internal/state` owns the whole
  vocabulary and every counting/attention table). A closed-unmerged PR notifies
  once and stops shielding its issue from the orphan revert. (Was:
  scm.DeriveStatus as the single collapsed derivation.)
- **[new: statusagent]** The optional `[statusagent]` interpreter (bounded
  `claude -p`, default sonnet) may OVERLAY the displayed agent state and add a
  headline — display only, `≈`-marked, confidence/freshness/supersession gated
  daemon-side. It must never touch Status, the axes, slot counting, reactions,
  write-back, answer gating, or send-keys.

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
