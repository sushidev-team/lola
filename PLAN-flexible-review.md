# Flexible Review System — Implementation Plan

## Locked decisions (override anything below that conflicts)

1. **GitHub transport = PR COMMENT only.** The `github` sink posts `gh pr comment <pr> --repo <repo> --body-file -` (a plain PR issue-comment, always allowed on lola's own PR) with the untrusted body on **stdin** — never argv. Do NOT use `gh pr review`. Name the seam `scm.Client.PostPRComment` / daemon `d.postPRComment` (file `internal/scm/reviewpost.go`). Everything else about the sink stands: empty-body skip, per-PR settle guard `PostedGitHubPRs[kind]`, transient-vs-permanent classification (permanent stamps the guard + logs once, transient retries next cycle), `ghError` secret-scrub, `reactionExecTimeout` bound, fail-closed on missing repo / gh-not-authed, still forbidden on `coderabbit-watch` by validation. Update §5.2, §1.4, §6 Phase 2, and §8 test argv accordingly (`gh pr comment ... --body-file -`).
2. **Per-project provider selection (Phase 7) IS in scope** — implement it, sequenced LAST. Full `match_labels`-style bitmap discipline exactly as §6 Phase 7 specifies (`Project.Review []provKind` + `ProjectInherits.Review` + pointer mirror nil=inherit vs `[]`=override-to-none + `ResolveInheritance` normalize+clone + form Review tab + `inherit_test.go` identity tests). This is the highest-blast-radius part: mutate `p.Review` ONLY through the explicit override step that clears `Inherits.Review`.
3. **Legacy + catalog coexistence = HARD validation error**, resolved by the explicit `lola config migrate-review` command (one-way, opt-in). No silent auto-migration on load. (As already specified in §1.4 / §4.3.)

## 0. Goal & scope

Generalize lola's two hardcoded review tables (`[review]` CLI pass in `internal/daemon/review.go`, `[coderabbit]` PR-comment watch in `internal/daemon/coderabbit.go`) into a flexible system:

- **(a) Pluggable providers** — three KINDS: `coderabbit-cli` (exec `coderabbit review` in the worktree, per-PR guard), `coderabbit-watch` (poll the PR for bot comments, watermark guard), `claude-session` (NEW headless `claude -p` review leaf, per-PR guard). Extensible to more kinds.
- **(b) Multiple per session** — any subset of kinds runs for one session (cli + watch + claude concurrently).
- **(c) Fallback chains** — a provider that can't answer (unavailable / over-quota) advances to a configured fallback provider.
- **(d) Per-provider transports** — a multiselect over `{lola, github, linear}`. `lola` (default-on) = notify + worker send-keys. `github` (NEW) = post findings as a GitHub PR review via `gh`. `linear` = Linear comment.

**Constraints:** at most ONE provider per kind (keeps the single-func exec seam + test-install model, and lets guards key by kind). Providers are GLOBAL (daemon-wide) for Phases 0-6; per-project selection is Phase 7 (bitmap-touching, optional).

**Build/test only via the Makefile** (`make build`, `make vet`, `make test`, `make check`). Do not run bare `go build`/`go test`.

---

## 1. Config schema (real `config.toml`)

### 1.1 Legacy tables — UNCHANGED, still supported forever

`internal/config/review.go` and `internal/config/coderabbit.go` are left **100% unchanged** (struct, pointer mirror, `resolveReview`/`resolveCodeRabbit`, `reviewFile`/`coderabbitFile`). A config with only these keeps working:

```toml
[review]                                   # -> synthesized effective provider kind "coderabbit-cli"
enabled = true
command = "coderabbit review --plain --type all"
on_pr_open = true
send_to_agent = true
comment_on_linear = false
timeout_seconds = 300

[coderabbit]                               # -> synthesized effective provider kind "coderabbit-watch"
enabled = true
author = "coderabbitai"
notify = true
send_to_agent = true
comment_on_linear = false
```

### 1.2 NEW canonical: the global provider catalog

Nested array-of-tables under `[review]` (TOML allows `[[review.provider]]` sub-tables alongside `[review]` scalar keys; this is what lets us detect and reject a mixed file):

```toml
[[review.provider]]
provider        = "coderabbit-cli"          # coderabbit-cli | coderabbit-watch | claude-session
enabled         = true
on_pr_open      = true                       # cli/claude only; default true
command         = "coderabbit review --plain --type all"   # coderabbit-cli only
timeout_seconds = 300                        # cli/claude only; default 300
model           = ""                         # claude-session only (optional --model)
author          = "coderabbitai"             # coderabbit-watch only; default "coderabbitai"
transports      = ["lola", "github"]         # {lola (always present), github, linear}
notify          = true                       # lola sink: desktop/Slack notify; default true
send_to_agent   = true                       # lola sink: worker hand-off; default true
fallback        = ["claude-session"]         # ordered kinds tried when THIS provider can't answer

[[review.provider]]
provider        = "claude-session"           # headless `claude -p` review; here used only as cr-cli's fallback
enabled         = true
timeout_seconds = 300
transports      = ["lola"]
```

### 1.3 Transport model (fixes the legacy `notify=false` regression)

- Canonical resolved sinks: **notify**, **agent**, **github**, **linear**.
- `transports` is a multiselect over friendly tokens `{lola, github, linear}`. `lola` is **always present** (resolve appends it if missing) — the always-on internal transport. `lola` expands to the notify + agent sinks.
- The two per-provider bools `notify` (default true) and `send_to_agent` (default true) **refine the lola transport**: they mute the notify sink and the worker hand-off independently. This is what preserves the legacy `[coderabbit].notify=false` opt-out — synthesis maps `cc.Notify -> notify bool`, `cc.SendToAgent -> send_to_agent bool` verbatim, and `routeFindings` fires the notify sink only when the bool is true.
- `github`/`linear` are additive public sinks (opt-in), gated purely by token presence.

**Resolution defaults** (in `resolveReviewProviders`, mirroring `resolveReview` at `internal/config/review.go:96-125`): `transports` absent -> `["lola"]`, always force `lola` present; `notify`/`send_to_agent`/`on_pr_open` absent -> true; `timeout_seconds` absent -> `DefaultReviewTimeoutSeconds` (300, `internal/config/review.go:23`); `author` absent -> `DefaultCodeRabbitAuthor` ("coderabbitai", `internal/config/coderabbit.go:27`); `fallback` absent -> none.

### 1.4 Validation (new `validateReviewProviders`, wired at `internal/config/validate.go:240` next to `validateReview`)

Reject: unknown `provider` kind; **more than one provider of the same kind**; unknown `transports` token; `github` on a `coderabbit-watch` (its feedback is already on the PR — posting a review of it is meaningless and a self-feedback loop risk); `fallback` entry that is an unknown kind / same kind / not enabled / forms a cycle; `fallback` on a `coderabbit-watch` (watch cannot classify quota — see §3.4); `timeout_seconds < 0`; and — the mixed-config guard — **a non-empty catalog together with a non-zero legacy `[review]` or `[coderabbit]` table** (error message points at `lola config migrate-review`). Mirrors the `PrioritySortKeys` dup/unknown checks at `internal/config/validate.go:226-230`. `validateReview` (`:299`) stays as-is.

---

## 2. Provider abstraction

### 2.1 Two execution SHAPES behind three kinds

| kind | shape | seam (Daemon func-field) | guard | worker payload |
|---|---|---|---|---|
| `coderabbit-cli` | pass (sync findings) | `d.reviewRun(ctx, dir, base) (string, error)` (`daemon.go:172`) | `ReviewedPRs["coderabbit-cli"]` | full sanitized findings |
| `claude-session` | pass (sync findings) | NEW `d.claudeReviewRun(ctx, dir, base) (string, error)` | `ReviewedPRs["claude-session"]` | full sanitized findings |
| `coderabbit-watch` | watch (poll + watermark) | `d.coderabbitComments(ctx, repo, pr, since, author) (string, time.Time, error)` (`daemon.go:143`) | `ReviewWatermarks["coderabbit-watch"]` | single-line pointer |

The raw seams stay **single func-fields** (unchanged today for cli/watch; claude added). Because at-most-one-provider-per-kind is enforced, one seam per kind is sufficient and the existing `fakeReview.install`/`fakeCodeRabbit.install` pattern (swap `d.reviewRun`/`d.coderabbitComments` after `newDaemon`) keeps working.

### 2.2 Descriptor + registry (new `internal/daemon/reviewer.go`)

```go
type provKind string // "coderabbit-cli" | "coderabbit-watch" | "claude-session"
type provShape int    // shapePass | shapeWatch

type reviewProvider struct {
    Kind        provKind
    Shape       provShape
    Transports  config.TransportSet   // resolved canonical sinks (notify/agent/github/linear)
    Notify      bool                  // lola: fire notify sink
    SendToAgent bool                  // lola: fire worker hand-off
    Handoff     handoffStyle          // handoffFull (cli/claude) | handoffPointer (watch)
    Fallback    []provKind            // ordered fallback chain (pass shapes only)
    Author      string                // watch only
}
```

`d.reviewProviders []reviewProvider` (guarded by `d.mu`), built by `setReviewProvidersLocked(nc *config.Config)`:
- If `len(nc.ReviewProviders) > 0` -> build descriptors from the catalog and (re)build the exec CLIENTS: `d.review` / `d.reviewRun` from the cli entry's command/timeout (generalizes `buildReview`/`setReviewLocked`, `review.go:59-88`); `d.claudeReview` / `d.claudeReviewRun` from the claude entry's model/timeout; `d.coderabbitComments` stays the stateless scm seam.
- Else **synthesize** from legacy `nc.Review`/`nc.CodeRabbit` (behaves exactly as today).
- Called from `Run` (like `setReviewLocked` at `daemon.go:348`) and `handleReload` (`server.go:448`).

**Late binding (fixes Design 1's test-seam hazard):** the descriptor does NOT capture the seam. `runProviderPass` / `runProviderWatch` look up `d.reviewRun` / `d.claudeReviewRun` / `d.coderabbitComments` under `d.mu` **at call time**, so a fake installed after `setReviewProvidersLocked` still wins.

**appliesIndependently:** a provider runs per-session only if enabled AND not referenced in any other enabled provider's `fallback`. A fallback-only provider (e.g. claude referenced by cli.fallback) runs ONLY when reached via the chain — this prevents the double-review/double-hand-off the critiques flagged.

### 2.3 New leaf `internal/reviewclaude` (claude-session)

Mirrors `internal/brain/brain.go` structure but wears `review.Client`'s signature (do NOT extend `brain` — its "never feed the worker" doc must stay true):

```go
type Client struct { Bin, Model string; Timeout time.Duration }
func (c *Client) Review(ctx context.Context, worktreeDir, baseBranch string) (string, error)
func (c *Client) Available() bool
```

- Package-level exec seam `runClaude` (like `brain.go:128`): `claude -p <review-instruction> --output-format text` (+ `--model` when set) via `buildArgs` (`brain.go:150`), `cmd.Dir = worktreeDir` (like `review.go:151`), the precomputed `git diff <base>...HEAD` piped on **stdin** (approach A — bounded, offline-testable, no tool grant; `cmd.Stdin` like `brain.go:133`), `cmd.Env` nil so auth inherits (`brain.go:138`).
- Sizes: ~300s default (review-sized, NOT brain's 120s), ~16KB output cap (`review.go:52`), larger stdin diff cap than brain's 12KB.
- Fixed review instruction (our own text — attacker cannot inject; the diff on stdin is data claude reads, never executed). Findings claude returns ARE untrusted (diff-derived) — handled downstream by `routeFindings`.
- Sentinels `ErrNotFound`/`ErrTimeout`/`ErrAuth`/`ErrQuota`; `classifyRunErr` scans stderr **and the captured stdout head** (claude may print a limit line to stdout and exit 0). `Available()` = `exec.LookPath`.

### 2.4 Over-quota / availability seam driving fallback

- Add `ErrQuota` sentinel + `looksLikeQuotaError` to **`internal/review`** (near `review.go:73-82`/`:200`) and to `internal/reviewclaude`. Conservative, secret-scrubbed cues: `{"out of reviews","usage limit","rate limit","rate_limit","quota","429","too many requests","exceeded","insufficient","credit balance"}`.
- `internal/review` currently classifies stderr only (`classifyRunErr`, `review.go:171`). Thread the captured **stdout head** into classification so a quota message printed to stdout (exit 0 or nonzero) is detected. `runReview` (`review.go:146`) already buffers stdout in a `cappedBuffer` — pass its head to `classifyRunErr`.
- **Fallback set = `{ErrNotFound, ErrTimeout, ErrQuota, unavailable}`** (a provider whose `Available()` is false, i.e. binary missing). `ErrAuth` and `ErrExit` are a **graceful skip that does NOT fall through** (fail-closed: auth is an operator fix; a real exit error must not silently burn the paid fallback).

---

## 3. Fallback + guards + budget

### 3.1 Chain execution (`runReviewChain`, in `reviewer.go`)

Per appliesIndependently pass-provider, in order:
1. **Stamp the chain guard BEFORE any exec:** `ReviewedPRs[primary.Kind] = s.PR.Number` via `d.sessions.Update` (preserves the crash-safety at `review.go:212-221`). Keyed on the PRIMARY kind so a fell-through fallback does not re-fire next cycle.
2. Attempt list = `[primary] + primary.Fallback`. Skip an entry whose client `Available()` is false (advance). Run the first available entry's seam.
3. `err == nil` -> route findings under the **PRIMARY's** transports (documented: fallback delivery uses the primary's sinks; to get claude's github post, configure github on cr-cli). STOP.
4. err in the fallback set -> advance to the next entry.
5. err = `ErrAuth`/`ErrExit` -> graceful skip, STOP (no fallback), guard left set, logged once.
6. Chain exhausted -> graceful skip, guard left set, logged once (matches `buildReview==nil` skip at `review.go:71`).

Watch providers (`coderabbit-watch`) have NO chain (see §3.4); they run `coderabbitWatch` (generalized) directly.

### 3.2 Fire-once guards — session state (fixes the highest-blast-radius migration)

Replace the two scalars with kind-keyed maps in `internal/session/session.go` (keep the four legacy fields parseable for migration + one release of rollback):

```go
ReviewedPRs      map[string]int       `json:"reviewed_prs,omitempty"`      // kind -> last PR reviewed (pass shapes)
ReviewWatermarks map[string]time.Time `json:"review_watermarks,omitempty"` // kind -> watermark (watch)
PendingHandoffs  map[string]string    `json:"pending_handoffs,omitempty"`  // kind -> deferred worker text
PostedGitHubPRs  map[string]int       `json:"posted_github_prs,omitempty"` // kind -> PR the github sink has SETTLED (success or permanent-fail)
```

`migrateReviewState()` (idempotent, run on Store load AND in Adopt): if the maps are nil, fold `ReviewedPR -> ReviewedPRs["coderabbit-cli"]`, `LastCodeRabbitAt -> ReviewWatermarks["coderabbit-watch"]`, `PendingReviewFindings -> PendingHandoffs["coderabbit-cli"]`, `PendingCodeRabbit -> PendingHandoffs["coderabbit-watch"]`. After the fold the maps are authoritative and **no code path reads the scalars again** (all reads go through the maps). Adopt (`daemon.go:791-794`) copies the four maps instead of the four scalars. The migration helper (§4) names synthesized providers with the same fixed kinds so guard keys carry over with no re-review.

### 3.3 Per-cycle budget (bounded + shutdown-shielded)

Keep the single shared `reviewCycleCtx` (`observer.go:147-156`, `daemon.go:174-182`) as the **outer** shutdown-abortable ceiling. Generalize the install gate from `reviewOn && rc.OnPROpen` to **"any pass-shape provider is enabled and on_pr_open"**. Each provider exec is ALSO self-bounded by its own `timeout_seconds` (via the client's own `context.WithTimeout`, `review.go:147` / reviewclaude), so no single exec (primary or fallback) can monopolize the shared budget — the chain runs sequentially, each entry capped by its own timeout. Watch stays per-call bounded by `reactExecTimeout` (`coderabbit.go:55`), no cycle budget (as today). Document: within one cycle a very slow chain may defer LATER sessions to the next cycle (they are not guard-stamped, so they retry) — existing behavior, acceptable.

### 3.4 Documented fallback limitation

Fallback is **pass-shape only**. GitHub-app CodeRabbit "out of reviews" arrives to the `coderabbit-watch` provider as a normal PR comment (non-empty findings, `err == nil`), which is classifier-undetectable, so a watch cannot trigger fallback. Validation forbids `fallback` on a `coderabbit-watch`. A user who wants quota->claude fallback must run the `coderabbit-cli` provider (whose exit/stderr carries the quota signal). This is called out in README + agent-rules.

---

## 4. Back-compat & migration (zero regression + Save/Load identity)

### 4.1 Config layer

- `ReviewConfig`/`CodeRabbitConfig` stay **comparable** (no slice fields added) so the `== (XConfig{})` zero-guards (`review.go:132`, `coderabbit.go:118`) still compile.
- New `config.ReviewProvider` + `config.TransportSet` (`[]config.Transport`) live on a SEPARATE `Config.ReviewProviders []ReviewProvider` field (near `config.go:219`), NOT nested in `ReviewConfig`.
- On disk: add ONE field `Provider []fileReviewProvider `toml:"provider,omitempty"`` to `fileReviewConfig` (`review.go:80`). `fileReviewProvider` is a pointer-per-field mirror (incl. `*[]Transport`, `*[]provKind`) — NON-comparable, so `reviewProvidersFile` uses **len-based emptiness** (`len==0 -> nil`), never `==`.
- **`resolveReview` refinement (fixes Design 2's Save/Load bug):** add `hasLegacyReviewScalars(fr)` — if all six legacy scalar pointers are nil, return the ZERO `ReviewConfig` even when `fr.Provider` is set, so a catalog-only file does NOT materialize a spurious `TimeoutSeconds=300` legacy block on Save.
- `config()` (`config.go:666`) also calls `resolveReviewProviders(fc.Review.Provider)`. `file()` (`config.go:704`) composes ONE mirror:

```go
func (c *Config) reviewMirror() *fileReviewConfig {
    scal := reviewFile(c.Review)                       // nil when c.Review == zero
    provs := reviewProvidersFile(c.ReviewProviders)    // nil when empty
    if scal == nil && provs == nil { return nil }      // omit whole [review] table
    if scal == nil { scal = &fileReviewConfig{} }      // catalog-only: nil scalars
    scal.Provider = provs
    return scal
}
```

This keeps: legacy-only files byte-identical (`scal` set, `provs` nil); catalog-only files emit `[[review.provider]]` with nil scalars; the existing 5 `[review]` + 5 `[coderabbit]` config tests stay green unmodified as the identity oracle.

### 4.2 Effective providers at load

`Config.EffectiveReviewProviders()` derives the runtime provider set at read time (like `AgentForProject`/`EffectiveCap` resolve at read time, `config.go:824-827`): if `ReviewProviders` non-empty, use it; else synthesize from `Review`/`CodeRabbit` (cli from `[review]`, watch from `[coderabbit]`), preserving the resolve ergonomics (`on_pr_open`/`send_to_agent` follow Enabled, `comment_on_linear` off). Never serialized. `setReviewProvidersLocked` consumes this.

### 4.3 Explicit migration (no silent-disable cliff)

- Mixed legacy+catalog is a HARD validation error (§1.4).
- `config.MigrateLegacyReview(c)` + hidden `lola config migrate-review`: synthesize catalog entries from the legacy tables — `[review] -> {provider:"coderabbit-cli", transports: ["lola"] + (comment_on_linear?["linear"]:[]), notify:true, send_to_agent:rc.SendToAgent, command, timeout, on_pr_open}`; `[coderabbit] -> {provider:"coderabbit-watch", transports: ["lola"] + (comment_on_linear?["linear"]:[]), notify:cc.Notify, send_to_agent:cc.SendToAgent, author}` — then CLEAR the legacy tables. One-way, opt-in, its own identity test. Guard keys (`coderabbit-cli`/`coderabbit-watch`) match the synthesized names so no re-review after migration.

### 4.4 GitHub self-feedback loop guard (bounded — no per-cycle exec)

Only relevant when BOTH a github-transport pass provider AND a `coderabbit-watch` are configured. In `setReviewProvidersLocked`, resolve the gh authenticated login ONCE via new `scm.Client.AuthedLogin(ctx)` (`gh api user --jq .login`), memoize it on the Client (sync.Once / cached field), store on the daemon, and pass it to `coderabbitComments` to filter out self-authored comments (`internal/scm/coderabbit.go`). If resolution fails, skip the filter (fail-open — the default `author="coderabbitai"` already won't match lola's gh login). This adds ZERO per-cycle execs, preserving the single-gh-call-per-cycle invariant (`scm/coderabbit.go:11-13`).

---

## 5. Transport dispatch + the NEW github sink

### 5.1 Unified `routeFindings` (`reviewer.go`, replaces `routeReviewFindings` review.go:259 + `routeCodeRabbit` coderabbit.go:92)

`d.routeFindings(ctx, s, p reviewProvider, findings string)`:
- `findings == ""` (clean): fire the notify sink Info "no issues" (if `p.Notify`), then RETURN — skip agent/linear/github (github especially: `gh` rejects an empty body).
- else, per resolved sink:
  - **notify** (if `p.Notify`): `d.notifier.Notify` Action with `reviewHead(findings, reviewNotifyHeadBytes)` (`review.go:283`). Human sink, full text safe, no sanitize. Titles/preambles are **per-kind** (see §5.4) so a claude-session's findings are never mislabeled "CodeRabbit".
  - **agent** (if `p.SendToAgent`): the EXISTING gated hand-off, generalized. `handoffFull` -> full sanitized findings (`sendReviewToAgent`, `review.go:320`); `handoffPointer` -> single-line pointer (`sendCodeRabbitToAgent`, `coderabbit.go:160`). Reuses `sanitizeAgentText` + atomic `AtPrompt` consume + defer-never-drop VERBATIM. Pending stash keyed `PendingHandoffs[p.Kind]`.
  - **linear** (token present): `commentOnLinear` (unify `commentReviewOnLinear` review.go:403 / `commentCodeRabbitOnLinear` coderabbit.go:238) via `d.ensureLinear()` + `CreateComment`. Human sink, no sanitize.
  - **github** (token present, pass shapes only): `d.postPRReview` (§5.2).

Only the **agent** sink sanitizes + idle-gates; notify/linear/github get full untrusted text (opposite treatment — `review.go:291-302`).

### 5.2 github sink — new gh WRITE (first in the repo)

- New `internal/scm/reviewpost.go`: `func (c *Client) PostPRReview(ctx, repo string, pr int, body string) error` running `gh pr review <pr> --repo <repo> --comment --body-file -` with `body` on **stdin** (untrusted, multi-KB, newline-laden — never argv). `--comment` = neutral COMMENTED review (never `--approve`/`--request-changes`). Needs a small stdin variant of `run` (`reaction.go:65` sets no `cmd.Stdin`); reuse `resolveBin` (`reaction.go:49`) + `reactionExecTimeout` + `ghError` scrub (`reaction.go:82`) -> secret discipline for free. Empty body -> return nil without exec. See §Open-Question re self-review: classify 422/403 as PERMANENT and (fork) optionally fall back to `gh pr comment <pr> --body-file -`.
- New Daemon seam `d.postPRReview func(ctx, repo string, pr int, body string) error` set in `newDaemon` (~`daemon.go:251`) to `scmc.PostPRReview`; tests install a counting fake (no gh-write fake exists today).
- **Idempotency + permanent-fail (fixes per-cycle spam):** the github sink no-ops when `PostedGitHubPRs[p.Kind] == s.PR.Number`. On a SUCCESSFUL post OR a PERMANENT gh error (422/403/no-write-permission), stamp `PostedGitHubPRs[p.Kind] = s.PR.Number` and log ONCE. On a TRANSIENT error (5xx/timeout/missing repo via gitrepo `""`), leave it unstamped, log once, retry next cycle. Body bounded (~16KB), NOT `sanitizeAgentText`'d.
- Fail-closed: missing repo / gh-not-authed -> skip silently.

### 5.3 Self-feedback: see §4.4 (memoized login filter). `github` is validated off `coderabbit-watch`, so a lola-posted review is never re-ingested by its own watch.

### 5.4 Per-kind labels (fixes "CodeRabbit" mislabel leak)

Replace hardcoded "CodeRabbit" literals in the route/handle code (`review.go:273,414,485,491`; the `config.ReviewNotifyTitle`/`ReviewToAgentPreamble` and `config.CodeRabbitNotifyTitle`/`CodeRabbitAgentPointerFmt` consts) with per-kind label consts selected by `p.Kind`, so claude-session findings read "Claude review", coderabbit read "CodeRabbit", etc. Keep the existing consts as the coderabbit-kind values.

---

## 6. Phased, file-by-file change list

### Phase 0 — quota classification in `internal/review` (isolated, low risk)
- `internal/review/review.go`: add `ErrQuota` (near `:73`) + `looksLikeQuotaError`; thread the captured stdout head into `classifyRunErr` (`:146,:171`) so quota is detected on stdout too.
- `internal/review/review_test.go`: add ErrQuota cases (stderr + stdout cues; a normal `ErrExit` is NOT quota). Keep existing buckets green.

### Phase 1 — new leaf `internal/reviewclaude`
- `internal/reviewclaude/reviewclaude.go`: NEW (§2.3).
- `internal/reviewclaude/reviewclaude_test.go`: NEW (§8).

### Phase 2 — new scm write + self-login
- `internal/scm/reviewpost.go`: NEW `PostPRReview` + stdin `run` variant + `AuthedLogin` (§4.4, §5.2).
- `internal/scm/coderabbit.go`: optional self-login filter (author == authed login).
- `internal/scm/reviewpost_test.go`: NEW (argv, body-on-stdin-not-argv, empty-body-skips, distinct sentinel, secret-scrub, size-bound). `internal/scm/coderabbit_test.go`: add a self-login-filter case.

### Phase 3 — config catalog
- `internal/config/reviewprovider.go`: NEW — `ReviewProvider`, `Transport`/`TransportSet`, `provKind` enums + per-kind label consts; `fileReviewProvider` pointer mirror; `resolveReviewProviders` (defaults, force-lola); `reviewProvidersFile` (len-based emptiness); `EffectiveReviewProviders()`; `MigrateLegacyReview`.
- `internal/config/review.go`: add `Provider []fileReviewProvider` to `fileReviewConfig` (`:80`); add `hasLegacyReviewScalars` + refine `resolveReview` (`:96`); scalar path UNCHANGED.
- `internal/config/config.go`: add `Config.ReviewProviders` (`:219`); `config()` resolves it (`:666`); `file()` uses `reviewMirror()` (`:704`).
- `internal/config/validate.go`: `validateReviewProviders` wired at `:240` (§1.4).
- `internal/config/config_test.go` + `reviewprovider_test.go`: keep the 5+5 legacy tests green unmodified; add catalog round-trip, legacy->effective synthesis, mixed-config rejection, catalog-only Save/Load identity, `MigrateLegacyReview` identity, defaults, fresh-omits.

### Phase 4 — session guards
- `internal/session/session.go`: add the four maps + `migrateReviewState()`; keep legacy scalars parseable.
- `internal/session/session_test.go`: JSON round-trip of maps; idempotent fold from legacy scalars.

### Phase 5 — daemon (the heart)
- `internal/daemon/daemon.go`: add seams `d.claudeReviewRun`, `d.postPRReview`, cached authed login; add `d.reviewProviders`, `d.claudeReview *reviewclaude.Client`; wire raw seams in `newDaemon` (~`:251`); call `setReviewProvidersLocked` in `Run` (`:348`); Adopt carries the four maps + `migrateReviewState` (`:791-794`).
- `internal/daemon/reviewer.go`: NEW — descriptor, `setReviewProvidersLocked` (catalog or legacy synth, late-bound seam lookup), `appliesIndependently`, `runReviewChain`, generalized `routeFindings`, generalized `sendToAgent`/`deferHandoff`/`flushPending`/`commentOnLinear`/`postPRReviewSink` keyed by kind, per-kind labels.
- `internal/daemon/review.go` + `internal/daemon/coderabbit.go`: absorb trigger/route/guard-keying into `reviewer.go`; keep the send-keys/defer/flush/linear helper BODIES (renamed generic, kind-keyed); keep `handleReview`/`handleCodeRabbit` force paths (`useCycleBudget=false`, zero-since) delegating to the kind's provider.
- `internal/daemon/observer.go`: replace the `reviewOnPROpen`/`coderabbitWatch`/two-flush block (`:344-353`) with a loop over `appliesIndependently` providers + a flush loop; generalize the budget gate (`:147`) to "any pass provider enabled+on_pr_open".
- `internal/daemon/server.go`: `handleReload` (`:448`) calls `setReviewProvidersLocked(nc)`.
- Tests: `internal/daemon/review_test.go`, `coderabbit_test.go` port to the unified path; add multi-provider, fallback, transport, github, migration, Adopt tests (§8).

### Phase 6 — protocol / CLI / TUI / desktop
- `internal/protocol/protocol.go`: add optional `Request.Provider string`; keep `ReviewData`/`CodeRabbitData` for aliases; update Cmd doc-enum (`:73`).
- `main.go`: `lola review <session> [--provider kind]`; keep `lola coderabbit` as a back-compat alias forcing the watch kind; add hidden `lola config migrate-review`.
- `internal/tui/client.go`: collapse render toward one `ReviewData` (keep both cases).
- `internal/tui/settingsform.go`: the `stCodeRabbit` tab becomes a per-kind provider editor (kind rows, transports multiselect via `setPicker(multi)` over `{lola,github,linear}`, `notify`/`send_to_agent` bools, fallback picker); `enableDefaults` (`:402`) + `save()` (`:1081`) list-driven; keep legacy sections read-only with a "migrate" action.
- `internal/tui/sessions.go`: keep `c` (coderabbit) alias.
- `desktop/configsvc.go`, `daemonsvc.go`, `frontend/src/lib/views/SettingsForm.svelte`, `store.svelte.ts`: flat `review*`/`cr*` DTO -> provider array with get/set loops; two static sections -> rendered provider cards (kind select, transport checkbox group incl. github, fallback select); keep `review()`/`coderabbit()` as aliases; `Promise.allSettled` degradation for a stale daemon answering a new cmd.

### Phase 7 — (OPTIONAL, LAST) per-project selection via the inheritance bitmap
Only if per-repo variation is required. Adds an inheritable `review = [kinds]` key following the `match_labels` discipline EXACTLY:
- `config.go`: `Project.Review []provKind` (resolved) + `ProjectInherits.Review bool` (`:76-86,158`); `fileProject.Review *[]provKind` (`:352`-style pointer, nil=inherit vs `[]`=override-to-none); `projectFromFile` derives `Inherits.Review = !hasReview` (`:476`); `projectToFile` omits when the bit is set (`ptr(p.Review, set(o.Review))`, `:557`); `Defaults.Review []provKind` + `fileDefaults.Review`; `ResolveInheritance` normalize (`in.Review = in.Review || p.Review == nil`, `:831`) + clone (`if in.Review { p.Review = slices.Clone(d.Review) }`, `:862`) — idempotent.
- **Bitmap trap preserved:** both form layers mutate `p.Review` ONLY through the explicit override step that CLEARS `Inherits.Review`, else `projectToFile` omits the key and Save silently discards the write. Bitmap zero-value stays "fully explicit".
- `validate.go`: a project's `review` kinds must exist+be enabled in the catalog.
- `form.go`: add a Review tab (`:82-95`) + register `Inherits.Review` in the inheritable map (`:110`).
- `inherit_test.go`: zero==explicit, inherited value never frozen on Save, mutate-without-clearing-bit caught, `ResolveInheritance` idempotent (Load->Save->Load identity).

### Phase 8 — docs
- `README.md`: command rows (`:111-112`); rewrite `[review]`/`[coderabbit]` sections (`:500-575`) into providers + transports (github/lola/linear) + fallback + migration + the watch-quota limitation.
- `config.example.toml`: add commented `[[review.provider]]` catalog; keep the legacy blocks (`:165-210`) with a migration note.
- `agent-rules.md`: extend the internal-helpers + slot/status rules (`:95-105`) with provider/fallback/transport rules, each new guard, and the watch-quota + self-feedback notes, marked as deltas.

---

## 7. How each hard invariant is preserved

- **Config inheritance-bitmap Save/Load identity** — untouched in Phases 0-6 (providers are GLOBAL, not on `Project`). Phase 7, if taken, follows the `match_labels` pointer-mirror + Inherits-bit + `ResolveInheritance` discipline verbatim (§Phase 7); the legacy `[review]`/`[coderabbit]` round-trip is guarded by keeping their structs comparable and the 5+5 tests green as the oracle, plus the `reviewMirror()`/`resolveReview` refinement for the catalog-only case.
- **Per-provider one-shot guards** — kind-keyed maps (`ReviewedPRs`/`ReviewWatermarks`/`PostedGitHubPRs`), stamped before the exec (chain guard on the primary kind), migrated idempotently from the scalars and carried through Adopt. Each kind fires once independently; a fallback runs under the primary's key so it never re-fires; a fallback-only provider stamps nothing of its own (no double-fire).
- **Send-keys safety** — the agent sink reuses `sendReviewToAgent`/`sendCodeRabbitToAgent` VERBATIM: `AtPrompt` atomic consume via `Store.Update`, `sanitizeAgentText` (CR/control strip), defer-never-drop, never-run-as-command, no rollback on send error.
- **Untrusted output out of the control loop** — notify/linear/github are human sinks (full text, no sanitize, never re-fed as control); only the agent sink sanitizes+gates. The github post is validated off `coderabbit-watch` and filtered by memoized self-login so it can't loop back.
- **Fail-closed / graceful skip** — unavailable/over-quota -> fall through or skip, logged once, guard left set, never an error per cycle; github permanent-fail stamps the settle guard (no spam); missing repo/gh-auth -> silent skip.
- **Secret discipline** — reviewclaude/review inherit daemon env (`cmd.Env` nil), stderr scrubbed via `redactSecrets`; github via `ghError` scrub; no credential in config/argv/log/error.
- **Shutdown-shielded + bounded** — shared `reviewCycleCtx` from `shutdownCtx` (outer abort), each exec self-bounded by its own timeout; watch bounded by `reactExecTimeout`; the github write runs under `reactionExecTimeout`; no new per-cycle gh exec (login memoized).
- **Zero regression** — legacy tables unchanged + synthesized to identical behavior (incl. `notify=false` via the notify bool); mixed config is a hard error with an explicit migration; existing 10 config tests stay green unmodified.

---

## 8. Test matrix (repo convention)

**Leaf (clone `internal/review/review_test.go` 5-bucket, package-level seam + `t.Cleanup`):**
- `reviewclaude_test.go`: argv (`-p ... --output-format text`, `--model`), `cmd.Dir=worktree`, diff-on-stdin (untrusted diff NOT executed), timeout, output trim/cap/rune-safety, sentinel classification incl. `ErrQuota` from BOTH stdout and stderr, secret scrub, `Available()`.
- `review_test.go`: `ErrQuota` on stderr and stdout head; a non-quota `ErrExit` is NOT quota.
- `scm/reviewpost_test.go`: `gh pr review <pr> --repo <repo> --comment --body-file -` argv, body-on-stdin-not-argv, empty-body-skips, distinct gh-error sentinel, secret-scrub, size-bound. `scm/coderabbit_test.go`: self-login-filtered comment dropped.

**Config (reproduce the 5-case convention, `config_test.go:1354`):** catalog default-off-when-absent; enabled defaults (transports->`[lola]`+forced, notify/send_to_agent->true, timeout 300); explicit-kept (transports=`["lola","github"]`, `send_to_agent=false` survive); Save->Load identity incl. a disabling zero; fresh-config-omits `[[review.provider]]`. PLUS: legacy-only byte-identical round-trip (existing 10 tests unmodified); catalog-only round-trip (no spurious `[review]` scalars — the Design-2 bug); legacy->`EffectiveReviewProviders` synthesis identical incl. `notify=false`; mixed-config validation error; `MigrateLegacyReview` identity + guard-key continuity; `validateReviewProviders` rejects unknown/dup kind, github-on-watch, bad/cyclic fallback.

**Session:** JSON round-trip of the four maps; idempotent `migrateReviewState` fold from legacy scalars; Adopt carries the maps.

**Wiring (`newTestDaemon` `tick_test.go:66` + `fakeReactSeams` `reactions_test.go:29`; install fakes on `d.reviewRun`/`d.claudeReviewRun`/`d.coderabbitComments`/`d.postPRReview`):**
- Per-provider: guard-before-exec ordering (`fr.onCall`); fire-once + re-fire on new PR (per-kind); busy->defer->flush per `PendingHandoffs[kind]`; clean->Info-only; disabled no-op; graceful skip on `ErrExit`/`ErrAuth` guard-left-set; force ignores guard; manual caller-ctx vs auto cycle-budget.
- **Multi-provider (new):** cli + claude + watch on one PR all run; independent per-kind guards; one firing never suppresses another; combined ordering under one budget; `liveCounted` unaffected.
- **Fallback (new):** primary `ErrQuota`/`ErrNotFound`/`ErrTimeout` -> fallback runs, findings route via the PRIMARY's transports; primary success -> fallback NOT run; primary `ErrExit`/`ErrAuth` -> graceful skip, no fallback; chain guard stamped once (no re-run next cycle); budget cancellation aborts the fallback; per-exec self-bound proven (a hung primary is killed at ITS timeout, fallback still runs).
- **Transports (new):** lola = notify + sanitized/idle-gated/deferred send-keys (reuse the CR/ANSI/NUL assertion `review_test.go:340`) with `notify=false` muting notify only; linear = `CreateComment` via `linear.Fake.CommentsByIssue`; github = `d.postPRReview` fake called once per PR, skipped when clean, idempotent, PERMANENT-fail stamps the settle guard (no re-call), human-sink-full-text (no sanitize); a github-on-watch config is rejected by validation.
- **Late-binding:** a fake seam installed AFTER `setReviewProvidersLocked` still takes effect (descriptor reads the seam at call time).
- **Back-compat:** a legacy-only config drives synthesized providers identically to today's tests; `migrateReviewState` + Adopt prove an upgraded/adopted session with old scalars is NOT re-reviewed.
- **Self-feedback:** with both a github pass provider and a watch, the authed login is resolved ONCE (no per-cycle second exec) and self-authored comments are filtered.

**Phase 7 (if taken):** `inherit_test.go`-style — bitmap zero==explicit, inherited `review` never frozen on Save, mutate-without-clearing-bit caught, `ResolveInheritance` idempotent (Load->Save->Load identity).
