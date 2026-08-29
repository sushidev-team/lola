# LolaTransport

The native half of the mobile client's connection to a lola daemon: a Capacitor
plugin (Swift, iOS) that owns the TLS socket, the wire framing, the certificate
pin and the M1 bearer handshake, and hands decoded frames to the WebView.

It exists because the daemon's remote listener is **raw TLS 1.3 over TCP with a
four-byte big-endian length prefix** — `tls.NewListener` in
`internal/remote/server.go` over `internal/protocol/framecodec.go` — and not a
WebSocket. There is no HTTP upgrade for a browser `WebSocket` to perform, `fetch`
cannot hold a bidirectional byte stream, and no browser exposes a per-connection
certificate-pinning hook for a self-signed peer. So the socket lives in Swift.

## What it does and does not own

It owns the connection, the framing in both directions (including reassembly of
a frame split across TCP segments and refusal of an oversized one), the SPKI
pin, the bearer handshake, and the coalescing of inbound frames onto a short
tick.

It does **not** own the envelope. It never parses a frame body beyond the
handshake and has no opinion about `sub`, `pty`, `resync` or a request's `id`.
Those live in `mobile/src/wire`, shared with the vitest suite and with any future
transport; a second copy in Swift would be a second copy of the protocol to keep
honest. It also does not own reconnection policy: it reports what happened and
stops, because only the app knows whether the user is looking at a live terminal
or at a cached list.

## Layout

```
mobile/plugins/lola-transport/
  package.json                  npm package; the `capacitor` key is what `cap sync` looks for
  Package.swift                 SPM integration (the Capacitor 8 default)
  LolaTransport.podspec         CocoaPods fallback, for `cap add ios --packagemanager CocoaPods`
  tsconfig.json
  src/
    definitions.ts              the plugin's TypeScript API
    index.ts                    registerPlugin("LolaTransport", ...)
    web.ts                      a fallback that refuses and explains why
  ios/Sources/LolaTransportCore/          no Capacitor, no sockets, all testable
    FrameCodec.swift            the length-prefixed encoder and the streaming decoder
    SPKIPin.swift               DER walk to SubjectPublicKeyInfo, SHA-256, base64
    HelloHandshake.swift        the M1 bearer frame and its reply classification
    JSONText.swift              Go-compatible JSON string escaping
    ConnectionState.swift       the phase/failure vocabulary and the bridge payload
  ios/Sources/LolaTransportPlugin/        the socket, the camera and the bridge
    LolaConnection.swift        NWConnection, TLS, handshake, read loop, batching, teardown
    LolaTransportPlugin.swift   CAPPlugin: option parsing, resolve/reject, notifyListeners
    LolaLog.swift               logging, and the rule that nothing on the wire is logged
    LolaQRScanner.swift         AVFoundation viewfinder; returns the decoded string, nothing more
    LolaTransportPlugin+Scanner.swift   scanQR / scanCapability, bridge translation only
    LolaDevLink.swift           the debug-only lola-dev:// parser; read its header first
    LolaTransportPlugin+DevLink.swift   observes the URL, emits the retained `devLink` event
  ios/Tests/LolaTransportCoreTests/
    GoldenVectors.swift         loads mobile/src/wire/testdata/frames.json by relative path
    FrameCodecTests.swift       the twenty golden vectors, encode and decode
    FrameDecoderTests.swift     partial reads, split prefixes, batching, oversized refusal
    SPKIPinTests.swift          real certificates, pins produced independently by OpenSSL
    HelloHandshakeTests.swift   the handshake frame and every reply shape
    JSONTextTests.swift         Go's escaping rules, including the surprising ones
    ConnectionStateTests.swift  the bridge payload's exact keys
```

## Building and running

Everything below happens on a Mac with Xcode 26 or newer (Capacitor 8's
requirement) and Node 22 or newer. None of it has been run in this repository;
these are the commands, in order.

**1. Build the plugin's JavaScript.** `cap sync` copies whatever is in `dist/`,
so this has to happen before the app is synced, every time the TypeScript
changes.

```sh
cd mobile/plugins/lola-transport
npm install
npm run build
```

**2. Add the plugin to the app.** From the mobile app directory (the one with
`capacitor.config.ts`):

```sh
cd mobile
npm install ./plugins/lola-transport
```

That writes `"lola-transport": "file:plugins/lola-transport"` into the app's
`package.json`. `cap sync` finds the plugin by scanning dependencies for a
package with a top-level `capacitor` key; there is no registry step and no
manual Xcode work.

**3. Build the web app and sync.**

```sh
npm run build          # Vite; build.outDir must equal capacitor.config.ts's webDir
npx cap sync ios
```

**4. Run.**

```sh
npx cap open ios       # opens ios/App/App.xcodeproj (SPM: there is no .xcworkspace)
```

Then Run from Xcode onto a **physical device**. A simulator is not sufficient
for this plugin: iOS local-network privacy is not implemented in the Simulator,
so the permission prompt, the denial path and therefore the whole first-connect
experience can pass on a simulator and fail on a phone.

**5. The app's `Info.plist` needs one key**, and it is the app's, not the
plugin's:

```xml
<key>NSLocalNetworkUsageDescription</key>
<string>Lola connects to the lola daemon on your Mac over your local network.</string>
```

The prompt fires on the first unicast to a private-range address. It does not
fire for loopback, so a connection over an SSH forward terminating on the
phone's own loopback never prompts — declare the key anyway, because the LAN
case does. `NSBonjourServices` is deliberately **not** needed: nothing here
browses, and adding entries is over-declaration that draws App Review questions.
No ATS exception is needed either: ATS does not apply to Network.framework
connections, so the self-signed endpoint needs no `NSAppTransportSecurity`
dictionary, and `NSAllowsArbitraryLoads` must never be added.

## Testing

The pure half is covered; the socket half is not, and cannot be without a
device.

```sh
cd mobile/plugins/lola-transport
xcodebuild test \
  -scheme LolaTransportPlugin \
  -destination 'platform=iOS Simulator,name=iPhone 16'
```

The tests read `mobile/src/wire/testdata/frames.json` by relative path from the
test source, so they must run from a checkout. `LOLA_FRAME_VECTORS` overrides
the path.

**These tests have never been executed.** They were written without an iOS SDK
or an Xcode toolchain available, so the first person to run them should expect
to fix compile errors before they see a red or green result. The algorithms they
cover were verified out of band instead: the DER walk and the SPKI hash were
reimplemented line for line in Python and checked against both fixture
certificates, reproducing OpenSSL's pins exactly, and the handshake frame's bytes
were compared against the golden vector.

What is **not** covered, and needs a device: the TLS handshake and the pin check
against a live daemon, `notifyListeners` throughput under a busy pane,
backgrounding and resume, the local-network permission prompt and its denial
path, and every deadline.

## Security notes

**Nothing secret is compiled in.** The host, the port, the pin and the bearer
key all arrive as `connect()` arguments. The key is handed straight to the
connection, is never stored on the plugin, never written to `UserDefaults`, and
never logged.

**Nothing on the wire is logged.** Not a frame body, not a byte of pane output,
not the key. The daemon holds the same line on its side — its audit line carries
the device, the command and the outcome and explicitly never the payload,
because `answer` carries prose a human typed at a phone. A client that logged
the same frames would undo that at the other end, and an iOS log is readable by
anyone with the device and a cable.

**The scanner reads, it does not interpret.** `scanQR` returns the decoded
string exactly as the symbol carried it; the plugin has no opinion about the
enrolment format, and the app is expected to treat the result as untrusted —
anyone can print a QR code. The decoded value is never logged, for the same
reason the key is not: its whole purpose is to carry a secret. Ask
`scanCapability()` before drawing a Scan button, because the Simulator has no
camera and a control that always fails on tap reads as a broken feature.

**The `lola-dev://` hand-off is compiled out of a release build.** It exists so
that a Simulator, which cannot scan, can still be pointed at a live daemon by a
script. `Package.swift` defines `LOLA_DEV_LINK` for the debug configuration and
nowhere else, and the whole of `LolaDevLink.swift` sits behind it — down to the
string constants, so `strings` over a shipped binary finds nothing to
investigate. It is deliberately not `lola://` and deliberately not the pairing
mechanism; the header of that file carries the argument in full, and
`mobile/README.md` has the commands. The delivered event is stamped
`source: "dev-url"` so the app can keep a banner up for as long as such a
connection is alive.

**The pin is the daemon's whole identity.** System trust evaluation is replaced
entirely, because it cannot succeed: the certificate is self-signed, is in no
trust store, and carries `DNSNames: ["lola"]` with loopback IP SANs. Omitting
`spkiPin` is possible in M1 — the pin has no distribution channel until M2's
pairing QR — but it requires `allowUnpinned: true` explicitly, logs a warning
naming M2, and reports the observed pin so a human can write it down. An omitted
option is what a typo looks like, and a check that vanishes on a typo is not a
check.

The daemon prints the pin at startup:

```
remote: listening on 127.0.0.1:7717
remote: generated a new device identity at ~/.lola/device.key (SPKI pin r3NLB1U...=)
```

It is also derivable from the certificate on disk:

```sh
openssl x509 -in ~/.lola/device.crt -pubkey -noout |
  openssl pkey -pubin -outform DER |
  openssl dgst -sha256 -binary | openssl base64
```

## The bridge, and why frames are batched

Capacitor's `notifyListeners` does not pass arguments to JavaScript. It
serializes the payload, interpolates it into a string of JavaScript **source**,
hops to the main thread, and has WebKit parse it. One bridge crossing per socket
read on a busy pane would put that cost in the path of every burst of agent
output.

So inbound frames are coalesced and delivered as an array of JSON strings. The
window is 16 ms, matching the daemon's own `panebus.DefaultFlushInterval` rather
than inventing a second cadence: the daemon has already batched on that tick, so
a matching window adds at most one more frame time. A batch also flushes early
once it reaches `maxBatchBytes`, which bounds the size of one JavaScript source
string.

The opposite direction is cheap — `postMessage` is a structured clone — so
keystrokes go one per call, and `send()` accepts an array anyway so a caller
that has several frames pays one socket write.
