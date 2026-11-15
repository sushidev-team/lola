import Capacitor
import Foundation
import LolaTransportCore
import UIKit

/// The bridge half of the QR scanner. Everything with a camera in it lives in
/// `LolaQRScanner.swift`; this file translates and keeps the payload native.
extension LolaTransportPlugin {
    /// What the app asks BEFORE it draws a Scan button. The Simulator has no
    /// camera, so a button that always fails would read as a bug in the feature.
    /// Resolves rather than rejects: "you cannot scan here" is an answer.
    @objc func scanCapability(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        let report = LolaQRScanner.availability()
        var payload: [String: Any] = ["available": report.available, "authorization": report.authorization]
        if let reason = report.reason { payload["reason"] = reason.rawValue }
        call.resolve(payload)
    }

    /// Opens the scanner. A `lola2.` payload is parsed and HELD natively under a
    /// `pairRef`; only its public half comes back. Cancellation resolves
    /// `{cancelled: true}`: it is the ordinary way a scan ends, not an error.
    @objc func scanPairing(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        let prompt = call.getString("prompt")
        DispatchQueue.main.async { [weak self] in
            LolaQRScanner.present(from: self?.bridge?.viewController, prompt: prompt) { outcome in
                switch outcome {
                case .value(let text):
                    // The decoded string is a pairing payload and is never logged.
                    self?.queue.async { self?.held(text, call) }
                case .cancelled:
                    call.resolve(["cancelled": true])
                case .failed(let failure):
                    call.reject(failure.reason, failure.code.rawValue)
                }
            }
        }
    }

    private func held(_ text: String, _ call: CAPPluginCall) {
        do {
            let payload = try PairPayload.decode(text)
            call.resolve([
                "cancelled": false,
                "pairRef": pairing.hold(payload),
                "daemonId": payload.daemonId,
                "addrs": payload.addrs.count,
                "relay": payload.relay != nil,
            ])
        } catch let failure as PairPayload.Failure {
            call.resolve(["cancelled": false, "invalid": failure.rawValue])
        } catch {
            call.resolve(["cancelled": false, "invalid": PairPayload.Failure.malformed.rawValue])
        }
    }
}
