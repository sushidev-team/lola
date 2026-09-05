import Security
import XCTest

@testable import LolaTransportCore

/// The Keychain half of the bearer key's life.
///
/// It is testable at all because `LolaKeychain` is four `SecItem` calls and a
/// classification, with no Capacitor and no Network framework in any of it —
/// the same rule the rest of `LolaTransportCore` is chosen by. These run
/// against the SIMULATOR's real keychain, which is what makes the
/// `errSecDuplicateItem` path a fact rather than a claim: there is no upsert in
/// the API, `SecItemAdd` reports a duplicate, and the recovery is a
/// `SecItemUpdate` with a differently-shaped pair of dictionaries.
final class LolaKeychainTests: XCTestCase {

    /// A fresh account per test, so a failure cannot poison the next run and two
    /// tests cannot address the same item.
    private var account = ""

    override func setUp() {
        super.setUp()
        account = "test-\(UUID().uuidString):7717"
    }

    override func tearDown() {
        LolaKeychain.delete(account: account)
        super.tearDown()
    }

    /// Skips the round-trip suite where the keychain is not reachable at all.
    ///
    /// A bare xctest bundle has no application identifier of its own, and the
    /// default keychain access group is derived from exactly that — so every
    /// `SecItem` call from this target answers `errSecMissingEntitlement`
    /// (-34018) unless the test target is given an app host. That is a property
    /// of the harness rather than of the code, and it is why the pure
    /// classification tests above are separate: they carry the decisions this
    /// file makes and they run everywhere.
    ///
    /// A skip STATES the gap, and names the status so nobody has to rediscover
    /// which one it was. A suite that silently exercised nothing would be worse
    /// than a red one. To make these run, set an app host on
    /// `LolaTransportCoreTests` in Xcode; the round trip itself is otherwise
    /// only observable from the app on a device or simulator.
    private func requireKeychain() throws {
        let probe = "probe-\(UUID().uuidString)"
        let outcome = LolaKeychain.write("x", account: probe)
        guard case .ok = outcome else {
            if case .failed(let status) = outcome {
                throw XCTSkip(
                    "this test bundle cannot reach the keychain (OSStatus \(status)); "
                        + "it has no application identifier of its own")
            }
            throw XCTSkip("this test bundle cannot reach the keychain")
        }
        LolaKeychain.delete(account: probe)
    }

    // MARK: - The classification, which needs no keychain at all

    func testNotFoundIsAnAnswerRatherThanAFailure() {
        // "There is no key for this daemon" is what a first launch looks like.
        // Folding it into a failure is how a fresh install shows a red banner
        // instead of a connect form.
        XCTAssertEqual(LolaKeychain.classify(errSecItemNotFound), .notFound)
    }

    func testDuplicateIsAnUpdateRatherThanAFailure() {
        // `SecItemAdd` has no upsert; a duplicate is simply how it says the
        // caller meant an update.
        XCTAssertEqual(LolaKeychain.classify(errSecDuplicateItem), .duplicate)
    }

    func testSuccessAndEverythingElse() {
        XCTAssertEqual(LolaKeychain.classify(errSecSuccess), .ok)
        XCTAssertEqual(LolaKeychain.classify(errSecInteractionNotAllowed), .failed(errSecInteractionNotAllowed))
    }

    func testAFailureMessageCarriesANumberAndNothingElse() {
        // It travels to a JavaScript rejection some future call site may
        // render. A message that carries only an OSStatus can never be the
        // thing that leaks a bearer key.
        let text = LolaKeychain.describe(errSecInteractionNotAllowed)
        XCTAssertTrue(text.contains("\(errSecInteractionNotAllowed)"))
        XCTAssertFalse(text.lowercased().contains("key "))
    }

    func testAnEmptyAccountIsRefused() {
        // The account is the app's endpoint id. An empty one means the caller
        // lost track of which daemon it meant, and writing it would file every
        // daemon's key under one item.
        XCTAssertEqual(LolaKeychain.write("secret", account: ""), .failed(errSecParam))
        XCTAssertEqual(LolaKeychain.read(account: ""), .failed(errSecParam))
        XCTAssertEqual(LolaKeychain.delete(account: ""), .failed(errSecParam))
    }

    func testAnEmptyValueIsRefused() {
        // An empty keychain item reads back as "there is a key, and it is
        // wrong", which is the most confusing state a connect screen can be in.
        // The app routes an empty value to `delete`; reaching `write` with one
        // is a defect.
        XCTAssertEqual(LolaKeychain.write("", account: account), .failed(errSecParam))
    }

    // MARK: - The real keychain

    func testAKeySurvivesAndComesBackByteForByte() throws {
        try requireKeychain()
        let key = "0123456789abcdef0123456789abcdef"
        XCTAssertEqual(LolaKeychain.write(key, account: account), .ok)
        XCTAssertEqual(LolaKeychain.read(account: account), .value(key))
    }

    func testWritingTwiceReplacesRatherThanFailing() throws {
        // THE PATH THE WHOLE FILE EXISTS FOR. Re-pairing the same Mac writes an
        // account that already has an item; without the duplicate branch this
        // is `errSecDuplicateItem`, the store reports a failure, and the app
        // quietly keeps the OLD key — so rotating the daemon's key would look
        // like the new one being rejected.
        try requireKeychain()
        XCTAssertEqual(LolaKeychain.write("first-key-aaaaaaaa", account: account), .ok)
        XCTAssertEqual(LolaKeychain.write("second-key-bbbbbbb", account: account), .ok)
        XCTAssertEqual(LolaKeychain.read(account: account), .value("second-key-bbbbbbb"))
    }

    func testReadingAnAccountThatWasNeverWrittenIsMissing() throws {
        try requireKeychain()
        XCTAssertEqual(LolaKeychain.read(account: "absent-\(UUID().uuidString)"), .missing)
    }

    func testDeleteIsIdempotent() throws {
        // "Forget this Mac" has to be safe to run twice, and a second tap must
        // not produce an error about the first one having worked.
        try requireKeychain()
        XCTAssertEqual(LolaKeychain.write("0123456789abcdef", account: account), .ok)
        XCTAssertEqual(LolaKeychain.delete(account: account), .ok)
        XCTAssertEqual(LolaKeychain.delete(account: account), .ok)
        XCTAssertEqual(LolaKeychain.read(account: account), .missing)
    }

    func testTwoDaemonsNeverShareOneKey() throws {
        // The account is `host:port`, so a second Mac is a second item. A shared
        // one would hand the wrong bearer key to whichever daemon was dialled
        // second and render as a refusal nobody could explain.
        try requireKeychain()
        let other = "test-\(UUID().uuidString):7717"
        defer { LolaKeychain.delete(account: other) }

        XCTAssertEqual(LolaKeychain.write("key-for-the-first-mac", account: account), .ok)
        XCTAssertEqual(LolaKeychain.write("key-for-the-second-mac", account: other), .ok)
        XCTAssertEqual(LolaKeychain.read(account: account), .value("key-for-the-first-mac"))
        XCTAssertEqual(LolaKeychain.read(account: other), .value("key-for-the-second-mac"))
    }

    func testTheItemIsReadableAfterFirstUnlockAndNeverSynced() throws {
        // The protection class is the whole security decision here, so it is
        // asserted against what is actually on the item rather than trusted to
        // the constant at the call site. `AfterFirstUnlock` is what lets an
        // unattended reconnect read the key on a locked phone; `ThisDeviceOnly`
        // is what keeps a device-bound credential out of iCloud Keychain and
        // out of an encrypted backup.
        try requireKeychain()
        XCTAssertEqual(LolaKeychain.write("0123456789abcdef", account: account), .ok)

        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: LolaKeychain.service,
            kSecAttrAccount as String: account,
            kSecMatchLimit as String: kSecMatchLimitOne,
            kSecReturnAttributes as String: true,
        ]
        var out: CFTypeRef?
        XCTAssertEqual(SecItemCopyMatching(query as CFDictionary, &out), errSecSuccess)
        let attributes = out as? [String: Any]
        XCTAssertEqual(
            attributes?[kSecAttrAccessible as String] as? String,
            kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String)

        // And the same after a REPLACE, because the update path re-asserts it
        // so that an item written by an older build is upgraded in place.
        XCTAssertEqual(LolaKeychain.write("fedcba9876543210", account: account), .ok)
        out = nil
        query[kSecReturnAttributes as String] = true
        XCTAssertEqual(SecItemCopyMatching(query as CFDictionary, &out), errSecSuccess)
        XCTAssertEqual(
            (out as? [String: Any])?[kSecAttrAccessible as String] as? String,
            kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String)
    }
}
