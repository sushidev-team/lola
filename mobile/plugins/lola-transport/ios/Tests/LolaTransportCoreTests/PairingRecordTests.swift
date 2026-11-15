import XCTest

@testable import LolaTransportCore

final class PairingRecordTests: XCTestCase {
    private func sample() -> PairingRecord {
        PairingRecord(
            daemonId: "d-1", name: "Studio", pin: "pin=", deviceId: "dev-1",
            addrs: ["192.168.1.20", "fd00::20"], port: 47821, lastGood: "192.168.1.20:47821",
            relayURL: "https://hub.example.com", relayState: .backoff,
            relayRetryAt: Date(timeIntervalSince1970: 1_800_000_060),
            pairedAt: Date(timeIntervalSince1970: 1_800_000_000), secureEnclave: true)
    }

    func testCodableRoundTrip() throws {
        let r = sample()
        XCTAssertEqual(try PairingRecord.decode(r.encoded()), r)
        XCTAssertEqual(r.relay, .backoff)
    }

    func testARecordOfAnotherVersionIsRefused() throws {
        var json = try JSONSerialization.jsonObject(with: sample().encoded()) as! [String: Any]
        json["v"] = 1
        let old = try JSONSerialization.data(withJSONObject: json)
        XCTAssertThrowsError(try PairingRecord.decode(old)) {
            XCTAssertEqual($0 as? PairingRecord.Failure, .unsupportedVersion(1))
        }
        XCTAssertThrowsError(try PairingRecord.decode(Data("garbage".utf8)))
    }

    func testNoFieldHoldsASecret() throws {
        let keys = Set((try JSONSerialization.jsonObject(with: sample().encoded()) as! [String: Any]).keys)
        XCTAssertEqual(
            keys,
            ["v", "daemonId", "name", "pin", "deviceId", "addrs", "port", "lastGood", "relayURL", "relayState", "relayRetryAt", "pairedAt", "secureEnclave"])
        for key in keys { XCTAssertFalse(key.lowercased().contains("bearer") || key.lowercased().contains("secret") || key == "tok", key) }
    }

    func testAnUnknownRelayStateReadsAsNone() throws {
        var r = sample()
        r.relayState = "from-the-future"
        XCTAssertEqual(r.relay, RelayState.none)
    }

    func testKeychainRoundTrip() throws {
        var r = sample()
        r.daemonId = "test-\(UUID().uuidString)"
        defer { try? PairingRecord.delete(daemonId: r.daemonId) }
        #if os(macOS)
        throw XCTSkip("a host test process has no keychain-access-group entitlement (errSecMissingEntitlement, -34018)")
        #endif
        try r.save()
        XCTAssertEqual(try PairingRecord.load(daemonId: r.daemonId), r)
        try PairingRecord.delete(daemonId: r.daemonId)
        XCTAssertNil(try PairingRecord.load(daemonId: r.daemonId))
    }
}
