import XCTest

@testable import LolaTransportCore

final class RelayCredentialsTests: XCTestCase {
    private let stored = Date(timeIntervalSince1970: 1_800_000_000)

    private func sample(_ bearer: String = "bearer-secret-value") -> RelayCredentials {
        RelayCredentials(hubURL: "https://hub.example.com", rv: "0123456789abcdef0123456789abcdef", bearer: bearer, storedAt: stored)
    }

    func testCodableRoundTripKeepsEveryField() throws {
        let c = sample()
        XCTAssertEqual(try RelayCredentials.decode(c.encoded()), c)
    }

    func testHoldsIsTheHexSHA256OfTheBearer() {
        // sha256("abc")
        XCTAssertEqual(
            sample("abc").holds,
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
    }

    func testTheBearerNeverAppearsInADescription() {
        XCTAssertFalse("\(sample())".contains("bearer-secret-value"))
        XCTAssertFalse(String(reflecting: sample()).contains("bearer-secret-value"))
    }

    func testAnUnreadableItemIsAnErrorNotAMissingCredential() {
        XCTAssertThrowsError(try RelayCredentials.decode(Data("{}".utf8)))
    }

    func testKeychainRoundTripIsKeyedByDaemonId() throws {
        let a = "test-\(UUID().uuidString)", b = "test-\(UUID().uuidString)"
        defer {
            try? RelayCredentials.delete(daemonId: a)
            try? RelayCredentials.delete(daemonId: b)
        }
        #if os(macOS)
        throw XCTSkip("a host test process has no keychain-access-group entitlement (errSecMissingEntitlement, -34018)")
        #endif
        try sample("one").save(daemonId: a)
        try sample("two").save(daemonId: b)
        XCTAssertEqual(try RelayCredentials.load(daemonId: a)?.bearer, "one")
        XCTAssertEqual(try RelayCredentials.load(daemonId: b)?.bearer, "two")
        try RelayCredentials.delete(daemonId: a)
        try RelayCredentials.delete(daemonId: a)
        XCTAssertNil(try RelayCredentials.load(daemonId: a))
        XCTAssertEqual(try RelayCredentials.load(daemonId: b)?.bearer, "two")
    }
}
