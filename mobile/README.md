# Lola mobile

The iPhone companion to lola: a Capacitor shell around the desktop app's own
Svelte components (Conversation, Changes, Review), talking to the daemon through
a native transport plugin. Design: [`docs/mobile-rethink.md`](../docs/mobile-rethink.md)
and the specs in [`docs/mobile/`](../docs/mobile/) (build order in `00-build-plan.md`,
client in `04-client.md`). Hand-off notes: [HANDOFF.md](HANDOFF.md).

## 1. What this is

`mobile/` is a second front end for the same daemon. It reuses
`desktop/frontend/src/lib` through a shim, and a few small seams in that tree
(pushed sources, one `data-queue-row` attribute) are written for the phone's
sake and keep the desktop polling exactly as before.

| Specifier | Desktop | Mobile |
| --- | --- | --- |
| `$lib/*` | its own `src/lib` | the same directory, aliased |
| `@bindings/internal/protocol` | generated DTO types | the same files (types only) |
| `@bindings/desktop` | generated Wails IPC | `src/wailsshim/desktop.ts` |
| `@wailsio/runtime` | the Wails event bus | `src/wailsshim/runtime.ts` |

`src/wailsshim/conformance.ts` fails `npm run check` when the shim drifts from
the generated bindings: a new bound method needs a shim route in the same change.

### Connecting

- **Pairing.** No build tag, no shared key. On the Mac run `lola pair` (or open
  Settings, Phones), scan the QR in the app, then type the 6-digit code the
  phone shows into the Mac. Each phone has its own key and its own capability
  tiers (`read`, `answer`, `control`, `destructive`, `shell`); the list is on
  the Mac (`lola devices`) and revocation is immediate.
- **Direct or Relay.** The app races the paths it knows: a direct TLS 1.3
  connection to the Mac (needs `[remote].bind = "lan"` or an IP, or a VPN) and,
  when a team hub is joined, the hub relay (`[remote].relay`, default on). The
  relay carries the phone's own end-to-end TLS session; the hub cannot read it.
  Settings, Connection shows which path is live.
- **Pushed updates.** The phone watches topics and the daemon pushes `event`
  frames; the phone does not poll. A topic the daemon refuses falls back to
  polling for that store only.
- **Tiers hide, never grant.** The daemon gate is the truth. A refused action
  shows the daemon's own sentence.

### Screens

Inbox (what needs you; the Filter sheet can group it by status or by project), Activity, Projects and Settings in the tab bar; Session
(Conversation, Changes, Review, Terminal) and Connect on top. Phone copy follows
the budget in `04-client.md` §5.5: titles up to three words, one short sentence
of body.

## 2. Prerequisites

| Thing | Version |
| --- | --- |
| macOS, Xcode | Xcode 26 or newer |
| Node | 22 or newer |
| Go | per `go.mod` |
| iPhone / Simulator | iOS 15 or newer |

A free Apple ID signs a device build; its profiles expire after seven days.
The Simulator has no camera and no local-network privacy, so scanning and the
permission prompt are only proven on a device (section 7).

## 3. Build and run

```sh
(cd ../desktop/frontend && npm ci)            # shared components resolve their deps here
(cd plugins/lola-transport && npm ci)
npm ci
npm run sync                                   # plugin JS, web build, cap sync ios
npm run sim                                    # build, install and launch on the newest iPhone simulator
npm run sim -- -l                              # live reload from the vite dev server
npm run ios                                    # sync, then open Xcode
```

`LOLA_SIM="iPhone 17 Pro" npm run sim` picks a simulator by name or UDID.
Safari's Web Inspector is on only for `sim` and `device` builds
(`LOLA_WEB_INSPECTOR`).

## 4. The daemon side

```sh
make daemon          # from the repo root: install the working-tree lola on PATH; starts nothing
make daemon-dev      # the same, then run it in the foreground
```

The new binary is not live until the daemon restarts (TUI `^r`, the app's
restart, or `daemon-dev`). Then in `~/.lola/config.toml`:

```toml
[remote]
enabled = true
bind = "lan"      # a direct path; "off" stops listening but not the relay
# relay = true    # the default; needs a joined hub
```

and pair with `lola pair`. `lola doctor` has a `phones` row for a broken
registry. The tier table is `internal/remote/tiers.go`; the client reads it from
`src/wire/testdata/tiers.json` and never spells a tier by hand.

## 5. Tests

```sh
npm run check                  # svelte-check, including the shim conformance test
npm test                       # vitest: wire, shim, stores, screens, sheets, perf
npm run build && npm run budget
npm run test:device
(cd plugins/lola-transport && LOLA_CORE_ONLY=1 swift test)   # Swift Core, Mac only
(cd ../desktop/frontend && npm run check && npm test)        # the desktop must stay green
```

Wire vectors are Go-written (`LOLA_WRITE_VECTORS=1`) and read by both sides
(`src/wire/testdata/`).

**Budgets** (`npm run budget`, `scripts/bundle-budget.mjs`): xterm and the diff
viewer stay out of the entry chunk; gzip ceilings are entry 220 KB, `session`
260 KB, `terminal` 200 KB, `diff` 90 KB. `src/perf/*.test.ts` hold the idle
(zero requests per second), send-to-echo (one request), keyed-delta and
virtualization budgets. Cold-start and reconnect times are measured on the
Simulator and a device (`04-client.md` §8.1).

### Reaching a screen without tapping

Never drive the UI with synthetic OS input (no `osascript`, `cliclick`,
CGEvent). Use, in order:

1. **vitest + `fireEvent`** for behaviour.
2. **`xcrun simctl io <udid> screenshot`** for appearance.
3. **Deep links.** `scripts/dev-link.sh --route /session/s1/conversation --sheet sessionMenu`
   relaunches the app on a booted Simulator (`LOLA_SIM` picks one) with
   `SIMCTL_CHILD_LOLA_DEV_LINK`. Only the Debug configuration compiles the
   link reader (`LOLA_DEV_LINK` in the plugin's `Package.swift`). A screen no
   link reaches is a bug: add a link target, never a tap.
4. **The preview harness.** `npm run dev`, then
   `http://localhost:<port>/preview.html?route=/inbox&fixture=busy&theme=catppuccin-mocha&kb=336`
   mounts the real app against a scripted Mac (`src/fixtures/`: `empty`, `busy`,
   `permission`, `dialog`, `question`, `long-story`, `composer`, `diff`,
   `merge-ready`, `ci-red`, `review-package`, `terminal`, `offline`,
   `key-changed`, `pairing`, `settings`). Without `fixture=` the harness picks
   one that serves the route. It reaches no daemon and no Keychain.

Routes (`src/lib/router.svelte.ts`), each optionally `?sheet=<name>`:

| Route | Screen |
| --- | --- |
| `/inbox[?filter=needs\|working\|review\|done]`, `/activity`, `/projects`, `/settings` | the four tabs |
| `/projects/<project>[/prs\|/tickets]` | project, PR picker, ticket picker |
| `/session/<id>/conversation\|changes\|review\|terminal[/<pane>]` | a session segment |
| `/connect[/code\|refused\|expired\|failed\|dropped\|not-paired\|no-camera]` | pairing states (only while unpaired) |
| `/dev/bridge-guard` | the widget bridge guard probe (not in production builds) |

Sheets: `sessionMenu`, `newSession`, `attach`, `model`, `merge`, `feedback`,
`checkpoints`, `bestOf`, `devLinks`, `rename`, `broadcast`, `tasks`,
`switchAgent`, `paneMenu`, `viewSettings`, `filter`, `connection`,
`reviewPackage`, `unpair`.

To pair a Simulator, run `scripts/dev-link.sh --pair [--route …]`. It runs
`lola pair --json`, hands the payload to the app through the launch
environment (the Simulator has no camera), reads the code the Debug build logs
(`lola-dev pair-code <code>`; release builds have no such line) and writes it as
the one stdin line `lola pair` waits for. Nothing is confirmed without that
line. Once paired, links carry only `route` and `sheet`: no credentials.
`lola pair` refuses a QR that names only loopback unless a hub is joined, so
pair with `bind = "lan"`; the hidden `lola pair --allow-loopback` (which
`dev-link.sh` does not pass) accepts a loopback-only QR for the Simulator.

Screenshots: `xcrun simctl io booted screenshot <file>.png`, written outside the
repo.

## 6. Troubleshooting

- **Not paired / "key changed".** The Mac no longer knows this phone's key, or
  its identity changed. Pair again: `lola pair`. Revoking a device on the Mac
  produces the same screen on the phone by design.
- **Direct fails, relay works.** The phone is off the Mac's network, or
  `[remote].bind` is `localhost`. Use `bind = "lan"` or stay on the relay.
- **Relay unavailable.** It needs a joined hub (`lola hub status`),
  `[remote].enabled` and `relay = true`; an older hub answers 404 and the phone
  stays LAN-only. `lola doctor` names the reason.
- **Local-network prompt denied.** iOS reports it as an unreachable host. Allow
  Lola under Settings, Privacy, Local Network. The Simulator never prompts.
- **Guest Wi-Fi / AP isolation.** Phone and Mac cannot see each other; use the
  relay or a VPN.
- **Every plugin call rejects "not implemented".** The web build ran without the
  native plugin (a browser, not the app). Use `npm run sim`.
- **Blank or unstyled screen.** Rebuild with `npm run sync`; check the Web
  Inspector console.
- **The new daemon command answers `unknown cmd`.** The running daemon is older
  than the build: `go tool nm $(command -v lola) | grep <symbol>`, then restart.

## 7. Running on a physical iPhone

Three things exist only on hardware: the camera (QR scan), the local-network
permission and real network transitions. `scripts/run-device.sh` builds,
installs and launches without opening Xcode:

```sh
npm run device                     # a connected phone
npm run wireless                   # a paired Wi-Fi phone
./scripts/run-device.sh --list     # known devices
LOLA_DEVICE=<name-or-udid> ./scripts/run-device.sh
```

The first run needs Xcode-owned steps the script checks and names: a signing
team (set it once in Xcode for the `App` target), a bundle id that is yours if
`dev.sushi.lola.mobile` is taken, and Developer Mode on the phone. A device
cannot reach a daemon bound to loopback: set `[remote].bind = "lan"` and pair
with the QR.

Unverified without hardware (see `04-client.md` §14): the camera scan, Secure
Enclave key generation, the TLS exporter match between iOS and Go, Wi-Fi to
cellular migration, relay against the deployed hub and the DiffView scroll
budget on an older phone.
