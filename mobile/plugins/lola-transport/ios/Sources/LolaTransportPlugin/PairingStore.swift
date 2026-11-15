import Foundation
import LolaTransportCore
import Security

/// Everything stored for the paired daemons, in one place: the records, the
/// relay credentials and the client keys are deleted together.
enum PairingStore {
    private static let lastPathPrefix = "dev.sushi.lola.lastPath."

    /// Every readable record. A record that no longer decodes is skipped: it
    /// cannot be connected to, and `forget` still removes it by id.
    static func records() -> [PairingRecord] {
        var out: CFTypeRef?
        let status = SecItemCopyMatching(
            [
                kSecClass as String: kSecClassGenericPassword,
                kSecAttrService as String: PairingRecord.service,
                kSecReturnAttributes as String: true,
                kSecMatchLimit as String: kSecMatchLimitAll,
            ] as CFDictionary, &out)
        guard status == errSecSuccess, let rows = out as? [[String: Any]] else { return [] }
        return rows.compactMap { row in
            guard let account = row[kSecAttrAccount as String] as? String else { return nil }
            return (try? PairingRecord.load(daemonId: account)) ?? nil
        }
    }

    static func forget(daemonId: String) {
        try? PairingRecord.delete(daemonId: daemonId)
        try? RelayCredentials.delete(daemonId: daemonId)
        ClientIdentity.delete(daemonId: daemonId)
        UserDefaults.standard.removeObject(forKey: lastPathPrefix + daemonId)
    }

    /// The path a daemon last authenticated on. A hint for the UI, not a secret.
    static func lastPath(daemonId: String) -> PathKind? {
        UserDefaults.standard.string(forKey: lastPathPrefix + daemonId).flatMap(PathKind.init(rawValue:))
    }

    static func setLastPath(_ kind: PathKind, daemonId: String) {
        UserDefaults.standard.set(kind.rawValue, forKey: lastPathPrefix + daemonId)
    }
}
