# Team session sharing (SUSHI-802)

Status: DESIGN. Nothing here is implemented yet; each phase lands behind its own tests.

A member can share a session with teammates. A teammate attaches to the
session's terminal from their own machine, through the team hub, and it feels
like a local attach. Everyone with access sees who is attached.

Motivating case: a Mac mini in the office runs offloaded work; teammates reach
its sessions from home.

## 1. What "attach" means, and why it is dangerous

Attaching gives keystrokes to the agent in that pane. The agent runs on the
owner's machine with the owner's credentials. So a team attach is **remote
control of the owner's agent**, the same reach the phone's `shell` tier has
(`docs/mobile/01-pairing-and-tiers.md`). This sets the rule everything else
follows:

> **The hub may route and display a share. It may never grant one.**

`hub/DESIGN.md`'s one rule (a compromised hub must not mean code execution on a
teammate's machine) applies unchanged. A hub that could say "member X may
attach" could inject its own key into the list and type into every shared
session. So authority lives only on the owner's machine.

## 2. Trust model

| Question | Answered by | Never by |
|---|---|---|
| Who is this peer? | Inner mutual TLS 1.3 key, pinned by the owner's daemon | the hub, a name, an IP |
| May this peer attach to THIS session? | The owner's local grant (`devices.json` member entry + per-session share) | hub ACLs, hub policy |
| Which sessions exist for me? | The hub's share listing (a hint) | the hub as authority: a listed share is re-checked by the owner's daemon at attach |
| Who is attached now? | The owner's daemon, pushed inside the inner session | the hub |

- **Member keys are pinned by a human, once.** The hub knows each member
  device's ed25519 key (it enrolled). The owner's app shows the member label,
  device name and key fingerprint; the owner confirms (like `--trust-key` for a
  policy key). Only then does the daemon add a `kind: "member"` entry to
  `devices.json` holding that device's TLS identity hash. The hub supplies the
  candidate, never the decision, and never a key after the fact: a changed key
  is a NEW entry that needs a new confirmation.
- **The inner session is end to end.** Member daemon ⇄ owner daemon, mutual TLS
  1.3, each pinned to the other. The hub splices bytes it cannot read, exactly
  as for a phone (`docs/mobile/03-hub-relay.md`).
- **Share is opt-in per session**, default off. A `[defaults].share_sessions`
  setting (`off` | `new`) makes NEW sessions start shared with the configured
  member set. Never inherited into a stack/KindLocal session, never retroactive.
- **Scope is per member AND per session.** A member entry carries
  `sessions: shared-only`. A connection from a member device can name only
  sessions with `Session.Shared` set whose `ShareWith` contains that member (or
  is empty = all granted members). Everything else answers as if the session
  does not exist.
- **Member tiers are a new, smaller set.** A member peer gets exactly:
  `sessions` (shared only, projection with no paths), `pane`/subscribe,
  `pty` input only when the grant is `control` (default `view`), `paneResize`
  never. Nothing else: no `open`, no `shellCreate`, no config, no hub, no
  `kill`. `internal/remote/tiers.go` stays a closed allowlist: a member peer is
  checked against a separate `memberCommands` allowlist; unknown = never.
- **View vs type.** Grant levels: `view` (read-only stream) and `type`
  (keystrokes through `typeToAgent`'s gates? No: raw pty write is how a local
  attach works). `type` therefore equals the `shell` tier on that one pane and
  is labelled that way in the UI. Default is `view`. The owner can drop a
  member to `view` or revoke live; both take effect on the next frame and close
  the member's connections.

### What the hub sees (additions to `hub/DESIGN.md`'s table)

| Sees | Never sees |
|---|---|
| Share id (random, per share), owner device, a display title the owner chose, project label if the owner opts in, agent kind, member list (device names) | session ids, paths, pane text, keystrokes, who typed what |
| Relay metadata: who dialed whom, when, byte counts | the inner stream |
| Presence IF the daemon reports it (see §5) | |

The share id is random and unrelated to the lola session id (which embeds
Linear identifiers).

## 3. Wire (hubproto, backward compatible)

All additions are optional; an old hub answers 404 → feature off, LAN/phone
paths unchanged. A new hub talking to an old daemon sees no shares.

- `POST /v1/shares` (device-signed, replace-all like rendezvous): the owner's
  device publishes its current shares: `[{id, title, agent, members:[device_id…]
  | null, level}]`. Validated whole (`ShareSet.Validate`), caps: 64 shares, 32
  members each, title ≤ 80 printable.
- `GET /v1/shares` (device-signed): shares visible to the asking device
  (same org, and the asking device is in `members` or `members` is null).
  Returned: `{id, title, agent, owner_device, owner_name, owner_member,
  online}`. `online` = owner's relay control channel is up.
- `GET /v1/relay/member?share=<id>` (WS upgrade, device-signed by the MEMBER):
  the hub checks the dialer is in the share's audience and the owner is online,
  then sends the owner `{t:"incoming", conn, rv}` with a hub-minted **member
  rendezvous** and splices the legs, as for a phone. The dialer's identity is
  not trusted by the owner (§2): it is only an admission hint. The owner's
  daemon requires the inner mTLS client key to match a member entry.
- Limits reuse `hubproto.Relay*` with the member bucket counted per dialer
  device and per owner device (4 concurrent members per owner by default).

Control `incoming` gains an optional `kind:"member"` (unknown fields are
already ignored, so an old daemon ignores the message and the dial times out
cleanly).

## 4. Daemon

- `config`: `[defaults].share_sessions`, `[[share_member]]`? No: members are in
  `devices.json`, not config.toml, because they are pinned keys (same reason
  phones are). The setting is only the default for new sessions.
- `session.Session`: `Shared bool`, `ShareID string` (random), `ShareWith
  []string`, `ShareLevel`. Written only via a `setShare` method; not an
  inheritable key.
- Commands (all human-only: tier `never` for phones, absent from the sandbox
  hook bridge, in `protocol.HubHumanCommands`' neighbour list
  `protocol.ShareCommands`): `shareSession`, `shareMembers` (candidates from the
  hub board + fingerprints), `shareTrust` (pin), `shareRevoke`,
  `shareAttached` (roster).
- Publish loop: rides the hub heartbeat (`hubwire.go`). A change to share state
  calls `hubShareDirty`; the loop POSTs the replace-all set. Failure keeps the
  last good set and retries with the relay's backoff; teardown of a session
  removes its share synchronously from the local set before any network call
  (so a lost hub never leaves a shared dead pane attachable: the local check
  is the gate).
- Accept side: `remote.Server` gets a member admission path. The peer's TLS key
  hash is looked up; a `kind:"member"` entry yields a `Peer` with
  `Scope: member`. Every frame is authorized against the live registry (same as
  phones) AND the session's current share state. Un-sharing a session ends its
  member subscriptions at once.
- Attach feel: reuse `panebus` (one tmux attach multiplexed across
  subscribers) — a member is one more subscriber, so cost on the owner is flat.
  The member side exposes the remote pane through the desktop's existing
  terminal component: a "remote" session row whose terminal source is a
  `pane` stream over the relay instead of `tmux attach`.
- Presence: the owner's daemon keeps `attached[session] = [{member, device,
  since, level}]` from live member subscriptions and pushes it as a `presence`
  event on the `sessions`/`talk` topic to everyone attached AND to the owner's
  UI. The hub does not carry it (blind). A teammate who only has the share
  listing but is not attached sees presence on connect.

## 5. Desktop

Compact icons, text only where needed (per the issue):

- Session row/header: a share icon button (toggle), a people icon with a count
  when members are attached (tooltip lists names; own-attach highlighted).
- Share popover: member checkboxes (labels), a view/type segmented icon pair,
  "Share new sessions" switch bound to the setting.
- "Shared with me" group in the sidebar: owner device (office-mac) → sessions,
  offline ones dimmed; click attaches; the terminal looks local.
- Trust prompt: member label, device name, fingerprint, one confirm.
- Keyboard equivalents for new bare keys; `HelpOverlay.svelte`.

## 6. Phases (each its own commit, green tests)

1. **Wire + hub**: `hubproto` shares/member-dial types and validation; hub
   store, API, relay admission, limits; hub tests.
2. **Daemon identity + gate**: member entries in `devices`, `Session.Share*`,
   member `Peer` scope with its own allowlist, un-share ends subscriptions;
   tier/field/golden tests.
3. **Daemon attach path**: publish loop, member dial client, relayed stream
   into `remote.Server`, presence.
4. **Desktop**: services, icons, shared group, remote terminal, trust prompt.
5. **Docs/doctor**: `lola doctor` row, README/config.example, mobile parity
   note (phones do not get share commands).

## 7. Non-goals / explicit risks

- No share without a joined hub. No LAN-direct member attach in v1.
- A member with `type` can do anything the agent's terminal allows on the
  owner's machine; sandboxed sessions contain it, and the UI says so. Sharing a
  non-sandboxed session at `type` asks for an extra confirm.
- Traffic analysis of who attaches to whom is visible to the hub (accepted, as
  for the phone relay).
- Hub enforces only admission hints and quotas; a hub that lies can deny
  service, not gain access.
