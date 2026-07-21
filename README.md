# Lola

[![CI](https://github.com/sushidev-team/lola/actions/workflows/ci.yml/badge.svg)](https://github.com/sushidev-team/lola/actions/workflows/ci.yml)

Named after *Lola rennt* — run, observe, run again.

`lola` is a single Go binary that watches [Linear](https://linear.app) for
issues matching a filter (team → project → cycle → workflow state → labels →
assignee) and spawns its **own** coding-agent session for each one: a dedicated
git worktree, a tmux session, and Claude Code running inside it.

**lola owns the whole run.** For every matched issue it creates a git worktree
from the project, runs the project's `post_create` setup, starts
Claude Code in a fresh tmux session with the issue as its briefing, and marks
the issue as picked up (label flip or seen-file) so it is never dispatched
twice. A read-only observer then tracks each session's tmux liveness and its
PR/CI state via `gh`.

**The coding agent is configurable.** By default lola runs **Claude Code**, but
each session can instead run the **OpenAI Codex CLI** (`codex`) or **opencode**
(`opencode`) — set globally in `[defaults].agent` and overridable per repository
with `[[project]].agent` (`claude` | `codex` | `opencode`, default `claude`).
All three get full lifecycle-callback parity; see
[The coding agent](#the-coding-agent) for how each one is launched and wired.

One binary, two roles:

- `lola run` — the daemon (the TUI starts it on open by default; or launchd
  keeps it alive — see [Running the daemon](#running-the-daemon-launchd-vs-tui))
- `lola` / `lola tui` — the TUI client (manage projects and their polling, drive
  per-project actions; starts/restarts/stops the daemon)
- every other subcommand talks to the daemon over the unix socket
  `~/.lola/lola.sock` (newline-delimited JSON)

The config file is the single source of truth; the TUI edits it and then
signals the daemon to reload.

## Build

Requires Go (module deps are vendored/pinned in `go.mod`; no network needed
beyond the module cache).

```sh
make build   # produces ./lola
make vet
make test
```

The Makefile sets a repo-local `GOCACHE` so builds work in sandboxed shells.

## Quick start

1. Build and install the binary somewhere absolute (launchd has no login-shell
   `PATH`):

   ```sh
   make build
   cp lola /usr/local/bin/lola
   ```

2. Make sure the native runtime's tools are on your `PATH`: `tmux`, `git`,
   `gh`, and the **configured coding agent's binary** — `claude` (the default),
   or `codex` / `opencode` if you select one (see
   [The coding agent](#the-coding-agent)). The daemon health-gate refuses to
   spawn while any of them is missing and reports it in `lola status`.

3. Store your Linear API key in the macOS Keychain (see
   [Secrets](#secrets)):

   ```sh
   security add-generic-password -a "$USER" -s lola-linear -U -w
   ```

4. Register at least one repository as a `[[project]]`, then give it a `team_id`
   (and the other polling fields) to start it watching Linear — start from
   [`config.example.toml`](config.example.toml), or run `lola` and build your
   first project in the TUI (it fetches teams/projects/states/labels from Linear
   as you go). A project with no `team_id` is still valid — it just doesn't poll,
   and is used for manual worktrees / opening PRs by hand.

5. Test a project's poll without side effects (the name is the project name):

   ```sh
   lola poll nori-app --once --dry-run
   ```

6. Install the LaunchAgent (see [launchd install](#launchd-install)) so the
   daemon runs permanently, or just run it in a terminal:

   ```sh
   lola run
   ```

## Commands

| Command | Description |
| --- | --- |
| `lola` / `lola tui` | Open the TUI. The landing **cockpit** shows a rail of polling projects and the live session view. On first run — no `config.toml` yet — this enters the setup wizard first. Keys: `p` open the **project list**, `d` inline health report, `P` edit the selected project, `S` global settings editor (`[defaults]`/`[notify]`/`[brain]`/`[review]`/`[coderabbit]`), `^r` restart the daemon (brings up the newest build), `^x` stop it (self-managed mode only). Enter on a project opens its **detail hub** — open a PR (picker → shell), start a Linear ticket (picker → worktree + agent), new manual worktree, manage the project's polling, view its sessions, edit the project. These hub actions are TUI-only (socket commands under the hood), not separate CLI subcommands. |
| `lola setup` | Run the first-run configuration wizard (Linear key → Keychain, one `[[project]]`, defaults) and write `config.toml`. Re-runnable any time. |
| `lola doctor` | Print an aligned health report (tmux/git/claude/gh on PATH, Linear key readable, daemon socket, config validity, per-project repos); exits 1 on a critical failure. Never prints the key value. |
| `lola run` | Start the daemon (this is what launchd invokes) |
| `lola stop` | Graceful shutdown: finish in-flight tick, close socket, exit 0 |
| `lola status` | Table per polling project (keyed by project name): enabled, last run, last spawn, running, last error — plus `runtimeOk` / `linearOk` health flags |
| `lola enable <name>` / `lola disable <name>` | Live pause/resume of one polling project's poll, keyed by project name (no restart) |
| `lola poll <name> --once [--dry-run]` | Run one polling project's tick now (keyed by project name); `--dry-run` prints matches (including cross-project overlaps) with **no** side effects — no spawn, no label flip, no seen write |
| `lola open <project> <branch\|PR#>` | Manually check out a branch or PR of a project into a **throwaway** worktree with a plain shell — no coding agent — so you can run and test it (e.g. review a PR). A bare number is a PR (fetched via `refs/pull/<n>/head`, works across forks); anything else is a branch. The worktree is **detached HEAD**, so teardown never touches the upstream branch. It shows in the sessions view (status `shell`) and never enters the reaction / write-back / review loop; tear it down with `lola kill <session>` (the printed message names it). In the TUI, press `O`. |
| `lola attach [session]` | Hand the terminal to tmux on lola's isolated server. With **no argument** it (re)builds an aggregate viewer session with **one tab per live agent** (windows linked from each session, so switching tabs pages through every agent) and attaches to it — "attach once, see all agents". With a **session id** it attaches straight to that one. Talks to tmux directly (works even if the daemon is down); detach with `C-b d`. The viewer is a linked view — detaching or rebuilding it never touches the agent sessions. |
| `lola kill <session> [--force]` | Terminate a session's agent (tmux) and clean up after it. A **clean** worktree is removed and the issue's slot is freed (so it can re-dispatch if it still matches); a **dirty** one (uncommitted changes) is kept for inspection and the command exits nonzero — rerun with `--force` to remove it anyway. The agent is always stopped first, even when the worktree is kept. |
| `lola revive <session>` | Inverse of `kill`: relaunch a **dead** session's agent on the worktree that was kept for inspection. Claude resumes its prior conversation via `--continue` when it recorded a transcript before dying, otherwise the agent restarts fresh on the same worktree. Refused if the session is still running. Use when a pane died to a transient fault (instant launch failure, crashed agent, machine sleep) rather than re-dispatching from scratch. |
| `lola answer <session> <text>` | Deliver a human's inline reply to a session parked for input. Refused unless the session's derived status is `needs_input` (the one moment the agent is provably idle at its prompt), so a reply can never corrupt a mid-turn agent. |
| `lola review <session> [--provider kind]` | Force a **pass-shape** review provider now, ignoring the once-per-PR guard, and route its findings per its transports. With no `--provider` it forces the primary enabled pass provider; `--provider coderabbit-cli\|claude-session` picks one explicitly. Skipped (not an error) when no such provider is enabled or its tool is unavailable. |
| `lola coderabbit <session>` | Back-compat alias that forces the **watch-shape** provider now (`coderabbit-watch`) — poll the session's open PR for CodeRabbit (GitHub-app) comments, ignoring the watermark, and route any found (notify / worker / Linear per config). Skipped (not an error) when the watch is disabled or the session has no open PR. |
| `lola reload` | Re-read `config.toml`; the daemon diffs projects and starts/stops poll goroutines without disturbing unaffected ones |
| `lola logs [name] [-f]` | Tail `~/.lola/daemon.log`, optionally filtered to one project (by name); `-f`/`--follow` to stream |

`lola hook <event>` also exists but is **internal and hidden**: the generated
Claude Code settings wire the agent's lifecycle hooks (Stop / Notification /
SessionEnd / PostToolUse) to it, and it posts the event to the daemon over the
socket. Never invoke it by hand.

`lola config migrate-review` is a hidden one-way maintenance command: it folds
the legacy `[review]`/`[coderabbit]` tables into the canonical
`[[review.provider]]` catalog (see [The review catalog](#the-review-catalog))
and clears the legacy tables, then leaves you to `lola reload`. It exists because
a config that mixes the legacy tables with the new catalog is a **hard
validation error** — this command is the explicit, opt-in resolution.

## Runtime layout

Everything lives under `~/.lola/` (override the directory with the `LOLA_HOME`
environment variable — tests rely on this):

| Path | Purpose |
| --- | --- |
| `config.toml` | Configuration (mode 0600, contains **no** secrets) |
| `lola.sock` | Daemon ↔ client unix socket (mode 0600) |
| `daemon.log` | Daemon log |
| `state/<project>.seen` | Per-project seen-issue state |
| `state/sessions.json` | Native session store (status, PR, worktree, tmux target) |
| `worktrees/<project>/<session>/` | Per-session git worktree |
| `cache/linear-<team>.json` | Cached Linear metadata for the TUI forms |

## Configuration reference

See [`config.example.toml`](config.example.toml) for a complete commented
example. All Linear references (`team_id`, `state_ids`, `match_labels`, …) are
Linear **UUIDs**, not names — the TUI form resolves names to IDs for you.

### `[defaults]`

| Key | Type | Description |
| --- | --- | --- |
| `poll_interval` | duration string, e.g. `"60s"`, `"2m"` | How often each polling project ticks. Default `60s`. Values below `30s` are silently clamped up to `30s` (Linear rate-limit floor). |
| `concurrency_cap` | int | Fallback cap for polling projects that don't set their own `concurrency_cap`. |
| `global_cap` | int | Hard ceiling on counted native sessions across **all polling projects**. Must be > 0. Per tick, a project's budget is `min(its cap, global_cap − live counted sessions)`. |
| `agent` | `"claude"` \| `"codex"` \| `"opencode"` | Coding agent spawned per session. Default `claude`. Global default; override per repo with `[[project]].agent`. Empty/omitted resolves to `claude`. See [The coding agent](#the-coding-agent). |
| `manage_daemon` | bool | Whether the TUI owns the daemon lifecycle: silently start the daemon on open when the socket is dead, `^r` restart, `^x` stop. Default `true`. Set `false` when a launchd `KeepAlive` job owns the daemon, so the TUI never fights the supervisor. See [Running the daemon](#running-the-daemon-launchd-vs-tui). |

`[defaults]` additionally carries a **fallback for each inheritable
`[[project]]` key**, so shared setup is written once instead of repeated per
repository — see [Project defaults](#project-defaults-inheritance).

### Project defaults (inheritance)

These `[defaults]` keys are the fallback for the same-named `[[project]]` key.
A project that **omits** the key inherits this value; a project that sets it
overrides it.

| Key | Type | Inherited by |
| --- | --- | --- |
| `branch_prefix` | string | `[[project]].branch_prefix` — prefix for a session's branch. Ultimate default `"lola/"`. |
| `symlinks` | string array | `[[project]].symlinks` |
| `post_create` | string array | `[[project]].post_create` |
| `env` | table of strings | `[[project]].env` (as `[defaults.env]`) |
| `match_labels` | string array | `[[project]].match_labels` |
| `match_mode` | `"any"` \| `"all"` | `[[project]].match_mode`. Ultimate default `"any"`. |
| `dedup_mode` | `"label"` \| `"seen"` \| `"state"` | `[[project]].dedup_mode`. Ultimate default `"seen"`. |
| `on_sent_set_label` | string (UUID) | `[[project]].on_sent_set_label` |
| `blocked_label_id` | string (UUID) | `[[project]].blocked_label_id` |
| `priority_sort` | string array | `[[project]].priority_sort`. Ultimate default `["priority", "createdAt"]`. See [Priority sort](#priority-sort). |

Inheritance is decided by **key presence, not by value**:

```toml
# key absent from the project  -> inherit [defaults]
match_labels = ["x"]           # -> override with ["x"]
match_labels = []              # -> override with NOTHING (match no labels)
```

An inherited key is never written into the project's own table, so changing a
default later still reaches every project that inherits it. `agent`,
`concurrency_cap` and `branch_prefix` are the exceptions to the presence rule:
for them an empty/zero value has always meant "fall back", and still does.

> **Use WORKSPACE labels here.** `match_labels`, `on_sent_set_label` and
> `blocked_label_id` hold Linear label UUIDs, and Linear has two kinds: *team*
> labels, scoped to a single team, and *workspace* (organisation) labels, which
> exist across every team. A `[defaults]` label is inherited by projects on any
> team, so it should be a **workspace** label — which is typically where a
> shared trigger label like `agent-ready` is defined anyway.
>
> Both settings screens offer only workspace labels for these keys; a project's
> own label pickers offer that project's team labels. Lola cannot tell the two
> apart offline (config validation never touches the network), so a team label
> put here by hand is not rejected — it will simply never match issues outside
> its own team.

#### Priority sort

`priority_sort` is **lola's own tie-break chain** for ranking the issues a tick
matched — not a Linear concept, and nothing is fetched from the API for it. The
keys are applied in the order given, and only two are understood:

| Key | Orders by |
| --- | --- |
| `priority` | Linear priority, highest first; issues with no priority sort last |
| `createdAt` | creation time, oldest first |

Order is the value: `["priority", "createdAt"]` takes the highest-priority
issue and breaks ties by age, while `["createdAt", "priority"]` is
oldest-first with priority as the tie-break. Anything left after the chain
falls back to the issue identifier, so the order is always deterministic.

Both settings screens offer these as an ordered picker showing the rank. An
unknown key used to be silently ignored by the sorter; it is now **rejected by
validation**, since a typo that quietly changes pickup order is worse than a
startup error.

### `[linear]`

| Key | Type | Description |
| --- | --- | --- |
| `api_key_keychain` | string | macOS Keychain **service name** holding the Linear API key. Tried first. |
| `api_key_env` | string | Name of an environment variable holding the key. Fallback when the keychain item is missing. |
| `endpoint` | string | GraphQL endpoint. Default `https://api.linear.app/graphql`. |

There is deliberately no `api_key` field — secrets never live in
`config.toml`, and lola never logs the key.

### `[[project]]` (one table per repository)

The repository registry the native runtime spawns into: lola creates one git
worktree per session under `~/.lola/worktrees/<project>/` and runs the coding
agent in tmux inside it. A project **polls Linear when — and only when — it has
a `team_id`** set (see [Polling fields](#project-polling-fields-optional-a-project-polls-when-team_id-is-set)
below); a project with no `team_id` is a valid, non-polling project, used purely
for manual worktrees and opening PRs by hand from the TUI. Validation of these
fields is purely static — path-exists / is-a-git-repo checks happen in the
runtime layer, not on config load.

| Key | Type | Description |
| --- | --- | --- |
| `name` | string | Unique project **id** (required), and a path segment: it names the worktree directory (`worktrees/<name>/`) and the seen file (`state/<name>.seen`), prefixes every session/tmux name (`lola-<name>-eng-42`), and is what `lola status`, `enable`/`disable`/`poll`/`logs` key by. Keep it slug-shaped (lowercase letters, digits, `.` `_` `-`) — the forms slugify what you type. Changing it is a **rename**, not an edit; see [Renaming a project](#renaming-a-project). |
| `label` | string | Free-text display name shown in the TUI and desktop (e.g. `"Nori App"`). Optional — empty falls back to `name`. Purely cosmetic: nothing keys by it, so you can change it at any time, including while sessions are running. |
| `path` | string | Absolute path to the main checkout (required). A leading `~` is expanded on load. Session worktrees live under `~/.lola/worktrees/`, never inside the checkout. |
| `repo` | string | GitHub repository as `owner/name`. Used for PR/CI observation of the sessions spawned for this project: the reconciler and observer pass it to `gh pr list --repo` so the open-PR check works regardless of the daemon's working directory. When empty, that check is unavailable and orphaned issues are **never** auto-reverted (fail-closed). Both forms **auto-detect** this from the checkout once `path` is set — see [Repo auto-detection](#repo-auto-detection). |
| `default_branch` | string | Branch new session worktrees start from, and the base the agent is told to open its PR against. Default `main`. Both forms offer the checkout's branches once `path` is set (local plus remote-tracking, the repository's own default first) while staying free text, so a path that is not a checkout is never a dead end. |
| `branch_prefix` | string | Prefix prepended to a session's derived branch name (e.g. `"feat/"` yields `feat/eng-42`). Empty inherits `[defaults].branch_prefix`, then `"lola/"`. |
| `post_create` | string array | Commands run inside a fresh worktree before the agent starts (e.g. `composer install`). Any failure blocks the session with a clear status — never a half-started agent. Omit to inherit `[defaults].post_create`. |
| `symlinks` | string array | Files symlinked from the main checkout into each worktree, e.g. `[".env"]`. Beware: a shared `.env` usually means every worktree talks to the same database. Omit to inherit `[defaults].symlinks`. |
| `env` | table of strings | Extra environment variables exported into each session (`[project.env]`); the agent and the `post_create` commands both see them. Omit to inherit `[defaults].env`. |
| `agent` | `"claude"` \| `"codex"` \| `"opencode"` | Coding agent for sessions spawned into this repo, overriding `[defaults].agent`. Empty/omitted inherits the global default (ultimately `claude`). See [The coding agent](#the-coding-agent). |

#### Repo auto-detection

Filling in a project's `path` makes the TUI and desktop forms resolve `repo`
from the checkout's git remotes, so `owner/name` need not be copied by hand. It
prefers the **`upstream`** remote over `origin` — in a fork, `origin` is your
fork but `upstream` is where the pull requests actually land, which is what
PR/CI observation must watch. A detected value is flagged as such in the form;
verify it on a fork.

Detection only ever **fills an empty field** and never overwrites a value you
set. When it cannot determine the repo — not a git checkout, no remotes, a
non-GitHub host, or a self-hosted GitHub Enterprise on a domain that does not
name GitHub — it leaves the field **empty rather than guessing**. That is the
safe direction: an empty `repo` disables the open-PR check (and so the orphan
revert, fail-closed), whereas a wrong one would have `gh pr list --repo` answer
confidently about someone else's repository.

It reads local git remotes only — no network, no `gh`, no auth.

#### Renaming a project

A project has two names, and they behave very differently.

**`label` is free.** It is the display string and nothing keys by it, so rename
it whenever you like — including with sessions running. In the TUI it is the
first field of the project form's Repo tab; saving is an ordinary config write.

**`name` is the id, and changing it is a migration.** It is baked into the
worktree directory (`worktrees/<name>/`), the seen file (`state/<name>.seen`)
and every session id — which is also the tmux session name
(`lola-<name>-eng-42`). The TUI still lets you edit it: type a new id (it
slugifies as you go) and save. The save is then routed to the **daemon**, which

- refuses if the project has any session in the store, naming them, because a
  live session's worktree path and tmux name embed the old id and moving them
  would mean `git worktree repair` and tmux surgery mid-flight;
- refuses if `worktrees/<old>/` still holds anything, since that is state
  nothing would resolve under the new name;
- otherwise renames the `[[project]]` entry in place, carries `state/<old>.seen`
  over to `state/<new>.seen` so already-dispatched issues are not re-dispatched,
  drops the now-empty `worktrees/<old>/`, and reloads.

So: **finish or kill a project's sessions, then rename.** A rename needs a
running daemon — it is the only thing that knows whether a session is live.

For a new project the id is derived from the label as you type (`Nori App` →
`nori-app`); typing in the id field yourself breaks that link for good.

### `[[project]]` polling fields (optional; a project polls when `team_id` is set)

These fields live inline on the `[[project]]` table and configure its Linear
watch. A project polls **iff** `team_id` is set; without one it never polls and
these fields are ignored. A project has at most **one** polling config. All
Linear IDs are UUIDs, matched by ID, never by name.

| Key | Type | Description |
| --- | --- | --- |
| `enabled` | bool | Whether the daemon runs this project's poll (live pause/resume). Only meaningful when `team_id` is set. |
| `team_id` | string | Linear team UUID. **Setting this is what makes the project poll**; omit it and the project is manual-only. |
| `project_id` | string | Linear project UUID. Empty = no project filter. |
| `cycle_mode` | `"none"` \| `"active"` \| `"pinned"` | `none` = no cycle filter; `active` = the team's active cycle, re-resolved at the start of **every** tick (handles rollover); `pinned` = the fixed cycle in `cycle_id`. |
| `cycle_id` | string | Cycle UUID; required iff `cycle_mode = "pinned"`. |
| `state_ids` | string array | Workflow state UUIDs to match (filtered by ID, never by name). Empty = any state. |
| `match_labels` | string array | Trigger label UUIDs. |
| `match_mode` | `"any"` \| `"all"` | `any` = issue has at least one trigger label; `all` = issue has every trigger label. |
| `assignee_mode` | `"anyone"` \| `"me"` \| `"user"` | `anyone` = no assignee filter; `me` = the authenticated user (Linear `viewer`); `user` = the user in `assignee_user_id`. |
| `assignee_user_id` | string | User UUID; required iff `assignee_mode = "user"`. |
| `concurrency_cap` | int | Max counted native sessions this project may occupy. Falls back to `[defaults].concurrency_cap` when 0/unset; the effective value must be > 0. |
| `priority_sort` | string array | Sort keys for deterministic selection when the budget caps the match list, e.g. `["priority", "createdAt"]`. |
| `dedup_mode` | `"label"` \| `"seen"` \| `"state"` | See below. |
| `on_sent_set_label` | string | Label UUID added after a successful spawn to mark the issue as picked up. Required iff `dedup_mode = "label"`; must **not** be one of `match_labels`. |

The GitHub repository the poll's open-PR check uses is the project's own `repo`
(above), not a separate per-poll field.

**Dedup modes** (pick one per polling project, they are not mixed):

- `label` — after a successful spawn, lola flips the issue's labels (removes
  **all** of `match_labels`, adds `on_sent_set_label`), so the issue simply
  stops matching the filter — under `match_mode = "any"` or `"all"`, with any
  number of trigger labels. The seen file is only a short-TTL race guard.
  Visible in Linear; survives daemon restarts. Label mode requires that
  `on_sent_set_label` is **not** one of `match_labels`, otherwise the issue
  would re-match immediately after the flip and respawn forever.
- `seen` — the seen file is authoritative: matched-and-spawned issue IDs are
  remembered and skipped. Entries whose issues no longer match the filter are
  pruned, so a reopened ticket re-queues. No labels are touched.
- `state` — lola advances the issue's own **workflow state** on spawn (see
  `on_spawn_state_id` under [Linear write-back](#linear-write-back-optional)),
  which moves it **out of** `state_ids` so it stops matching — no seen file, no
  label flip, and the transition is visible in Linear. Requires `state_ids` set
  and `on_spawn_state_id` set to a state that is **not** one of `state_ids`
  (otherwise the issue keeps matching after the move and respawns forever).

Regardless of mode, a daemon-global in-flight set prevents two polling projects
from spawning the same issue in one cycle, and every dispatch is gated on the native
runtime being healthy — if `tmux`/`git`/`claude` are not all resolvable the
tick is skipped and **nothing** (labels, seen, in-flight) is mutated.

### Linear write-back (optional)

Per-project polling fields that let lola narrate the agent's progress back onto
the Linear issue — advancing the issue's **workflow state** and/or posting a
short comment at each lifecycle point (P4). **Entirely optional**: every field
defaults to `""` (no transition / no label) or `false` (no comment), so a
polling project that sets none of them behaves exactly as before. All IDs are Linear UUIDs; they are
validated only for non-emptiness where a feature requires one and are **never**
resolved against Linear at config time (that is a runtime check).

You don't hand-write these UUIDs: the **project editor** in the TUI (`lola`, edit
a project) exposes every write-back field below its trigger fields, picking the state
and label from the **same Linear pickers** used for `state_ids` / trigger labels,
and toggling the booleans in place. Editing config.toml by hand still works.

| Key | Type | Description |
| --- | --- | --- |
| `on_spawn_state_id` | string | Workflow state the issue moves to when a session is spawned. `""` = no transition. **Required** when `dedup_mode = "state"`, and then must **not** be one of `state_ids`. |
| `on_pr_state_id` | string | State when the agent opens a PR (e.g. "In Review"). `""` = no transition. Gated by `pr_requires_checks`. |
| `on_merged_state_id` | string | State when the PR merges (e.g. "Done"). `""` = no transition. |
| `blocked_label_id` | string | Label added on escalation (agent blocked, needs a human). `""` = none. |
| `pr_requires_checks` | bool | Hold the `on_pr_state_id` move **and** `comment_on_pr` until the PR is _valid_ — open, not a draft, and all CI/CodeRabbit checks green (none failing or pending) — instead of firing the instant a PR opens. Default `false` (fire on open). |
| `comment_on_spawn` | bool | Also post a short comment when the session spawns. Default `false`. |
| `comment_on_pr` | bool | Also post a short comment when the agent opens a PR. Default `false`. |
| `comment_on_merged` | bool | Also post a short comment when the PR merges. Default `false`. |
| `comment_on_blocked` | bool | Also post a short comment on escalation, with the block reason. Default `false`. |

The `comment_on_*` toggles use lola's built-in comment templates. Like the
`[reactions]` messages, they are filled by **plain string replacement**, never a
template engine, so a PR link or a blocked-reason detail can never inject
template directives. The placeholders are `{{.Session}}` (spawn comment),
`{{.PR}}` (PR comment), and `{{.Detail}}` (blocked comment); the merged comment
is a bare acknowledgement.

State transitions and comments are independent — you can move the issue without
commenting, comment without moving it, or do both. They also compose with any
`dedup_mode`: e.g. a `label`-dedup project can still set `on_pr_state_id` to move
the issue to "In Review" when a PR opens. Only `dedup_mode = "state"` *depends*
on `on_spawn_state_id` (that transition is what dedups the issue).

By default the `on_pr_state_id` move fires the moment a PR opens, even while CI
is still red or running. Set `pr_requires_checks = true` to hold "In Review"
until the PR is actually **valid** — open, not a draft, and every CI/CodeRabbit
check green — so the issue advances only once the work has passed its checks.
The gate uses the same `statusCheckRollup` signal as the rest of lola, so a
CodeRabbit run counts toward it only if CodeRabbit registers a check (it usually
does); a review-without-a-check does not hold the transition.

### `[reactions]` (optional)

How lola reacts when a live session's derived PR/CI status changes (the P3
reaction engine). **Entirely optional** — every reaction and the whole table
default sensibly, so existing configs keep working unchanged. Each reaction is
its own sub-table (`[reactions.<name>]`) with the same three keys:

| Key | Type | Description |
| --- | --- | --- |
| `auto` | bool | React automatically, versus notify-and-park only. |
| `retries` | int | Automatic recovery attempts before escalating. Meaningful for `ci_failed` only; must be `>= 0`. |
| `message` | string | Template sent to the live agent. Empty = react but send nothing. |

A `message` may contain the placeholders `{{.Detail}}` (fetched CI logs or
review comments), `{{.Issue}}` (Linear identifier), and `{{.PR}}` (PR
number/URL). They are filled by **plain string replacement**, not a template
engine, so an agent-authored PR body or a failing log can never inject template
directives.

| Reaction | Default `auto` | Default `retries` | Behavior |
| --- | --- | --- | --- |
| `ci_failed` | `true` | `2` | Feed the failing check logs into the session with a recovery prompt; retry N times, then escalate. |
| `changes_requested` | `true` | — | Relay review comments (incl. inline) into the session; re-request review on the next push. |
| `merge_conflict` | `true` | — | Tell the agent to rebase and resolve (detected via the observer's `mergeable`). |
| `approved_and_green` | **`false`** | — | **Never auto-merge.** Notify and park the worktree for a human; the default message is empty. |
| `merged` | `true` | — | Auto cleanup: remove the worktree + branch, archive the session, free the slot. |

Any unset field takes its default, so you can override just one thing (e.g.
`retries = 0` to disable CI recovery, or `auto = false` on a single reaction)
and leave the rest alone. Explicit `false`/`0`/`""` are honored, not treated as
"unset".

### `[notify]` (optional)

Best-effort desktop + Slack notifications, routed by priority (P3.20). Optional;
the defaults below apply when the table is absent.

| Key | Type | Description |
| --- | --- | --- |
| `desktop` | bool | macOS desktop banners. Default `true` on macOS, `false` elsewhere (no-op off macOS). |
| `slack_webhook_env` | string | **Name** of an environment variable holding the Slack incoming-webhook URL. Default `SLACK_WEBHOOK_URL`. |
| `routing` | table of string arrays | Per-priority channel lists under `[notify.routing]`; each channel is `"desktop"` or `"slack"`. |

There is deliberately **no webhook URL field** — the URL is a secret and never
lives in `config.toml`. lola reads it from the named environment variable at
notify time and never logs it; when the variable is unset, the Slack channel is
simply disabled.

Routing priorities and their defaults:

| Priority | Default channels | Used for |
| --- | --- | --- |
| `urgent` | `["desktop", "slack"]` | needs-input, escalations |
| `action` | `["desktop", "slack"]` | changes-requested, CI failed after retries |
| `info` | `["slack"]` | approved+parked, merged+cleaned |

Priorities you omit under `[notify.routing]` keep their default channels; set a
priority to `[]` to route it nowhere.

### `[brain]` (optional, off by default)

The P5 orchestrator brain: when enabled, lola makes a single headless
`claude -p` call to produce a short **human-facing summary** at two decision
points — when a session **escalates** (why it's blocked and the next step) and
when a PR is **approved and green** (what it changes and any risk to check
before merging). **Opt-in and off by default**: omit the table (or leave
`enabled = false`) and behavior is unchanged — lola uses its generic notify and
comment templates.

| Key | Type | Description |
| --- | --- | --- |
| `enabled` | bool | Master switch. Default `false`; an absent table is also off. |
| `model` | string | Passed to claude as `--model` when set; empty uses claude's default model. |
| `timeout_seconds` | int | Hard cap on each summary call. Must be `>= 0`. Default `120`. |
| `summarize_escalation` | bool | Summarize why a stuck session is blocked. Defaults to `true` when `enabled`. |
| `summarize_approved` | bool | Summarize an approved+green PR before a human merges. Defaults to `true` when `enabled`. |

Setting `enabled = true` alone turns on both summaries with the default timeout;
set either `summarize_*` to `false` to run only the other. The two summarizers
default to on only while `enabled`, and explicit `false`/`0` are honored, not
treated as "unset".

The summary is **read-only and strictly one-directional**: it is delivered to
the notifier and an optional Linear comment **only, and is never fed back into
the worker agent**. The context the brain summarizes (PR diff, CI logs, tmux
pane tail) is attacker-influenceable, so its output is treated as untrusted text
shown to a human — safe for a notification or comment, never an action in the
control loop. Each call is fired at most **once per transition**, bounded by
`timeout_seconds`, attempted **once** (no retries), and **skips gracefully** —
falling back to the generic template — on any error or timeout.

The daemon execs `claude` directly and relies on your existing claude auth
(`~/.claude`) or `ANTHROPIC_API_KEY` in the daemon's environment; lola does not
manage keys here. Each summary **spends Anthropic tokens**, so the feature is
deliberately opt-in. `lola doctor` reports whether `claude` is on `PATH`.

### The review catalog

lola's review system is the **QA buddy** — *not* a second live agent, but a set
of **event-triggered review providers** that inspect a session's PR and route
their findings back to the worker and/or to humans. It has two spellings:

- the **legacy** `[review]` / `[coderabbit]` tables (below) — still supported
  forever, unchanged; and
- the **canonical `[[review.provider]]` catalog** — a flexible superset that adds
  pluggable provider kinds, per-provider transports, and fallback chains.

The two are **mutually exclusive**: a config that carries both is a hard
validation error. Convert the legacy tables to the catalog once with
`lola config migrate-review` (it folds them into equivalent providers and clears
the legacy tables — one-way, opt-in, then `lola reload`). Both spellings are
**opt-in and off by default**: omit them entirely for zero behavior change.

#### `[[review.provider]]` — the catalog

Each `[[review.provider]]` is one provider of a given **kind**. At most **one
provider per kind** is allowed (guards key by kind).

| Key | Type | Applies to | Description |
| --- | --- | --- | --- |
| `provider` | string | all | Kind: `coderabbit-cli` \| `coderabbit-watch` \| `claude-session`. Required. |
| `enabled` | bool | all | Master switch. Default `false`. |
| `on_pr_open` | bool | pass shapes | Run automatically when a session first opens a PR. Default `true`. |
| `command` | string | `coderabbit-cli` | Optional space-split argv override; empty uses the runner default. |
| `timeout_seconds` | int | pass shapes | Hard cap per pass. Must be `>= 0`. Default `300`. |
| `model` | string | `claude-session` | Optional `--model` for the headless `claude -p` review; empty = claude's default. |
| `author` | string | `coderabbit-watch` | Login **substring** matched (case-insensitively) against each comment author. Default `"coderabbitai"`. |
| `transports` | []string | all | Multiselect over `{lola, github, linear}`; see below. Default `["lola"]`; `lola` is always forced present. |
| `notify` | bool | all | `lola` transport: surface findings to a human (desktop/Slack). Default `true`. |
| `send_to_agent` | bool | all | `lola` transport: hand findings to the worker via the send-keys gate. Default `true`. |
| `fallback` | []string | pass shapes | Ordered kinds tried when this provider **can't answer** (unavailable / over-quota). Default none. |

**Provider kinds** — three kinds map to two execution **shapes**:

- **`coderabbit-cli`** (*pass* shape): execs the CodeRabbit CLI against the PR
  branch and returns findings synchronously. Runs **once per PR** (a per-session
  guard records the reviewed PR number; a new PR number re-triggers once).
- **`claude-session`** (*pass* shape): a headless `claude -p` review — lola pipes
  the branch diff on stdin and claude returns findings. Same once-per-PR guard.
  Useful as a `coderabbit-cli` **fallback** (see below). Execs `claude` directly
  and relies on your existing claude auth (`~/.claude` / `ANTHROPIC_API_KEY`).
- **`coderabbit-watch`** (*watch* shape): **polls** each session's GitHub PR (via
  `gh`, on the ~30s observer cadence) for comments/reviews left by the CodeRabbit
  **GitHub app** — or any reviewer bot, via `author` — and routes each **new**
  one. A per-session **watermark** makes the poll fire-once per comment *and*
  survive downtime (a webhook would be lost while the daemon is stopped; the next
  cycle reconciles the PR's current comments instead of replaying a missed event).

**Transports** — where a provider's findings go. `transports` is a multiselect
over three friendly tokens:

- **`lola`** — the always-on internal transport (auto-appended if you omit it).
  It expands to two sinks refined by the per-provider bools: the **notify** sink
  (desktop/Slack, gated by `notify`) and the **worker hand-off** sink (send-keys,
  gated by `send_to_agent`). This is what preserves the legacy `notify = false`
  opt-out — mute either sink independently while the other still fires.
- **`github`** — post findings as a **plain GitHub PR comment** (`gh pr comment`,
  body on stdin, never a review that could approve/request-changes). **Pass
  shapes only** — validation forbids `github` on a `coderabbit-watch` (its
  feedback is already on the PR; re-posting it would be a self-feedback loop).
  The post is idempotent per PR (a settle guard prevents per-cycle spam; a
  permanent gh failure such as 422/403 stamps the guard and logs once; a
  transient failure retries next cycle). Fail-closed: a missing repo or
  unauthenticated `gh` skips silently.
- **`linear`** — mirror findings onto the session's Linear issue as a comment.

Only the **worker hand-off** sanitizes and idle-gates its text; **notify /
github / linear are human sinks** that carry the full untrusted findings verbatim
(never re-fed into the control loop). The findings are untrusted (diff/CI-derived
content), so the worker path uses the **same send-keys safety** as reactions:
sanitized (control chars stripped), delivered only when the agent is idle at its
prompt (deferred otherwise), and **never run as a command**. Titles/preambles are
**per-kind**, so a `claude-session`'s findings are labeled "Claude review", not
"CodeRabbit".

**Fallback chains** — a `fallback = [...]` list lets a pass provider hand off to
another **pass** kind when it **can't answer**: the tool is missing, times out,
or reports **over-quota** ("out of reviews", usage/rate limit, 429, …). lola
advances through the chain and routes the first successful result **under the
primary's transports** (to get a fallback's github post, put `github` on the
primary). A real exit error or an auth failure is a **graceful skip that does not
fall through** (fail-closed: auth is an operator fix; a genuine failure must not
silently burn the paid fallback). Each entry is bounded by its own
`timeout_seconds`, and the whole cycle is shutdown-abortable.

> **Watch cannot fall back.** Fallback is **pass-shape only**, and validation
> forbids `fallback` on a `coderabbit-watch`. When the CodeRabbit **GitHub app**
> is out of reviews, it posts that as an ordinary PR comment — non-empty,
> `err == nil`, classifier-undetectable — so a watch has no signal to trigger a
> fallback on. If you want quota → `claude-session` fallback, run the
> **`coderabbit-cli`** provider (whose exit/stderr carries the quota signal) with
> `fallback = ["claude-session"]`.

**Validation** rejects: an unknown `provider` kind; more than one provider per
kind; an unknown `transports` token; `github` on a `coderabbit-watch`; `fallback`
on a `coderabbit-watch`; a `fallback` entry that is unknown / the provider's own
kind / a watch kind / absent-or-disabled in the catalog / part of a cycle;
`timeout_seconds < 0`; and a **catalog alongside a non-empty legacy table** (run
`lola config migrate-review`).

Force any pass provider now with `lola review <session> [--provider kind]`, or the
watch with `lola coderabbit <session>` — both ignore the once-per-PR guard /
watermark.

### `[review]` (legacy, optional, off by default)

The legacy CLI pass — equivalent to a single `coderabbit-cli` provider. Still
supported forever, but prefer the catalog (`lola config migrate-review`
converts). When enabled, the first time a session opens a PR lola execs the
CodeRabbit CLI against that branch and hands the findings back to the worker and,
optionally, to a human via notify + a Linear comment.

| Key | Type | Description |
| --- | --- | --- |
| `enabled` | bool | Master switch. Default `false`; an absent table is also off. |
| `command` | string | Optional override of the coderabbit argv as a space-split string (e.g. `"coderabbit review --plain --type all"`). Empty uses the runner's built-in default invocation. |
| `on_pr_open` | bool | Run the pass automatically when a session first opens a PR. Defaults to `true` when `enabled`. |
| `send_to_agent` | bool | Feed the findings back to the worker through the send-keys gate. Defaults to `true` when `enabled`. |
| `comment_on_linear` | bool | Also post the findings as a Linear comment. Defaults to `false` regardless of `enabled`. |
| `timeout_seconds` | int | Hard cap on each review pass. Must be `>= 0`. Default `300`. |

Setting `enabled = true` alone runs the pass on PR open and feeds the worker with
the default timeout; `on_pr_open` and `send_to_agent` follow `enabled` unless set
explicitly, while `comment_on_linear` stays off until you opt in. Explicit
`false`/`0`/`""` are honored, not treated as "unset".

The findings are **untrusted text** (they embed diff content), so they are routed
through the **same send-keys safety** as reactions: sanitized (control characters
stripped), delivered only when the agent is idle at its prompt (deferred
otherwise), and **never run as a command**. The `coderabbit` CLI must be
**installed and authenticated** (`coderabbit auth login`); a pass **spends a
CodeRabbit review** (~300s budget) and **skips gracefully** if coderabbit is
missing, unauthenticated, or times out.

### `[coderabbit]` (legacy, optional, off by default)

The legacy PR-comment watch — equivalent to a single `coderabbit-watch` provider.
Still supported forever, but prefer the catalog. It **polls each session's GitHub
PR** (via `gh`, on the ~30s observer cadence) for comments/reviews left by the
CodeRabbit **GitHub app** — or any reviewer bot, via `author` — and routes each
**new** one to a human (notify), the worker agent (sanitized + idle-gated), and/or
a Linear comment.

| Key | Type | Description |
| --- | --- | --- |
| `enabled` | bool | Master switch. Default `false`; an absent table is also off. |
| `author` | string | Login **substring** matched (case-insensitively) against each comment/review author. The CodeRabbit app posts as `coderabbitai[bot]`, so the default `"coderabbitai"` matches it; set another value to watch a different bot. |
| `notify` | bool | Surface each new comment to a human. Defaults to `true` when `enabled`. |
| `send_to_agent` | bool | Relay each new comment to the worker through the send-keys gate. Defaults to `true` when `enabled`. |
| `comment_on_linear` | bool | Also mirror each new comment onto the Linear issue. Defaults to `false` regardless of `enabled`. |

Setting `enabled = true` alone watches for the CodeRabbit app and both notifies
and feeds the worker; `notify` and `send_to_agent` follow `enabled` unless set
explicitly, while `comment_on_linear` stays off until you opt in. Explicit
`false`/`""` are honored, not treated as "unset". A per-session **watermark**
(`last_coderabbit_at`) makes the poll fire-once per comment and survive downtime.
The comment text is **untrusted** (attacker-authorable), so the worker hand-off
uses the same send-keys safety as `[review]`. Cost is `gh` only: one read-only
`gh pr view` per enabled session per cycle while its PR is open.

### `[tmux]` (optional)

Tunes the **attach UX** for the isolated tmux server lola runs its sessions on.
Every field has a safe default, so omitting the table is **zero behavior
change**. Nothing here is validated (all values are free-form) and a config
without `[tmux]` always validates.

| Key | Type | Description |
| --- | --- | --- |
| `socket_name` | string | The tmux server socket (`tmux -L <name>`) lola runs sessions on. Default `"lola"`. This is its **own** server, isolated from your default tmux. |
| `detach_key` | string | Opt-in single key bound to detach (e.g. `"F12"`). Empty keeps tmux's default **Ctrl-b d**. The status bar's detach hint follows whatever this resolves to. |
| `status_right` | string | Raw tmux `status-right` format override. Empty uses lola's built-in branded bar. |
| `mouse` | bool | Enable tmux mouse mode inside the session. Default `false`. |

### `[ui]` (optional)

**Appearance only** — nothing the daemon does reads this table, so omitting it
is zero behavior change. One identifier drives all of lola-desktop's color: the
app chrome, the embedded terminals, and the ANSI palette the session grid
renders pane snapshots with.

| Key | Type | Description |
| --- | --- | --- |
| `theme` | string | `catppuccin-latte` \| `catppuccin-frappe` \| `catppuccin-macchiato` \| `catppuccin-mocha`. Empty or absent uses `catppuccin-mocha`. |

Unlike `[tmux]`, the theme **is** validated: an unrecognized name is rejected at
load with an error listing the accepted values, rather than silently rendering
the default — a typo you cannot see is worse than a startup error. A config
that never sets `[ui]` never grows the table on save, so the default stays a
default instead of being frozen into your file.

## The coding agent

Each matched issue gets its own session — a git worktree plus a tmux pane
running a coding agent. **Which agent lola launches is configurable**, with full
lifecycle-callback parity across all three:

| Kind | Binary | Launched as | Lifecycle callbacks |
| --- | --- | --- | --- |
| `claude` (default) | `claude` | `claude --settings .lola/settings.json <prompt>` | Claude Code hooks: lola writes `.lola/settings.json` wiring Stop / Notification / SessionEnd / PostToolUse / UserPromptSubmit to `lola hook`. |
| `codex` | `codex` | `codex --ask-for-approval never --sandbox workspace-write <prompt>` (unattended) | Codex `notify`: lola sets `CODEX_HOME=<worktree>/.lola/codex` and writes its `config.toml` with `notify = ["lola", "hook", "codex-notify"]`; codex invokes that with a JSON payload on each turn-complete / approval-request, normalized to `stop` / `notification`. |
| `opencode` | `opencode` | `opencode --prompt <prompt> --auto` (unattended) | opencode plugin: lola writes `<worktree>/.opencode/plugins/lola-hook.js`, an in-process plugin that shells `lola hook <event>` on `session.idle` (→ `stop`), `permission.asked` (→ `notification`), and `tool.execute.after` (→ `tool_use`). |

Set the agent globally in `[defaults].agent` and override it per repository with
`[[project]].agent`; an empty or omitted value resolves to `claude`, so existing
configs are unchanged. The daemon **health-gate checks the configured agent's
binary** (not always `claude`) before every dispatch, and `lola doctor` reports
whether it is on `PATH`.

**Callback artifacts are per-session and git-excluded.** claude's
`.lola/settings.json` and codex's `.lola/codex/` live under the already-excluded
`.lola/` directory; for opencode sessions lola also excludes `.opencode/` from
the worktree. `LOLA_SESSION` is exported into the pane so every callback path
attributes the event to the right session.

**Pane-scraping backstop.** Independently of the callbacks, lola classifies each
session's tmux pane text (working vs. waiting-for-input, plus question
extraction) with **agent-aware** cues, so a session stays tracked even if a hook
is dropped. The daemon's status transitions key off the normalized event names
(`stop` / `notification` / `session_end` / `tool_use` / `user_prompt`), so they
are identical across all three agents.

**Provider auth is inherited from the daemon/pane environment**, never stored in
`config.toml`: `ANTHROPIC_API_KEY` (claude) or `OPENAI_API_KEY` (codex), or an
existing CLI login — `~/.claude`, `codex login` (whose `auth.json` lola
best-effort symlinks into the isolated `CODEX_HOME`), or `opencode auth`.

**Distinct from lola's internal helpers.** The `[brain]` summarizer, `[review]`,
and `[coderabbit]` features always shell out to `claude -p` regardless of this
setting — they are lola-internal helpers, **not** the coding agent, and never
change with the agent choice.

**Note:** codex/opencode are exercised end-to-end only with those binaries
installed; the Go test suite covers the launch-arg and callback-wiring plumbing
but does not run the real CLIs.

## Attaching to a session

Each agent runs inside a **tmux session** — a git worktree with Claude Code
live in it. Open the TUI's **session view** and press **Enter** on a session to
attach; the terminal hands over to tmux and you see the agent's pane, dressed
with a branded status bar (the **LOLA** brand, the session's Linear issue as its
label, and a detach hint).

To leave without stopping the agent, **detach**: press **Ctrl-b d** (tmux's
default), or the single key you bound via `[tmux].detach_key` — the status bar's
hint always names the right one. Detaching just returns you to the TUI; the
session keeps running.

lola runs these sessions on its **own tmux server** (`tmux -L lola`, or your
`[tmux].socket_name`), fully separate from your personal default tmux. Attaching
to or detaching from a lola session never touches your own tmux sessions,
options, or key bindings.

## Secrets

The Linear API key is resolved at dispatch time, keychain first, then
environment:

1. **Keychain** (recommended). Store the key under the service name you put in
   `[linear].api_key_keychain`:

   ```sh
   security add-generic-password -a "$USER" -s lola-linear -U -w
   ```

   lola reads it back with `security find-generic-password -s <name> -w`.
   A missing item falls through to the env var; any other keychain error
   (locked keychain, etc.) is surfaced, not masked.

2. **Environment variable** fallback. Set `[linear].api_key_env` to the
   variable's *name* (e.g. `LINEAR_API_KEY`) and export it in the daemon's
   environment. Note that a launchd-managed daemon does not inherit your shell
   environment — for launchd use, prefer the keychain, or add the variable to
   the plist's `EnvironmentVariables` dict (keeping in mind plists are plain
   files; the keychain is the safer place).

The key is never written to `config.toml` and never logged.

## Running the daemon: launchd vs TUI

There are two ways to keep the daemon running — pick **one**:

- **TUI-managed (default).** The TUI owns the daemon lifecycle. Opening `lola`
  silently starts the daemon if the socket is dead; `^r` restarts it and `^x`
  stops it, right from the cockpit. A restart re-execs the *current* `lola`
  binary, so a rebuilt binary (`make build`) always brings up the newest daemon
  — the intended dev loop. The daemon is detached and survives closing the TUI,
  but nothing respawns it after a crash or reboot. This is the default; no
  install step is needed.
- **launchd-managed.** A `KeepAlive` LaunchAgent starts the daemon at login and
  restarts it if it dies (survives sleep/wake). Best for a set-and-forget
  install. See [launchd install](#launchd-install) below.

**Do not run both.** Under launchd `KeepAlive`, stopping the daemon from the TUI
just makes launchd respawn it in ~1s (and it respawns the *installed* binary,
not your dev build). If you use launchd, set `[defaults].manage_daemon = false`
so the TUI leaves the lifecycle to launchd and hides its start/stop/restart
keys.

## launchd install

lola ships as a **LaunchAgent** (per-user, in `~/Library/LaunchAgents`), *not*
a LaunchDaemon. This is deliberate: lola runs its sessions inside tmux in your
user / GUI login context. A LaunchDaemon runs as root outside any user session
and could not attach to your tmux server, your keychain, or your GUI context.

launchd also does **not** give processes your login-shell `PATH` — a bare
launchd job sees only `/usr/bin:/bin:/usr/sbin:/sbin`. The shipped plist
therefore injects a `PATH` that includes `/opt/homebrew/bin` and
`/usr/local/bin` (so `tmux`, `git`, `gh`, and `claude` resolve) plus `HOME`.

Install:

```sh
cp contrib/com.user.lola.plist ~/Library/LaunchAgents/

# Point it at your real binary path and home directory:
sed -i '' \
  -e "s|/usr/local/bin/lola|$(command -v lola)|" \
  -e "s|/Users/YOU|$HOME|g" \
  ~/Library/LaunchAgents/com.user.lola.plist

launchctl bootstrap gui/$UID ~/Library/LaunchAgents/com.user.lola.plist
```

`RunAtLoad` + `KeepAlive` are both true: the daemon starts at login and is
restarted if it dies (it also survives sleep/wake). Logs go to
`~/.lola/daemon.log` — note the plist must contain the *expanded* path
(`/Users/YOU/.lola/daemon.log`), because launchd does not expand `~`.

Because `KeepAlive` now owns the lifecycle, set `[defaults].manage_daemon =
false` so the TUI does not fight it (see [Running the
daemon](#running-the-daemon-launchd-vs-tui)).

Useful afterwards:

```sh
launchctl kickstart -k gui/$UID/com.user.lola     # (re)start now
launchctl bootout gui/$UID/com.user.lola          # uninstall
lola status                                       # is it healthy?
lola logs -f                                      # watch it work
```

## How a tick works (short version)

For each polling project (`team_id` set and `enabled`), every `poll_interval`:

1. Check the native runtime is healthy: `tmux` available, `git` and `claude`
   resolvable, and the project itself resolves. Unhealthy → skip tick,
   record the error in `status`, mutate nothing.
2. Resolve the API key (keychain → env). If `cycle_mode = "active"`, resolve
   the team's active cycle now.
3. Query matching issues (paginated, 100 per page, until exhausted).
4. Drop issues already in-flight in another polling project, then apply this
   project's dedup mode.
5. Sort by `priority_sort`, take up to
   `min(concurrency_cap, global_cap − live counted native sessions)`.
6. Per issue: record it as in-flight/seen **first**, then spawn the native
   session — a git worktree from the project (`post_create` + symlinks), a tmux
   session running the configured coding agent (by default
   `claude --settings <generated hooks>`; see
   [The coding agent](#the-coding-agent)) with the issue's identifier and title
   as its briefing — then (label mode, on success only) re-read the issue's
   current labels fresh and flip them: remove all trigger labels, add the sent
   label.

A read-only observer (~30 s) tracks each session's tmux liveness and PR/CI
state (`gh`). A periodic reconciliation pass (~5 min) reverts issues that were
marked as sent but have no counted native session and no open PR after an
orphan timeout (default 15 min), so lost work re-queues instead of vanishing.

Failures ("runtime unavailable", "Linear auth failed", label write failed) are
always surfaced in `lola status` and the log — never silently swallowed.

## History

Through P0–P2, lola ran as a thin **trigger**: it dispatched matched Linear
issues into a separate Agent Orchestrator (AO) instance via an `ao spawn`
bridge (`internal/ao`, a per-poll `runtime = "ao" | "native"` switch, an `[ao]`
config table, and an `ao_project` per poll). AO owned git, worktrees, PRs, and
CI. That bridge has been removed: lola is **native-only** now — it spawns and
observes its own worktree + tmux + Claude Code sessions, and polling is a
property of a `[[project]]` (a project with a `team_id`) rather than a separate
config table as it once was. See [PLAN.md](PLAN.md) for the roadmap; the reaction engine
that acts on CI/review state (P3) is still future work — today the observer
tracks PR/CI but does not yet react.
