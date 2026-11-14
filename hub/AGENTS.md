# AGENTS.md

Guidance for coding agents working on the team hub (this directory, `hub/`). Read [DESIGN.md](DESIGN.md) first: it is the
contract.

## The one rule

**A compromised hub must not mean code execution on every teammate's machine.** So:

- The hub never writes a prompt, a command or free text that reaches an agent. A work item is a closed
  `kind` plus fields that pass `hubproto.Validate`. Re-validate before anything leaves the hub (offers
  filter invalid items; escalation text is built from re-validated fields only).
- The hub holds no policy SIGNING key. It verifies a pre-signed bundle against the org's registered
  admin keys and routes it. Never add server-side signing.
- No executing key (`post_create`, `pre_remove`, `dev_commands`, `custom-cli`, `sandbox` images, any
  shell line) may ever be settable through policy. That set is defined in `hubproto.Settings`.
- No HUB-OPERATOR endpoint on the API listener. Operating the hub (orgs, admin keys, policy, items,
  create codes) runs on the host. The API serves two team requests and nothing more:
  - `POST /v1/orgs` creates an org with a one-time create code the operator minted, signed by the
    device key AND the admin key it registers. Without a code it answers 401. It is the ONLY request
    that registers an admin key, and only for the org it creates. The one exception is the
    operator's own switch: `serve --open-teams` (off by default) takes a request WITHOUT a code.
    It keeps both signatures, has its own hub-wide bucket (never the enroll bucket) and stops at
    `--max-teams` orgs. Never turn it on by default, never make it settable through the API, and
    never let it skip a code that IS sent.
  - `GET /v1/hub` is unsigned and answers `hubproto.HubInfo`: whether a team can be created
    without a code right now, and nothing else. It takes no input and says the same to every
    caller. Never add a name, a count, a version or anything about an org to it.
  - ORG-ADMIN requests (`/v1/org/settings`, `/v1/org/invites`, `/v1/org/devices/revoke`) go through
    `api.adminAuthed`: an enrolled, unrevoked device AND a second signature by a key in that org's
    `AdminKeys` (`hubproto.VerifyAdmin`, domain-separated, naming the org and the device). The org
    is ALWAYS the device's, never a request field. The settable set is closed and non-executing:
    Linear key, Linear team, digest webhook, enroll tokens, device revoke. Never add a request that
    registers or removes an admin key, publishes a policy, or returns a secret.
- Operating the hub runs on the host: the CLI against the store
  file, or the admin web console (`internal/console`), which is a deliberate, fenced exception. Its
  rails are not negotiable: it listens on a LOOPBACK address only (`console.CheckLoopback` before
  anything starts, `console.Listen` re-checks the bound address, and the Host header must be a
  loopback name so DNS rebinding is refused); it needs the token in `<data dir>/admin.token` (0600),
  exchanged for an HttpOnly, SameSite=Strict session cookie held in a server-side map with an
  expiry; every mutation is a POST with a same-origin Origin (or Referer) AND the session's CSRF
  token. Every page is `html/template`; never mark stored data safe, item fields and device names
  are display data. Assets are embedded, no third-party JS/CSS, and the CSP stays strict. Remote
  admin goes through an SSH tunnel, never a public bind. Why: setting up a team (enroll tokens,
  policy keys, manual chores) by CLI alone kept the hub from being usable by the people it serves,
  and a loopback console holds no more power than shell access to the host already does.
- Admin operations live ONCE in `internal/admin`; the CLI, the console and the org-admin API
  (`admin.Ops`, behind `api.OrgAdmin`) all call it. Do not add an operation to one surface only.
- The hub never opens a connection to a daemon, never merges, never pushes.

## Device-facing read views

`GET /v1/board` and `GET /v1/org` are signed and scoped to the caller's org. They carry device NAMES
of the org (never ids, public keys or repo lists), validated item fields, the lease holder's name and
`mine`, the outcome PR and the escalation identifier. Build every row with `api.ItemView` and drop a
row that fails `hubproto`'s `Validate`: the daemon drops the whole response if one row is bad.

## What the hub may see

Org, devices and their public keys, capacity (free slots, agents), opted-in repo slugs, findings'
validated fields, leases, outcomes (PR URL + delivery), and each org's Linear key and digest webhook
(sealed at rest). Accepted risk: a compromised hub with its secret key can read those; it still
cannot execute anything on a member's machine. It must never receive or store code, diffs,
paths, pane text, transcripts, prompts, review findings or lola session ids. Do not add a field that
carries any of them.

## Rules for changes

- **Stdlib only** (plus `hubproto`). No cobra, no router, no ORM. Use `flag`.
- **The wire contract lives in `../hubproto`**, not here: a separate module the daemon imports
  too. A new frame or field is changed there first, in the same pull request; the hub only imports
  it (`replace` in `go.mod`). Keep every change backward compatible: installed daemons and the
  deployed hub run different versions.
- **The hub is its own module** (`hub/go.mod`) and must stay one: it may import nothing from the
  rest of the lola repository except `hubproto`.
- Errors returned to clients carry a code and a FIXED message (`internal/api.messages`). Never echo a
  request field. Rejection reasons are hub constants.
- Secrets: each org's Linear key and digest webhook are stored ONLY sealed (`internal/secret`,
  AES-256-GCM, bound to org and field) under `LOLA_HUB_SECRET_KEY`, which is env-only: never logged,
  never in argv, never stored. A secret is WRITE-ONLY: read from stdin, a form field or a signed
  request body; never a flag, never echoed, never in an event, a flash, a log or any response. Open
  one only for the outbound call that needs it. Store error CLASSES from Linear and the webhook, not
  response bodies. Enroll tokens and create codes are stored as SHA-256 only and printed once.
- The hub calls out to exactly two places: Linear's fixed endpoint and an org's digest webhook. The
  webhook must pass `hubproto.ValidDigestWebhook` (Slack only) when set AND again before every post,
  with redirects off: an org admin must never be able to aim the hub at its own network.
- Anything from Linear that reaches a member (team id, key, name) is validated by `hubproto`
  first; a team name that fails is sent empty.
- `internal/lease` is pure (no I/O, no clock). Every state transition goes through it.
- `internal/store` holds no business rules. `Update` commits only when the callback returns nil, so
  return an error to abort.
- Tests never touch the network: Linear is behind `escalate.Creator`; the API is tested through
  `httptest` with a daemon signing via `hubproto`.

## Build

```sh
make check   # build + vet + test + gofmt, with a repo-local GOCACHE
lola-hub dev --seed-demo             # a local hub + console for testing
```
