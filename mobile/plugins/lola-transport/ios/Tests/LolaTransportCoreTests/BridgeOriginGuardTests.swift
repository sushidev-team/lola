import XCTest

@testable import LolaTransportCore

/// The §1.9 admit table: only the top frame of the app's own origin passes.
final class BridgeOriginGuardTests: XCTestCase {
    private let release: BridgeOriginGuard.Origin = ("capacitor", "localhost", 0)
    private let dev: BridgeOriginGuard.Origin = ("http", "192.168.1.20", 9246)

    private func admit(_ main: Bool, _ scheme: String, _ host: String, _ port: Int,
                       app: BridgeOriginGuard.Origin) -> Bool {
        BridgeOriginGuard.admit(isMainFrame: main, scheme: scheme, host: host, port: port, app: app)
    }

    func testMainFrameOfAppOriginIsAdmitted() {
        XCTAssertTrue(admit(true, "capacitor", "localhost", 0, app: release))
        XCTAssertTrue(admit(true, "http", "192.168.1.20", 9246, app: dev))
    }

    func testSubframeIsDroppedEvenFromAppOrigin() {
        XCTAssertFalse(admit(false, "capacitor", "localhost", 0, app: release))
        XCTAssertFalse(admit(false, "http", "192.168.1.20", 9246, app: dev))
    }

    func testOpaqueDataOriginIsDropped() {
        XCTAssertFalse(admit(true, "data", "", 0, app: release))
        XCTAssertFalse(admit(true, "", "", 0, app: release))
        XCTAssertFalse(admit(false, "data", "", 0, app: release))
    }

    func testForeignOriginsAreDropped() {
        XCTAssertFalse(admit(true, "https", "example.com", 443, app: release))
        XCTAssertFalse(admit(true, "capacitor", "evil", 0, app: release))
        XCTAssertFalse(admit(true, "http", "localhost", 0, app: release))
        XCTAssertFalse(admit(true, "http", "192.168.1.20", 9247, app: dev))
        XCTAssertFalse(admit(true, "https", "192.168.1.20", 9246, app: dev))
        XCTAssertFalse(admit(true, "http", "192.168.1.21", 9246, app: dev))
    }

    func testSchemeAndHostCompareCaseInsensitively() {
        XCTAssertTrue(admit(true, "Capacitor", "LocalHost", 0, app: release))
    }

    func testDefaultPortEqualsNoPort() {
        let plain: BridgeOriginGuard.Origin = ("https", "app.example", 443)
        XCTAssertTrue(admit(true, "https", "app.example", 0, app: plain))
        let none: BridgeOriginGuard.Origin = ("http", "app.example", 0)
        XCTAssertTrue(admit(true, "http", "app.example", 80, app: none))
        XCTAssertFalse(admit(true, "http", "app.example", 443, app: none))
    }
}
