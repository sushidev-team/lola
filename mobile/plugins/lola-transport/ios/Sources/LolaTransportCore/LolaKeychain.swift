import Foundation
import Security

/// Generic-password CRUD, one item per (service, account).
///
/// Core, not Plugin, because `SecItem*` needs neither Capacitor nor Network:
/// the decisions live here and the plugin only forwards calls.
///
/// - Uniqueness is service + account (+ access group + synchronizable);
///   `kSecAttrGeneric` is not part of it.
/// - No access group is set: the app's default group needs no entitlements file.
/// - Items are `AfterFirstUnlockThisDeviceOnly`: readable while the screen is
///   locked (an unattended reconnect needs that), never synced, never in a
///   backup. No `.userPresence` / biometry: those block every read on Face ID.
/// - A write is one `SecItemAdd`, or one `SecItemUpdate` on a duplicate, so a
///   record is replaced atomically.
/// - No value, and no account, ever appears in a message: failures carry an
///   `OSStatus` only.
public enum LolaKeychain {

    /// Reading, writing or deleting failed with this status.
    public struct Failure: Error, Equatable {
        public let status: OSStatus
        public init(_ status: OSStatus) { self.status = status }
    }

    public enum Status: Equatable {
        case ok
        case notFound
        case duplicate
        case failed(OSStatus)
    }

    /// `missing` is an answer (a first launch), not a failure.
    public enum Read: Equatable {
        case value(Data)
        case missing
        case failed(OSStatus)
    }

    private static let accessible = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly

    /// The raw `OSStatus` vocabulary reduced to the outcomes callers branch on.
    public static func classify(_ raw: OSStatus) -> Status {
        switch raw {
        case errSecSuccess: return .ok
        case errSecItemNotFound: return .notFound
        case errSecDuplicateItem: return .duplicate
        default: return .failed(raw)
        }
    }

    public static func describe(_ status: OSStatus) -> String {
        "the keychain refused the operation (OSStatus \(status))"
    }

    /// Creates or replaces. `kSecAttrAccessible` is re-asserted on the update
    /// path so an item written by an older build is upgraded in place.
    @discardableResult
    public static func write(_ value: Data, service: String, account: String) -> Status {
        guard !service.isEmpty, !account.isEmpty, !value.isEmpty else { return .failed(errSecParam) }

        var add = query(service, account)
        add[kSecValueData as String] = value
        add[kSecAttrAccessible as String] = accessible

        switch classify(SecItemAdd(add as CFDictionary, nil)) {
        case .ok:
            return .ok
        case .duplicate:
            let attributes: [String: Any] = [
                kSecValueData as String: value,
                kSecAttrAccessible as String: accessible,
            ]
            let updated = SecItemUpdate(query(service, account) as CFDictionary, attributes as CFDictionary)
            return classify(updated) == .ok ? .ok : .failed(updated)
        case .notFound:
            return .failed(errSecItemNotFound)
        case .failed(let raw):
            return .failed(raw)
        }
    }

    public static func read(service: String, account: String) -> Read {
        guard !service.isEmpty, !account.isEmpty else { return .failed(errSecParam) }
        var q = query(service, account)
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        q[kSecReturnData as String] = true

        var out: CFTypeRef?
        let raw = SecItemCopyMatching(q as CFDictionary, &out)
        switch classify(raw) {
        case .ok:
            guard let data = out as? Data else { return .failed(errSecDecode) }
            return .value(data)
        case .notFound:
            return .missing
        case .duplicate, .failed:
            return .failed(raw)
        }
    }

    /// Idempotent: deleting what is not there succeeds.
    @discardableResult
    public static func delete(service: String, account: String) -> Status {
        guard !service.isEmpty, !account.isEmpty else { return .failed(errSecParam) }
        let raw = SecItemDelete(query(service, account) as CFDictionary)
        switch classify(raw) {
        case .ok, .notFound: return .ok
        case .duplicate, .failed: return .failed(raw)
        }
    }

    private static func query(_ service: String, _ account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }
}
