import XCTest

@testable import LolaTransportCore

/// The bridge payload's shape.
///
/// These are the exact keys `LolaStateEvent` declares in `src/definitions.ts`.
/// Nothing in Swift or TypeScript checks the other, so this test is the seam:
/// a renamed key here has to be renamed there, and a payload that starts
/// sending nulls where the TypeScript declares optionals turns every `if
/// (event.code)` in the app into a truthiness bug.
final class ConnectionStateTests: XCTestCase {

    func testAMinimalEventCarriesOnlyEpochAndPhase() {
        let payload = LolaConnectionEvent(epoch: 3, phase: .connecting).payload()
        XCTAssertEqual(payload.count, 2)
        XCTAssertEqual(payload["epoch"] as? Int, 3)
        XCTAssertEqual(payload["phase"] as? String, "connecting")
    }

    func testAbsentFieldsAreOmittedRatherThanSentAsNull() {
        // A JavaScript optional means "the key is not there". Sending null
        // instead would still be falsy, but it would also make every payload
        // the same size and hide which fields the plugin actually knows.
        let payload = LolaConnectionEvent(
            epoch: 1, phase: .failed, code: .network, reason: "unreachable"
        ).payload()
        XCTAssertNil(payload["spkiPin"])
        XCTAssertNil(payload["pinned"])
        XCTAssertNil(payload["daemonCode"])
        XCTAssertNil(payload["minV"])
        XCTAssertNil(payload["maxV"])
    }

    func testAConnectedEventReportsThePinAndWhetherItWasChecked() {
        let payload = LolaConnectionEvent(
            epoch: 7, phase: .connected, spkiPin: "abc=", pinned: false
        ).payload()
        XCTAssertEqual(payload["spkiPin"] as? String, "abc=")
        XCTAssertEqual(payload["pinned"] as? Bool, false)
        // The observed pin is reported whether or not it was verified: on an
        // unpinned first connection it is the value a human is about to write
        // down, and on a pinned one it is what makes the connection auditable.
    }

    func testAVersionRefusalCarriesBothBounds() {
        let payload = LolaConnectionEvent(
            epoch: 2,
            phase: .failed,
            code: .protocolViolation,
            reason: "refused",
            daemonCode: "unsupported_version",
            minV: 1,
            maxV: 1
        ).payload()
        XCTAssertEqual(payload["code"] as? String, "protocol")
        XCTAssertEqual(payload["daemonCode"] as? String, "unsupported_version")
        XCTAssertEqual(payload["minV"] as? Int, 1)
        XCTAssertEqual(payload["maxV"] as? Int, 1)
    }

    func testTheWireStringsAreTheOnesTypeScriptDeclares() {
        // `protocol` and `pin_mismatch` are the two that cannot be spelled the
        // same in both languages: one is a Swift keyword and the other is
        // camelCase on this side and snake_case on the wire.
        XCTAssertEqual(LolaFailureCode.protocolViolation.rawValue, "protocol")
        XCTAssertEqual(LolaFailureCode.pinMismatch.rawValue, "pin_mismatch")
        XCTAssertEqual(LolaFailureCode.peerClosed.rawValue, "peer_closed")
        XCTAssertEqual(LolaFailureCode.clientClosed.rawValue, "client_closed")
        XCTAssertEqual(LolaFailureCode.internalError.rawValue, "internal")
        XCTAssertEqual(LolaFailureCode.network.rawValue, "network")
        XCTAssertEqual(LolaFailureCode.timeout.rawValue, "timeout")
        XCTAssertEqual(LolaFailureCode.tls.rawValue, "tls")
        XCTAssertEqual(LolaFailureCode.rejected.rawValue, "rejected")
        XCTAssertEqual(LolaFailureCode.backgrounded.rawValue, "backgrounded")

        XCTAssertEqual(LolaConnectionPhase.connecting.rawValue, "connecting")
        XCTAssertEqual(LolaConnectionPhase.handshaking.rawValue, "handshaking")
        XCTAssertEqual(LolaConnectionPhase.connected.rawValue, "connected")
        XCTAssertEqual(LolaConnectionPhase.failed.rawValue, "failed")
        XCTAssertEqual(LolaConnectionPhase.closed.rawValue, "closed")
    }
}
