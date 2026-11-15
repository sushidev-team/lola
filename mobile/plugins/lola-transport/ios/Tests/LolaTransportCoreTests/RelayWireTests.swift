import XCTest

@testable import LolaTransportCore

final class RelayWireTests: XCTestCase {
    private let t0 = Date(timeIntervalSince1970: 1_800_000_000)
    private let hub = "https://hub.example.com"

    // MARK: Request

    func testTheRequestCarriesExactlyTheRelayHeaders() throws {
        let r = RelayWire.request(url: URL(string: "https://hub.example.com")!, rv: "rv1", bearer: "tok1")
        XCTAssertEqual(r.url?.absoluteString, "wss://hub.example.com/v1/relay/phone")
        XCTAssertEqual(r.httpMethod, "GET")
        XCTAssertEqual(r.value(forHTTPHeaderField: "Lola-Relay-Rv"), "rv1")
        XCTAssertEqual(r.value(forHTTPHeaderField: "Lola-Relay-Bearer"), "tok1")
        XCTAssertEqual(r.value(forHTTPHeaderField: "Sec-WebSocket-Protocol"), "lola-relay.v1")
        let names = Set((r.allHTTPHeaderFields ?? [:]).keys.map { $0.lowercased() })
        XCTAssertEqual(names, ["lola-relay-rv", "lola-relay-bearer", "sec-websocket-protocol"])
        XCTAssertNil(r.value(forHTTPHeaderField: "Authorization"))
        XCTAssertNil(r.value(forHTTPHeaderField: "Origin"))
        XCTAssertFalse(r.httpShouldHandleCookies)
        // The request timeout is the open socket's idle timeout: it outlasts the
        // hub's 25 s ping and 60 s dead-after, while the 8 s upgrade has its own timer.
        XCTAssertEqual(r.timeoutInterval, RelayWire.idleTimeout)
        XCTAssertGreaterThan(RelayWire.idleTimeout, 60)
        XCTAssertEqual(RelayWire.upgradeTimeout, 8)
    }

    func testTheRequestAppendsToABasePathAndDropsAQuery() {
        let r = RelayWire.request(url: URL(string: "https://example.com/hub/?x=1#f")!, rv: "r", bearer: "b")
        XCTAssertEqual(r.url?.absoluteString, "wss://example.com/hub/v1/relay/phone")
    }

    // MARK: Refusals

    func testEveryRefusalRow() {
        let cases: [(Int, String?, String?, RelayRefusal)] = [
            (401, nil, nil, .credentialDead),
            (401, "unauthorized", nil, .credentialDead),
            (404, nil, nil, .hubTooOld),
            (404, "not_found", nil, .hubTooOld),
            (503, "offline", nil, .daemonOffline),
            (503, "busy", nil, .busy),
            (503, nil, nil, .busy),
            (503, "weird", nil, .busy),
            (504, "relay_timeout", nil, .timeout),
            (504, nil, nil, .timeout),
            (429, "rate_limited", "7", .rateLimited(7)),
            (429, "rate_limited", nil, .rateLimited(5)),
            (429, nil, "soon", .rateLimited(5)),
            (429, nil, "-3", .rateLimited(5)),
            (429, nil, "999999", .rateLimited(3600)),
            (403, "forbidden", nil, .forbidden),
            (400, "bad_request", nil, .badRequest),
            (500, "internal", nil, .other(500)),
            (302, nil, nil, .other(302)),
        ]
        for (status, code, retry, want) in cases {
            XCTAssertEqual(RelayWire.refusal(status: status, code: code, retryAfter: retry), want, "\(status) \(code ?? "-") \(retry ?? "-")")
        }
    }

    // MARK: Policy (injected clock)

    func testA401AfterTheGraceKillsTheCredentialForAnHour() {
        var p = RelayPolicy()
        p.record(.credentialDead, now: t0, storedAt: t0.addingTimeInterval(-3600), url: hub)
        XCTAssertEqual(p.state, .credentialDead)
        XCTAssertFalse(p.allowsAttempt(now: t0.addingTimeInterval(3599), url: hub))
        XCTAssertTrue(p.allowsAttempt(now: t0.addingTimeInterval(3600), url: hub))
    }

    func testA401WithinTheFreshGraceIsABackoff() {
        var p = RelayPolicy()
        p.record(.credentialDead, now: t0, storedAt: t0.addingTimeInterval(-59), url: hub)
        XCTAssertEqual(p.state, .backoff)
        XCTAssertFalse(p.allowsAttempt(now: t0.addingTimeInterval(1), url: hub))
        XCTAssertTrue(p.allowsAttempt(now: t0.addingTimeInterval(2), url: hub))
        var late = RelayPolicy()
        late.record(.credentialDead, now: t0, storedAt: t0.addingTimeInterval(-60), url: hub)
        XCTAssertEqual(late.state, .credentialDead, "the grace is exclusive of 60 s")
    }

    func testAStoredBearerLiftsACredentialDeadPause() {
        var p = RelayPolicy()
        p.record(.credentialDead, now: t0, storedAt: nil, url: hub)
        p.credentialStored()
        XCTAssertEqual(p.state, .ok)
        XCTAssertTrue(p.allowsAttempt(now: t0, url: hub))
    }

    func testAnOldHubIsRechecked6HoursLaterOrWhenTheURLChanges() {
        var p = RelayPolicy()
        p.record(.hubTooOld, now: t0, storedAt: nil, url: hub)
        XCTAssertEqual(p.state, .hubTooOld)
        XCTAssertFalse(p.allowsAttempt(now: t0.addingTimeInterval(6 * 3600 - 1), url: hub))
        XCTAssertTrue(p.allowsAttempt(now: t0.addingTimeInterval(6 * 3600), url: hub))
        XCTAssertTrue(p.allowsAttempt(now: t0.addingTimeInterval(1), url: "https://other.example.com"))
    }

    func testTheDaemonSayingAvailableLiftsAnOldHubPauseAtOnce() {
        var p = RelayPolicy()
        p.record(.hubTooOld, now: t0, storedAt: nil, url: hub)
        XCTAssertFalse(p.allowsAttempt(now: t0.addingTimeInterval(60), url: hub))
        p.relayAvailable()
        XCTAssertEqual(p.state, .ok)
        XCTAssertNil(p.pausedURL)
        XCTAssertTrue(p.allowsAttempt(now: t0.addingTimeInterval(60), url: hub))
    }

    func testTheDaemonSayingAvailableKeepsEveryOtherPause() {
        var dead = RelayPolicy()
        dead.record(.credentialDead, now: t0, storedAt: nil, url: hub)
        let deadBefore = dead
        dead.relayAvailable()
        XCTAssertEqual(dead, deadBefore, "only a stored bearer lifts a dead credential")

        var limited = RelayPolicy()
        limited.record(.rateLimited(30), now: t0, storedAt: nil, url: hub)
        let limitedBefore = limited
        limited.relayAvailable()
        XCTAssertEqual(limited, limitedBefore, "a rate-limit floor holds")
    }

    func testBackoffFollowsTheLadderAndASuccessResetsIt() {
        var p = RelayPolicy()
        var waits: [TimeInterval] = []
        for refusal in [RelayRefusal.daemonOffline, .busy, .timeout, .forbidden, .badRequest, .other(500), .busy] {
            p.record(refusal, now: t0, storedAt: nil, url: hub)
            XCTAssertEqual(p.state, .backoff)
            waits.append(p.retryAt!.timeIntervalSince(t0))
        }
        XCTAssertEqual(waits, [2, 4, 8, 16, 32, 60, 60])
        p.recordSuccess()
        XCTAssertEqual(p.state, .ok)
        p.record(.busy, now: t0, storedAt: nil, url: hub)
        XCTAssertEqual(p.retryAt!.timeIntervalSince(t0), 2)
    }

    func testRateLimitedWaitsAtLeastTwoSeconds() {
        var p = RelayPolicy()
        p.record(.rateLimited(0), now: t0, storedAt: nil, url: hub)
        XCTAssertEqual(p.retryAt!.timeIntervalSince(t0), 2)
        p.record(.rateLimited(30), now: t0, storedAt: nil, url: hub)
        XCTAssertEqual(p.retryAt!.timeIntervalSince(t0), 30)
        XCTAssertEqual(p.state, .backoff)
    }
}
