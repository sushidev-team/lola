# Lola

Named after *Lola rennt* — run, observe, run again.

`lola` is a single Go binary that watches [Linear](https://linear.app) for
issues matching a filter (team → project → cycle → workflow state → labels →
assignee) and spawns its **own** coding-agent session for each one: a dedicated
git worktree, a tmux session, and Claude Code running inside it.

**lola owns the whole run.** For every matched issue it creates a git worktree
from the referenced project, runs the project's `post_create` setup, starts
Claude Code in a fresh tmux session with the issue as its briefing, and marks
the issue as picked up (label flip or seen-file) so it is never dispatched
twice. A read-only observer then tracks each session's tmux liveness and its
PR/CI state via `gh`.

One binary, two roles:

- `lola run` — the daemon (launchd keeps it alive)
- `lola` / `lola tui` — the TUI client (list, create, edit, pause polls)
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
   `gh`, and `claude` (Claude Code). The daemon refuses to spawn while any of
   them is missing and reports it in `lola status`.

3. Store your Linear API key in the macOS Keychain (see
   [Secrets](#secrets)):

   ```sh
   security add-generic-password -a "$USER" -s lola-linear -U -w
   ```

4. Register at least one repository as a `[[project]]` and create a poll that
   references it — start from [`config.example.toml`](config.example.toml), or
   run `lola` and build your first poll in the TUI (it fetches
   teams/projects/states/labels from Linear as you go).

5. Test a poll without side effects:

   ```sh
   lola poll my-poll --once --dry-run
   ```

6. Install the LaunchAgent (see [launchd install](#launchd-install)) so the
   daemon runs permanently, or just run it in a terminal:

   ```sh
   lola run
   ```

## Commands

| Command | Description |
| --- | --- |
| `lola` / `lola tui` | Open the TUI (list polls, create/edit/delete, pause/resume; second tab: live session view). On first run — no `config.toml` yet — this enters the setup wizard first. Press `d` in the polls view for an inline health report. |
| `lola setup` | Run the first-run configuration wizard (Linear key → Keychain, one `[[project]]`, defaults) and write `config.toml`. Re-runnable any time. |
| `lola doctor` | Print an aligned health report (tmux/git/claude/gh on PATH, Linear key readable, daemon socket, config validity, per-project repos); exits 1 on a critical failure. Never prints the key value. |
| `lola run` | Start the daemon (this is what launchd invokes) |
| `lola stop` | Graceful shutdown: finish in-flight tick, close socket, exit 0 |
| `lola status` | Table per poll: enabled, last run, last spawn, running, last error — plus `runtimeOk` / `linearOk` health flags |
| `lola enable <poll>` / `lola disable <poll>` | Live pause/resume of one poll (no restart) |
| `lola poll <poll> --once [--dry-run]` | Run one tick now; `--dry-run` prints matches (including cross-poll overlaps) with **no** side effects — no spawn, no label flip, no seen write |
| `lola kill <session> [--force]` | Terminate a session's agent (tmux) and clean up after it. A **clean** worktree is removed and the issue's slot is freed (so it can re-dispatch if it still matches); a **dirty** one (uncommitted changes) is kept for inspection and the command exits nonzero — rerun with `--force` to remove it anyway. The agent is always stopped first, even when the worktree is kept. |
| `lola reload` | Re-read `config.toml`; the daemon diffs polls and starts/stops goroutines without disturbing unaffected ones |
| `lola logs [poll] [-f]` | Tail `~/.lola/daemon.log`, optionally filtered to one poll; `-f`/`--follow` to stream |

`lola hook <event>` also exists but is **internal and hidden**: the generated
Claude Code settings wire the agent's lifecycle hooks (Stop / Notification /
SessionEnd / PostToolUse) to it, and it posts the event to the daemon over the
socket. Never invoke it by hand.

## Runtime layout

Everything lives under `~/.lola/` (override the directory with the `LOLA_HOME`
environment variable — tests rely on this):

| Path | Purpose |
| --- | --- |
| `config.toml` | Configuration (mode 0600, contains **no** secrets) |
| `lola.sock` | Daemon ↔ client unix socket (mode 0600) |
| `daemon.log` | Daemon log |
| `state/<poll>.seen` | Per-poll seen-issue state |
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
| `poll_interval` | duration string, e.g. `"60s"`, `"2m"` | How often each poll ticks. Default `60s`. Values below `30s` are silently clamped up to `30s` (Linear rate-limit floor). |
| `concurrency_cap` | int | Fallback per-poll cap for polls that don't set their own `concurrency_cap`. |
| `global_cap` | int | Hard ceiling on counted native sessions across **all** polls. Must be > 0. Per tick, a poll's budget is `min(poll cap, global_cap − live counted sessions)`. |

### `[linear]`

| Key | Type | Description |
| --- | --- | --- |
| `api_key_keychain` | string | macOS Keychain **service name** holding the Linear API key. Tried first. |
| `api_key_env` | string | Name of an environment variable holding the key. Fallback when the keychain item is missing. |
| `endpoint` | string | GraphQL endpoint. Default `https://api.linear.app/graphql`. |

There is deliberately no `api_key` field — secrets never live in
`config.toml`, and lola never logs the key.

### `[[project]]` (one table per repository)

The repository registry the native runtime spawns into. Every poll references a
project by `name`; lola then creates one git worktree per session under
`~/.lola/worktrees/<project>/` and runs Claude Code in tmux inside it.
Validation of these fields is purely static — path-exists / is-a-git-repo
checks happen in the runtime layer, not on config load.

| Key | Type | Description |
| --- | --- | --- |
| `name` | string | Unique project name (required). Referenced by `[[poll]].project`. |
| `path` | string | Absolute path to the main checkout (required). A leading `~` is expanded on load. Session worktrees live under `~/.lola/worktrees/`, never inside the checkout. |
| `repo` | string | GitHub repository as `owner/name`. Used for PR/CI observation of the sessions spawned for this project. |
| `default_branch` | string | Branch new session worktrees start from, and the base the agent is told to open its PR against. Default `main`. |
| `post_create` | string array | Commands run inside a fresh worktree before the agent starts (e.g. `composer install`). Any failure blocks the session with a clear status — never a half-started agent. |
| `symlinks` | string array | Files symlinked from the main checkout into each worktree, e.g. `[".env"]`. Beware: a shared `.env` usually means every worktree talks to the same database. |
| `env` | table of strings | Extra environment variables exported into each session (`[project.env]`); the agent and the `post_create` commands both see them. |

### `[[poll]]` (one table per poll)

| Key | Type | Description |
| --- | --- | --- |
| `name` | string | Unique poll name (required). Used by `enable`/`disable`/`poll`/`logs` and the seen-file name. |
| `enabled` | bool | Whether the daemon runs this poll. |
| `team_id` | string | Linear team UUID (required). |
| `project_id` | string | Linear project UUID. Empty = no project filter. |
| `cycle_mode` | `"none"` \| `"active"` \| `"pinned"` | `none` = no cycle filter; `active` = the team's active cycle, re-resolved at the start of **every** tick (handles rollover); `pinned` = the fixed cycle in `cycle_id`. |
| `cycle_id` | string | Cycle UUID; required iff `cycle_mode = "pinned"`. |
| `state_ids` | string array | Workflow state UUIDs to match (filtered by ID, never by name). Empty = any state. |
| `match_labels` | string array | Trigger label UUIDs. |
| `match_mode` | `"any"` \| `"all"` | `any` = issue has at least one trigger label; `all` = issue has every trigger label. |
| `assignee_mode` | `"anyone"` \| `"me"` \| `"user"` | `anyone` = no assignee filter; `me` = the authenticated user (Linear `viewer`); `user` = the user in `assignee_user_id`. |
| `assignee_user_id` | string | User UUID; required iff `assignee_mode = "user"`. |
| `project` | string | Name of a `[[project]]` entry (**required**). lola creates a git worktree from it and runs Claude Code in tmux inside it. Must resolve to a defined `[[project]]`. |
| `repo` | string | GitHub repository as `owner/name` (e.g. `sushidev-team/nori-app`). The reconciler and observer pass it to `gh pr list --repo` so their open-PR check works regardless of the daemon's working directory. **Optional** — when empty it falls back to the referenced project's `repo`; with neither set the PR check is unavailable and orphaned issues are **never** auto-reverted (fail-closed). |
| `concurrency_cap` | int | Max counted native sessions this poll may occupy. Falls back to `[defaults].concurrency_cap` when 0/unset; the effective value must be > 0. |
| `priority_sort` | string array | Sort keys for deterministic selection when the budget caps the match list, e.g. `["priority", "createdAt"]`. |
| `dedup_mode` | `"label"` \| `"seen"` \| `"state"` | See below. |
| `on_sent_set_label` | string | Label UUID added after a successful spawn to mark the issue as picked up. Required iff `dedup_mode = "label"`; must **not** be one of `match_labels`. |

**Dedup modes** (pick one per poll, they are not mixed):

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

Regardless of mode, a daemon-global in-flight set prevents two polls from
spawning the same issue in one cycle, and every dispatch is gated on the native
runtime being healthy — if `tmux`/`git`/`claude` are not all resolvable the
tick is skipped and **nothing** (labels, seen, in-flight) is mutated.

### Linear write-back (optional)

Per-poll fields that let lola narrate the agent's progress back onto the Linear
issue — advancing the issue's **workflow state** and/or posting a short comment
at each lifecycle point (P4). **Entirely optional**: every field defaults to
`""` (no transition / no label) or `false` (no comment), so a poll that sets
none of them behaves exactly as before. All IDs are Linear UUIDs; they are
validated only for non-emptiness where a feature requires one and are **never**
resolved against Linear at config time (that is a runtime check).

| Key | Type | Description |
| --- | --- | --- |
| `on_spawn_state_id` | string | Workflow state the issue moves to when a session is spawned. `""` = no transition. **Required** when `dedup_mode = "state"`, and then must **not** be one of `state_ids`. |
| `on_pr_state_id` | string | State when the agent opens a PR (e.g. "In Review"). `""` = no transition. |
| `on_merged_state_id` | string | State when the PR merges (e.g. "Done"). `""` = no transition. |
| `blocked_label_id` | string | Label added on escalation (agent blocked, needs a human). `""` = none. |
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
`dedup_mode`: e.g. a `label`-dedup poll can still set `on_pr_state_id` to move
the issue to "In Review" when a PR opens. Only `dedup_mode = "state"` *depends*
on `on_spawn_state_id` (that transition is what dedups the issue).

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

Useful afterwards:

```sh
launchctl kickstart -k gui/$UID/com.user.lola     # (re)start now
launchctl bootout gui/$UID/com.user.lola          # uninstall
lola status                                       # is it healthy?
lola logs -f                                      # watch it work
```

## How a tick works (short version)

For each enabled poll, every `poll_interval`:

1. Check the native runtime is healthy: `tmux` available, `git` and `claude`
   resolvable, and the poll's `[[project]]` resolves. Unhealthy → skip tick,
   record the error in `status`, mutate nothing.
2. Resolve the API key (keychain → env). If `cycle_mode = "active"`, resolve
   the team's active cycle now.
3. Query matching issues (paginated, 100 per page, until exhausted).
4. Drop issues already in-flight in another poll, then apply the poll's dedup
   mode.
5. Sort by `priority_sort`, take up to
   `min(concurrency_cap, global_cap − live counted native sessions)`.
6. Per issue: record it as in-flight/seen **first**, then spawn the native
   session — a git worktree from the project (`post_create` + symlinks), a tmux
   session running `claude --settings <generated hooks>` with the issue's
   identifier and title as its briefing — then (label mode, on success only)
   re-read the issue's current labels fresh and flip them: remove all trigger
   labels, add the sent label.

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
observes its own worktree + tmux + Claude Code sessions, and every poll targets
a `[[project]]`. See [PLAN.md](PLAN.md) for the roadmap; the reaction engine
that acts on CI/review state (P3) is still future work — today the observer
tracks PR/CI but does not yet react.
