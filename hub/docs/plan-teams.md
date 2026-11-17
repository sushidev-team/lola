# Plan: self-service teams and per-team Linear settings

Status, 2026-11-14: all four steps are implemented and uncommitted: `hubproto` and the daemon and
app in lola (`feature/hub-client`), the hub here. Checked end to end with lola's client against a
local `lola-hub dev`: create a team, set the webhook and team, a refused Linear key, invite, join,
revoke.

## Goal

From the lola app's team icon, a user joins a team with one invite string or creates a new team.
Team admins set their team's Linear key, Linear team and digest webhook themselves, without the
hub operator.

## Before

- Teams are **orgs**; one hub holds several. Per org: Linear team id (`admin org linear`, console).
- Hub-wide only: `LOLA_HUB_LINEAR_KEY` (env), digest webhook (env, single-org hubs only).
- Admin = hub operator only (host CLI, loopback console).
- `lola-hub invite` prints `lola-hub:<url>#<token>[#<keyid>:<pubkey>]`. lola parses it
  (`internal/hub/invite.go`, `hubinvite.ts`), but the app still shows separate join fields.
- `hubproto` has no frames for creating an org or changing its settings.

## Design

### App: team sheet (team icon)

**Not joined:**

1. **Join a team.** Paste one invite. The app parses it, enrolls with the token and pins the
   signing key from the invite. The admin's app put that key in the invite, never the hub.
2. **Create a team.** Paste one create code (`lola-hub-create:<url>#<code>`: it carries the hub
   URL, so there is no second field) and enter a name. The app:
   - generates an ed25519 team admin key, in `<LOLA_HOME>/state/` (0600) or the macOS Keychain.
     It must differ from the device key;
   - calls `POST /v1/orgs` with the name, the admin public key and this device's enrollment,
     signed by both keys;
   - pins the admin key as a trusted policy key.

   The creator is enrolled and becomes admin.

**Joined:** a team card (name, members, policy version, Leave). One team per device for now,
because the daemon has one hub identity (`state/hub.json`).

**Admin key on this device:** an Admin section in the Team view.

- **Invite teammate.** The hub mints the token. The app adds the URL and signing key itself
  (`hubproto.FormatInvite`).
- **Linear.**
  - Paste a key. The hub checks it against Linear and returns the workspace's teams, so the
    admin picks one from a list.
  - Write-only: the app sees only status (set, last error class).
- **Digest.** A Slack webhook, write-only.
- **Devices.** Revoke, by display name.
- Later: publish a policy from the app.

### Hub

- **Create codes.** The operator mints single-use codes (`lola-hub admin create-code`, console)
  that gate `POST /v1/orgs`, so the public hub is not open signup. Stored hashed, expiring.
- **Org-admin requests.** A device-signed request plus a second signature by a key in
  `Org.AdminKeys`, limited to the device's own org. The fields are a closed set: Linear key and
  team, digest webhook, invites, device revoke. None can execute.
- **Sealed secrets.**
  - Per-org Linear key and webhook are stored sealed with AES-256-GCM; the additional
    authenticated data is the org name and the field.
  - The master key is `LOLA_HUB_SECRET_KEY`, from env only. Never logged, never returned.
  - A copy of the store file alone leaks nothing.
- **Open creation, off by default.** `serve --open-teams` takes `POST /v1/orgs` without a code,
  for a hub on a private network: the app then needs only the hub's address. Both signatures
  stay; one hub-wide bucket and `--max-teams` bound it.
- **Create flow in the app.** The person enters the hub's address; the app asks the unsigned
  `GET /v1/hub` whether the hub is open. Open: team name and device name. Not open: the create
  code is asked too. A whole `lola-hub-create:` line entered as the address skips the question.
- **Per-org escalation and digest.** Each org uses its own key and webhook, and the single-org
  limit goes away.
- **Console and CLI.** The same operations (`internal/admin`), write-only for secrets.
- **k8s.** The `lola-hub-env` Secret holds `LOLA_HUB_SECRET_KEY`.

### What changed against the first draft, and why

| Draft | Now | Why |
|---|---|---|
| Admin request "signed by a key in `Org.AdminKeys`" | Device signature AND admin signature, with a domain prefix, naming org and device | The admin key also signs policy: without a domain prefix one signature could stand in for the other. Requiring a live device reuses the replay cache, rate limit and revocation, and a leaked admin key alone reaches nothing. |
| Digest: "a Slack webhook" | Only `https://hooks.slack.com/services/...`, re-checked before every post, redirects off | The hub posts to this URL by itself. Any URL an org admin may choose is a request forgery into the hub's own network (cluster services, cloud metadata). |
| AAD = org name | AAD = org + field | Otherwise a store edit can move the sealed webhook into the key slot and the hub sends it to Linear as a credential. |
| "Returns the workspace's teams" | Team id, key and name are validated by `hubproto`; a name that fails is sent empty | Linear team names are free text. Nothing free-form from the hub reaches a member unchecked. |
| Create: name + create code | One line `lola-hub-create:<url>#<code>` | The draft never said where the app gets the hub URL. |
| Create request | Also signed by the admin key it registers | Proves the creator holds the key; nobody can register someone else's public key. |
| Remove `LOLA_HUB_LINEAR_KEY` / `LOLA_HUB_DIGEST_WEBHOOK` | A single-org hub adopts them into its org on the first start, sealed, then they are ignored | Production runs on them today. Dropping them would silently stop escalation on the next deploy. |
| Devices: revoke | By display name | Device ids never leave the hub. |
| (missing) | `admin policy keys remove` (CLI and console) | An admin key now changes settings. A leaked one must be removable. |
| (missing) | Unused invites capped per org (200), create codes per hub (100), org-admin requests rate limited per org | Invites are now minted over the API. |
| (missing) | The Linear key is checked BEFORE it is stored; a refused key answers `invalid_secret`, an unreachable Linear `unavailable` | The admin learns at once, and a wrong key never replaces a working one. |
| Console "shows status" | Console and CLI also SET secrets, write-only (stdin, password field) | Rule: an admin operation exists on every surface. The operator needs a path that does not involve the app. |

### Contract changes (AGENTS.md / DESIGN.md), done

- "No admin endpoint on the API listener" became "no **hub-operator** endpoint". Create-with-code
  and org-admin requests are allowed, as a closed set.
- "`LOLA_HUB_LINEAR_KEY` env-only, never stored" became "per-org secrets stored sealed under an
  env-only master key, write-only".
- New: the hub's outbound calls are Linear and a Slack-only webhook.
- **Accepted risk:** a compromised hub can read every org's Linear key, because it needs the keys
  to escalate. Use Linear keys with minimal permissions. Code execution on members' machines
  stays impossible.

## Steps

1. **lola `hubproto`** — done (`hubproto/teams.go`, in the `hub-client` worktree):
   - invite and create-code parse and format;
   - admin-signed requests (`SignAdmin`, `VerifyAdmin`);
   - `CreateOrgRequest`, `OrgSettingsRequest`, `OrgSettingsView`, `InviteRequest`,
     `InviteResponse`, `DeviceRevokeRequest`.

   The hub now lives in the lola repository as `hub/` and builds against `../hubproto`
   directly, so there is no pin to move.
2. **lola-hub** — done: create codes, `/v1/orgs`, org-admin endpoints, `internal/secret`, per-org
   escalator and digest, legacy-env adoption, console, CLI, docs, k8s Secret and workflow.

   Rollout: add the GitHub secret `LOLA_HUB_SECRET_KEY`, deploy once with the old secrets still
   set (the hub adopts them), then delete the old secrets and their lines in the workflow.
3. **lola daemon** — done:
   - admin key storage: `state/hub-admin-key` (0600), a record naming its hub and team, written
     pending before the create code is sent, kept on leave (`internal/hub/admin.go`);
   - client calls signed with both keys (`Client.rawSigned` + `hubproto.SignAdmin`), every answer
     validated, a view of another team refused;
   - socket commands `hubCreateTeam`, `hubInvite`, `hubTeamSettings`, `hubRevoke`
     (`internal/daemon/hubadmin.go`), all in `HubHumanCommands`; `hubStatus.admin`;
   - `hubJoin` already took one invite. It keeps lola's own `ParseInvite`, which also accepts the
     bare token the CLI allows.
4. **lola app** — done:
   - Settings → Team hub, not joined: "Join a team" (one invite) and "Create a team" (one create
     code and a name; `HubCreateTeam.svelte`);
   - Settings → Team hub, admin: "Team admin" with invite, Linear key and team, digest webhook
     (`HubAdmin.svelte`). It sits with the other hub forms, not in the Team view;
   - Team view, admin: "Remove" beside every other Mac.

Not built: a CLI for creating or administering a team (`lola hub create-team` ...), the macOS
Keychain for the admin key, and publishing a policy from the app.

## Decided

- **Creating a team is gated by a create code.** Open signup on a public hub would let anyone
  store Linear keys and make the hub call Linear. The operator may lift the gate for a private
  hub with `--open-teams`; production does not set it.
- **A lost admin key is replaced by the operator** (`admin policy keys add` / `remove`). No
  self-service re-key: a stolen device must not be able to mint an admin. An org may hold several
  admin keys, so a co-admin is the operator registering a second key.
- **One team per device** for now.

## Open

- A co-admin invite that carries a second admin key without the operator.
- Rotating `LOLA_HUB_SECRET_KEY` without re-entering every org's secrets.
- Other digest targets than Slack (an operator-set host allowlist).
