import Foundation
import Security

/// The bearer key's one durable home on the device.
///
/// WHY THIS IS IN `LolaTransportCore` and not beside the socket. The rule this
/// package splits on is "everything a test can exercise without a device", and
/// the Keychain qualifies: `SecItem*` is four C functions over a dictionary,
/// there is no Capacitor and no Network framework in any of it, and the
/// simulator has a real keychain a test can round-trip against. The bridge half
/// — turning a `CAPPluginCall` into one of these calls — is what lives in the
/// plugin target, and it is three lines per method precisely because the
/// decisions are all here.
///
/// WHAT IS STORED, AND WHAT IS NOT. One generic-password item per endpoint,
/// holding the M1 bearer key and nothing else. The host, the port and the SPKI
/// pin stay in the WebView's `localStorage`, because they are public — the
/// daemon prints the pin in its own startup log and the pin is a hash of a
/// public key — and keeping them out of here keeps the secret store to exactly
/// one item per daemon, which is what makes "forget this Mac" a single delete.
///
/// THE UNIQUENESS KEY is `kSecAttrService` + `kSecAttrAccount` + the access
/// group + `kSecAttrSynchronizable`. `kSecAttrGeneric` is NOT part of it, which
/// is the classic way to end up with two items that cannot both be addressed.
/// The account is the app's own endpoint id (`host:port`), so two daemons never
/// collide and neither ever sees the other's key.
///
/// NO ACCESS GROUP IS SET. Omitting `kSecAttrAccessGroup` uses the app's default
/// group, derived from its `application-identifier`, which needs no entitlements
/// file — and there is none in `ios/App`. Naming a group would require one, and
/// would buy sharing with an app extension this project does not have.
///
/// NOTHING HERE EVER LOGS, RETURNS OR EMBEDS THE VALUE. The failure messages
/// carry an `OSStatus` and a word; that is the whole vocabulary, for the same
/// reason `LolaLog` has the one it has.
public enum LolaKeychain {

    /// The service every item is filed under. It is the bundle id plus a
    /// qualifier rather than the bare bundle id, so that a second kind of
    /// secret — should one ever exist — is a different service rather than a
    /// clash inside this one.
    public static let service = "dev.sushi.lola.mobile.remote"

    /// The protection class, and the reason it is this one rather than a
    /// stricter one.
    ///
    /// `AfterFirstUnlock` so that a read can succeed while the screen is
    /// locked. The app reconnects when it returns to the foreground, and from
    /// M5 a push may wake it — a `WhenUnlocked` item fails both of those and
    /// buys protection that is only meaningful against an attacker who has the
    /// phone in a state where the bearer key is the smaller of the user's
    /// problems.
    ///
    /// `ThisDeviceOnly` so a device-bound credential never reaches iCloud
    /// Keychain, an encrypted backup, or a restore onto a new phone. Migrating
    /// to a new device therefore always re-pairs, which PLAN.md already treats
    /// as a guaranteed flow.
    ///
    /// DELIBERATELY NOT `kSecAttrAccessControl` with `.userPresence` or
    /// `.biometryCurrentSet`. Those are mutually exclusive with
    /// `kSecAttrAccessible` and make every read block on Face ID — including
    /// the unattended reconnect this item exists to serve. A gated key breaks
    /// reconnect, users turn pairing off, and the feature dies; PLAN.md settles
    /// this the same way for M2's device key.
    ///
    /// The cost, stated plainly: between the first unlock after boot and the
    /// next reboot the item is readable by anything with code execution inside
    /// this app's container. The mitigation for that is daemon-side — rotate
    /// the key — not a storage class.
    private static let accessible = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly

    /// What a Keychain call decided.
    ///
    /// `notFound` and `duplicate` are named because neither is an error. "There
    /// is no key for this daemon" is an ANSWER — it is what a first launch
    /// looks like — and `errSecDuplicateItem` is simply how `SecItemAdd`
    /// reports that the caller meant an update. Folding either into a failure
    /// is how a fresh install ends up showing a red banner.
    public enum Status: Equatable {
        case ok
        case notFound
        case duplicate
        case failed(OSStatus)
    }

    /// One read. `missing` is an answer, not a failure; see `Status`.
    public enum Read: Equatable {
        case value(String)
        case missing
        case failed(OSStatus)
    }

    /// The raw `OSStatus` vocabulary, reduced to the three outcomes callers
    /// branch on. Pure, so the branching is pinned by a test rather than by
    /// whichever error a simulator happened to produce on the day.
    public static func classify(_ raw: OSStatus) -> Status {
        switch raw {
        case errSecSuccess: return .ok
        case errSecItemNotFound: return .notFound
        case errSecDuplicateItem: return .duplicate
        default: return .failed(raw)
        }
    }

    /// A human line for a failure.
    ///
    /// It names the numeric `OSStatus` and nothing else. Not the value, and not
    /// the account either — the account is an address rather than a secret, but
    /// this string travels to a JavaScript rejection that some future call site
    /// may well render, and a message that carries only a number can never be
    /// the thing that leaks.
    public static func describe(_ status: OSStatus) -> String {
        "the keychain refused the operation (OSStatus \(status))"
    }

    /// Store one key, creating or replacing.
    ///
    /// `SecItemAdd` first, then `SecItemUpdate` on `errSecDuplicateItem`, which
    /// is the only correct order: there is no upsert, and leading with an
    /// update fails on a first launch. The two dictionaries an update takes are
    /// NOT interchangeable — the query carries no `kSecValueData` and no return
    /// keys, the attributes carry no `kSecClass` — and getting that wrong is
    /// `errSecParam` rather than anything descriptive.
    ///
    /// `kSecAttrAccessible` is re-asserted on the update path so an item written
    /// by an older build is upgraded in place rather than keeping whatever
    /// protection class it was created with.
    @discardableResult
    public static func write(_ value: String, account: String) -> Status {
        guard !account.isEmpty, !value.isEmpty else { return .failed(errSecParam) }
        let data = Data(value.utf8)

        var add = query(account)
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = accessible

        switch classify(SecItemAdd(add as CFDictionary, nil)) {
        case .ok:
            return .ok
        case .duplicate:
            let attributes: [String: Any] = [
                kSecValueData as String: data,
                kSecAttrAccessible as String: accessible,
            ]
            let updated = SecItemUpdate(query(account) as CFDictionary, attributes as CFDictionary)
            return classify(updated) == .ok ? .ok : .failed(updated)
        case .notFound:
            // `SecItemAdd` has no reason to report this; treat it as the
            // failure it would be rather than silently reporting success.
            return .failed(errSecItemNotFound)
        case .failed(let raw):
            return .failed(raw)
        }
    }

    /// Read one key back.
    ///
    /// Data that will not decode as UTF-8 is reported as a FAILURE rather than
    /// as `missing`, even though both end in the same place for the caller (an
    /// empty key, and a connect screen). A corrupt item is a fact worth being
    /// able to see; "there is nothing here" is a different sentence.
    public static func read(account: String) -> Read {
        guard !account.isEmpty else { return .failed(errSecParam) }
        var q = query(account)
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        q[kSecReturnData as String] = true

        var out: CFTypeRef?
        let raw = SecItemCopyMatching(q as CFDictionary, &out)
        switch classify(raw) {
        case .ok:
            guard let data = out as? Data, let text = String(data: data, encoding: .utf8) else {
                return .failed(errSecDecode)
            }
            return .value(text)
        case .notFound:
            return .missing
        case .duplicate, .failed:
            return .failed(raw)
        }
    }

    /// Remove one key. IDEMPOTENT: deleting what is not there succeeds, because
    /// "forget this Mac" has to be safe to run twice and a second tap must not
    /// produce an error about the first one having worked.
    @discardableResult
    public static func delete(account: String) -> Status {
        guard !account.isEmpty else { return .failed(errSecParam) }
        let raw = SecItemDelete(query(account) as CFDictionary)
        switch classify(raw) {
        case .ok, .notFound: return .ok
        case .duplicate, .failed: return .failed(raw)
        }
    }

    /// The primary key, and nothing else. Every call builds its dictionary from
    /// this so the four operations cannot drift into addressing different items.
    private static func query(_ account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }
}
