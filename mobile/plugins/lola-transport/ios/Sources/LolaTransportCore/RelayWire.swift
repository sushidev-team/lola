import Foundation

/// How a hub refused a relay upgrade (03 §13.2). Every refusal is an HTTP
/// status before the 101; the body is not available to the client.
public enum RelayRefusal: Equatable, Sendable {
    /// 401, any code.
    case credentialDead
    /// 404, with or without `Lola-Relay-Code` (an old hub has no relay).
    case hubTooOld
    /// 503 + `offline`.
    case daemonOffline
    /// 503 + `busy`, or 503 with no known code.
    case busy
    /// 504, or the upgrade timeout elapsed.
    case timeout
    /// 429 with `Retry-After` (default 5 s).
    case rateLimited(TimeInterval)
    case forbidden
    case badRequest
    case other(Int)
}

/// The pure half of the relay phone leg: the request and the refusal mapping.
public enum RelayWire {
    public static let phonePath = "/v1/relay/phone"
    public static let subprotocol = "lola-relay.v1"
    public static let rvHeader = "Lola-Relay-Rv"
    public static let bearerHeader = "Lola-Relay-Bearer"
    public static let codeHeader = "Lola-Relay-Code"
    /// = `hubproto.RelayPhoneAttemptTimeout`.
    public static let upgradeTimeout: TimeInterval = 8
    /// URLSession's request timeout, which a WebSocket task also applies as its
    /// receive idle timeout. It must outlast the hub's silence between pings
    /// (`hubproto.RelayDeadAfter` = 60 s); the upgrade is bounded by
    /// `upgradeTimeout` on its own timer.
    public static let idleTimeout: TimeInterval = 120
    public static let defaultRetryAfter: TimeInterval = 5
    public static let maxRetryAfter: TimeInterval = 3600

    /// The upgrade request for `url` (the hub base URL, https) and a bearer
    /// (a stored one, or the QR's window `tok` while pairing).
    ///
    /// No `Authorization`, no `Origin`, no cookies, no query string.
    public static func request(url: URL, rv: String, bearer: String) -> URLRequest {
        var parts = URLComponents(url: url, resolvingAgainstBaseURL: false)
        switch parts?.scheme?.lowercased() {
        case "https": parts?.scheme = "wss"
        case "http": parts?.scheme = "ws"
        default: break
        }
        var path = parts?.path ?? ""
        while path.hasSuffix("/") { path.removeLast() }
        parts?.path = path + phonePath
        parts?.query = nil
        parts?.fragment = nil

        var request = URLRequest(url: parts?.url ?? url)
        request.httpMethod = "GET"
        request.timeoutInterval = idleTimeout
        request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData
        request.httpShouldHandleCookies = false
        request.setValue(rv, forHTTPHeaderField: rvHeader)
        request.setValue(bearer, forHTTPHeaderField: bearerHeader)
        request.setValue(subprotocol, forHTTPHeaderField: "Sec-WebSocket-Protocol")
        return request
    }

    /// 03 §13.2. `code` is the `Lola-Relay-Code` header; `retryAfter` the
    /// `Retry-After` header (delta-seconds; an HTTP-date is not honoured).
    public static func refusal(status: Int, code: String?, retryAfter: String?) -> RelayRefusal {
        switch status {
        case 401: return .credentialDead
        case 404: return .hubTooOld
        case 503: return code == "offline" ? .daemonOffline : .busy
        case 504: return .timeout
        case 429: return .rateLimited(retryAfterSeconds(retryAfter))
        case 403: return .forbidden
        case 400: return .badRequest
        default: return .other(status)
        }
    }

    private static func retryAfterSeconds(_ header: String?) -> TimeInterval {
        guard let header, let n = Int(header.trimmingCharacters(in: .whitespaces)), n >= 0 else {
            return defaultRetryAfter
        }
        return min(TimeInterval(n), maxRetryAfter)
    }
}

/// Per-daemon relay pause and backoff rules (04 §1.3, 03 §13.3), on an
/// injected clock. A value type so the connector can persist it in the
/// `PairingRecord` (`relayState`, `relayRetryAt`).
public struct RelayPolicy: Equatable, Sendable {
    /// `RelayFreshCredentialGrace`: a 401 this soon after storing a bearer is
    /// a backoff, because the hub may not have the new hash yet.
    public static let freshCredentialGrace: TimeInterval = 60
    /// `RelayPhoneUnauthorizedPause`.
    public static let unauthorizedPause: TimeInterval = 3600
    /// `RelayUnsupportedRecheck`.
    public static let unsupportedPause: TimeInterval = 6 * 3600
    public static let rateLimitFloor: TimeInterval = 2

    public private(set) var state: RelayState
    public private(set) var retryAt: Date?
    /// Consecutive backoff-class failures; indexes the relay ladder.
    public private(set) var failures: Int
    /// The hub URL a `hubTooOld` pause was recorded for; another URL lifts it.
    public private(set) var pausedURL: String?

    public init(state: RelayState = .ok, retryAt: Date? = nil, failures: Int = 0, pausedURL: String? = nil) {
        self.state = state
        self.retryAt = retryAt
        self.failures = failures
        self.pausedURL = pausedURL
    }

    /// May a relay attempt start now? `url` is the stored hub URL.
    public func allowsAttempt(now: Date, url: String?) -> Bool {
        if state == .hubTooOld, let pausedURL, let url, pausedURL != url { return true }
        guard let retryAt else { return state != .credentialDead }
        return now >= retryAt
    }

    /// Folds one refused upgrade in. `storedAt` is `RelayCredentials.storedAt`.
    public mutating func record(_ refusal: RelayRefusal, now: Date, storedAt: Date?, url: String?) {
        switch refusal {
        case .credentialDead:
            if let storedAt, now.timeIntervalSince(storedAt) < Self.freshCredentialGrace {
                backoff(now: now)
            } else {
                state = .credentialDead
                retryAt = now.addingTimeInterval(Self.unauthorizedPause)
                failures = 0
            }
        case .hubTooOld:
            state = .hubTooOld
            retryAt = now.addingTimeInterval(Self.unsupportedPause)
            pausedURL = url
            failures = 0
        case .rateLimited(let wait):
            state = .backoff
            retryAt = now.addingTimeInterval(max(wait, Self.rateLimitFloor))
        case .daemonOffline, .busy, .timeout, .forbidden, .badRequest, .other:
            backoff(now: now)
        }
    }

    /// An authenticated relay session clears every pause.
    public mutating func recordSuccess() {
        state = .ok
        retryAt = nil
        failures = 0
        pausedURL = nil
    }

    /// A `relayCred` reply stored a bearer: a dead credential is alive again.
    public mutating func credentialStored() {
        state = .ok
        retryAt = nil
        failures = 0
        pausedURL = nil
    }

    /// The daemon answered `relayCred` with `available`: it has not seen the
    /// hub refuse the relay, so a `hubTooOld` pause (which may stem from a
    /// hub upgraded since) is lifted. Every other state is kept: a dead
    /// credential lifts only on a stored bearer, and a backoff keeps its floor.
    public mutating func relayAvailable() {
        guard state == .hubTooOld else { return }
        state = .ok
        retryAt = nil
        failures = 0
        pausedURL = nil
    }

    private mutating func backoff(now: Date) {
        failures += 1
        state = .backoff
        let ms = ReconnectPolicy.relayDelayMs(failures: failures)
        retryAt = now.addingTimeInterval(TimeInterval(ms) / 1000)
    }
}
