import CryptoKit
import Foundation

/// A `PairResult` (01 §6.4 step 7) the daemon sends once a human confirmed.
public struct PairResult: Decodable, Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    public struct Relay: Decodable, Equatable, Sendable {
        public let rv: String
        public let bearer: String
    }

    public let deviceId: String
    public let name: String
    public let tiers: [String]
    public let host: String
    public let relay: Relay?

    public var description: String { "PairResult(deviceId: \(deviceId), relay: \(relay != nil))" }
    public var debugDescription: String { description }
}

/// What the daemon answered on the `lola-pair` request id.
public enum PairReply: Equatable, Sendable {
    case paired(PairResult)
    case rejected
    case expired
    case failed(reason: String)
    /// `not_paired`: the window closed, or too many candidates, before the
    /// proof was read. The plugin reports it as `expired`.
    case notPaired
    /// Not a frame the pairing can act on.
    case unrecognized
}

/// The `remote.pair` request, the two HMACs and the reply classifier
/// (01 §6.4). Pure: the exporter secrets come from the TLS stack.
public enum PairHandshake {
    public static let requestID = "lola-pair"
    public static let proofExporterLabel = "EXPORTER-lola-pair-proof-v1"
    public static let codeExporterLabel = "EXPORTER-lola-pair-code-v1"
    public static let exporterLength = 32
    private static let codeContext = Data("lola-pair-code-v1".utf8)

    /// `base64url_nopad(HMAC-SHA256(key: secret, msg: exporter))`.
    public static func proof(secret: Data, exporter: Data) -> String {
        Base64URL.encode(hmac(key: secret, message: exporter))
    }

    /// The six digits both ends compute and the human types on the Mac.
    /// `daemonSPKI` and `clientSPKI` are the SHA-256 hashes of each side's
    /// SubjectPublicKeyInfo (32 bytes each), as in Go's `protocol.PairCode`.
    public static func code(exporterCode: Data, daemonSPKI: Data, clientSPKI: Data) -> String {
        let mac = hmac(key: exporterCode, message: codeContext + daemonSPKI + clientSPKI)
        let value = mac.prefix(8).reduce(UInt64(0)) { ($0 << 8) | UInt64($1) }
        return String(format: "%06llu", value % 1_000_000)
    }

    /// The one frame an unpaired certificate may send, without the length
    /// prefix. Key order matches Go's encoding of `protocol.Frame`.
    public static func body(name: String, proof: String) -> Data {
        let payload = "{\"proof\":\(JSONText.quote(proof)),\"name\":\(JSONText.quote(name)),\"platform\":\"ios\"}"
        let frame = "{\"v\":2,\"type\":\"req\",\"id\":\"\(requestID)\",\"cmd\":\"remote.pair\",\"payload\":\(payload)}"
        return Data(frame.utf8)
    }

    private struct Envelope: Decodable {
        let type: String
        let id: String?
        let payload: Payload?
    }

    private struct Payload: Decodable {
        let ok: Bool?
        let data: PairResult?
        let error: String?
        let code: String?
        let message: String?
    }

    /// Classifies the daemon's answer (01 §6.4 step 7, §6.5). `not_paired`
    /// carries no id; every other answer must be on `lola-pair`.
    public static func classify(_ frame: Data) -> PairReply {
        guard let env = try? JSONDecoder().decode(Envelope.self, from: frame) else { return .unrecognized }
        switch env.type {
        case "resp":
            guard env.id == requestID, let p = env.payload else { return .unrecognized }
            if p.ok == true, let result = p.data { return .paired(result) }
            return .failed(reason: p.error ?? "pairing failed")
        case "err":
            guard let p = env.payload else { return .unrecognized }
            if p.code == "not_paired" { return .notPaired }
            guard env.id == requestID else { return .unrecognized }
            switch p.code {
            case "pair_rejected": return .rejected
            case "pair_expired": return .expired
            case "pair_failed": return .failed(reason: p.message ?? "pairing failed")
            default: return .unrecognized
            }
        default:
            return .unrecognized
        }
    }

    private static func hmac(key: Data, message: Data) -> Data {
        Data(HMAC<SHA256>.authenticationCode(for: message, using: SymmetricKey(data: key)))
    }
}
