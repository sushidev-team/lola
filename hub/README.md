# lola-hub

`lola-hub` is the team coordinator for [lola](https://github.com/sushidev-team/lola) daemons. It
deduplicates chores that every teammate's lola would otherwise do at once (a security advisory in a
shared repo, a red Dependabot/Renovate PR), leases each one to a single member's daemon, records the
outcome and escalates to Linear when a person is needed. It also stores and routes the team's signed
policy bundle.

It never executes anything, never holds code and never contacts a daemon. Daemons call it, outbound,
with signed requests. See [DESIGN.md](DESIGN.md) for the contract and the security rules; the wire
format lives in the sibling `hubproto` module (`../hubproto`).

## Build

```sh
make build    # ./lola-hub
make check    # build + vet + test + gofmt
```

The hub is its own Go module (`hub/go.mod`) inside the lola repository. It uses Go 1.25+ and
the standard library only, plus `hubproto`, the wire contract in `../hubproto`. A `replace` in
`go.mod` points at that directory, so the hub always builds against the checkout's contract:
there is no version to pin, and one pull request changes contract, hub and daemon together.
Nothing is ever downloaded (`GOPROXY=off` in the Makefile).

## Trying it locally

```sh
make build
./lola-hub dev --seed-demo --demo-repo <owner>/<repo>
```

`dev` creates `./data-dev/hub.json`, ensures the org (`--org dev`), mints a fresh enroll token, serves
the API on `127.0.0.1:8099` and the admin console on `127.0.0.1:8098`, and prints:

```
  Hub URL        http://127.0.0.1:8099
  Org            dev
  Enroll token   <32 chars>   (single use, expires ...)
  Admin console  http://127.0.0.1:8098
  Admin token    <64 hex>
```

Then in Lola: Settings → Team hub → paste the hub URL and the enroll token (or
`echo <token> | lola hub join http://127.0.0.1:8099`). lola accepts plain http only for a loopback hub.
`--seed-demo` adds three valid example chores in `--demo-repo` (default `acme/demo`); a daemon is
offered them only for a repo one of its opted-in projects names. Each start mints a new enroll token
(tokens are stored hashed, so an old one cannot be printed again) and reuses the admin token.

## Setting up a team

Four steps, two machines. The hub host never holds the policy signing key.

```sh
# 0. Once per hub: the key that seals every org's Linear key and digest
#    webhook in the store. Keep it in your secret manager.
lola-hub secret-key            # -> LOLA_HUB_SECRET_KEY

# 1. On the hub host: the org and the console's admin token, in one step.
#    With LOLA_HUB_LINEAR_KEY in the environment, a team KEY (ENG) is resolved
#    to its id and the key becomes this org's own (stored sealed). The org's
#    admin can also set both later, from Lola.
LOLA_HUB_SECRET_KEY=... LOLA_HUB_LINEAR_KEY=lin_api_... \
  lola-hub init --org acme --linear-team ENG --data /var/lib/lola-hub/hub.json
#   created org acme (escalations go to Linear team <uuid>)
#   admin token: <64 hex>          <- printed once; also in <data dir>/admin.token (0600)

# 2. On YOUR machine: a policy key, a starter policy and that policy signed.
lola-hub policy init --org acme --out ~/lola-hub-acme
#   trusted:    k0123456789abcdef:<base64url>
#   (policy.key 0600, policy.json to edit, policy.signed.json = v1)

# 3. On the hub host: register the signer and publish, in one call. The hub
#    still verifies the signature; it never signs.
lola-hub admin policy publish --register-key k0123456789abcdef:<base64url> policy.signed.json

# 4. On the hub host: one invite per person (or per device with --count).
lola-hub invite --url https://hub.example.com --org acme --label "Ana Lopez" \
    --trusted k0123456789abcdef:<base64url>
#   lola-hub:https://hub.example.com#<token>#k0123456789abcdef:<base64url>
```

An invite is `lola-hub:<url>#<token>[#<key_id>:<pub>]`: the hub URL, a single-use enroll token
(default lifetime 7 days, `--ttl`) and, when `--trusted` is given, the policy key the new member's
daemon pins. The key is taken ONLY from the admin's `--trusted` flag, never looked up from the store,
so a compromised hub cannot choose the key members trust. Each invite is printed once; tokens are
stored as hashes. The `--label` is the member name: the enrolled device carries it as `member`, and
the board and the org view show it. Device names are made unique per org (`-2`, `-3`).

## Running it

```sh
LOLA_HUB_SECRET_KEY=... \
  lola-hub serve --addr :8080 --data /var/lib/lola-hub/hub.json
```

`serve` flags:

| Flag | Default | Meaning |
|---|---|---|
| `--addr` | `:8080` | listen address |
| `--admin-addr` | — | admin web console address; LOOPBACK ONLY (`127.0.0.1:8098`); off when empty |
| `--data` | `$LOLA_HUB_DATA` or `data/hub.json` | store snapshot (JSON, 0600, directory 0700) |
| `--tls-cert`, `--tls-key` | — | serve HTTPS directly; omit when a proxy terminates TLS |
| `--linear-team` | `$LOLA_HUB_LINEAR_TEAM` | Linear team id for a single-org hub whose org set none |
| `--lease-ttl` | `30m` | lease length, renewed by each heartbeat |
| `--escalate-after` | `2` | failed attempts before an item escalates (an org policy's `escalate_after` wins) |
| `--sweep` | `1m` | how often expired leases, grace windows, idle reviews and stale items are swept |
| `--max-lease-age` | `24h` | how long renewals may keep ONE claim alive; after it renew fails and a `timeout` attempt counts |
| `--stale-after` | `48h` | an open item not re-reported this long, while an active device still reports its repo, closes as failed (`closed_stale`) |

`LOLA_HUB_SECRET_KEY` (32 random bytes, base64; `lola-hub secret-key` prints one) seals each org's
Linear key and digest webhook before they reach the store (AES-256-GCM, bound to the org and the
field). It is read once at startup and removed from the environment. It never appears in argv, the
store or a log line. Without it the hub stores no secret and escalated items stay pending; nothing
else is affected. Lose it and the stored keys and webhooks cannot be opened: enter them again.

Each org escalates with its OWN Linear key into its own team. Both are set by the org's admin from
Lola (see "Self-service teams"), or by the operator: `admin org linear-key <org>` (the key on stdin,
never a flag), `admin org linear <org> --team ID`, or the console. One org's key never files another
org's issue, and no endpoint, page or log ever shows a key again once it is stored. The
`--linear-team` fallback applies only while the hub holds a single org; an org without a key or a
team keeps its escalations pending. An org files at most 20 issues per 24h;
the rest wait. Every attempt is recorded before Linear is called and the issue is created under an id
derived from the hub item, so a crash or a lost answer never files a second issue.

Escalation details:

- **One issue per finding, not per item.** The issue id is derived from the org and the finding's
  dedup key (`hubproto.EscalationIssueID`), and every attempt looks it up before creating one. A
  finding that is purged and reported again, or escalates a second time, finds its old issue.
- **Fail groups.** Items a daemon failed with the same `group` share ONE issue that lists them.
- **Order.** Pending escalations go out most severe first, then oldest first, so the daily cap holds
  back the least urgent.
- **Done items.** A pending escalation of an item that is already done is dropped. When every item of
  a sent issue is done (by a member's PR or `admin items resolve`), the hub comments with the PR and
  closes the issue. The issue URL is stored and shown on the board and in the console.
- The issue body names the last attempt's PR when there is one; every field is re-validated.

### Daily digest

An org with a digest webhook gets a daily digest in its own channel: new critical chores, chores in
review for more than 3 days, claimed for more than 12 hours, failed in the last day, and escalations
waiting. It is built only from validated item fields and counts, as `{"text": ...}`. The webhook is
set like the Linear key (from Lola, `admin org digest <org>` with the URL on stdin, or the console),
stored sealed and never shown or logged. It must be a Slack incoming webhook
(`https://hooks.slack.com/services/...`): the hub posts to this URL by itself, so an org admin must
not be able to aim it at an address inside the hub's network. The URL is re-checked on every post
and redirects are not followed. One org's chores never reach another org's channel. It fails open.

**Upgrading a hub that used `LOLA_HUB_LINEAR_KEY` / `LOLA_HUB_DIGEST_WEBHOOK`.** They are no longer
read at runtime. Start the new version ONCE with `LOLA_HUB_SECRET_KEY` and the old variables still
set: a hub with exactly one org adopts them (and `--linear-team`) into that org, sealed, and logs
it. Then remove the old variables. A hub with several orgs adopts nothing: set each org's own.

`GET /healthz` answers `ok` without authentication.

## Admin console

`serve --admin-addr 127.0.0.1:8098` starts a web console for everything the admin commands do:
orgs with item counts and recent events; per org, enroll tokens with a member name (shown once, with
a copy button), the token list (id, member, created, expires, unused / used by / revoked / expired,
revoke), devices with member, last seen and the policy version each applies (revoke, with a confirm),
chores filtered by state (purge one or every item of a state; resolve or reopen an escalated one),
an item page with its attempt history, the 90-day stats table, the per-org event log, "Add chore
manually" (validated by the same rules as a daemon's report, reporter `admin`), the Linear key and
team and the digest webhook (write-only: a stored secret is never shown), admin keys (add, remove),
create codes (shown once), and publishing a pre-signed policy by pasting it (optionally registering
its signer in the same step). Every page says plainly when the hub has no secret key, or the org has
no Linear key or no team, and how many escalations wait.

Its rails:

- **Loopback only.** Any other address (`:8098`, `0.0.0.0:8098`, a LAN IP) is refused at startup, the
  bound address is checked again, and a request whose Host header is not a loopback name is refused
  (DNS rebinding). For a remote hub, tunnel: `ssh -L 8098:127.0.0.1:8098 hub-host`.
- **Token.** `<data dir>/admin.token` (0600) is created on first start; `serve` prints only its path
  (stderr is the server log), `dev` prints the token itself. Signing in
  exchanges it for a random session (HttpOnly, SameSite=Strict, Path=/, 12h, server-side). Failed
  sign-ins are throttled. Rotate by deleting the file and restarting.
- **CSRF.** Every change is a POST that must carry a same-origin Origin (or Referer) and the session's
  CSRF token; anything else is 403.
- **Escaping.** Pages are `html/template` with a strict CSP and embedded assets only; item fields and
  device names are always rendered as text.

## Admin commands

Admin commands run ON THE HOST against the store file. They share the file safely with a running
server (each write takes a lock and the server reloads changes). Every one takes `--data PATH`.

```sh
lola-hub admin org create <name>                     # name: ^[a-z0-9][a-z0-9_-]{0,63}$
lola-hub admin org list
lola-hub admin org linear <org> --team ID | --clear   # the org's Linear team for escalations
lola-hub admin org linear-key <org> [--clear]        # the org's Linear key, read from STDIN, stored sealed
lola-hub admin org digest <org> [--clear]            # the org's Slack digest webhook, read from STDIN, stored sealed
lola-hub admin create-code [--url HUBURL] [--ttl 168h]  # lets a teammate create ONE team from Lola; printed ONCE
lola-hub admin policy keys remove --org acme --key-id ID  # the key can no longer publish or change org settings
lola-hub admin enroll create --org acme [--label "Ana Lopez"] [--ttl 24h]  # a single-use token, printed ONCE; stored hashed
lola-hub admin enroll list [--org acme]              # id, member, created, expires, state; never the token
lola-hub admin enroll revoke <token-id>              # an unused token only
lola-hub admin devices list [--org acme]
lola-hub admin devices revoke <device-id>            # also reopens the device's leased items
lola-hub admin items list [--org acme] [--state open|claimed|in_review|done|failed|escalated]
lola-hub admin items resolve <item-id>               # escalated -> done: a person fixed it
lola-hub admin items reopen <item-id>                # escalated -> open, attempts reset (keeps its one issue)
lola-hub admin items purge <item-id> | --org acme --state done
lola-hub admin items add --org acme --kind dep_advisory --repo acme/web --ecosystem npm \
    --package lodash --vulnerable "< 4.17.21" --fixed 4.17.21 --advisory GHSA-35jh-r3h4-6jhm --severity high
lola-hub admin items add --org acme --kind red_bump_pr --repo acme/web --pr 42 \
    --branch dependabot/npm_and_yarn/axios-1.7.4 --package axios [--author renovate[bot]]
lola-hub admin events list [--org acme] [--limit 50]
lola-hub admin policy keys add --org acme --key-id <id> --public-key <base64url>
lola-hub admin policy publish [--register-key <key_id>:<pub>] <signed-policy.json>
```

`lola-hub init` and `lola-hub invite` (see "Setting up a team") also run on the host.

A teammate joins with the token: `lola hub join https://hub.example.com` (the token on stdin, or `--token-file`).

### Team policy

The hub cannot sign a policy; an admin signs it offline and the hub only checks and routes it.

```sh
# On the admin's machine (the private key never goes to the hub):
lola-hub policy init --org acme --out ~/lola-hub-acme   # key + starter policy + v1 signed, in one step
# or by hand:
lola-hub policy keygen --out ~/.lola-hub-admin.key
#   key_id:     k0123456789abcdef
#   public_key: <base64url>
#   trusted:    k0123456789abcdef:<base64url>     <- goes into each daemon's [hub].trusted_policy_keys

cat > policy.json <<'JSON'
{"org": "acme", "version": 1, "issued_at": "0001-01-01T00:00:00Z", "key_id": "",
 "settings": {"review_providers": ["claude-session"], "escalate_after": 3}}
JSON
lola-hub policy sign --key ~/.lola-hub-admin.key --out signed.json policy.json

# On the hub host:
lola-hub admin policy keys add --org acme --key-id k0123456789abcdef --public-key <base64url>
lola-hub admin policy publish signed.json
```

`policy sign` decodes strictly: an unknown key, including any executing key such as `post_create`,
fails before anything is signed. `publish` verifies the signature against the org's registered keys
and refuses a version that is not newer than the published one. Daemons verify again against the keys
THEY pin, so a hub that registered a wrong key still cannot get a policy accepted.

## Self-service teams

A teammate can create a team, and a team's admins can run it, from the Lola app without the hub
operator.

- **Create a team.** The operator mints a one-time create code (`lola-hub admin create-code --url
  HUBURL`, or the console) and sends `lola-hub-create:<url>#<code>` privately. In Lola the creator
  enters a team name; the app generates an ed25519 **team admin key** on that machine and calls
  `POST /v1/orgs`. The creator's device is enrolled and the admin key registered. Without a code
  the endpoint answers 401, so a public hub is not open signup.
- **Create a team without a code.** `lola-hub serve --open-teams` (off by default) lets anyone who
  can reach the hub create a team: in Lola they enter the hub's address and are asked for a team
  name right away (a hub that is not open asks for the create code at that point).
  Use it only on a hub strangers cannot reach (VPN, internal network). It is limited to a burst of
  5 teams, then one per ten minutes hub-wide, and to `--max-teams` teams (default 50); past that
  the hub asks for a create code again.
- **Team admin.** Whoever holds the admin key on an enrolled device of the team can, for that team
  only: set the Linear key (checked against Linear first; the answer lists the workspace's teams to
  pick from), the Linear team and the Slack digest webhook; mint invites; revoke devices by name.
  These requests carry two signatures: the device's and the admin key's.
- **Invites.** The hub returns only a token. The admin's app builds
  `lola-hub:<url>#<token>#<key_id>:<pub>` itself, so the key a new member pins never comes from
  the hub.
- **What it cannot do.** No request registers an admin key, publishes a policy or returns a
  secret. A compromised hub can read the Linear keys it holds (it needs them to file issues), so
  give it Linear keys with the least access that can create issues; it still cannot run anything
  on a member's machine.
- **Lost admin key.** The operator registers a new one (`admin policy keys add`) and removes the
  old one (`admin policy keys remove`). Members pin the new key from a fresh invite.

## HTTP API

All `/v1` paths are defined in `hubproto` (`frames.go`, `teams.go`). Every request except enroll is
signed by the device (`Lola-Device`, `Lola-Timestamp`, `Lola-Nonce`, `Lola-Signature`). An ORG ADMIN
request also carries `Lola-Admin-Key` and `Lola-Admin-Signature`: a second ed25519 signature, by a
key registered in the device's org, over a domain-separated string naming the org, the device and
the request (20 per org in a burst, then one per 5 s). Bodies are capped at 64 KiB
and decoded strictly. Errors are `{"error": "<fixed text>", "code": "<code>"}` and never echo request
fields.

| Method + path | Body → answer | Notes |
|---|---|---|
| `POST /v1/enroll` | `EnrollRequest` → `EnrollResponse` | signed with the key being enrolled; token single use, unexpired; a key enrolls once (409 after) |
| `POST /v1/heartbeat` | `HeartbeatRequest` → `HeartbeatResponse` | stores free slots, agents, repos and `policy_version` (the policy the daemon applies, 0 = none); renews each `live_leases` id the device holds, lists the rest as `lost`; an `in_review` item the device owns is in neither list |
| `POST /v1/findings` | `FindingsRequest` → `FindingsResponse` | per-item rejections: `invalid_item`, `has_id`, `repo_not_reported`, `org_full` (10000 live items: open, claimed, escalated), `device_quota` (500 new items per device per hour) |
| `GET /v1/offers[?kind=K…]` | → `OffersResponse` | open items of the org in the device's reported repos, most severe then oldest first, ≤200 |
| `GET /v1/policy` | → `Policy` | 404 `no_policy` when none is published |
| `GET /v1/stats` | → `StatsView` | READ-ONLY: the org's daily counters (opened, done, failed, escalated, fix seconds) for the last 90 days |
| `GET /v1/board[?state=S&limit=N]` | → `BoardResponse` | READ-ONLY: the org's items, newest activity first, `limit` 1..500 (default 200); a lease shows the holder's device NAME, member and `mine` for the caller; `owner` only on `in_review`, `done`, `failed` and `escalated` items, `claimed_at` only on `claimed` and `in_review`; plus `history`, `last_fail`, `reopened` and the escalation's identifier and URL; no device ids, keys or repo lists |
| `GET /v1/org` | → `OrgView` | READ-ONLY: org name, the org's non-revoked device names with `me`, last seen, free slots, agents, `active` (heartbeat within 15 min), member and the policy version each device applies, and the published policy version (0 = none) |
| `GET /v1/hub` | → `HubInfo` | unsigned; says only whether a team can be created without a create code right now |
| `POST /v1/orgs` | `CreateOrgRequest` → `CreateOrgResponse` | create a team with a one-time create code (or without one on a hub started with `--open-teams`); signed by the device key being enrolled AND the admin key being registered; a taken name answers 409 and leaves the code unused |
| `GET /v1/org/settings` | → `OrgSettingsView` | ORG ADMIN: status only (key set, team, webhook set, last error class), never a secret; `?teams=1` also lists the Linear workspace's teams with the stored key |
| `POST /v1/org/settings` | `OrgSettingsRequest` → `OrgSettingsView` | ORG ADMIN: Linear key (checked against Linear before it is stored: 422 `invalid_secret` if refused, 503 `unavailable` if Linear or the secret key is missing), Linear team, Slack digest webhook; a pointer to `""` clears |
| `POST /v1/org/invites` | `InviteRequest` → `InviteResponse` | ORG ADMIN: one labelled single-use enroll token; the token only, never a key |
| `POST /v1/org/devices/revoke` | `DeviceRevokeRequest` → `{}` | ORG ADMIN: revoke a device of the org by its display name |
| `POST /v1/items/{id}/claim` | empty → `Lease` | 409 `conflict` if not open; 403 if the repo is not one the device reported |
| `POST /v1/items/{id}/progress` | `ProgressRequest` → `{}` | a PR is open: the holder moves `claimed` → `in_review` (lease dropped, device recorded as owner); the owner updates the delivery |
| `POST /v1/items/{id}/complete` | `CompleteRequest` → `{}` | PR must be in the item's repo (and be the item's PR for `red_bump_pr`); 409 `lease_lost` without a live lease |
| `POST /v1/items/{id}/fail` | `FailRequest` → `{}` | `reason` is one of `spawn_failed`, `agent_gave_up`, `ci_failed`, `pr_closed`, `timeout`, `refused`, `sandbox_required`, `not_reproducible`; optional `pr_url` (the PR the attempt left) and `group` (`^[a-z0-9]{8,32}$`: items failed under one group escalate as one Linear issue); see the lease rules below |
| `POST /v1/items/{id}/release` | empty → `{}` | back to open, no attempt counted |

Status codes: 400 `bad_request`, 401 `unauthorized` / `stale_timestamp` / `replayed_nonce`, 403
`forbidden`, 404 `not_found` / `no_policy`, 409 `conflict` / `lease_lost`, 413 `too_large`, 429
`rate_limited`, 500 `internal`. Unknown and revoked devices both get 401 `unauthorized`.

Bodies are decoded with unknown fields refused, so a hub that predates a field answers 400 when a
daemon sends it (heartbeat `policy_version`, fail `pr_url` / `group`) and 404 for `progress`. A
daemon talking to an older hub leaves those fields out, and falls back to complete-on-PR when
`progress` is 404.

### Lease rules

- A claim leases an OPEN item to one device for the lease TTL; each heartbeat that lists it renews it.
  An expired lease holds the item for its previous holder for a grace window (2× the TTL): it is
  offered to nobody else, and the holder's renew or claim takes it back without a new attempt. After
  the grace the item reopens. Renewals stop at `--max-lease-age`: then the hub counts a `timeout`.
- `progress` with a PR moves the item to `in_review`: no longer offered, not reopened by re-reports,
  owned by that device. `complete` and `fail` come from the owner. An `in_review` item with no
  progress call for 14 days reopens.
- A device that fails, releases or refuses an item, or lets it lapse past grace, is not offered it
  again for 6 hours.
- An open item nobody re-reports for `--stale-after` while some active device still reports its repo
  closes as `failed` (history `closed_stale`).
- Every item keeps its last 20 attempts (claimed, in review, completed, failed, released, expired,
  reopened, closed stale) with device name, member, reason and PR.
- `complete` → `done` with the PR URL and delivery state.
- `fail` counts an attempt and reopens the item, until `escalate_after` attempts: then the item is
  `escalated` (no longer offered) and gets ONE Linear issue. Exceptions: `refused` and
  `sandbox_required` are local policy, not a failed fix, so they reopen without counting;
  `not_reproducible` closes the item as `failed` without escalating.
- A `critical` advisory is escalated as soon as it is first seen AND stays on offer.
- Findings dedup per org by `hubproto.DedupKey`. A re-report bumps `last_seen` and the reporter count;
  while the item is open the newest report replaces its fields. A `done` or `failed` item that is
  reported again at least 24 hours after it closed reopens with its attempts reset: the fix did not
  land. Sooner than that a re-report is expected (merge and the next scan take time) and changes
  nothing. An escalated item belongs to a person and never reopens on its own.
- A revoked device can do nothing; revoking reopens its leased items.
- `admin items resolve` closes an escalated item as done; `admin items reopen` puts it back on offer
  with its attempts reset.
- `done` and `failed` items are deleted 7 days after they closed (a re-report after 24h would reopen
  them anyway), unless an escalation is still pending. `admin items purge` deletes by hand.

## Deploying

- **TLS is mandatory in production.** Either terminate it at a reverse proxy (Caddy, nginx, a cloud
  load balancer) and run `serve` on a private address, or pass `--tls-cert` / `--tls-key`. Signatures
  stop replay and tampering but not observation.
- **Single process.** The file store is one snapshot with a lock; run one `serve` per data file. Back
  up the data file (it holds device public keys, hashed tokens, items, policies and each org's
  Linear key and webhook SEALED under `LOLA_HUB_SECRET_KEY`; without that key the file gives away
  no secret). Back up the secret key separately.
- **Docker:** from the repository root, `docker build -f hub/Dockerfile -t lola-hub .` then
  `docker run -v lola-hub-data:/data -p 8080:8080 -e LOLA_HUB_SECRET_KEY lola-hub`.
  The context is the repository root because the image builds against `../hubproto`;
  `hub/Dockerfile.dockerignore` keeps it to `hub/` and `hubproto/`.
  It is distroless, static and runs as non-root. Setup and admin commands run in the same container:
  `docker exec <c> /lola-hub init --org acme`, `docker exec <c> /lola-hub invite --url ... --org acme --label "Ana"`,
  `docker exec <c> /lola-hub admin enroll list`.
- **Console in Docker:** loopback inside a container is not reachable through `-p`, and the console
  refuses any other bind, so the loopback rule holds by running the console where loopback is real:
  - run a second process ON THE HOST against the same data file (a bind mount instead of a named
    volume), API off: `lola-hub serve --addr 127.0.0.1:0 --admin-addr 127.0.0.1:8098 --data /srv/lola-hub/hub.json`.
    Start it WITHOUT `LOLA_HUB_SECRET_KEY` in its environment, so only the container escalates and
    posts digests (that console then cannot store a secret; use `docker exec ... admin org
    linear-key` or Lola for those). The file store is shared safely (each write takes a
    lock and reloads; sweeps are idempotent); keep ONE process serving the public API. Then open `http://127.0.0.1:8098`, or from your laptop
    `ssh -L 8098:127.0.0.1:8098 hub-host`.
  - or skip the console and use `docker exec` for every admin command.
  Never publish the console port (`-p 8098:8098` with a non-loopback bind is refused anyway).
- Rate limits: 2 requests/s per device with a burst of 60; enrollment shares one global bucket.

### Kubernetes (https://lola.sushidev.kitchen)

The hub runs on the DigitalOcean cluster `sushidev-cluster-02`, namespace `lola-hub`. Manifests
are Kustomize under `hub/deploy/k8s` (`base/` is the workload, `overlays/production/` adds the
namespace, ingress and image tag).

| | |
|---|---|
| API | https://lola.sushidev.kitchen (ingress-nginx, cert-manager `image-proxy-letsencrypt-production`) |
| Image | `registry.digitalocean.com/sushidev-registry-01/lola-hub:<sha>` |
| Data | PVC `lola-hub-data` (`do-block-storage-retain`), store at `/data/hub/hub.json` |
| Console | `127.0.0.1:8098` inside the pod, reached only with `kubectl port-forward` |

**Deploy flow.** `.github/workflows/hub.yml` (in the repository root) runs for changes under
`hub/` and `hubproto/` only. It runs `make check` on every push and PR. On `main`, it builds and
pushes `lola-hub:<sha>`, then the `deploy` job (GitHub environment `hub-production`) fetches a
short-lived kubeconfig with `doctl` and runs `kubectl apply -k
hub/deploy/k8s/overlays/production` with the SHA as the image tag. The Deployment has one
replica and uses `Recreate`, so two pods never share the store. A lola change that touches
neither directory deploys nothing, and lola's own CI and releases skip `hub/`.

**GitHub settings** (on the lola repository). Secrets `DO_ACCESS_TOKEN`, `DO_REGISTRY` and
`LOLA_HUB_SECRET_KEY` (`lola-hub secret-key`). The workflow writes the secret key into the
Secret `lola-hub-env` through a 0600 temp file. It never puts it in argv or the manifests. When
the Secret changes, the workflow restarts the pod, because the hub reads its environment once
at startup.

**DNS.** `lola.sushidev.kitchen` needs an A record to the cluster's ingress load balancer
(`157.245.24.200`). The certificate is issued after the record resolves.

**First setup** (once, after the first deploy):

```sh
ctx="--context do-fra1-sushidev-cluster-02 -n lola-hub"
# Org + admin token. The token is printed ONCE: store it in 1Password.
kubectl $ctx exec deploy/lola-hub -- /lola-hub init --org sushi --linear-team ENG
```

**Admin console.** It binds pod loopback only, and no Service or Ingress exposes it. Access to
it is gated by Kubernetes RBAC on `pods/portforward`, the cluster equivalent of the SSH
tunnel above:

```sh
kubectl --context do-fra1-sushidev-cluster-02 -n lola-hub port-forward deploy/lola-hub 8098:8098
# then open http://localhost:8098 and sign in with the admin token
```

Admin CLI commands run in the same pod: `kubectl $ctx exec deploy/lola-hub -- /lola-hub admin
devices list`.

**Lost admin token.** The image is distroless, so it has no `cat`. Read the token from a
non-root debug container that shares the hub's process namespace:

```sh
pod=$(kubectl $ctx get pod -l app=lola-hub -o name)
kubectl $ctx debug -it "$pod" --target=lola-hub --profile=restricted \
  --image=gcr.io/distroless/static-debian12:debug-nonroot -- cat /proc/1/root/data/hub/admin.token
```

**Backups.** The PVC uses a Retain storage class, so deleting the namespace keeps the volume.
Snapshot it in the DigitalOcean console (Volumes) before risky changes.
