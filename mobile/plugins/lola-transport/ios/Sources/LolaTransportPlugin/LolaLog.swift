import Foundation
import os

/// The plugin's logging, and its one rule: nothing that passes through this
/// transport is ever logged. No frame body, no pane byte, no secret, no
/// pairing payload. The vocabulary is a phase, a host and port, a count and an
/// error's kind; anything needing more is a packet capture on the developer's
/// own machine.
enum LolaLog {
    private static let log = Logger(subsystem: "dev.sushi.lola.mobile", category: "transport")

    static func info(_ message: String) {
        log.info("\(message, privacy: .public)")
    }

    static func warn(_ message: String) {
        log.warning("\(message, privacy: .public)")
    }

    static func error(_ message: String) {
        log.error("\(message, privacy: .public)")
    }

    #if LOLA_DEV_LINK
        /// The one line the Simulator harness reads (`lola-dev pair-code <6 digits>`).
        /// The code is a display value shown to the human on the phone, not a
        /// secret, and only a `dev-launch` pairing ever writes it.
        static func devLinkNote(_ message: String) {
            log.info("lola-dev \(message, privacy: .public)")
        }
    #endif
}
