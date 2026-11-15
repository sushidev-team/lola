import XCTest

@testable import LolaTransportCore

final class InboundFilterTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)
    private let stored = RelayCredentials(
        hubURL: "https://hub.example.com", rv: "0123456789abcdef0123456789abcdef", bearer: "old-bearer",
        storedAt: Date(timeIntervalSince1970: 1_700_000_000))

    private func data(_ s: String) -> Data { Data(s.utf8) }

    private let readyFrame = "{\"v\":2,\"type\":\"ready\",\"payload\":{\"deviceId\":\"dd3f\",\"name\":\"Dev iPhone\",\"tiers\":[\"read\",\"answer\"],\"via\":\"lan\",\"host\":\"studio.local\"}}"
    private let eventFrame = "{\"v\":2,\"type\":\"event\",\"id\":\"w1\",\"topic\":\"talk:x\",\"seq\":1,\"payload\":{}}"

    private func reply(_ n: Int, _ body: String) -> Data {
        data("{\"v\":2,\"type\":\"resp\",\"id\":\"lola-relaycred-\(n)\",\"payload\":{\"ok\":true,\"data\":\(body)}}")
    }

    private func sent(_ out: InboundFilter.Output) -> [[String: Any]] {
        out.effects.compactMap { if case .send(let d) = $0 { return (try? JSONSerialization.jsonObject(with: d)) as? [String: Any] } else { return nil } }
    }

    func testReadyBecomesADeviceNotificationAndTheFirstRelayCredRequest() throws {
        var f = InboundFilter(stored: stored)
        let out = f.filter([data(readyFrame)], now: now)
        XCTAssertEqual(out.frames, [])
        guard case .device(let info) = out.effects[0] else { return XCTFail() }
        XCTAssertEqual(info.deviceId, "dd3f")
        XCTAssertEqual(info.tiers, ["read", "answer"])
        XCTAssertEqual(info.via, "lan")
        let req = sent(out)
        XCTAssertEqual(req.count, 1)
        XCTAssertEqual(req[0]["id"] as? String, "lola-relaycred-1")
        XCTAssertEqual(req[0]["cmd"] as? String, "relayCred")
        let args = (req[0]["payload"] as? [String: Any])?["args"] as? [String: Any]
        XCTAssertEqual(args?["holds"] as? String, stored.holds)
    }

    func testTheRequestOmitsHoldsWithNoStoredBearerOrAfterACredentialDeath() {
        for filter in [InboundFilter(stored: nil), InboundFilter(stored: stored, credentialDead: true)] {
            var f = filter
            let args = ((sent(f.filter([data(readyFrame)], now: now))[0]["payload"] as? [String: Any])?["args"]) as? [String: Any]
            XCTAssertEqual(args?.count, 0)
        }
    }

    func testABatchWithAReadyACredentialReplyAndAnEventKeepsOnlyTheEvent() throws {
        var f = InboundFilter(stored: stored)
        _ = f.filter([data(readyFrame)], now: now)
        let out = f.filter(
            [data(readyFrame.replacingOccurrences(of: "\"ready\"", with: "\"ready\"")), reply(1, "{\"available\":true,\"url\":\"https://hub.example.com\",\"rv\":\"\(stored.rv)\",\"bearer\":\"new-bearer\"}"), data(eventFrame)],
            now: now)
        XCTAssertEqual(out.frames, [data(eventFrame)])

        let saves = out.effects.compactMap { e -> RelayCredentials? in if case .saveCredentials(let c) = e { return c } else { return nil } }
        XCTAssertEqual(saves.count, 1)
        XCTAssertEqual(saves[0].bearer, "new-bearer")
        XCTAssertEqual(saves[0].storedAt, now)

        let devices = out.effects.filter { if case .device = $0 { return true } else { return false } }
        XCTAssertEqual(devices.count, 0, "a second ready on one session is dropped")

        let acks = sent(out)
        XCTAssertEqual(acks.count, 1)
        XCTAssertEqual(acks[0]["id"] as? String, "lola-relaycred-2")
        let args = (acks[0]["payload"] as? [String: Any])?["args"] as? [String: Any]
        XCTAssertEqual(args?["holds"] as? String, saves[0].holds)
        XCTAssertEqual(f.stored?.bearer, "new-bearer")
    }

    func testAnUnavailableReplyKeepsTheStoredRecord() {
        var f = InboundFilter(stored: stored)
        _ = f.filter([data(readyFrame)], now: now)
        let out = f.filter([reply(1, "{\"available\":false}")], now: now)
        XCTAssertEqual(out, InboundFilter.Output())
        XCTAssertEqual(f.stored, stored)
    }

    func testAChangedURLOrRVReplacesTheRecordButKeepsTheBearerAndItsAge() {
        var f = InboundFilter(stored: stored)
        _ = f.filter([data(readyFrame)], now: now)
        let out = f.filter([reply(1, "{\"available\":true,\"url\":\"https://hub2.example.com\",\"rv\":\"\(stored.rv)\"}")], now: now)
        XCTAssertEqual(sent(out).count, 0)
        guard case .saveCredentials(let saved) = out.effects[0] else { return XCTFail() }
        XCTAssertEqual(saved.hubURL, "https://hub2.example.com")
        XCTAssertEqual(saved.bearer, "old-bearer")
        XCTAssertEqual(saved.storedAt, stored.storedAt)
    }

    func testTheAcknowledgementsReplyNeverTriggersAnotherRequest() {
        var f = InboundFilter(stored: stored)
        _ = f.filter([data(readyFrame)], now: now)
        _ = f.filter([reply(1, "{\"available\":true,\"url\":\"u\",\"rv\":\"r\",\"bearer\":\"b1\"}")], now: now)
        let out = f.filter([reply(2, "{\"available\":true,\"url\":\"u\",\"rv\":\"r\",\"bearer\":\"b2\"}")], now: now)
        XCTAssertEqual(sent(out).count, 0)
        XCTAssertEqual(f.stored?.bearer, "b2")
    }

    func testARefusedRelayCredIsIgnoredAndNeverReachesJS() {
        var f = InboundFilter(stored: stored)
        _ = f.filter([data(readyFrame)], now: now)
        let err = data("{\"v\":2,\"type\":\"err\",\"id\":\"lola-relaycred-1\",\"payload\":{\"code\":\"unknown_cmd\",\"message\":\"x\"}}")
        XCTAssertEqual(f.filter([err], now: now), InboundFilter.Output())
        XCTAssertEqual(f.stored, stored)
    }

    func testUnsolicitedReservedRepliesAreDroppedAndOrdinaryFramesPass() {
        var f = InboundFilter(stored: stored)
        _ = f.filter([data(readyFrame)], now: now)
        let stray = reply(99, "{\"available\":true,\"url\":\"u\",\"rv\":\"r\",\"bearer\":\"evil\"}")
        let resp = data("{\"v\":2,\"type\":\"resp\",\"id\":\"r1\",\"payload\":{\"ok\":true}}")
        let junk = data("not json")
        let out = f.filter([stray, resp, junk], now: now)
        XCTAssertEqual(out.frames, [resp, junk])
        XCTAssertEqual(out.effects, [])
        XCTAssertEqual(f.stored, stored)
    }

    func testABearerWithoutAURLAndNoStoredRecordIsNotSaved() {
        var f = InboundFilter(stored: nil)
        _ = f.filter([data(readyFrame)], now: now)
        let out = f.filter([reply(1, "{\"available\":true,\"bearer\":\"b\"}")], now: now)
        XCTAssertEqual(out.effects, [])
    }

    // MARK: Outbound

    func testOutboundBatchesTouchingNativeStateAreRejected() {
        let ok = "{\"v\":2,\"type\":\"req\",\"id\":\"r1\",\"cmd\":\"sessions\",\"payload\":{\"cmd\":\"sessions\"}}"
        XCTAssertEqual(InboundFilter.checkOutbound([data(ok)]), .allowed)
        XCTAssertEqual(InboundFilter.checkOutbound([]), .allowed)
        let reservedID = "{\"v\":2,\"type\":\"req\",\"id\":\"lola-x\",\"cmd\":\"sessions\"}"
        let topCmd = "{\"v\":2,\"type\":\"req\",\"id\":\"r2\",\"cmd\":\"relayCred\"}"
        let payloadCmd = "{\"v\":2,\"type\":\"req\",\"id\":\"r3\",\"cmd\":\"x\",\"payload\":{\"cmd\":\"relayCred\"}}"
        for bad in [reservedID, topCmd, payloadCmd] {
            XCTAssertEqual(InboundFilter.checkOutbound([data(ok), data(bad)]), .reserved, bad)
        }
        XCTAssertEqual(InboundFilter.checkOutbound([data("nope")]), .malformed)
        XCTAssertEqual(InboundFilter.checkOutbound([data("[1]")]), .malformed)
    }
}
