import Capacitor
import Foundation
import LolaTransportCore

/// The Capacitor bridge. It owns one `LolaConnection` and translates between
/// JavaScript's vocabulary and Swift's; it contains no protocol knowledge of
/// its own, which is what keeps the interesting parts testable without a
/// device.
///
/// `identifier` and `jsName` are the two strings that make the bridge resolve.
/// `jsName` must match the name passed to `registerPlugin` in `src/index.ts`,
/// and `identifier` must match the `@objc` class name; a mismatch in either
/// shows up as every call rejecting with "not implemented", which reads like a
/// missing method rather than a naming error.
@objc(LolaTransportPlugin)
public class LolaTransportPlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "LolaTransportPlugin"
    public let jsName = "LolaTransport"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "connect", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "disconnect", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "send", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "status", returnType: CAPPluginReturnPromise),
        // The hand-off half. `scanQR` and `scanCapability` live in
        // LolaTransportPlugin+Scanner.swift; a method is bridged by being named
        // here AND being `@objc` on this class, and an extension satisfies the
        // second half, so a new one only ever adds a line to this array.
        CAPPluginMethod(name: "scanQR", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "scanCapability", returnType: CAPPluginReturnPromise),
        // The secret store. Bodies live in LolaTransportPlugin+Secrets.swift,
        // the SecItem calls themselves in LolaTransportCore.LolaKeychain.
        // `secretstore.ts` PROBES for these three names before it uses them and
        // degrades to an in-memory map when they are absent, so removing a line
        // here does not break the app - it silently makes the bearer key
        // survive only as long as the process, which is the bug this shipped to
        // fix. Do not rename them without changing that module.
        CAPPluginMethod(name: "secretSet", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "secretGet", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "secretDelete", returnType: CAPPluginReturnPromise),
    ]

    /// Capacitor calls this once, when the bridge finishes registering the
    /// plugin. It is the earliest point at which `notifyListeners` has anywhere
    /// to deliver to, and it is before the WebView has run any JavaScript,
    /// which is exactly the window a cold-launch URL arrives in.
    override public func load() {
        super.load()
        // A no-op in a release build. See LolaDevLink.swift.
        startDevLinkObservation()

        // Force the connection into existence NOW rather than on the first
        // `connect` call. It is lazy because building a socket eagerly would be
        // wasteful, but its INIT is what registers the app-lifecycle observers,
        // and those have to be in place before the first background/foreground
        // pair rather than before the first connection. A phone that is
        // backgrounded while the app sits on the connect form would otherwise
        // register them on the way back in, i.e. one notification too late, and
        // the very first `appState` event would be missed.
        //
        // The cost is a DispatchQueue and two NotificationCenter tokens. No
        // socket is opened here.
        _ = connection
    }

    /// Defaults, each mirroring a named constant on the daemon side rather than
    /// being chosen here. Where the two ends disagree about a bound, the
    /// disagreement is only visible under load, which is the worst time to find
    /// it.
    private enum Defaults {
        /// `config.DefaultRemotePort`.
        static let port = 7717
        /// `remote.handshakeTimeout`.
        static let connectTimeoutMs = 10_000
        static let handshakeTimeoutMs = 10_000
        /// `remote.writeTimeout`.
        static let writeTimeoutMs = 15_000
        /// `panebus.DefaultFlushInterval`.
        static let flushIntervalMs = 16
        static let maxBatchBytes = 256 * 1024
    }

    private lazy var connection: LolaConnection = {
        let c = LolaConnection()
        c.onFrames = { [weak self] frames, epoch in
            self?.notifyListeners("frames", data: ["epoch": epoch, "frames": frames])
        }
        c.onState = { [weak self] event in
            self?.notifyListeners("state", data: event.payload())
        }
        // Relayed verbatim and acted on nowhere in Swift. See the note on
        // `LolaConnection.onAppState`: the plugin reports, the app decides.
        c.onAppState = { [weak self] active in
            // A transition and nothing else, which is the whole of LolaLog's
            // permitted vocabulary. It is here because the failure this feature
            // fixes is invisible from the outside — a phone that came back from
            // standby still disconnected looks identical whether the signal was
            // never emitted, never delivered, or delivered and ignored — and
            // these two lines are what tell those apart without a debugger.
            LolaLog.info("app \(active ? "entered the foreground" : "entered the background")")
            self?.notifyListeners("appState", data: ["active": active])
        }
        return c
    }()

    @objc func connect(_ call: CAPPluginCall) {
        guard let host = call.getString("host"), !host.isEmpty else {
            call.reject("host is required", LolaFailureCode.network.rawValue)
            return
        }
        let portValue = call.getInt("port") ?? Defaults.port
        guard portValue > 0, portValue <= 65535 else {
            call.reject("port must be between 1 and 65535", LolaFailureCode.network.rawValue)
            return
        }

        // The key is read out of the call and handed straight to the
        // connection. It is never stored on the plugin, never written to
        // UserDefaults, and never logged. Where it comes from before this point
        // is the app's business; nothing about it is compiled in.
        let config = LolaConnection.Config(
            host: host,
            port: UInt16(portValue),
            spkiPin: nonEmpty(call.getString("spkiPin")),
            allowUnpinned: call.getBool("allowUnpinned") ?? false,
            insecureKey: nonEmpty(call.getString("insecureKey")),
            connectTimeoutMs: call.getInt("connectTimeoutMs") ?? Defaults.connectTimeoutMs,
            handshakeTimeoutMs: call.getInt("handshakeTimeoutMs") ?? Defaults.handshakeTimeoutMs,
            writeTimeoutMs: call.getInt("writeTimeoutMs") ?? Defaults.writeTimeoutMs,
            flushIntervalMs: call.getInt("flushIntervalMs") ?? Defaults.flushIntervalMs,
            maxBatchBytes: call.getInt("maxBatchBytes") ?? Defaults.maxBatchBytes
        )

        connection.connect(config) { result in
            switch result {
            case .success(let outcome):
                call.resolve([
                    "epoch": outcome.epoch,
                    "host": outcome.host,
                    "port": Int(outcome.port),
                    "spkiPin": outcome.spkiPin,
                    "pinned": outcome.pinned,
                ])
            case .failure(let failure):
                // The failure code travels in the rejection's `code` field, so
                // the app branches on a value rather than on the text of a
                // message.
                //
                // `daemonCode` TRAVELS STRUCTURED, in the rejection's data
                // dictionary, and that is load-bearing rather than tidy.
                // Capacitor merges that dictionary into the JS error object, so
                // the app reads a field. It used to be folded into the message
                // on the theory that the `state` event carried the structured
                // copy anyway — but `terminate` settles this completion BEFORE
                // it emits that event, and the two cross the bridge as separate
                // evaluations, so at the instant the app's catch block runs the
                // event provably has not arrived. The app therefore saw a
                // transport code, no refusal, and reported a rejected bearer
                // key as "not on this network": the one failure with a
                // one-field fix, shown as the one with no fix.
                //
                // The message keeps its human suffix; nothing parses it.
                var message = failure.reason
                var data: [String: Any] = [:]
                if let daemonCode = failure.daemonCode {
                    message += " [daemon: \(daemonCode)]"
                    data["daemonCode"] = daemonCode
                }
                if let minV = failure.minV, let maxV = failure.maxV {
                    message += " [daemon speaks envelope v\(minV)..v\(maxV)]"
                    data["minV"] = minV
                    data["maxV"] = maxV
                }
                call.reject(message, failure.code.rawValue, nil, data.isEmpty ? nil : data)
            }
        }
    }

    @objc func disconnect(_ call: CAPPluginCall) {
        connection.disconnect(reason: call.getString("reason"))
        call.resolve()
    }

    @objc func send(_ call: CAPPluginCall) {
        guard let raw = call.getArray("frames") else {
            call.reject("frames is required", LolaFailureCode.internalError.rawValue)
            return
        }
        var bodies: [String] = []
        bodies.reserveCapacity(raw.count)
        for element in raw {
            guard let body = element as? String else {
                call.reject(
                    "frames must be an array of JSON strings",
                    LolaFailureCode.internalError.rawValue)
                return
            }
            bodies.append(body)
        }

        connection.send(bodies: bodies) { result in
            switch result {
            case .success:
                call.resolve()
            case .failure(let failure):
                call.reject(failure.reason, failure.code.rawValue)
            }
        }
    }

    @objc func status(_ call: CAPPluginCall) {
        connection.snapshot { snapshot in
            var payload: [String: Any] = [
                "epoch": snapshot.epoch,
                "phase": snapshot.phase.rawValue,
                "framesIn": snapshot.framesIn,
                "framesOut": snapshot.framesOut,
                "bytesIn": snapshot.bytesIn,
                "bytesOut": snapshot.bytesOut,
            ]
            if let host = snapshot.host { payload["host"] = host }
            if let port = snapshot.port { payload["port"] = Int(port) }
            if let pinned = snapshot.pinned { payload["pinned"] = pinned }
            call.resolve(payload)
        }
    }

    /// An empty string from JavaScript means "not supplied". Treating `""` as a
    /// value would turn an unfilled text field into a zero-length bearer key or
    /// an empty pin, and the empty pin in particular would look like an
    /// intentional unpinned connection.
    private func nonEmpty(_ value: String?) -> String? {
        guard let value, !value.isEmpty else { return nil }
        return value
    }
}
