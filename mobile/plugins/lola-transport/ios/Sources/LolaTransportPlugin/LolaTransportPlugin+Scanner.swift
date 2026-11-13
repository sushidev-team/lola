import Capacitor
import Foundation
import UIKit

/// The bridge half of the QR scanner. Everything with a camera in it lives in
/// `LolaQRScanner.swift`; this file only translates.
extension LolaTransportPlugin {
    /// What the app should ask BEFORE it draws a Scan button.
    ///
    /// It exists because of the Simulator. There is no camera there and there
    /// cannot be one, so the whole development loop would otherwise show a
    /// control that always fails on tap — and a dead control is read as a bug
    /// in the feature rather than as a property of the machine. Resolves rather
    /// than rejects: "you cannot scan here" is an answer, not an error.
    @objc func scanCapability(_ call: CAPPluginCall) {
        let report = LolaQRScanner.availability()
        var payload: [String: Any] = [
            "available": report.available,
            "authorization": report.authorization,
        ]
        if let reason = report.reason {
            payload["reason"] = reason.rawValue
        }
        call.resolve(payload)
    }

    /// Opens the scanner and resolves with whatever the symbol carried.
    ///
    /// Cancellation RESOLVES with `cancelled: true` rather than rejecting. A
    /// human tapping Cancel is the ordinary way this ends, and routing it
    /// through the error channel means every call site has to tell an expected
    /// outcome apart from a broken camera by reading a code — which is exactly
    /// the sort of thing that ends up rendered as a red banner.
    @objc func scanQR(_ call: CAPPluginCall) {
        let prompt = call.getString("prompt")

        // Presenting a view controller is main-thread work, and the bridge's
        // own view controller may only be touched there.
        DispatchQueue.main.async { [weak self] in
            LolaQRScanner.present(from: self?.bridge?.viewController, prompt: prompt) { outcome in
                switch outcome {
                case .value(let value):
                    // The decoded string is NOT logged. It is a pairing payload
                    // whose whole point is to carry a secret, and LolaLog's one
                    // rule is that nothing passing through this plugin reaches
                    // the device log.
                    call.resolve(["cancelled": false, "value": value])
                case .cancelled:
                    call.resolve(["cancelled": true])
                case .failed(let failure):
                    call.reject(failure.reason, failure.code.rawValue)
                }
            }
        }
    }
}
