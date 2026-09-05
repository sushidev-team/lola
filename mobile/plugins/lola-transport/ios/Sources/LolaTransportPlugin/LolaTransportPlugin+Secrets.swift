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
/// A MISSING KEY RESOLVES, IT DOES NOT REJECT. `secretHas` answers
/// `{ has: false }` when there is no item, because a first launch is not an
/// error and a call site that has to read a code to tell "nothing stored yet"
/// from "the keychain is broken" is a call site that renders the first one as a
/// red banner. This is the same rule `scanQR` follows for cancellation.
///
/// -------------------------------------------------------------------------
/// NOTHING HERE EVER RETURNS THE VALUE, AND THAT IS A FIX, NOT A STYLE.
///
/// This file used to expose `secretGet`, resolving `{ value: <the key> }` so
/// the connect store could pass it to `connect`. It could not be made safe by
/// being careful in this file, because the leak is one layer down: Capacitor's
/// bridge logs every resolved payload —
///
///     let resultJson = result.jsonPayload()
///     CAPLog.print("⚡️  TO JS", resultJson.prefix(256))
///
/// — so the bearer key was printed in cleartext to the app's console on every
/// launch of every Debug build. That was verified on a simulator against the
/// daemon's real `~/.lola/remote.key`, not reasoned about. `CAPLog` is off in a
/// Release configuration, but Debug is the only configuration this project
/// builds today, and the line reaches Xcode's console, `cap run ios`, the
/// simulator run script, and anything pasted or screen-shared out of them.
///
/// So the read moved to the side that needs it: `connect` takes a `keyRef` (an
/// endpoint id — an address, not a secret) and reads the Keychain in Swift.
/// What is left here is the question the WebView actually has, which is whether
/// a key exists at all. A boolean cannot leak a credential no matter what logs
/// it.
/// -------------------------------------------------------------------------
///
/// NOTHING HERE LOGS A VALUE either, and no rejection message carries one. The
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

    /// Whether a key is stored for one endpoint. Never the key itself.
    ///
    /// A read failure answers `false` rather than rejecting. The caller's only
    /// use for this is deciding whether to offer a reconnect or a form, and a
    /// keychain that cannot be read is, for that purpose, one with nothing in
    /// it — while a rejection would put a red banner in front of a user whose
    /// remedy is to type the key they were going to have to type anyway. The
    /// `OSStatus` is logged so the difference is still diagnosable.
    @objc func secretHas(_ call: CAPPluginCall) {
        guard let account = call.getString("key"), !account.isEmpty else {
            call.reject("key is required", Self.keychainErrorCode)
            return
        }

        switch LolaKeychain.read(account: account) {
        case .value(let value):
            // The BOOLEAN crosses the bridge. The value does not, and must not:
            // see the note at the top of this file.
            call.resolve(["has": !value.isEmpty])
        case .missing:
            call.resolve(["has": false])
        case .failed(let status):
            LolaLog.warn("keychain: read failed (OSStatus \(status))")
            call.resolve(["has": false])
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
