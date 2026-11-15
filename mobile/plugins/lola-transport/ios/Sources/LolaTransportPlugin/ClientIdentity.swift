import CryptoKit
import Foundation
import LolaTransportCore
import Security

/// The phone's per-daemon client key and certificate (04 §1.5).
///
/// One key per paired daemon, so two daemons cannot correlate the phone and
/// revoking on one never touches the other. The key is non-exportable (Secure
/// Enclave when the device has one), never synced, and has no biometric gate:
/// an unattended reconnect must be able to use it. The daemon trusts the SPKI
/// only, so the self-signed certificate carries nothing else that matters.
enum ClientIdentity {
    struct Created {
        let identity: SecIdentity
        /// SHA-256 of the client's SPKI, an input of the pairing code.
        let spkiHash: Data
        let secureEnclave: Bool
    }

    enum Failure: Error {
        case key(String)
        case certificate
        case store(OSStatus)
        case noIdentity
    }

    /// DER prefix of a P-256 `SubjectPublicKeyInfo`; the 65-byte X9.63 point follows.
    private static let spkiPrefix = Data([
        0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01,
        0x06, 0x08, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
    ])

    /// Where an identity lives: the paired one, or a pairing's candidate.
    private struct Name {
        let tag: Data
        let label: String

        static func paired(_ daemonId: String) -> Name {
            Name(tag: Data("dev.sushi.lola.client.\(daemonId)".utf8), label: "lola.client.\(daemonId)")
        }

        static func pending(_ daemonId: String) -> Name {
            Name(
                tag: Data("dev.sushi.lola.client.pending.\(daemonId)".utf8),
                label: "lola.client.pending.\(daemonId)")
        }
    }

    /// Generates a pairing's key and certificate under a PENDING name. The
    /// identity this daemon is already paired with keeps working until the
    /// pairing succeeds (`promote`) and is untouched when it does not
    /// (`discard`). An earlier pending pair for the daemon is replaced.
    static func generatePending(daemonId: String) throws -> Created {
        let name = Name.pending(daemonId)
        remove(name)

        let enclave = SecureEnclave.isAvailable
        var privateAttributes: [String: Any] = [
            kSecAttrIsPermanent as String: true,
            kSecAttrApplicationTag as String: name.tag,
        ]
        var attributes: [String: Any] = [
            kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrKeySizeInBits as String: 256,
        ]
        if enclave {
            // `.privateKeyUsage` is an enclave flag; the software branch sets
            // accessibility only.
            guard
                let access = SecAccessControlCreateWithFlags(
                    nil, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly, .privateKeyUsage, nil)
            else { throw Failure.key("access control") }
            attributes[kSecAttrTokenID as String] = kSecAttrTokenIDSecureEnclave
            privateAttributes[kSecAttrAccessControl as String] = access
        } else {
            privateAttributes[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        }
        attributes[kSecPrivateKeyAttrs as String] = privateAttributes

        var error: Unmanaged<CFError>?
        guard let privateKey = SecKeyCreateRandomKey(attributes as CFDictionary, &error) else {
            throw Failure.key(error?.takeRetainedValue().localizedDescription ?? "key generation failed")
        }
        guard
            let publicKey = SecKeyCopyPublicKey(privateKey),
            let x963 = SecKeyCopyExternalRepresentation(publicKey, &error) as Data?
        else { throw Failure.key("public key") }

        let der: Data
        do {
            der = try ClientCertificate.build(publicKeyX963: x963) { message in
                var signError: Unmanaged<CFError>?
                guard
                    let signature = SecKeyCreateSignature(
                        privateKey, .ecdsaSignatureMessageX962SHA256, message as CFData, &signError) as Data?
                else { throw Failure.certificate }
                return signature
            }
        } catch {
            remove(name)
            throw Failure.certificate
        }

        guard let certificate = SecCertificateCreateWithData(nil, der as CFData) else {
            remove(name)
            throw Failure.certificate
        }
        let status = SecItemAdd(
            [
                kSecClass as String: kSecClassCertificate,
                kSecValueRef as String: certificate,
                kSecAttrLabel as String: name.label,
                kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            ] as CFDictionary, nil)
        guard status == errSecSuccess else {
            remove(name)
            throw Failure.store(status)
        }
        guard let identity = identity(label: name.label) else {
            remove(name)
            throw Failure.noIdentity
        }
        return Created(
            identity: identity, spkiHash: Data(SHA256.hash(data: spkiPrefix + x963)), secureEnclave: enclave)
    }

    /// Makes the pending identity the daemon's paired one and deletes the one
    /// it replaces. Nothing changes when the pending certificate cannot be
    /// renamed.
    static func promote(daemonId: String) throws {
        let pending = Name.pending(daemonId)
        let paired = Name.paired(daemonId)
        let previous = identity(label: paired.label)
        let renamed = SecItemUpdate(
            [kSecClass as String: kSecClassCertificate, kSecAttrLabel as String: pending.label] as CFDictionary,
            [kSecAttrLabel as String: paired.label] as CFDictionary)
        guard renamed == errSecSuccess else { throw Failure.store(renamed) }
        if let previous { removeItems(of: previous) }
        deleteKeys(tag: paired.tag)
        // The identity is found by its certificate's label alone, and `remove`
        // reaches the key through it, so a key that keeps its pending tag
        // still works and is still deleted.
        let retagged = SecItemUpdate(
            keyQuery(tag: pending.tag) as CFDictionary, [kSecAttrApplicationTag as String: paired.tag] as CFDictionary)
        if retagged != errSecSuccess { LolaLog.warn("the paired key kept its pending tag") }
    }

    /// Deletes a pairing's pending key and certificate. Idempotent.
    static func discard(daemonId: String) {
        remove(.pending(daemonId))
    }

    /// The stored identity, nil when this daemon has none.
    static func identity(daemonId: String) -> SecIdentity? {
        identity(label: Name.paired(daemonId).label)
    }

    /// Deletes the paired key and certificate. Idempotent.
    static func delete(daemonId: String) {
        remove(.paired(daemonId))
    }

    private static func identity(label: String) -> SecIdentity? {
        var out: CFTypeRef?
        let status = SecItemCopyMatching(
            [
                kSecClass as String: kSecClassIdentity,
                kSecAttrLabel as String: label,
                kSecReturnRef as String: true,
                kSecMatchLimit as String: kSecMatchLimitOne,
            ] as CFDictionary, &out)
        guard status == errSecSuccess, let out, CFGetTypeID(out) == SecIdentityGetTypeID() else { return nil }
        return (out as! SecIdentity)
    }

    private static func remove(_ name: Name) {
        if let identity = identity(label: name.label) { removeItems(of: identity) }
        SecItemDelete([kSecClass as String: kSecClassCertificate, kSecAttrLabel as String: name.label] as CFDictionary)
        deleteKeys(tag: name.tag)
    }

    /// Deletes exactly this identity's certificate and private key, by reference.
    private static func removeItems(of identity: SecIdentity) {
        var key: SecKey?
        if SecIdentityCopyPrivateKey(identity, &key) == errSecSuccess, let key {
            SecItemDelete([kSecValueRef as String: key] as CFDictionary)
        }
        var certificate: SecCertificate?
        if SecIdentityCopyCertificate(identity, &certificate) == errSecSuccess, let certificate {
            SecItemDelete([kSecValueRef as String: certificate] as CFDictionary)
        }
    }

    private static func deleteKeys(tag: Data) {
        SecItemDelete(keyQuery(tag: tag) as CFDictionary)
    }

    private static func keyQuery(tag: Data) -> [String: Any] {
        [
            kSecClass as String: kSecClassKey,
            kSecAttrApplicationTag as String: tag,
            kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
        ]
    }
}
