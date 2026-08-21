# SUSHI-585 — Agent fallback: quota detection, handoff, manual switch

## Goal

When a session's coding agent hits its usage/subscription limit, lola detects it
from the pane, and hands the session to a fallback agent — automatically
(configured chain) or manually (per-spawn picker + "switch agent" action) —
re-spawning the new agent on the SAME worktree/branch with a deterministic
handoff document instead of a 1M-token transcript import.

## User-approved decisions (2026-08-21)

- Scope: everything in ONE PR (detection + chain + handoff + manual picker).
- Handoff payload: spec pointer + git state + pane tail in `.lola/handoff.md`.
  Simplification (stated here deliberately): NO headless LLM summary pass in the
  takeover path — the doc is deterministic and the launch prompt tells the new
  agent to consult the predecessor's transcript file selectively if needed.
  Rationale: a summarizing exec would take minutes, can itself hit the same
  quota, and the compressed-context goal is already met by a bounded doc.
- Detection: pane classification (new `attention.ActivityQuotaLimited`), reusing
  the per-CLI verbatim limit banners (researched from anthropics/claude-code,
  openai/codex, anomalyco/opencode sources).

## Design

### 1. Config (`internal/config`)

- `[defaults].agent_fallback = ["codex", "opencode"]` — ordered fallback chain.
- `[[project]].agent_fallback` — per-project override, added to the
  ProjectInherits bitmap (absent = inherit, `agent_fallback = []` = override to
  nothing/disabled). fileProject gets a `*[]string`.
- `[reactions].agent_fallback` sub-table reusing the Reaction struct; only
  `auto` is meaningful: `false` (default) = notify-only on detection,
  `true` = auto-switch to the next chain entry.
- Validate: entries must be `claude|codex|opencode`; no duplicates.
- New resolver: `FallbackChainFor(project string) []string` (project → defaults
  → empty).

### 2. Detection (`internal/attention`)

- New `ActivityQuotaLimited` Activity value. In `Classify`, checked AFTER
  Blocked (modal owns screen) and AFTER `hasLiveWorkingCue` (a streaming agent
  with a stale banner in scrollback is NOT dead — false positives here kill a
  healthy session), BEFORE the waiting/weak-working cues.
- Scan window: last ~30 lines only (a new `quotaTailLines`), so a banner that
  scrolled up does not match.
- Vocabulary (per kind, case-insensitive; verbatim from the CLIs):
  - claude: `You've hit your (session )?limit · resets`, `You're out of extra
    usage`, `Limit reached – contact an admin`, `API Error: Rate limit
    reached`, `API Error: Usage credits required`
  - codex: `You've hit your usage limit`, `Your workspace is out of credits`,
    `You hit your spend cap`, `Usage limit reached. You've reached your usage
    limit`, `Request a limit increase from your owner`
  - opencode: `Free usage exceeded`, `Subscription quota exhausted`,
    `insufficient_quota`, `exceeded_current_quota_error`, `You exceeded your
    current quota`, `credit balance is too low to access the`, `suspended due
    to insufficient balance`, `Too Many Requests: quota exceeded`
  - NEGATIVE: opencode's transient `retrying in Xs - attempt #N` lines must NOT
  match (the agent is still working).
- `agentReconcile` (daemon/observer.go): ActivityQuotaLimited →
  `AgentWaitingInput` + `InputReason = InputQuotaLimited`, AtPrompt closed +
  verified (pane is live evidence; a quota-dead agent must not receive
  send-keys).
- Rollup stays `needs_input` — NO new status string, so `state.AllStatuses`,
  the consumer tables and the desktop parity test are untouched (verified:
  InputReason is not pinned by desktop/state_parity_test.go and not read in
  desktop/frontend).

### 3. State (`internal/state`)

- New `InputQuotaLimited InputReason = "quota_limited"`. Display label "usage
  limit reached" in the UIs where InputReason is rendered.

### 4. Runtime (`internal/runtime`)

New `Native.SwitchAgent(ctx, s session.Session, kind agent.Kind, paneTail
string) (session.Session, error)`:

1. Refuse when the worktree dir is gone (nothing to relaunch into).
2. `KillSessionTree(s.TmuxName)` — the AGENT pane only; `-shell-N` / `-dev-N` /
   `-review` aux sessions stay up (the worktree survives, so they keep working).
3. Write `.lola/handoff.md` (below).
4. `writeAgentArtifacts(dir, kind)` for the NEW kind; exclude `.opencode/` when
   kind==opencode (excludeGitPattern is idempotent).
5. Rewrite `.lola/env` with the new kind (CODEX_HOME appears/disappears).
6. `tmux.NewSession(id, dir, launchCommandHandoff(id, kind))` — fresh
   conversation (resume=false); the prompt becomes: "You are lola session <id>,
   taking over from <old kind> which hit its usage limit. Read
   .lola/handoff.md first, then .lola/prompt.md."
7. Re-apply `ConfigureSession` chrome.
8. Return the record with `Agent = kind`, agent axis `AgentStarting`
   (SetAgentState, which re-arms the anti-false-working clock).

Handoff doc (deterministic, bounded): issue id+title, branch, previous agent
kind + reason, predecessor transcript path (session.TranscriptPath) with a
"read selectively, do not import wholesale" instruction, git facts via a new
read-only `worktree.Manager` helper (log --oneline base..HEAD capped ~20,
status --porcelain capped, diff --stat capped), last ~80 pane-tail lines
(control chars stripped), and the same commit/push/PR expectations prompt.md
carries.

### 5. Daemon

- `NativeAPI` interface: add `SwitchAgent`; add trailing `agentOverride string`
  param ("" = resolve from config) to `Spawn`, `OpenManualAgent`,
  `OpenPRAgent`. dispatch passes "". Runtime: `resolveKind(p, override)` =
  override when `agent.Valid(override)`, else the config chain.
- `internal/daemon/fallback.go` (new): `maybeFallback(ctx, s)` called from
  observeNative after react:
  - gate: HasAgent, alive, InputReason==InputQuotaLimited.
  - resolve chain via cfg.FallbackChainFor(s.Project), skipping the current
    kind and any kind in `session.AgentsTried` (loop guard: no A→B→A).
  - first entry whose binary resolves on PATH wins (reuse runtimeHealth seam);
    none → notify once "no fallback agent available".
  - `[reactions].agent_fallback.auto`: true → perform the switch (same code
    path as the manual command), false (default) → notify once (urgent) naming
    the suggested kind.
  - one-shot per (session, kind): persist `AgentsTried []string` +
    `FallbackCount int` on the session; a switch stamps the OLD kind into
    AgentsTried. The new agent quota-limiting later continues the chain.
- `handleSwitchAgent` (cmd=switchAgent): refuse unknown session / agentless /
  same kind / invalid kind / target binary not on PATH. Capture the pane tail
  (bounded, via the paneTail seam), call nat.SwitchAgent, Store.Update the
  record (Agent, AgentStarting, clear InputReason, close AtPrompt, stamp
  AgentsTried), Save, recordSessionEvent, notify info. Manual switch is allowed
  even for a healthy idle/working agent (human override, like kill) — the UIs
  confirm first.
- handleOpenTicket / handleOpenManual: honor the override — resolve
  `agent.Parse(override)` when `agent.Valid(override)`, health-gate THAT
  binary, pass the override into Spawn/OpenManualAgent.

### 6. Protocol (`internal/protocol`)

- `OpenTicketArgs.AgentKind string` + `OpenManualArgs.AgentKind string`
  (`agentKind`, omitempty; "" = configured default).
- New cmd `switchAgent`: `SwitchAgentArgs{Session, Agent}` →
  `SwitchAgentData{Agent, Message}`.
- `SessionInfo.Agent string` (from session.Agent) so the UIs can show the
  current kind and gate the switch affordance.

### 7. TUI (`internal/tui`)

- ticketpicker: `a` cycles the agent choice (default → claude → codex →
  opencode), shown in the footer; openTicketCmd carries it.
- manual spawn (manual.go + its form): agent-kind selector when useAgent is on.
- sessionview: `A` on an agent session opens a small chooser (3 kinds) →
  cmd=switchAgent; header shows the session's agent.
- needs_input label for InputReason "quota_limited" → "usage limit".

### 8. Desktop (`desktop/`)

- TS types + store: `switchAgent(session, agent)` action; openTicket/openManual
  pass agentKind; SessionInfo.agent.
- TicketPicker.svelte + ProjectDetail.svelte (manual spawn): an agent-kind
  Select (default = the project's configured agent — ProjectInfo.agent is
  already on the wire).
- SessionMenu: "Switch agent" items (3 kinds, current marked/disabled),
  confirmed through the confirm store (the live agent pane is replaced).
- Agent kind chip in the session header; "usage limit reached" needs-input
  label.

### 9. Doctor

When a fallback chain is configured, health-check each entry's binary and
report (warn-level, not critical).

### 10. Docs

README ("The coding agent" gains a fallback subsection; config reference rows)
+ config.example.toml entries.

## Files touched (planned)

- config: `config.go`, `inherit*.go`, `validate.go`, tests; `config.example.toml`
- attention: `activity.go` + tests
- state: `state.go` (+ maybe axes tests)
- worktree: small read-only git-log helper + test
- runtime: `native.go` (+ new `switchagent.go`), tests
- daemon: `daemon.go` (interface), `fallback.go` (new), `server.go`,
  `tickets.go`, `open.go`, `observer.go` (agentReconcile), tests
- protocol: `protocol.go`
- tui: `ticketpicker.go`, `manual.go`, `sessionview.go`, `client.go`, tests
- desktop: store.svelte.ts, TicketPicker.svelte, ProjectDetail.svelte,
  SessionMenu/sessionmenu, types, tests
- README.md

## Verification

Repo gates run via the Makefile env (repo-local GOCACHE). Expected result for
every row: the named command exits 0.

- config: `GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go test ./internal/config/ -run 'AgentFallback|FallbackChain' -v`
  — chain validation rejects unknown kinds and duplicates; save→load round-trip
  keeps a project override, omits an inherited key (inheritance stays live);
  `agent_fallback = []` round-trips as "override to nothing".
- attention: `go test ./internal/attention/ -run Quota -v` — one positive
  fixture per CLI's verbatim banner classifies ActivityQuotaLimited; negatives:
  opencode transient `retrying in Ns` lines, a banner scrolled above the
  30-line window, a mid-turn pane whose status line is live (working wins), a
  review-body prose mention of "rate limit".
- state: `go test ./internal/state/` — InputQuotaLimited round-trips.
- runtime: `go test ./internal/runtime/ -run SwitchAgent -v` — fake tmux/WT:
  artifacts rewritten for the new kind, .lola/env rewritten (CODEX_HOME
  appears for codex), handoff.md written with issue/branch/git state/pane
  tail, ONLY the agent pane killed (shell/dev/review tabs untouched), chrome
  re-applied, worktree-gone refusal, session returned Agent=new+starting.
- daemon: `go test ./internal/daemon/ -run 'SwitchAgent|Fallback|Quota|OpenTicket|OpenManual' -v`
  — handleSwitchAgent refusals (unknown session / agentless / same kind /
  invalid kind / binary missing); auto-fallback advances the chain once per
  quota episode and never revisits AgentsTried; auto=off notifies once and
  switches nothing; openTicket/openManual override selects the binary that
  gets health-gated and spawned.
- observer: agentReconcile maps a quota pane to waiting_input +
  quota_limited and closes AtPrompt (`go test ./internal/daemon/ -run Reconcile`).
- TUI: `go test ./internal/tui/ -run 'Ticket|Manual|SessionView|SwitchAgent' -v`.
- Desktop: `cd desktop/frontend && npm test -- --run` (vitest) for the
  touched components (TicketPicker, SessionMenu, store) — expect pass;
  `npm run check` (svelte-check) clean if configured.
- Full gate: `make check` (build + vet + test) green before review.
- Manual smoke (run before PR, documented in the PR test plan):
  1. `lola openTicket` with agentKind=codex on a test project → pane runs
     codex, SessionInfo shows agent=codex.
  2. `lola switch-agent <session> claude` → claude pane replaces codex in the
     same worktree, `.lola/handoff.md` exists and names the previous agent,
     shell/dev tabs survive.
  3. With `[reactions].agent_fallback auto = true` and a fallback chain set,
     a pane showing a quota banner flips the session to the next chain agent
     within one observe cycle (verified on a live test session with a
     scripted pane).

## Risks / rails

- False-positive detection kills a live agent → mitigated by ordering (live
  working cue wins), the 30-line window, verbatim banners, and auto=off
  default; the old agent's transcript/worktree survives either way.
- Loop guard: AgentsTried prevents A→B→A; auto-switch consumes chain entries.
- A quota-dead agent's AtPrompt is closed so no reaction/hand-off types into
  it.
- Send-keys safety is untouched: the fallback path never types into the OLD
  agent; it replaces the pane.
