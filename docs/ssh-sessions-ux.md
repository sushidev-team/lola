# Agent-assisted SSH: experience and visual spec

> **Superseded in part.** The session view is now `docs/unified-session.md`, and where the two
> differ that one is right. A session has one story for every host, with All as its default scope,
> and a host is a scope rather than a tab. The story and a host's terminal share one column, reached
> by the strip or ⌘\\, and the header has no switch. Sign-in happens on a card in the tray. The
> composer has two modes, Ask and Run; see "One story, and commands a human runs" in
> `ssh-sessions.md`. Taking the wheel stays on the story. Removing a host is in the host bar's `⋯`,
> and it keeps your messages.


The engineering contract is `docs/ssh-sessions.md`. This file says what the human
sees, reads, presses and is told. Build from it without guessing. If something here
needs a wire change, change the contract and `internal/protocol/ssh.go` in the same
step, as section 20 records for the changes this spec already made.

`{Agent}` in copy is the agent's display name: Claude, Codex or OpenCode. `{host}` is
the host's label, else its name. Everything else in quotation marks or a copy table is
final text. A sentence that exists as a constant in `internal/protocol/ssh.go`
(`SSHDataWarning` and friends) must be read from that constant, never retyped.

---

## 1. The idea

**A calm, precise instrument.** Think of a flight recorder with a good reader, not a
dashboard and not a chat app. Nothing here moves unless something is happening. There
are no cards on cards, no gauges, no big numbers, no gradients. Text carries it.

The human can always do one of three things, and each has one home:

| Mode | Question | Home |
|---|---|---|
| **Glance** | What is it doing, what has it learned, does it need me? | Session row, workspace header, the Brief |
| **Dig** | Show me exactly what ran and what came back. | One click from any claim to a block, one more to raw output |
| **Drive** | Let me do it. | The wheel, and the terminal beside the stream |

### 1.1 Three voices

This is the whole visual system. Everything on the timeline speaks in exactly one of
three voices, and the voices differ in **type, container, position and marker** at
once, so the difference survives no colour, a glance and a small window.

| Voice | Who | Means | Type | Container | Marker on the rail |
|---|---|---|---|---|---|
| **Lola** | The daemon | A **fact**. It recorded this itself | `font-mono text-sm` | A plate: `bg-panel border border-edge/60 rounded-md` | Solid, status-shaped (`BoardMarker`) |
| **Agent** | The coding agent | A **claim**. It may be wrong, or steered by what it read | `font-sans`, default 13px | None. Bare text on the canvas | Hollow ring: `h-2 w-2 rounded-full border border-faint` |
| **You** | The human | A **statement**. You said this | `font-sans`, default 13px | A soft tint: `bg-sel/50 rounded-md px-2.5 py-1.5`, with a `You` label in `text-sm text-accent-ink` | Solid `bg-accent` square, `h-2 w-2` |

Rules that keep it honest:

1. **Nothing inside a plate was composed by the agent, apart from the exact command
   line it asked to run.** The `why` hangs **outside** its plate, in the agent's voice.
2. **Nothing the agent wrote may ever draw a plate or a solid marker.** Severity may
   colour a claim's ring. It never fills it and never boxes it.
3. **A green tick is a daemon fact.** The agent's words get no green tick, ever, not
   even a "confirmed" hypothesis, which reads as a word on a coloured ring.
4. **An evidence link is a small plate inside a claim**, so a reader sees exactly
   where the claim touches something recorded. It shows the block's facts and never
   says "verified", because the block being real does not make the claim true.

The stream's filter row carries a permanent, quiet legend. It both teaches and
confirms:

```
Everything  Facts  Findings                        ▪ Lola recorded  ○ Agent says  ▪ You
```

(`text-sm text-faint`, the "You" square in `text-accent`.)

### 1.2 The colour budget

Tokens only, per `app.css`. Each colour has one job in this feature and is not spent on
anything else here.

| Token | Job here |
|---|---|
| `orange` | **You are needed.** A go, an approval, authentication. Same meaning as the app's needs-you pill |
| `bad` | Production tier tag and band. Non-zero exit, failure, denial |
| `warn` | Staging tier. A live grant. Disconnected. Unknown results |
| `accent` / `accent-ink` | **You.** Your voice, your wheel, your driving interval, the focus ring |
| `info` | The agent is at the wheel and working. Running commands |
| `good` | Exit 0 tick only, at small size |
| `faint` | Everything else: metadata, dev tier, labels, system events |

Hierarchy is size, then weight, then colour, and an element spends only two, per
`app.css`. The five type sizes stand. No `text-[Npx]`, no weight 700. Every number is
`num`. Every piece of content is `.selectable`; row chrome is not.

### 1.3 Motion budget

Motion means "happening now". 150 to 200ms, ease-out, for every transition. Only three
things loop: the running arc (`BoardMarker kind="running" live`), the connecting pulse
on the header's state dot, and the agent's `LivePulse`. Everything holds a distinct
static shape under `prefers-reduced-motion`. Nothing animates on arrival in the stream
except a 150ms opacity fade. No staggering.

### 1.4 Tier is unmissable and never shouts

`SSHTierTag.svelte`: the `label` utility in an outlined tag, `h-5 px-1.5 rounded border`.

| Tier | Word | Classes |
|---|---|---|
| production (also unset and unknown) | `PRODUCTION` | `border-bad/60 text-bad` |
| staging | `STAGING` | `border-warn/50 text-warn` |
| dev | `DEV` | `border-edge text-faint` |

Never filled. A permanently filled red tag becomes wallpaper in a day, and it would
compete with the one solid thing that matters, an action. Three more places carry tier:

- **The band.** A 2px line across the top of the workspace, and again across the top of
  every go and approval card: `bg-bad/70` production, `bg-warn/50` staging, none on dev.
  You cannot be looking at a production workspace without a red line above it.
- **The approval button's words.** They name the host. See section 9.
- **The session row's chip.** See section 15.

---

## 2. Where it lives

`SessionEmbed`'s tab strip gains one cell, same shape as `Agent` and `Diff`:

```
| SSH 2 | Agent | Diff | sh1 | + |
```

- The `SSH` tab exists when `session.ssh?.connections.length > 0`. The count, in
  `text-orange`, is pending approvals plus connections awaiting a go.
- A **direct session** (`location === "ssh"`) opens on `SSH`. Its strip has no `Diff`
  and no Checkpoints, because it has no checkout.
- A **running session that asks** gets the tab when the request arrives. Its selection
  does not move and focus is not taken. The row, the notification and the count do that
  work.
- The `-ssh-N` terminal **never appears as a strip tab**. It is mounted only inside the
  workspace. One tmux session with two xterm clients would fight over its size.
- The session `SidePanel` (Report, Checkpoints) is hidden on this tab. The Brief covers
  the agent's account here, and three columns of reading is one too many.

---

## 3. The workspace

Two columns: the **stream** you read and talk in, the **terminal** you drive in. The
conversation is not a third pane. Your words and the agent's are items in the stream,
interleaved with what actually ran, because that interleaving is the story.

```
┌─ band: 2px bg-bad/70 ───────────────────────────────────────────────────────────────────────┐
│ [PRODUCTION] prod-api  deploy@10.0.1.4   ● Connected 12m      At the wheel [Agent|You]    ⋯ │  header
├────────────────────────────────────────────────┬────────────────────────────────────────────┤
│ BRIEF  (agent's account, then one fact row)    │ Shell  Agent                         ⤢  ×  │
│ ○ API restarts because the disk fills…    2m   │                                            │
│ ○ Plan 2/5  ▰▰▱▱▱  Checking disk pressure      │  deploy@prod-api:~$ █                      │
│ ○ 1 confirmed  2 open   ○ 1 high  2 medium     │                                            │
│ ▪ Running  df -h /var  · 4s                    │                                            │
│ ───────────────────────────────────────────    │                                            │
│ Everything  Facts  Findings      ▪ ○ ▪ legend  │                                            │
│ ┊                                              │                                            │
│ ○ I'll check whether the disk is full.         │                                            │
│ ┊                                              │                                            │
│ ▪ ┌──────────────────────────────────────┐     │                                            │
│ ┊ │$ df -h /var        exit 0 · 0.3s  ▸  │     │                                            │
│ ┊ └──────────────────────────────────────┘     │                                            │
│ ┊ ○ Agent: check free space on the log volume  │                                            │
│ ┊                                              │                                            │
│ ▪ Look at the log rotation too.         You    │                                            │
│ ┊                                              │                                            │
│ (stream scrolls)                               │                                            │
├────────────────────────────────────────────────┤                                            │
│ DOCK: approval or go card, only when one waits │                                            │
├────────────────────────────────────────────────┤                                            │
│ Claude is working. It gets your message at its │                                            │
│ next command.                                  │                                            │
│ ┌────────────────────────────────────────────┐ │                                            │
│ │ Talk to Claude…                          ↩ │ │                                            │
│ └────────────────────────────────────────────┘ │                                            │
└────────────────────────────────────────────────┴────────────────────────────────────────────┘
```

### 3.1 Structure and sizing

CSS grid throughout, per the WebKit gotcha in `CLAUDE.md`. Never a flex child stretching
in a column.

```
workspace:     grid-rows-[auto_auto_minmax(0,1fr)]              band, header, body
body:          grid-cols-[minmax(0,var(--ssh-split))_6px_minmax(0,1fr)]
stream column: grid-rows-[auto_minmax(0,1fr)_auto_auto]         brief, items, dock, composer
```

Surfaces: the stream is `bg-canvas`, so plates (`bg-panel`) read as raised. The terminal
is `bg-panel p-4`, the same as every terminal in the app, so its padding stays seamless.
No `isolate` or `z-0` around the terminal: that blanks the WebGL canvas in WKWebView.

**The split.**

| Width of the main pane | Layout |
|---|---|
| 980px and up | Two columns. Default 52% stream, 48% terminal. Stream min 420px, terminal min 360px |
| Under 980px | One column with a segmented `Stream / Terminal` in the header. The dock and the composer stay on `Stream` |

The 6px gutter is the drag handle: `cursor-col-resize`, a 1px `bg-edge/60` line that
becomes `bg-accent` on hover and drag, `role="separator"`, ←/→ moves it 2% when focused,
double-click resets. Pointer events, not HTML5 drag-and-drop, like the sidebar. The ratio
persists per session in `localStorage`. xterm re-fits on `ResizeObserver` and tells tmux.

| Trigger | What the split does |
|---|---|
| Authentication begins | Terminal shown if collapsed. Split unchanged. Terminal takes focus only when the human just pressed go |
| Take the wheel | Terminal grows to 62% over 200ms, focus moves into it. The previous ratio is remembered |
| Hand back | Returns to the remembered ratio unless the human dragged while driving |
| `⌘\` or the `×` in the terminal header | Terminal collapses to a 32px vertical rail reading "Shell" with its live dot. Click to restore |
| `⤢` in the terminal header | Terminal maximizes, and the stream collapses to a 32px rail carrying the orange count. Esc does not restore it, because the terminal owns Esc. `⤢` or `⌘\` does |

Terminal state is never lost: the xterm stays mounted while collapsed, and only a change
of active shell remounts it.

### 3.2 The terminal pane's tabs

`Shell` and `Agent` sit above the terminal. `Shell` is the host (`conn.tab`). `Agent` is
the agent's own pane. Both are `LiveTerminal`, full input, always yours. The strip's main
`Agent` tab reaches the same pane, and only one mount exists at a time, because selecting
the strip's tab leaves the workspace.

On first focus of `Shell` while the wheel is `agent`, a one-line hint sits above the
terminal for that connection, until dismissed:

> Claude keeps working while you type here. Take the wheel to stop it. ×

### 3.3 More than one host

A session may hold several connections. Under the header, a row of host chips, shown only
when two or more are in play. A closed or denied connection stays in the stream but leaves
this row, unless it is the one on screen:

```
[PRODUCTION prod-api ●]  [STAGING stage-api ●  1]
```

A chip selects the connection that the header's controls, the terminal and the dock apply
to. The stream shows all of them unless filtered, and each plate then prefixes its command
with the host name in `text-faint`. The orange number is that connection's pending count.

---

## 4. The header

One line, `min-h-10 px-3 border-b border-edge/60 flex-wrap`. It reads left to right as a
sentence: **where, who as, what state, who has the wheel.**

```
[PRODUCTION] prod-api  deploy@10.0.1.4   ● Connected 12m  │ Grant 12:04  Revoke │  At the wheel [Agent|You]  ⋯
```

| Part | Treatment |
|---|---|
| Tier tag | Section 1.4 |
| Host label | `font-medium text-ink` |
| Identity | `font-mono text-sm text-faint selectable`: `ResolvedUser@ResolvedHostname`. Falls back to `User@alias`, then `alias`. Tooltip: "What your ssh config says this alias means." |
| Sandbox | Only when **not** sandboxed, `text-sm text-warn`: `Unsandboxed`, tooltip `SSHUnsandboxedNote` |
| State | A dot plus a word, section 12's table |
| Grant | Only while live: `text-warn num`, a `m:ss` countdown, `Revoke` as `Button size="xs"`. Tooltip: "The agent can run any command here without asking until this ends." |
| Wheel | Section 10 |
| `⋯` | `Button icon size="xs"` menu, `MenuItem` rows: Copy ssh alias, Open a new shell (when the tab is dead), Show agent terminal, Close connection… |

---

## 5. The Brief: the glance

Pinned at the top of the stream, `border-b border-edge/60 px-3 py-2.5`, with
`max-h-[33%] overflow-y-auto`. A chevron collapses it to its first line, per session. It
is **derived by the client from timeline items** and holds the agent's standing account,
plus one fact row. Elements updated through `--ref` stay at their original position in the
stream, so this is where their current state stays visible.

```
AGENT'S ACCOUNT                                                                    ▾
○ Disk on /var fills every 40 minutes. Log rotation stopped after the 2 Nov deploy.  2m
○ Plan 2/5  ▰▰▱▱▱  Checking log rotation config
○ Theories  1 confirmed · 2 open · 1 refuted       Findings  ◉ 1 high  ◉ 2 medium
───
▪ Running   df -h /var · 4s                                                    Stop
```

- `AGENT'S ACCOUNT` is `label text-faint`. The word "account" is deliberate. It is theirs.
- Line 1: the latest `summary`'s title in `text-ink copy`. Its `TimeAgo` in `text-sm`, and
  `text-warn` once older than 15 minutes (`boardStale`'s threshold). Older text fades to
  `opacity-60`, as `BoardPanel` does: a confident stale summary is worse than none.
- Line 2: the latest `plan`. The bar uses `▰▱` in `text-faint` and never green, since this
  is a claim of progress. Clicking scrolls to the plan.
- Line 3: hypothesis counts and findings by severity, worst first. Each count is a filter
  shortcut: clicking `Findings` or `Theories` applies the `Findings` filter.
- Lines with nothing to say are omitted.
- The last row is the **Now row**, the only fact here, in Lola's voice with a solid marker:

| Fact | Now row |
|---|---|
| Newest running block | `▪ Running  {command}  · 4s` plus `Stop` |
| Pending approval | `▪ Waiting for you to approve {n} command{s}` in `text-orange` |
| Running blocks plus queued | `▪ Running {n}, {m} queued` |
| Agent working, nothing running | `▪ {Agent} is working` with `LivePulse` |
| Agent idle | `▪ {Agent} is waiting` |
| Agent `needs_you` in its own pane | `▪ {Agent} is waiting on a prompt in its own terminal.` + `Show` |
| Agent gone | `▪ {Agent} has exited` + `Revive` |

**Empty Brief**, before any summary or plan: only the Now row shows, with no label.

---

## 6. The stream

The left gutter is a 1px vertical `bg-edge/60` **rail** at x=20px, grid
`grid-cols-[40px_minmax(0,1fr)]`, items spaced `gap-y-3`. Markers sit centred on the rail.

**The rail records the connection.** It is a fact drawn as a line:

| Period | Rail |
|---|---|
| Connected, wheel with the agent | Solid `bg-edge/60` |
| You drove (wheel `human`) | 2px `bg-accent`, with its label, section 10.4 |
| Disconnected, not connected yet, or after close | Dashed `edge/60` |

### 6.1 Order and scroll

- Sort by `Order`, upsert by `ID`. Fetch `sshTimeline{After: lastSeq}` when
  `session.ssh.lastSeq` moves, and on a 1s tick while this tab is on screen and a block is
  running. Keep the items in a store keyed by session, so returning to the tab is instant.
- **Stick to the bottom** while the reader is within 40px of it. Otherwise never move
  them. New content raises a pill above the composer: `3 new · Jump to latest`.
- Approvals never need finding: the dock pins them (section 9). The stream still has the
  block's row, reading `Waiting for you`.
- Dig jumps push a **Back** pill at the same spot: `← Back to where you were`. Digging must
  not lose your place.

### 6.2 Filter

Segmented `Button size="xs"` with `selected`, sticky under the Brief. Per session.

| Filter | Shows |
|---|---|
| **Everything** | All items |
| **Facts** | Blocks and system events only. Every claim vanishes, `why`s too. This is the audit view: what actually happened, with no account of it |
| **Findings** | The findings, hypotheses, evidence, recommendations and proposals, each with its evidence plates. The conclusion view |

`/` opens a find field at the filter row. It matches command, why, element text and human
text in loaded items, with `↑↓` to walk matches. It does not search output, whose own find
lives in the viewer. Copy: placeholder `Find in this timeline`, no results `Nothing here
matches. Output has its own find.`

### 6.3 Folding

Three or more consecutive blocks that all `succeeded`, with nothing between them, fold into
one plate:

```
▪ ┌───────────────────────────────────────────────┐
  │ 5 commands · all exit 0 · 2.1s total        ▸ │
  └───────────────────────────────────────────────┘
```

Never fold a failed, refused, stopped or running block, one with `actor = human`, or one
that an element's evidence points at, since a jump must always land on something visible.
The Facts filter folds nothing, since it is the audit view.

### 6.4 System events

Lola's voice with no plate, because they are small and frequent. A small solid tick on the
rail, `font-mono text-sm text-faint`: `System.Text`, then time at the right, then
`System.Note` on a second line in your voice if present.

The exact copy is daemon-side `Text`, one sentence each:

| Kind | Text |
|---|---|
| `requested` | `{Agent} asked to connect to {host}.` |
| `go` | `You gave the go. Opening a terminal.` |
| `denied` | `You denied the connection.` |
| `connected` | `Connected to {host}.` |
| `disconnected` | `Connection lost.` or `Connection closed after {n} minutes idle.` |
| `failed` | `ssh exited before connecting. Its message is in the terminal.` |
| `wheel_taken` | `You took the wheel.` |
| `handed_back` | `You handed the wheel back after {duration}.` plus `Transcript sent.` when `HasTranscript` |
| `grant_created` | `You allowed every command on {host} for {n} minutes.` |
| `grant_ended` | `Grant ended: expired.` / `revoked.` / `you took the wheel.` / `connection closed.` |
| `closed` | `Connection closed.` |

---

## 7. Block anatomy

A block is a plate at three depths. Each depth answers one more question and no click is
wasted getting there.

### 7.1 Depth 1: the row (glance)

```
 ✗  ┌────────────────────────────────────────────────────────────────────────┐
 ┊  │ $ systemctl status api                    exit 3 · 0.4s · 2 KB   14:02 ▸│
 ┊  │   Active: failed (Result: exit-code) since Wed 13:58:11 UTC             │
 ┊  └────────────────────────────────────────────────────────────────────────┘
 ┊  ○ {Agent}: see whether the service restarted
```

- Line 1: `$` in `text-faint`, then the command in `text-ink`, single line, truncated. The
  full text is one click away and always shown whole at approval. At the right, in
  `text-sm text-faint num`: result, duration, stored size, time, chevron. The result is
  `text-bad` when it is a non-zero exit.
- Line 2: `Preview`'s last non-empty line in `text-faint`, only when the block failed or is
  running. Exit-0 rows stay one line, so a healthy run reads calm. `stderr` previews get a
  `text-sm` `stderr` prefix.
- Plate tint: failed, timed out, stopped or lost take a bad or warn border plus a 6% tint MIXED INTO
  the panel, `bg-[color-mix(in_oklab,var(--color-bad)_6%,var(--color-panel))]!`. A translucent
  `bg-bad/5` replaces `bg-panel`, so the plate sinks into the canvas and a warning reads weaker than a
  success. Succeeded stays plain.
- **Outside the plate**, in the agent's voice: `Why`, `text-sm text-faint`, prefixed by a
  hollow ring and the agent's name. It collapses with the row; in the Facts filter it hides.

A whole row is a `<button>` for expand. `aria-expanded` on it.

**A non-zero exit is not a verdict.** `grep` exits 1 on no match. The row says `exit 1` in
red and never the word "failed". The status marker is the `fail` shape, because the daemon's
status is `failed`, and the words stay factual.

**Per status:**

| Status | Marker | Right side of line 1 | Line 2 / `StatusDetail` | Plate |
|---|---|---|---|---|
| `pending_approval` | `blocked`, orange | `Waiting for you` in `text-orange` · `{ago}` | `Expires in {t}` | `border-orange/40` |
| `queued` | `pending` | `Queued` | `Approved. Waiting for a free slot.` | plain |
| `running` | `running` live, info | `1m 12s of 2m` | latest preview line, live | `border-info/40` |
| `succeeded` | `pass` | `exit 0 · 0.3s · 2 KB` | none | plain |
| `failed` | `fail` | `exit 3 · 0.4s · 2 KB` | preview | `border-bad/35` + tint |
| `denied` | hollow ring with a slash | `Denied` | `You denied this.` then your reason in your voice | plain, `opacity-70` |
| `expired` | hollow ring with a slash | `Expired` | `Nobody answered within {n} minutes. It did not run.` | plain, `opacity-70` |
| `refused` | hollow ring with a slash | `Did not run` | `StatusDetail`, section 12's refusal copy | plain, `opacity-70` |
| `timed_out` | `fail`, warn | `Timed out · {t}` | `Stopped locally at the {t} limit. The remote process may still be running.` | `border-warn/35` + tint |
| `stopped` | `fail`, warn | `Stopped · {t}` | `StatusDetail`, then `SSHRemoteOutlives` when it had started | `border-warn/35` + tint |
| `lost` | `?` in a warn ring | `Result unknown · {t}` | `StatusDetail`: `The connection dropped.` or `Lola restarted while this ran.` then `The remote process may still be running.` | `border-warn/35` + tint |

`BoardMarker` gains kinds `refused` (slash) and `unknown` (`?`). Its shapes stay distinct with
no colour and no motion.

**Approval is a fact worth seeing.** A tag at the right of line 1, `text-sm text-faint`, only
when not the usual per-command path: `grant` (with `text-warn`) or `you ran this` for
`actor = human`. A human's run also takes a solid accent marker, because you did this.

### 7.2 Depth 2: expanded (dig)

Click anywhere on the row. The plate grows in place, nothing else moves, and scroll is kept.

```
 ✗  ┌────────────────────────────────────────────────────────────────────────┐
    │ b12                                                                 ▾  │
    │ $ systemctl status api                                                 │
    │                                                                        │
    │ Host     prod-api (deploy@10.0.1.4)   Started  14:02:11                │
    │ Tier     PRODUCTION                   Ended    14:02:11 · 0.4s         │
    │ Runs in  /srv/api                     Exit     3                       │
    │ Asked by {Agent}                      Allowed  You approved at 14:02   │
    │                                                                        │
    │ stdout 2 KB   stderr 0 B                         Wrap  Find  Copy  ⤢   │
    │ ┌────────────────────────────────────────────────────────────────────┐ │
    │ │ ● api.service - API                                                │ │
    │ │    Active: failed (Result: exit-code) since Wed 13:58:11 UTC       │ │
    │ │    …                                                               │ │
    │ └────────────────────────────────────────────────────────────────────┘ │
    │ Copy command   Ask about this                                          │
    └────────────────────────────────────────────────────────────────────────┘
    ○ {Agent}: see whether the service restarted
```

- The block id `b12` appears here, `text-sm text-faint`, `.selectable`.
- The command is whole, wrapped, `whitespace-pre-wrap break-all`, exact bytes.
- A two-column definition list, labels `text-sm text-faint`, values mono. `Runs in` shows
  `login directory` in `text-faint` when `Cwd` is empty. `Timeout` is added only while
  running. `Allowed` reads, per approval mode: `You approved at {t}` / `Grant, until {t}` /
  `You ran this`.
- **Ask about this** puts `About b12: ` in the composer and focuses it. It takes you from
  digging to talking without retyping an id.
- While running, a `Stop` action (`variant="danger" size="xs"`) sits top right, with
  `SSHRemoteOutlives` beside it in `text-sm text-faint`. Stop asks nothing. It is the safe
  direction, and a second press does nothing.

### 7.3 Depth 3: raw output

Output appears the moment a block expands. There is no "show output" step. Fetch through
`sshOutput` lazily, on expand.

- **Streams are separate, on purpose.** Two text tabs: `stdout 2 KB`, `stderr 0 B`. Opens on
  `stderr` when the block failed and it has bytes, otherwise `stdout`. When a stream is empty
  its tab is `text-faint`. There is no merged view: lola has no record of how the two
  interleaved, and a false ordering would be worse than two tabs.
- `font-mono text-sm`, `selectable`, `bg-canvas`, 1px `border-edge/60`, up to 320px high with
  its own scroll. `⤢` expands it to fill the whole stream column, with Esc to return.
- **Text nodes only.** Tokens matching `https?://` become links through `store.openURL`.
  Nothing else in output is ever interactive, and no ANSI colour survives, since the
  `Cleaner` already stripped it.
- **Long output.** Load 256 KiB at a time, on scroll to the end. Past the cap, the head ends,
  then a marker row, then `Tail`:

  ```
  ─────  3.1 MB not kept  ─────  (text-warn, centred)
  ```

  Copy: `Lola kept the first {cap} and the last 64 KB. The middle is gone.`
- **Find:** `Find` or `/` when the viewer has focus. Matches loaded text, with count `3 of 41`.
  If more is unloaded: `Searched the first {n}. Load the rest`.
- **Copy** copies loaded text of the selected stream. If more exists: `Copied the loaded
  {n}. Load the rest first to copy it all.`
- **Live.** A running block's viewer polls `sshOutput` at 1s from its offset and stays at the
  bottom. Scrolling up pauses following, and a pill reads `Following paused · Resume`.
- **Empty and absent.**

| Case | Copy |
|---|---|
| No bytes, block ended | `No output.` |
| No bytes, running | `No output yet.` |
| Never ran | `This command did not run, so there is no output.` |
| `Binary` | `This output contained binary data. Control characters were removed.` (banner, text follows) |
| `Pruned` | `Lola removed this output to stay within its storage limit. The command, exit code and timing stay on record.` |
| Write failure (`DroppedBytes` with no stored bytes) | `Lola could not store this output. {n} were lost. The exit code is still accurate.` |
| Fetch fails | `Could not load this output.` + `Try again` |

---

## 8. Elements

Every element is in the agent's voice: bare text, hollow ring, no plate, no solid fill. Kinds
differ by a quiet `text-sm text-faint` lead word and by small structure, never by a box. A run
of consecutive claims gets the agent's name once, as `{Agent}` in `label text-faint`, above
the first. `Revision > 1` adds `updated {ago}` in `text-sm text-faint`, tooltip: `{Agent}
changed this {n} times. Lola keeps only the latest version.`

`Body` is folded behind `▸ More` unless section 8.1's table says otherwise, and uses `copy`.
Bare block ids in any agent text (`\bb[0-9]+\b` matching a known block) become the same link
as an evidence plate. Other text is text, plus `https?://` through `store.openURL`.

An unknown kind, from a daemon newer than this build, renders as a plain note: title and body
with no lead word.

### 8.1 The ten kinds

**summary.** Where the investigation stands. `text-ink`, full `copy` measure, ring in
`text-ink`. Body shown open. It also feeds the Brief's first line. Lead word: none.

```
○ Disk on /var fills every 40 minutes. Log rotation stopped after the 2 Nov deploy.
```

**narration.** The agent's running voice, and its answer when you talk to it. Quietest kind.
`text-ink/75`, no lead word. Body shown open, since it is often an answer. Consecutive
narrations tighten to `gap-y-1` so a burst reads as one paragraph. A narration directly after
your message is the conversation, and it needs nothing more than proximity.

**hypothesis.** A theory. Lead word `Theory`, then the state as a word, never a tick.

| State | Ring | State word |
|---|---|---|
| `open` | `border-faint` | `Open` in `text-faint` |
| `confirmed` | `border-ink` plus a centre dot in `bg-ink` at 4px | `Agent says confirmed` in `text-ink` |
| `refuted` | `border-faint`, title `line-through decoration-edge text-faint` | `Agent says refuted` in `text-faint` |

`Agent says` is required. A bare "Confirmed" would read as lola's verdict.

**finding.** What it learned. Lead word is the severity. The ring takes the severity's colour,
and is still hollow, so a critical finding is a red ring and not a red block.

| Severity | Ring | Word |
|---|---|---|
| `critical` | `border-bad` 2px | `Critical` in `text-bad` |
| `high` | `border-orange` | `High` in `text-orange` |
| `medium` | `border-warn` | `Medium` in `text-warn` |
| `low` | `border-info` | `Low` in `text-faint` |
| `info` | `border-faint` | `Note` in `text-faint` |

```
○ High  Log rotation has not run since 2 Nov.               updated 1m
        [b12 exit 3]  [b14 exit 0]
```

Severity never raises attention. An agent cannot page you by saying "critical".

**plan.** Lead word `Plan` with the title. An ordered list with `BoardPanel`'s rail, drawn as
hollow shapes: `pending` hollow circle in `border-edge`, `active` hollow with `text-ink` text,
`done` a hollow circle with the text `line-through decoration-edge text-faint`, `skipped` the
text `text-faint` with `(skipped)`, `failed` a `border-bad` ring and `text-ink` text. Steps
are not green. Green is a daemon's word. Revisions update it in place.

**metric.** Key-values it read. Lead word `Read` and the title. A two-column list: name
`text-faint`, value and unit in `font-sans num text-ink`. Sans, not mono, because a value the
agent copied is not a recorded fact. Tooltip on the list: `{Agent} copied these from its
commands. Check the linked output to confirm.` An evidence plate beside it is how to check.

**evidence.** A claim tied to blocks. Title as an ordinary claim, plates below it, always
visible. This is the kind that exists to be dug into.

**recommendation.** Advice in prose. Lead word `Suggests`. Body shown open. No buttons: it is
talk. If it should be runnable, the agent should post a proposal.

**proposal.** A command the agent thinks you might run. **It never runs by itself**, and it
looks like what it is: a claim, carrying a mono command that is not yet a fact.

```
○ Proposed   Rotate logs now and restart the service
  │ $ logrotate -f /etc/logrotate.conf && systemctl restart api       ← mono on a dashed
  │   in /srv                                                           border-edge box
  Run on prod-api…   Decline…   Copy
```

- A **dashed** `border-dashed border-edge` box around the command. The dashes say it is not on
  the record yet. Solid plates are the record.
- `Run on {host}…` is `variant="secondary"`. Clicking opens the inline run check: section 9's
  card, as a human-run variant, in place. It is never a direct run, even on `dev`. The human
  runs it with `Run on {host}` in that card.
- The client sends the `Revision` it drew. **If the revision moves while the run check is
  open**, the check closes and the card says `{Agent} changed this proposal. Read it again.`
  in `text-warn`. A server refusal for the same reason shows the same sentence.
- After `accepted`: `You ran this` plus a link to the block plate. After `declined`:
  `You declined this` plus your reason in your voice. The command remains, struck through.
- `Copy` is for pasting into your own terminal, which is the route for editing it first.

**question.** The agent asks you. Lead word `Asks`. Choices are `Button variant="secondary"
size="sm"`, plus a text field for free answers. The revision is pinned in the same way.

```
○ Asks   Should I keep digging, or do you want to look first?
  [Keep going]  [Wait for me]   Answer in your own words…
```

- Answered: the buttons go, and your answer sits below in your voice with its delivery state.
- An open question or undecided proposal adds to the row's `OpenItems` as a quiet number and
  **does not raise attention**. In the stream the lead word reads `Asks` or `Proposed` until
  you answer, and the Brief's Now row is unaffected. An agent's question can be ignored, by
  design.

### 8.2 Evidence plates: claim to fact in one click

```
[b12 exit 3 · 0.4s]
```

A tiny plate, `font-mono text-sm bg-panel border border-edge/60 rounded px-1.5`. It reads the
**live block** when it is in the store, else the facts copied into `Evidence`. Non-zero exit in
`text-bad`. Hover highlights the block's row with `border-accent`. Click scrolls to the block,
expands it, flashes its border over 200ms and pushes the Back pill. Tooltip: `Lola recorded
this command. That does not mean {Agent}'s reading of it is correct.`

| Case | Plate |
|---|---|
| `Verified`, block present | `b12 exit 3 · 0.4s` |
| Block pending or running | `b12 running` |
| `Verified` false, or pruned from the store | `b12 not on record` in `text-warn`, not clickable, tooltip `Lola has no record of this block. It may have been removed to stay within the limit.` |

---

## 9. The dock: go and approval cards

These are the moments trust is spent. They are the one place the UI is allowed to be loud, and
it still has only one solid control.

The **dock** sits between the stream and the composer. It can't scroll away. It holds one card:
authentication first (section 12), then the oldest approval, then a go. With two or more
pending, its header shows `1 of 3`, and answering advances to the next. `role="alert"` when it
appears. It **never takes focus by appearing**, so a keystroke meant for the terminal or the
composer cannot land on it. An answered card unmounts and focus goes with it, so the next card
needs its own explicit focus, and a held key cannot walk the queue.

**It always fits.** The stream column's rows are `auto minmax(72px,1fr) minmax(0,auto) auto`, Brief,
stream, dock, composer (`.ssh-col` in `app.css`). The composer keeps its row, and the dock takes what is
left, up to 60% of the column. In the default list layout that column is only a few hundred pixels:

| Column height | With no card | With a card |
|---|---|---|
| Over 520px | Full Brief and legend | Brief drops to its Now row, legend hidden |
| Up to 520px | Brief is its summary line and Now row, legend hidden | Same as above |
| Up to 420px | Same | A go or approval also takes the stream's row, and the card reads title, command, then the rest |

A card has **one scroller**: its body. The decision is a **pinned footer** below it, so a long command
can never push Deny and Confirm off the card.

Across all cards:

- The 2px tier band on top. `border border-orange/40 bg-panel rounded-md`.
- **Arming.** The confirm button is disabled for 800ms after a card appears, and again whenever
  the shown command changes, so nothing clicks or keys through a reflex. No spinner: it fades in.
- **Reading.** An approval or a proposal's run check also holds confirm until the end of the command
  and the warnings have been on screen, with `Scroll to the end to run this` beside the button. This is
  the TUI's rule for a long command: a dangerous last line must have been seen. Arrow keys and PgUp/PgDn
  scroll the body from the focused card.
- `Y` and `N` ignore `KeyboardEvent.repeat`.
- Every field below is drawn **from the block and connection**. Nothing is a summary of them.

### 9.1 Approval card

```
═══════════════ band: bg-bad/70 ═══════════════════════════════════════════════
 ● Claude wants to run a command                                         1 of 3
 Host       prod-api  [PRODUCTION]
 Connects   deploy@10.0.1.4 (alias prod-api)
 Runs in    /srv/api                           Timeout  2m

 ○ Claude says: service is wedged after the disk filled up

 Everything the agent reads here is sent to its model provider.
 Lola does not filter commands. The remote user's permissions are the real limit.
 Production asks for every command. Expires in 29 min.
 ┌───────────────────────────────────────────────────────────────────────────┐
 │ systemctl restart api                                                     │
 └───────────────────────────────────────────────────────────────────────────┘
 ───────────────────────────────────────────────────────── pinned, never scrolls
 Deny…                                                    Run once on prod-api
```

| Element | Rule |
|---|---|
| Title | `{Agent} wants to run a command`, in `font-medium text-orange` with a solid `bg-orange` dot |
| Command | **Whole, never truncated.** `font-mono whitespace-pre-wrap`, `overflow-wrap: anywhere`, `bg-canvas`, `selectable`. No scroller of its own: it scrolls with the card's body, so its tail cannot hide behind a second one. Over 12 lines the footer hint reads `{n} lines. Scroll to the end to run this` |
| Facts | `Connects` spans the row, because the exact identity is what is being read here. `Runs in` reads `login directory` when empty. `Connects` from resolved fields, else configured fields, else `Lola could not read this from your ssh config.` in `text-warn` |
| Why | **Outside** the fact rows, in the agent's voice: hollow ring plus `{Agent} says: {why}` |
| Warnings | `text-sm text-ink/70` (faint is under 4.5:1 in Latte), above the command, one per line: `SSHDataWarning`, `SSHUserIsTheLimit`, plus `SSHUnsandboxedNote` in `text-warn` for an unsandboxed agent |
| Tier line | production: `Production asks for every command.` Staging and dev with `GrantMaxMinutes > 0`: the grant control. Otherwise: `This host asks for every command.` |
| Expiry | `Expires in 29 min`, whole minutes, so a screen with several pending rows holds still. Seconds only under 5 minutes, with `text-warn` |
| Deny | `Button variant="danger" size="md"` `Deny…`, recoloured `text-ink!` at rest so it does not look disabled, red on hover. Opens a reason field in place |
| Confirm | `Button variant="primary" size="md"` `Run once on {host}`. Naming the host is the point: you read what you approve |

**Deny flow.** A one-line field appears with `Tell {Agent} why (optional)` as placeholder.
`↩` denies, Esc cancels, buttons `Back` and `Deny`. The block reads `Denied`, and your reason
sits on it in your voice. Reply text: `{Agent} gets your reason with its next command.`

**Grant control**, staging and dev only:

```
 ☐ Also let {Agent} run anything on stage-api without asking for  [ 15 min ▾ ]
    A grant is not limited to this command. It ends if you take the wheel.
```

Uses `Checkbox` and `Select` (5, 15, 30, 60, 120, up to `GrantMaxMinutes`). Off by default,
every time. On, the button reads `Run and allow for 15 min`. The header shows the countdown
once it starts, and the block carries the `grant` tag. Production never renders this control.

**After the answer.** The dock advances or closes. The block row moves on to `Queued` or
`Running`. There is no toast: the row is the feedback.

**If the world moves under the card:** taking the wheel or a dropped connection refuses the
block, and the card goes with it. If you had pressed confirm in the same moment and the daemon
refuses: flash `That command was already refused: {StatusDetail}`. Only a block already
decided or refused reads that way. Any other error, such as a grant the host will not
give, is shown in the daemon's own words.

### 9.2 Go card (a running session asked)

```
═══════════════ band ══════════════════════════════════════════════════════════
 ● Claude asks to connect
 Host       prod-api  [PRODUCTION]
 Connects   deploy@10.0.1.4 (alias prod-api)
 Session    Fix checkout timeout

 ○ Claude says why: checkout errors point at the API host; I need its logs
 ○ Claude says it will: read service logs and disk usage

 Opens a terminal for you to sign in. Nothing connects until you press this.
 Each command will still ask for approval.
 Everything the agent reads here is sent to its model provider.

 Deny…                                              Open terminal and connect
```

The second sentence becomes `Each command will ask, unless you open a grant.` where grants are
allowed. Pressing go enters `Connecting`, shows the terminal, and moves focus into it, because
the next thing that happens is the human's to answer.

A request needs no expiry and shows none: a request nobody has seen is not a decision.

### 9.3 Proposal run check

The same card, in place in the stream rather than in the dock, because the human asked for it.
Title `Run this yourself on {host}?`, no `Why` line (the proposal's text sits right above), no
arming skip: arming still applies. Confirm: `Run on {host}`. Add a line: `You are running this,
not {Agent}. It is recorded as yours.` Opening it scrolls the card into view and focuses it, so its
keys work at once, and cancelling returns focus to `Run on {host}…`.

---

## 10. The wheel

**Who may run commands is a state, not an action.** A two-position segmented control, in the
header, on every connection:

```
At the wheel  [ Agent | You ]
```

`Button size="xs"` pair inside a `role="radiogroup"`, `selected` on the current. `Agent` reads
in `text-info` when selected, `You` in `text-accent-ink`. Tooltips: `{Agent} may run commands,
subject to approval.` / `You are driving. {Agent} cannot run commands.`

The terminal is yours in **both** positions. The tooltip and the hint in section 3.2 say so.

### 10.1 Taking it

One action, no confirmation, instant. Click `You`, `W`, or `⌘⇧L` (which works from inside the
terminal). Taking control is the safe direction, and a dialog would be a delay at the exact
moment someone wants to stop something.

Within 200ms, in this order, all of them visible:

1. `You` selects, and a running `m:ss` appears beside it.
2. The terminal pane's border goes to `border-accent` 2px, and it grows to 62% and takes focus.
3. The rail turns accent from this point on, with a system row: `You took the wheel.`
4. Pending commands' cards leave the dock. Each row reads `Did not run · You took the wheel.`
   A live grant ends, and the header's countdown goes with it.
5. The **driving bar** replaces the composer's state line (section 11):

```
 You have the wheel.  Commands you run in the terminal are not recorded.  Hand back…
```

(`text-accent-ink`, and `Hand back…` is `Button variant="secondary" size="sm"`.) The second
sentence is `SSHDrivingNote`.

### 10.2 What is still running

If commands are in flight, taking the wheel does not stop them. The driving bar adds one line per
running command, each one visible and stoppable:

```
 1 command is still running:  journalctl -fu api · 41s    Stop
 Stopping ends the local ssh. The remote process may keep running.
```

Stop is not offered as a checkbox at takeover, because that would add a decision to a moment that
needs one action. The CLI's `lola ssh take --stop` is there for scripts.

### 10.3 While you drive

- The agent is told plainly: any `lola ssh run` returns `human_has_control`. The stream shows it as
  a muted refused row, because you should know it tried: `Did not run · You had the wheel.`
- `lola ssh post` still works. The agent can talk while you drive, and you can talk to it.
- Its proposals can still be run by you. That is a human acting.

### 10.4 Handing back

Click `Agent`, `W` or `⌘⇧L`. It opens the **handback panel**, which replaces the composer in place,
and focus goes to the note field. Not a modal: you need the terminal and the stream in view while
you decide what to say.

```
 HAND BACK TO CLAUDE
 ┌───────────────────────────────────────────────────────────────────────────┐
 │ Say what you did or what to do next (optional)                            │
 └───────────────────────────────────────────────────────────────────────────┘
 Your terminal since 14:02                                         4.1 KB of 8 KB
 ┌───────────────────────────────────────────────────────────────────────────┐
 │ deploy@prod-api:~$ logrotate -f /etc/logrotate.conf                       │
 │ deploy@prod-api:~$ df -h /var                                             │
 │ /dev/sda1  50G  31G  17G  65% /var                                        │
 │ …                                                                         │
 └───────────────────────────────────────────────────────────────────────────┘
 This text goes to the agent and its model provider. Remove anything you would not paste there.

 Keep driving           Hand back without it          Hand back with this text
```

- The transcript is **shown by default**, from `sshHandbackPreview`, in an editable `<textarea>`,
  `font-mono text-sm`, 8 rows. While loading: `Reading your terminal…` plus two skeleton lines.
- **Two equal buttons, both `secondary`, no primary and no default.** Whether what you did goes to a
  model provider is an explicit choice each time, and it is why this is not a checkbox with a
  remembered state. `↩` in the note field does nothing, and `⌘↩` is not bound, so no key can
  choose for you.
- `Truncated`: `Your terminal history did not go back far enough. This is what is left, and some of
  your session may be missing.` Over the cap: `Only the last 8 KB can be sent.`
- Empty transcript text: the "with" button is disabled, reading `Nothing to send`.
- Preview fails: `Could not read your terminal. You can still hand back with a note.` The textarea
  stays, empty and editable, so a human may type what they did by hand.
- `Keep driving`, Esc: closes the panel. Nothing changes.
- Nothing is captured until this panel opens, and what is sent is the textarea's text, never a
  fresh capture.

### 10.5 After handing back

`You` deselects, the terminal border returns to `border-edge/60`, and the split returns. The accent
rail stops, and the interval labels itself on the rail's side:

```
 ┃  You drove for 12m. Commands you ran in the terminal are not recorded.
 ┃  ▪ Rotated logs by hand, disk is at 65%.                                You
 ┃     Your terminal since 14:02 · 4.1 KB ▸                     Delivered
```

The transcript folds behind `▸`, and it shows exactly what went, since `SSHHumanMessage.Transcript`
keeps it. The human's note is an ordinary "You" item, with the same queued state as anything said
in the composer (section 11). Handing back never restores a grant, and the card says so the next
time: no special copy, the dock simply asks again.

### 10.6 Sharing the terminal without the wheel

Typing in `Shell` with the wheel on `agent` is allowed and not observed. Both of you are then
working the host at once, by your choice. The hint in section 3.2 is all the UI says.

---

## 11. The composer

Pinned at the bottom of the stream. The state line above it tells the truth about what will happen
to what you say, and it is **derived from facts**: `agentState`, `inputReason`, the wheel.

```
 Claude is working. It gets your message at its next command.
 ┌───────────────────────────────────────────────────────────────────────────┐
 │ Talk to Claude…                                                         ↩ │
 └───────────────────────────────────────────────────────────────────────────┘
```

| Agent state | State line (`text-sm text-faint`) | Placeholder |
|---|---|---|
| `working`, `starting` | `{Agent} is working. It gets your message at its next command.` + `LivePulse` | `Talk to {Agent}…` |
| `idle`, or `waiting_input` parked on its idle nudge | `{Agent} is waiting.` | `Tell {Agent} what to do…` |
| `waiting_input`, permission or dialog | `{Agent} is waiting on a prompt in its own terminal. It will get this after that.` + `Show` (`text-orange`) | `Talk to {Agent}…` |
| `exited`, `dead` | `{Agent} has exited. Your message waits until it is back.` + `Revive` | `Talk to {Agent}…` |
| Direct session starting | `{Agent} is starting.` | `Talk to {Agent}…` |
| Wheel `human` | The driving bar replaces this line (section 10.1) | `Talk to {Agent}. It cannot run commands while you drive.` |

**Keys.** `↩` sends. `⇧↩` inserts a line, matching the app's rule for terminals. Esc leaves the
field and keeps the draft. It grows to 6 lines, then scrolls. Empty, send is disabled. Drafts persist
per session in the store. Counter from 80% of `SSHMaxSayBytes`: `{n} KB of 8 KB`, `text-warn`.

**It never interrupts.** Sending while the agent is mid-turn does not touch its pane (see the send-keys
invariant). If you need it to stop right now, that is what the wheel is for, or `Show` to reach its
terminal directly.

**What you sent.** It appears immediately as a "You" item, with its delivery state beneath it:

| State | Under the message (`text-sm text-faint`) |
|---|---|
| Queued | `Queued` + `Recall` (`Button size="xs"`) |
| Delivered between commands (`Via = reply`) | `Delivered with its last command · 14:03` |
| Delivered at the prompt (`Via = prompt`) | `Delivered at its prompt · 14:03` |
| Recalled | The text `line-through text-faint`, `Recalled` |
| Recall lost the race | Flash: `Too late: {Agent} already has this.` |
| Agent gone | `Queued. {Agent} has exited.` + `Recall` |

The word is "Delivered". It never says "Read" or "Seen": lola cannot know that.

**Where the answer appears.** The briefing asks the agent to answer with `narration`, so its reply
lands right under your message, in its voice. An agent may still answer only in its own terminal. If a
message has been delivered for 60s with no new element from the agent, and its axis has gone idle, one
muted line appears below it:

> {Agent} may have answered in its own terminal. Show

---

## 12. Every state, with copy

Header state: a `h-1.5 w-1.5` dot plus a word. The dock, the terminal pane and the stream each say
their part. Nothing is left blank or spinning.

| State | Header | Dock or stream | Terminal pane | Composer |
|---|---|---|---|---|
| **Requested** | `○ Waiting for your go` `text-orange` | Go card | Placeholder: `No connection yet. Nothing has been attempted.` | Normal |
| **Denied** | `○ Denied` `text-faint` | System row, plus your reason | Hidden. Column goes to full width | Normal |
| **Connecting** (first 4s) | `● Connecting…` pulse, `text-info` | System row: `You gave the go. Opening a terminal.` | Live terminal, focused | Normal |
| **Authenticating** | `● Sign in using the terminal` `text-orange`, `Gives up in 4:12` | Auth card, below | Live terminal, 2px `border-orange` | Normal |
| **Connected** | `● Connected 12m` `text-good` dot, word `text-ink` | none | Live | Normal |
| **Connected, shell closed** (`TabAlive` false) | `● Connected` plus `Shell closed` `text-faint` | none | Placeholder, below | Normal |
| **Waiting for approval** | Unchanged. Tab and row carry orange | Approval card | Live | Normal |
| **Running** | Unchanged | Plate, section 7, and the Now row | Live | Normal |
| **Long output** | Unchanged | Section 7.3 | Live | Normal |
| **Human driving** | Wheel `You`, plus `m:ss` | Rail accent, in-flight lines | `border-accent` 2px | Driving bar |
| **Handed back, queued** | Wheel `Agent` | "You" item `Queued` | Live | Normal |
| **Disconnected** (network) | `○ Disconnected` `text-warn` | Reconnect card, below. Rail dashed | Tab shows ssh's last words, `Reconnect` overlay | Normal |
| **Disconnected** (idle) | `○ Idle, connection closed` `text-faint` | Reconnect card, no alarm | Same | Normal |
| **Failed** | `○ Could not connect` `text-bad` | Failed card, below | The dead tab's output, `remain-on-exit` | Normal |
| **Closed** | `○ Closed` `text-faint` | System row, Ended bar | Hidden | Ended bar |
| **Agent gone** | Unchanged | Now row says it | Unchanged, and yours | Line says it |

### Cards and placeholders

**Auth card** (dock, `border-orange/40`), during `authenticating`:

> **Sign in using the terminal.** Host key, passphrase and MFA happen there. {Agent} never sees them,
> and neither does Lola.
> Gives up in 4:12.

No button. The terminal is the action, and clicking the card focuses it.

**Connected, shell closed** (terminal pane placeholder):

> Your shell on prod-api ended. The connection is still up, and {Agent} can keep working.
> It closes by itself after 15 minutes with no client.
> [Open a new shell]

(`15 minutes` from `IdleLingerSeconds`.)

**Disconnected card:**

> **Connection to prod-api dropped.** {n} commands were running. Their results are unknown, and they
> may still be running on the host.
> Reconnecting opens a terminal, and may ask for MFA again.
> [Reconnect]   [Close connection]

Idle variant: `The connection closed after 15 minutes with nothing using it.` with no alarm colour.
`SSHDataWarning` also goes on Reconnect, since that is a go.

**Failed card:**

> **Could not connect to prod-api.** ssh's message is in the terminal.
> [Try again]   [Close connection]

Timed-out variant (`StateDetail` says so): `Nobody finished signing in within 5 minutes.`
`ssh` missing: `ssh was not found on this Mac. Lola needs it to connect.`

**Closed, the Ended bar**, replacing the composer:

> Connection closed. The record of what ran stays here while this session exists.
> The remote process may keep running if any were still going. *(only when commands ran)*
> Direct: `[New session on prod-api]  [End session…]`. From a running session: `[Back to Agent]`.

**Close connection** uses the `confirm` store:

| Field | Copy |
|---|---|
| Title | `Close the connection to {host}?` |
| Body | `This ends your shell and closes the connection. {Agent} will not be able to run commands here.` |
| Detail | `{n} commands are still running. Stopping ends the local ssh. The remote process may keep running.` Else `SSHRemoteOutlives` when `CommandsRun > 0`, nothing otherwise |
| Confirm | `Close connection` |

**End a direct session**, through the existing kill dialog, with one added detail: `The command record
stays on disk for 14 days in ~/.lola/state/ssh/.`

### Refusal copy (block `StatusDetail`, daemon-side)

| Code | Row text |
|---|---|
| `human_has_control` | `You had the wheel.` |
| `not_connected` | `The connection was not up.` |
| `awaiting_go` | `Still waiting for your go.` |
| `too_many_queued` | `Too many commands were already waiting.` |

### Stream empties and errors

| State | Copy |
|---|---|
| Loading first fetch | 3 skeleton plates, `bg-sel/40`, no spinner |
| Connected, nothing yet, agent present | `Connected. {Agent} has not run anything yet.` |
| Connected, nothing yet, started with no brief | `Connected. {Agent} is waiting for you to tell it what to look at.` |
| You started at the wheel | `You have the wheel. {Agent} is waiting until you hand it back or ask it something.` |
| Filter empty, Facts | `No commands yet.` |
| Filter empty, Findings | `{Agent} has not posted any findings or theories yet.` |
| Fetch failed | `Could not load this timeline. Lola keeps recording it.` + `Try again` |
| Daemon predates the command | `This daemon predates SSH sessions. Restart it to use them.` + the existing restart action |
| Daemon down | The app's standing banner, plus stream content stays readable and all actions disable |

---

## 13. Starting a session on a host

**Entry points.** Session menu `New SSH Session…` (`⌘⇧H`). Command palette (`⌘K`), which also lists
each host directly as `SSH to {host}`. Sidebar `SSH sessions` section's `+`. A `New session on
{host}` button on an Ended bar. `lola ssh open <host>`.

A `Modal`, `NewSSHSession.svelte`, using `<Button>`, `<Select>`, `<Checkbox>` and the `field` input
class from `NewSession`. No native controls.

```
┌ New SSH session ─────────────────────────────────────────────────────────── ✕ ┐
│ Host                                                                          │
│ ● [PRODUCTION] prod-api    deploy@prod-api            Agent runs in a container│
│ ○ [STAGING]    stage-api   ops@stage-api              Agent runs on this Mac   │
│ ○ [DEV]        sandbox-1   martin@sandbox-1           Agent runs on this Mac   │
│ ○ [PRODUCTION] billing-db  (ssh config user)          Cannot start          ⓘ │
│                                                                               │
│ What should Claude look at?                                                   │
│ ┌───────────────────────────────────────────────────────────────────────────┐ │
│ │ API restarts every 40 minutes since this morning's deploy…                │ │
│ └───────────────────────────────────────────────────────────────────────────┘ │
│ Optional. Without it, Claude waits for you.                                   │
│                                                                               │
│ Who drives first   [ Claude | You ]                                           │
│ Agent              [ Claude (host default) ▾ ]      Name   [ optional       ] │
│                                                                               │
│ ─────────────────────────────────────────────────────────────────────────     │
│ Connecting opens a terminal for you to sign in.                               │
│ Production asks you to approve every command.                                 │
│ Everything the agent reads here is sent to its model provider.                │
│                                                                               │
│ Cancel                                                    Connect to prod-api │
└───────────────────────────────────────────────────────────────────────────────┘
```

- **Host** is a radio list, not a `Select`, because tier has to be visible before a pick. Arrow keys
  move, and the first usable host is selected unless a host was named on the way in. Order follows
  config.
- The right column reads `DirectSandbox`: `Agent runs in a container` or `Agent runs on this Mac`, the
  latter `text-warn` on production. A refused host is disabled, reads `Cannot start` and shows
  `DirectRefused` under its row while focused, whole, because it names the ways out.
- **Already open.** A host with a live session in `store.sessions` reads `Open in {title} · on this Mac`
  (or `· in a container`), keeping `text-warn` on production, and the footer gains a `secondary` `Go to that session`. Starting another is
  still allowed.
- **Brief** takes focus on open. `⌘↩` submits, and `↩` inserts a line since this is prose.
- **Who drives first** defaults to the agent. `You` changes the bottom line to `You start at the wheel.
  Claude can read what you tell it and cannot run commands until you hand back.`
- **Footer.** `SSHDataWarning` is always there. The tier line reflects the host: `Production asks you
  to approve every command.` / `Each command asks for your approval, unless you open a grant.`. For an
  unsandboxed agent, `SSHUnsandboxedNote` in `text-warn`.
- **Confirm:** `Button variant="primary" size="md"`, `Connect to {host}`, with `loading` while
  `sshOpen` runs. Errors in `text-sm text-bad role="alert"`, and the form stays filled.
- On success: close, select the new session, open `SSH`, focus the terminal. The header reads
  `Connecting…`.

**No hosts:**

> **No hosts yet.** Add one in Settings, using an alias from your ssh config. Lola holds no address and
> no key.
> [Open SSH host settings]

---

## 14. Host settings

`SettingsForm` gains a tab `SSH hosts` in the `Connections` group, between `Notifications` and `Phone
access`. A list above a form, the shape of the review providers editor.

```
SSH hosts
Hosts an agent can ask to reach. Lola holds no address and no key: `alias` is resolved by your own
~/.ssh/config, and you sign in yourself in a real terminal.

[PRODUCTION] prod-api    deploy@prod-api    Direct: container     ▸
[STAGING]    stage-api   ops@stage-api      Direct: this Mac      ▸
+ Add host
```

**The form:**

| Field | Control | Help text |
|---|---|---|
| SSH alias | Text, autofocus, required | `A Host from your ~/.ssh/config.` |
| What it means | Read-only, live from `sshResolve` on blur and on `user` change | `Connects to deploy@10.0.1.4:22 through bastion.` Failure: `ssh could not read this alias: {Error}` in `text-warn`, which does not block saving. Beneath, `Read from your ssh config. Nothing connected.` |
| Label | Text | `Shown in Lola. Defaults to the name.` |
| Name | Text, derived from alias, editable | `Agents and the CLI use this. Letters, digits, dots, dashes.` |
| Remote user | Text | `A restricted user is the real limit on what a command can do. Lola does not filter commands.` Empty, in `text-warn` on production: `Without a restricted user, commands run as whoever your ssh config connects as.` |
| Tier | `Button` segmented: Production / Staging / Dev | Production: `Every command asks. No grants. Refuses agents that are not sandboxed.` Staging: `Every command asks, unless you open a grant, up to 30 minutes.` Dev: the same, 2 hours |
| Direct session agent runs | `Select`: Tier default / Container / On this Mac | Reads what the choice resolves to right now, from `SSHDirectSandbox`, in a line beneath, e.g. `In a container, the default on production.` |
| Allow unsandboxed agents | `Checkbox`, production only | `Lets an agent that runs as you work here. It could read your keys or talk to Lola directly, so the approval gate would stop accidents and well-behaved agents, not a determined one. Leave off unless you accept that for this host.` In `text-warn` when on |
| Agent | `Select`: Default / Claude / Codex / OpenCode | |
| Default directory | Text | `Absolute path on the host. Commands that name none run here.` |

**Advanced**, behind `Disclosure label="Limits"`, with numeric fields whose placeholder is the
effective default: longest grant (minutes; hidden on production), command timeout, longest command
timeout, output kept per stream, idle linger, sign-in wait, approval wait. Each with one line of help
and a unit suffix.

**Footer.** The form's usual save. `Remove host` as `variant="danger"`, through `confirm`: title
`Remove {host}?`, body `Its live connections close, and agents can no longer ask for it.`, detail
`SSHRemoteOutlives` when any command ran. Config validation errors appear in place, in the form's
existing error treatment. Lola never connects from this screen, and it says so once, above.

---

## 15. In the session list

The status pill stays the agent axis, and `PrBadge` stays the delivery axis. SSH adds a third chip,
`SSHChip.svelte`, beside them. It appears wherever `PrBadge` does: table, kanban card, grid tile.

```
◐  Investigate checkout     PRODUCTION prod-api · 2 to approve       ○ Disk fills every 40 min…
```

The chip is the `label`-styled tier word abbreviated (`PROD`, `STG`, `DEV`), the host, then the state
word, in the first of these that applies:

| Fact | Words | Colour |
|---|---|---|
| Authenticating | `sign in` | `text-orange` |
| `PendingApprovals > 0` | `{n} to approve` | `text-orange` |
| Requested | `wants to connect` | `text-orange` |
| Wheel `human` | `you're driving` | `text-accent-ink` |
| Live grant | `grant m:ss` | `text-warn` |
| Connecting | `connecting…` | `text-faint` |
| Disconnected | `disconnected` | `text-warn` |
| Failed | `could not connect` | `text-bad` |
| Connected, running | `running` | `text-info` |
| Connected | `connected` | `text-faint` |
| Closed, Denied | No chip. A session whose every connection ended reads as an ordinary session |

Over several connections: the most urgent, plus `+1`.

- **Row activity line.** `RunningCommand` in `font-mono text-sm text-faint` while running, else
  `Headline` behind a hollow ring in `text-sm text-faint`. A plate is not needed at this size: the
  ring and the mono are the same two voices. `OpenItems` adds a quiet `· 1 question`.
- **Hover.** A `HoverCard` on the chip: `NeedsHumanWhy`, then `NextApproval`'s exact command in mono
  and its host and tier, then `Review`. Approval does not happen here. The card's rules need the whole
  dock.
- **Attention.** `SessionInfo.ssh.needsHuman` ORs into every attention surface: `SidebarTriage`, the
  needs-you filter, `n`/`N`. It does not move the kanban column, because that remains a function of
  the two axes. A jump (`n`, click) lands on the workspace with the card in view.
- **Sidebar.** A new `SSH sessions` section beside `Temporary sessions`, the same shape, listing
  `location === "ssh"` sessions, with the tier word in the row and the orange dot when needed. `+`
  opens the new-session modal. The section shows only when `[[ssh_host]]` exists.
- **Notifications** are the daemon's: one for a go, one per approval, one for a drop mid-command. Their
  text is lola's, plus a clipped command. Clicking one opens the workspace.
- **Context menu**, `SessionMenu`: `Open SSH view`, `Take the wheel` or `Hand back…`, `Close connection…`
  when the session has one, and `New SSH session on this host` on a direct session.

---

## 16. Keyboard

Follows the app's rule: **bare keys are the frontend's, ⌘ chords are the menu's, never both.** List
every one in `lib/views/HelpOverlay.svelte`, under a new `SSH` group.

### 16.1 Menu chords, in the Session menu

These work from inside a live terminal, because AppKit dispatches them before the web view.

| Chord | Item | Does |
|---|---|---|
| `⌘⇧H` | New SSH Session… | Section 13 |
| `⌘⇧L` | Take or Return the Wheel | Takes at once. If you already hold it, opens the handback panel |
| `⌘⇧A` | Review Next Approval | Goes to the session and card that has waited longest, and focuses that card |
| `⌘L` | Talk to Agent | Focuses the composer |
| `⌘\` | Show or Hide Terminal | Section 3.1 |

The menu has no chord for approving. See section 16.3.

### 16.2 Bare keys, selection level

Active when no field or terminal has focus. Added to `App.svelte`'s `onKey`, under `isChord`.

| Key | Does |
|---|---|
| `h` | Open or leave the SSH tab of the selection |
| `i` | Focus the composer |
| `t` | Move focus into the stream |
| `W` | Take the wheel, or open the handback panel (shifted, like `D`: it refuses pending commands) |
| `A` | Same as `⌘⇧A` |
| `F` | Cycle the filter |
| `/` | Find in timeline (SSH tab only) |
| `Enter` | Existing behaviour: focus the terminal |

### 16.3 Inside the stream

The stream owns the keyboard while focused, like a terminal does: **every bare key not listed is
swallowed**, so `x` cannot ask to kill a session from inside it. Esc returns focus to the app.

| Key | Does |
|---|---|
| `j` `k` `↑` `↓` | Move between items |
| `Enter` `→` | Expand, or follow an evidence plate. `←` collapses |
| `Tab` in an expanded block | Moves between its actions and tabs |
| `s` | Switch between stdout and stderr in the focused block |
| `a` | Ask about this: fills the composer |
| `X` | Stop the focused running block |
| `Y` | **Confirm the focused card, once armed:** run the command, or give the go |
| `N` | Deny the focused card, opening its reason field |

`W`, `A` and `?` pass through to the app, so one-key Drive still works with the stream focused.

**Approval has no global key at all.** It needs a card holding focus, which only `⌘⇧A`, `A`, `t` or a
click can give it, plus 800ms of arming. A terminal holding the keyboard can never approve anything,
which is the exact accident to prevent. `Y` is shifted like every other heavy key. Enter does nothing
on a card, and the confirm button is never autofocused.

---

## 17. The TUI

Same model and same words, within what a terminal can draw. `internal/tui/ssh*.go`, palette-derived
styles registered in `rebuildStyles()`.

**The three voices** survive without fonts, by marker, indent and a band:

| Voice | Treatment |
|---|---|
| Lola | Marker on the rail, `$ ` then the text, on a `colSel` background band across the full line, in `colText` |
| Agent | `○` plus 2 more spaces of indent, no band, `faintText`-to-`colText`. Never a `✓` |
| You | `▌` in `colAccent`, then text, no band |

### 17.1 The cockpit

- **Row.** The list's status column stays. A chip beside it, in the existing cell style: `PROD prod-api
  2↑` (orange for needs-human), `you` in accent while driving. `NeedsHuman` ORs into `!` and `n`/`N`.
- **Detail card** gains a block: `ssh:  PROD prod-api · connected 12m · wheel agent`, then the headline
  behind `○`, then the running command, then `H workspace · 2 to approve`.
- **Keybar:** `H ssh` when the selection has a connection, in orange with the count when it needs you.
  `H new ssh` when it has none and hosts are configured. `H` opens the host picker from the projects
  home screen too, because a new session names a host, not a project.
- **Embedded terminal tabs:** `-ssh-N` is a tab here, unlike the app, labelled `ssh` via `sshtab`,
  because the TUI has one attach at a time and no workspace column to hold it.

### 17.2 The workspace overlay

`H` opens a full-screen overlay, the same shape as the diff overlay (`f`). On a session with no
connection, `H` opens the host picker for a new direct session instead.

```
 PRODUCTION  prod-api  deploy@10.0.1.4   ● connected 12m   wheel AGENT   grant —             ✕ esc
 ──────────────────────────────────────────────────────────────────────────────────────────────────
 Claude's account  (claims)
   ○ Disk on /var fills every 40 minutes. Log rotation stopped after the 2 Nov deploy.   2m
   ○ plan 2/5 ▰▰▱▱▱ Checking log rotation config     theories 1✱ 2?     findings 1 high 2 medium
 ▪ running  df -h /var · 4s
 ──────────────────────────────────────────────────────────────────────────────────────────────────
 │ ○ I'll check whether the disk is full.
 │
 ✓ $ df -h /var                                                       exit 0 · 0.3s          14:01
 │     ○ Claude: check free space on the log volume
 │
 ✗ $ systemctl status api                                             exit 3 · 0.4s · 2 KB   14:02
 │   Active: failed (Result: exit-code) since Wed 13:58:11 UTC
 │     ○ Claude: see whether the service restarted
 │
 │ ○ High  Log rotation has not run since 2 Nov.     [b12 exit 3]  [b14 exit 0]
 │ ▌Look at the log rotation too.                                             delivered
 ──────────────────────────────────────────────────────────────────────────────────────────────────
 ⚠ Claude wants to run a command on prod-api  PRODUCTION                                     1 of 3
   $ systemctl restart api
   in /srv/api · as deploy@10.0.1.4 · timeout 2m
   ○ Claude says: service is wedged after the disk filled up
   Everything the agent reads here is sent to its model provider.
   Lola does not filter commands. The remote user's permissions are the real limit.
   Y run once on prod-api · N deny · expires in 29 min
 ──────────────────────────────────────────────────────────────────────────────────────────────────
 i talk · j/k move · enter open · W wheel · T terminal · F filter · X stop · C close · ? help
```

- Every line is bounded to the width (`previewLine`), per the "a wrapped row smears the repaint" rule.
  The one exception is an approval's command, which **wraps whole** with a `│` continuation, and past 8
  lines says `… {n} more lines: enter to read all`, with `Y` disabled until that view has been opened.
- The approval band takes as many lines as it needs, and the stream gets what is left. `Y` is ignored
  for 800ms after a card appears, and the hint shows `Y` in `faintText` until armed.
- `y` is not accepted. `Y` is. `N` denies, opening a one-line reason prompt.

**Keys in the overlay** (scoped, esc closes):

| Key | Does |
|---|---|
| `j` `k` `g` `G` | Move |
| `enter` | Expand a block: facts, then a pager of its output |
| `tab` | stdout / stderr, in the pager |
| `Y` / `N` | Confirm / deny the card in the band: run once, give the go, reconnect, try again. The band names what `Y` does |
| `W` | Take the wheel at once. If you hold it, opens handback |
| `T` | Jump to the `ssh` terminal tab, focused (`^q` back, as everywhere) |
| `A` | The agent's terminal tab |
| `i` | Talk: a one-line prompt in the keybar (`esc` cancels, as the `a` answer prompt does) |
| `R` | Recall your last queued message |
| `P` / `d` on a proposal | Run check / decline. On a question: `1`–`6` picks a choice, `i` answers freely |
| `F` | Everything / Facts / Findings |
| `/` | Find |
| `X` | Stop the selected running block |
| `C` | Close the connection (y/n, naming what may survive) |
| `t` | On an approval, where the host allows one: cycle the grant. Off on every new card. Never on production |
| `V` | Revoke the live grant |
| `e` / `b` | From a claim, follow its evidence to the block, each press the next link / back to where you were |
| `a` | Ask about the selected block: fills the talk prompt with `About b12: ` |
| `o` | Open the first address in the row or on the pager's screen, through the daemon's `openURL` |
| `<` `>` | Switch connection, when a session holds two or more |
| `+` | Host picker, for a new session |

**Taking the wheel** in the TUI: the wheel goes `YOU`, in `colAccent` reverse, and focus jumps straight
into the `ssh` embed. A line above the keybar reads: `You have the wheel. Commands you run in the
terminal are not recorded. ^q, then W to hand back.` In-flight commands list above it, each `X` stoppable, plus
`SSHRemoteOutlives`.

**Handing back** is a full-screen form: a one-line note, then the transcript in a viewport:

```
 HAND BACK TO CLAUDE
 Note: rotated logs by hand, disk is at 65%_

 Your terminal since 14:02 · 4.1 KB of 8 KB                 j/k move · d delete line
 deploy@prod-api:~$ logrotate -f /etc/logrotate.conf
 deploy@prod-api:~$ df -h /var
 …
 This text goes to the agent and its model provider. Remove anything you would not paste there.

 Y hand back with this text · S hand back without it · esc keep driving
```

The form opens on the note, and `tab` moves to the transcript. `Y` and `S` only answer from the transcript,
so a `Y` typed into the note stays a letter. `enter` chooses nothing in either place.

`d` deletes a line, and that is the redaction tool. No `$EDITOR`, which would write the transcript to
disk. Both answers are shifted, neither is a default, and this form's `esc` only returns you to the
terminal.

**New session** in the TUI: the host picker from `H`, then a one-line brief, `tab` to the drive-first
toggle, `enter` connects. Same footer words. The picker offers no agent or name field: `lola ssh open --agent --name` has them, and the host's
`agent` key sets the default. **Host settings:** a new `SSH hosts` tab in `S`, with the
fields from section 14, sharing their help text. **Refused host:** listed, greyed, `DirectRefused` on
the footer when selected.

States use the app's words in section 12 throughout, in `warnText`, `badText`, `faintText`.

---

## 18. Accessibility

- The stream is `role="log" aria-label="SSH timeline"`, announcing only new claims and block outcomes
  in a short form (`b12 exit 3`), never output. A live region on the log itself would read every
  expanded output aloud, so the log is `aria-live="off"` and one visually hidden `aria-live="polite"`
  sibling carries the short form.
- A dock card is `role="alert"`, announcing the title, host and tier. Command text is read when focused.
- Voices have hidden labels: `Lola recorded`, `{Agent} says`, `You said`. A screen reader must get the
  same distinction a sighted reader gets from the type and the plate.
- The wheel is a `radiogroup`. Its accessible name reads `Who may run commands`.
- Colour is never alone: tier has a word, status has a shape and a word, and severity has a word.
- Contrast is the app's tokens on `canvas`, `panel` and `sel`. Latte reaches them through
  `toTokens`, and nothing here uses a literal colour. `/10` and `/5` tints only sit behind `text-ink`.
- Focus rings are the app's `:focus-visible`. Dock cards take a ring when focused by `⌘⇧A`.

---

## 19. Build notes

New components in `desktop/frontend/src/lib/components/ssh/`:

| Component | Holds |
|---|---|
| `SSHWorkspace.svelte` | Band, header, split, columns |
| `SSHHeader.svelte`, `SSHWheel.svelte`, `SSHTierTag.svelte` | Section 4, 10, 1.4 |
| `SSHBrief.svelte` | Section 5, pure over items |
| `SSHStream.svelte` | Rail, ordering, filter, folding, find, scroll pill, Back pill |
| `SSHBlock.svelte`, `SSHOutput.svelte` | Section 7 |
| `SSHElement.svelte` | Section 8, one component switching on kind, unknown kinds as a note |
| `SSHEvidence.svelte` | Section 8.2 |
| `SSHDock.svelte`, `SSHApproval.svelte`, `SSHGoCard.svelte`, `SSHConnCard.svelte` | Sections 9 and 12 |
| `SSHComposer.svelte`, `SSHHandback.svelte` | Sections 11 and 10.4 |
| `SSHChip.svelte` | Section 15, used by table, kanban and tiles |
| `NewSSHSession.svelte` | Section 13 |

Logic with no DOM goes in `desktop/frontend/src/lib/ssh.ts` and `ssh.svelte.ts`, and is what tests
drive: tier normalization (unset means production), fold grouping, brief derivation, the chip's
priority table, the composer's state line, the block status table, the bare block-id tokenizer, URL
tokenizing. Rules a test must pin:

- A block's `why`, and every element, never renders inside a plate. Assert on the DOM structure.
- A go or approval card disables confirm until armed, and re-arms on a new command.
- The grant control does not exist on production, whatever `GrantMaxMinutes` says.
- A `confirmed` hypothesis contains the words `Agent says` and no `text-good`.
- An evidence plate never contains the word `verified`.
- Unknown tier renders `PRODUCTION`. Unknown kind renders text. Unknown status renders words, never a
  false pass.
- `Run` on a proposal sends the drawn revision, and a moved revision closes the check.
- Handback offers two equal actions and sends a transcript only from the "with" one.
- Nothing renders agent or remote text through `{@html}`. Grep for it, as `Controls.test.ts` does for
  native controls.
- Bare `Y` does nothing unless a card holds focus. Bare `x` does nothing while the stream holds focus.

Respect the existing rules: every action is `<Button>`, every popover row is `<MenuItem>`,
`enabled:hover:`, recolour with a trailing `!`, every class literal, `confirm` for irreversible actions,
no native form controls, and Sentence-case labels. Verify layout in the packaged app, not only `wails3
dev`. **Never drive the UI with synthetic input:** component tests with `fireEvent`, and screenshots for
looks.

---

## 20. What this spec changed in the contract

The user's-seat critique of `docs/ssh-sessions.md`. Every item below is already applied there and in
`internal/protocol/ssh.go`.

| Gap, from the human's seat | Fix |
|---|---|
| **A proposal could change under the click.** `Merge` lets an agent rewrite an undecided proposal's command via `--ref`, so a human could read one command and run another | `SSHProposalArgs.Revision`, required for `run`, mismatch runs nothing. Same pin on `SSHAnswerArgs` |
| **Talking to a busy agent went nowhere.** Delivery waited for a resting prompt, which a long investigation may not reach for half an hour | `Inbox` on agent-side replies: delivered with its next command, with no keystroke in its pane. `Via` says which path won |
| **A sent message could not be taken back** | `sshRecall`, one timeline item per message, `PendingSSH` derived from the store |
| **The agent's reply had nowhere to appear.** It prints to its own pane, which the wire never sees | Briefing rule plus `narration` in `Usage`. Agent pane one click away, plus a hint if a reply seems to have gone there |
| **A direct session started with nothing to do** | `SSHOpenArgs.Brief`. With none, the agent is told to wait |
| **No way to look first, then ask** | `SSHOpenArgs.Wheel`, `lola ssh open --drive` |
| **Collapsed rows needed one output fetch each** | `SSHBlock.Preview` / `PreviewStream`, sizes and preview refreshed at most once a second |
| **A row could not show the exact pending command** | `SSHSessionInfo.NextApproval`, `RunningCommand` |
| **An approval card needed a second fetch to know whether grants exist, and had no expiry** | `SSHConnInfo.GrantMaxMinutes`, `SSHBlock.ApprovalExpiresAt` |
| **The cwd shown might not be the one used** | `Block.Cwd` is the effective directory |
| **MFA with an invisible clock, and an unstated idle limit** | `SSHConnInfo.ConnectDeadline`, `IdleLingerSeconds` |
| **Close could not say what may survive** | `SSHConnInfo.CommandsRun` |
| **Dead `-ssh-N` tabs could pile up, and one session could get two attachments** | One terminal per connection, a replace rule, and the app never shows the tab in the strip |
| **A dropped connection raised nothing, or would raise forever** | `disconnected` never raises `NeedsHuman`. One notification, only when commands were live |
| **Unstated urgency order** | Authentication, approval, go |
| **A handback item could not show what was sent** | `SSHHumanMessage.Origin`, `Transcript`, `Conn`, `DeliveredAt`, `Recalled` |
| **A host form could not show what an alias means** | `sshResolve`, `ssh -G`, no connection |
| **New-session form needed the default agent** | `SSHHostInfo.DirectAgent` |
| **The same words, everywhere** | `SSHDrivingNote`, `SSHTranscriptWarning` |
