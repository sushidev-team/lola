# Mobile rethink — plan

Status: built on branch `feature/mobile-rethink`, not yet merged. §1 records
the starting point, not the current code; §3.4 and §10 list what is not built
or not verified (also `mobile/HANDOFF.md`). The standing contracts are
`docs/mobile/01..04-*.md`. Supersedes the milestone order in
`mobile/PLAN.md`; that file's threat-model reasoning still applies where this
document does not say otherwise.

## 1. Where we start (verified in code, 2026-11-14)

- `mobile/` is **Capacitor 8 + Svelte 5 + Tailwind v4**, not React Native. It
  already reuses `desktop/frontend/src/lib` through Vite aliases; a native Swift
  plugin (`mobile/plugins/lola-transport`) owns the socket.
- The phone listener (`internal/remote`) is milestone 1: raw TLS 1.3, one
  shared bearer key, compiled only with `-tags lola_insecure`. There is no
  pairing, no device registry, no per-device revocation.
- Phones are denied every Conversation command (`agentSay`, `agentChoose`,
  `agentStop`, `talkState`, `agentFeed`, …), `sshTimeline`, and every
  `reviewPackage*` command (`internal/remote/policy.go`). So the phone has a
  terminal and a session list, and nothing else.
- Nothing is pushed except PTY bytes. The phone polls `sessions` every 2 s; the
  desktop polls `talkState` every 1 s.
- **The hub has no relay today.** It is signed request/response HTTPS for
  chores, policy and the board. `hub/DESIGN.md` names a "blind relay for the
  phone" as unbuilt Phase 3; `mobile/PLAN.md` rejects a relay outright. This
  plan builds the relay and resolves that conflict in favour of `hub/DESIGN.md`.
- The mobile shim has drifted: `npm run check` fails with 13 errors, the
  offline snapshot renders an empty list, no CI job covers `mobile/`.

## 2. Goals → decisions

| # | Goal | Decision |
|---|------|----------|
| 1 | Easy connect | One QR on the desktop (Settings → Phones, and `lola pair`). Scan once: the phone learns the daemon's key pin, LAN addresses, and the hub relay rendezvous. The phone shows a 6-digit code; the human types it on the desktop, which shows the device name. No address, port, pin or key is ever typed. |
| 2 | Minimal text | Copy budget: titles ≤ 3 words, body ≤ 1 short sentence, no explanatory paragraphs. Diagnostics collapse behind one "Details" disclosure. |
| 3 | Agent interface = desktop | Mount the desktop's own Conversation components (`Workspace`, `Timeline`, `ActionZone`, `Composer`, `Tray`, cards) in the phone column. Same story, same cards, same composer, same slash commands, stop, stop & send, queue, model settings, images. |
| 4 | PR review = desktop | Mount `ChangesView`/`DiffView` (inline comments → `cmd=feedback`), `BeforeMerge` (readiness, threads, merge, request review, ask agent to address), and the review workspace (packages, screenshots, checks, mark reviewed). |
| 5 | Everything that advances a session | Full parity list in §6; the daemon gate, not the client, decides what a device may do. |
| 6 | Performance | Server push replaces all polling; deltas, not snapshots; virtualized lists; one shared clock; terminal attaches only on screen. Budgets in §8. |
| 7 | Direct Wi-Fi/VPN automatic, optional | The phone races a direct path (known LAN/VPN addresses, mDNS) against the hub relay and uses the first that authenticates, preferring direct. Switching paths is invisible. No setting is required; relay needs a joined hub, direct needs `[remote].bind` beyond loopback. |
| 8 | Beautiful | New information architecture (§7), native-feeling motion and sheets, and three visual critique rounds against simulator screenshots. |

## 3. Security model (the part that must not be wrong)

The phone becomes a second human surface. That is a trust-boundary change and
is made explicitly, not by loosening a list.

### 3.1 Device identity and pairing (replaces `lola_insecure`)

- The daemon identity stays the P-256 key in `~/.lola/device.key`; the phone
  pins its SPKI.
- The phone generates a P-256 key in the Keychain (Secure Enclave when
  available) and presents a self-signed client certificate. TLS 1.3 becomes
  **mutual**: `ClientAuth: RequireAnyClientCert`, then the client SPKI is looked
  up in `~/.lola/devices.json` (0600).
- Pairing: `cmd=pairBegin` (local socket only) mints a one-time secret valid
  for 5 minutes and returns a QR payload `lola2.<base64url>` with
  `{v, pin, addrs, port, secret, relay?: {url, rv, tok}}` (`tok`: a random
  pairing-window token for the hub, never derived from the secret). The phone
  connects (any path), and its first frame is `remote.pair` carrying an HMAC
  over the TLS exporter value with the secret, plus a device name. Both sides
  derive a 6-digit code from the exporter + both SPKIs; the PHONE shows it and
  the human TYPES it on the desktop (Settings → Phones or `lola pair`), which
  never displays it — a displayed code plus a Confirm button would prove no
  comparison. Only `cmd=pairConfirm` from the local socket, carrying the typed
  code (3 tries), writes the device into the registry. The secret is
  single-use and is burned by the first VALID proof; a wrong proof closes only
  that connection (5 strikes fail the pairing), so a stranger on the LAN
  cannot cancel pairings. The pairing connection closes after its outcome and
  the phone reconnects as a paired device. Details: `docs/mobile/01-pairing-and-tiers.md`.
- A paired connection's first frame from the daemon is `ready` (device id,
  name, tiers, path); TLS 1.3 completes before the server judges the client
  certificate, so only `ready` means authenticated. A post-handshake refusal
  is an `err` frame `not_paired`.
- Revocation: `cmd=revokeDevice` removes the entry, closes its live
  connections and drops its relay rendezvous at the hub. No other device is
  affected. A tier edit (`cmd=setDeviceTiers`) also closes the device's
  connections, so its next `ready` carries the new tiers.
- `internal/remote/insecure.go`, the bearer key, `remote.hello`, the connect
  code `lola1.` and the build tag are deleted. An untagged build listens.
- Pairing and device commands stay unreachable from any remote peer and from
  the sandbox bridge.

### 3.2 Capability tiers — a closed allowlist

`internal/remote/policy.go` flips from denylist to **allowlist**. A command no
tier names is refused. Each device carries a set of tiers, chosen at pairing
and editable on the desktop:

| Tier | Commands (representative) | Default |
|------|---------------------------|---------|
| `read` | `sessions`, `projects`, `status`, `pane`, `panes`, `diff`, `checkpoints`, `checkpointDiff`, `prReadiness`, `talkState`, `agentFeed`, `agentFeedOutput`, `sessionTimeline`, `endedSessions`, `reviewPackages`, `reviewPackageDiff`, `reviewPackageImage`, `reviewPackageChecks`, `prs`, `tickets`, `hubStatus`, `bestOfCompare`, `attachmentImage`, `relayCred`, `watch`/`unwatch`, pane `sub` (read-only) | on |
| `answer` | `agentSay`, `agentChoose`, `agentStop`, `agentQueueRelease`, `agentSetting`, `answer`, `feedback`, `attachImage`, `reviewPackageMark`, `reviewPackageFeedback`, `sshRecall`, `sshAnswer` (not for an SSH-scoped element) | on |
| `control` | `openPr`, `openTicket`, `openManual`/`openExisting` (new worktree only; a shell needs `shell`), `revive`, `review`, `coderabbit`, `resolveConflict`, `addressPRFeedback`, `mergePR`, `switchAgent`, `dev`, `devFreePort`, `sessionTask`, `renameSession`, `broadcast`, `bestOf`, `restoreCheckpoint`, `forkCheckpoint`, `pollOnce`, `enable`, `disable`, `reviewPackageRecover` | on |
| `destructive` | `kill` (never `force`), `closeExitedTemporary`, `bestOfPick` (kills the losing attempts) | on |
| `shell` | pane `pty` write, `shellCreate`, `paneClose`, `paneResize`, `open`, `reviewPackageVerify`, `reviewPackageCapture` (each runs a command line or agent-writable code) | on, shown as a named toggle at pairing |

Never reachable by a phone, whatever its tiers: `stop`, `reload`,
`renameProject`, `hookEvent`, `agentReport`, pairing/device commands, config
writes (except `enable`/`disable`), `openURL` (it opens the Mac's browser; the
phone opens URLs itself), every `protocol.HubHumanCommands` entry, every ssh
host command (`sshOpen`, `sshApprove`, `sshPromptAnswer`, `sshProposal`,
`sshWidgetStatus`, …), and everything outside §6 (plans, goals, knowledge,
review profiles). SSH sessions are visible read-only through
`sessionTimeline`; their host acts stay on the desktop. The full table is
`docs/mobile/01-pairing-and-tiers.md` §8.3.

Consequences in the code:

- The Conversation commands (including `sshRecall` and `sshAnswer`, which act
  on any session's Conversation store) move out of `protocol.SSHHumanCommands`
  into their own `protocol.TalkCommands` list, so "phones get no ssh human
  commands" stays literally true and stays pinned by
  `TestEverySSHCommandIsDenied`.
- A new read-only `sessionTimeline` command exposes lola's story for a session
  to a remote peer **with private Run blocks, prompt cards, host/user/cwd, the
  handback transcript and widget documents removed** (`Private` until
  `sshShareBlock` stays a desktop-only fact; widgets never run on a phone).
- Every phone command still goes through the same daemon handlers and so the
  same idle gate, pane proof, `lockHumanPrompt`/`lockHumanMessage`, and
  `typedNote`. A phone send is a human send; nothing new types into an agent.
- `normalizeRequest` keeps clearing `Force` and now also drops fields a tier
  does not own (`confirmUnready` on `mergePR` requires `control` and is
  audited).
- Every mutating remote command writes an audit line naming the device.
- Rate limits per device; request concurrency stays bounded.

### 3.3 Hub blind relay

- **The hub carries ciphertext only.** The byte stream inside the relay is the
  same mutual-TLS 1.3 session a LAN connection uses, terminated at the phone
  and the daemon. The hub never holds a key that decrypts it, never sees a
  command, session id, path or pane text. This is `hub/DESIGN.md`'s
  "Plaintext phone traffic — never" row, made concrete.
- **Outbound only, as today.** The daemon dials the hub; the hub never dials a
  daemon. `GET /v1/relay/daemon` (WebSocket, device-signed with the existing
  ed25519 hub key) is the daemon's standing control channel. A phone dials
  `GET /v1/relay/phone` (WebSocket) with a rendezvous id and a bearer the
  daemon minted at pairing; the hub stores only its SHA-256. On a match the hub
  tells the daemon over the control channel, the daemon dials
  `GET /v1/relay/accept` for that connection, and the hub splices the two
  sockets.
- A relayed connection is handed to the unchanged `remote.Server` frame loop as
  a `net.Conn` wrapped in server-side TLS. One code path for LAN and relay.
- The phone presents its rendezvous id and bearer in `Lola-Relay-Rv` /
  `Lola-Relay-Bearer` headers. Its bearer is minted at pairing (a fresh rv,
  never the QR's) and rotated through a read-tier `relayCred` request the phone
  makes after each authenticated session; the daemon registers hashes only,
  with a signed replace-all POST. Contract: `docs/mobile/03-hub-relay.md`.
- A hostile hub can drop, delay or observe traffic volume. It cannot read,
  inject or impersonate: both ends pin each other's SPKI.
- The hub adds no dependency: WebSocket (RFC 6455, binary frames) is
  implemented on `net/http` hijack in `hubproto/` so daemon and hub share one
  codec. Limits: per-device relay count, idle timeout, byte-rate cap, max
  frame; every limit is a constant in `hubproto`.
- Wire goes into `hubproto/` first and stays backward compatible: a hub
  without `/v1/relay/*` answers 404 and the daemon marks relay unavailable; an
  old daemon never calls it.
- Relay is opt-out: `[remote].relay = true` by default once a device is paired
  and a hub is joined; `false` keeps the daemon LAN/VPN only. `[remote].bind =
  "off"` stops listening on every interface but no longer stops the relay (a
  relay-only daemon runs a listener-less `remote.Server`).
- Ingress needs WebSocket timeouts (`proxy-read-timeout`/`proxy-send-timeout`
  3600); the handler hijacks, so the server's 30 s write timeout no longer
  applies to it.

### 3.4 Not in this build

- **Push notifications (APNs).** Needs the publisher's APNs key and the hub
  push gateway. The event model below is what a push gateway would later
  summarise; nothing here blocks it.
- Android.
- tsnet. A VPN or WireGuard interface already works as a direct path once
  `[remote].bind` includes it.

## 4. Wire: push instead of polling

Protocol version becomes 2 and the daemon requires it on every frame (M1
clients are gone with the bearer key). Contract: `docs/mobile/02-events.md`.

- New frame type `event`: `{type:"event", id, topic, seq, payload}`; `id` is
  the `watch` request's id, so a re-watch needs no ordering against in-flight
  events. An `err` frame on a watch id ends that one stream.
- `watch {topics:[…]}` / `unwatch` requests. Topics:
  - `sessions` — first event is a full snapshot, then deltas
    `{upsert:[SessionInfo], remove:[id]}`.
  - `projects`, `status` — replace-on-change.
  - `talk:<session>` — `TalkStateData` on change (composer, prompt/cards,
    queue, feed seq/epoch, health).
  - `feed:<session>` — appended `AgentFeedItem`s after a cursor.
  - `timeline:<session>` — upserts of lola's story items.
  - `diff:<session>` — a "changed" tick; the client refetches lazily.
- The daemon owns the cadence: one watcher per (connection, topic) computes a
  cheap fingerprint when the underlying source moves (observer pass, hook
  event, feed advance) or on a short tick while a session is working, and sends
  only on change. A phone with the screen off receives nothing and costs
  nothing.
- `seq` is per topic; a gap means `watch` again and take the snapshot.
- Backpressure: a slow connection coalesces to the latest state per topic and
  never blocks the daemon.
- `attachImage {session, name, mime, data(base64), part, last}`: chunked upload
  into `<worktree>/.lola/images`, size-, type- and quota-capped, returning the
  same attachment token the desktop composer produces; `attachmentImage`
  (read, chunked) lets the phone show an attachment.

## 5. Client architecture (technical overhaul)

Keep Capacitor + Svelte: it is what makes "equal to the desktop" true instead
of approximate. Rework what is underneath.

- **Transport plugin (Swift).** Client-certificate identity in the Keychain;
  two dialers behind one `Path` protocol (direct `NWConnection`; relay
  `URLSessionWebSocketTask` piped through a loopback bridge into the same
  `NWConnection` TLS stack, because only URLSession exposes a refused upgrade's
  status); a racing connector with happy-eyeballs-style stagger (direct first,
  relay after 300 ms, first path to deliver `ready` wins, the others are
  cancelled); seamless re-race on network change (`NWPathMonitor`) and on
  foreground; resume re-`watch`es topics. One typed plugin surface; the six
  ad-hoc `globalThis.Capacitor.Plugins` probes are deleted. Secrets (pairing
  secret, relay token and bearers) never cross the Capacitor bridge, and the
  bridge accepts messages from the app's main frame only. Contract:
  `docs/mobile/04-client.md`.
- **Shim.** `mobile/src/wailsshim/desktop.ts` is split per service and
  completed: `TalkService`, a read-only `SSHService` subset backed by
  `sessionTimeline`, and the missing `DaemonService` methods (diff, feedback,
  checkpoints, readiness, merge, best-of, review packages, images). A type
  test asserts the shim satisfies the generated binding signatures, so drift
  fails `npm run check` instead of the user.
- **Push bridge.** `bridge.ts` stops polling: it `watch`es topics and feeds the
  desktop stores. `talk.svelte.ts`, `feed.svelte.ts` and the timeline fetch get
  a small "pushed source" seam so desktop keeps its polling and mobile uses
  events — same store, two drivers.
- **Bugs.** Offline snapshot (`retainAll`), stale denial mirror (deleted — the
  server's allowlist is the truth and a refusal no longer closes the socket),
  Activity list key, `geom` churn in the terminal.
- **Hygiene.** Dead desktop-only paths removed, duplicated constants merged,
  history-essay comments cut to the invariant they protect, files over ~500
  lines split along their seams, `mobile/README.md` and `HANDOFF.md` rewritten
  to the new truth.
- **CI.** A `mobile` job: `npm run check`, `npm test`, plugin Swift tests via
  `swift test` on the pure `LolaTransportCore` target.

## 6. Parity list (goal 5)

Everything below is reachable on the phone when the device holds the tier:

- Read: sessions, Conversation story, tool output, subagent threads, changes
  and diffs, checkpoints, PR readiness and review threads, review packages and
  screenshots, checks, activity, projects, hub status.
- Speak: message, slash command, stop, stop & send, release queue, recall,
  model/effort/speed, attach image (camera, photos, paste), long-paste folding.
  (No `!` line on any surface: the daemon defuses a leading `!` everywhere.)
- Decide: permission, question and modal cards (hash-pinned `agentChoose`),
  question elements (`sshAnswer`, not for SSH-scoped ones), background a
  command. Widgets and SSH proposals stay on the desktop.
- Review: inline diff comments and a note (queued, never forced), mark
  reviewed, request agent review, CodeRabbit, ask agent to address feedback,
  merge with method, resolve conflicts.
- Steer: open PR / ticket / worktree, revive, switch agent, dev server toggle
  and open URL, session tasks, rename, broadcast, best-of compare and pick,
  checkpoint restore and fork, remove session (confirm).
- Terminal: live agent pane and shells with the accessory bar, as today.

Stays on the desktop: config and project editing, hub team administration and
claims, ssh host acts, widgets, pairing and device management, forced kill,
daemon stop/reload, opening a URL in the Mac's browser.

## 7. Information architecture and design

- **Connect.** One screen: camera viewfinder, one line of text. After the
  scan, the 6-digit code to type on the Mac. Then you are in. A quiet link path indicator (direct
  / relay) lives in Settings, not on screen.
- **Inbox** (home). What needs you first, as large cards with the one action
  that resolves each (answer a question, approve, merge, look at red CI). Below
  it, working / in review / done as dense rows. Pull to refresh is gone —
  nothing is stale.
- **Session.** Full-height Conversation. A top segmented control: Conversation
  · Changes · Review · Terminal. The composer and cards dock above the
  keyboard. The session menu is one sheet.
- **Review.** Readiness at the top (checks, threads, mergeable), the diff
  below with tap-to-comment, a sticky bar: "Send feedback (n)" / "Merge".
- **Projects**, **Activity**, **Settings** remain tabs; Settings shrinks to
  device, theme, about.
- Visual language: the desktop's Catppuccin tokens, larger touch type scale,
  44 pt targets, sheets with detents, spring transitions tied to gestures,
  haptics on decide/send, reduced-motion respected. Sentence case, no
  explanatory paragraphs.
- Desktop gets **Settings → Phones**: QR, pairing confirm (type the phone's
  code; "via relay" shown), device list with tiers and revoke, relay status
  and the `[remote].relay` switch.

## 8. Performance budgets

- Cold start to interactive inbox: < 1.0 s on a warm connection, < 2.5 s cold
  over relay.
- Reconnect after foreground: < 500 ms direct, < 1.2 s relay.
- Send-to-echo for a composer message: one round trip; no polling delay.
- Idle cost: zero requests per second with nothing changing.
- Scrolling: lists virtualized over 50 rows; no layout work per push for rows
  that did not change (keyed deltas); one shared clock for relative times.
- Bundle: route-level code splitting so the terminal (xterm) and the diff
  viewer load on first use.

## 9. Execution

Five workflows, run in order, each reviewed before the next starts. Builders
are Sonnet; architects, security reviewers and visual critics are Opus. The
work breakdown of workflows 2–4 (waves, items, file ownership, interfaces,
tests, gates) is `docs/mobile/00-build-plan.md`.

1. **Contracts.** Per-area specs under `docs/mobile/` (pairing + tiers, events,
   relay, client seams) written against the real code, then attacked by
   independent security and compatibility reviewers and revised.
2. **Daemon + hub.** Pairing and registry, allowlist tiers, event push,
   `sessionTimeline`, `attachImage`, hub relay, daemon relay client, desktop
   Phones settings, CLI. Gate: `make check`, `cd hub && make check`, review.
3. **Client foundation.** Swift transport (identity, racing, relay), shim
   completion, push bridge, bug fixes, hygiene, CI job. Gate: `npm run check`,
   `npm test`, `swift test`.
4. **Interface.** Connect, Inbox, Session (Conversation, Changes, Review,
   Terminal), sheets, desktop Phones UI — then three visual critique rounds on
   simulator and preview screenshots, each producing fixes.
5. **Hardening.** End-to-end run against a real daemon in the simulator,
   adversarial security review of the finished boundary, performance pass
   against §8, docs (`AGENTS.md` invariants, `mobile/README.md`,
   `hub/DESIGN.md`, `README.md` `[remote]`).

## 10. What will remain unverified by this build

- Anything needing hardware: the camera scan, Secure Enclave key generation,
  real network handover between Wi-Fi and cellular.
- The relay against the deployed hub: it is built and tested locally; the hub
  deploys from `main` and is not deployed by this work.
- App Store signing and distribution.
