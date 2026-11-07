# One session view for every agent

This is the design of the desktop app's session view: what a session looks like, why, and the rules that keep it that way. It is a maintained document. When the view changes, change this. `CLAUDE.md` carries the invariants that code must not break, and `docs/ssh-sessions.md` is the engineering contract for hosts. Where this and the code disagree, read the code and fix whichever is wrong.

Path shorthand: `FE/` = `desktop/frontend/src/lib/`. `S/` = `FE/components/session/`. `C/` = `FE/components/ssh/`.

A few values below are stated as they were designed. The ways the build differs, and why, are in "As built" at the end.

---

## 1. Principles

1. **A session is one story with one place to speak.** Connections, widgets, diffs and PRs happen in the story. They are not places you go.
2. **Top is what happened to the work. Bottom is what you do now.** The delivery axis (PR, CI) lives in the top bar. The agent axis (working, resting, needs you) lives at the bottom left of the session, in every view. Each fact is said once.
3. **Containers mean something.** A *plate* means lola witnessed this. A *card* means you can act on this now. Everything else sits on the ground with no box. This one rule delivers most of the calm and most of facts-versus-claims.
4. **One focal point per state.** Blur at 8px and one region reads first. Section 2.6 names it per state.
5. **Nothing moves by itself.** Roster order is stable. An agent post never changes the layout. A card never takes focus by appearing. Nothing reorders by recency.
6. **Nothing hides behind a bare count.** A collapsed run still names files and commands. A failure, a running tool and anything waiting never collapse.
7. **The terminal is a peer.** One always-visible tab, one chord (`⌘\`), a sticky preference, automatic for sessions where the pane is the point.
8. **↩ never interrupts.** Human messages go immediately to a proven empty composer, including during a turn; the agent decides when to act on them. A draft, permission dialog, unsupported input, or screen keeps them queued. Automation and slash commands retain their idle gates. Stopping is a named, separate act with a stated scope.
9. **Honest over smooth.** No fake streaming, no "read", no "no network", no green tick on anything the agent wrote. Degradation is visible.
10. **Fun comes from competence.** Reopening a long session as a readable story, the ⌘J rhythm, the receipt at the end of a turn, a widget one click away. No confetti, no mascot, no sound.

---

---

## 2. The design

### 2.1 Nouns

The UI uses only these. "SSH tab", "workspace", "Agent tab" and "live agent terminal" leave the copy.

| Noun | Meaning |
|---|---|
| Session | One row in the roster. One agent, one story, one worktree or folder. |
| Conversation | The session's story and composer. The default view. |
| Terminal | The agent's real TUI in tmux. |
| Changes | What the session did to the code: diff, turns, comments. |
| Host | An ssh connection. A scope inside a session, never a view. |
| Widget | A sandboxed page an agent posted. The owner's word, so it stays. |
| Waiting | Sessions blocked on a human or regressed with nothing working on them. |

### 2.2 Depths

Two depths only. The list-above-terminal habit survives without an attached client.

- **Overview** (`nav.focusedTerm` empty). List, board or grid lens. The band below the list is a **Glance**: read-only, no xterm, no composer. Moving `j`/`k` no longer attaches a tmux client, which today silently defers every automated hand-off for the selected row.
- **Session** (`nav.focusedTerm` set). Enter, double-click, roster click, ⌘K, ⌘J. Fills `<main>`, under the top bar.

Roster click opens the session. Table or board row click selects and shows the Glance, as today.

> Changed after the first build: the band under the list is no longer a read-only Glance. It is the one session view at a smaller height, composer, tabs and terminal included, so Enter only changes its height. See "One view at two heights" at the end of this file.

**Everything that used to change the compact embed must now open the session,** or it acts on something nobody can see, because the Glance has no tabs, no composer and no tray. That covers `s`, `f`, `⌘T`, `⌘⇧D`, `⌘L`, `⌘\`, `h`, `A`, `⌘⇧A`, the `SSHChip` click, `ssh.open`, and the menu's Add shell, Review diff, Checkpoints, Open SSH view and Connect. `<` and `>` do nothing in Overview. All of them go through one idempotent `nav.openSession(id)`: it sets `selectedId` and `focusedTerm`, leaves the grid lens with `returnLens` as `toggleFocusTerm` does, and returns to the cockpit view without changing project scope. It never toggles. `projectActions.openSession` calls `toggleFocusTerm`, so a roster click on the open session would close it, and it also re-scopes the list. `ssh.reviewNext` (`ssh.svelte.ts:488`) drops focus and would land on a Glance: it uses `nav.openSession` too.

### 2.3 Region maps

Scale: sidebar 248, top bar 44, strip 32, host bar 32, tier band 2, story line 24, footer row 28. Bands have fixed heights and never wrap, so the PTY never reflows because a chip arrived. `←` is `<Button icon>` and the only drawn way back to Overview.

#### A. Resting, agent only: the baseline

```
┌ 248 ──────────────────┬────────────────────────────────────────────────────────────────────────┐
│ lola                  │ ←  Fix login redirect  ENG-42    #412 ✓ CI ▾   ▷ :8000 ↗             ⋯ │ 44
│ Sessions  Projects    │ Conversation  Terminal  Changes +84 −12      zsh  dev: composer dev  + │ 32
│                       ├────────────────────────────────────────────────────────────────────────┤
│ LOLA                  │        Plan 5/5 · Done, waiting on review                     Report ▾ │ 24
│ ● Fix login redirect  │                                                                        │
│   Done 4m ago · #412  │        ░ Why does the redirect loop after logout?                   ░  │ you: soft fill
│ ◐ Tenant DB        ✎  │                                                                        │
│   Bash · 2m           │        Read  auth.go, middleware.go, session.go +3                     │ 12 mono, faint
│ ◐ Docs nav            │        Edit  middleware.go                                   +12 −3    │
│   Edit                │        Ran   go test ./internal/auth                         ok  4s    │
│ WEB                   │                                                                        │
│ ○ Fonts               │        The cookie was cleared after the redirect fired, so the         │ 14/22 ink:
│   Done 2h ago         │        middleware saw a live session and bounced you back. I moved     │ FOCAL with
│ SSH                   │        the clear ahead of the redirect and added a regression test.    │ the caret
│ ◐ prod-api outage     │                                                                        │
│   prod-api            │        Done · 2m 31s · +84 −12 in 6 files · #14                        │ 12 faint
│ TEMPORARY          +  │                                                                        │
│ $ scratch             │       ╭──────────────────────────────────────────────────────────────╮ │
│                       │       │ Reply to Claude…                                             │ │ field, 14/22
│ All clear · 3 running │       │ ○ Resting                                             Send ↩ │ │ footer 28
│ ⚙ budget 41%          │       ╰──────────────────────────────────────────────────────────────╯ │
└───────────────────────┴────────────────────────────────────────────────────────────────────────┘
```

The timeline and centered footer use one chronological column at every size; see "The Conversation's layouts" for reading measures and typography.

#### B. Working, with a queue

```
│ ←  Fix login redirect  ENG-42    #412 ◌ CI ▾                                                 ⋯ │
│ Conversation  Terminal  Changes +60 −4                                                       + │
├────────────────────────────────────────────────────────────────────────────────────────────────┤
│        I'll add the column first, then backfill.                                               │
│        Read  schema.sql, users.go                                                              │
│        Edit  0042_add_flag.sql                                                 +18             │
│        Ran   go test ./db                                              running  0:41           │ FOCAL: the only
│                                                           ↓ 3 new                              │ thing that moves
│        Queued  also cover the rollback                                       Edit  ×           │ 24 each, unboxed
│        also cover the rollback                                               Edit  ×           │ 28 each, unboxed
│       ╭──────────────────────────────────────────────────────────────────────────────╮         │
│       │ and check the index name                                                     │         │
│       │ ◐ Working 3m 12s  ■ Stop ⌘.                    Stop & send       Send ↩     │         │
│       ╰──────────────────────────────────────────────────────────────────────────────╯         │
```

Tool timer on the row, turn timer in the footer, and they measure different things. `Stop` keeps one position whatever the field holds. `Stop & send` appears only with text in the field. Primary keeps its position and size; its label names what ↩ does.

#### C. Needs you: permission prompt

```
│ ←  Tenant DB  ENG-417    No PR                                                               ⋯ │
│ Conversation  Terminal  Changes +88 −4                                                       + │
├────────────────────────────────────────────────────────────────────────────────────────────────┤
│        Edit  tenants.go                                                +60 −4                  │ story stays,
│        I need to run the migration against the test database.                                  │ no dimming
│       ┏ Claude wants to run ─────────────────────────────────── 1 of 2  ‹ ›   Terminal ⌘\ ┓    │ FOCAL: the only
│       ┃ psql "$TEST_DB" -f migrations/0042_tenants.sql                                    ┃    │ orange on screen
│       ┃                                                                                   ┃    │ (mono, whole,
│       ┃ 1 Yes                                                                    primary  ┃    │  never clipped)
│       ┃ [2] Yes, and always allow access to migrations/ from this project                 ┃    │ pane order, pane
│       ┃ [3] Yes, and switch to auto mode                                                  ┃    │ words, verbatim
│       ┃ 4 No  Ends this turn                                                              ┃    │
│       ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛    │
│       ╭──────────────────────────────────────────────────────────────────────────────╮         │
│       │ Queue a note for after you answer…                                           │         │ composer loses
│       │ ◉ Waiting on the card                                           Queue ↩      │         │ its primary:
│       ╰──────────────────────────────────────────────────────────────────────────────╯         │ `secondary`
```

Card height is capped at 45% of the column. Body scrolls, decision rows stay pinned. Question card: same shape, question in 14/22, options numbered with their descriptions, single-select only. A multi-select or free-text question shows its text and `Answer in terminal ⌘\`.

#### D. Modal, or a screen in the pane

```
│       ┏ Claude opened a screen that only takes keys ──────────── open 2m ─────────────┓        │
│       ┃  (up to 8 lines of the pane, mono, faint)                                     ┃        │
│       ┃                                                          Open terminal ⌘\     ┃        │
│       ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛        │
│       │ ◉ A screen is open in the terminal                              Queue ↩      │         │
```

No field exists on this card, ever.

#### E. Stopped, queue held

```
│        Ran   go test ./db                                            stopped  0:41             │
│        You stopped this turn. Background shells keep running.                                  │ moment, 12 faint
│        Stopped · 41s                                                                           │ receipt
│        Held                                                                                    │
│        also cover the rollback                                               Edit  ×           │
│       │ ○ Stopped · 1 held                                               Send them ↩  │        │
```

#### F. Host connected, production, several hosts

```
│ ←  Why did the api restart                                                                   ⋯ │ 44
│ Conversation  Terminal                       $ prod-api  $ staging  zsh   +        Widgets 1 • │ 32
│═══════════════════════════════ 2px: bad, highest live tier ════════════════════════════════════│ 2
│ All  [PROD] prod-api ●  staging ●  + Connect                       Everything ▾  Take the wheel ⋯│ 32
├────────────────────────────────────────────────────────────────────────────────────────────────┤
│        ░ Why did the api restart overnight?                                                 ░  │
│        Looking at the unit's state first.                                                      │
│       ┌──────────────────────────────────────────────────────────────────────────┐             │ PLATE: lola
│       │ prod-api  $ systemctl status api                          exit 0 · 0.4s ▸│             │ witnessed it
│       └──────────────────────────────────────────────────────────────────────────┘             │
│        High  OOM killer ended the api at 03:12                                                 │ claim: no box
│       ┏ Claude wants to run on prod-api  [PROD]  Expires 3:58 ━━━━━━━━━━━━━━━━━━━━┓            │ existing approval
│       ┃ journalctl -u api --since '03:00' | tail -200                             ┃            │ card, its rules
│       ┃ Deny…                                            Run once on prod-api     ┃            │ unchanged
│       ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛            │
│       │ Talk to Claude…                                                              │         │
│       │ ◐ Working 1m  ■ Stop ⌘.        Pick a host above to run         Send ↩      │         │ All + 2 live:
```

Scope selected, or exactly one live host: secondary reads `Run on prod-api ⌘↩`. Run mode changes the whole field, and the ring and ground take the tier, never the accent: production `border-bad bg-bad/5`, staging `border-warn bg-warn/5`, dev `border-ink/40 bg-canvas`, plus mono, `$`, a 3px tier rail and a footer rule in the tier's colour (the rule is always there and transparent in Ask, so holding ⌘ moves nothing), `deploy@prod-api:/srv` on the left of the footer, `Ask ↩` as secondary, `Run on prod-api ⌘↩` as primary. Dev tier rail is neutral `bg-ink/40`, not accent.

#### G. Widget posted, dock open

```
│        ┌───────────────────────────────────────────────────┐ │ Queue depth ×  Errors ×  by Claude │
│        │ Queue depth   by Claude                    Open   │ │────────────────────────────────────│
│        │ Live count of the jobs waiting                    │ │                                    │
│        └───────────────────────────────────────────────────┘ │  (sandboxed frame, frame layer)    │
│        OOM killer ended the api at 03:12                     │                                    │
│       ╭──────────────────────────────────────────────────╮   │                                    │
│       │ ◐ Working 1m  ■ Stop ⌘.                 Send ↩  │   │                                    │
```

`Open` is the per-session consent click and the open, in one gesture. The strip's `Widgets 2 •` toggles the dock (`w`). A post creates a card and a dot. It never creates a panel. Below `MIN_MAIN` the dock is the existing drawer.

#### H. Terminal view (also Changes and shell tabs: same footer strip)

```
│ ←  Fix login redirect  ENG-42    #412 ◌ CI ▾                                                 ⋯ │ 44
│ Conversation  Terminal  Changes +60 −4                                                       + │ 32
├────────────────────────────────────────────────────────────────────────────────────────────────┤
│  real xterm, attached only while this view is on screen, every agent key works                 │
│                                                                                                │
├────────────────────────────────────────────────────────────────────────────────────────────────┤
│ ◐ Working 3m 12s  ■ Stop ⌘.     1 queued · waits until you leave      Waiting 2 ⌘J  Conversation ⌘\ │ 28 fixed
```

The strip always exists in the view, so the PTY never resizes. Bottom-left state sits at the same place as the composer footer in map A. That shared position is the continuity that makes the axis findable.

#### I. Changes

```
│ All vs base   +142 −30 │ internal/retry/retry.go   +12 −3                                       │
│ Turns                  │ + if delay > maxBackoff { delay = maxBackoff }                         │
│  #14 · just now        │   ┌ Your comment ──────────────────────────────┐                       │
│  #13 · 14 min          │   │ Why not reuse the shared limiter?          │                       │
│  ⟲ before restore      │   └────────────────────────────────────────────┘                       │
│ Files                  │                                                                        │
│  ● retry.go            │                                                                        │
├────────────────────────┴────────────────────────────────────────────────────────────────────────┤
│ ○ Resting                                    3 comments drafted   Send to Claude   Conversation ⌘\│
```

Restore and Fork sit on a selected turn, copy: "Files only. The conversation and git history do not move."

#### J. Overview with Glance

```
│ Sessions · 7                                             List  Board  Grid        budget 41%   │ 44 unchanged
│ (table: fit-content(45vh), scrolls past it)               band: minmax(0,1fr), the rest         │
├────────────────────────────────────────────────────────────────────────────────────────────────┤
│ ◐ Working 3m   Fix login redirect  ENG-42                #412 ◌ CI            ↩ Open  ⌘\ Terminal │ 32
│    Edit  middleware.go                                                 +12 −3                  │ tail: last rows
│    Ran   go test ./internal/auth                              running  0:41                    │ newest, scrolls, same
│    The cookie was cleared after the redirect fired…                                            │ row components
```

A session whose sticky view is Terminal, or an agentless one, shows a `SnapshotTile` (capture-pane, no attach) instead of the tail.

#### K. Narrow (main column under 900px)

```
┌──────────────────────────────────────────┐
│ ◉3 ← Tenant DB       #412 ✗ ▾          ⋯ │ 44  ◉3 appears when the sidebar is hidden; dev chip and
│ Conversation  Terminal  Changes  ⋯ 2     │ 32  action move into ⋯; second tab group into `⋯ N`
│ prod-api ▾                               │ 32  HostBar becomes one picker
│  Edit  tenants.go              +60 −4    │
│ ┏ Claude wants to run ───── 1 of 2 ───┓  │     cap 60%; the rows stay
│ ┃ psql "$TEST_DB" -f …                ┃  │
│ ┃ 1 Yes  4 No                  More ▾ ┃  │     qualified options go under More, shown in full
│ ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛  │
│ │ ◉ Waiting above            Queue ↩  │  │
└──────────────────────────────────────────┘
```

The dock becomes a drawer before the conversation squeezes. The story line hides under 520px of column height.

#### L. Other states, by what replaces what

| State | Timeline | Bottom |
|---|---|---|
| Feed `partial` | One 12px faint line at the top: "Some of this conversation could not be read. The terminal has all of it." + `Terminal ⌘\` | Composer normal |
| Feed `unreadable` or `unsupported` | Notice row, word `warn`: "This Claude Code version's log isn't readable. Your agent is fine." + `Terminal` button. If the human has no sticky view and the last health this client saw for the session was bad, the next open lands on Terminal. An open that learns this only after it painted stays put and shows the notice: it never flips. | Composer normal: it does not depend on the feed |
| No log yet | Task title, the `initial` row if there is one, "Claude is reading the task". `prompt.md` is not on the wire and stays off it | Composer normal |
| Agent gone | Story stays | Draft stays. `✕ Claude exited`, secondary `Queue ↩`, primary `Revive`. Queued rows stay visible and recallable. |
| Quota limited | Story stays | `◉ Usage limit · resets 14:00`, `Switch agent…`, `Queue ↩` |
| Daemon down | Last fetched items stay, controls quiet | Draft stays, send disabled. `Daemon down. Your draft is kept and nothing is queued.` + `Terminal ⌘\`. Sidebar: `State unknown`, never "All clear". |
| Ended | Read-only story, top bar shows `Ended · PR #412 merged` | No composer. Line: "This session has ended. Its worktree is gone." + `Open PR ↗` |
| Goal attempt | Opens on Terminal. Conversation is read-only. No Changes tab. | No field. Line: "A goal runs this session." + `Open goal` |
| Agentless (temporary, shell) | No Conversation. Strip only when more than one terminal exists | Footer strip only |
| Fork | First row is a divider naming parent and checkpoint | Composer normal |
| Best-of attempt | Normal. The action slot reads `Compare attempts` | Composer normal |
| Stack | Normal. Rows about one app name it. Delivery chip is the focus app, `+2 apps` in its popover | Composer normal |

#### M. New session

Modal stays (`NewSession.svelte`), 560px, prompt first.

```
┌ New session ──────────────────────────────────────┐
│ What should Claude do?                            │ text-lg
│ ╭───────────────────────────────────────────────╮ │
│ │ (textarea, 14/22, autofocus, grows to 8 rows) │ │
│ ╰───────────────────────────────────────────────╯ │
│ Project [lola ▾]   Run [Coding agent ▾]           │ Run: Shell hides the prompt, the session is agentless
│ ▸ Options  agent, location, branch, name          │ branch prefilled, see below
│                               Cancel   Start ↩    │ ⇧↩ newline
└───────────────────────────────────────────────────┘
```

Branch defaults to `<branch_prefix><Slug of first 6 words>`, editable under Options, so the prompt alone can start a session. `OpenManualArgs.Prompt` already exists; confirm `handleOpenDirectory` honours it for `main`. The prompt becomes the story's first row (origin `initial`). A shell in an existing session is ⌘T, another tab. The temporary shell stays ⌘⇧N.

### 2.4 Top bar and strip contents

**Session bar**, inside `MainTopBar` when `nav.focusedTerm` is set. `S/SessionBar.svelte`, `grid-template-columns:auto minmax(0,1fr) auto`, 44px, stays the drag band, no wrap.

1. `←` `<Button icon>`, Overview.
2. Title 13/500 ink, then issue key 12 faint. Click renames. Hover card: agent, model, branch, worktree, sandbox, ports, usage. The filled location, agent and sandbox chips are gone.
3. **Delivery chip**: `PrBadge`, absent with no PR. Click opens a popover: checks grouped required and other, review gate, `ReviewTallies`, merge queue position, overlaps, per-app rows for stacks, then `Open PR ↗` and `Trigger review`.
4. **Dev chip**, only when `devCommands` is non-empty: `▷ Dev`, or `▷ :8000 ↗` with a green dot. URL opens through `store.openURL`, never `window.open`.
5. One contextual action at most, `variant="secondary"`: `Resolve conflicts` > `Revive` > `Compare attempts`. Never solid.
6. `⋯`: the shared `sessionMenu` (unreachable from fullscreen today), plus `Open sessions in: Conversation | Terminal`.

Removed from the header: skull, Finder, layout toggles, `SSH` button, `Active`, `Review`, `flex-wrap`, the fullscreen accent frame (`SessionsColumn.svelte:88-90`). Connect lives in `+`. `sessionMenu` carries `Open in Finder`, since Finder has no other home. `⋯` carries `Report`, which switches to Conversation and opens the story line's overlay, because Terminal and Changes have no story line and a terminal-first user would otherwise lose the report.

**View strip**, `S/ViewStrip.svelte`, 32px, real `role="tablist"`, arrow keys.

```
Conversation ◉  Terminal  Changes +84 −12 • 3    $ prod-api  zsh  review  dev: x  +      Widgets 2 •
└── fixed, never moves, never scrolls ──────┘    └── mono, scrolls past a fade ──┘        └ fixed ┘
```

- Active: `text-ink` plus a 2px `bg-ink` underline. Inactive: `text-faint`. Never colour alone.
- 24px gap between groups, no rule. The second group is mono because those are machine things.
- `◉` on Conversation only when a card waits and you are elsewhere.
- `×` only on the second group, on hover or focus. A host's `$` cell has no ×. Removing a host and its record moves to the host chip's menu behind `confirm`.
- `+` sits right after the tabs it adds to, and keeps its place when they scroll past the fade. It is a `MenuItem` list: New shell ⌘T, Connect to host…, Dev commands.
- `<` `>` ring over every cell in visual order. Selection persists per session in `lola.tabs`. A host's `$` cell selects that shell (`view.shellConn`) and leaves the scope alone, so under All the story stays All. Picking a scope does move the shell to that host, and a command a person runs opens the shell of the host it ran on, because a shell looked at earlier must never be what takes the keyboard after a Run. New code reads `view.scope` and `view.shellConn`, never `view.active()`, which answers neither question.

**Host bar**, `C/HostBar.svelte`. 32px, only with a connection. Segmented scope, default **All**. Right side: `deploy:/srv` of the selected host, grant state, `Everything ▾`, `Take the wheel` (keeps today's interrupt-and-take), `⋯` with Root shell, Close, `Remove host and its record…`. The tier band sits directly above it and follows the selected scope, or the highest live tier under All.

**Sidebar**, `Sessions | Projects` tabs (`Tabs.svelte`), default Sessions.

- **Projects** tab is exactly today's body: `SidebarTriage`, `SidebarProjects`, `SidebarActivity`. Nothing moves in it, so folders and drag are untouched.
- **Sessions** tab is `SidebarRoster`, whose groups include direct host sessions and temporary shells, plus `SidebarWaiting` in the `auto` row.
- **Roster order is stable:** config project order, then `firstSeen`, then id. Never `store.sessions` order, which sorts attention first. Groups use the `label` utility, 24px. Group order: projects, `SSH`, `Temporary` (with `+`), then a collapsed `Ended N`.
- **Row:** 44px, two lines, no border. Selected `bg-sel`, hover `bg-sel/50`, no accent. Line 1: glyph (16px cell), title 13/18 ink truncating, `✎` if a draft exists, age 12 faint tabular when it matters. Line 2: 12/16, first that applies:

| Line 2 | Colour |
|---|---|
| lola's own ask: "Wants permission", "Has a question", "A screen needs a key", "Approve on prod-api" | `text-orange` |
| "CI failed", "Changes requested", "Conflict" | word in `text-bad` |
| `currentTool` name, with age | `text-faint` |
| Reported blocker | `text-faint`, prefix "Says blocked:" |
| "Done 4m ago", then PR number | `text-faint` |

- **State glyphs, this vocabulary and only here (roster, footer, Glance header):**

| Glyph | Means | Colour |
|---|---|---|
| `◉` | Needs you | `text-orange` |
| `◐` | Working | `text-info`. Static in the roster. Only the open session's footer glyph pulses, and not under reduced motion. |
| `●` | Finished a turn you have not seen. Drawn as a 6px filled dot in `bg-ink-2` (`StateGlyph.svelte`), never a full-size disc, because it must not outweigh `◉` | `text-ink-2` |
| `○` | Resting, seen | `text-faint` |
| `✕` | Gone | `text-faint` |
| `$` | Shell | `text-faint`, mono |

- **Waiting line**, 28px: `◉ Waiting 3 · ⌘J` in `text-orange`. Empty: `All clear · 3 running`, faint. Down: `State unknown`. Row lines already carry the ask, so there is no second list.

### 2.5 Timeline

The newest tool action keeps its output open until the next action arrives. Completed file operations and git actions use explicit verbs (Edited, Deleted, Moved, Committed, Pushed); checkout setup stays in the tooltip. Source excerpts and recorded edit patches are highlighted. Compiler diagnostics wrap separately from code, with file locations, error severity, aligned gutters, and distinct test results.

Send clears the draft immediately and shows a local `Sending…` receipt and spinner until the daemon responds. Accepted messages remain visible until the timeline catches up. A failed send restores the draft without dropping newly typed text. Image attachment reserves its token and a loading tile before preparation starts; the tile becomes its thumbnail in place. Send waits for preparation, and removing a tile prevents a late response from restoring it.

One rail with the agent beside it and the person across from it. This table is the first version's: its marks, the rule against them and its sides are replaced by "The timeline layout" and "The Conversation's layouts" below, which win where they differ. Voice still comes from container and type.

| Row | Look (exact) | Source | Voice |
|---|---|---|---|
| Your message | `w-fit max-w-full rounded-md bg-sel/35 px-3 py-2 text-prose`, text `text-ink-2`, no name. It hugs its text and keeps the left edge: full ink on a column-wide fill read first in every state and looked like a dead input. Folds past 12 lines behind `Show all`. Status word below only while unsettled: `Sending`, `Returned to Claude's prompt · Terminal`, `Recalled` | Local pending receipt, then store human item; never the feed | Pending until lola acknowledges the send |
| Typed in terminal | Same size, no fill, 12 faint label `In terminal` above, folds past 12 lines behind `Show all` | Feed user record that matched no send | Claim |
| Task, system or teammate turn start | 12 faint, one line: `Background task finished`, `Message from accept-job · its summary`. Another agent's markup and JSON never reach the row | Feed origin | Claim |
| Agent prose | `text-prose text-ink max-w-[72ch]`, `SSHRich`. No box, no name | Feed text | Claim |
| Tool row | 24px grid `[56px minmax(0,1fr) auto 16px]`: verb mono 12 faint, target mono 12 `text-ink-2`, status + duration mono 12 faint `.num`, chevron slot. Baseline-aligned on `leading-6`. Zero sides of a diff are dropped (`+18`, not `+18 −0`). **While running:** a current-line tint `bg-info/12` that bleeds 8px past the column, verb flips (`Ran` to `Running`, `Read` to `Reading`, `Edit` to `Editing`, `Write` to `Writing`, `Search` stays) in `font-medium text-ink`, target mono 13 `font-medium text-ink`, timer `font-medium text-info`. Nothing loops. | Feed tool | Claim, **including its `ok`**. Never the word "exit", never a tick, never `text-good`. `failed` is `text-bad` while nothing has answered it. Once the agent has spoken after it in the same turn (`settled`) the word stays, in `text-ink-2`, so red means unaddressed. A failed row still never groups. |
| Evidence on a claim | No box. One mono 12 faint line directly under the finding's title and above `More`: clipped command, then `exit 0 · 0.4s` | Store block | Fact, about the command only |
| Plate (ssh block) | `rounded-md border border-edge/60 bg-panel px-3 py-2`, mono 12, `exit 0 · 0.4s` at a fixed right edge, host name under All (`showHost`), `Private · Share` when private | Store block | Fact |
| Claim element (finding, plan, hypothesis, metric, narration) | Unboxed. Severity is a word: `font-medium`, `text-bad` for critical and high, ink otherwise. Never orange. | Store element | Claim |
| Widget card | `rounded-md border border-edge/60 bg-panel px-3 py-2`, title 13/500, `by Claude` 12 magenta, one faint line, `Open` secondary xs, `Shown` and disabled while its panel shows | Store element | Claim, a card because you can act |
| Moment | 12 `text-faint`, one line, lola's sentence. Failure word `text-bad`. A hover action is `xs` ghost. | Store system event | Fact |
| Divider | Hairline + 12 faint label: `New session started`, `Compacted`, `Context cleared`. A previous life collapses behind `Earlier session, 4 days ago ▸` | Store or feed | |
| Receipt | 12 faint: `Done · 2m 31s · +84 −12 in 6 files · #14`. On hover or focus, same line: `View changes  Restore  Fork`. Interrupted: `Stopped · 41s`. No checkpoint, no number. | Store checkpoint event | Fact |

Questions and proposals from `lola ssh post` leave a plain record row here and their controls go to the tray. A proposal with no host shows `Copy`, never `Run`.

**Tool collapse.** Ladder: run line, row, bounded output (lazily from `agentFeedOutput`), then Changes, Terminal or full output. Pure in `FE/turns.ts`.

| Category | Rule | Run line |
|---|---|---|
| Read, search, list | Consecutive completed ones group at once | `Read  auth.go, middleware.go, session.go +3` |
| Edit, write | One row per file up to 3, then grouped. Row click opens Changes at that file | `Edit  middleware.go, routes.go +4   +60 −12` |
| Bash and others | One row per call. 3 or more clean in a row group | `Ran  go test ./…, go vet ./… +1   12s` |
| Subagent | Always its own row. Expands to one indent step, read on demand | `Agent  Find the auth bug · 3m` |
| Thinking | Nothing. 1138 of 1227 blocks were empty | |
| `lola report`, `lola ssh` calls | Dropped: the store shows them as facts | |

Hard rules:

1. Never a bare count. A run line names as many items as fit, then `+N`. Paths clip in the middle, commands at the end. Full value on hover and focus.
2. A failed, running or interrupted call never groups. A run splits around it. A failed row opens its 240-character preview, one 16px indent.
3. A run of more than 8 rows between prose collapses by category into one run line per category, whatever the rule above says. A real turn is hundreds of tool rows around one-sentence prose (p50 148 characters) and this is what keeps prose the figure.
4. A human's expansion survives re-render. Header stays stationary, content grows down.
5. Running state comes from an unresolved `tool_use` in the log. Reconcile: if the agent is not working, an unresolved call reads `no result`, never `running 3h`. Codex logs a command only on completion, so it shows nothing rather than a guess, plus one quiet line the first time: "Codex reports commands when they finish."

**Merge** (client side, so no private block can reach an agent-readable path): `agentFeed` + `sshTimeline`, sorted by `deliveredAt ?? at`, stable within a source. A forged time can misplace a row and do nothing else. Undelivered human items are not in the log: they are queue rows in the action zone.

**Turn.** Starts at a human message or other trigger, ends at Stop or interrupt. Older turns than the last 10 fold to first answer line + receipt and mount no rows. Off-screen turns are placeholders at their measured height (IntersectionObserver, 2-screen margin). Older history pages with `Before`. The composer has no dependency on the items effect, so typing cannot lag behind a 2,000-row story.

**Scroll.** A short story sits on the action zone: the scroller is a grid with an empty `minmax(0,1fr)` row above the column, which collapses to nothing once the story overflows, so what the agent said stays beside what you act on. Not `justify-end`, which breaks scrolling up, and not a flex auto margin (WebKit). No `overflow-anchor` (WKWebView lacks it). ResizeObserver stick-to-bottom, threshold 60px, cancelled on the same scroll-up gesture. A selection suspends following. Sending returns to the bottom. Expanding pins the header. The jump pill says what waits: `3 new` or `Waiting for you`. 

**Since you looked.** Counted locally, no LLM: `──── Since you looked: 3 turns · 11 files · CI passed ────` at the first unseen row. Keyed on a timestamp in `lola.read.<id>`, never on feed seq (it resets on restart). The view opens at the tail with a pill `Since you looked ↑`, so it cannot steal your place. `u` jumps.

**Story line.** `S/StoryLine.svelte`, with `BoardPanel` in its overlay. One 24px sticky row, only with a board or brief: `Plan 3/7 · Writing the migration        Report ▾`. Report expands as an overlay (`absolute`, max 60% height, Esc closes) and never pushes the timeline. A reported blocker is orange text, with no Attention, count or tray effect. "Unverified claim" stays here.

**Accessibility.** `role=log`, named, `aria-live=off`. A separate always-mounted `role=status` gets lifecycle only: turn started, finished, stopped, card waiting. Rows carry visually hidden sender headings. Generation never moves focus. Focus returns to the composer when a card it left is gone. Selection works everywhere, ⌘C copies.

### 2.6 Action zone

Bottom up: composer, queue rows, tray. Nothing above moves when something below changes height, and the timeline stays stuck if it was stuck.

**Focal point by state:**

| State | Focal | Recedes |
|---|---|---|
| Resting | Last prose, then composer caret | Tool rows, receipt, bars |
| Working | The live tool row | Prior turns. The composer's ring is neutral even with focus: accent is the caret and the one primary |
| Needs you | The tray card | Composer loses its primary |
| Run mode | The whole composer field | Tier rail plus mono reads in grayscale too |
| Gone | `Revive` in the footer | Story stays readable |
| Terminal | The pane | Top bar and footer strip |
| Changes | The current hunk | File list, turn rail |
| Overview | Selected row and Waiting rows | Glance |

**Composer**, `S/Composer.svelte`, destination logic pure in `FE/composer.ts`.

```
╭───────────────────────────────────────────────────────────────╮
│ field: 14/22, grows to 40% of column height, then scrolls     │  rounded-[10px] border-edge bg-panel
│ STATE  [its action]                  secondary      PRIMARY   │  28px footer, three groups at most
╰───────────────────────────────────────────────────────────────╯
```

Primary is `variant="primary" size="sm"`, `min-width:88px`, always the same place. At most one filled control. Stop is `variant="danger" size="xs"`, neutral at rest, red on approach.

| Situation | Left (state) | Secondary | Primary |
|---|---|---|---|
| Resting, proof holds | `○ Resting` | | `Send ↩` |
| Working, empty field | `◐ Working 3m 12s  ■ Stop ⌘.` | | `Send ↩` disabled |
| Working, text | same | `Stop & send` | `Send ↩` |
| Tray card waiting | `◉ Waiting on the card`, orange on the glyph only, so the card stays the only orange sentence | | `Queue ↩`, `secondary` |
| Modal | `◉ A screen is open in the terminal`, words in `text-ink-2`. The card's `Open terminal ⌘\` is the window's primary and the way there | | `Queue ↩`, `secondary` |
| Pane holds a draft | `○ Claude's own prompt has text` + `Terminal ⌘\` | | `Queue ↩` |
| Stopped, held, empty field | `○ Stopped · 2 held` | | `Send them ↩` |
| Stopped, held, text | `○ Stopped · 2 held` + `Send them` secondary | | `Send ↩` |
| Host in scope, Ask | state as above | `Run on prod-api ⌘↩` | as above |
| All, 2+ live hosts | state as above | `Pick a host above to run`, faint, not a button | as above |
| Run | `deploy@prod-api:/srv` | `Ask ↩` | `Run on prod-api ⌘↩` |
| You drive a host | `You're driving prod-api` + `Hand back` | | as above |
| Gone | `✕ Claude exited` | `Queue ↩` | `Revive` |
| Agent cannot prove an empty prompt | `lola can't confirm this agent's prompt is free` + `Terminal ⌘\` | | `Queue ↩`, `secondary` |
| Daemon down | see 2.3 L | | `Send ↩` disabled |

**Controls a send can change under the cursor refuse clicks for 800ms after any send.** That covers `Stop`, `Stop & send` and `Revive`.

**Keys.** ↩ sends or queues, named in the footer. ⇧↩ and ⌥↩ insert a newline (⌥↩ submits today: fix it). ⌘↩ runs on a host, only with a host. With no host, ⌘↩ keeps the draft and shows one line: "Run needs a host. ⌘T opens a shell." Hold ⌘ and double ⌘ do nothing with no live host. ↑ on an empty field takes the newest queued message back, then walks history. History is per destination. Esc blurs and keeps the draft. There is **no Stop & send chord**.

**Drafts.** Agent draft keyed `"agent"`, in localStorage (`lola.drafts`, 50 sessions, 8 KiB). Command drafts per connection, memory only. History per host, so ↑ cannot recall a staging command into production. Prompt answers go nowhere near any of it. Both `✎` and the kept draft across every switch read from this.

**Input rules. `FE/slash.ts` is pure.**

| Input | Rule |
|---|---|
| `/` at column 0 | Small list under the field, filtered as you type, from a per-agent table. ↩ or Tab fills the exact name. A prefix is never sent. |
| Known inline (`/compact`, `/context`, `/rename x`, `/clear`) | Sent exact, alone. Output comes back through the feed. |
| Known screen (`/config`, `/usage`, `/cost`, `/help`, `/model`, `/resume`, `/agents`, `/mcp`, `/permissions`, `/login`, `/logout`, `/doctor`, `/memory`, `/hooks`, `/rewind`, `/status`, `/theme`, `/vim`, `/terminal-setup`) | **Never sent.** The row reads `Opens in terminal`, and ↩ switches to Terminal with nothing typed. Only `/config`, `/usage`, `/cost` and help screens were measured: the rest is judgement that errs safe. |
| Unknown `/word` at start | Footer warns: "lola doesn't know /word. Claude drops the text if it doesn't either." Second ↩ sends. Path-shaped `/tmp/foo` is not flagged. Codex and opencode tables are empty, so every `/word` takes this path. |
| Leading `!` | Daemon adds a leading space (measured to neutralize it), footer says "Sent as text. To run on this machine, use the terminal." |
| Trailing `\` | Stripped. It would otherwise sit in Claude's prompt while lola says working. |
| Over 800 bytes | Paste transport (section 4.4). Counter from 75% of the 8 KiB cap. |
| Text a widget filled | `lola.ask` and `lola.run` still only fill and keep every refusal they have. Field gets a 2px magenta left rail and a chip `Filled by Queue depth · Clear`, whatever its first character. A leading `!` or `/` gets a leading space. Nobody has measured whether a space neutralizes `/`, so a fill beginning with `/` is refused. |

**Queue rows**, `S/QueueRows.svelte`. Directly above the composer, 24px each on a `[56px minmax(0,1fr) auto auto]` grid, unboxed so they never resemble the composer. `Edit` (returns text to an empty field) and `×` (recall). The label `Queued` or `Held` sits in the first row's 56px gutter, so the text lines up with tool targets, and `sends together when it rests` goes on one 20px line after the rows, with more than one. In-flight commands while you drive use the same grid with `Running` in the gutter. A row shows a one-time reason when it waits on a terminal: `waits until you leave the terminal`. Delivered means it appears in the log.

**Tray**, `S/Tray.svelte`. The one home for "needs you". One card at a time, oldest first, stable, `1 of 3 ‹ ›`. **Never takes focus by appearing** (an old dock took it, and that bug is why this is a rule). Drop `role=alert` on the wrapper. Announce one short line through status.

Tier 1, orange, is what a fact says needs you: `SSHApproval`, `SSHGoCard`, `SSHConnCard`, `SSHPromptCard`, `PermissionCard`, `QuestionCard`, `ModalCard`, quota. Tier 2, neutral border, follows and is never counted: `question` and `proposal` elements. `DevClashBanner` sits last, neutral, re-verifying at click as it does today.

Every kind keeps its own rules. A password card is its own component, never mixed. Approval card: arm delay, read-to-end, pinned revision, shifted `Y`/`N`, stays; its warn words drop to one.

**Permission and question card rules:**

- Draws the pane's labels verbatim, in pane order. Danger by weight, never position. Exactly one primary: a label that is exactly `Yes` and has no qualifier, as wide as its words. Every other row is a `ghost` that gets its chip on hover, so a card is not boxes inside a box. A digit is a keycap with no brackets, on the title's left edge. A question's option has its description (`AgentChoice.Detail`) beside its name, and a row that needs typing ends in `Terminal ⌘\`. While a press is on its way its keycap sinks and its label says `Sent`, which claims only that lola pressed the key.
- Press path is `agentChoose` with a hash of the exact lines drawn, command included (section 4.4). A stale card refuses and redraws. Labels alone are not enough: "Yes" is identical across prompts.
- Keys on a focused card: the pane's digits, one press, no Enter. Dead for 400ms after appearing or after its text changes. Key repeat ignored. Enter does nothing. A long input must be scrolled to its end before keys and buttons arm.
- Focus reaches a card only by the human: ⌘J, Tab from an empty composer, or a click. Esc returns to the composer. Tab needs its own handler: the tray precedes the composer in the DOM, so a plain Tab moves on to the footer's buttons.
- The card carries no lock, logo or trust styling. A process can print anything.
- Answered: a row `You chose: 1 Yes` (origin `choice`, `Delivered` set at creation), drawn as a **moment, never the you fill**, because its label is pane text and the you fill is the one thing a process must not be able to put words into. The card clears from a pane re-read, because denied prompts fire no hook.

### 2.7 Terminal policy

- Attach only while the view is on screen. Leaving detaches. Mounted in the leaf `S/TerminalCell.svelte` only. One xterm per tmux session. Keep `{#key name}`. On screen also means `nav.view === "cockpit"`: Home and project views are an opaque overlay on a mounted cockpit, and a terminal left attached under one defers every automated send. `TerminalCell` is always rendered, with `name` possibly empty, and holds the only `{#if name}{#key name}`. No parent template gets an `{#if}` around it, `SessionEmbed`'s `{#if !session}` included: Enter and Esc now flip that branch, which they never did before, so the terminal's cell sits outside it.
- `Conversation` stays mounted behind it (`visibility:hidden`, `inert`) in the same grid cell, so widgets do not reload when you visit the terminal. The terminal is a sibling of `DockArea`, outside any `isolate`. **`visibility:hidden` on an ancestor does not hide a frame:** `DockArea:441` sets `visibility:visible` on the shown frame's wrapper, and a child's `visible` wins, so the widget would paint over the terminal and take its clicks. `DockArea` gets a `covered` prop, true for the agent terminal, shell tabs and Changes. Covered frames get `visibility:hidden`, no `pointer-events-auto` and `active={on && !covered}`. Only those expressions change: the each block keeps its structure, key and order. `active` false tells the page it is hidden, so a page may pause. It keeps its state and does not reload.
- A host's `$` shell stays what it is today: `SSHTerminalPane`, parked in the dock's main slot, name from `SSHConnInfo.tab`, never in `Shells`.
- `⌘\`: from Conversation, a selected host's shell, else the last terminal used in this session, default the agent's. From any terminal, back. A `Terminal` button sits on every card and notice that needs it.
- Esc in a terminal is the agent's. Ctrl-Q leaves on every surface: to Conversation, or Overview when agentless.
- Sticky: `lola.tabs` per session, `lola.session.view` global (Appearance and `⋯`), both localStorage because the daemon never reads them.

**`defaultView(session, sticky, globalPref, feedKnown)`, pure in `FE/sessionview.ts`.** In this order: agentless, shell, temporary or orphaned means `terminal`. Goal-managed (`goalId` is already on the wire) means `terminal`. Per-session sticky wins next. A global preference of Terminal next (the default of Conversation decides nothing here, or the rules below never run). A direct ssh session or one with a connection means `conversation`, whatever its agent: its story comes from the store, and opening a codex ssh session on Terminal would regress today. Agent with no feed reader, or `feedKnown === false` (the last health this client saw was `unreadable` or `unsupported`), means `terminal`. Otherwise `conversation`.

Grid tiles always read a terminal.

### 2.8 Keyboard

Five new or ungated chords, all in the Session menu and `HelpOverlay`:

| Chord | Action | Status |
|---|---|---|
| `⌘J` | Next waiting (see below) | New |
| `⌘.` | Stop this turn | New |
| `⌘⇧D` | Toggle Changes, return to previous view | New |
| `⌘L` | Focus composer. From a terminal, goes to Conversation | Needs no connection |
| `⌘\` | Toggle terminal | Needs no connection |

None collide with `desktop/main.go` or Wails `menuitem_roles.go`. `⌘0` is Reset Zoom, `⌘1-9` stay unspent. Existing chords stay. `Force Reload` stays on `⌥⌘R`.

**Every chord targets `nav.focusedTerm || nav.selectedId`.** `ssh.open`, `nav.select` and a fork can move it while a session is open. `⌘.` must never stop a session you are not looking at, and `⌘⇧K` has this bug today.

**⌘J.** `FE/queue.ts`, pure. Blocked (`displayFor === needs_you` or `ssh.needsHuman`, since `agentStateSince`) first, then regressed (delivery `ci_failed`, `changes_requested` or `merge_conflict` AND agent not working or starting, since `statusSince`), oldest first within each. Moves to the item after the current one, wraps. "Ready to review" is deliberately not in it: a resting agent is the normal state and would flood it. Landing: a card focused in the tray; a modal, or a session whose sticky view is Terminal, on the terminal focused; regressed on Conversation at the last moment row. In a session `n`/`N` do the same. In Overview they select the next row and stay there, as today. `A` and `⌘⇧A` keep their approval meaning and open the session.

**Bare keys.** `typing()` is the only guard, so an open session has its own branch: without it control would fall into the globals and `cockpitKey`, where `j` moves the list behind an open session, Enter closes it and `x` asks to kill. `inSession = nav.focusedTerm && nav.view === "cockpit"`: handle the session row below in a new `sessionKey(e)` and return. Otherwise run `A` and `W` from `ssh.bareKey`, the globals and `cockpitKey`. `i t F /` must not run in Overview: they bump focus counters for a composer that is not mounted, and the request fires on the next open. The focused timeline leaves keys it does not use to the app.

| Where | Keys |
|---|---|
| Session, no field focused | `i` composer, `t` timeline or card, `j k g G` rows when timeline focused, `[ ]` turns, `u` first unseen, `w` dock, `f` Changes, `/` find, `?` help, `n N`, `A`, `W` (with a host), `F` (with a host), `h` (same as ⌘\, every session; it needs a connection today), `b` sidebar, `< , > .` ring the strip |
| Overview | as today, except the keys in 2.2 that now open the session |

`x s c D R o P p d S V Enter` stay Overview-only. They are dead in fullscreen today, so nothing is retired, and a lost focus cannot fire them mid-sentence. Their session paths are chords and `⋯`. Clicking the timeline without selecting returns focus to the composer.

**Esc ladder.** Conversation: list or overlay → composer blur → timeline blur → Overview. One step per press. Esc never reaches the agent from here. Changes: `DiffView`'s own field first, then back to the previous view. A terminal holding the keyboard owns Esc and Ctrl-Q leaves; one without the keyboard takes Esc like Changes. Focus on arrival: composer in Conversation, xterm in Terminal, nothing in Overview.

### 2.9 Visual rules

**Type.** Add one token, nothing renamed: `--text-prose: 14px` with `--text-prose--line-height: 22px` in `app.css`, and edit its comment from five sizes to six. Use is agent prose, your messages, the card question and the composer field. Nothing else.

| Size | Use |
|---|---|
| `text-sm` 12/16 | Meta, chips, footer, receipts, moments, tool rows (mono) |
| base 13/18 | Controls, session title (500), roster title, card labels |
| `text-prose` 14/22 | Reading |
| `text-lg` 15/20 | Empty-state heading, New session heading |

At most three on a normal screen. Weights 400 and 500, plus the tokens' own 600. Mono only on machine facts. `.num` tabular on every timer, count and exit code. Delete `text-[12px]`, `text-[13.5px]`, `text-[10px]` (`SSHBlock:97,112,114`, `SSHTierTag:10`).

**Ink.** Three tiers: `text-ink`, `text-ink-2`, `text-faint`. `ink-2` is a token (`subtext0` walked to AA on canvas, panel and sel), because an alpha of ink lands under `faint` on latte and under AA on sel in frappé and macchiato. `ink` and `ink-2` clear 4.5:1 on all three surfaces in all four flavors. `faint` is the deliberate exception: 3:1 in the dark flavors (measured 3.02 to 3.40 on sel, 3.99 to 4.49 on panel), because a brighter one stops being de-emphasized, and AA on canvas and panel in latte, where 3:1 reads as washed out. Nothing essential rides on `faint`, and a selected roster row's line 2 steps up to `ink-2`. `text-faint` stays at 12px or above.

**Surfaces.** `bg-canvas` for sidebar and timeline ground. `bg-panel` for composer, plates, cards, dock, terminal. `bg-sel` for selection and hover, `bg-sel/35` for your message. Three levels, two nested borders at most. Rules: one `border-b border-edge/60` under the strip, none between top bar and strip, none around timeline bands. Radius: `rounded-md` for plates and your message, `rounded-[10px]` for composer and cards.

**Spacing.** Tokens 4, 8, 12, 16, 24, 32 only. Between-group gap is at least 2× within-group at every level.

| Between | Gap |
|---|---|
| Rows in one work run | 0, on a 24px row |
| Work run and prose of the same message | 12 |
| Prose and its receipt | 8 |
| Messages in one turn | 16 |
| Turns | 32, no rule |
| Timeline end and tray or composer | 16 |
| Tray and queue rows or composer | 8 |

**Colour budget. Each hue has one job.** Saturated pixels under about 10% at rest.

| Hue | Only means | Max at rest |
|---|---|---|
| `accent` | Caret, global keyboard focus ring, the one primary button. Never the composer's ring | 1 |
| `orange` | A human can act now | 0 when clear, then one card border, its title, roster glyphs |
| `bad` | A failure, production tier | Only with a real failure or prod in scope |
| `warn` | Staging tier, degraded feed, one risk word on an approval | Cut from 44 uses to about 5 |
| `info` | Working glyph, the live tool row's timer and its current-line tint | Static per row |
| `good` | Passing check in the delivery chip, `ok`/`exit 0` on a plate, dev dot | Rare |
| `magenta` | "An agent wrote this page": `by Claude`, widget tab underline, 1px frame edge while shown | Only with a widget |

Your message is neutral, never accent. Dev rail is neutral. `DockPanel:137-138` loses its tinted strip. Colour never carries state alone: every status has a glyph or word. No filled pill anywhere in the session view.

**Motion.** 100-150ms state, 150-200ms expand, nothing over 250ms, all off under reduced motion. Nothing animates around a terminal. New rows fade once at 150ms, never replayed on refetch. Spinners get show delay and minimum time.

**Grid, not flex.** Every fill is `display:grid` with `minmax(0,1fr)`. No flex child relied on to stretch in a flex column. Fixed bands are grid rows. Button hover is `enabled:hover:`. Recolouring a variant needs the trailing `!`.

**Removed:** about 12 markers, two names for the agent, filter row as a band, Now row and composer state line as two things, 3 of 4 Stops, `SidePanel` as a column, host tabs in the strip, fullscreen accent frame, magenta fill, most `warn`, every "SSH" in a session without one.

---

---

## 3. Decisions

| Question | Decision | Reason |
|---|---|---|
| **Terminal attach** | Attach only while shown. No hidden read-only client: it was measured that a sole read-only client makes every `send-keys` fail and still resizes the agent. No mirror panel. Glance uses capture-pane. | Keeps automation flowing. Removes the riskiest mount path. |
| **Attached check** | A send pressed now uses `lockHumanPrompt`, with no attached check. A queued send, human or machine, keeps `lockAutomationPrompt`, attached check included. | a queued flush can type into a terminal in use, including a phone's attach. Queue row says why it waits. |
| **Ingest or post-only** | Ingest. New reader `internal/agentfeed`, display-only, not persisted, log is the store. `lola post` still works and shows as elements. | Post-only is the human talking to silence: agents forget. |
| **Where agent text lives** | Client-side merge of `agentFeed` and `sshTimeline`. No `SSHItemAgent` in the store. | No store growth, no whole-file rewrites, no agent-readable path to private blocks. |
| **New wire on `SessionInfo`** | None. Viewed-session state comes from `talkState`. | `handleRemote` strips only `SSH`, so a new field reaches phones by default. |
| **Trash** | Removes the connection, its blocks with output, connection-scoped elements and events, and handback items. **Keeps** `say` human items (label dropped) and everything session-wide. Last-tab wipe removed. Copy: "Removes this host, its commands and their output. Your messages stay." | A closed host must not erase a conversation.  |
| **One story or per tab** | One story. Default scope All. A host is a filter plus the Run target. Folder, terminal and command draft stay per connection. | An agent's conversation is one story.  |
| **All and the wheel** | Say with `Conn == ""` hands back nothing and leaves `TalkConn` alone. Composer states "You're driving prod-api" with `Hand back`. With a host selected, it behaves as today for that host. | Otherwise one chat message returns every wheel. |
| **Run under All** | One live host: Run targets it, named on the button. Several: Run is disabled, "Pick a host above". | Choosing a production target must be a human act. |
| **Post scoping** | No `--host` and no evidence means `Conn == ""`. `TalkConn` still routes `ConnForHost`. | Narration must not die with a tab. |
| **Goal sessions** | Open on Terminal. Conversation read-only. No composer, no Changes, no Stop. Link to the goal. `agentSay`, `agentStop`, `agentChoose` refuse. | observer `continue`s, `flushHandoffsOnStop` returns early, `handleDiff` refuses, no checkpoints. A queued Say would never flush, and the goal controller owns delivery. |
| **Temporary and shell-only** | Terminal body. No Conversation, no composer, strip only with several terminals, footer strip always. Observer closes an exited temporary session. | `sshTalkStore` already refuses agentless. Glance mounts no terminal, so exit cleanup cannot hang off `onExit`. |
| **Phones and TUI** | Unchanged and terminal-first. All new commands denied through `SSHHumanCommands`. No shim changes. | Phone parity needs its own read-only command written into a tier by hand. |
| **Compact band** | Kept as a Glance with no xterm. | Keeps a habit (Cursor's lesson) and ends browse-time attach. |
| **Queue semantics** | Next delivery is the head batch: leading non-command entries joined by a blank line, or one command alone. After a batch, the rest waits for the next rest. | Simple. A joined `/compact` would become arguments. |
| **Steer** | No true mid-turn steer. Queue, Stop, and a named `Stop & send` button. the gates forbid typing into a working pane, Claude's own queue cannot be recalled, and Esc starts it. | Measured. Research wants it, so this is a visible cost. |
| **Stop and the wheel** | Split. `Stop ⌘.` is this turn only, no connection needed. `Take the wheel` keeps interrupt-and-take. With a live host the tooltip adds: "Commands already running on prod-api keep running. Stop them on their row." | Stated scope. A single header `Stop` used to do both. |
| **Stop then send** | Stop sets a 30s hold on all automation sends, and holds the human queue until the next send or `Send them`. A `user_prompt` hook (the human typed in the terminal), `sshSay` and `sshAnswer` (the TUI has no `Send them`) release the queue too. | Otherwise a CI reaction or queued text types the moment the pane rests after a person said stop. |
| **Widgets earning "fun"** | First-class card, one click to consent and open, `Widgets N` always in the strip, always-on agent briefing. No frame moves. A post never opens a panel. | The owner's favourite must not be a small grey row, and S3 means no agent knew it existed outside ssh. |
| **Ended sessions** | `dropSession` writes an `Ended` record into the store. Roster has a collapsed `Ended N`. Conversation reads store + transcript, read-only, 14 days. | Merged cleanup otherwise deletes the story at the best moment. |
| **Daemon down** | Desktop retains all sessions on down. Items stay in view memory. Waiting says unknown. | `^r` is a daily event, and "All clear" would be false. |
| **Codex and opencode** | Same view. Reader, composer and Stop are each enabled per agent by `agent.Caps`, only after measurement. Unverified means not offered. `Send` false removes only the press-now path: text still queues and flushes through the automation gate and the `lola ssh` inbox, as it does today. Until then they open on Terminal, which still has the footer. | Spike covers claude only. No real codex pane has been tested. |
| **Permission arm** | 400ms, focus required, read-to-end. Not ssh's 800ms. | A local worktree agent, and fatigue is the larger risk. Judgement, easy to change. |
| **Sticky storage** | localStorage. | The daemon never reads it, same reasoning as `desktop-update.json`. |

---

---

## 4. Data model and wire

Everything is additive with `omitempty`, and there is no migration. Old stores load: new fields read zero. Render an unknown item kind as nothing.

### 4.1 New file `internal/protocol/talk.go`

```go
package protocol

import "time"

// Conversation commands. All are HUMAN side: they go in SSHHumanCommands, which
// refuses them at the hook bridge and at remote. Never add one to SSHAgentCommands.
const (
	CmdAgentFeed         = "agentFeed"
	CmdAgentFeedOutput   = "agentFeedOutput"
	CmdTalkState         = "talkState"
	CmdAgentSay          = "agentSay"
	CmdAgentStop         = "agentStop"
	CmdAgentChoose       = "agentChoose"
	CmdAgentQueueRelease = "agentQueueRelease"
	CmdEndedSessions     = "endedSessions"
)

// Feed health. Display only.
const (
	FeedOK          = "ok"
	FeedEmpty       = "empty"       // no log yet: not a warning
	FeedPartial     = "partial"     // hook counts and feed counts disagree
	FeedUnreadable  = "unreadable"  // the file grows and yields no items
	FeedUnsupported = "unsupported" // no reader for this agent or version
)

// Feed item kinds. Status values: running | ok | error | interrupted | no_result.
const (
	FeedItemUser    = "user"
	FeedItemText    = "text"
	FeedItemTool    = "tool"
	FeedItemDivider = "divider"
	FeedItemNotice  = "notice"
)

// After == 0 and Before == 0 means the newest Limit items, in Order, with More true
// when older ones exist. A client never loops on More: older history is paged by Before.
type AgentFeedArgs struct {
	Session string `json:"session"`
	Epoch   string `json:"epoch,omitempty"`
	After   int64  `json:"after,omitempty"`
	Before  int64  `json:"before,omitempty"` // Order, for older history
	Parent  string `json:"parent,omitempty"` // subagent thread
	Limit   int    `json:"limit,omitempty"`  // 0 means 300
}

type AgentFeedData struct {
	Session string          `json:"session"`
	Epoch   string          `json:"epoch"` // changed: discard and refetch
	LastSeq int64           `json:"lastSeq"`
	More    bool            `json:"more,omitempty"`
	// Scanning: the scanner has not reached EOF, so these are not yet the tail.
	// Health is not judged while it is true. The client says "Reading this conversation".
	Scanning bool           `json:"scanning,omitempty"`
	Health  string          `json:"health"`
	Items   []AgentFeedItem `json:"items"`
}

// AgentFeedItem is everything lola read from the agent's own log. Every field is a
// CLAIM: the agent can write that file. Echo alone is lola's: this user record
// matches a send lola recorded, so the client hides it and shows the store's row.
type AgentFeedItem struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Order     int64          `json:"order"`
	Seq       int64          `json:"seq"`
	At        time.Time      `json:"at,omitzero"`
	Turn      string         `json:"turn,omitempty"`
	Parent    string         `json:"parent,omitempty"`
	Text      string         `json:"text,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	Origin    string         `json:"origin,omitempty"` // terminal | system | task | teammate
	Echo      bool           `json:"echo,omitempty"`
	Tool      *AgentFeedTool `json:"tool,omitempty"`
}

type AgentFeedTool struct {
	Name       string `json:"name"`
	Category   string `json:"category"` // read | edit | run | agent | other
	Summary    string `json:"summary"`
	Detail     string `json:"detail,omitempty"` // the whole line, when Summary shortened or replaced it
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Added      int    `json:"added,omitempty"`
	Removed    int    `json:"removed,omitempty"`
	Preview    string `json:"preview,omitempty"` // 240 chars, sanitized
	HasOutput  bool   `json:"hasOutput,omitempty"`
	Subagent   string `json:"subagent,omitempty"`
}

type AgentFeedOutputArgs struct {
	Session string `json:"session"`
	Item    string `json:"item"`
}
type AgentFeedOutputData struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// TalkCaps says what lola has measured for this session's agent. Unverified means
// not offered. Why is lola's own sentence when Send is false.
type TalkCaps struct {
	Feed  bool   `json:"feed"`
	Send  bool   `json:"send"`
	Stop  bool   `json:"stop"`
	Cards bool   `json:"cards"`
	Why   string `json:"why,omitempty"`
}

// Composer states, as a fresh pane read says.
const (
	ComposerResting = "resting"
	ComposerWorking = "working"
	ComposerDraft   = "draft"
	ComposerPrompt  = "prompt"
	ComposerModal   = "modal"
	ComposerGone    = "gone"
	ComposerUnknown = "unknown"
)

// AgentPrompt is a read of the pane. Untrusted text, memory only: never written to
// the session snapshot, the store or a log. Hash pins exactly what a human read.
type AgentPrompt struct {
	Kind    string        `json:"kind"` // permission | question | question_multi | modal
	Hash    string        `json:"hash"`
	Lines   []string      `json:"lines"`
	Choices []AgentChoice `json:"choices,omitempty"`
	Since   time.Time     `json:"since,omitzero"`
}
type AgentChoice struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"` // a question option's description, under its name in the pane
}

type TalkStateArgs struct {
	Session string `json:"session"`
}
type TalkStateData struct {
	Session   string       `json:"session"`
	Caps      TalkCaps     `json:"caps"`
	Composer  string       `json:"composer"`
	Prompt    *AgentPrompt `json:"prompt,omitempty"`
	Deferred  string       `json:"deferred,omitempty"` // AutomationDeferredReason
	QueueHeld bool         `json:"queueHeld,omitempty"`
	FeedSeq   int64        `json:"feedSeq"`
	Health    string       `json:"health"`
}

type AgentSayArgs struct {
	Session   string `json:"session"`
	Text      string `json:"text"`
	Conn      string `json:"conn,omitempty"`      // the host in scope, "" under All
	StopFirst bool   `json:"stopFirst,omitempty"` // Stop & send
}
type AgentSayData struct {
	Item      string `json:"item"`
	Delivered bool   `json:"delivered"`
	Why       string `json:"why,omitempty"` // lola's sentence when queued
	AgentGone bool   `json:"agentGone,omitempty"`
}

type AgentStopArgs struct {
	Session string `json:"session"`
}
type AgentStopData struct {
	Stopped bool   `json:"stopped"`
	Why     string `json:"why,omitempty"` // "Already stopped"
	Held    int    `json:"held,omitempty"`
}

type AgentChooseArgs struct {
	Session string `json:"session"`
	Hash    string `json:"hash"`
	Key     string `json:"key"`
}
type AgentChooseData struct {
	Sent   bool         `json:"sent"`
	Why    string       `json:"why,omitempty"`
	Prompt *AgentPrompt `json:"prompt,omitempty"` // the pane's next prompt, if any
}

type AgentQueueReleaseArgs struct {
	Session string `json:"session"`
}

// "Send them" is a press: it clears QueueHeld and the hold, then attempts the head
// batch through lockHumanPrompt. Answers with AgentSayData.

type EndedSession struct {
	ID       string    `json:"id"`
	Project  string    `json:"project,omitempty"`
	Title    string    `json:"title,omitempty"`
	Issue    string    `json:"issue,omitempty"`
	Agent    string    `json:"agent,omitempty"`
	EndedAt  time.Time `json:"endedAt,omitzero"`
	Reason   string    `json:"reason,omitempty"` // lola's words: "PR #412 merged"
	PRNumber int       `json:"prNumber,omitempty"`
	PRURL    string    `json:"prUrl,omitempty"`
}
type EndedSessionsData struct {
	Sessions []EndedSession `json:"sessions"`
}
```

### 4.2 Edits to `internal/protocol/ssh.go`

```go
// Append to SSHHumanCommands:
//   CmdAgentFeed, CmdAgentFeedOutput, CmdTalkState, CmdAgentSay, CmdAgentStop,
//   CmdAgentChoose, CmdAgentQueueRelease, CmdEndedSessions
// SSHAgentCommands is NOT touched. hookBridgeCmds is NOT touched.

// Human origins, additive:
SSHHumanInitial = "initial" // the brief the session started with
SSHHumanChoice  = "choice"  // a key a human pressed on a card: "You chose: 1 Yes"

// System events, additive. All Text is lola's own sentence, Conn "":
SSHEventSessionStarted = "session_started"
SSHEventSessionEnded   = "session_ended"
SSHEventAgentExited    = "agent_exited"
SSHEventStopped        = "stopped"
SSHEventCheckpoint     = "checkpoint" // a turn's receipt
SSHEventPR             = "pr"         // opened, merged, closed
SSHEventCI             = "ci"         // a required check turned red or green
SSHEventReview         = "review"     // a tally only, never findings
SSHEventHandoff        = "handoff"    // lola typed something. Says which and why.

// SSHSystemEvent gains:
Seq        int   `json:"seq,omitempty"`        // checkpoint
Added      int   `json:"added,omitempty"`      // checkpoint
Removed    int   `json:"removed,omitempty"`    // checkpoint
Files      int   `json:"files,omitempty"`      // checkpoint
DurationMs int64 `json:"durationMs,omitempty"` // turn time, Stop to Stop

// SSHHumanMessage gains:
Returned bool `json:"returned,omitempty"` // Esc put it back in the agent's prompt as a draft
```

Add every new kind to `SSH_EVENT_KINDS` in `FE/ssh.ts` and to the list in `desktop/ssh_parity_test.go`, which compares both ways, and regenerate bindings for the new fields. The TUI's pager prints system events, so P3 filters it in `internal/tui/sshmodel.go` to the kinds it had, or every turn prints a receipt there. `handleSSHAgentPoll` returns a block and the inbox only, so none of this reaches an agent.

### 4.3 Store and session

```go
// internal/sshstore
type QueueEntry struct { // existing, plus:
	Human   bool `json:"human,omitempty"`   // an agentSay: Text is unframed
	Command bool `json:"command,omitempty"` // a slash command: always delivered alone
}
// State gains:
Sent  []SentBatch `json:"sent,omitempty"`  // cap 256, oldest first
Ended *EndedInfo  `json:"ended,omitempty"` // cleared by session_started

type SentBatch struct {
	Hash  string    `json:"hash"` // sha256 of agentfeed.Normalize(typed text)
	Items []string  `json:"items,omitempty"`
	At    time.Time `json:"at"`
}
type EndedInfo struct {
	protocol.EndedSession
	Segments []string `json:"segments,omitempty"`
	Slug     string   `json:"slug,omitempty"` // the transcript directory's name
}
const MaxHuman = 2000 // Prune: oldest Delivered or Recalled first, never one with a queue entry

// (st *State) NextBatch() (text string, ids []int): the head batch. See 3.
// PendingSSH becomes exactly NextBatch's text, so deliverQueued types one batch.
// sshInbox wraps q.Human text as "[lola] The human says:\n<text>" at hand-out.

// internal/session.Session gains:
TranscriptSegments []string  `json:"transcript_segments,omitempty"` // base names, cap 32
StopHoldUntil      time.Time `json:"stop_hold_until,omitzero"`
QueueHeld          bool      `json:"queue_held,omitempty"`
// Add all three to the adoption merge beside TranscriptPath (daemon.go:1357).
```

**Queue rules the old join hid.**

1. **Claim by position, never by substring.** `deliverSSHQueue`'s `took` uses `strings.Contains(text, q.Text)`, safe only because every entry carried a frame. Unframed, `yes` is inside `yes please`, so an entry never typed is marked delivered and lost. Walk the head entries until their join equals the typed text, and claim those.
2. `sshInbox` skips `Command` entries, because `/compact` in an agent's tool output means nothing, and hands out nothing while `QueueHeld`. `Deliver` returns entries rather than strings so it can wrap `Human` ones.
3. `sshSyncPending` writes `PendingSSH = ""` while `QueueHeld`. That one place holds the prompt path, so `deliverQueued` and `flushReviewHandoffs`' slot need no check. An empty queue clears `QueueHeld`.
4. `initial` and `choice` items are created with `Delivered` set, or both clients offer to recall them (`internal/tui/sshkeys.go:437`).

Trash and post-scope changes follow section 3. `handleSSHTrash` says the same.

### 4.4 Daemon behaviour, exactly

**Gates (`internal/daemon/talkgate.go`, `autoprompt.go`).**

```go
// lockHumanPrompt: sendGate.Lock → re-Get → humanSendable → capture VISIBLE pane +
// cursor → Classify == ActivityWaiting → attention.ComposerEmptyAt. No promptAttached,
// no automationDeferred. Returns unlock, or a reason: working|draft|modal|prompt|gone.
func (d *Daemon) lockHumanPrompt(ctx context.Context, s session.Session) (unlock func(), why string)

func humanSendable(s session.Session) bool {
	return handoffDeliverable(s) || s.AgentState == state.AgentStarting
}
```

`handoffDeliverable` itself does not widen, and `InputPermission` stays excluded. `lockAutomationPrompt` gains exactly one check: `time.Now().Before(cur.StopHoldUntil)` returns false. That one line covers feedback, broadcast, ssh, reactions and review hand-offs. `automationPromptReady` switches to `ComposerEmptyAt`, which fixes "a user with a status line defers forever". When `paneCursor` or `paneVisible` is nil it falls back to `EmptyComposer` on `paneTail`: 18 daemon test files inject only `paneTail`. Production is wired and pinned by `TestDaemonWiresPaneSeams`. `resolveConflict` moves to `lockHumanPrompt` because it is a human click, but **`typeAtRestingPrompt` also serves the merge queue** (`mergequeue.go:228`), which must keep `lockAutomationPrompt`, its attached check and the stop hold. So the helper takes the lock and the `typedNote` as arguments and each caller names its own. In `handleAnswer`, the `AgentIdle + AtPrompt` case additionally requires `ComposerEmptyAt`; the waiting cases keep today's proof.

**Classifier (`internal/attention`).**

- BUG A: `clean := strings.TrimRight(stripANSI(paneText), " \t\n")` in `Classify`. Validated against saved captures of real panes.
- `type Cursor struct{ X, Y int; Visible bool }`, defined in `attention` so it stays free of `tmux`. `ComposerEmptyAt(visible string, c Cursor, k agent.Kind) bool`. Claude: flag 1, x 2, row at `y` is a bare caret or dim-only (`suggestionOnly`), rows above and below are rules. Other agents: `EmptyComposer` unchanged. Vim NORMAL is undetectable: document it.
- `ReadPrompt(visible string, c Cursor, k agent.Kind) (Prompt, bool)`, with `Prompt` a local struct that the daemon maps onto `protocol.AgentPrompt`. Fix `Parse` for the real prompt: drop a trailing ` Esc to cancel · Tab to amend`-style footer before the block scan. Kind `modal` when `ActivityBlocked`, or `waiting + cursor hidden + not empty + no parsed options`. `question_multi` when a `Submit` row exists. Hash is sha256 over the exact visible lines of the prompt block.

**Transport (`internal/tmux/client.go`).**

- `SendKeys`: at or under 800 bytes unchanged. Over 800: `load-buffer -b lola-send-<rand> -` on stdin, `paste-buffer -p -d -b … -t pane`, wait 300ms, `Enter`. Leave copy mode first. Delete the buffer on failure, as `PasteSecret` does. Never `paste-buffer` without `-p`: LF becomes CR.
- New: `SendKey(ctx, name, key)`: one `send-keys -l` of one character, no Enter, leaving copy mode first as `SendEscape` does. A digit sent to a scrolled-back pane is a copy-mode command. `PaneCursor(ctx, name) (Cursor, error)`: `#{cursor_x} #{cursor_y} #{cursor_flag}`. `CaptureVisible(ctx, name)`: no `-S`, read before the cursor.

**`sanitizeAgentText`:** a leading `!` gets a leading space, so no machine or human send can run a shell. Normalize for humans in one helper `humanText`: `sshelement.Para` with the 8 KiB cap, tabs to 4 spaces, strip one trailing `\`.

**`agentSay`.**

1. Refuse: unknown, agentless, `GoalManaged` ("A goal runs this session"). `sshTalkStore` creates the store. `!Caps.Send` does not refuse: a codex session with a host can talk today, through the inbox and the automation gate, and a refusal would make the footer's `Queue ↩` a dead button.
2. `Conn != ""`: hand back that tab if driven, set `TalkConn`. `Conn == ""`: neither. The same rule goes into `handleSSHSay`, whose `tab == nil` branch is the bug.
3. `humanText`. `Command = isSlash(text)`. With a host in scope, more than one live connection and not a command, prefix `[lola] Said while looking at prod-api:\n`. Otherwise **no frame**. `lola ssh` inbox hand-out wraps it instead.
4. `AddHuman` + `Enqueue(Human:true, Command:…)`. Nothing is cleared yet, because step 5 would set it again.
5. `StopFirst`, only with `Caps.Stop`: under the gate, `stopLocked` (the Esc and its proof, no hold, no `QueueHeld`), wait at least 50ms, poll the pane up to 1.5s for resting and empty, then **release the gate**, because `sendGate` is not reentrant and step 6 takes it.
6. Clear `QueueHeld` and sync `PendingSSH`: this is the human speaking again. Attempt once: with `Caps.Send`, `deliverQueued` with `lock: d.humanLock, admit: humanSendable`; without it, plain `deliverSSHQueue`. Add those two optional fields to `queuedText`; default is today's behaviour. Clear `StopHoldUntil` after the attempt, so nothing automated can type between the clear and the lock. Otherwise it stays queued for the ordinary flush, attached check included. Reply says why, in lola's words.

**`agentStop`.** Under `sendGate`: `Caps.Stop`, not goal, `Classify == ActivityWorking` on a fresh capture, else `{Stopped:false, Why:"Already stopped"}`. Debounce 600ms per session: two Escs on an empty composer open Rewind. `SendEscape`. Set `StopHoldUntil = now+30s`, `QueueHeld = queue non-empty`. Record `lastEsc[id]`: `typeToAgentLocked` waits until 50ms after it, or the first character is eaten. Release the gate. Then asynchronously after 400ms, on the drain group: capture, `agentReconcile` with `agentlog.Unknown` as its verdict (Esc fires no Stop hook, or the pill says working for 30s), `recordCheckpointAsync(id,"stop")`, emit `stopped`, and if the pane is `waiting + not empty` and a human item was delivered in the last 120s, set `Returned` on it (measured: Esc before first output returns the message to the composer while the log says it was sent).

**`agentChoose`.** Under `sendGate`: `Caps.Cards`, not goal. Capture visible and cursor, `ReadPrompt`. Refuse unless `Hash` matches and `Key` is one of `Choices`. `SendKey`, no Enter. A stale key would become a draft, which is why the read sits immediately before the press. Release. After 300ms re-read and set the axis from the pane, or the pill lies for 30s. Add human item `You chose: <key> <label>`, label clipped and sanitized, `Delivered` set (see 2.6). Return the next prompt. Never `d.sendKeys`.

**`talkState`.** Called once a second by a mounted session view while the window is focused. Advance the feed scanner (budget 2 MB). `PaneCursor`, claude only. If `cursor_flag == 0`, or the record says `waiting_input`: capture and `ReadPrompt`. Keep the result in a memory map; never persist. If it reads permission or question and the record does not yet, set `AgentWaitingInput` + reason: this is pane evidence, as `agentReconcile` already uses. If it reads `ActivityWorking` while the record says waiting, demote it: a prompt answered in the terminal fires no hook. Route both through `agentReconcile`, not a second set of rules. Modal raises no Attention. When idle or unknown, one capture for `Composer`. Bounded execs, `reactExecTimeout`.

**Hooks.** Add `PermissionRequest` to `settingsFile.Hooks` as `lola hook permission_request`, `async: true`, 10s. `handleHookEvent` case: no direct axis write, because it is async and can land after the answer. Start a bounded async pane read through the same path as `talkState`. Bounded 2s, exit 0, as every hook. `handleHookEvent` also bumps `hookCounts` (`user_prompt`, `tool_use`, `stop`), appends the segment from `AgentSessionID` to `TranscriptSegments`, and on `user_prompt` clears `QueueHeld`. No `PreToolUse`: a running row from the log is at most about 1s late, and each hook spawns a process. Old sessions keep `Notification` (up to 12s late) until revived, and `talkState` catches a viewed one sooner.

**Every typed path leaves a row.** Change the signature so the compiler enforces it:

```go
type typedNote struct {
	Session string // base id
	Label   string // lola's sentence for a handoff row; "" for a human's own send
	Items   []string
}
func (d *Daemon) typeToAgentLocked(ctx context.Context, n typedNote, target, text string, timeout time.Duration) error
```

On success, `noteTyped`: append a `SentBatch` (hash of `agentfeed.Normalize(text)`) and, when `Label != ""`, a `handoff` row through the timeline worker. `typeToAgent` takes the note too, since `answer.go:179` calls it. Sites: `answer.go:179`, `feedback.go:247` (label from a new `queuedText.label`), `reactions.go:794`, `reviewer.go:1016`, `resolveconflict.go:166` (label from its caller). `noteTyped` lives in `talknote.go`. Labels: `lola told Claude to fix failing CI`, `lola sent CodeRabbit's findings to Claude`, `Your 4 diff comments went to Claude`, `Your broadcast went to Claude`, `You asked Claude to resolve conflicts`, `lola asked Claude to catch up for the merge queue`.

**Timeline worker (`internal/daemon/timeline.go`).** A buffered channel and one panic-guarded goroutine on the shutdown-shielded context. `emitSystem` never blocks and never fails a caller, the opposite of a block's rule. It applies `session.BaseID`, and refuses a session unknown to the store unless the caller is the ended path, so a gone store's retention clock is not reset. `OnTransition` runs inside the store callback: enqueue only, never write there (lock order, `ssh.go:21`). Emitters: `session_started` on fresh spawn (not revive or switch), `agent_exited`, `pr` and `ci` from `Delivery` changes off existing guards, `review` tally at `noteReviewOutcome`. Never from `notification` hooks. Never derive wording from the rollup `status`.

**`s.ssh` no longer means "has a host".** A store with any item makes `sshGlance` non-nil, so `session_started` gives every agent session an `s.ssh` with no connections. Test `connections.length` wherever a host is meant. Checked and already safe: `chipFor`, `sshTopConn`, `sshcockpit.go:164`, `sshmodel.go:215`. `SessionsTable:168` will now show `SSHActivity` for a plain session that posted narration, which is correct.

**The first row.** `handleOpenManual` (`open.go:88`) and `handleOpenDirectory` record `a.Prompt` as `AddHuman{Origin: initial, Delivered: true}`. **`handleOpenDirectory` drops the prompt today:** `Native.OpenDirectory` (`runtime/local.go:34`) takes none, and the handler refuses a branch for `main`, so `NewSession` sends a derived branch only for a new worktree. It takes the parameter, shell-quoted into `launchLine`'s first-turn prompt, plus a `SentBatch` for it. In a worktree the first user record is lola's own `You are lola session <id>. Read .lola/prompt.md…` (`native.go:888`), which would render as `In terminal` at the top of every story. `runtime.IsLaunchPrompt(id, text)` exists for this, and `talkfeed.go` marks such a record `Echo`, handoff variant included. A forged one can only hide itself.

**Ended.** `dropSession` writes `State.Ended` before `sessions.Delete`, so a client that sees the row go can read it, and skips agentless sessions. `endedSessions` walks `sshstore.Root`, because `d.ssh.stores` holds only stores opened since start, and caches 30s.

**Receipts.** In `recordCheckpoint` on `stop`: one local `git diff --shortstat` between this checkpoint and the previous, through `gitguard.Apply`, summed per app for stacks. Emit `checkpoint`. `Record` skips an identical tree, and `KindLocal` sessions have no checkpoints: emit a numberless receipt with only duration, from Stop-to-Stop timing.

**Briefing.** Split `withSSHBriefing`: an always-on section "Talking and widgets" for every agent launch, 5 lines at most (`lola post narration`, `lola post widget`, that the human reads your text anyway so do not repeat it there, that widgets are how you show a live thing), and the ssh section only with hosts. New verb alias `lola post`, same `CmdSSHAgentPost`. `lola ssh post` stays valid forever, because long-lived panes have prompts naming it. Bridge allowlist unchanged.

**Temporary cleanup.** In the observer's agentless branch: a `location == temporary` session whose pane is gone and that has no shell tabs goes through `handleCloseExitedTemporary`'s logic.

### 4.5 `internal/agentfeed` (stdlib leaf + `protocol`)

```go
type Health = string
type Source interface {
	Advance(ctx context.Context, budget int64) (changed []protocol.AgentFeedItem, h Health)
}
func NewClaude(root, slug string, segments func() []string) Source
func NewCodex(root, cwd string) Source
func NewOpencode(cwd string) Source
func Normalize(text string) string // strip <pasted_content…> wrappers, leading space, tabs→4 spaces, one trailing `\`, trim
```

Scanner shape: reuse `claimaudit.Scanner` (`scanner.go:82-138`): `os.SameFile` identity, shrink restarts, trailing partial line left, per-pass budget, oversized line skipped. Pipeline: prefix-skip `{"type":"api-request`, `attachment`, `file-history`; decode a minimal record; text blocks sanitized and capped at 16 KB with `Truncated`; `tool_use` keeps name, category, a one-line summary, never `Write.content`; `tool_result` pairs by `tool_use_id`, keeps `is_error`, a 240-character preview and `{file, offset, length}` instead of the body. Skip `isMeta`, unwrap command tags, drop Bash starting `lola report` or `lola ssh`. Shapes to parse (measured): `queue-operation`, `system/local_command`, `<bash-input>`/`<bash-stdout>`, `[Request interrupted by user…]`, `<pasted_content id="…">`, `<task-notification>`, `compact_boundary`. Dedupe by record `uuid`. Stable ids `<uuid>:<blockIndex>`. New segment means a divider item.

**Path rule, for every agent.** Never read a path a hook handed over. Segment key is a base name matching `^[0-9a-f-]{36}\.jsonl$`, from `HookPayload.AgentSessionID`. A live session with no segments (it predates the field, and at rest it fires no hook) seeds from the base name of `Session.TranscriptPath`, same regex, or its first open shows nothing until its next turn. Directory is `usage.SlugDir(root, sessionWorktree(home, s))`. Direct child, `Lstat` regular, opened with no-follow. That also handles a sandboxed agent's container path (`runtime/sandbox.go:309-321`). Same check for subagent files. Codex: under the sessions root, with `session_meta.cwd` matching and `parent_thread_id` empty. Opencode: `sqlite3 -readonly`, query on stdin, `directory` under `~/.lola/worktrees`.

**Pull-driven, no goroutine, no tailer.** `talkState` and `agentFeed` advance it. `talkState`'s 2 MB budget would take a minute to reach the tail of a 123 MB file, so `agentFeed` advances by wall clock (1s per call, per-session mutex) and answers `Scanning` until EOF. LRU of 8 sessions, last 5,000 items in memory per session, older by re-scan. Rescan on restart gives a new `epoch`; clients reset and keep scroll and expansion by stable id.

**Daemon composition (`internal/daemon/talkfeed.go`).** After `Advance`, mark `Echo` where a user item's `Normalize` hash equals an unmatched `SentBatch` within ±5 minutes. Unmatched user items get `Origin` `terminal`, `task` or `system`. Resolve a gone session through `State.Ended`.

**Health.** Keep hook counters per session in memory since scanner start: `user_prompt`, `tool_use`, `stop`. Feed `partial`: stops ≥ 3 with no text items, or `tool_use` ≥ 20 with tool results under 80% of it, or `user_prompt` ≥ 3 with no user items. `unreadable`: file grew past 256 KB since scanner start and yielded no items. `empty`: no file. `unsupported`: `!Caps.Feed`. Agent liveness never reads any of this. `lola doctor` reports parse rate on the newest transcript.

**Guard test.** A grep-style test fails if anything under `internal/daemon` other than `talkfeed.go` and `talknote.go` (the `noteTyped` hash helper) imports `agentfeed`, and if `reactions`, `observer`, `reviewer`, `dispatch` or `reconcile` reference `AgentFeed`.

### 4.6 `internal/agent.Caps`

```go
func Caps(k Kind) struct{ Feed, Send, Stop, Cards, Cursor bool }
```

Claude: all true, from measurement. Codex and opencode start all false. `Send`, `Stop`, `Cards` and `Cursor` live in `caps.go` and `Feed` in `caps_feed.go`, and each goes on only with a real capture checked in. `desktop/talk_parity_test.go` pins it against `FEED_AGENTS` in `FE/sessionview.ts`.

### 4.7 Wails

`desktop/talksvc.go`, `TalkService`, one socket round-trip per method, no state, same shape as `SSHService`. Register in `main.go`, regenerate the checked-in `desktop/frontend/bindings`. Do not add it to `wailsshim`. Only `FE/talk.svelte.ts` and `FE/feed.svelte.ts` import it, and `store.svelte.ts` never does.

**Skew and migration.**

- A daemon older than the app answers `unknown cmd`. Clients read that as `Health: unsupported` with every cap false: the session opens on Terminal and shows the notice. Never a blank column. A session with a connection keeps Conversation, and its composer falls back to `ssh.say` on `unknown cmd`, since a stale daemon still has `sshSay`.
- Live sessions need nothing: the first open scans the existing transcript. Their `--settings` lack `PermissionRequest` until revived. Do not rewrite a live session's settings.
- Old stores need nothing. Old items with `Conn` set stay scoped and interleave by time under All.
- `lola ssh post` stays valid forever, and the existing frame on `sshSay` stays for the TUI.
- Upgrade the daemon first. Restart it after `go install`, per `CLAUDE.md`.

---

## 5. How it stays inside the constraints

| Constraint | How |
|---|---|
| Agent stays a TUI in tmux | No change to launch or adoption. Timeline can fail alone. |
| Transcript is an untrusted claim | Section 4.5, guard test, invariant 1. |
| `feedHealth` visible | 2.3 L, partial reconciled against hooks. |
| Terminal never demoted | Always-visible tab, `⌘\`, sticky, `defaultView`, a button on every card. |
| Sends keep gates | `lockHumanPrompt` and `lockAutomationPrompt` both prove. `agentChoose` and `agentStop` hold `sendGate` and re-read. Nothing calls `d.sendKeys` directly. |
| Bridge allowlist | Untouched. All new commands in `SSHHumanCommands`. Tests iterate it. |
| Facts versus claims | Plate versus ground, section 2.5. |
| Widgets, prompts, ssh | Frame layer, `allow-scripts`, `data:` document, port channel, ping, prompt path and `-O check` not touched. Additions only: filled state, leading space. |
| Phone | No wire field, no shim change, the mobile build stays green. |

---

---

## 7. Visual review rubric

Each is answerable by looking at a screenshot. **B** blocks a change. Blur and grayscale checks: `sips` or a Playwright CSS filter on the page.

| # | Sev | Check | Scenes |
|---|---|---|---|
| 1 | B | Blur at 8px: exactly one region reads first, and it is the focal point named in 2.6 | `rest`, `working-queue`, `permission`, `ssh-run-mode`, `terminal` |
| 2 | B | Grayscale: order of importance holds, and every state glyph and status stays distinct | same, plus `overview-glance` |
| 3 | B | At most one filled control per region, two in the window | all |
| 4 | B | Permission card: the only orange in the main column; whole command visible; labels in pane order with one primary at most | `permission` |
| 5 | B | A plate looks unlike a tool row at a glance, and no tool row has a box, a tick, green, or the word "exit" | `ssh-prod-multi` |
| 6 | B | Run mode reads from the whole field without reading a word, in grayscale too | `ssh-run-mode` |
| 7 | B | The footer says what ↩ does, in words, in every state. Stop is at the same x whether or not the field has text | `working-queue`, `rest`, `permission` |
| 8 | B | A session with something waiting is identifiable within 2 seconds, with the sidebar open and with it hidden | `permission`, `narrow` |
| 9 | B | Between-group gap at least twice within-group. Measure: run row to row, prose to run, message to message, turn to turn | `rest`, `long-turn` |
| 10 | B | A collapsed row is one line and names items. No "Read 3 files" with no names, anywhere | `long-turn` |
| 11 | B | A failure in a long turn is visible with nothing expanded | `long-turn` |
| 12 | B | Prose dominates a real turn: squinting at `long-turn`, the eye lands on prose, and tool rows take less than half the height on screen | `long-turn` |
| 13 | B | Terminal is named and reachable on screen in every state where a session has one | all session scenes |
| 14 | M | Chrome is quieter than content: top bar, strip and sidebar never out-contrast the timeline | `rest`, `rest-latte` |
| 15 | M | At most 3 type sizes and 3 ink tiers at rest. Mono only on machine facts | `rest` |
| 16 | M | Saturated colour under about 10% at rest, and each hue on screen means its one thing. No orange when nothing waits | `rest`, `dock-open` |
| 17 | M | One continuous left edge through messages, tool rows, plates, tray and composer. Offsets are only the 16px indent | `rest`, `ssh-prod-multi` |
| 18 | M | Actions sit beside what they affect: Stop by the state, host actions on the host bar, PR actions in the PR chip | `working-queue`, `ssh-prod-multi` |
| 19 | M | Only plates and cards have borders. No third boxed kind, and queue rows do not resemble the composer | `working-queue`, `rest` |
| 20 | M | Widget card is noticeable and inviting, and the dock's magenta is a label and a hairline, never the most saturated thing on screen | `widget-card`, `dock-open` |
| 21 | M | Chrome height: 76px with no host, at most 134px with host and story line. Timeline keeps at least half the column with a card open at 900px tall | `rest`, `ssh-prod-multi`, `permission` |
| 22 | M | Narrow: conversation is never squeezed by a panel, title truncates, chips keep a word | `narrow`, `dock-open` at 800 wide |
| 23 | M | Empty and degraded states say what to do, with the action inside. Daemon-down never reads as all clear | `no-log`, `feed-unreadable`, `daemon-down`, `ended` |
| 24 | M | Latte holds: contrast, rails and faint text all legible, nothing invisible | every `-latte` |
| 25 | M | Glyph, label and shortcut baselines align across a footer and across roster rows, within 1px | `working-queue`, `overview-glance` |

---

---

## 8. Cuts and risks

### 8.1 Cut, with the reason

| Cut | Why |
|---|---|
| Panel rail, four layout presets, Custom, `MAX_GROUPS` | Most modes for the least return. Same cognitive-load complaint as Cursor 3 |
| Terminal mirror, terminals in the frame layer, side-by-side attached terminal | Riskiest mount path. Size persists after attach. Laptops have room for two panes |
| Hidden or read-only attach | Measured broken |
| Headless stream-json, token streaming, `MessageDisplay` | Lead's constraint. The log is block-level; no fake typing |
| True mid-turn steer, `⌥↩`/`⇧⌘↩` Stop & send chord | Gates forbid the first. ⌥↩ is a newline by muscle memory, so it would kill a tool mid-sentence. Button only until real use asks |
| `PreToolUse` hook | Spawn per tool. A running row is at most 1s later from the log |
| Peek (Quick Look and Changes overlay) | A second surface for Changes. Story line overlay covers Report. First candidate after real use |
| `@` file picker, attachments and image paste | Table stakes, not what makes this work. Need gitguard listings and no-follow writes on an agent-writable path. **First follow-up.** Typed `@path` still submits; image paste still works in Terminal |
| Multi-select and free-text answers on cards | Key dance only partly measured. Terminal is honest |
| Story and Facts timeline modes, `F` on plain sessions | Run collapsing does the job. Revisit if `long-turn` review disagrees |
| ⌘1-9, ⌘[ history, hold ⌘ slot numbers, ⌘0, Start and stay, Dock badge | Spend chords on the loop. ⌘0 collides. Renumbering breaks spatial memory |
| "Ready" tier in ⌘J, per-file viewed marks | Floods the loop. Blob-hash marks are their own feature |
| `lib/ssh.ts` split, dock tab namespaces, `lola.ssh.dock` rename, component renames beyond rewritten files, wire renames | Cosmetic. Risk through parity tests, trust lists and three clients. Built-in Overview and Changes tabs use reserved `@lola/` ids in the same dock tree; agent widgets keep their own ids |
| Local Run blocks | A block is a promise from an exec channel. ⌘T gives a real shell |
| `!` as a feature, LLM catch-up, merge button, conversation rewind, phone or TUI parity, onboarding tour, density settings | Unsafe, or noise, or a separate project |
| `Generation` stamps on blocks | A divider handles the visible problem. See risk 9 |

### 8.2 Known risks

1. **Real transcripts may disagree with the fixtures.** Rubric plus `long-turn` is the check, and prose dominance (check 12) is the assumption most likely to fail. Fix by tuning the collapse threshold, not by adding modes.
2. **Log format is internal to Claude Code** and can change on any release. Allowlist parsing, goldens, visible `partial`/`unreadable`, doctor check. Failure costs the story only; composer, Stop and cards do not depend on the feed.
3. **The measurements are one machine, claude only, 1-3 trials, Claude Code 2.1.318.** Cards and cursor proof are a rendering mirror. They fail closed to Terminal, not to typing. `Caps` keeps codex and opencode off until measured.
4. **Modal detection is partial.** A screen the detector misses leaves a message queued and the footer unclear, never typed-into, because the empty proof still holds. Vim NORMAL cannot be detected, and a send there runs as vim commands: documented.
5. **Residual race in `lockHumanPrompt`:** keystrokes in an attached terminal between proof and send can merge. tmux offers nothing atomic. Limited to a press-now send, while the composer and terminal share one window. A phone typing at that instant remains possible.
6. **`SessionsColumn` remount shape** is reasoned from the freeze notes, and only a packaged `.app` can prove it. If the template freezes, fall back to one instance whose `sessionId` prop changes, which is the existing pattern.
7. **`isolate` around the parked host shell:** `SSHTerminalPane:3-5` and `DockArea:379,382` disagree. It ships today and this plan does not change it. The agent terminal is outside it by design.
8. **1s `talkState` poll per open session** costs up to 3 tmux execs a second for one window. Paused when the window is unfocused.
9. **Respawned session id:** `sshAgentPoll` can serve an old life's blocks to the new agent. Rare, display-level today. Not fixed here. Its queue and its transcript are handled: see "Reuse of a session id" below.
10. **Widget navigation hole** stays as `CLAUDE.md` records. More sessions now host widgets because every agent is briefed. The consent click and frame rules hold; the UI still never claims "no network".
11. **Always-on briefing costs context** on every launch. Kept to 5 lines. Drop further if agents post noise.
12. **Unsandboxed agents can forge anything in their log and reach the socket.** Honest limit: the design restricts them to the claim voice and does not prevent it.
13. **Mobile `check` is already red** at 13 errors, so drift shows only as a count. Do not "fix" it as a side effect of something else.
14. **A Terminal view left on screen while the human is away keeps queued text and automation waiting.** The attached check cannot tell. The footer and queue row say why, and it is no worse than today, where every selected row held an attached client.

### 8.3 Owner smoke list, packaged `.app`, about 10 minutes

A person does this on the packaged app: no agent may use synthetic OS input, and Playwright WebKit is not WKWebView.

1. Enter and Esc on a row 20 times. The sidebar keeps updating (no template freeze).
2. Open a Claude session mid-turn: story backfills, running row shows, typing has no lag.
3. Send at rest. Send while working: it queues, recall works. `⌘.` stops it, nothing fires for 30s, queued text holds.
4. Real permission prompt (a session in manual mode): card appears, digit answers, card clears. `No` clears too.
5. Type `/config` in the composer: it opens Terminal and types nothing.
6. `⌘\` both ways with a widget open: the widget never shows over the terminal, and comes back as you left it, with no reload and no second consent. A page may pause while covered. Terminal does not reflow when a chip arrives.
7. `^r` the daemon while reading: story stays, footer says down, draft survives, no "All clear".
8. A PR merges: the session moves to Ended and its story stays readable.
9. A widget posted in a plain session: card appears, `Open` opens it, nothing else moved.
10. Host connected to something harmless: Run names the host, All with two hosts refuses, a message under All does not hand back the wheel.

**Three things worth saying plainly:** there is no true mid-turn steer; `Stop` no longer takes the wheel; removing a host keeps your messages but deletes its commands and output.

---

---

## As built

Where the app differs from the design above, on purpose.

- **Revive is only in the footer.** The top bar's contextual action is `Resolve conflicts` > `Compare attempts`. The footer owns Revive in every view and it is the focal point of that state, so a second one in the bar would compete with it.
- **Usage limit is a footer row, not a tray card.** It has one action, and a card plus a footer would say it twice.
- **`Run on X` stays offered while you drive a host.** Someone driving is the likeliest person to run something.
- **A double ⌘ carries the text across** when the other destination's draft is empty. Otherwise text typed in the wrong mode would vanish.
- **A widget fill starting with `/` is refused**, because nobody measured whether a leading space defuses one.
- **In a run of more than 8 rows, a failure folds into its category's line,** named first with `N failed` in `text-bad`. One row per failure made a long turn about 70% tool rows. The run still in progress keeps its last failure's row and preview. Running, interrupted and no-result rows are never folded.
- **A question on a narrow card keeps all its answers.** Only a permission card folds its qualified options under `More`, because a question has no plain Yes or No to keep in view.
- **A permission card's title says `wants permission`** unless the first line names a command. "Wants to run" on an Edit dialog would be false.
- **`Type something` on a question opens the terminal** and presses nothing.
- **`Widgets N` hides at 0.**
- **Take the wheel is disabled under All with several live hosts,** with a "Pick a host first" tooltip, because choosing a production target must be a human act.
- **`[`, `]` and `u` are a window listener in `S/Timeline`,** not in `App.sessionKey`.
- **`Changes +84 −12` comes from one `cmd=diff`** on arrival and each time the agent comes to rest, for the session on screen only (`FE/changes.svelte.ts`), because no new field may reach `SessionInfo`. An unknown figure renders as nothing.
- **The delivery popover shows `checks` and `review` as words.** `SessionInfo` carries no per-check breakdown.
- **A turn in Changes is read-only.** Comments anchor on the diff against the base, since a line number in one turn's diff means nothing to the agent later.
- **The working state's focal point is motion.** The live tool row's timer is the only thing that moves. A static blur of a short story reads your own message first, so check 1 of the rubric is only partly answerable from a screenshot in that state.

## Rules a review added

These came out of reviewing the built view. Each has a test, and each is here because the code cannot show why it is shaped that way.

**A dialog's top edge.** `attention.blockTop` takes the one rule at the left margin that spans the whole pane. A command or question can print rows of rule characters, so the nearest rule proves nothing, and anything that leaves the edge in doubt (two such rows, dashed dividers that cannot pair below the edge, a divider above it) reads as a modal with no keys. The hash then covers the whole block, so a card can no longer show a harmless tail over a different head.

**What a person typed keeps its shape.** `humanText` removes control bytes and trailing backslashes and turns tabs into spaces, and leaves indentation and blank lines alone. Over `SSHMaxSayBytes` is refused rather than clipped, because the words exist nowhere else. A known screen command is refused in `handleAgentSay` too, whichever client sent it and however it was spelled, and `TestScreenCommandsMatchTheApp` holds the Go list and `slash.ts` together.

**The transport.** `send-keys -l --` with a trailing `;` written as `\;`, measured on tmux 3.7c. The paste path over 800 bytes is for Claude Code only (`agent.Caps.Paste`). Codex and opencode keep plain `send-keys`, as they did, until someone measures the paste and its Enter on them. A sandboxed Claude Code keeps the paste: `docker run -it` was not measured, but the program reading the bytes is the measured one, and typing over 1 KB at it is known to damage text.

**A stop.** Every Esc that interrupts a turn goes through `escIfWorking`, with its pane proof and its debounce, so the conversation's Stop and the wheel's interrupt cannot add up to two Escs on a resting composer. A wheel interrupt starts the hold on automation but does not hold the queue, since taking the wheel does not say "keep my words". Stop & send starts the hold with its Esc and keeps it when the message did not go. It also compares `-- INSERT --` across the Esc: if a vim composer left INSERT, the message stays held for a person to send, because typed there it would run as commands. Whether an Esc mid-turn does switch modes has not been measured, and the comparison costs nothing if it does not.

**A held queue.** The `user_prompt` hook fires for what lola types too, so `typeToAgentLocked` marks its own send and that hook does not release a human's hold. A prompt typed in the terminal does.

**A permission park.** It can end without a hook: a denial fires none, and the Notification hook can land after the answer. The talk path releases `InputPermission` on a read with the cursor proof, which a dialog cannot give. A notification hook re-reads the pane after it parks. `InputDialog` and `InputQuotaLimited` are never released by a bare resting pane this way. The memory of a prompt lasts until a read proves it gone, since a draft or a read straddling an answer proves nothing.

**Reuse of a session id.** `session_started` on a store with an ended record drops the queue, marks those messages recalled, and carries the old transcript's segments onto the new record. Words meant for the old agent are an instruction nobody gave this one. A first session has no ended record, so its own first words are safe from this.

**A reader rebuilt.** A `Feed` starts its count over, and a rescan can come back lower, so `talkState` carries `feedEpoch` and a client also treats a lower sequence as a reset. Without this a mounted view stops updating until the new count passes the old one.

**How far sends reach back.** A restart, an eviction or paging older history matches the whole log against `Sent`, so it holds 256 sends. A user record older than that record is hidden rather than labelled as typed in the terminal, since it may be a CI reaction or a joined batch. What is lost is the rare terminal row that old.

**Receipts.** A receipt carries the time the Stop hook arrived. The snapshot can outlast the send that hook flushes, and stamped when it was written it would sort after the next message and close the wrong turn.

**Queued messages in the log.** Claude Code records a message typed mid-turn as a `queued_command` attachment, and usually writes no user record for it. `claudeSource.attachment` reads those from a person and from background tasks, and a user record of the same words within ten minutes is not shown again.

**CI rows.** Leaving `ci_failed` proves nothing: a push whose checks have not registered reads as no checks at all. The red is remembered and only `ChecksState == pass` answers it, in whichever cycle that shows up. The memory does not survive a restart, so a restart in between loses the green row and never invents one.

**Retention.** A store with nothing to resume is not written at startup, because a write moves the file's time and a daemon restarted weekly would keep every store forever. A store with a recorded end ages from that end, and it is not loaded into memory at startup. A claim left by a crash is cleared there, so the message can go again or be recalled. It may be typed twice, which is better than losing the one copy.

**Focus requests.** A request for a card waits for the session's next talk and timeline answers, and no longer, with a two second limit. With no card by then the keyboard goes to the composer. A card that appears later never takes focus. `holdArrival` lasts one flush, so a request on a session already on its conversation cannot swallow the next real arrival.

**A log that cannot be read.** The last bad health is kept per agent in `lola.feed.bad`, so the next open lands on the terminal even before its first answer. The pin in `terms.arrive` only guards a session while it is open, and `terms.leave` drops what nobody chose. A good answer clears the record.

**A long brief.** A local session's launch line goes in a private file that the shell removes as it starts, so tmux gets a short command. The brief is still the agent's argument: a file for it to read would make it ask permission to leave the borrowed directory on its first turn.

## The Conversation's layouts

The conversation uses one chronological layout at every height: both user messages and agent output align left, and the composer occupies a centered footer below the entire timeline.
The timeline fills the available pane; the composer has a 960px cap. Paragraphs stop at 70ch,
while tables and tool output can use the wider lane. Narrow containers reduce padding and
stack ledger labels. Short containers preserve the footer and scroll the history.

Typography follows the desktop's compact reading scale: 14px/22px prose, 12px/17px
metadata, 15px section headings, and 16px/24px answer leads. Weight, spacing, speaker
labels, and enclosure establish hierarchy rather than oversized text. The native system
sans is used for reading; monospace is reserved for code and measured values.

- Person first in the DOM and above the reply, with a speaker label and timestamp.
- Only recorded user messages receive a bubble; transcript-only messages retain their terminal label.
- Agent identity introduces each expanded turn. Completed turns collapse preceding narration
  and tools into a compact Work details disclosure, with failure counts kept visible.
- Responses have a restrained panel. Supporting sections collapse by heading; headings naming
  risks, failures, questions, confirmation or next steps start open. Headingless long responses
  show the opening and keep the remainder under Full response. No source text is deleted.
- Search and jumps open containing work groups and response sections before scrolling.
- Completion summaries wrap their actions; file names align left beneath them.
- State and controls remain visible in the composer at every width. Long drafts scroll
  after 160px or 30% of the conversation height, whichever is smaller.
- The timeline rail ends above the footer; no second status copy or side composer.

### An answer as elements

`lib/answerblocks.ts` turns an answer into data for `C/SSHRich`, with no HTML. It is display-only and fails open: anything it does not understand stays a paragraph, it drops only markers it consumed, a throw returns the text as one paragraph, and a property test checks that no word is lost. The mapping follows what real answers contain: lists in 58 of 89, bold-only lines used as headings in 29, paragraphs opening on a bold label in 25, tables in 2, `##` headings in none.

| Source | Becomes |
|---|---|
| First paragraph | Lead: its first sentence if at most 140 characters, else the paragraph if at most 160, else none. Never cut inside code or bold, or after a known abbreviation |
| Bold-only line, `#`–`######` | Heading |
| `ul` with at least 2 items, 2 of 3 labelled; 2 or more `**Label:** text` paragraphs in a row | Ledger: a 22% label column and a full measure beside it. A label over 28 characters stacks above its text |
| Other lists | Hanging list. Indented items become a sublist in every renderer, so a nested bullet no longer ends a numbered list |
| `✓ ✗ ⚠ ✅ ❌`, `[ok] [warn] [bad]` opening an item | Status mark. `ok` is `text-ink-2` here, never green |
| Last paragraph ending in `?`, at most 280 characters, not the only block | Closing question, in place, with a side line |
| `>`, or a paragraph opening `Note/Warning/Caution/Important/Hinweis/Warnung/Achtung/Wichtig:` | Callout with a side line, no fill |
| Pipe table | Table with rules. Exact status words in warn or bad, never good |
| Fence | Mono with a side line, never a plate. Over 8 lines it folds, first line as its label |

Inline, `inlineSpans` recurses into bold and em, so code inside bold is code. `#123` is magenta, `PrBadge`'s colour, and links when the session's own PR names the repository. `[text](http...)` opens through the daemon, and any other target stays literal. A label's tone comes from a small en and de word list (`labelTone`): a claim about words, display-only, ink otherwise, and never green.

Known conflicts, left as they were: the 12-line clamp on a pasted message against "messages never fold", and the composer footer's pulse in the column against "nothing loops". A right-hugged bubble sits about 310 to 350px from the prose edge, past the perception guide's 240px judgement, because "agent left, person right" is the owner's own wording.

## One view at two heights

The owner asked for this after using the first build. It replaces the read-only Glance.

- `SessionsColumn` keeps one `SessionEmbed`, for the open session or else the selected row, and its wrapper is a CSS state. Enter changes the height and nothing remounts. The list row is `fit-content(45vh)` and the session takes the rest, at least 280px.
- The band draws its own title row, because the top bar names the list, and its button expands. It takes the keyboard only from a click: no autofocus on the composer or terminal while unopened, so `j` and `k` keep walking the list. `Esc` leaves the composer.
- The selection reaches the view after 120 ms, so holding `j` mounts nothing per row. A session that opens on its terminal attaches while it is selected, which defers automated sends to it for that long. This is the cost of one view, and a session on its Conversation has none.
- **The sidebar** (`S/SessionSide`, `lib/sessionside.ts`) is the agent's account beside the story: summary, board, plan, findings, theories, numbers, notes and widgets. It is the Overview tab in the session dock. At 1180px and up, Overview and Changes start together in one right column; posted widgets join as individual tabs, behind the active tab. Widget titles should be short (one to three words); long labels truncate with the full title in a tooltip. The divider resizes the conversation or terminal; tabs can be dragged to split and join panels using the existing dock tree, and arrangements persist per session. The three-line Panels menu lists built-in panels and each widget, with checkmarks to show or hide them. Hidden widgets stay hidden on updates. Below that, the compact drawer, story line and Report stand in.
- **A turn's answer stands alone.** `turnParts` folds tools before the agent's last word into dig lines naming what they touched, and narration stays up between them (see "The timeline layout"). Plates, cards, widgets, elements, messages and moments never fold. What is still running after its last word stays open.
- **A long paste or an image is an attachment** (`lib/composerpaste.ts`, `desktop/talkimage.go`). A tile above the field shows it, a thumbnail or a size with its number on the corner, and the field holds `(1)`. Removing either removes both. The real text, or the path of the image's copy in `.lola/images`, takes the reference's place when the message goes. `Ctrl+V`, or pasting an image or an image file's path, attaches one, and `osascript` only reads the clipboard. The same text pasted again straight away shows it plainly. A command never holds a reference, because a person must read what runs on a host. The limit is 128 KB and 8 KB for an agent with no paste. A paste that size into a real Claude Code is not measured. A tile with no thumbnail draws a glyph rather than a word, and its title says what it is. The story reads an image's path back as `(1)` beside a small tile (`youAttachments`), for display only: the record keeps the path and find reads it. Only a path under `.lola/images` is read this way. The thumbnail is memory only (`view.sentThumbs`), so after a restart the tile is the glyph. Clicking a numbered tile opens a gallery for that message, starting at that image. The gallery reads the original through a size-limited, raster-only binding rooted in the session image folder; unavailable originals retain a cached thumbnail if present. Arrow keys and numbered thumbnails navigate; Escape closes and restores focus. Clicking the image toggles fit/full size.
- **Dev addresses rank across tabs** (`devurl.Merge`). Every address is a chip, and the same local port under two names is one.

## The timeline layout

The owner asked for a timeline: styled output with smaller text between it for whoever wants to dig. It replaces the rule that nothing in the story carries a marker. Everything here is one layout at every height and width.

- **The rail is one line for the whole story.** A hairline in the column's padding, 16px left of the text edge, drawn by the stream column rather than per turn, so a folded or off-screen turn does not break it. It stops above the tray; the state stays in the composer footer at every width. `--cv-sb` accounts for the scroller's reserved scrollbar width.
- **A mark names a kind, never a state.** `S/RailMark`, from `railKind`: a ring for what opened a turn, a small dot for narration, a larger one for the answer, an outlined square for a plate or widget, a diamond for a finding, a cap for the receipt, a ring with a static dashed tail for a running call, and a plain tick for a dig line. All neutral, none animated, all hidden from a reader. The only colour is the tick of a failure nothing has answered. State glyphs stay out.
- **A beat's shape comes from its text, its role from its position.** `answerShaped` (a newline, over 280 characters, or a list, table or fence marker) is what makes prose an answer with elements, and everything else one inline line. On real transcripts that matched 72 of 89 turn-ending answers and 2 of 333 narration beats, from the text alone, and the gap above follows it. `answerRowId` makes the last prose the answer once the turn has a receipt or the agent can no longer speak. That changes only colour and marker, so nothing reflows when a later word arrives.
- **A dig line is sans at rest.** `S/DigLine`, past tense, up to three names a verb, then `+N`. A resting wall of mono is what made this read like the shell tab, so mono returns only when someone opens a line. A diff on one is the log's claim and stays neutral.
- **Six narration beats stay up.** The newest six before the answer. Older ones fold with their work into one dig line, because real turns reach 63 beats, and this is the only way prose folds. Prose with no work beside it stays, since a fold would name nothing. When a beat folds while someone reads above the tail, `holdAnchor` pays the height back, because WKWebView has no overflow-anchor.
- **A folded turn has two lanes.** What the agent did under its column, what the person asked under theirs, so position still says who. A turn no person opened keeps their lane empty. A run of them sits close, so older turns read as one list. Restore and Fork are one click away, inside the turn. Below 1040px it falls back to one row.
- **The running head is only for the column.** The strip naming the ask shows below 1040px. Above it, the pinned message and the sticky lead do its job.

Two places differ from the spec this came from. A background task or a hand-off also counts as what opened a turn, because real sessions have many turns no person started, and they read as a blank head and a fold with no start otherwise. A run of folded turns is 4px apart rather than 32px, because at 32px real data showed twelve older turns on a screen and read as twelve separate events.


### Automation and list density

Lola handoff events use a purple labeled disclosure, separate from human messages.
Older review follow-ups lacking a send receipt are recognized only by the combined
list/reply/resolve recipe signature and labeled “recognized template.” This is
presentation-only; no automation or permission gate trusts that classification.

The overview sessions list scrolls after ten measured body rows plus its sticky
header, still subject to the existing 45vh ceiling. Measurement adapts to the taller
two-line rows, leaving the remaining height to the session view.

### Current conversation presentation

The latest layout supersedes the earlier two-lane and six-beat descriptions above.
Timeline and composer share a 1040px maximum column; prose is limited to about 70ch.
Human requests align right in blue notes; agent answers use teal summary bands without
repeating an agent-name heading. Lola instructions stay purple. Body text is 13px
with a 21px line height; an answer's opening gets the larger reading tier.

Completed activity folds into a compact stack of colored icons and counts (commands, edits,
reads, other calls and progress updates). The current update, running calls and
unanswered failures stay visible. Expanding reveals chronological commentary and
the existing animated tool stacks; individual calls still expand independently.
Search and evidence jumps open both levels. Supporting answer sections are inline
text disclosures; one opens at a time. Activity stays unboxed. Finished answers have
one enclosed surface: summary band, aligned body details and, when adjacent, the
turn receipt as its footer. An intervening event keeps the receipt separate.
No generated summaries or additional model calls are used.

Automatic regrouping slides departed rows away and eases keyed neighbors into
place. A stack acknowledges a new member with a brief settling motion and a rolling
count. All motion honors reduced motion. Collapsed command stacks show only their
latest call; the complete record stays inside the stack and its tooltip.

The composer is a rounded blue-tinted input with a complete focus border and a
bright send action. Command mode retains its separate destination and risk colors.
Receipts remain quiet metadata. The sticky context strip offers Back to message;
full text remains in its tooltip. Code and commands receive syntax coloring; logs
remain neutral and hashes stay whole.


Consecutive file changes appear as one inline Changes entry (a new burst starts after
another tool or a gap longer than 30 seconds). Its file list follows incoming edits;
selecting a file pins that selection. The latest burst opens automatically and folds
when the agent moves on, while a manually opened entry stays open. Recorded patches
remain separate when a file is edited repeatedly; their line counts are activity totals,
not a synthetic net diff. Source reads show ten highlighted lines with full output on
request. Queued messages offer Send now with immediate loading feedback; a refused
attempt keeps the messages and displays the daemon's reason.

### Retained PR review workspace

Changes and PR status offer **Review PR**, a full-screen inspection surface over
the existing mounted session. It retains the familiar diff, local checks and
Before merge controls, and adds agent-published Markdown and captioned screenshot
pairs. **Review material** in an ended session opens the retained packages without
offering live checkout actions. Stack packages remain associated with their member.

Material is an agent claim. The package records the submitted base/head and the
captured bytes; matching the observed PR head does not independently verify what
the image depicts. Failed PR reads show unknown freshness. Older package heads
show outdated material. Refreshing never switches the package under a reviewer;
new material has an explicit action to open it.

Write-up and screen comments join the existing per-session diff-feedback draft,
including the package and revision identities. Sending uses the existing safe
feedback queue. No local review action posts a GitHub comment implicitly.
The full contract and current limits are in `docs/pr-review-workspace.md`.
