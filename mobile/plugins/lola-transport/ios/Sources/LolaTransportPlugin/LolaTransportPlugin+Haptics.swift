import Capacitor
import Foundation
import LolaTransportCore
import UIKit

extension LolaTransportPlugin {
    private static var lastHaptic = DispatchTime(uptimeNanoseconds: 0)

    /// One haptic. Calls closer than `HAPTIC_MIN_GAP_MS` to the last are
    /// dropped, so a burst of events never turns into a buzz. Always resolves:
    /// a missing motor is not a failure the app can act on.
    @objc func haptic(_ call: CAPPluginCall) {
        guard admit(call) else { return }
        guard let kind = call.getString("kind") else {
            return call.reject("kind is required", FailureCode.internalError.rawValue)
        }
        DispatchQueue.main.async {
            let now = DispatchTime.now()
            let gap = Int((now.uptimeNanoseconds &- Self.lastHaptic.uptimeNanoseconds) / 1_000_000)
            if gap >= Limits.hapticMinGapMs {
                Self.lastHaptic = now
                switch kind {
                case "light": UIImpactFeedbackGenerator(style: .light).impactOccurred()
                case "medium": UIImpactFeedbackGenerator(style: .medium).impactOccurred()
                case "rigid": UIImpactFeedbackGenerator(style: .rigid).impactOccurred()
                case "selection": UISelectionFeedbackGenerator().selectionChanged()
                case "success": UINotificationFeedbackGenerator().notificationOccurred(.success)
                case "warning": UINotificationFeedbackGenerator().notificationOccurred(.warning)
                case "error": UINotificationFeedbackGenerator().notificationOccurred(.error)
                default: break
                }
            }
            call.resolve()
        }
    }
}
