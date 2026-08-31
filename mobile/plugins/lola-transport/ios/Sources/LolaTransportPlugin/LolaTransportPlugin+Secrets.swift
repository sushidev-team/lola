import Capacitor
import Foundation
import LolaTransportCore

/// The bridge half of the Keychain. Everything with a `SecItem` call in it lives
/// in `LolaTransportCore.LolaKeychain`; this file only translates.
///
/// WHY THE PLUGIN OWNS THE SECRET STORE AT ALL. The key is a bearer credential,
/// and the WebView is the wrong side of the app to keep one on: `localStorage`
/// is plain, unencrypted, per-origin storage inside the container, readable by
/// any code running in the page and backed up with the app. The plugin is also
/// the only component that ever needs the plaintext — it puts the key on the
/// wire during the bearer handshake — so writing it here means the key crosses
/// the bridge exactly twice in its life: once when a human types or scans it,
/// and once on each read at reconnect.
///
/// A MISSING KEY RESOLVES, IT DOES NOT REJECT. `secretGet` answers
/// `{ value: null }` when there is no item, because a first launch is not an
/// error and a call site that has to read a code to tell "nothing stored yet"
/// from "the keychain is broken" is a call site that renders the first one as a
/// red banner. This is the same rule `scanQR` follows for cancellation.
///
/// NOTHING HERE LOGS A VALUE, and no rejection message carries one. The
/// failures name an `OSStatus` and stop; see `LolaKeychain.describe`.
extension LolaTransportPlugin {

    /// Store one key under an endpoint id. Replaces silently.
    @objc func secretSet(_ call: CAPPluginCall) {
        guard let account = call.getString("key"), !account.isEmpty else {
            call.reject("key is required", Self.keychainErrorCode)
            return
        }
        guard let value = call.getString("value"), !value.isEmpty else {
            // Deleting on empty is the APP's rule, not this layer's — an empty
            // keychain item reads back as "there is a key, and it is wrong",
            // which is the most confusing state a connect screen can be in — so
            // `secretstore.ts` routes an empty value to `secretDelete` and an
            // empty one arriving here is a bug rather than an intent.
            call.reject("value is required", Self.keychainErrorCode)
            return
        }

        switch LolaKeychain.write(value, account: account) {
        case .ok:
            LolaLog.info("keychain: stored the key for one endpoint")
            call.resolve()
        case .notFound, .duplicate:
            // `write` resolves both internally; reaching here would be a defect
            // in it rather than a keychain condition.
            call.reject(LolaKeychain.describe(errSecInternalError), Self.keychainErrorCode)
        case .failed(let status):
            LolaLog.warn("keychain: store failed (OSStatus \(status))")
            call.reject(LolaKeychain.describe(status), Self.keychainErrorCode)
        }
    }

    /// Read one key back, or `null`.
    @objc func secretGet(_ call: CAPPluginCall) {
        guard let account = call.getString("key"), !account.isEmpty else {
            call.reject("key is required", Self.keychainErrorCode)
            return
        }

        switch LolaKeychain.read(account: account) {
        case .value(let value):
            // The value crosses the bridge; it does not go anywhere else. There
            // is no log line here on purpose, not even a length.
            call.resolve(["value": value])
        case .missing:
            call.resolve(["value": NSNull()])
        case .failed(let status):
            LolaLog.warn("keychain: read failed (OSStatus \(status))")
            call.reject(LolaKeychain.describe(status), Self.keychainErrorCode)
        }
    }

    /// Forget one key. Succeeds when there was nothing to forget.
    @objc func secretDelete(_ call: CAPPluginCall) {
        guard let account = call.getString("key"), !account.isEmpty else {
            call.reject("key is required", Self.keychainErrorCode)
            return
        }

        switch LolaKeychain.delete(account: account) {
        case .ok:
            LolaLog.info("keychain: forgot the key for one endpoint")
            call.resolve()
        case .notFound, .duplicate:
            call.resolve()
        case .failed(let status):
            LolaLog.warn("keychain: delete failed (OSStatus \(status))")
            call.reject(LolaKeychain.describe(status), Self.keychainErrorCode)
        }
    }

    /// The rejection code every keychain failure carries.
    ///
    /// Deliberately NOT a `LolaFailureCode`: that vocabulary describes a socket,
    /// and `diagnose` reads it to decide between "wrong network" and "wrong
    /// key". A storage failure is neither, and borrowing one of those words
    /// would make a locked keychain render as an unreachable daemon.
    static var keychainErrorCode: String { "keychain" }
}
