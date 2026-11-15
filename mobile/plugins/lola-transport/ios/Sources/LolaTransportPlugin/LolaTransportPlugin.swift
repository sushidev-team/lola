import Capacitor
import Foundation
import LolaTransportCore
import UIKit

/// The Capacitor bridge. It translates between JavaScript's vocabulary and
/// Swift's and holds no protocol knowledge of its own: racing, framing,
/// pairing and the credential refresh live in `Connector`, `Session`,
/// `Pairing` and Core.
///
/// `identifier` and `jsName` are the two strings that make the bridge resolve.
/// `jsName` must match `registerPlugin` in `src/index.ts` and `identifier` the
/// `@objc` class name; a mismatch shows up as every call rejecting with "not
/// implemented", which reads like a missing method rather than a naming error.
///
/// No method, listener payload or log line carries a secret: Capacitor logs
/// every resolved payload in Debug builds, so the pairing secret, the relay
/// token and bearer, and the private key never leave native code.
@objc(LolaTransportPlugin)
public class LolaTransportPlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "LolaTransportPlugin"
    public let jsName = "LolaTransport"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "scanCapability", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "scanPairing", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "pairBegin", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "pairCancel", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "daemons", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "rename", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "forget", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "connect", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "disconnect", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "send", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "adopt", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "status", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "pickImage", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "readClipboardImage", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "haptic", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "openURL", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "setIdleTimerDisabled", returnType: CAPPluginReturnPromise),
    ]

    /// False when the bridge guard could not be installed: every method then
    /// rejects, so a broken guard fails closed for the plugin holding the session.
    private var guardInstalled = false

    /// One serial queue for the monitor, the connector and pairing.
    let queue = DispatchQueue(label: "dev.sushi.lola.transport")
    lazy var monitor = PathMonitor(queue: queue)
    lazy var pairing: Pairing = {
        let pairing = Pairing(queue: queue, monitor: monitor)
        pairing.onNotice = { [weak self] notice in self?.notifyListeners("pairing", data: notice.payload()) }
        return pairing
    }()
    lazy var connector: Connector = {
        let connector = Connector(queue: queue, monitor: monitor)
        connector.onFrames = { [weak self] frames, epoch in
            self?.notifyListeners("frames", data: ["epoch": epoch, "frames": frames])
        }
        connector.onState = { [weak self] state in self?.notifyListeners("state", data: state.payload()) }
        connector.onDevice = { [weak self] epoch, device in
            self?.notifyListeners("device", data: ["epoch": epoch, "device": device.payload()])
        }
        connector.onAppState = { [weak self] active in
            LolaLog.info("app \(active ? "entered the foreground" : "entered the background")")
            self?.notifyListeners("appState", data: ["active": active])
        }
        return connector
    }()

    /// Runs when the bridge registers the plugin, before the web view loads any
    /// page script: the guard goes in first.
    override public func load() {
        guardInstalled = installBridgeGuard(bridge: bridge)
        super.load()
        // A no-op in a release build. See LolaDevLink.swift.
        startDevLinkObservation()
        // Created now, not on the first `connect`: building the connector is
        // what registers the app-lifecycle observers, and they must be in
        // place before the first background/foreground pair.
        _ = connector
    }

    /// Rejects when the guard is missing. Every method begins with this.
    func admit(_ call: CAPPluginCall) -> Bool {
        guard guardInstalled else {
            call.reject("bridge guard missing", FailureCode.internalError.rawValue)
            return false
        }
        return true
    }

    // MARK: Paired daemons

    @objc func daemons(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        queue.async {
            let rows: [[String: Any]] = PairingStore.records().map { record in
                var row: [String: Any] = [
                    "daemonId": record.daemonId,
                    "name": record.name,
                    "pairedAt": Int(record.pairedAt.timeIntervalSince1970 * 1000),
                    "relay": record.relay.rawValue,
                    "secureEnclave": record.secureEnclave,
                ]
                if let last = PairingStore.lastPath(daemonId: record.daemonId) { row["lastPath"] = last.rawValue }
                return row
            }
            call.resolve(["daemons": rows])
        }
    }

    @objc func rename(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let daemonId = call.getString("daemonId"), let name = call.getString("name"),
            !name.trimmingCharacters(in: .whitespaces).isEmpty
        else { return call.reject("daemonId and name are required", FailureCode.internalError.rawValue) }
        queue.async {
            guard var record = (try? PairingRecord.load(daemonId: daemonId)) ?? nil else { return call.resolve() }
            record.name = name
            do {
                try record.save()
                call.resolve()
            } catch {
                call.reject("the name could not be saved", FailureCode.internalError.rawValue)
            }
        }
    }

    @objc func forget(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let daemonId = call.getString("daemonId") else {
            return call.reject("daemonId is required", FailureCode.internalError.rawValue)
        }
        connector.forget(daemonId: daemonId) {
            PairingStore.forget(daemonId: daemonId)
            call.resolve()
        }
    }

    // MARK: Pairing

    @objc func pairBegin(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let ref = call.getString("pairRef"), let name = call.getString("deviceName") else {
            return call.reject("pairRef and deviceName are required", FailureCode.internalError.rawValue)
        }
        queue.async {
            self.pairing.begin(pairRef: ref, deviceName: name) { result in
                switch result {
                case .success(let begun): call.resolve(["code": begun.code, "path": begun.path.payload()])
                case .failure(let failure): call.reject(failure.message, failure.code)
                }
            }
        }
    }

    @objc func pairCancel(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let ref = call.getString("pairRef") else {
            return call.reject("pairRef is required", FailureCode.internalError.rawValue)
        }
        queue.async {
            self.pairing.cancel(pairRef: ref)
            call.resolve()
        }
    }

    // MARK: Connection

    @objc func connect(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let daemonId = call.getString("daemonId") else {
            return call.reject("daemonId is required", FailureCode.internalError.rawValue)
        }
        connector.connect(daemonId: daemonId) { result in
            switch result {
            case .success(let connected):
                call.resolve([
                    "epoch": connected.epoch,
                    "path": connected.path.payload(),
                    "raceMs": connected.raceMs,
                    "device": connected.device.payload(),
                ])
            case .failure(let failure):
                // The daemon's code travels structured, in the rejection's data:
                // Capacitor merges it into the JS error, and the `state` event
                // that also carries it crosses the bridge separately, so the
                // catch block may run before it arrives.
                var data: [String: Any] = [:]
                if let daemonCode = failure.daemonCode { data["daemonCode"] = daemonCode }
                if let minV = failure.minV { data["minV"] = minV }
                if let maxV = failure.maxV { data["maxV"] = maxV }
                call.reject(failure.reason, failure.code.rawValue, nil, data.isEmpty ? nil : data)
            }
        }
    }

    @objc func disconnect(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        connector.disconnect(reason: call.getString("reason"))
        call.resolve()
    }

    @objc func send(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let epoch = call.getInt("epoch"), let raw = call.getArray("frames") else {
            return call.reject("epoch and frames are required", FailureCode.internalError.rawValue)
        }
        let bodies = raw.compactMap { $0 as? String }
        guard bodies.count == raw.count else {
            return call.reject("frames must be an array of JSON strings", FailureCode.internalError.rawValue)
        }
        // JS must not read or disturb the relay credential: frames that touch
        // native state never leave.
        switch InboundFilter.checkOutbound(bodies.map { Data($0.utf8) }) {
        case .allowed: break
        case .reserved: return call.reject("reserved frame", FailureCode.internalError.rawValue)
        case .malformed: return call.reject("malformed frame", FailureCode.internalError.rawValue)
        }
        connector.send(epoch: epoch, bodies: bodies) { result in
            switch result {
            case .success: call.resolve()
            case .failure(let failure): call.reject(failure.reason, failure.code.rawValue)
            }
        }
    }

    @objc func adopt(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let epoch = call.getInt("epoch") else {
            return call.reject("epoch is required", FailureCode.internalError.rawValue)
        }
        connector.adopt(epoch: epoch) { adopted in
            if adopted {
                call.resolve()
            } else {
                call.reject("that migration is gone", FailureCode.clientClosed.rawValue)
            }
        }
    }

    @objc func status(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        connector.status { status in
            var payload: [String: Any] = [
                "epoch": status.epoch,
                "phase": status.phase.rawValue,
                "relay": status.relay.rawValue,
                "framesIn": status.framesIn,
                "framesOut": status.framesOut,
                "bytesIn": status.bytesIn,
                "bytesOut": status.bytesOut,
                "launchedAt": status.launchedAt,
            ]
            if let path = status.path { payload["path"] = path.payload() }
            call.resolve(payload)
        }
    }

    // MARK: Screen

    /// Keeps the screen awake while a pairing waits for the Mac: auto-lock
    /// would background the app and fail the pairing as `phone_left`.
    @objc func setIdleTimerDisabled(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        let disabled = call.getBool("disabled") ?? false
        DispatchQueue.main.async {
            UIApplication.shared.isIdleTimerDisabled = disabled
            call.resolve()
        }
    }
}

extension PairingNotice {
    func payload() -> [String: Any] {
        var out: [String: Any] = ["pairRef": pairRef, "outcome": outcome.rawValue]
        if let daemonId { out["daemonId"] = daemonId }
        if let name { out["name"] = name }
        if let reason { out["reason"] = reason }
        return out
    }
}

extension ReadyInfo {
    func payload() -> [String: Any] {
        ["deviceId": deviceId, "name": name, "tiers": tiers, "host": host]
    }
}
