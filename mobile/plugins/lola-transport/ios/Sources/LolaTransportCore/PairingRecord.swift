import Foundation

/// Everything the phone remembers about one paired daemon, except secrets.
///
/// Kept in the Keychain (generic password, account = `daemonId`). It holds no
/// bearer: the relay bearer lives only in `RelayCredentials`.
public struct PairingRecord: Codable, Equatable, Sendable {
    public static let currentVersion = 2
    public static let service = "dev.sushi.lola.mobile.pairing"

    public var v: Int = PairingRecord.currentVersion
    public var daemonId: String
    public var name: String
    public var pin: String
    public var deviceId: String
    public var addrs: [String]
    public var port: Int
    /// The direct `host:port` that last authenticated.
    public var lastGood: String?
    /// Hub base URL; the bearer is not here.
    public var relayURL: String?
    public var relayState: String
    public var relayRetryAt: Date?
    public var pairedAt: Date
    public var secureEnclave: Bool

    public init(
        daemonId: String, name: String, pin: String, deviceId: String,
        addrs: [String], port: Int, lastGood: String? = nil,
        relayURL: String? = nil, relayState: RelayState = .none, relayRetryAt: Date? = nil,
        pairedAt: Date, secureEnclave: Bool
    ) {
        self.daemonId = daemonId
        self.name = name
        self.pin = pin
        self.deviceId = deviceId
        self.addrs = addrs
        self.port = port
        self.lastGood = lastGood
        self.relayURL = relayURL
        self.relayState = relayState.rawValue
        self.relayRetryAt = relayRetryAt
        self.pairedAt = pairedAt
        self.secureEnclave = secureEnclave
    }

    public enum Failure: Error, Equatable {
        case unsupportedVersion(Int)
        case undecodable
    }

    private static func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.sortedKeys]
        return e
    }

    private static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    public func encoded() throws -> Data { try Self.encoder().encode(self) }

    /// A record of another version is refused, never half-read.
    public static func decode(_ data: Data) throws -> PairingRecord {
        struct Probe: Decodable { let v: Int }
        guard let probe = try? decoder().decode(Probe.self, from: data) else { throw Failure.undecodable }
        guard probe.v == currentVersion else { throw Failure.unsupportedVersion(probe.v) }
        guard let record = try? decoder().decode(PairingRecord.self, from: data) else { throw Failure.undecodable }
        return record
    }

    public var relay: RelayState { RelayState(rawValue: relayState) ?? .none }

    // MARK: Keychain

    public static func load(daemonId: String) throws -> PairingRecord? {
        switch LolaKeychain.read(service: service, account: daemonId) {
        case .value(let data): return try decode(data)
        case .missing: return nil
        case .failed(let status): throw LolaKeychain.Failure(status)
        }
    }

    public func save() throws {
        switch LolaKeychain.write(try encoded(), service: Self.service, account: daemonId) {
        case .ok: return
        case .notFound, .duplicate: throw LolaKeychain.Failure(errSecInternalError)
        case .failed(let status): throw LolaKeychain.Failure(status)
        }
    }

    public static func delete(daemonId: String) throws {
        if case .failed(let status) = LolaKeychain.delete(service: service, account: daemonId) {
            throw LolaKeychain.Failure(status)
        }
    }
}
