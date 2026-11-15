import Foundation

/// The QR payload `lola2.<base64url(JSON)>` (01 §6.2), decoded strictly.
///
/// The secret and the relay `tok` never appear in an error or a description,
/// so a failed decode of a camera frame cannot leak them into a log.
public struct PairPayload: Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    public static let prefix = "lola2."
    public static let retiredPrefix = "lola1."
    public static let maxAddrs = 8

    public struct Relay: Equatable, Sendable {
        public let url: String
        public let rv: String
        public let tok: String
    }

    /// Why a scanned string is not a payload. Maps one to one to the plugin's
    /// `invalid` field.
    public enum Failure: String, Error, Equatable, Sendable {
        case notLola = "not_lola"
        case oldFormat = "old_format"
        case malformed
    }

    /// Daemon SPKI SHA-256, standard base64 (44 chars).
    public let pin: String
    public let addrs: [String]
    public let port: Int
    /// The 32 decoded secret bytes: the HMAC key of the pairing proof.
    public let secret: Data
    public let relay: Relay?

    /// base64url (no padding) of the pinned SPKI hash; names the daemon in
    /// the Keychain and in `daemons()`.
    public var daemonId: String {
        // `decode` guarantees the pin is a 32-byte base64 value.
        Base64URL.encode(Data(base64Encoded: pin) ?? Data())
    }

    public var description: String {
        "PairPayload(addrs: \(addrs.count), relay: \(relay != nil), secret: <redacted>)"
    }
    public var debugDescription: String { description }

    private struct Wire: Decodable {
        let v: Int
        let pin: String
        let addrs: [String]?
        let port: Int?
        let secret: String
        let relay: RelayWire?
    }

    private struct RelayWire: Decodable {
        let url: String
        let rv: String
        let tok: String
    }

    /// Unknown JSON fields are ignored so a newer daemon can add one.
    public static func decode(_ text: String) throws -> PairPayload {
        guard text.hasPrefix(prefix) else {
            throw text.hasPrefix(retiredPrefix) ? Failure.oldFormat : Failure.notLola
        }
        guard text.utf8.count <= Limits.maxPairPayloadChars,
            let raw = Base64URL.decode(String(text.dropFirst(prefix.count))),
            let wire = try? JSONDecoder().decode(Wire.self, from: raw),
            wire.v == 2
        else { throw Failure.malformed }

        guard let pinBytes = Data(base64Encoded: wire.pin), pinBytes.count == 32,
            wire.pin.utf8.count == 44,
            let secret = Base64URL.decode(wire.secret), secret.count == 32
        else { throw Failure.malformed }

        let addrs = wire.addrs ?? []
        let port = wire.port ?? 0
        guard addrs.count <= maxAddrs, addrs.allSatisfy(isIPLiteral) else { throw Failure.malformed }
        if addrs.isEmpty {
            guard port == 0 else { throw Failure.malformed }
        } else {
            guard (1...65535).contains(port) else { throw Failure.malformed }
        }

        var relay: Relay?
        if let r = wire.relay {
            guard isHTTPSURL(r.url), matches(r.rv, "^[0-9a-f]{32}$"), matches(r.tok, "^[A-Za-z0-9_-]{43}$")
            else { throw Failure.malformed }
            relay = Relay(url: r.url, rv: r.rv, tok: r.tok)
        }
        guard !addrs.isEmpty || relay != nil else { throw Failure.malformed }

        return PairPayload(pin: wire.pin, addrs: addrs, port: port, secret: secret, relay: relay)
    }

    private static func isIPLiteral(_ s: String) -> Bool {
        guard !s.contains("%") else { return false }
        var v4 = in_addr()
        var v6 = in6_addr()
        return inet_pton(AF_INET, s, &v4) == 1 || inet_pton(AF_INET6, s, &v6) == 1
    }

    private static func isHTTPSURL(_ s: String) -> Bool {
        guard let c = URLComponents(string: s), c.scheme == "https", let host = c.host, !host.isEmpty else { return false }
        return true
    }

    private static func matches(_ s: String, _ pattern: String) -> Bool {
        s.range(of: pattern, options: .regularExpression) != nil
    }
}
