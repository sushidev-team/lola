# Hand-off — the lola mobile app

What is not yet proven, and the few traps worth knowing first. Design:
`docs/mobile-rethink.md` and `docs/mobile/01..04-*.md`. Operating manual:
`mobile/README.md`. Rules: `AGENTS.md` (Phones, Server push, Phone client, the
hub relay). `mobile/PLAN.md` is the historical M1 record.

## Proven by tests

Wire, event, pairing and tier vectors (Go-written, read by both sides); tiers,
request rebuild, pairing and revocation in `internal/remote` and
`internal/daemon`; the relay against fakes and a loopback hub
(`hub/e2e`, `internal/remote/relay_test.go`); the shim, stores, screens and
sheets under vitest; the bundle and perf budgets; Swift Core under
`LOLA_CORE_ONLY=1 swift test`.

## Unverified

Each needs hardware, a deployed service or a signing account. None is
exercised in this tree.

- **Camera.** The QR scanner on a real iPhone (the Simulator has no camera;
  `dev-link.sh --pair` feeds the payload through the launch environment).
- **Secure Enclave.** Client key generation and `SecIdentity` lookup on a
  device. The Simulator falls back to a software key.
- **TLS exporter.** iOS `sec_protocol_metadata_create_secret` (no context)
  equal to Go's `ExportKeyingMaterial(label, nil, 32)`. The vectors prove the
  HMACs, not the exporter; a mismatch breaks every pairing.
- **Real handover.** Wi-Fi to cellular, sleep and wake, and direct-to-relay
  migration timing on a device.
- **Local-network permission.** The iOS prompt and a denial, on hardware only.
- **Deployed hub.** The relay against `lola.sushidev.kitchen` is not live.
  The Hub workflow run for the mobile-rethink merge (59da966c, run
  43693322369) failed at `make check` on a flaky `TestDevSetsUpAndServes`
  ("TempDir RemoveAll cleanup: ... directory not empty"), so build-image and
  deploy were skipped and the hub still answers 404 on every `/v1/relay/*`
  path. The cause was `runHub` returning while its sweep, recheck, escalation
  and digest goroutines could still write into `--data`; it now waits for
  them (`hub/cmd/lola-hub/serve.go`, 50 race runs clean). The daemon now
  leaves its `unsupported` hold on `lola hub sync` or within 15 minutes of a
  heartbeat, and the phone lifts its `hub_too_old` pause on the next
  `relayCred` that says available, so neither waits 6 h after the upgrade.
  Still to do: deploy the hub from `main`, check that
  `curl -si https://lola.sushidev.kitchen/v1/relay/phone` answers with a
  `Lola-Relay-Code` header, run `lola hub sync`, then a cellular smoke test
  (connection sheet Path = Relay; `lola hub status` relay line "on" with one
  phone through the relay). Ingress timeouts and the `--client-ip` bucket are
  unmeasured. The relay needs a joined team hub; there is no solo path.
- **Bridge guard in WebKit.** That WebKit exposes `bridge` to a sandboxed
  `data:` frame and `BridgeGuard.swift` drops it (`/dev/bridge-guard`).
- **App Store signing and distribution.** No TestFlight or App Store build has
  been made; device builds use a personal team.
- **Feel.** DiffView scroll on an older iPhone, haptics, long press on glass
  (jsdom has no `PointerEvent`), pinning a real tmux window to phone width.

Not built: push notifications (APNs), Android, tsnet.

## Traps

- **`make build` never reaches the running daemon.** Use `make daemon` (or
  `make daemon-dev`) and restart it. A stale daemon answers `unknown cmd`;
  check `go tool nm $(command -v lola) | grep <symbol>`.
- **Never drive the UI with synthetic OS input.** Reach screens with
  `dev-link.sh` or `preview.html`; see `mobile/README.md`.
- **Secrets never cross the Capacitor bridge.** Capacitor logs every resolved
  payload in Debug builds. The pairing secret, the QR `tok`, relay bearers and
  the client key stay in Swift and the Keychain.
- **Discovery finds, TLS trusts.** An mDNS address is a candidate only.
  `NSBonjourServices` must list `_lola._tcp`, or iOS returns nothing and no
  error.
- **Pane pins.** Releasing a pinned window takes `resize-window -A` after
  unsetting `window-size`. `PaneResizeData.pinned` is the only field that says
  which way a call went. A refused pin (`DaemonError`) proves nothing landed; a
  timeout is assumed to have landed. A release of a pane that is gone is
  success.
- **Pane names are identity.** Match tmux names anchored at both ends
  (`lola-fe-42` is a prefix of `lola-fe-420-shell-1`). A pane rename is a label
  on the phone, never a tmux rename. The agent pane can be resized, never
  closed.
