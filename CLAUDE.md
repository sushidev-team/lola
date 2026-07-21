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

## Desktop app (`desktop/`)

`desktop/` is **lola-desktop**, a native macOS app (Wails 3 + Svelte 5 runes +
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

- **`wails3 task build` only rebuilds the loose `bin/lola-desktop`. The `.app`
  bundle is a copy made by `wails3 task package`.** So `open bin/lola-desktop.app`
  after a `build` launches the *old* bundled binary — every source change looks
  like a no-op. **Iterate with `wails3 dev`** (live source, Web Inspector);
  `wails3 task package` when you want the `.app`.
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
- Fonts: the terminals + mono UI use bundled **JetBrains Mono**
  (`@fontsource/jetbrains-mono`, imported in `main.ts`); xterm re-fits on
  `document.fonts.ready` so cell metrics match once it loads.
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
