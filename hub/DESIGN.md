# lola hub — design

`lola-hub` is the hosted, team-wide COORDINATOR for [lola](https://github.com/sushidev-team/lola)
daemons. It never executes anything and never holds code. Each developer's lola daemon stays the
authority over its own machine; the hub schedules, deduplicates, leases and records.

This document is the contract for the first build (Phase 1) and the map for what follows.

## Why it exists

lola was built single-player: one daemon, one machine, Linear as the shared state. Three things need
a party every daemon can reach:

1. **Team distribution of day-to-day chores** — security advisories, red dependency-bump PRs, the
   work that never makes it into Linear but has to be done by someone. Without a coordinator every
   teammate's lola fixes the same GHSA in the same repo.
2. **Shared, signed team settings** — one review-provider catalog, one budget, one merge-queue policy
   across the team, without each person hand-copying `config.toml`.
3. **Reaching a phone off-network** (later phases) — an App Store build's APNs key belongs to the
   publisher, so a per-daemon push sender cannot ship; and a phone off the daemon's network has no
   route without a relay.

This deliberately reverses the "no cloud backend, ever" line in lola's `mobile/PLAN.md`. The argument
there was uptime responsibility, which a team service takes on anyway. The other argument — a new
trust boundary — still holds, and the rules below are how it is kept.

## The one rule

**A compromised hub must not mean code execution on every teammate's machine.** Every decision below
follows from it.

- **Outbound only.** A daemon calls the hub over HTTPS. No daemon opens a port for the hub.
- **The hub never writes a prompt.** A work item is a closed `kind` enum plus fields, each validated
  against a strict pattern (`hubproto.Validate`). The daemon renders the agent's prompt from its OWN
  template. No free text from the hub reaches an agent.
- **The hub only routes signed policy; it cannot sign it.** Team settings are an ed25519-signed bundle.
  The admin keys that sign it are pinned in each daemon's local config (`[hub].trusted_policy_keys`),
  so the hub cannot substitute its own key.
- **Executing keys never travel.** `post_create`, `pre_remove`, `dev_commands`, `custom-cli` commands,
  `sandbox` images and anything else that is a shell line are rejected by the policy schema itself.
  A team-pushed `post_create` would be remote code execution on every member.
- **Hub work runs sandboxed by default.** `[hub].require_sandbox = true` (default) refuses a chore for
  a project without `sandbox = "docker"`. Opting out is a local, explicit decision.
- **The hub never merges, never pushes, never touches a repository.** It sees a PR URL after the fact.
- **The daemon decides what it accepts.** A chore is claimed only for a project that opted in
  (`[[project]].hub_chores = true`), only for an accepted kind, only under the daily cap, and only
  through the same slot reservation, `dispatchHold` and health gate as any Linear dispatch.

## What the hub may see

| The hub sees | The hub never sees |
|---|---|
| Org, members, device public keys | Code, diffs, file names, local paths |
| Capacity: free slots, installed agents | Pane text, transcripts, prompts |
| Repo slugs (`owner/name`) a member opted in | Review findings, brain summaries |
| Findings: advisory id, package, versions, severity, repo | The full dependency inventory |
| Claims and leases | lola session ids (they embed Linear identifiers) |
| Outcomes: PR URL, delivery state, done / failed | Plaintext phone traffic (later: relay) |

## Architecture

```
  lola daemon (dev A) ──┐   signed HTTPS (outbound)   ┌───────────────┐
  lola daemon (dev B) ──┼────────────────────────────▶│   lola-hub    │──▶ Linear (escalation only)
  lola daemon (dev C) ──┘                             │ store · lease │
                                                      └───────────────┘
```

### The contract: `hubproto`

The wire contract lives in the **lola** repository as a nested, dependency-free Go module,
`github.com/sushidev-team/lola/hubproto`. It is public so anyone can read what a daemon will accept,
even if a hub deployment is private. The hub imports it; lola's root module requires it through a
`replace ./hubproto`. It owns:

- **Request signing.** Every daemon request carries `Lola-Device`, `Lola-Timestamp`, `Lola-Nonce` and
  `Lola-Signature` headers. The signature is ed25519 over a canonical string:
  `method \n path \n timestamp \n nonce \n hex(sha256(body))`. The hub rejects a timestamp outside
  ±5 minutes and a nonce it has seen within that window.
- **Work-item kinds.** Closed enum, Phase 1:
  - `dep_advisory` — a security advisory affecting a dependency in a repo. Fields: `repo`, `ecosystem`,
    `package`, `vulnerable` (range: operator and version TOKENS only, never a word), `fixed` (version, may be empty), `advisory` (GHSA/CVE/OSV id),
    `severity` (`low|moderate|high|critical`). The daemon opens a NEW branch and asks the agent to
    upgrade to a fixed version, adapt the code, and make CI green.
  - `red_bump_pr` — an open dependency-bump PR (Dependabot / Renovate) whose required checks fail.
    Fields: `repo`, `pr` (number), `branch`, `package`, `author` (bot login). The daemon opens a
    session ON that existing branch (lola's `pr` session kind) and asks the agent to fix the fallout.
- **Dedup key** per kind: `dep_advisory` → `repo|advisory|package`, `red_bump_pr` → `repo|pr`.
- **Validation** (`Validate`) of every field against strict patterns: repo `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`,
  advisory `^(GHSA(-[23456789cfghjmpqrvwx]{4}){3}|CVE-\d{4}-\d{4,}|[A-Z]+-\d{4}-[A-Za-z0-9-]+)$`, package and
  version by conservative per-character patterns with length caps. A field that does not validate makes
  the whole item invalid — on BOTH sides: the hub refuses to store it, the daemon refuses to claim it.
- **Signed policy bundle.** `{org, version, issued_at, settings{…}, key_id, signature}`. `settings`
  is a closed struct of NON-executing keys only; unknown keys fail verification.
- **Team frames** (`teams.go`): create an org with a create code, org settings (status view,
  write-only secrets), invites, device revoke, the admin signature, and the invite and create-code
  strings (`lola-hub:<url>#<token>[#<key_id>:<pub>]`, `lola-hub-create:<url>#<code>`).
- **Frames**: enroll, heartbeat (capacity + repos + the policy version applied), report findings,
  list offers, claim, renew lease, progress (a PR is open), complete, fail, release, fetch policy, and
  three read-only views for the desktop app: the org board (`/v1/board`), the org (`/v1/org`) and the
  stats (`/v1/stats`). JSON over HTTPS, `/v1/…`, versioned by path. Every later addition is optional
  on the wire, so an old frame stays byte-identical.

### Hub (`lola-hub` repo)

- `cmd/lola-hub` — `serve`, `dev` (a local hub for testing in one command), `init` (org + console
  admin token in one step), `invite` (labelled enroll tokens printed as one-line invites), the
  offline `policy init|keygen|sign` and the local `admin` commands (org, enroll tokens, devices,
  items, policy, create codes). Admin commands operate on the store directly from the host; the API
  listener has no hub-operator endpoint (see "Self-service teams" for what an org's own admins
  reach).
- `internal/admin` — every administrative operation, once, shared by the CLI and the console.
- `internal/console` — the admin web console (`serve --admin-addr`). This revises the Phase 1 line
  "no admin HTTP endpoints at all": a team could not be set up without shell access to the hub host,
  and the console keeps the same power boundary. It is LOOPBACK ONLY (the bind address and the Host
  header are both checked, so neither a public bind nor DNS rebinding reaches it), it requires the
  token in `<data dir>/admin.token` (0600, created on first start) exchanged for an HttpOnly,
  SameSite=Strict session cookie, and every mutation is a POST with a same-origin Origin/Referer and
  a CSRF token. It renders with `html/template` and a strict CSP, with embedded assets only. It never
  signs a policy (it verifies a pasted bundle) and shows an enroll token once, on the response that
  created it.
- `internal/store` — a `Store` interface with a file-backed implementation (one JSON snapshot,
  atomic temp+rename, 0600, single process). Postgres is a later implementation of the same interface.
- `internal/lease` — pure lease state machine: `open → claimed → in_review → done | failed`; a
  claim carries a TTL (default 30 min) renewed by heartbeat; each failure increments `attempts`; at
  `escalate_after` attempts (default 2) or immediately for `critical` severity the item escalates.
  The revisions below (C1–C8) refine expiry, review and cooldown.
- `internal/api` — HTTP handlers, signature middleware, replay cache, per-device rate limit,
  bounded bodies (64 KiB).
- `internal/escalate` — creates ONE Linear issue per escalated FINDING (or fail group) through the
  GraphQL API, with the ORG's own key (stored sealed, opened only for the call, never a log line),
  and closes it when its items are done (C9). It also posts each org's optional daily digest to
  that org's own Slack webhook. Fail open: an escalation that cannot be sent is retried, the item
  stays escalated-pending; a digest that cannot be posted is skipped.
- `internal/secret` — seals per-org secrets with AES-256-GCM under `LOLA_HUB_SECRET_KEY` (env only).
  The additional authenticated data names the org and the field, so a sealed value cannot be moved
  to another org or field by editing the store.

### Daemon side (lola repo)

- `[hub]` config: `enabled`, `url`, `device_name`, `accept_kinds`, `max_claims_per_day` (default 4),
  `require_sandbox` (default true), `trusted_policy_keys`, `poll_seconds` (default 300).
  `[[project]].hub_chores` (default false) opts a project in. Neither is a `[defaults]` key: an
  inherited opt-in would enrol every project by accident.
- `internal/hub` — the client (signing, bounded requests), the device key
  (`~/.lola/state/hub-key`, 0600, ed25519; never in config, argv or a log), the prompt templates per
  kind, the findings collector, and policy verification.
- **Findings come from GitHub, not from an inventory upload.** For each opted-in project the daemon
  reads `gh api repos/{owner}/{repo}/dependabot/alerts?state=open` and the open PRs authored by
  `dependabot[bot]` / `renovate[bot]` with failing required checks, and reports only the validated
  fields above. The hub dedups across the team.
- `internal/daemon/hubwire.go` — one loop on the shutdown-shielded context, panic-guarded, every
  exec and request bounded: heartbeat → report findings → list offers → for each acceptable offer,
  `dispatchHold` + health gate + `reserveSlots` → claim → spawn → record `Session.HubItem`. Lease
  renewal rides the heartbeat for every live hub session; the outcome (PR URL + delivery) is
  reported when the observer sees it change; teardown or a failed spawn releases or fails the item.
- **An offer is re-read from GitHub before the claim.** The hub's item is a claim about a repo, so
  the daemon confirms it with the member's own `gh` and builds the session from GitHub's answer:
  a `red_bump_pr` must be OPEN, authored by the named bot, not cross-repository, on exactly the named
  branch and still failing (`gh pr view`); a `dep_advisory` must match an open Dependabot alert by
  advisory + package + ecosystem, and the briefing's range and fixed version come from that alert.
  Any doubt refuses the offer, unclaimed.
- CLI: `lola hub join <url>` (the one-time token on stdin, prompted without echo on a terminal, or
  `--token-file`; never in argv), `lola hub status`, `lola hub leave`.

## Lifecycle revisions (C1–C11)

Decided after the first build, from running it with a team. Every one is additive on the wire, with
one caveat: the hub decodes bodies with unknown fields refused, so a hub that predates C3 or C6
answers 400 to a fail carrying `pr_url` / `group` or a heartbeat carrying `policy_version`, and 404
to `progress`. A daemon facing an older hub leaves those fields out and completes on PR instead.

Fail reasons are a closed set: `spawn_failed`, `agent_gave_up`, `ci_failed`, `pr_closed`,
`timeout`, `refused`, `sandbox_required` (both local policy: reopen without counting an attempt),
`not_reproducible` (closes as failed, no escalation).

- **C1 `in_review`.** A daemon that opened a PR calls `POST /v1/items/{id}/progress` (`pr_url`,
  `delivery`): the lease holder moves the item `claimed → in_review` (lease dropped, the device
  recorded as OWNER); the owner may call it again to update the delivery. An in-review item is not
  offered and not reopened by re-reports; `complete` and `fail` come from the owner. With no progress
  call for 14 days it reopens. A daemon talking to an older hub falls back to complete-on-PR on 404.
- **C2 grace and max age.** The lease records `claimed_at`. An expired lease holds the item for its
  previous holder for 2× the TTL: offered to nobody else, and the holder's claim takes it back without
  a new attempt. Renewals stop at `--max-lease-age` (default 24h): renew fails and a `timeout`
  attempt counts.
- **C3 fail groups.** A fail may carry `pr_url` and a `group` (`^[a-z0-9]{8,32}$`). Items failed under
  one group escalate as ONE Linear issue listing them.
- **C4 history.** Each item keeps its last 20 attempts (`claimed`, `in_review`, `completed`,
  `failed`, `released`, `expired`, `reopened`, `closed_stale`) with device NAME, member, reason, PR
  and time. The board shows it with `last_fail`, `reopened`, `claimed_at` (claimed and in_review
  only), `owner` (in_review, done, failed and escalated only) and the issue URL; a row that breaks
  either rule fails `hubproto` validation and is dropped.
  A device id never leaves the hub.
- **C5 members.** An admin-set `label` on an enroll token (`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)
  becomes the device's `member`. Device names are unique per org (suffix `-2`, `-3`). Tokens are
  listed (never the secret: a hash-prefix id, label, created, expires, unused / used by / revoked /
  expired) and an unused one can be revoked; revoking also pulls its expiry back, so the same check
  that refuses an expired token refuses it.
- **C6 policy version.** The heartbeat reports the policy version the daemon applies; the org view
  and the console show it per device.
- **C7 stale close.** An open item not re-reported for `--stale-after` (default 48h) while some active
  device reports its repo closes as failed (`closed_stale`). No frame: silence from every member who
  can see the repo is the signal.
- **C8 cooldown.** A device that failed, released, refused or let an item lapse past grace is not
  offered it again for 6 hours.
- **C9 escalation.** The issue id is derived from org + dedup key (or fail group), not the item id,
  so a finding never gets a second issue. The issue URL is stored. A pending escalation of an item
  already done is dropped; sends go most severe first, then oldest; when every item of a sent issue
  is done the hub comments (PR + delivery, validated) and closes it. Admin `items resolve`
  (escalated → done) and `items reopen` (escalated → open, attempts reset; the issue stays). An item
  that went back on offer after its escalation was sent (a new episode) and escalates again is
  announced again; when its stable id names a closed issue, the hub comments and reopens it.
- **C10 grouped bumps.** A `red_bump_pr` may have an empty `package` (Dependabot's "bump the X
  group"); its dedup key is unchanged.
- **C11 stats.** `GET /v1/stats` returns the org's daily counters for 90 days (opened, done, failed,
  escalated, the sum of fix seconds). Counters only, no item data.

Setup is two commands per machine: `lola-hub init` on the host, `lola-hub policy init` on the
admin's machine, then `admin policy publish --register-key` and `lola-hub invite`. An invite is
`lola-hub:<url>#<token>[#<key_id>:<pub>]`; its key comes ONLY from the admin's `--trusted` flag and
is never read from the store, so a compromised hub cannot pick the key a member pins. `admin policy
publish --register-key` registers the signer and publishes in one transaction, and still verifies the
signature: the hub cannot sign.

## Self-service teams

Setting a team up by CLI on the hub host kept the hub from the people it serves, and one hub-wide
Linear key meant every org shared a workspace. So a member can create a team and a team's admins can
run it from the Lola app, within these rules:

- **Team admin = a key, not an account.** Creating a team generates an ed25519 admin key on the
  creator's machine. It is the key that signs the team's policy and the key members pin. The hub
  stores the public half in `Org.AdminKeys`.
- **Create codes gate creation.** `POST /v1/orgs` needs a one-time code the operator minted (stored
  hashed, single use, expiring). The request is signed by the device key and by the admin key it
  registers, which proves possession. A public hub is not open signup.
- **Open creation is the operator's switch.** `serve --open-teams` (off by default) lets a request
  without a code create a team, for a hub on a private network where knowing the address is enough.
  Both signatures stay required. It is bounded by one hub-wide bucket (a burst of 5, then one team
  per ten minutes) and by `--max-teams` (50): past it the hub answers 503 and only a create code
  adds a team. A code that is sent is still checked. What it opens to anyone who reaches the hub:
  holding a team name, storing a Linear key and a webhook, and making the hub call Linear.
- **The hub says whether it is open.** `GET /v1/hub` is unsigned and answers one boolean
  (`open_teams`: open, and below the team cap). The app asks it after the person enters the hub's
  address, to decide whether to ask for a create code. It is a hint: `POST /v1/orgs` is the gate.
- **Org-admin requests carry two signatures.** The device's (an enrolled, unrevoked device of the
  org; freshness and replay protection) and the admin key's over
  `"lola-hub-admin-v1" \n org \n device \n <the request's canonical string>`. The domain prefix keeps
  a policy signature and an admin signature from standing in for each other; the org and the device
  keep it from being lifted onto another request. The org acted on is the device's, never a field.
- **A closed, non-executing set.** Linear key, Linear team, digest webhook, enroll tokens, device
  revoke. No request registers or removes an admin key, publishes a policy or returns a secret, so
  the hub still cannot be made to hand a daemon a key or a policy it did not get from an admin.
- **Invites never carry a hub-chosen key.** The hub returns only an enroll token. The admin's app
  builds the invite with the URL it already uses and the admin key it holds.
- **Secrets are write-only and sealed.** A Linear key is checked against Linear before it is stored
  (the answer lists the workspace's teams, each field validated, so the admin picks one). Views
  carry status only: set or not, and the class of the last error.
- **The digest webhook is Slack only.** The hub posts to it unprompted, so any other URL would let
  an org admin reach addresses inside the hub's network. It is validated when set and before every
  post; redirects are not followed.
- **Accepted risk.** The hub must be able to open an org's Linear key to file an issue, so a
  compromised hub (store plus secret key) can read every org's key. Use Linear keys with the least
  access that can create issues. The one rule is untouched: nothing here reaches an agent.
- **Lost or leaked admin key.** The operator adds a new key and removes the old one on the host.
  There is no self-service re-key: a stolen device key alone must not be able to mint an admin.
- **One team per device** for now: a daemon has one hub identity.

A hub that still has the old hub-wide `LOLA_HUB_LINEAR_KEY` / `LOLA_HUB_DIGEST_WEBHOOK` adopts them
into its only org at the next start (sealed) and never reads them again; with several orgs it
adopts nothing.

## Where Linear fits

Linear stays the source of truth for PLANNED work. The chore lane is for mechanical work that
verifies itself (CI green, advisory no longer open). A chore becomes a Linear issue only when it
needs a person — it failed `escalate_after` times, or it is `critical`. The hub does not mirror chores
into Linear.

## Phases

1. **Phase 1 (this build):** identity and enrollment, signed requests, the chore lane
   (`dep_advisory`, `red_bump_pr`) with leases and dedup, Linear escalation, the signed policy bundle
   (contract + verification + distribution; the daemon VERIFIES and STORES it and shows it in
   `lola hub status` — applying it into config inheritance is Phase 2).
2. **Phase 2:** apply verified policy as a third inheritance layer (team → `[defaults]` →
   `[[project]]`) with `locked` / `suggested` keys, matched by repo slug; desktop UI.
3. **Phase 3:** push gateway (APNs + FCM, opaque handles only) and the blind relay for the phone,
   end-to-end inside the existing pairing transcript.
4. **Phase 4:** scheduled triage leases ("triage repo X today" run on a member's machine), local
   lockfile matching against OSV, Postgres store.
5. **Phase 5:** lola as a Linear agent app — delegating an issue to "lola" routes it to a member's
   daemon through the same claim path.

## Non-goals

- Running a model in the hub. LLM spend and code stay on the members' machines.
- Hub-initiated connections to daemons.
- Auto-merge from the hub.
