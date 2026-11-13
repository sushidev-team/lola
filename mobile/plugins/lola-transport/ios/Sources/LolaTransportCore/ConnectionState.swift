import Foundation

/// The connection lifecycle, as the app sees it.
///
/// It is a separate vocabulary from `NWConnection.State` on purpose. Network
/// framework's states answer "what is the socket doing", and the app's question
/// is different and coarser: is there something to type into, and if not, is
/// that because the phone is on the wrong network or because the daemon said
/// no. Those two are the distinction the whole enum exists to make, because
/// they have completely different remedies and a single "disconnected" state
/// forces a human to guess which one they are looking at.
public enum LolaConnectionPhase: String, Equatable, Sendable {
    case connecting
    case handshaking
    case connected
    case failed
    case closed
}

/// Why a connection failed or closed.
///
/// Deliberately small. Every value here has to survive being rendered as a
/// sentence to somebody holding a phone, so a code that only a developer could
/// act on belongs in `reason` instead.
public enum LolaFailureCode: String, Equatable, Sendable {
    /// No route, connection refused, or the local-network permission was
    /// denied.
    ///
    /// Those three genuinely cannot be told apart on iOS: a denied local-network
    /// permission surfaces as an ordinary unreachable-host failure, the prompt
    /// never comes back, and there is no API that reports the decision. So the
    /// app shows the Settings hint on a first-connect failure and does not
    /// pretend to know which of the three it is.
    case network

    /// A connect or handshake budget elapsed.
    case timeout

    /// The TLS handshake failed for some reason other than the pin.
    case tls

    /// The peer's SPKI hash is not the pinned one. Distinct from `tls` because
    /// it is the one TLS failure with a specific, actionable meaning: either
    /// the pin was copied wrong, or this is not the daemon it claims to be.
    case pinMismatch = "pin_mismatch"

    /// The daemon refused the handshake.
    case rejected

    /// The peer violated the framing: a zero-length or oversized frame.
    case protocolViolation = "protocol"

    /// The daemon closed the connection.
    case peerClosed = "peer_closed"

    /// `disconnect()` was called.
    case clientClosed = "client_closed"

    /// The app was backgrounded and the socket was torn down.
    ///
    /// This is not a failure and is reported as a `closed` phase. It has its
    /// own code because the app's correct response is unlike any other: come
    /// back to the foreground and reconnect, rather than show anything.
    case backgrounded

    /// A defect in the plugin.
    case internalError = "internal"
}

/// One lifecycle event, ready to be turned into a bridge payload.
///
/// `epoch` is what makes a late callback recognisably stale. Network
/// framework's handlers are queued, and tearing a connection down does not
/// unqueue the callbacks already scheduled behind it, so a connection that has
/// been replaced can still deliver a state change or a read afterwards. Every
/// event carries the epoch of the connection that produced it, and the JavaScript
/// side drops anything that is not the current one.
public struct LolaConnectionEvent: Equatable, Sendable {
    public let epoch: Int
    public let phase: LolaConnectionPhase
    public let code: LolaFailureCode?
    public let reason: String?
    public let spkiPin: String?
    public let pinned: Bool?
    public let daemonCode: String?
    public let minV: Int?
    public let maxV: Int?

    public init(
        epoch: Int,
        phase: LolaConnectionPhase,
        code: LolaFailureCode? = nil,
        reason: String? = nil,
        spkiPin: String? = nil,
        pinned: Bool? = nil,
        daemonCode: String? = nil,
        minV: Int? = nil,
        maxV: Int? = nil
    ) {
        self.epoch = epoch
        self.phase = phase
        self.code = code
        self.reason = reason
        self.spkiPin = spkiPin
        self.pinned = pinned
        self.daemonCode = daemonCode
        self.minV = minV
        self.maxV = maxV
    }

    /// The dictionary shape `notifyListeners` sends and `LolaStateEvent`
    /// declares. Absent fields are omitted rather than sent as null, so the
    /// TypeScript optionals mean what they say.
    public func payload() -> [String: Any] {
        var out: [String: Any] = ["epoch": epoch, "phase": phase.rawValue]
        if let code { out["code"] = code.rawValue }
        if let reason { out["reason"] = reason }
        if let spkiPin { out["spkiPin"] = spkiPin }
        if let pinned { out["pinned"] = pinned }
        if let daemonCode { out["daemonCode"] = daemonCode }
        if let minV { out["minV"] = minV }
        if let maxV { out["maxV"] = maxV }
        return out
    }
}
