# lola: poll-centric → project-centric restructure — implementation plan

## Frozen foundation (read this first — it resolves the cross-dimension conflicts)

The four design dimensions each invented a slightly different session discriminator, and the adversarial reviews proved they cannot be built against one another as written. Before any code is touched, this plan **freezes one shared session contract** that every dimension conforms to. Every later section assumes it.

**The session discriminator is two independent axes, not one:**

- `Kind ∈ {linear, pr, manual}` — governs Linear coupling and teardown branch ownership.
- `Agentless bool` — governs the agent stack (hooks / reactions / review / pane classification / send-keys).

Rationale (folds critique invariant-violations #1): agent-ness and Linear-coupling are orthogonal. A `pr` session can run an agent (push-to-PR) **or** be a plain shell (today's `lola open`). Folding agent-ness into `Kind` (as the datamodel/protocol drafts did) mis-routes a PR shell into the agent stack, where `DeriveStatus(alive,nil)="working"` silently occupies a `global_cap` slot. We drop the datamodel draft's `BranchOwned` and the protocol draft's fourth `scratch` kind — both are derivable from `(Kind, Agentless)`.

**Fail-closed derivation (folds invariant-violations #2):**

```go
func (s Session) EffectiveKind() Kind {
    if s.Kind != "" { return s.Kind }
    if s.Manual   { return KindManual }   // legacy alias
    if s.IssueUUID == "" { return KindPR } // fail CLOSED: no UUID ⇒ never a Linear writer
    return KindLinear
}
func (s Session) LinearBound() bool { return s.EffectiveKind() == KindLinear && s.IssueUUID != "" }
func (s Session) HasAgent()    bool { return !s.Agentless }
```

An unstamped keyless agent session must **never** reach a Linear write. `LinearBound()` requires both `Kind==linear` **and** a non-empty UUID.

**Single stamper.** Exactly one layer sets `Kind`/`Agentless`: `runtime.finishLaunch`. No handler post-stamps a returned session. This kills the "two owners, no source of truth" divergence.

**Two independent observer gates (folds #1, #8 invariant-violations; risk #8):**

```
per native session:
  if s.Agentless { observeShell(s); continue }     // AGENT gate — shell/dead only, no pane, no reactions
  … agent + PR stack (pane, prForBranch, DeriveStatus, reactions, review, coderabbit) …
  if s.LinearBound() { … write-back, label flip, orphan reconcile … }   // LINEAR gate, independent
```

**ID-shape + Adopt (folds invariant-violations #4, risk #14).** Three ID prefixes, each recoverable after a store loss:
- `lola-<proj>-<issue>` → linear (issue-shaped segment).
- `lola-<proj>-pr-<slug>` → pr. **New `pr-` infix** (not `open-`).
- `lola-<proj>-open-<slug>` → manual-shell (today's `open-`).

`Kind` is persisted and authoritative. `Adopt` recovers it from persisted `sessions.json`; after a full store loss it reconstructs from the ID infix, and resolves `Agentless` with a bounded `agentArtifactPresent(dir)` probe (`.lola/settings.json || .lola/codex/config.toml || .opencode/plugins/lola-hook.js`).

**Teardown branch ownership** is a pure function of the frozen axes, no extra bit:
```go
ownsBranch := (s.EffectiveKind()==KindLinear) || (s.EffectiveKind()==KindManual)
// pr = upstream/detached → never delete. Kill passes branch="" iff !ownsBranch.
```

**Config has exactly one writer discipline** (folds protocol-races #3): all config mutation is client-side `config.Save` (so it works daemon-down — see TUI #1), **serialized by an advisory `flock` on `config.toml`** taken by both `config.Save` and every daemon config write (`handleEnable`/`reload` read path). This closes the cross-process lost-update window the re-read-rebase only shrinks. We do **not** add `projectAdd`/`projectRemove` socket commands.

Everything below conforms to this foundation.

---

## 1. Bird's-eye of today

**Model.** `config.toml` holds a flat `[]Project` (local repo: path/repo/default_branch/agent/env/symlinks/post_create) and a flat `[]Poll`. A poll is a filter unit bound to exactly one project by name (`Poll.Project = [[project]].name`, required, must resolve). Many polls → one project; a project may have zero polls and is still valid.

**How a poll becomes a session** (`internal/daemon/dispatch.go`, one tick):
1. Snapshot config under `d.mu`, release. Resolve `pollCap`, `globalCap`, `pollRepo`, the native runtime, and `agentBin = agent.Parse(AgentForProject(poll.Project)).Binary()`.
2. **Health-gate**: `runtimeHealth(agentBin)` (tmux+git+that agent binary) + project resolves + native non-nil. On failure: skip, record `lastError`, **mutate nothing**.
3. Resolve Linear client/viewer/cycle fresh; `MatchingIssues` (paginated, filter from poll mode fields; team clause always set).
4. Dedup: cross-poll `inflight.Has(uuid)` first, then per-mode (label/seen/state). Sort (`priority,createdAt`). Budget = `min(pollCap, globalCap − liveCounted)`, `liveCounted` from the store snapshot (`NativeLiveCounted`).
5. Per issue up to budget: **mark inflight + persist seen BEFORE `nat.Spawn`** (crash guard), `Spawn` (worktree → symlinks → post_create → tmux agent), **Upsert immediately** so the next Budget counts it, then (label mode) re-read labels fresh + flip, then P4 write-back.

`nat.Spawn` is hard-coupled to a Linear issue: it errors on empty `Issue.Identifier`; the identifier drives the session ID, branch, and `promptMD` briefing.

**Observer** (~30s) merges tmux liveness + `gh pr list --head <branch>` PR facts + pane classification into `DeriveStatus`, then fires reactions/write-back/review/coderabbit. `cmd=sessions` serves this cache with zero execs. `lola open` already exists: a detached-HEAD worktree + plain shell, `Manual:true`, `Status:"shell"`, skipped by every engine via one `if s.Manual { … continue }`.

**What must change.** (a) Projects become the top-level TUI object and can exist with no poll; polls nest under them on disk. (b) New user-initiated launch paths: open a PR's branch, start a Linear ticket on demand, open a manual worktree — each optionally with an agent or a bare shell. (c) The session model must distinguish three kinds without letting keyless agent sessions touch Linear. (d) The TUI's information architecture flips from a poll cockpit to a project navigator. None of this may weaken the dispatch crash-guard, `liveCounted`, health-gate, send-keys idle gate, secret discipline, or the `sessions`-is-a-pure-reader invariant.

---

## 2. Target model

**Project is first-class.** A `[[project]]` is a local git checkout lola can act in. It owns: path, repo, default branch, **branch prefix** (new), agent, env/symlinks/post_create, an optional **Linear team/project binding** (new — see TUI ticket picker), and zero-or-more nested polls.

**Poll is an optional nested property of a project.** On disk a poll lives at `[[project.poll]]`; in memory it stays in a **flat `Config.Polls`** with `Project` back-filled, so `dispatch.go`, `syncWorkers`, `PollByName`, `enable/disable`, and `PollStatus` are byte-for-byte unchanged. Nesting is a serialization concern only.

**Session Kind discriminator** (frozen foundation): `linear | pr | manual` × `Agentless`. Relationship to sources:

| Launch | Kind | Agentless | Slot-counts? | Linear writes? | Branch |
|---|---|---|---|---|---|
| poll dispatch / ticket picker | linear | false | yes | yes | lola-owned `lola/<id>` |
| PR picker → agent | pr | false | yes | no | upstream (tracking, no-delete) |
| PR picker → shell (`lola open`) | pr | true | no (`shell`) | no | upstream/detached (no-delete) |
| manual worktree → agent | manual | false | yes | no | lola-owned new branch (deleted on teardown) |
| manual worktree → shell | manual | true | no (`shell`) | no | lola-owned new branch (deleted on teardown) |

The project/poll/session relationship: a project may drive **zero or more** polls (automatic, `linear`-kind sessions) and **any number** of user-initiated sessions of any kind. Sessions reference their project by name and store their own worktree path (so teardown survives project removal — see §6).

---

## 3. Config schema & migration

### 3.1 In-memory structs (`internal/config/config.go`)

`Project` gains three fields; the public flat view is otherwise unchanged.

```go
type Project struct {
    Name          string `toml:"name"`
    Path          string `toml:"path"`
    Repo          string `toml:"repo"`
    DefaultBranch string `toml:"default_branch"`
    BranchPrefix  string `toml:"branch_prefix"`   // NEW; "" ⇒ DefaultBranchPrefix "lola/"
    LinearTeamID  string `toml:"linear_team_id"`  // NEW; team binding for the ticket picker (pollless projects)
    LinearProjID  string `toml:"linear_project_id"` // NEW; optional default Linear project for the picker
    Agent         string `toml:"agent"`
    PostCreate    []string          `toml:"post_create"`
    Symlinks      []string          `toml:"symlinks"`
    Env           map[string]string `toml:"env"`
    Polls         []Poll            `toml:"-"` // transient; populated only during (de)serialization
}
```

`Poll` is unchanged except `Project` gets `,omitempty` (so nested polls don't re-emit the parent name).

`Config` is unchanged: it still exposes `Projects []Project` and flat `Polls []Poll`.

### 3.2 Disk mirror — where nesting happens

```go
type fileProject struct {
    Name, Path, Repo, DefaultBranch string
    BranchPrefix string `toml:"branch_prefix,omitempty"`
    LinearTeamID string `toml:"linear_team_id,omitempty"`
    LinearProjID string `toml:"linear_project_id,omitempty"`
    Agent string; PostCreate, Symlinks []string; Env map[string]string
    Polls []Poll `toml:"poll"` // -> [[project.poll]]
}
type fileConfig struct {
    Defaults fileDefaults; Linear LinearConfig
    Projects []fileProject `toml:"project"`
    Polls    []Poll        `toml:"poll,omitempty"` // COMPAT-ONLY (legacy top-level + orphans)
    // sub-table pointer mirrors unchanged
}
```

`fc.config()` flatten — **back-fill only when empty; error on conflict** (folds invariant-violations #7 / protocol-races #11):
```go
for _, fp := range fc.Projects {
    c.Projects = append(c.Projects, projectFrom(fp)) // Polls left empty in memory
    for _, p := range fp.Polls {
        if p.Project != "" && p.Project != fp.Name {
            errs = append(errs, fmt.Errorf("poll %q under project %q sets project=%q", p.Name, fp.Name, p.Project))
            continue
        }
        p.Project = fp.Name
        flat = append(flat, p)
    }
}
flat = append(flat, fc.Polls...) // legacy/orphan top-level keep explicit Project
```
(Because `Load` can't return per-field errors cleanly, the conflict is recorded and surfaced by `Validate`; the flatten also refuses to silently repoint.)

`c.file()` re-nest: bucket each resolvable poll under its project (dropping `Project`); polls whose `project` doesn't resolve go to the top-level `[[poll]]` orphan table (never dropped). `Load`/`Save` are otherwise unchanged (tilde-expand loop and atomic temp+rename 0600 stay).

### 3.3 Old → new mapping

| Old (disk) | New (disk) | In-memory |
|---|---|---|
| `[[project]]` | `[[project]]` (+ 3 new keys) | `Projects[i]` |
| `[[project]].default_branch` | same | base branch |
| hardcoded `"lola/"` in native.go | `[[project]].branch_prefix` | `BranchPrefix`, resolver `BranchPrefixForProject` |
| — | `[[project]].linear_team_id` / `linear_project_id` | ticket-picker binding |
| `[[poll]]` (top-level) | `[[project.poll]]` (nested) | `Polls[i]`, `Project` back-filled |
| `[[poll]].project="x"` | implied by nesting (omitted) | back-filled from parent |
| `[[poll]]` w/ bad project | stays top-level `[[poll]]` | orphan; still fails Validate |
| `[defaults]`/`[linear]`/sub-tables | unchanged | unchanged |

### 3.4 Migration — lazy, zero-loss, no new command (folds risk #1/#2, protocol-races migration note)

The datamodel draft's approach is correct and the migration critique confirmed it is safe **because `[[poll]]` stays a live compat field**, not a dropped unknown key:

1. **Old config loads verbatim.** `fc.config()` flattens top-level `[[poll]]` into `c.Polls` exactly as today.
2. **First `Save` migrates in place.** Any TUI edit re-nests resolvable polls under their project via `c.file()` and drops the emptied top-level table. Atomic temp+rename; no forced rewrite, no migration bookkeeping, no crash window.
3. **Unresolvable poll → orphan round-trip**, never discarded; keeps failing `Validate` until fixed.
4. **Hand-edited old-schema `[[poll]]` after upgrade still works** — it is a live compat table, not silently ignored. (Risk #2 is thereby avoided by construction; we additionally emit a one-line deprecation notice in `Validate` when a top-level `[[poll]]` is present so users know to let the TUI normalize it.)

**New static validations** (all offline, no execs):
- **Global poll-name uniqueness across the post-flatten union** (folds invariant-violations #11, protocol-races #11). `d.seen` and `enable/disable` key on poll name; two `nightly` polls under different projects would share a seen map. Enforced before anything consumes `c.Polls`. Documented loudly: **poll names are global despite the nesting.**
- `BranchPrefix` shape check (no whitespace / no `..`, ref-safe), resolver `BranchPrefixForProject`.
- Nested-poll `project` conflict (from §3.2).

`PollRepo`, `AgentForProject`, `EffectiveCap`, `PollByName`, `ProjectByName` keep working against the flat slices — no signature changes.

**Recommendation:** do **not** add a rewrite-on-load or a `lola migrate` command. Lazy-on-save keeps `Load` pure and read-only.

---

## 4. TUI design (centerpiece)

### 4.0 Foundations that fix the flow critiques

- **Home renders from local `cfg`, decorated by the daemon** (folds tui-flow #1). Project *identities* come from `rootModel.cfg.Projects` and are always navigable — even on a cold start or a dead daemon. `ProjectInfo` from the `projects` command only **decorates** rows (Live/NeedsYou/PollError/PathOK/agent-health). No daemon ⇒ counts show `—`, everything still works, including `a` add-project.
- **Config edits are client-side** (`config.Save` under flock) exactly as `projectForm`/`form.go` do today — daemon-down-safe. `enable/disable` prefer the socket when the daemon is up (existing `toggleSelected`).
- **Per-project agent health** (folds tui-flow #4). `ProjectInfo.AgentOK`/`AgentBin` is resolved by the daemon via `AgentForProject(name)` + PATH probe, cached alongside the `status` probe. The detail health line and spawn-verb disabling key on **that**, never the default-agent probe.
- **Capabilities gate keys, not tooltips** (folds tui-flow #7). Until the `pr`/manual-agent kind ships, `a` (agent-on-open) and the Launch=agent field are **omitted** from the keybar and form. If ever shown-but-unavailable, pressing emits an explicit message line, never a silent no-op.
- **Stable sort** (folds tui-flow #8). Home sorts attention-first **only on explicit refresh** (`o` / manual `r`) and on push; between 5s ticks the row order is frozen (counts update in place). No reflow under a reading user.
- **Navigation keys** (folds tui-flow #10/#11): `esc`/`h`/`←` pop a level; `q` at Home requires a confirm; `e` means edit-project everywhere (poll editing is `P`); detail action `s` enters sessions; inside sessions `s` toggles shell/agent (unchanged from today).
- **Narrow degradation** (folds tui-flow #9). Every stack screen has a `W<72 || H<18` variant: drop the breadcrumb line, collapse any embed, single-column lists, line-count-stable. Same rule `narrowCockpit()` follows.
- **Event feed keeps a home** (folds tui-flow #5): a global **Activity** strip on Home (right gutter when `W≥100`, else a `^a` overlay), fed by `SessionsData.Events`, newest-first.

### 4.1 Navigation stack

```go
type screen interface {
    Update(tea.Msg) (screen, tea.Cmd)
    View(w, h int) string
    KeyHelp() []keyHint
    Title() string
    OnPush() tea.Cmd   // fires loads
}
type rootModel struct {
    stack []screen                 // [0]=home, top=active
    settings *settingsForm; doctor *doctorOverlay; palette *paletteOverlay
    cfg *config.Config; cfgPath string
    status *protocol.StatusData
    projects map[string]protocol.ProjectInfo // decoration cache, keyed by name
    width, height int; daemonOp string
    // embed machinery (terms, agentTerm, spin…) unchanged
}
```

Fixed frame: vitals bar / breadcrumb / active `screen.View` / message / keybar, overlays composited via existing `modalOver`. `push`/`pop` manage the slice; `esc/h/←` pops.

Screen inventory: Home (always `stack[0]`), Project detail, PR picker, Ticket picker (with a **team-select first step**), Manual worktree form, Sessions (scoped/global — reuses `sessionsModel` verbatim), Poll edit (`formModel`, unchanged, reached via detail `P`). Settings/Doctor/Palette float as overlays.

### 4.2 Shared async pattern

Every load: `OnPush` sets `state=Loading`, issues a socket `tea.Cmd`, transitions on a typed `tea.Msg` carrying `(target, reqGen)` for stale-drop. Mutations return `*DoneMsg` → flash + `fetchSessionsCmd`. This is exactly the existing `fetchSessionsCmd`/`answerDoneMsg` pattern extended.

```go
func listPRsCmd(project string, gen int) tea.Cmd {
    return func() tea.Msg {
        resp, err := requestFn(protocol.Request{Cmd:"prs", Args: mustJSON(protocol.PrsArgs{Project:project})})
        if errors.Is(err, errDaemonDown) { return prsMsg{project, gen, nil, "", true} }
        if err != nil { return prsMsg{project, gen, nil, err.Error(), false} }
        if !resp.OK  { return prsMsg{project, gen, nil, resp.Error, false} }
        var d protocol.PrsData; _ = json.Unmarshal(resp.Data, &d)
        return prsMsg{project, gen, &d, "", false}
    }
}
```
`loadState` machine per screen: `Loading | Ready | Empty | ErrState | PermState | ConfigGap | DaemonDown`.

The shared `filterList` component (powers projects/PRs/tickets): owns cursor/viewport/`/`-filter/`selID` pin, subsequence fuzzy match, widths over the full set. Pure and unit-testable (mirrors `sessionview.go`). Consumers feed rows + a renderer.

### 4.3 HOME (projects)

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ lola  daemon ● running · runtime ✓ · linear ✓ · 3 need you · 9 live      14:22 │
│ lola ▸ projects                                                                │
├──────────────────────────────────────────────────────────────────────────────┤
│ /nori_                                                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│    PROJECT     PATH               POLL        LIVE  ATTENTION      LAST         │
│ ───────────────────────────────────────────────────────────────────────────── │
│ ›  nori        ~/src/nori         ● 2 on      4     2 need·1 ci    2m           │
│    kombu       ~/src/kombu        ○ paused    1     —             41m           │
│    ume         ~/src/ume          ⚠ no polls  0     —             —             │
│    shoyu       ~/archive/shoyu    ⚠ missing   0     —             —             │
├──────────────────────────────────────────────────────────────────────────────┤
│ added project "ponzu"                                                          │
│ ↑↓ move · enter open · a add · e edit · space poll · x remove · s sessions · / │
└──────────────────────────────────────────────────────────────────────────────┘
```
POLL glyph (shape-distinct, not color-only): `● N on` / `○ paused` / `⚠ N err` / `⚠ no polls` / `⚠ missing` (from `PathOK`). ATTENTION: `N need` (urgent) + `N ci` (broken). Rows from local cfg; counts from decoration cache.

**Empty state:** centered CTA "No projects yet. Press `a` to add your first repo. (a project is any local git checkout; polling is optional.)"

Keys: `↑↓/kj` move · `g/G` edges · `enter/l/→` open detail · `a` add (inline `addProjectForm`: Name/Path/Repo/DefaultBranch/Agent; saves even if path missing → `⚠ missing`) · `e` edit · `x` remove (confirm counts live sessions + dependent polls — see §4.7) · `space` toggle poll (1 poll: flip; N: inline chooser) · `s` global sessions · `o` cycle sort · `/` filter · `S`/`d`/`^k` overlays · `^r`/`^x` daemon · `q` quit (confirm).

### 4.4 PROJECT DETAIL

```
│ lola ▸ nori                                                                    │
│ ╭─ nori ────────────────────────────────────────────────────────────────────╮ │
│ │ path ~/src/nori   repo acme/nori   agent claude   base main                 │ │
│ │ polls ● triage (on) · ○ nightly (off)                                        │ │
│ │ health runtime ✓ · git ✓ · claude ✓        4 live · 2 need you · 1 ci-red    │ │
│ ╰─────────────────────────────────────────────────────────────────────────────╯│
│ ╭─ Actions ──────────────────────────────────────────────────────────────────╮ │
│ │  p  Open a PR ……… pick from 6 open pull requests                            │ │
│ │  t  Start a ticket … pick a Linear issue → worktree + agent                 │ │
│ │  w  New worktree …… branch off main, agent or shell                         │ │
│ │  P  Polls ……… add / edit / toggle this project's polls                      │ │
│ │  s  Sessions …… 4 live in this project                                       │ │
│ ╰─────────────────────────────────────────────────────────────────────────────╯│
│ ╭─ Live in nori ─ (top by attention) ────────────────────────────────────────╮ │
│ │ › ENG-42 fix oauth flow   needs you  #229 ✓          2h                      │ │
│ │   ENG-31 cache layer      ci_failed  #231 ✕ ci      1h                      │ │
│ ╰─────────────────────────────────────────────────────────────────────────────╯│
│ p PR · t ticket · w worktree · P polls · s sessions · enter open · esc back    │
```
Health line keys on the **per-project agent** bit. If red, the three spawn verbs render disabled with the reason ("git not found — see doctor"); the daemon independently re-gates. Dual affordance: menu cursor + direct mnemonics.

Keys: `p`/`t`/`w`/`P`/`s` · `↑↓` menu/strip · `enter` fire/open session · `esc/h/←` back · overlays/daemon global.

### 4.5 PR PICKER

```
│ lola ▸ nori ▸ open PRs                                                          │
│ /oauth_                                                          6 open · 20s ago│
│    PR    TITLE                   AUTHOR   BRANCH             CI   REVIEW         │
│ ───────────────────────────────────────────────────────────────────────────── │
│ › #229  fix oauth token refresh  mreit    fix/oauth-refresh  ✓    ✓ appr        │
│   #231  cache layer               dlee     feat/cache         ✕    ○ req         │
│   #230  wip dark mode    [draft]  akim     feat/dark-mode     —    ○             │
│   #240  contrib fix     [fork]    ext      patch-1            ✓    ○             │
│ enter opens branch detached (run/test) · a agent-on-PR · r refresh · / · esc    │
```
Rows: `#num`, title, `author.login`, `headRefName`, CI pill (existing `checksState`), review pill; `[draft]`/`[fork]` badges. Sort by `updatedAt`; `AlreadyOpen` greys a branch with a live session. **`AgeSeconds`/`Stale` surfaced** in the header (folds protocol-races #12).

**Actions (folds invariant-violations #6/#10, protocol-races #12, tui #7/#12):**
- `enter` → `cmd=open` with `Ref=headRefName` → **detached** worktree + shell (`pr`-kind, `Agentless=true`). Copy says "detached (run/test)" — **not** "pushable" (fixes the mislabel).
- `a` → `cmd=openPr {Agent:true}` → **tracking** worktree + agent (pushable). Shown only when the `pr` kind is built (phase 5). **Fork guard:** if `headRepositoryOwner != base owner`, `a` refuses with "fork PR — open detached (enter) for run/test; push-back not supported" and the merged-PR TTL guard applies.
- `o` → open PR URL. **Decision (folds tui #12):** a small bounded socket command `openURL` that shells the platform opener **on the daemon** (keeps the client exec-free invariant literal). Not `xdg-open` client-side.
- `r` refresh (bypass TTL).

States: Loading spinner ("Fetching open PRs from acme/nori…") · Empty ("No open PRs — w to create / r") · Error ("Couldn't list PRs: `<sanitized>` — r retry", keep dimmed prior rows) · ConfigGap (no repo → "set owner/name via e") · PermState (gh auth → "run gh auth login, then r") · DaemonDown ("^r to start").

### 4.6 TICKET PICKER — with a real team-select first step

Folds tui-flow #2 (the flagship gap): a pollless project has no team, so the picker's first screen resolves one.

```
step 0 (only if project has no linear_team_id and no poll ProjectID):
│ lola ▸ nori ▸ tickets ▸ choose team                                            │
│ pick the Linear team to browse (saved to the project)                          │
│ › Engineering (ENG)                                                            │
│   Platform (PLT)                                                               │
│ enter select (saves linear_team_id) · / filter · esc back                      │

step 1:
│ lola ▸ nori ▸ tickets                                                          │
│ scope: ‹ mine ›  project  team  todo          /auth_          12 · 45s ago     │
│    ISSUE   TITLE                     STATE       ASSIGNEE  PRIORITY            │
│ › ENG-42  fix oauth token refresh    In Progress me        urgent             │
│   ENG-58  add auth rate-limit         Todo        me        high               │
│ enter → worktree lola/eng-42 + claude · [ ] scope · r refresh · / · esc        │
```
The team-select reuses `meta.go`'s `Teams`/`Projects` pickers, **persists `linear_team_id`/`linear_project_id` onto the `[[project]]`** (client-side save), and has its own Loading/Empty/Error/PermState. Scopes: `mine` (assignee=viewer, team-scoped by default; cross-team offered as a variant with an inline "showing this team only" note if the daemon can't express it), `project`, `team`, `todo` (state types `backlog`+`unstarted`, resolved via freshly-fetched `States(team)` — **not** the disk cache, folds protocol-races #12).

Rows need the widened Linear query (`assignee`, `state{name,type}`, `url`) → `PickerIssue`. `enter` → `cmd=openTicket` (dispatch-safe ordering, §5). Seeding is launch-time only (`.lola/prompt.md` names the identifier; the agent fetches the issue itself — no untrusted body in the control loop).

States as above plus PermState "Linear API key not found (keychain/env names shown), set it then r".

### 4.7 MANUAL WORKTREE form

```
│ lola ▸ nori ▸ new worktree                                                     │
│   Branch   fix/flaky-login-test_                                               │
│   Base     main                    (default branch)                            │
│   Launch   ‹ agent (claude) ›   shell                                          │
│   Prompt   investigate intermittent login timeout in auth_test.go              │
│   → ~/.lola/worktrees/nori/lola-nori-fix-flaky-login-test                      │
│     git worktree add -b fix/flaky-login-test … origin/main                     │
│ ↑↓ field · space toggle launch · ^s create · esc cancel                        │
```
Client-side shape validation (no spaces/`..`/leading-dash); daemon owns real git validation. `^s` → `cmd=openManual`. Launch=agent field shown only when the manual-agent kind is built (phase 5); shell ships in phase 4.

**On success default (folds tui-flow #13):** push the scoped Sessions screen focused on the returned `SessionID` so create → land on the live agent/shell, not a menu.

### 4.8 SESSIONS (scoped + global)

Reuses `sessionsModel` verbatim (table/kanban, answer card, agent/shell embed, idle-gated `answer`). Scoped (from detail): `filter.Project` set, no PROJECT column, `esc`→detail. Global (from Home): PROJECT column shown, `esc`→Home. The answer card remains the only type-into-agent path.

### 4.9 Master keybindings

Global: `S` settings · `d` doctor · `^k` palette · `^r/^x` daemon · `esc/h/←` pop · `q` quit (confirm at Home) · `^c` quit/interrupt-child.
Home: `↑↓/kj g/G` · `enter/l/→` · `a e x space` · `s` global sessions · `o` sort · `/`.
Detail: `p t w P s` · `↑↓` · `enter`.
PR picker: `↑↓/kj g/G` · `enter` detached · `a` agent (gated) · `o` browser · `r` · `/`.
Ticket picker: `↑↓/kj g/G` · `enter` · `[`/`]`/`←→` scope · `r` · `/`.
Manual: `↑↓/tab` · type/`backspace` · `space` launch · `^s` · `esc`.
Sessions: existing 20-key set + `esc` pop.
Embed: `^q` unfocus · `^g` select-mode · else forwarded (unchanged).

### 4.10 Async state matrix

| Screen | Loading | Empty | Error | Perm/ConfigGap | DaemonDown |
|---|---|---|---|---|---|
| Home | brief vitals spinner; rows from cfg instant | "No projects — a" CTA | "Couldn't read config" keep rows | `⚠ missing`/`⚠ no polls` per-row | rows from cfg; counts `—`; vitals `daemon ✕ ^r` |
| PR picker | "Fetching open PRs…" | "No open PRs — w/r" | "Couldn't list PRs — r", dim prior | no repo → "set repo (e)"; gh auth → PermState | "Daemon not running — ^r" |
| Ticket picker | "Loading issues (scope)…" | "No issues — [ ]/r" | "Linear query failed — r" | no key → PermState (names only); no team → team-select step | "Daemon not running — ^r" |
| Manual | — | — | inline `✗ <daemon err>` | health red → verbs disabled + reason | submit flash "daemon down" |
| Sessions | spinner to first msg | "No sessions yet" | existing `dataErr` banner | — | existing banner |

---

## 5. Protocol & daemon

### 5.1 Wire shape

Add **one** field to `protocol.Request`: `Args json.RawMessage`. All existing flat fields and the 15 existing commands are byte-for-byte unchanged. New `Cmd` values: `projects | prs | tickets | openPr | openTicket | openManual | openURL`. **No `projectAdd`/`projectRemove`/`pollToggle`** — config edits are client-side (frozen foundation); keep existing `enable`/`disable`.

### 5.2 New commands

**`projects`** — cache-served, **zero execs** (like `sessions`). Joins `d.cfg.Projects` (under `d.mu`) + `d.status` tracker + `d.sessions.Snapshot()`. Adds the **per-project agent-health** bit computed from the same `runtimeHealth` inputs the status path already has (resolve `AgentForProject(name)`, PATH-probe once, cache).
```go
type ProjectsData struct{ Projects []ProjectInfo }
type ProjectInfo struct {
    Name, Path, Repo, DefaultBranch, Agent string
    AgentOK bool; AgentBin string                     // per-project agent health (folds tui #4)
    PollCount, PollsEnabled int; Polls []string
    LastRun time.Time; LastError string
    Sessions, LiveCounted, NeedsYou, OpenPRs int
    PathOK bool                                        // runtime os.Stat + .git (cached), NOT config's job
    RepoConfigured bool
}
```
`PathOK` uses a short-TTL cached `os.Stat`/`.git` probe so the hot cache-read path never execs git per project.

**`prs`** — one `gh pr list --state open --json number,title,author,headRefName,isDraft,mergeable,reviewDecision,statusCheckRollup,updatedAt,headRepositoryOwner`. New `scm.ListOpenPRs`; reuses `checksState`/`prRow→PR`. TTL cache + **singleflight per repo**, fetch on `context.WithoutCancel`+`prsExecTimeout` (folds protocol-races #7). `PrRow` includes `Branch=headRefName`, `Status=DeriveStatus(true,&pr)`, `IsFork` (from `headRepositoryOwner`), `AlreadyOpen`, `AgeSeconds`, `Stale`. Preconditions: project resolves + repo non-empty + gh on PATH (fail-closed — a gh error is never "no PRs").

**`tickets`** — `linear.ListIssues(TicketFilter)` (new; `Poll`-independent; makes the team clause optional; widened selection set → `PickerIssue`). `ensureLinear` resolves the key by name; a missing key returns a sanitized `"linear api key not configured"` (never the value). TTL cache + singleflight per query-sig on `WithoutCancel`. `todo` scope resolves state types → IDs against **freshly fetched** `States(team)`. `AlreadyLive` from `inflight.Has(uuid) || store has session` (UI-layer dedup hint; the hard guard is at open time).

**`openTicket`** — the invariant-critical one (folds invariant-violations #2, protocol-races #1/#4/#6). It must **reproduce the exact tick dedup ordering**, not a claim-only prefix:

```go
func (d *Daemon) handleOpenTicket(ctx, a OpenTicketArgs) (OpenTicketData, error) {
    // snapshot project/native/agentBin/poll under d.mu, release
    if err := d.runtimeHealth(agentBin); err != nil { return _, err } // gate: mutate nothing
    // Resolve EVERY enabled poll in this project whose filter this issue matches.
    // (A manual open of a matchable ticket MUST dedup exactly like a tick, per matching poll.)
    matched := d.pollsMatching(a.Project, a.UUID)
    // (a) ATOMIC claim + PERSIST SEEN before spawn, for the issue and every matching poll:
    if !d.inflight.Claim(a.UUID, a.Identifier) { return _, errAlreadyInFlight }
    for _, pl := range matched {              // seen-before-spawn crash guard, per matching poll
        if pl.DedupMode != "state" { d.seen.Set(pl.Name, a.UUID, now); }
    }
    if err := d.seen.SaveAll(); err != nil { d.inflight.Remove(a.UUID); rollbackSeen(); return _, err }
    // (b) spawn (WithoutCancel + nativeSpawnTimeout); rollback claim+seen on failure
    sess, err := nat.Spawn(cctx, *p, issueFrom(a)); if err != nil { rollback(); return _, err }
    // (c) label flip for each matching label-mode poll (re-read fresh + ApplyLabelDelta), record RemovedLabels
    // (d) writeBackSpawn per attributed poll
    sess.Kind = "linear"                       // stamped here is fine: Spawn returns a linear session
    d.sessions.Upsert(sess); d.recordBirth(sess); d.sessions.Save()
    return …
}
```
Key resolutions:
- **Seen persists before spawn** → a crash between spawn and label-flip is deduped on restart (fixes protocol-races #1/#4). If the issue matches no poll (a genuinely ad-hoc ticket), the in-flight claim + the live session are the guard and re-pickup after teardown is legitimate.
- **inflight leak on prune** (folds protocol-races #6): `PruneOlderThan` and every terminal-status transition **release the UUID claim**; additionally, each tick **reconciles the in-flight set against the store snapshot** (drop any claim with no live session) so a lost release can never wedge re-pickup forever.
- Budget is deliberately bypassed at creation (manual override) — see §5.4.

**`openPr`** — tracking (`--track`) worktree + agent (`Agent:true`) or detached shell (`Agent:false`, but that path stays `cmd=open`). Health-gate: full agent gate when `Agent`, git+tmux otherwise. **Atomic ID reservation** under the store lock before the long spawn (folds protocol-races #5): `Claim`-on-`ManualSessionID(project, "pr-"+slug)`; release on failure. Fork guard as in TUI #6/§4.5. Stamped `Kind=pr` by `finishLaunch`.

**`openManual`** — new-branch worktree + agent or shell. Same atomic ID reservation on `ManualSessionID(project,"open-"+branch)` for shell / a `manual-`-shaped ID for agent. `Kind=manual`. Shell = `Agentless=true`.

**`openURL`** — bounded daemon-side opener for the PR-browser action (keeps client exec-free).

### 5.3 Sync/async + execs table

| Cmd | Sync/async | Execs | Health-gate | Deadline | Cache | Push/pull |
|---|---|---|---|---|---|---|
| projects | sync | none | none | — | store+status+cfg snapshots | pull, 5s tick |
| prs | sync (blocks on miss) | `gh pr list` | project+repo+gh | prsExecTimeout 10s | TTL 20s + singleflight/repo (WithoutCancel) | on-demand |
| tickets | sync (blocks on miss) | Linear GraphQL | ensureLinear; team-or-me | ticketsExecTimeout 30s | TTL 45s + singleflight/sig | on-demand |
| openTicket | sync | git+post_create+tmux+agent; Linear iff matched polls | full dispatch gate; atomic UUID claim + seen-before-spawn | nativeSpawnTimeout 10m | — | mutation |
| openPr | sync | git fetch+`worktree add --track`+tmux+agent | full if Agent else git+tmux; atomic ID claim | nativeSpawnTimeout | — | mutation |
| openManual | sync | git `worktree add -b`+tmux+agent | full if Agent else git+tmux; atomic ID claim | nativeSpawnTimeout | — | mutation |
| openURL | sync | platform opener | — | 5s | — | mutation |

Existing 15 commands unchanged. `sessions` stays a pure reader (folds risk #12). All three `open*` handlers register in the `connWg` drain group and run the spawn on `WithoutCancel`+timeout so shutdown waits (like `pollOnce`).

### 5.4 Budget / global_cap decision (folds invariant-violations #3, protocol-races #8)

Manual agent opens (`openTicket`/`openPr(agent)`/`openManual(agent)`) **count toward `liveCounted`** (they occupy runners) but **bypass the `Budget` gate at creation** — a deliberate operator override, matching `lola open` semantics. To make the "manual throttles the poll" claim true during a long `post_create`, each `open*` handler **Upserts a placeholder `working` session record before the long spawn** (mirroring dispatch's immediate-upsert intent), reconciled by the observer once the real session is live. `global_cap` is **redefined and documented** (README + config reference) as *"the poll-dispatch ceiling; manually opened sessions over-subscribe it deliberately and throttle polls, never the reverse,"* and each over-subscription is logged. No silent contract flip.

### 5.5 Reload / worker diffing (folds risk #16)

Nesting changes the on-disk shape but **not** the in-memory `Config.Projects`/`Config.Polls`, so `syncWorkers`' `reflect.DeepEqual(w.poll, p)` and the native-rebuild `!DeepEqual(old.Projects, nc.Projects)` keep working unchanged — **provided** the three new `Project` fields participate in the DeepEqual (they do, being plain fields). `enable/disable` still target poll name (now globally unique). One addition: the native-rebuild trigger must also fire when a project's new `BranchPrefix`/`Linear*` fields change (covered by the existing `Projects` DeepEqual).

### 5.6 Concurrency summary

Two clients on `prs`/`tickets` → singleflight collapses to one exec. Dispatch vs `openTicket` same UUID → shared `inflight.Claim` test-and-set; loser skips. Two `openTicket` same UUID → one winner. Two `openPr`/`openManual` same branch → atomic ID claim, second refused. Config edits → flock-serialized client writes + re-read rebase. Shutdown → `WithoutCancel`+bounded execs + drain-group registration.

---

## 6. Runtime & launch

### 6.1 One discriminated entry point (`internal/runtime/launch.go`)

```go
type LaunchMode int
const ( LaunchIssue LaunchMode = iota; LaunchPR; LaunchManual )
type LaunchSpec struct {
    Mode LaunchMode; Project config.Project
    Issue linear.Issue         // LaunchIssue
    BranchRef string; Track bool // LaunchPR
    NewBranch, Base string      // LaunchManual
    Agent bool; Prompt string   // LaunchPR/LaunchManual (LaunchIssue always agent)
}
func (n *Native) Launch(ctx, spec LaunchSpec) (session.Session, error)
```
`Spawn`/`Open` become thin shims (keep existing tests/callers green). Every mode converges on **one shared tail** `finishLaunch(…, agent bool, kind agent.Kind)` — the **single stamper** of `Kind`/`Agentless` and the single place `writeAgentArtifacts` (hook settings) is called, structurally guaranteeing "hooks only when an agent launches":

```go
Prepare(dir); excludeLolaDir(dir); mkdir(.lola)
if agent {
    writeFile(.lola/prompt.md, prompt)   // promptMD (issue) OR caller prompt
    n.writeAgentArtifacts(dir, kind)     // hooks ONLY on this branch
    writeFile(.lola/env, envFile(...))   // LOLA_SESSION + LinearKey + project env
    cmd = launchCommand(id, kind, false)
} else {
    writeFile(.lola/env, manualEnvFile(p)) // project env only, NO key, NO LOLA_SESSION
    cmd = shellCommand()                   // plain shell, no hooks
}
Tmux.NewSession(id, dir, cmd); Tmux.ConfigureSession(...)
// session record: Kind/Agentless/Issue/Branch/Status per mode
```

### 6.2 Per-mode

- **LaunchIssue** = today's `Spawn`, byte-for-byte. `WT.Create` new branch off `origin/<DefaultBranch>` (prefix now `BranchPrefixForProject`). ID `lola-<proj>-<issue>`. `Kind=linear, Agentless=false`. Slot-counts.
- **LaunchPR.** ID `lola-<proj>-pr-<slug>` (**new infix**). `Track=false` → `CheckoutRef` (detached; today's `open`); `Track=true` → new `CheckoutTracking` (fetch + `worktree add --track -b <branch> origin/<branch>`; refuse `ErrBranchCheckedOut` if the branch is live elsewhere; **fork guard**: caller passes only same-owner branches — the daemon refuses fork PRs for tracking). Agent → prompt=`prBriefing`; shell → `Agentless=true`. Branch stored = real head branch (so `gh pr list --head` matches). `Kind=pr`. Agent slot-counts; shell = `shell` (no slot).
- **LaunchManual.** ID `lola-<proj>-open-<slug>` (shell) or a `manual-` shape (agent). `CreateFrom(p, id, NewBranch, base)` (new; base "" ⇒ today's logic). `freeManualSlot` errors on collision (human-named — no `-r` retry). `Kind=manual`. Teardown deletes the lola-owned branch.

### 6.3 worktree.Manager

Unchanged: `Create` (now delegates to `CreateFrom(…, "")`), `CheckoutRef`, `Prepare`, `Remove`, `guardRemovable`, `ErrDirty`. New: `CreateFrom(ctx,p,id,branch,base)`, `CheckoutTracking(ctx,p,id,branch)` + `ErrBranchCheckedOut`.

### 6.4 Session record carries its own worktree path (folds tui-flow #6, protocol-races #9)

To let `Kill` tear down a session **after its project is removed from config**, the session persists `Worktree string` (the absolute dir). `Kill` uses `s.Worktree` when the project no longer resolves, instead of refusing with `removeWorktree=false`. This turns "project removal leaks worktrees forever" into "removal is clean." `projectRemove` (client-side) additionally **counts live sessions in its confirm** and offers "kill N first" as the default.

### 6.5 Adopt / observer / Kill by kind

- **Adopt**: primary = persisted `Kind`/`Agentless`; store-loss backstop = ID infix (`pr-`/`open-`/issue-shaped) + `agentArtifactPresent(dir)` for agent-ness. Never kills/removes.
- **Observer**: `if s.Agentless { observeShell; continue }` (agent gate), then the agent+PR stack (works for `pr`/`manual` agents — keyed on branch/PR/pane, no Linear needed), then `if s.LinearBound() { … }` for every Linear write.
- **Kill**: tmux first; `branch=""` iff `!ownsBranch` (pr = never delete; linear/manual = delete their branch). Keyless sessions skip `clearLabelDispatch`/inflight-remove via existing `IssueUUID==""` guards.

### 6.6 Landmines closed in code

`writeBackState` gains `if !s.LinearBound() { return }` (defense-in-depth even with the observer gate — folds invariant-violations #5). `pollForSession` returns nil for non-`linear` kinds (kills the project fallback for keyless sessions). `revive` inflight re-arm guards `IssueUUID != ""`.

---

## 7. Session model & store

`internal/session/session.go` gains, all `omitempty`:
```go
Kind      Kind   `json:"kind,omitempty"`      // linear|pr|manual; "" resolved by EffectiveKind()
Agentless bool   `json:"agentless,omitempty"` // shell pane, no agent/hooks
Worktree  string `json:"worktree,omitempty"`  // absolute dir; teardown survives project removal
```
`Manual bool` retained (deprecated alias: `Kind!=linear && Agentless`) so legacy snapshots and any old code decode. `Source`/`AOStatus` keep round-tripping (folds risk #10). `load()` back-fills `Kind` via `EffectiveKind()` (fail-closed) for pre-Kind rows; corrupt/missing tolerance and empty-ID skip unchanged.

**Optional-by-kind** (frozen table §2). Linear-bound fields (`Issue/IssueUUID/Title/PollName/RemovedLabels/WB*`) are empty for `pr`/`manual`. Agent-bound fields (`AtPrompt/CIRetries/Escalated/Pending*`) apply to any `!Agentless` session. Git/PR fields (`Branch/Repo/PR/ReviewedPR/LastCodeRabbitAt`) apply to `linear`+`pr` agents.

**liveCounted** (folds invariant-violations #3, risk #5): unchanged — `Source=="native"` + status ∈ `nativeCountingStatuses`. `pr`/`manual` **agent** sessions carry `working`/`ci_failed`/… → count. Shells carry `shell` → don't. Read from the store snapshot only.

Round-trip test mirroring `TestAgentRoundTrip` covers `Kind`/`Agentless`/`Worktree` and legacy `Manual→manual` derivation.

---

## 8. Phased roadmap

Each phase is independently shippable and testable. **Phase 0 is the frozen contract and must land first** (the critiques' root-cause fix).

**Phase 0 — freeze the session contract.** Files: `internal/session/session.go` (Kind/Agentless/Worktree, `EffectiveKind` fail-closed, `LinearBound`, `Manual` alias, `load()` backfill); `internal/daemon/writeback.go` (`LinearBound` guard + `pollForSession` non-linear returns nil); `observer.go` (split the single `if s.Manual` into Agentless-gate + LinearBound-gate); `revive.go`/`kill.go` (branch-ownership by kind, `Worktree`-based teardown). DoD tests: round-trip incl. legacy `Manual`; observer routes shell/linear/pr correctly; `writeBackState` no-ops on `IssueUUID==""`; `pollForSession` project-fallback disabled for non-linear; Kill deletes manual branch / spares pr branch / tears down after project removed.

**Phase 1 — config nesting + migration.** Files: `internal/config/{config.go,validate.go}`. `fileProject`, flatten/re-nest, back-fill-only-when-empty + conflict error, global poll-name uniqueness, `BranchPrefix`/`Linear*` fields + resolvers, top-level `[[poll]]` deprecation notice, flock in `Save`. DoD: old config loads then round-trips nested; orphan poll stays top-level; nested explicit-project conflict errors; duplicate poll name across projects rejected; missing-file → defaults; flock serializes concurrent saves.

**Phase 2 — navigation stack + `projects` command + Home.** Files: `internal/tui/{app.go→rootModel stack, home.go, filterlist.go}`, `internal/protocol/protocol.go` (`Args`, `ProjectsData`/`ProjectInfo`), `internal/daemon/{server.go, projects.go, status.go}` (per-project agent-health + `PathOK` probe). Home renders from cfg, decorated by cache; add/edit/remove/toggle reuse existing config plumbing (client-side, flock). Current cockpit becomes the global Sessions screen. DoD: Home navigable with daemon down; `projects` execs nothing; per-project agent health correct for a codex project with claude installed; sort frozen between ticks; add-project saves with missing path → `⚠ missing`; remove-project confirm counts live sessions + dependent polls.

**Phase 3 — project detail.** Files: `internal/tui/project.go`. Composes cached `ProjectInfo` + action menu + reused live strip; health-line/verb-disable on per-project agent bit. DoD (model tests): verbs disabled when agent red; mnemonics + menu both fire; live strip pins by ID.

**Phase 4 — PR picker + manual-shell + openURL.** Files: `internal/scm/client.go` (`ListOpenPRs`, `prRow` fields incl. `headRepositoryOwner`), `internal/daemon/{prs.go, openurl.go, daemon.go seam}`, `internal/protocol` (`PrsArgs`/`PrsData`/`PrRow`), `internal/tui/prpicker.go`, `internal/worktree/manager.go` (`CreateFrom`), `internal/runtime/launch.go` (LaunchManual shell), `internal/daemon/openmanual.go` (shell only). `enter` → existing `open` (detached, labeled "run/test"); `w` shell. DoD: `ListOpenPRs` decodes rollup via existing `checksState`; singleflight collapses concurrent `prs`; fork rows flagged; TTL/stale surfaced; `Stale` served on expiry without a second exec; manual-shell creates new branch, Kill deletes it; atomic ID claim refuses a second same-branch open.

**Phase 5 — pr/manual agent kind (unlocks PR `a` + manual agent).** Files: `internal/worktree/manager.go` (`CheckoutTracking`+`ErrBranchCheckedOut`), `internal/runtime/launch.go` (LaunchPR agent + `finishLaunch` single-stamper), `internal/daemon/{openpr.go, openmanual.go agent path}` (atomic ID claim, placeholder upsert, fork refusal, budget-bypass + over-subscription log). DoD: pr-agent session slot-counts; Adopt recovers pr kind after store loss (persisted + `pr-` infix + artifact probe); observer runs agent+PR stack but zero Linear writes on a pr session sharing a project with a poll; fork PR refused for tracking, offered detached; global_cap over-subscription logged.

**Phase 6 — ticket picker + `openTicket`.** Files: `internal/linear/{iface.go,queries.go,filter.go,fake.go}` (`ListIssues`, optional team clause, `PickerIssue`), `internal/daemon/{tickets.go, openticket.go}`, `internal/protocol` (`TicketsArgs`/`TicketsData`/`TicketRow`, `OpenTicketArgs/Data`), `internal/tui/ticketpicker.go` (+ team-select step, persist `linear_team_id`). DoD: cross-team `mine` filter builds without a team clause; `todo` resolves state types via fresh `States`; **`openTicket` persists seen-before-spawn for every matching poll and Upserts before return** (order asserted via `Fake.CallLog` mirroring dispatch tests); crash-after-spawn-before-flip does not double-dispatch (seen present); dispatch vs openTicket same UUID → exactly one spawns; prune releases the inflight claim; tick reconciles claims against store.

**Phase 7 — polish.** Command palette `^k`, Activity strip/overlay, small-terminal variants for each stack screen, quit-confirm, `openURL` wired to PR `o`.

---

## 9. Risk register (folded, with mitigations)

| # | Risk | Mitigation (where in this plan) | Sev |
|---|---|---|---|
| 1 | Discriminator divergence → PR shell mis-routed into agent stack, leaks a slot | Frozen `Kind`+`Agentless`; observer Agentless-gate before Kind; drop `BranchOwned`/`scratch` (§0, §6.5) | Critical |
| 2 | `EffectiveKind` fails open → keyless agent runs Linear stack | Fail-closed derivation (empty Kind + empty UUID ⇒ pr); `LinearBound()` needs UUID; single stamper (§0) | Critical |
| 3 | `writeBackState`/`pollForSession` fire on empty UUID | Observer LinearBound-gate + in-function `IssueUUID==""` guard + non-linear fallback returns nil; landed in Phase 0 (§6.6) | Critical |
| 4 | `openTicket` skips seen-before-spawn → double-dispatch on crash/restart | Reproduce exact tick ordering: persist seen (per matching poll) before Spawn, Upsert before return (§5.2) | Critical |
| 5 | Silent `[[poll]]` loss on upgrade | `[[poll]]` stays a live compat table; flatten preserves; deprecation notice; orphans round-trip (§3.4) | High |
| 6 | Config lost-update across TUI/daemon | Single client-side writer + `flock` on config.toml (§0, Phase 1) | High |
| 7 | Manual agent opens breach `global_cap` silently | Count toward liveCounted, deliberate Budget bypass, placeholder upsert, **redefine+document** global_cap, log over-subscription (§5.4) | High |
| 8 | pr-agent mis-adopted as shell after store loss | Persist Kind + new `pr-` infix + artifact probe (§0, §6.5) | High |
| 9 | inflight claim leak on prune → ticket never re-picked | Prune/terminal transitions release claim; per-tick reconcile claims vs store snapshot (§5.2) | High |
| 10 | Fork PRs break tracking + observation | Picker flags `IsFork`; tracking refused for forks; detached fallback only (§4.5, §6.2) | High |
| 11 | Health-gate resolves default agent, not project's → false green | Per-project `AgentOK`/`AgentBin` in `ProjectInfo`; detail line + verb-disable key on it (§4.0, §5.2) | High |
| 12 | Home blank when daemon down/cold-start | Home renders from local cfg; daemon only decorates (§4.0) | High |
| 13 | Ticket picker unusable for pollless project (no team) | Team-select first step persisting `linear_team_id`; `ListIssues` optional team clause (§4.6, §6/Phase 6) | High |
| 14 | Reconcile can't resolve label config / sweeps keyless sessions | Reconcile per linear poll; only `LinearBound` sessions count; pr/manual never enter label-revert; fail-closed on unknowns (§6.6) | High |
| 15 | `sessions` loses exec-free guarantee | `prs`/`tickets` are separate bounded commands; `sessions` stays pure reader (§5.3) | High |
| 16 | singleflight leader inherits caller ctx → one disconnect fails all waiters | Fetch on `WithoutCancel` + own deadline (§5.2) | Medium |
| 17 | projectRemove orphans worktrees / un-manages sessions | Session persists `Worktree`; Kill uses it post-removal; remove-confirm offers kill-first (§6.4, §4.7) | Medium |
| 18 | Nested poll silently clobbers explicit `project` | Back-fill only when empty; conflict errors in Validate (§3.2) | Medium |
| 19 | Version rollback reintroduces the write-back landmine | Document downgrade-unsafe once keyless agent sessions exist (release note); Manual alias keeps old-daemon shell-routing for shells (§7) | Medium |
| 20 | Cache staleness → acting on merged PR / missing state | Surface `AgeSeconds`/`Stale`; refresh `States` before type-based ticket query; short TTLs (§4.5, §5.2) | Medium |
| 21 | Dead advertised keys (`a`, agent-mode) | Omit gated keys until capability ships; explicit message if ever shown-unavailable (§4.0) | Medium |
| 22 | Home re-sort reflow under reading user | Freeze order between explicit refreshes (§4.0) | Medium |
| 23 | Adopt/reload DeepEqual misses new project fields | New `Project` fields participate in DeepEqual; native-rebuild trigger covers them (§5.5) | Low |
| 24 | Small-terminal frame smear on new screens | Per-screen narrow variant, line-count-stable (§4.0, Phase 7) | Low |
| 25 | Secret leak via ticket command | Resolve key by name; sanitized errors; metadata-only payloads; launch-time prompt embedding only (§5.2) | Low |

---

## 10. Open questions for the user (each with a recommended default)

1. **Ticket-picker default scope.** Recommend **`mine`, scoped to the project's saved team**, with `[`/`]` to widen to team/todo/project. (Cross-team `mine` only if the daemon relaxes the team clause; until then show "this team only".)
2. **Does a manual agent open count against `global_cap`?** Recommend **yes to `liveCounted` (it occupies a runner), but bypass the Budget gate at creation** as a deliberate override; document `global_cap` as the poll-dispatch ceiling. Manual **shells** never count.
3. **Default base branch for manual worktrees.** Recommend **the project's `default_branch`** (already resolved, falls back to `main`), overridable per-form.
4. **Migration auto-rewrite?** Recommend **lazy-on-save only** (no `lola migrate`, no rewrite-on-load) — keeps `Load` pure and crash-safe. Old `[[poll]]` stays a working compat table indefinitely.
5. **`openTicket` for a ticket a poll can match.** Recommend **run the full per-matching-poll dedup** (seen-before-spawn + label/state flip + write-back), so a manual open is indistinguishable from a dispatched one and can't respawn-forever. Ad-hoc tickets (no matching poll) rely on the inflight claim + live session and are legitimately re-pickable after teardown.
6. **PR-picker `enter` default: shell or agent?** Recommend **detached shell** as the safe, always-available default (ships Phase 4); agent-on-PR is the explicit `a` upgrade (Phase 5). Never present a fork PR as agent-trackable.
7. **After a spawn, land on the session or the menu?** Recommend **push the scoped Sessions screen focused on the new session** (create → see your agent), overridable by a `[defaults].spawn_return = menu|session` preference.
8. **Config write ownership.** Recommend **client-side writes + `flock`** (daemon-down-safe, consistent with today's `projectForm`); do **not** add `projectAdd`/`projectRemove` socket commands.
9. **`branch_prefix` default.** Recommend **`"lola/"`** (moves today's hardcoded prefix into per-project config, unchanged behavior).
10. **Downgrade support once keyless agent sessions exist.** Recommend **declaring downgrade unsafe** in release notes rather than adding an old-daemon poison pill that would mis-route shells; the risk is confined to users who roll back a daemon while a `pr`/manual-agent session is live.
