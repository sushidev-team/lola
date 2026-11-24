# Linear agent: what is missing, and who gets an `@lola`

Research for SUSHI-809. The native Linear agent (`[linear_agent]`, SUSHI-619)
already ships: OAuth app actor, Agent Session polling, dispatch by issue team,
mirror of progress, plan approval, reply relay, optional webhook doorbell and
hub doorbell. This note lists what is still missing for `@lola` to work for a
team, and proposes how a mention is distributed. Nothing here is implemented.

## What exists (do not redo)

| Piece | Where |
| --- | --- |
| OAuth `actor=app` install, token refresh | `internal/linear/oauth.go`, `lola linear-agent login` |
| Poll `agentSessions`, dispatch `pending` ones | `linearagent.go` `considerAgentSession` |
| Route by issue **team** (+ Linear project) to a `[[project]]` | `projectForAgentIssue` |
| Mirror plan, phase, PR, CI, elicitation, response | `mirrorAgentSession` |
| Replies: plan verdict, else queued notice | `handleAgentPrompt` |
| Doorbell: local webhook, hub long-poll | `linearwebhook.go`, `hubdoorbell.go` |

## Gaps

### G1. No notion of WHO asked (blocking for teams)
`linear.AgentSession` carries id, status and issue only. Linear's `AgentSession`
also exposes the human who started it (`creator`), the mentioning comment
(`sourceComment`), and the issue's `assignee` / `delegate`. lola reads none of
them, so it cannot decide whose machine should act.

### G2. One OAuth app, many daemons: every daemon claims every session
The app token is workspace-wide and `agentSessions` returns all of this app's
sessions. If two teammates run lola with `[linear_agent]` enabled, both see the
same `pending` session. Only the local ledger and local issue claim prevent a
double spawn, and neither is shared. Result: two worktrees, two PRs, two
acknowledgements on one thread. Today this is safe only with exactly one
daemon holding the token.

### G3. Token distribution
Each member would need the client secret and an admin-approved install. The
token lives in one Keychain. There is no way for a hub org to hold the install
once and let members act as "lola" without sharing a bearer token, which must
not happen (see AGENTS.md, hub is a claim and a candidate, never a command).

### G4. Linear-side setup nobody checks
- The install needs the scopes `linear.AgentScopes` requests (`read`, `write`,
  `app:assignable`, `app:mentionable`). `lola doctor` does not verify the
  granted scopes against that list.
- Webhook category *Agent session events* must be on for the doorbell; the
  hub verifies HMAC but nothing tells the admin it is missing.
- The app must be enabled for the right teams; a team without access shows no
  `@lola` and no error.
- Linear requires a first activity within about 10 seconds of session
  creation or the session looks unresponsive. A 15s poll misses that. The
  doorbell (webhook/hub) is necessary but not sufficient:
  `considerAgentSession` runs `openTicket` (worktree, post_create) BEFORE
  `bindAgentSession` posts the first thought, which can also exceed the
  deadline. The selected daemon must acknowledge immediately after claiming
  and before starting the worktree.

### G5. Event coverage
Polling only treats `pending` sessions as triggers. Not handled:
- a new `prompt` on a session lola has no live lola session for (the
  daemon is offline, the session was torn down): the reply is dropped;
- `stop` signal is acknowledged but never halts anything (deliberate);
- sessions on a team with no `[[project]]` end in an error activity: fine.

Not gaps: replies already land under the mentioning comment (Linear renders
`response`/`elicitation`/`error` activities in that thread), and project
routing already prefers an exact Linear `project_id` match; config order only
breaks ties among team-wide entries.

### G6. Offline / absent owner
If the chosen machine is asleep, the session sits `pending` and the human sees
nothing. Only a daemon holding the app token can post an activity (the hub only
verifies the webhook), so with per-member tokens nobody can say "nobody is
online" when every member is offline.

### G7. Visibility
`lola linear-agent` status shows poll state only. No per-session view of
"claimed by X". The app/TUI cannot show which teammate owns an Agent Session.

## Concept: who gets the message

Principle: the Agent Session belongs to exactly one lola daemon, chosen by a
rule, claimed through a shared arbiter, and the loser stays silent.

### Routing rule (first match wins)
1. **Delegate / assignee is a human** whose Linear user id is bound to a lola
   device: that device's daemon. (Delegating to lola while assigned to Ana
   means "Ana's lola".)
2. **Session creator** (the person who typed `@lola`) with a bound device.
3. **Project owner**: a `[[project]]` (or team) entry in the hub's org config
   naming a default member.
4. **Round robin** across online members who opted in and have a free slot
   (`reserveSlots` already answers this locally).
5. Nobody online: the session stays `pending` and is queued until a
   credentialed daemon reconnects, then it is acknowledged and routed. Document
   that delay; nobody else can post an error (see G6).

Binding Linear user to device: each member sets `[linear_agent].user_id`
(auto-filled from the API key's `viewer`, which lola already knows for
`[linear].api_key`). The daemon publishes it with its hub enrollment; the hub
keeps `linear_user_id -> device` for members of the org.

### Arbitration: the hub claims, the daemon verifies
Reuse the hub chore model instead of inventing one:
- Hub already sees the Linear event (doorbell). Add a typed offer
  `linear_agent_session { session_id, issue }` that is a CLAIM only.
- Atomicity: the hub claim must be granted BEFORE `openTicket`, and the hub
  must not reassign after spawn authorization until the first attempt's outcome
  is reconciled. A Linear activity cannot arbitrate: `agentActivityCreate` has
  no expected-status or lease token, so a late acknowledgement cannot exclude a
  second worker whose lease replaced the first during startup.
- The chosen daemon acknowledges at once, re-reads the Agent Session from
  Linear, and runs the exact gates of a Linear dispatch: `dispatchHold`, health
  gate, `reserveSlots`, project routing. The hub never writes a prompt or a
  branch.
- Lease semantics as for hub chores: a daemon that does not ack in N seconds
  (before any spawn) loses the lease and the hub offers the next candidate.
- Fencing: every grant carries a monotonically increasing epoch. Just before
  `openTicket` the daemon asks the hub to authorize the spawn for that epoch;
  the hub refuses a stale epoch, so an expired owner that wakes up late neither
  spawns nor posts. After authorization the hub does not reassign: a lost
  daemon is reconciled (session present or not) before any new grant, and the
  new owner binds to a surviving lola session rather than spawning again.

### Token model (pick one; recommended first)
A. **Per-member OAuth install**: each member runs `lola linear-agent login`
   and their token is an `actor=app` token of the same app. PREREQUISITE: today
   `linearAgentLogin` and token refresh both need the app's client secret, so
   members would have to receive it. A secret-free flow (PKCE public client,
   for login AND refresh) must exist first, or this model is not viable.
B. One shared token held only by an org "relay" daemon that forwards. Rejected:
   a single point that holds a workspace-wide secret, against the hub rules.

### Behavior without a hub
Single-user mode stays as is. For a small team without a hub, add a
`[linear_agent].only_when_assigned_to_me = true` filter. Every daemon resolves
the SAME single owner with the ordered routing rules above (delegate/assignee,
else creator) and acts only if that owner's id equals its own `user_id`. Never
"assignee OR creator": a mention by Bob on Ana's issue would admit both
daemons. This fixes G2 for the common case with no new infra.

## Proposed work, in order

1. **Read who asked.** Extend `AgentSession` with `creator{id}`, issue
   `assignee{id}` / `delegate{id}`; keep it display+routing data, never control.
   (G1, small, safe on its own.)
2. **Owner filter.** `[linear_agent].user_id` (default from `viewer`) and
   `only_when_assigned_to_me`, single resolved owner; ledger entry records
   `rejected: not mine` silently (no activity, another daemon may own it).
   (G2, solo/small teams.)
3. **Setup check in `lola doctor`**: granted scopes vs `linear.AgentScopes`,
   webhook category, team access, doorbell configured. (G4)
4. **Secret-free login + refresh** (PKCE), prerequisite for per-member
   installs. (G3)
5. **Hub routing**: `linear_user_id` in enrollment, offer kind
   `linear_agent_session`, atomic claim before `openTicket`, immediate ack,
   queue while nobody is online. Wire change in `hubproto/` and `hub/` in the
   SAME PR, backward compatible. (G2/G6)
6. **Missed replies**: when a prompt arrives for a session with no live lola
   session, post a note instead of dropping it. (G5)
7. **Status surface**: owner and lease in `lola linear-agent status`, app and
   TUI. (G7)

## Open questions for a human
- Is per-member OAuth install acceptable to the workspace admin, or must the
  app be installed once? (decides token model A vs hub-held credentials)
- Should an unassigned `@lola` mention go to the creator, or to a round robin?
- Is a delay while nobody is online acceptable?

## Status

Built (PR #94):

| Step | What |
| --- | --- |
| 1 | Who asked is read: creator, assignee, delegate, and the mention comment text |
| 2 | `[linear_agent].user_id` (default: the `[linear]` API key's user) and `only_when_assigned_to_me`, one resolved owner per session |
| 3 | `lola doctor` row "linear agent": token, granted scopes, doorbell, owner filter |
| 4 | Secret-free login and refresh (PKCE; the client secret is optional) |
| 5 | Hub arbitration: `/v1/linear/{claim,authorize,settle}`, epoch fencing, deference to the owner's live daemon, immediate acknowledgement before the worktree; `[linear_agent].hub_claim` |
| 6 | A reply into an ended or finished thread is answered once |
| 7 | Ownership and waiting count in `lola linear-agent status` and the TUI |

Known limits: a capped owner keeps the thread (it renews its claim; handing a
queued session to a free peer is not built); an issue already running on another
member's machine from a label poll is not detected across machines; the owner
filter and `hub_claim` are `config.toml` keys, the settings forms do not edit
them; the desktop app does not show the owner.
