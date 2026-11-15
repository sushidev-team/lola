import CryptoKit
import Foundation

/// The relay credential of one paired daemon: the ONLY place a bearer is
/// stored. Keychain generic password, account = `daemonId`.
public struct RelayCredentials: Codable, Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    public static let service = "dev.sushi.lola.mobile.relay"

    public var hubURL: String
    public var rv: String
    public var bearer: String
    /// When this bearer was stored; a 401 within `RelayPolicy.freshCredentialGrace`
    /// of it is a backoff, not a dead credential.
    public var storedAt: Date

    public init(hubURL: String, rv: String, bearer: String, storedAt: Date) {
        self.hubURL = hubURL
        self.rv = rv
        self.bearer = bearer
        self.storedAt = storedAt
    }

    /// Hex SHA-256 of the bearer: what `relayCred {holds}` carries.
    public var holds: String {
        SHA256.hash(data: Data(bearer.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    public var description: String { "RelayCredentials(rv: \(rv), bearer: <redacted>)" }
    public var debugDescription: String { description }

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
    public static func decode(_ data: Data) throws -> RelayCredentials { try decoder().decode(RelayCredentials.self, from: data) }

    // MARK: Keychain

    /// `nil` means no credential is stored.
    public static func load(daemonId: String) throws -> RelayCredentials? {
        switch LolaKeychain.read(service: service, account: daemonId) {
        case .value(let data): return try decode(data)
        case .missing: return nil
        case .failed(let status): throw LolaKeychain.Failure(status)
        }
    }

    /// One `SecItemUpdate` or `SecItemAdd`: the record is replaced whole.
    public func save(daemonId: String) throws {
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
