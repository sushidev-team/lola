import Foundation
import os

/// The plugin's logging, and its one rule.
///
/// NOTHING that passes through this transport is ever logged: not the bearer
/// key, not a frame body, not a byte of pane output. The daemon holds the same
/// line on its own side — `internal/remote`'s audit line carries the device,
/// the command and the outcome and explicitly never the payload, because
/// `answer` carries prose a human typed at a phone and may contain a pasted
/// token. A client that logged the same frames would undo that at the other
/// end, and on iOS the log is readable by anyone with the device and a cable.
///
/// So the vocabulary here is deliberately thin: a phase, a host and port, a
/// count, an error's kind. When something needs more detail than that to
/// diagnose, the right answer is a packet capture on the developer's own
/// machine, not a wider log.
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
}
