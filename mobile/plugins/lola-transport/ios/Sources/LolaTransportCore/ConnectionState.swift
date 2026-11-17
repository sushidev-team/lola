import Foundation

/// The connection lifecycle as the app sees it (04 §1.6). Coarser than
/// `NWConnection.State` on purpose: the app asks "is there something to type
/// into, and if not, why", never "what is the socket doing".
public enum ConnectionPhase: String, Equatable, Sendable {
    case idle, racing, ready, migrating, closed, failed
}

/// Why a connection failed or closed. Every value must survive being rendered
/// as one sentence to somebody holding a phone.
public enum FailureCode: String, Equatable, Sendable {
    /// The path monitor reports no usable interface.
    case offline
    /// Every candidate failed before an authenticated `ready`.
    case unreachable
    case timeout
    /// Every candidate that completed a certificate exchange showed the same
    /// key, and it is not the pinned one.
    case keyChanged = "key_changed"
    /// The daemon (not a hub, not a stranger) refused this device.
    case notPaired = "not_paired"
    case unsupportedVersion = "unsupported_version"
    /// Relay was the only candidate and the hub answered 503 or 504.
    case relayUnavailable = "relay_unavailable"
    case protocolViolation = "protocol"
    case peerClosed = "peer_closed"
    case clientClosed = "client_closed"
    case backgrounded
    case internalError = "internal"

    /// The connector retries these on its own while the app is foreground;
    /// `notPaired` and `unsupportedVersion` never retry.
    public var isRetryable: Bool {
        switch self {
        case .offline, .unreachable, .timeout, .relayUnavailable, .peerClosed, .backgrounded, .keyChanged:
            return true
        case .notPaired, .unsupportedVersion, .protocolViolation, .clientClosed, .internalError:
            return false
        }
    }
}

public enum RelayState: String, Equatable, Sendable, Codable {
    case none, ok, backoff
    case credentialDead = "credential_dead"
    case hubTooOld = "hub_too_old"
}

public enum PathKind: String, Equatable, Sendable {
    /// The daemon's `ReadyData.via == "lan"`.
    case direct
    case relay
}

public enum NetworkInterface: String, Equatable, Sendable {
    case wifi, cellular, wired, other
}

public struct PathInfo: Equatable, Sendable {
    public let kind: PathKind
    public let host: String?
    public let port: Int?
    public let interface: NetworkInterface?

    public init(kind: PathKind, host: String? = nil, port: Int? = nil, interface: NetworkInterface? = nil) {
        self.kind = kind
        self.host = host
        self.port = port
        self.interface = interface
    }

    /// Absent fields are omitted, never sent as null, so the TypeScript
    /// optionals mean what they say.
    public func payload() -> [String: Any] {
        var out: [String: Any] = ["kind": kind.rawValue]
        if let host { out["host"] = host }
        if let port { out["port"] = port }
        if let interface { out["interface"] = interface.rawValue }
        return out
    }
}

/// One lifecycle event, shaped like `StateEvent` in `src/definitions.ts`.
///
/// `epoch` makes a late callback recognisably stale: Network.framework's
/// handlers are queued behind a teardown, so a replaced connection can still
/// deliver a state change. JS drops anything that is not the current epoch.
public struct ConnectionState: Equatable, Sendable {
    public let epoch: Int
    public let phase: ConnectionPhase
    public let path: PathInfo?
    public let code: FailureCode?
    public let daemonCode: String?
    public let minV: Int?
    public let maxV: Int?
    public let raceMs: Int?
    public let relay: RelayState?

    public init(
        epoch: Int,
        phase: ConnectionPhase,
        path: PathInfo? = nil,
        code: FailureCode? = nil,
        daemonCode: String? = nil,
        minV: Int? = nil,
        maxV: Int? = nil,
        raceMs: Int? = nil,
        relay: RelayState? = nil
    ) {
        self.epoch = epoch
        self.phase = phase
        self.path = path
        self.code = code
        self.daemonCode = daemonCode
        self.minV = minV
        self.maxV = maxV
        self.raceMs = raceMs
        self.relay = relay
    }

    public func payload() -> [String: Any] {
        var out: [String: Any] = ["epoch": epoch, "phase": phase.rawValue]
        if let path { out["path"] = path.payload() }
        if let code { out["code"] = code.rawValue }
        if let daemonCode { out["daemonCode"] = daemonCode }
        if let minV { out["minV"] = minV }
        if let maxV { out["maxV"] = maxV }
        if let raceMs { out["raceMs"] = raceMs }
        if let relay { out["relay"] = relay.rawValue }
        return out
    }
}
