import XCTest

@testable import LolaTransportCore

final class ReconnectPolicyTests: XCTestCase {
    func testTheConnectLadder() {
        var p = ReconnectPolicy()
        XCTAssertEqual((0..<8).map { _ in p.nextConnectDelayMs() }, [0, 1000, 2000, 4000, 8000, 15000, 15000, 15000])
    }

    func testTheRelayLadder() {
        var p = ReconnectPolicy()
        XCTAssertEqual((0..<8).map { _ in p.nextRelayDelayMs() }, [2000, 4000, 8000, 16000, 32000, 60000, 60000, 60000])
    }

    func testTheLaddersAreIndependentAndResetSeparately() {
        var p = ReconnectPolicy()
        _ = p.nextConnectDelayMs(); _ = p.nextConnectDelayMs()
        XCTAssertEqual(p.nextRelayDelayMs(), 2000)
        p.resetConnect()
        XCTAssertEqual(p.nextConnectDelayMs(), 0)
        XCTAssertEqual(p.nextRelayDelayMs(), 4000)
        p.resetRelay()
        XCTAssertEqual(p.nextRelayDelayMs(), 2000)
        p.reset()
        XCTAssertEqual(p.nextConnectDelayMs(), 0)
        XCTAssertEqual(p.nextRelayDelayMs(), 2000)
    }

    func testARetryAfterFloorRaisesADelayButNeverLowersOne() {
        var p = ReconnectPolicy()
        XCTAssertEqual(p.nextRelayDelayMs(floorMs: 10_000), 10_000)
        XCTAssertEqual(p.nextRelayDelayMs(floorMs: 1000), 4000)
        XCTAssertEqual(p.nextConnectDelayMs(floorMs: 500), 500)
    }

    func testRelayDelayByFailureCount() {
        XCTAssertEqual([0, 1, 2, 6, 7, 99].map(ReconnectPolicy.relayDelayMs(failures:)), [2000, 2000, 4000, 60000, 60000, 60000])
    }
}
