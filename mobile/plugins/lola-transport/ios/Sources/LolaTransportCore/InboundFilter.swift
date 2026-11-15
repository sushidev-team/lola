import Foundation

/// The `ready` payload (01 §4.6).
public struct ReadyInfo: Decodable, Equatable, Sendable {
    public let deviceId: String
    public let name: String
    public let tiers: [String]
    public let via: String
    public let host: String
}

/// Decides which inbound frames stay native (04 §1.2 "Native frames") and
/// runs the relay-credential exchange, so JS never sees a bearer.
///
/// - `ready` is consumed: it authenticates the attempt and becomes a `device`
///   notification. A second `ready` on the same session is dropped.
/// - `resp`/`err` whose id starts with `lola-` answer a native request and
///   never reach JS.
/// - Everything else is passed through untouched.
///
/// Right after `ready` the filter asks for `relayCred {holds}`. A reply with a
/// bearer is saved and acknowledged once by repeating the request with the new
/// hash; the acknowledgement's own reply never triggers another one.
public struct InboundFilter {
    public static let reservedIDPrefix = "lola-"
    public static let relayCredIDPrefix = "lola-relaycred-"
    public static let relayCredCommand = "relayCred"

    public enum Effect: Equatable {
        case device(ReadyInfo)
        /// Persist with `RelayCredentials.save(daemonId:)`.
        case saveCredentials(RelayCredentials)
        /// A complete frame body to write on the session (no length prefix).
        case send(Data)
    }

    public struct Output: Equatable {
        /// Bodies to hand to JS, in order.
        public var frames: [Data] = []
        public var effects: [Effect] = []
    }

    public enum OutboundVerdict: Equatable {
        case allowed
        /// A frame with a `lola-` id or the `relayCred` command.
        case reserved
        /// A body that is not a JSON object.
        case malformed
    }

    private enum Pending { case query, acknowledgement }

    public private(set) var stored: RelayCredentials?
    private let holdsOmitted: Bool
    private var sawReady = false
    private var nextRequest = 1
    private var pending: [String: Pending] = [:]

    /// - Parameters:
    ///   - stored: the daemon's current `RelayCredentials`, if any.
    ///   - credentialDead: the hub refused the stored bearer; omit `holds` so
    ///     the daemon reissues one.
    public init(stored: RelayCredentials?, credentialDead: Bool = false) {
        self.stored = stored
        self.holdsOmitted = credentialDead
    }

    private struct Envelope: Decodable {
        let type: String
        let id: String?
        let payload: Payload?
    }

    private struct Payload: Decodable {
        let ok: Bool?
        let data: CredData?
    }

    private struct CredData: Decodable {
        let available: Bool
        let url: String?
        let rv: String?
        let bearer: String?
    }

    public mutating func filter(_ batch: [Data], now: Date) -> Output {
        var out = Output()
        for body in batch {
            guard let env = try? JSONDecoder().decode(Envelope.self, from: body) else {
                out.frames.append(body)
                continue
            }
            if env.type == "ready" {
                guard !sawReady, let info = Self.ready(in: body) else { continue }
                sawReady = true
                out.effects.append(.device(info))
                out.effects.append(.send(request(holds: holdsOmitted ? nil : stored?.holds, kind: .query)))
            } else if (env.type == "resp" || env.type == "err"), let id = env.id, id.hasPrefix(Self.reservedIDPrefix) {
                handleReply(id: id, env: env, now: now, into: &out)
            } else {
                out.frames.append(body)
            }
        }
        return out
    }

    /// Rejects a batch that touches native state: any `lola-` id, or the
    /// `relayCred` command named at the top level or inside the payload.
    public static func checkOutbound(_ batch: [Data]) -> OutboundVerdict {
        for body in batch {
            guard let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] else { return .malformed }
            if let id = object["id"] as? String, id.hasPrefix(reservedIDPrefix) { return .reserved }
            if object["cmd"] as? String == relayCredCommand { return .reserved }
            if let payload = object["payload"] as? [String: Any], payload["cmd"] as? String == relayCredCommand {
                return .reserved
            }
        }
        return .allowed
    }

    private static func ready(in body: Data) -> ReadyInfo? {
        struct Frame: Decodable { let payload: ReadyInfo }
        return (try? JSONDecoder().decode(Frame.self, from: body))?.payload
    }

    private mutating func handleReply(id: String, env: Envelope, now: Date, into out: inout Output) {
        guard let kind = pending.removeValue(forKey: id) else { return }
        // `err` (an older daemon answers `unknown_cmd`) and `ok:false` change nothing.
        guard env.type == "resp", env.payload?.ok == true, let data = env.payload?.data, data.available else { return }

        let url = data.url ?? stored?.hubURL
        let rv = data.rv ?? stored?.rv
        if let bearer = data.bearer, !bearer.isEmpty, let url, !url.isEmpty, let rv, !rv.isEmpty {
            let next = RelayCredentials(hubURL: url, rv: rv, bearer: bearer, storedAt: now)
            stored = next
            out.effects.append(.saveCredentials(next))
            if kind == .query { out.effects.append(.send(request(holds: next.holds, kind: .acknowledgement))) }
        } else if var current = stored {
            let newURL = data.url ?? current.hubURL
            let newRV = data.rv ?? current.rv
            guard newURL != current.hubURL || newRV != current.rv else { return }
            current.hubURL = newURL
            current.rv = newRV
            stored = current
            out.effects.append(.saveCredentials(current))
        }
    }

    private mutating func request(holds: String?, kind: Pending) -> Data {
        let id = "\(Self.relayCredIDPrefix)\(nextRequest)"
        nextRequest += 1
        pending[id] = kind
        let args = holds.map { "{\"holds\":\(JSONText.quote($0))}" } ?? "{}"
        let body = "{\"v\":2,\"type\":\"req\",\"id\":\"\(id)\",\"cmd\":\"\(Self.relayCredCommand)\","
            + "\"payload\":{\"cmd\":\"\(Self.relayCredCommand)\",\"args\":\(args)}}"
        return Data(body.utf8)
    }
}
