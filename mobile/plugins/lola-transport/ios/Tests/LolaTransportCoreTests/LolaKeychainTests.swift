import Security
import XCTest

@testable import LolaTransportCore

/// A bare xctest bundle on a macOS host has no application identifier, so the
/// default keychain access group does not exist and every `SecItem` call
/// answers `errSecMissingEntitlement` (-34018). The round-trip tests skip with
/// that status named; the classification tests carry the decisions and run
/// everywhere.
final class LolaKeychainTests: XCTestCase {
    private let service = "dev.sushi.lola.mobile.test"
    private var account = ""

    override func setUp() {
        super.setUp()
        account = "test-\(UUID().uuidString)"
    }

    override func tearDown() {
        LolaKeychain.delete(service: service, account: account)
        super.tearDown()
    }

    private func requireKeychain() throws {
        #if os(macOS)
        throw XCTSkip("a host test process has no keychain-access-group entitlement (errSecMissingEntitlement, -34018)")
        #endif
        let probe = "probe-\(UUID().uuidString)"
        let outcome = LolaKeychain.write(Data("x".utf8), service: service, account: probe)
        guard case .ok = outcome else { return XCTFail("the simulator keychain must be reachable: \(outcome)") }
        LolaKeychain.delete(service: service, account: probe)
    }

    // MARK: Classification

    func testNotFoundAndDuplicateAreAnswersNotFailures() {
        XCTAssertEqual(LolaKeychain.classify(errSecItemNotFound), .notFound)
        XCTAssertEqual(LolaKeychain.classify(errSecDuplicateItem), .duplicate)
        XCTAssertEqual(LolaKeychain.classify(errSecSuccess), .ok)
        XCTAssertEqual(LolaKeychain.classify(errSecInteractionNotAllowed), .failed(errSecInteractionNotAllowed))
    }

    func testAFailureMessageCarriesANumberAndNothingElse() {
        let text = LolaKeychain.describe(errSecInteractionNotAllowed)
        XCTAssertTrue(text.contains("\(errSecInteractionNotAllowed)"))
    }

    func testEmptyServiceAccountOrValueIsRefused() {
        XCTAssertEqual(LolaKeychain.write(Data("v".utf8), service: service, account: ""), .failed(errSecParam))
        XCTAssertEqual(LolaKeychain.write(Data("v".utf8), service: "", account: account), .failed(errSecParam))
        XCTAssertEqual(LolaKeychain.write(Data(), service: service, account: account), .failed(errSecParam))
        XCTAssertEqual(LolaKeychain.read(service: service, account: ""), .failed(errSecParam))
        XCTAssertEqual(LolaKeychain.delete(service: service, account: ""), .failed(errSecParam))
    }

    // MARK: Real keychain

    func testAValueComesBackByteForByte() throws {
        try requireKeychain()
        let value = Data([0, 1, 2, 255, 254])
        XCTAssertEqual(LolaKeychain.write(value, service: service, account: account), .ok)
        XCTAssertEqual(LolaKeychain.read(service: service, account: account), .value(value))
    }

    func testWritingTwiceReplacesRatherThanFailing() throws {
        try requireKeychain()
        XCTAssertEqual(LolaKeychain.write(Data("first".utf8), service: service, account: account), .ok)
        XCTAssertEqual(LolaKeychain.write(Data("second".utf8), service: service, account: account), .ok)
        XCTAssertEqual(LolaKeychain.read(service: service, account: account), .value(Data("second".utf8)))
    }

    func testMissingIsAnAnswerAndDeleteIsIdempotent() throws {
        try requireKeychain()
        XCTAssertEqual(LolaKeychain.read(service: service, account: account), .missing)
        XCTAssertEqual(LolaKeychain.write(Data("v".utf8), service: service, account: account), .ok)
        XCTAssertEqual(LolaKeychain.delete(service: service, account: account), .ok)
        XCTAssertEqual(LolaKeychain.delete(service: service, account: account), .ok)
        XCTAssertEqual(LolaKeychain.read(service: service, account: account), .missing)
    }

    func testServicesDoNotShareAnAccount() throws {
        try requireKeychain()
        let other = "dev.sushi.lola.mobile.test-other"
        defer { LolaKeychain.delete(service: other, account: account) }
        XCTAssertEqual(LolaKeychain.write(Data("a".utf8), service: service, account: account), .ok)
        XCTAssertEqual(LolaKeychain.write(Data("b".utf8), service: other, account: account), .ok)
        XCTAssertEqual(LolaKeychain.read(service: service, account: account), .value(Data("a".utf8)))
        XCTAssertEqual(LolaKeychain.read(service: other, account: account), .value(Data("b".utf8)))
    }

    func testTheItemIsAfterFirstUnlockThisDeviceOnlyOnCreateAndReplace() throws {
        try requireKeychain()
        func accessible() -> String? {
            let query: [String: Any] = [
                kSecClass as String: kSecClassGenericPassword,
                kSecAttrService as String: service,
                kSecAttrAccount as String: account,
                kSecMatchLimit as String: kSecMatchLimitOne,
                kSecReturnAttributes as String: true,
            ]
            var out: CFTypeRef?
            XCTAssertEqual(SecItemCopyMatching(query as CFDictionary, &out), errSecSuccess)
            return (out as? [String: Any])?[kSecAttrAccessible as String] as? String
        }
        let expected = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String
        XCTAssertEqual(LolaKeychain.write(Data("one".utf8), service: service, account: account), .ok)
        XCTAssertEqual(accessible(), expected)
        XCTAssertEqual(LolaKeychain.write(Data("two".utf8), service: service, account: account), .ok)
        XCTAssertEqual(accessible(), expected)
    }
}
