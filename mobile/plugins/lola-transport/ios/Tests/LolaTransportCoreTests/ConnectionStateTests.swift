import XCTest

@testable import LolaTransportCore

/// The bridge payload's shape: the exact keys `StateEvent` declares in
/// `src/definitions.ts`. Nothing checks the other side, so a renamed key here
/// must be renamed there.
final class ConnectionStateTests: XCTestCase {

    func testAMinimalEventCarriesOnlyEpochAndPhase() {
        let payload = ConnectionState(epoch: 3, phase: .racing).payload()
        XCTAssertEqual(payload.count, 2)
        XCTAssertEqual(payload["epoch"] as? Int, 3)
        XCTAssertEqual(payload["phase"] as? String, "racing")
    }

    func testAbsentFieldsAreOmittedRatherThanSentAsNull() {
        let payload = ConnectionState(epoch: 1, phase: .failed, code: .unreachable).payload()
        for key in ["path", "daemonCode", "minV", "maxV", "raceMs", "relay"] { XCTAssertNil(payload[key], key) }
    }

    func testAReadyEventCarriesPathRaceTimeAndRelayState() {
        let payload = ConnectionState(
            epoch: 7, phase: .ready,
            path: PathInfo(kind: .relay, host: "hub.example.com", port: 443, interface: .cellular),
            raceMs: 812, relay: .ok
        ).payload()
        let path = payload["path"] as? [String: Any]
        XCTAssertEqual(path?["kind"] as? String, "relay")
        XCTAssertEqual(path?["host"] as? String, "hub.example.com")
        XCTAssertEqual(path?["port"] as? Int, 443)
        XCTAssertEqual(path?["interface"] as? String, "cellular")
        XCTAssertEqual(payload["raceMs"] as? Int, 812)
        XCTAssertEqual(payload["relay"] as? String, "ok")
    }

    func testAVersionRefusalCarriesBothBounds() {
        let payload = ConnectionState(
            epoch: 2, phase: .failed, code: .unsupportedVersion, daemonCode: "unsupported_version", minV: 2, maxV: 2
        ).payload()
        XCTAssertEqual(payload["code"] as? String, "unsupported_version")
        XCTAssertEqual(payload["daemonCode"] as? String, "unsupported_version")
        XCTAssertEqual(payload["minV"] as? Int, 2)
        XCTAssertEqual(payload["maxV"] as? Int, 2)
    }

    func testTheWireStringsAreTheOnesTypeScriptDeclares() {
        let codes: [FailureCode: String] = [
            .offline: "offline", .unreachable: "unreachable", .timeout: "timeout", .keyChanged: "key_changed",
            .notPaired: "not_paired", .unsupportedVersion: "unsupported_version",
            .relayUnavailable: "relay_unavailable", .protocolViolation: "protocol", .peerClosed: "peer_closed",
            .clientClosed: "client_closed", .backgrounded: "backgrounded", .internalError: "internal",
        ]
        for (code, raw) in codes { XCTAssertEqual(code.rawValue, raw) }
        XCTAssertEqual(
            [ConnectionPhase.idle, .racing, .ready, .migrating, .closed, .failed].map(\.rawValue),
            ["idle", "racing", "ready", "migrating", "closed", "failed"])
        XCTAssertEqual(
            [RelayState.none, .ok, .backoff, .credentialDead, .hubTooOld].map(\.rawValue),
            ["none", "ok", "backoff", "credential_dead", "hub_too_old"])
        XCTAssertEqual([PathKind.direct, .relay].map(\.rawValue), ["direct", "relay"])
    }

    func testOnlyTheDaemonsRefusalsAreTerminal() {
        let terminal = Set([FailureCode.notPaired, .unsupportedVersion, .protocolViolation, .clientClosed, .internalError])
        for code in [FailureCode.offline, .unreachable, .timeout, .keyChanged, .relayUnavailable, .peerClosed, .backgrounded] {
            XCTAssertTrue(code.isRetryable, code.rawValue)
        }
        for code in terminal { XCTAssertFalse(code.isRetryable, code.rawValue) }
    }
}
