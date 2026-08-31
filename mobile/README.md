# Lola mobile (M1)

The iPhone companion to lola: a Capacitor shell around the **desktop app's own
Svelte component library**, talking to the daemon's `internal/remote` listener
through a native TLS transport plugin.

The architecture decision and the full milestone plan are in [PLAN.md](PLAN.md).
This file is the operating manual: what M1 is, what you need, and the exact
sequence that gets it onto a phone.

---

## 1. What this is

`mobile/` is a second front end for the same daemon, not a second product. It
reuses `desktop/frontend/src/lib` unmodified — the store, `theme.ts`,
`filters.ts`, `StatusPill`, `PrBadge`, `AgentActivity`, `Checkbox`, `Button` —
by pointing three module specifiers somewhere else at build time:

| Specifier | Desktop | Mobile |
| --- | --- | --- |
| `$lib/*` | its own `src/lib` | the same directory, aliased |
| `@bindings/internal/protocol` | generated DTO types | the same files (types only, erased at build) |
| `@bindings/desktop` | generated Wails IPC | `mobile/src/wailsshim/desktop.ts` |
| `@wailsio/runtime` | the Wails event bus | `mobile/src/wailsshim/runtime.ts` |

The alias table lives in `mobile/vite.config.ts`, mirrored for the type checker
in `mobile/tsconfig.json`. Nothing under `desktop/` is written by this project.

### What M1 does

- Connect to one daemon by address, SPKI pin and bearer key.
- Read the session list: attention-first ordering, the five triage chips derived
  from `state.KanbanColumns()`, free-text search over issue key, title and
  project.
- Attach to one live agent pane over the remote protocol, render it with
  xterm.js, pan and pinch it, and type into it — including Escape, Ctrl-C,
  Shift-Tab, Shift+Enter and the arrow keys, from an accessory bar that encodes
  the bytes itself.
- Scroll the agent's own transcript through the daemon's scroll RPC.

### What M1 does not do

- **No pairing and no cryptographic identity.** Authentication is a shared
  bearer key from `LOLA_REMOTE_INSECURE_KEY`. That path only exists in a daemon
  built with `-tags lola_insecure`, and that build **forces the listener to
  loopback** whatever `[remote].bind` says. Section 4 is about the consequence.
- **No mutating controls beyond typing into a pane.** No kill, revive, review,
  agent switch or dev toggle; those are M3.

  There is a trap armed for whoever adds the first one. Five reused store
  actions — `askKill`, `askForceKill`, `askFreePort`, `askSwitchAgent`,
  `askStopDaemon` in `desktop/frontend/src/lib/store.svelte.ts` — push a request
  into `$lib/confirm.svelte` and wait for a mounted `ConfirmDialog` to resolve
  it. The mobile app mounts none, so any of them would set `confirm.request` and
  then do nothing at all: no dialog, no flash, no error. `store.kill(id)` reaches
  it without looking like an `ask*` call, by falling back to `askForceKill` on
  the daemon's dirty-worktree refusal. Nothing in `mobile/src` calls a mutating
  store action today, so the trap is armed and untripped. Mount a mobile
  `ConfirmDialog` bound to the same `confirm` store when the first one lands.
- **No config editing.** Every `ConfigService` write rejects with a named error.
- **No push, no offline staleness banner, no aux pane picker, no Android.**
- **No theme picker, and the phone is dark on purpose.** The desktop and the TUI
  both paint from `[ui].theme`, `catppuccin-latte` included; the phone paints
  from `theme-runtime`'s cached flavor and falls back to the compiled default,
  because no remote command reads the Mac's `[ui].theme` and the phone's flavor
  is the phone's. iOS light appearance therefore changes nothing here. That is a
  stated shape rather than an omission: a terminal-centric surface held at
  arm's length is a dark surface, and following the system would mean rendering
  an agent's pane in a palette the agent's own colours were not chosen for.
- **The bearer key IS persisted, and this is the one M1 shape worth reading
  twice.** Ticking "Remember this Mac" writes the key to the iOS Keychain as a
  generic-password item under the endpoint's `host:port`, with
  `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`. Concretely: it survives
  standby, a relaunch and iOS reclaiming the backgrounded app; it never reaches
  iCloud Keychain, an encrypted backup or a restore onto new hardware (a new
  phone always re-pairs); and it is readable by this app from the first unlock
  after a reboot onward, which is what lets the app reconnect on its own with
  nobody watching. `mobile/src/lib/secretstore.ts` still degrades to an
  in-memory map if the Keychain refuses, and the connect screen then says the
  key will have to be typed again rather than claiming otherwise.

  The plaintext never comes back across the bridge. The plugin has no method
  that returns it — `secretHas` answers a boolean and `connect` takes a
  `keyRef` naming the Keychain account, so the native side does the read. That
  is not tidiness: Capacitor's own bridge logs every resolved payload
  (`CAPLog.print("TO JS", result.jsonPayload())`), so an earlier `secretGet`
  printed the bearer key in cleartext to the app's console on every launch of
  every Debug build.

- **What that costs, stated plainly, because M1 has none of M2's mitigations.**
  A stolen phone that is unlocked reconnects on its own and can type into live
  coding agents. There is no per-device identity (the insecure listener labels
  every peer `"insecure"`), no capability tier enforced per frame, no epoch
  revocation, no device list on the Mac, no biometric gate and no audit line —
  all of those are M2, and none of them exists yet. The only revocation M1 has
  is regenerating the key on the Mac, which un-pairs **every** phone at once;
  the phone's own copy then goes when the daemon refuses it, or when you use
  **Forget this Mac** on the Disconnect sheet. Between the first unlock after a
  reboot and the next reboot, anything with code execution inside this app's
  container can read the item. If that trade is not one you want on a given
  phone, leave "Remember this Mac" unticked — the key then lives only as long as
  the app run, which is exactly what it did before.

- **Disconnect and forget are different, and both are on the Disconnect sheet.**
  Disconnect closes the socket for this run; the pairing survives, and the next
  launch reconnects. "Forget this Mac" deletes the Keychain item and the
  remembered address, so the next launch starts at the pairing screen.

### Two things that are wired but that only a device can prove

Both are invisible in `npm run dev`, because a desktop browser has neither a
soft keyboard nor Safari's gesture arbitration. Check them in the Web Inspector
on the first device run rather than assuming them.

Safari's Web Inspector is enabled by `scripts/run-sim.sh` and
`scripts/run-device.sh`, which export `LOLA_WEB_INSPECTOR=1` before `cap sync`.
It is **not** on by default any more: the config key reads that variable, so a
build made by any other route ships without a debugger. That changed when the
bearer key became durable — a build carrying a Keychain credential should not
have an attachable inspector unless somebody asked for one.

- **The keyboard inset.** `capacitor.config.ts` sets
  `Keyboard.resize: KeyboardResize.None` on purpose — letting the plugin resize
  the WebView fights the terminal's own measurement into a resize storm — so
  `mobile/src/lib/keyboardinset.ts` pays the height back as bottom padding
  itself. It reaches the plugin through a dynamic `import("@capacitor/keyboard")`
  rather than through `Capacitor.Plugins.Keyboard`, because that global is
  populated by the package's own `registerPlugin` call and by nothing else — a
  probe that never imports the package finds nothing on a device either, and the
  failure is silent: the accessory bar just sits under the raised keyboard.
  That failure has actually happened, which is why there is now a SECOND source:
  `window.visualViewport`, which shrinks by exactly the keyboard's overlap under
  `KeyboardResize.None` and needs no plugin at all. The two are combined by
  `Math.max`, so whichever is working carries the inset and the other reports 0.
  Confirm the bar rises with the keyboard; if it does not, neither source is
  reporting and the Web Inspector is the place to find out which.
- **`touch-action: none` on the pane.** The one-finger drag inside a terminal is
  a scroll RPC, not a browser pan, so Safari must not be allowed to claim the
  gesture — once it does, `preventDefault()` is ignored and the gesture arrives
  as `touchcancel`. `app.css` therefore keeps its blanket `*` rule inside
  `@layer base` (Tailwind emits utilities in `@layer utilities`, and an
  UNLAYERED author rule outranks a layered one regardless of specificity) and
  adds an unlayered `.term-pane` rule as belt and braces. Confirm the frame's
  computed `touch-action` reads `none`.

---

## 2. Prerequisites

| Thing | Version | Why |
| --- | --- | --- |
| macOS | 14 or newer | Xcode 26's floor |
| Xcode | 26 or newer | Capacitor 8's requirement |
| Node | 22 or newer | Capacitor 8's requirement |
| Go | 1.24 or newer | the daemon; the repo builds under 1.26 |
| A **physical iPhone** | iOS 15 or newer | the plugin's deployment target |
| An Apple ID | any, free is fine | code signing |

A free Apple ID works. Two things follow from it: its provisioning profiles
expire after **seven days**, so the app stops launching and has to be re-run
from Xcode; and the bundle id `dev.sushi.lola.mobile` may already be taken in
Apple's namespace, in which case pick your own (section 3, step 7).

The Simulator is genuinely useful here and is called out where it is, but it
cannot substitute for a device: iOS local-network privacy is not implemented in
the Simulator, so the permission prompt, the denial path and therefore the whole
first-connect experience pass there and can still fail on a phone.

---

## 3. From this checkout to the app on a device

Every command is given with the directory it runs in. Run them in this order.

### 1. Install the app's dependencies

```sh
cd mobile
npm install
```

Produces `mobile/node_modules`, and symlinks `plugins/lola-transport` in as the
`lola-transport` dependency. It does **not** build the plugin.

Three Capacitor plugins come in with it and all three need a `cap sync` (step 6)
before they exist on the native side: `@capacitor/keyboard` (the soft-keyboard
inset, see section 1), `@capacitor/browser` (opening a link the terminal printed
in a real browser chrome rather than inside the app's own WebView), and the
local `lola-transport`. `cap sync` runs `pod install` for you; if CocoaPods is
not installed, that is where it fails, and the message names the pod.

Two dependencies have **no importer** and are deliberate:
`@xterm/addon-fit` and `@xterm/addon-webgl`. `MobileTerminal.svelte` uses
neither — it sizes the grid from the resync frame and pans over it, because
fitting would reflow three quarters of a 200-column agent pane away. They are
here because `desktop/frontend/src/lib/components/LiveTerminal.svelte` imports
both, and the shim deliberately keeps `TermService.Attach` working so that
component stays reusable. Removing them would make that promise untrue the
first time somebody took it up.

### 2. Build the plugin's JavaScript

```sh
cd mobile
npm run build:plugin
```

Produces `mobile/plugins/lola-transport/dist/esm/`. This has to happen before
every sync, because `cap sync` copies whatever is in `dist/` and there is no
`prepare` hook doing it for you. `npm run sync` (step 5) runs it for you.

If `tsc` is not found, the plugin's own devDependencies were not installed:

```sh
cd mobile/plugins/lola-transport
npm install && npm run build
```

### 3. Build the web app

```sh
cd mobile
npm run build
```

Produces `mobile/dist/`. `build.outDir` in `vite.config.ts` and `webDir` in
`capacitor.config.ts` must stay equal; if they ever disagree, the app silently
ships the previous build.

### 4. Add the iOS platform

```sh
cd mobile
npx cap add ios
```

Produces `mobile/ios/App/App.xcodeproj` plus `ios/App/CapApp-SPM/Package.swift`.
Capacitor 8 defaults to Swift Package Manager, so there is **no** `.xcworkspace`,
no `Podfile` and no `pod install` step. `cap add` finishes by running a sync,
which is why step 3 comes first.

`mobile/ios/App/**` is meant to be committed — it carries the Xcode project, the
SPM manifest, the `Info.plist` and the icons. `mobile/.gitignore` already
excludes everything derived from it. Commit it once it exists.

### 5. Declare the local-network usage string

Open `mobile/ios/App/App/Info.plist` and add:

```xml
<key>NSLocalNetworkUsageDescription</key>
<string>Lola connects to the lola daemon on your Mac over your local network.</string>
```

This is required. iOS fires the local-network prompt on the first unicast to a
private-range address, and an app without the string is denied without ever
prompting. It does **not** fire for loopback, so a Simulator run or a phone-side
SSH forward never prompts — declare it anyway, because the LAN case does.

`NSBonjourServices` is deliberately not needed: nothing browses. No ATS
exception is needed either — App Transport Security does not apply to
Network.framework connections, so the self-signed endpoint needs no
`NSAppTransportSecurity` dictionary and `NSAllowsArbitraryLoads` must never be
added.

Optional, and only if the iOS 26 control restyling bothers you: add
`<key>UIDesignRequiresCompatibility</key><true/>` to opt the app out of Liquid
Glass. Every control in this app is drawn by the app itself, so this changes
very little.

### 6. Sync

```sh
cd mobile
npm run sync
```

That is `build:plugin && vite build && cap sync ios`. It copies `dist/` into
`ios/App/App/public/`, writes `capacitor.config.json`, and adds the plugin's
Swift package to `CapApp-SPM/Package.swift` by path. **Re-run this after every
source change**; Xcode alone does not pick up web changes.

### 7. Open Xcode and set signing

```sh
cd mobile
npx cap open ios
```

In Xcode: select the **App** project in the navigator, then the **App** target,
then **Signing & Capabilities**.

- Tick **Automatically manage signing**.
- Set **Team** to your Apple ID's personal team (Xcode → Settings → Accounts if
  it is not listed).
- If signing fails on the bundle id, change **Bundle Identifier** to something
  of your own (for example `dev.<yourname>.lola.mobile`) and put the same value
  in `mobile/capacitor.config.ts`'s `appId` so the two do not drift.

### 8. Run on the device

Plug the phone in, enable **Settings → Privacy & Security → Developer Mode** on
it (iOS 16 and newer, one reboot), select it as the run destination in Xcode,
and press Run. The first launch also needs the developer certificate trusted on
the phone: **Settings → General → VPN & Device Management → your Apple ID →
Trust**.

### Day-to-day loop after that

```sh
cd mobile
npm run sync      # then press Run in Xcode
```

Browser development (`npm run dev`, port 9246 — the desktop keeps 9245, so both
can run at once) renders the whole UI but **cannot reach a daemon**: a browser
cannot open a raw TLS socket with a four-byte length prefix, which is the entire
reason the native plugin exists. `main.ts` catches the failed transport import
and the connect screen says plainly that no transport is available.

---

## 4. The daemon side

### 4.0 The short version

Sections 4.1 to 4.4 explain each step and why it exists. Once you have read them
once, the whole sequence is one command, from either side of the project:

```sh
make mobile-dev            # from the repository root
npm run daemon             # from mobile/, the same script
```

It installs the `lola_insecure` build, verifies with `go tool nm` that the
binary now on `PATH` really carries the listener, generates a bearer key at
`~/.lola/remote-dev-key` (0600) on first use and reuses it afterwards, stops a
daemon that is already running — which is deliberate, because that daemon was
almost certainly started without the key in its environment and would refuse
every connection while looking perfectly healthy — and then runs the tagged
daemon in the foreground with the key exported, so the listener line and its
SPKI pin land in your terminal.

To read the connect details again without restarting anything:

```sh
make mobile-info           # or: npm run daemon:info
```

The script does **not** edit `~/.lola/config.toml`. If `[remote]` is missing or
not enabled it says so and stops, because a helper that quietly rewrites an
operator's configuration to make its own happy path work is how a machine ends
up listening for reasons nobody remembers. Do 4.1 by hand, once.


### 4.1 Configure the listener

Add to `~/.lola/config.toml`:

```toml
[remote]
enabled = true
# Under a lola_insecure build this is FORCED to "localhost" whatever it says,
# and the daemon logs a warning when it overrides you. See 4.5.
bind = "localhost"
port = 7717
```

Defaults if you omit keys: `bind = "localhost"`, `port = 7717`
(`config.DefaultRemoteBind` / `config.DefaultRemotePort`). An absent `[remote]`
table means disabled with zero behaviour change. `bind = "off"` is the
"keep my settings, stop listening" state and is distinct from `enabled = false`.

`localhost` binds **both** loopback families, `127.0.0.1:7717` and `[::1]:7717`.

### 4.2 Build the daemon with the M1 tag

M1's bearer-key authentication lives behind `//go:build lola_insecure`. An
ordinary build has no way to authenticate a phone at all, and says so in the log
rather than leaving a dead port. `make build` does **not** pass the tag, so
build it explicitly:

```sh
cd /path/to/lola
GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go build -tags lola_insecure -o lola .
```

The daemon the TUI restarts and the desktop app spawns is resolved from `PATH`
(normally `$GOPATH/bin/lola`), not from the repo. To make the tagged build the
one that actually runs:

```sh
cd /path/to/lola
GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go install -tags lola_insecure .
```

The daemon does not hot-reload its own binary, so restart it afterwards (the
TUI's `^r`, the desktop app's restart button, or stop and start it by hand).

### 4.3 Generate and pass the bearer key

The key is read once from the environment at startup. It is never in argv, never
in a log line, never in an error, and it must not be committed anywhere.

```sh
export LOLA_REMOTE_INSECURE_KEY="$(openssl rand -hex 24)"
```

At least **16 characters** or the listener refuses to start — there is no key
derivation and no handshake rate limit, so the whole of the secret's strength is
its length.

The daemon inherits this from whoever launches it. If the TUI or the desktop app
manages the daemon lifecycle, export the variable in the shell you launch *them*
from; otherwise start the daemon by hand in a shell that has it:

```sh
export LOLA_REMOTE_INSECURE_KEY="$(openssl rand -hex 24)"
echo "$LOLA_REMOTE_INSECURE_KEY"    # you will type this into the phone
lola run
```

### 4.4 Confirm the listener is actually up

```sh
lola logs -f
```

A working start prints, in this order:

```
remote: WARNING this daemon authenticates phones with a shared LOLA_REMOTE_INSECURE_KEY bearer key and no cryptography
remote: listening on 127.0.0.1:7717
remote: listening on [::1]:7717
remote: phone listener up on 127.0.0.1:7717, [::1]:7717 (SPKI pin r3NLB1U…=)
```

The first time it runs it also prints
`remote: generated a new device identity at ~/.lola/device.key (SPKI pin …)`.

Two failures with distinct lines:

```
remote: not listening: no authorizer: set LOLA_REMOTE_INSECURE_KEY to a random secret of at least 16 characters
remote: [remote] is enabled but this build has no phone listener; M1 authentication is the insecure bearer-key path and is only compiled with -tags lola_insecure
```

The first means the environment variable is missing or too short. The second
means you are running an untagged binary — see 4.2, and check *which* image the
process is really executing:

```sh
lsof -p "$(pgrep -n lola)" | awk '$4=="txt"'
stat -f '%i %N' "$(command -v lola)"
```

Independently of the log, the socket itself:

```sh
lsof -nP -iTCP:7717 -sTCP:LISTEN
```

### The SPKI pin

The pin is the daemon's whole identity — the certificate is self-signed, is in
no trust store, and carries `DNSNames: ["lola"]`, so ordinary chain validation
cannot succeed against any address even in principle. The client replaces trust
evaluation entirely and compares the hash itself.

Take it from the startup log, or derive it:

```sh
openssl x509 -in ~/.lola/device.crt -pubkey -noout |
  openssl pkey -pubin -outform DER |
  openssl dgst -sha256 -binary | openssl base64
```

It is standard base64 with padding, and it is public — it is a hash of a public
key. Type it into the phone's **SPKI pin** field along with the address; only
the access key is a secret.

### 4.5 How the phone actually reaches a loopback-bound daemon

**This is the step a reader who follows the recipe will get stuck on, and the
app will look broken when they do.** A `lola_insecure` daemon binds loopback
only, on purpose: a shared bearer secret must never sit on a network interface.
So a phone on the same WiFi has nothing to connect to, and the failure is an
ordinary connect timeout that reads exactly like a wrong address.

Pick one of these three.

**A. Run in the Simulator first.** The Simulator shares the Mac's loopback, so
`127.0.0.1` port `7717` in the app connects straight to the daemon with no
forwarding at all. This is the cheapest way to prove the pipe works end to end —
handshake, session list, live pane, keystrokes — before adding a network problem
on top. It cannot validate the local-network permission path, which does not
exist there.

**B. Forward to the phone's own loopback over SSH.** An iOS SSH client with port
forwarding (Termius, Blink and others) can open a local forward from the phone's
`127.0.0.1:7717` to the Mac's `127.0.0.1:7717`. The app then connects to
`127.0.0.1` and the bearer key never touches a network listener, which preserves
exactly the property the forced bind exists for. The cost is fragility: the
forwarding app has to stay alive, and iOS suspends background apps.

**C. Put a TCP forwarder in front of it on the Mac.** Reliable, and it spends
the property:

```sh
# on the Mac; 192.168.1.5 is the Mac's own LAN address
socat TCP-LISTEN:7717,bind=192.168.1.5,reuseaddr,fork TCP:127.0.0.1:7717
```

The phone then connects to `192.168.1.5` port `7717`. Binding the LAN address
does not clash with the daemon's loopback listener — they are different
addresses. TLS still terminates at the daemon, so the bearer key is not on the
wire in clear and the SPKI pin still identifies the peer; what you have given up
is that **anything on that network can now reach the listener and try keys
against it**, which is precisely what the build tag was refusing to allow.
Use a long key, use it on a network you trust, and stop the forwarder when you
are done.

There is no fourth option in M1. `[remote].bind = "lan"` is overridden and
logged; it cannot be configured around. M2 replaces the whole path with mutual
TLS and pairing, at which point a real LAN bind becomes the supported answer.

### 4.6 What the phone is allowed to ask for

Nine commands are refused for every remote peer unconditionally, in code:
`stop`, `reload`, `renameProject`, `hookEvent`, `pairBegin`, `pairStatus`,
`pairConfirm`, `devices`, `revokeDevice`. A denied command is answered and then
the daemon **closes the connection**, taking every live pane subscription with
it — so the client mirrors the list and refuses locally first
(`mobile/src/wire/protocol.ts`). `kill` is reachable but the daemon clears
`force` on every remote request, so a dirty worktree can only be removed at the
Mac.

---

## 5. Tests

### What runs today

```sh
# The daemon, including the golden wire vectors shared with the TypeScript side
cd /path/to/lola
make check

# The tag-split half, which make check does not build
GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go test -tags lola_insecure ./internal/remote/...

# The mobile TypeScript suite: the wire package, the shim, and the phone's
# own pure logic (key bytes, viewport maths, diagnosis, endpoint parsing)
cd mobile
npm test

# Type checking across the alias table
cd mobile
npm run check

# The desktop suite, which this project must never disturb
cd desktop/frontend
npx vitest run
```

`mobile/src/wire/testdata/frames.json` is read by **both**
`internal/protocol/goldenvectors_test.go` and `mobile/src/wire/codec.test.ts`.
It is the only mechanical thing holding the Go, TypeScript and Swift views of
the wire format together — there is no code generator between them. Go is the
source of truth: if the Go test fails, fix the vector, never the package.

### Driving the app without a camera

The connect screen's fast path is a QR code the desktop app draws. The iOS
Simulator has no camera and cannot be given one, so on the Simulator that path
is dead: `LolaTransport.scanCapability()` reports
`{ available: false, reason: "no_camera" }` there and the app hides the Scan
button. That would leave an agent or a CI job with no way to put the app in
front of a live daemon at all — which is what the development URL hand-off is
for.

**It is not the pairing mechanism, and it never becomes one.** PLAN.md, under
*Pairing*, settles that the payload a human scans is an opaque `lola1.` token
and deliberately not a URI, because a custom scheme cannot be claimed
exclusively and the system camera would hand the secret to whichever app
registered it. That argument holds here with more force, not less: M1's bearer
key is longer-lived than M2's `qr_secret`. So the QR stays opaque, this is a
separate scheme named `lola-dev`, and everything that turns a URL into a
connection is compiled out unless the plugin package is built in its debug
configuration. A release binary contains none of it — not the parser, not the
observer, not even the name of the environment variable:

```sh
# Release: nothing. Debug: the observer, the parser and the log strings.
nm -a Release-iphonesimulator/App.app/App | grep -c LolaDevLink
strings -a Release-iphonesimulator/App.app/App | grep -c LOLA_DEV_LINK

# The JAVASCRIPT half is not compiled out, and the audit has to cover it. Vite
# has no configuration-conditional strip, so `src/lib/devlink.ts` ships in every
# bundle; it is inert without the native emitter, but a regression that re-armed
# the native side would pass an `nm`/`strings` pass that only looked at the
# binary.
grep -rc "dev-launch" Release-iphonesimulator/App.app/public/assets/*.js
```

Two things stay in a release build ON PURPOSE, and both are named here so
nobody has to rediscover them. `Info.plist` still registers the `lola-dev`
scheme, so another app can `canOpenURL("lola-dev://")` and learn Lola is
installed — gating that needs a per-configuration build setting on the app
target rather than a plist key, which is a change to the generated Xcode project
and has not been made. And the JavaScript listener above still registers; with
no native emitter it never fires.

The app also shows a persistent banner while a connection that arrived this way
is pending or up. A labelled test fixture is a tool; the same thing unlabelled
is a back door.

**The two routes are different doors, and the event says which.** `source` on
the `devLink` event is `dev-url` for a link the OS router delivered and
`dev-launch` for one this process was started with, and the app treats them
differently on purpose:

| `source`     | Arrives from                       | Who can send one              | What the app does           |
| ------------ | ---------------------------------- | ----------------------------- | --------------------------- |
| `dev-url`    | `simctl openurl`, Notes, Messages  | any app on the device         | fills the form, waits for a tap |
| `dev-launch` | `SIMCTL_CHILD_LOLA_DEV_LINK`, argv | only whoever starts the app   | connects                    |

The `connect` action takes `host`/`addr`, `port`, `pin`/`spkiPin`,
`key`/`insecureKey` **or** `keyfile`, and optionally `pane` and `session`. The
pin is required. `pane` names the tmux target to open once connected and
`session` the session it belongs to (defaulting to `pane`); both are destinations
rather than capabilities, since a link that never connects cannot use one.

A routed URL may not dial because anybody can send one — that is PLAN.md's
whole objection to URL-routed pairing, and importing it here after going to the
trouble of avoiding it would be perverse. A launch environment is not that: it
can only be set by the thing that STARTS the process, which on a device means a
debugger and in CI means already owning the machine. Whoever can do that does
not need this feature. `devLinkSource` in `src/lib/devlink.ts` makes the call
and fails closed — anything but the exact string `dev-launch` is treated as a
routed URL.

**THE OS LOGS THE URL, SO THE KEY IN IT IS DISCLOSED.** This is a property of
iOS, not of the plugin — the logging happens before the app runs, and there is
nothing the app can do about it afterwards. All three URL forms are affected:

| Form | What lands in the device's unified log |
| --- | --- |
| `simctl openurl` | `CoreSimulatorBridge: Opening URL (lola-dev://connect?…&key=…)` |
| `SIMCTL_CHILD_LOLA_DEV_LINK` | the whole URL, inside the logged child environment dictionary |
| `--lola-dev-link` | the whole URL, twice, inside the logged argument vector |

That store is persistent (`…/Devices/<udid>/data/var/db/diagnostics`), survives
relaunches, and is what `simctl diagnose` collects. The plugin's own logging is
clean — `LolaLog` prints a host, a port and three booleans and never a
credential — but two things underneath it are not the plugin's to control, and
both have printed the key. The OS logs the launch URL, as above. Capacitor's
bridge logs every resolved payload, which is why the plugin has no method that
returns a secret at all (see `LolaTransportPlugin+Secrets.swift`); that half is
fixed, and it is worth knowing it was ever true, because a build older than that
change printed the key on every launch. **A
key used with `key=` must be treated as disclosed**: rotate it, and
`xcrun simctl erase` the simulator, before sharing a diagnose bundle.

So do not use `key=`. Use `keyfile=`, which carries a NAME rather than a
credential: the bytes are staged into the app's own container, read once and
deleted. `scripts/dev-link.sh` does the whole sequence and never prints the key:

```sh
cd mobile
./scripts/dev-link.sh                    # connect, land on the session list
./scripts/dev-link.sh lola-nori-eng-42   # connect and open that pane
```

`LOLA_SIM` picks a simulator (default: the booted one), `LOLA_HOST` and
`LOLA_PORT` the address. The pane argument is the other half of the same
problem: the terminal screen is reachable only by tapping a session row, the
Simulator has no gesture API, and its device window is absent from the
accessibility tree — so without a deep link the app's central surface cannot be
screenshotted or regression-tested by anything but a human. A pane name grants
nothing; it is only useful to a link that already connected.

**DO NOT RUN `make mobile-info` INSIDE A LOLA SESSION.** lola captures its own
tmux panes and ships the capture onward — `[statusagent]` hands a pane tail to a
bounded `claude -p`, and `[brain]` does the same with a 40-line tail — so a key
printed into an agent's pane is sent to a third-party model. That key types into
live coding agents and cannot be revoked per device. `--info` and `--key` refuse
when `$LOLA_SESSION` is set for exactly this reason; read the values in the
desktop app's **Settings → Remote** instead, which has no pane to capture.

The three raw forms below still exist, and are what `dev-link.sh` builds on. Get
the values from `make mobile-info` **in a terminal outside any lola session**:

```sh
UDID=95232C8C-8CA0-4E2C-B9AC-90141C11C5E2
eval "$(cd /path/to/lola && make mobile-info | awk '{if($1=="key")print "KEY="$2; if($1=="pin")print "PIN="$2}')"
LINK="lola-dev://connect?host=127.0.0.1&port=7717&pin=$PIN&key=$KEY"

# 1. Headless, and what dev-link.sh uses. Cold-launches the app with the link in
#    its environment, and CONNECTS on its own. No prompt, no tap. Spelled with
#    key= here it puts the key in the log; dev-link.sh spells it keyfile=.
SIMCTL_CHILD_LOLA_DEV_LINK="$LINK"   xcrun simctl launch "$UDID" dev.sushi.lola.mobile

# 2. The same thing as an argument, for a launcher that passes argv rather than
#    an environment. Two dashes: a single-dash argument is swallowed by
#    UserDefaults' own parsing and would silently become a preference.
xcrun simctl launch "$UDID" dev.sushi.lola.mobile --lola-dev-link "$LINK"

# 3. The URL itself. Needs TWO human taps: one on the system's "Open in Lola?"
#    confirmation, and one on Connect once the form is filled. See below.
xcrun simctl openurl "$UDID" "$LINK"
```

**`simctl openurl` is not scriptable on iOS 26.** The system interposes an
`Open in "Lola"?` confirmation on any custom-scheme open whose source it does
not recognise, and `simctl` is exactly such a source. It appears whether or not
the app is already in the foreground, it is drawn inside the simulated device
rather than in a Mac window, and `simctl` has no gesture API — so there is
nothing to click and nothing to automate. That is why forms 1 and 2 exist; they
carry the identical string through the identical parser, and differ only in the
`source` the event carries — which is the difference between filling the form and
connecting. Use form 3 when a person is watching, or on a real device where the
link arrives from Notes or Messages.

Both were verified against a live daemon on 95232C8C: form 1 from a COLD-BOOTED
simulator reaches the session list unattended, and the daemon logs
`accepting 127.0.0.1:<port> over the insecure M1 bearer-key path`. The plugin's
own line names the route it took:

```sh
xcrun simctl spawn "$UDID" log show --last 2m --info \
  --predicate 'subsystem == "dev.sushi.lola.mobile"' --style compact
# ... dev link accepted for 127.0.0.1:7717 via=dev-launch pin=true key=true pane=true
```

`--info` is not optional there: `LolaLog` writes at the info level, which
`log show` omits by default — a missing line reads as "the link never arrived"
when it merely was not printed.

Confirm it landed without printing the key anywhere:

```sh
xcrun simctl spawn "$UDID" log stream --level debug \
  --predicate 'subsystem == "dev.sushi.lola.mobile"'
# dev link accepted for 127.0.0.1:7717 via=dev-launch pin=true key=true pane=false
# dev link posted to the bridge
```

The two log lines are deliberately the whole diagnostic. The host and port are
addresses rather than secrets, and the booleans distinguish the failures that
otherwise look identical from outside — a link that parsed but reached no
plugin, and one that reached the bridge and is waiting for the app to register a
listener. **The plugin never logs the pin or the key.** That is a statement
about the plugin only; see the table above for what the OS logs before the
plugin is reached.

The parser FAILS CLOSED, which is worth knowing before blaming the app:

| URL | Result |
| --- | --- |
| a pin in base64url, padded or not | accepted, normalized to standard base64 |
| a malformed or wrong-length pin | the whole link is rejected |
| no pin | the whole link is rejected |
| `keyfile` naming anything but a bare filename | the key is dropped; the form asks for one |
| `port` outside 1..65535 | the whole link is rejected |
| `pane` with anything but `[A-Za-z0-9._-]` | the pane is dropped; the app lands on the list |
| any action but `connect` | ignored |

An unusable pin and an ABSENT one now fail in the same place, which they did
not: a malformed pin has always rejected the whole link, while a missing one was
passed through to be caught by the connect form's validator. Both spellings of
the same mistake belong at the same fence. The reason is that an unpinned
connection accepts whatever certificate answers — the one genuinely dangerous
state this transport has — so reaching it through a typo would make the pin
decorative.

### What cannot run yet, and why

- **`npm test` before `npm install`.** `mobile/` has no `node_modules` in a
  fresh checkout, so the suite cannot resolve `svelte` or `vitest`. Step 3.1
  first.
- **The Swift tests.** `mobile/plugins/lola-transport/ios/Tests/` covers the
  frame codec, the SPKI DER walk, the handshake classification and Go-compatible
  JSON escaping, and **has never been executed** — it was written without an
  iOS SDK available. Expect to fix compile errors before you see a result:

  ```sh
  cd mobile/plugins/lola-transport
  xcodebuild test -scheme LolaTransportPlugin \
    -destination 'platform=iOS Simulator,name=iPhone 16'
  ```

  The algorithms were verified out of band instead: the DER walk and SPKI hash
  were reimplemented in Python against both fixture certificates and reproduce
  OpenSSL's pins exactly, and the handshake frame's bytes were compared against
  the golden vector.
- **Everything in PLAN.md's M1 *Definition of done*.** Every item there is a
  measurement against a physical iPhone with a live agent — pane legibility,
  the key set working against a real Claude Code session, three concurrent
  clients, a mid-stream desktop resize, idle bandwidth under 1 KB/minute, and
  the denied-local-network first launch. None of it can be self-reported by
  anything in this repository.
- **The socket half of the plugin.** TLS against a live daemon, the pin check,
  `notifyListeners` throughput on a busy pane, backgrounding and resume, and
  every deadline all need a device.

---

## 6. Troubleshooting

### The local-network permission prompt

It appears on the first connection to a **private-range** address, and only
then. Loopback never prompts, so options A and B in section 4.5 never show it.

If it is denied, **iOS never asks again**, and a denied permission is reported to
the app as an ordinary unreachable host — there is no API that can tell them
apart. The connect screen names this possibility whenever the address is private
and the connection timed out. To fix: **Settings → Privacy & Security → Local
Network → Lola**, then relaunch the app.

### The pin does not match

The app reports a pin mismatch distinctly from every other TLS failure, because
it is the one with a specific meaning: either the pin was copied wrong, or the
peer is not the daemon it claims to be.

- Re-read it from `remote: phone listener up on … (SPKI pin …)`, or with the
  `openssl` pipeline in section 4.4. It ends in `=` — include the padding.
- Deleting or regenerating `~/.lola/device.key` changes the pin. Every paired
  phone must be given the new one.
- A forwarder in front of the daemon (option C) does not change the pin: TLS is
  end to end and socat only relays bytes.

### The key is wrong, or there is no key

A refused key is reported by the daemon as `denied` and the connection is closed
immediately. The app distinguishes this from "unreachable" because the daemon
*answered* — if you see the refusal, the address and the pin are both right and
only the key is wrong.

A key **shorter than 16 characters** is a different failure that looks the same
from the phone: the daemon's listener never started at all, so nothing is
listening and the app reports a connect timeout. Check `lola logs -f` for
`remote: not listening: no authorizer: …` before hunting for a network problem.

Remember that the key is not persisted in M1 (section 1) — it has to be retyped
on every launch, and an empty field is a common cause of a first-tap failure.

### The phone is on a different network

Check both ends rather than one:

```sh
ipconfig getifaddr en0                    # on the Mac
```

and on the phone, Settings → Wi-Fi → the network's info panel. A VPN profile on
the phone (or Private Relay) will route the connection off the LAN even when the
SSID matches. Turn it off for the test.

### Guest WiFi and AP isolation

Many guest and hotel networks enable client isolation, which blocks phone → Mac
traffic *even though both devices are on the same SSID and both have addresses
in the same subnet*. Nothing on either machine can detect it, and it looks
exactly like a firewall or a wrong address.

Two ways to confirm it in a minute: start the phone's personal hotspot, join the
Mac to it, and try again with the Mac's new address; or run the Simulator
(section 4.5, option A), which does not touch the network at all. If the
Simulator works and the phone does not, the network is the problem.

Also check the Mac's own firewall: **System Settings → Network → Firewall**. If
it is on, the forwarder process (or `lola`) needs to be allowed to accept
incoming connections.

### Every plugin call rejects with "not implemented"

The bridge did not resolve the plugin. Either `plugins/lola-transport/dist/` was
missing when `cap sync` ran — run `npm run sync`, which builds it first — or the
`jsName` in `LolaTransportPlugin.swift` and the name passed to `registerPlugin`
in `src/index.ts` have drifted apart. Both must read `LolaTransport`.

### The app launches to a blank white screen

`cap sync` was not re-run after `vite build`, or `build.outDir` and `webDir`
disagree. Both are `dist`. Run `npm run sync` and Run again. Use Safari's Web
Inspector (Develop → your phone → Lola) to see the actual error; it is enabled
in `capacitor.config.ts`.

### The app renders but is completely unstyled

Tailwind v4 scans source text, and the `@import "tailwindcss"` that generates
the utilities lives in the desktop's stylesheet. `mobile/src/app.css` declares
`@source "./"` and `@source "../../desktop/frontend/src/lib"` for exactly this
reason. If you add a directory of components outside both, add an `@source` for
it — there is no error and no warning, the classes simply compile to nothing.

### The connection drops the moment you do something

An `unknown_cmd` refusal is fatal on the daemon side: it writes one error frame
and closes, taking every pane subscription with it. If a connection dies on a
specific action, the client sent a command the daemon denies. The client's own
mirror of that list is `DENIED_COMMANDS` in `mobile/src/wire/protocol.ts`; if it
has drifted from `internal/remote/policy.go`, that is the bug.

### The terminal scrolls the wrong way

**Positive scrolls back into history**, which is the daemon's convention and the
opposite of a browser wheel delta (`internal/tmux/client.go`'s `ScrollPane`
opens with `up := lines > 0`). `mobile/src/lib/viewport.ts`'s `SCROLL_BACK` is
the one place the app decides this. The client must never synthesize wheel bytes
itself: the daemon chooses between the program's own transcript and tmux copy
mode, and an agent on the alternate screen has no tmux scrollback at all.
