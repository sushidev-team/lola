import XCTest

@testable import LolaTransportCore

final class PathRaceTests: XCTestCase {
    private let a = RaceCandidate.direct("192.168.1.20", port: 7717)
    private let b = RaceCandidate.direct("192.168.1.21", port: 7717)
    private let c = RaceCandidate.direct("10.0.0.9", port: 7717)
    private let relay = RaceCandidate.relay
    private let wall = Date(timeIntervalSince1970: 1_800_000_000)
    private let spkiX = Data(repeating: 1, count: 32)
    private let spkiY = Data(repeating: 2, count: 32)

    private func race(
        _ direct: [RaceCandidate], relay hasRelay: Bool = false, mode: RacePlan.Mode = .cold,
        policy: RelayPolicy = RelayPolicy(), storedAt: Date? = nil,
        wifi: Bool = true, satisfied: Bool = true
    ) -> PathRace {
        PathRace(plan: RacePlan(
            mode: mode, direct: direct, hasRelay: hasRelay, relayPolicy: policy, relayStoredAt: storedAt,
            relayURL: "https://hub.example.com", wallClock: wall, hasWifiOrWired: wifi, monitorSatisfied: satisfied))
    }

    // MARK: Schedule

    func testDirectCandidatesStaggerBy150msAndRelayStartsAt300() {
        var r = race([a, b, c], relay: true)
        XCTAssertEqual(r.step(now: 0, event: .begin), [.start(a)])
        XCTAssertEqual(r.nextWake, 150)
        XCTAssertEqual(r.step(now: 149, event: .tick), [])
        XCTAssertEqual(r.step(now: 150, event: .tick), [.start(b)])
        // c is due at 300 but two direct attempts are in flight; the relay is due at 300 too.
        XCTAssertEqual(r.step(now: 300, event: .tick), [.start(relay)])
    }

    func testRelayStartsAtOnceWithNoDirectCandidateOrNoWifi() {
        var none = race([], relay: true)
        XCTAssertEqual(none.step(now: 10, event: .begin), [.start(relay)])
        var cellular = race([a], relay: true, wifi: false)
        XCTAssertEqual(cellular.step(now: 0, event: .begin), [.start(a), .start(relay)])
    }

    func testAtMostTwoDirectAttemptsAreInFlight() {
        var r = race([a, b, c])
        _ = r.step(now: 0, event: .begin)
        XCTAssertEqual(r.step(now: 150, event: .tick), [.start(b)])
        XCTAssertEqual(r.step(now: 400, event: .tick), [])
        // A failure frees a direct slot and the third starts at once.
        XCTAssertEqual(r.step(now: 500, event: .failed(a, .network("refused"))), [.start(c)])
    }

    func testAColdRaceAllowsThreeAttemptsAndAMigrationRaceTwo() {
        var cold = race([a, b], relay: true)
        _ = cold.step(now: 0, event: .begin)
        _ = cold.step(now: 150, event: .tick)
        XCTAssertEqual(cold.step(now: 300, event: .tick), [.start(relay)])

        var migration = race([a, b], relay: true, mode: .migration)
        _ = migration.step(now: 0, event: .begin)
        _ = migration.step(now: 150, event: .tick)
        XCTAssertEqual(migration.step(now: 300, event: .tick), [], "two in flight already")
        XCTAssertEqual(migration.step(now: 400, event: .failed(a, .network("x"))), [.start(relay)])
    }

    func testMDNSCandidatesAreDedupedCappedAndStaggered() {
        var r = race([a])
        _ = r.step(now: 0, event: .begin)
        let found = (1...12).map { RaceCandidate.direct("10.1.0.\($0)", port: 7717) }
        // `a` is a duplicate; the first new candidate takes the stagger slot after it.
        XCTAssertEqual(r.step(now: 10, event: .discovered([a] + found)), [])
        XCTAssertEqual(r.step(now: 150, event: .tick), [.start(found[0])])
        var started = [a, found[0]]
        var pendingFirst = r.step(now: 160, event: .failed(found[0], .network("refused")))
        for t in stride(from: 170, through: 3900, by: 10) {
            var pending = pendingFirst + r.step(now: t, event: .tick)
            pendingFirst = []
            while case .start(let s)? = pending.first {
                pending.removeFirst()
                started.append(s)
                pending += r.step(now: t, event: .failed(s, .network("refused")))
            }
        }
        XCTAssertEqual(started.count, 9, "the original candidate and at most 8 from mDNS")
        XCTAssertEqual(Set(started).count, 9)
        XCTAssertFalse(started.contains(found[8]))
    }

    func testEveryAttemptTimesOutOnItsOwnClock() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        XCTAssertEqual(r.step(now: 4000, event: .tick), [.cancel(a)])
        XCTAssertEqual(r.step(now: 12_300, event: .tick), [.cancel(relay), .fail(.timeout)])
    }

    func testTheWholeRaceEndsAt13Seconds() {
        var r = race([a, b], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 4000, event: .failed(a, .network("x")))
        let actions = r.step(now: 13_000, event: .tick)
        XCTAssertEqual(actions.last, .fail(.timeout))
        XCTAssertTrue(r.finished)
        XCTAssertNil(r.nextWake)
    }

    // MARK: Winning

    func testAFirstDirectReadyWinsAndCancelsTheRest() {
        var r = race([a, b], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 300, event: .tick)
        XCTAssertEqual(r.step(now: 320, event: .ready(b)), [.cancel(a), .cancel(relay), .win(b)])
        XCTAssertTrue(r.finished)
    }

    func testARelayReadyWaitsForADirectAttemptAlreadyAtTLSReady() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 400, event: .tlsReady(a))
        XCTAssertEqual(r.step(now: 500, event: .ready(relay)), [])
        XCTAssertEqual(r.nextWake, 750)
        XCTAssertEqual(r.step(now: 749, event: .tick), [])
        XCTAssertEqual(r.step(now: 750, event: .tick), [.cancel(a), .win(relay)])
    }

    func testDirectWinsATieAndWinsInsideTheGrace() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 400, event: .tlsReady(a))
        _ = r.step(now: 500, event: .ready(relay))
        XCTAssertEqual(r.step(now: 600, event: .ready(a)), [.cancel(relay), .win(a)])
    }

    func testARelayReadyWinsAtOnceWhenNoDirectAttemptIsAtTLSReady() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        XCTAssertEqual(r.step(now: 600, event: .ready(relay)), [.cancel(a), .win(relay)])
        XCTAssertEqual(r.relayPolicy.state, .ok)
    }

    func testARelayReadyWinsAsSoonAsTheDirectAttemptItWaitedForFails() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 400, event: .tlsReady(a))
        _ = r.step(now: 500, event: .ready(relay))
        XCTAssertEqual(r.step(now: 550, event: .failed(a, .tlsBeforeReady("reset"))), [.win(relay)])
    }

    func testALateReadyAfterTheWinIsCancelled() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 200, event: .ready(a))
        XCTAssertEqual(r.step(now: 210, event: .ready(b)), [.cancel(b)])
        XCTAssertEqual(r.step(now: 210, event: .tlsReady(b)), [.cancel(b)])
        XCTAssertEqual(r.step(now: 210, event: .ready(a)), [])
    }

    // MARK: Classification

    func testAnMDNSKeyMismatchDoesNotStopARelayWin() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        let rogue = RaceCandidate.direct("10.9.9.9", port: 7717)
        _ = r.step(now: 5, event: .discovered([rogue]))
        XCTAssertEqual(r.step(now: 150, event: .tick), [.start(rogue)])
        _ = r.step(now: 200, event: .failed(rogue, .keyMismatch(spki: spkiX)))
        XCTAssertEqual(r.step(now: 300, event: .tick), [.start(relay)])
        XCTAssertEqual(r.step(now: 700, event: .ready(relay)), [.cancel(a), .win(relay)])
    }

    func testARelay401ThenADirectReadyWinsAndRecordsCredentialDead() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 350, event: .failed(relay, .relay(.credentialDead)))
        XCTAssertEqual(r.step(now: 400, event: .ready(a)), [.win(a)])
        XCTAssertEqual(r.relayPolicy.state, .credentialDead)
    }

    func testARelay401WithinTheFreshGraceIsABackoff() {
        var r = race([a], relay: true, storedAt: wall.addingTimeInterval(-30))
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 350, event: .failed(relay, .relay(.credentialDead)))
        _ = r.step(now: 400, event: .ready(a))
        XCTAssertEqual(r.relayPolicy.state, .backoff)
    }

    func testATLSAlertOnOneCandidateAndSuccessOnAnotherWins() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        XCTAssertEqual(r.step(now: 200, event: .failed(a, .tlsBeforeReady("alert"))), [])
        XCTAssertEqual(r.step(now: 250, event: .ready(b)), [.win(b)])
    }

    func testAnAlertAfterReadyEndsTheRaceAtOnceAsNotPaired() {
        var r = race([a, b], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        XCTAssertEqual(r.step(now: 200, event: .failed(a, .alertAfterReady("bad_certificate"))), [.cancel(b), .fail(.notPaired)])
        XCTAssertTrue(r.finished)
    }

    func testDaemonRefusalsEndTheRaceAtOnce() {
        var p = race([a])
        _ = p.step(now: 0, event: .begin)
        XCTAssertEqual(p.step(now: 20, event: .refused(a, .notPaired)), [.cancel(a), .fail(.notPaired)])
        var v = race([a])
        _ = v.step(now: 0, event: .begin)
        XCTAssertEqual(v.step(now: 20, event: .refused(a, .unsupportedVersion)), [.cancel(a), .fail(.unsupportedVersion)])
    }

    func testTheSameWrongKeyOnEveryCandidateIncludingTheRelayIsKeyChanged() {
        var r = race([a, b], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 400, event: .failed(a, .keyMismatch(spki: spkiX)))
        _ = r.step(now: 410, event: .failed(b, .keyMismatch(spki: spkiX)))
        XCTAssertEqual(r.step(now: 420, event: .failed(relay, .keyMismatch(spki: spkiX))), [.fail(.keyChanged)])
    }

    func testTheSameWrongKeyWithoutTheRelayIsNotKeyChanged() {
        var r = race([a, b], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 400, event: .failed(a, .keyMismatch(spki: spkiX)))
        _ = r.step(now: 410, event: .failed(b, .keyMismatch(spki: spkiX)))
        XCTAssertEqual(r.step(now: 420, event: .failed(relay, .relay(.daemonOffline))), [.fail(.unreachable)])
    }

    func testAWrongKeyIsNotKeyChangedWhenAnotherCandidateProvedThePin() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 200, event: .failed(a, .keyMismatch(spki: spkiX)))
        _ = r.step(now: 250, event: .tlsReady(b))
        XCTAssertEqual(r.step(now: 300, event: .failed(b, .network("closed before ready"))), [.fail(.unreachable)])
    }

    func testAPausedRelayStillBlocksKeyChangedWhenTheDaemonHasOne() {
        var paused = RelayPolicy()
        paused.record(.hubTooOld, now: wall, storedAt: nil, url: "https://hub.example.com")
        var r = race([a], relay: true, policy: paused)
        _ = r.step(now: 0, event: .begin)
        XCTAssertEqual(r.step(now: 100, event: .failed(a, .keyMismatch(spki: spkiX))), [.fail(.unreachable)])
        var noRelay = race([a])
        _ = noRelay.step(now: 0, event: .begin)
        XCTAssertEqual(noRelay.step(now: 100, event: .failed(a, .keyMismatch(spki: spkiX))), [.fail(.keyChanged)])
    }

    func testARelayThatSentReadyAndThenDiedNeverWins() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        _ = r.step(now: 400, event: .tlsReady(a))
        XCTAssertEqual(r.step(now: 500, event: .ready(relay)), [])
        XCTAssertEqual(r.step(now: 600, event: .failed(relay, .network("closed"))), [])
        XCTAssertEqual(r.step(now: 750, event: .tick), [])
        XCTAssertEqual(r.step(now: 800, event: .failed(a, .network("closed before ready"))), [.fail(.unreachable)])
    }

    func testTwoDifferentWrongKeysAreUnreachable() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 400, event: .failed(a, .keyMismatch(spki: spkiX)))
        XCTAssertEqual(r.step(now: 410, event: .failed(b, .keyMismatch(spki: spkiY))), [.fail(.unreachable)])
    }

    func testARelayOnlyRaceRefusedWith503Or504IsRelayUnavailable() {
        for refusal in [RelayRefusal.busy, .daemonOffline, .timeout] {
            var r = race([], relay: true)
            _ = r.step(now: 0, event: .begin)
            XCTAssertEqual(r.step(now: 100, event: .failed(relay, .relay(refusal))), [.fail(.relayUnavailable)], "\(refusal)")
        }
        var dead = race([], relay: true)
        _ = dead.step(now: 0, event: .begin)
        XCTAssertEqual(dead.step(now: 100, event: .failed(relay, .relay(.credentialDead))), [.fail(.unreachable)])
    }

    func testOfflineWhenTheMonitorHasNoSatisfiedPath() {
        var r = race([a], satisfied: false)
        _ = r.step(now: 0, event: .begin)
        XCTAssertEqual(r.step(now: 100, event: .failed(a, .network("no route"))), [.fail(.offline)])
    }

    func testTimeoutBeatsUnreachableWhenAnAttemptTimedOut() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 200, event: .failed(b, .network("refused")))
        XCTAssertEqual(r.step(now: 4000, event: .tick), [.cancel(a), .fail(.timeout)])
    }

    func testAPausedRelayIsNotACandidate() {
        var paused = RelayPolicy()
        paused.record(.hubTooOld, now: wall, storedAt: nil, url: "https://hub.example.com")
        var r = race([a], relay: true, policy: paused)
        _ = r.step(now: 0, event: .begin)
        XCTAssertEqual(r.step(now: 400, event: .tick), [])
        XCTAssertEqual(r.step(now: 500, event: .failed(a, .network("x"))), [.fail(.unreachable)])
    }

    func testAnEmptyRaceWaitsForMDNSUntilItsTimeout() {
        var r = race([])
        XCTAssertEqual(r.step(now: 0, event: .begin), [])
        XCTAssertFalse(r.finished)
        let found = RaceCandidate.direct("10.0.0.5", port: 7717)
        XCTAssertEqual(r.step(now: 500, event: .discovered([found])), [.start(found)])
        XCTAssertEqual(r.step(now: 13_000, event: .tick), [.cancel(found), .fail(.timeout)])
        var silent = race([])
        _ = silent.step(now: 0, event: .begin)
        XCTAssertEqual(silent.step(now: 13_000, event: .tick), [.fail(.unreachable)])
    }

    func testEventsBeforeBeginAreIgnored() {
        var r = race([a])
        XCTAssertEqual(r.step(now: 0, event: .tick), [])
        XCTAssertEqual(r.step(now: 0, event: .ready(a)), [])
        XCTAssertNil(r.nextWake)
    }

    func testEventsForAnUnknownOrFinishedAttemptChangeNothing() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        XCTAssertEqual(r.step(now: 10, event: .failed(b, .network("never started"))), [])
        XCTAssertEqual(r.step(now: 20, event: .ready(b)), [])
    }

    func testHostPortRoundTripAndAddressRanking() {
        XCTAssertEqual(RaceCandidate(hostPort: "192.168.1.2:7717"), .direct("192.168.1.2", port: 7717))
        XCTAssertEqual(RaceCandidate(hostPort: "[fd00::2]:7717"), .direct("fd00::2", port: 7717))
        XCTAssertEqual(RaceCandidate.direct("fd00::2", port: 7717).hostPort, "[fd00::2]:7717")
        XCTAssertNil(RaceCandidate(hostPort: "nope"))
        XCTAssertNil(RaceCandidate(hostPort: "h:0"))
        let plan = RacePlan.directCandidates(lastGood: "10.0.0.9:7717", addrs: ["fd00::2", "10.0.0.9", "169.254.1.1", "8.8.8.8", "192.168.1.2"], port: 7717)
        XCTAssertEqual(plan.map(\.host), ["10.0.0.9", "192.168.1.2", "8.8.8.8", "169.254.1.1", "fd00::2"])
        XCTAssertEqual(plan.first?.hostPort, "10.0.0.9:7717")
    }
}

final class AddressRankTests: XCTestCase {
    func testOrderIsPrivateV4OtherV4LinkLocalV6LinkLocalV6LoopbackLast() {
        XCTAssertEqual(
            AddressRank.rank(["127.0.0.1", "fe80::1", "fd00::1", "169.254.4.4", "1.2.3.4", "::1", "172.20.0.1", "10.0.0.1", "192.168.0.1"]),
            ["172.20.0.1", "10.0.0.1", "192.168.0.1", "1.2.3.4", "169.254.4.4", "fd00::1", "fe80::1", "127.0.0.1", "::1"])
    }

    func testJunkZoneIdsAndDuplicatesAreDropped() {
        XCTAssertEqual(AddressRank.rank(["fe80::1%en0", "host.local", "", " 10.0.0.1 ", "10.0.0.1"]), ["10.0.0.1"])
        XCTAssertEqual(AddressRank.rank(["172.32.0.1"]), ["172.32.0.1"])
    }
}

final class PairRaceTests: XCTestCase {
    private let a = RaceCandidate.direct("192.168.1.20", port: 7717)
    private let b = RaceCandidate.direct("192.168.1.21", port: 7717)
    private let c = RaceCandidate.direct("192.168.1.22", port: 7717)
    private let relay = RaceCandidate.relay

    private func race(_ direct: [RaceCandidate], relay: Bool = false) -> PathRace {
        PathRace(plan: RacePlan(mode: .pairing, direct: direct, hasRelay: relay))
    }

    func testNeverMoreThanTwoInFlight() {
        var r = race([a, b, c], relay: true)
        XCTAssertEqual(r.step(now: 0, event: .begin), [.start(a)])
        XCTAssertEqual(r.step(now: 150, event: .tick), [.start(b)])
        XCTAssertEqual(r.step(now: 1000, event: .tick), [])
        XCTAssertEqual(r.step(now: 1100, event: .failed(a, .network("x"))), [.start(c)])
    }

    func testTheFirstTLSReadyWinsAndCancelsEveryOtherAttemptBeforeAnyWrite() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        XCTAssertEqual(r.step(now: 300, event: .tlsReady(b)), [.cancel(a), .win(b)])
    }

    func testASecondTLSReadyAfterTheWinIsCancelled() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 300, event: .tlsReady(a))
        XCTAssertEqual(r.step(now: 301, event: .tlsReady(b)), [.cancel(b)])
        XCTAssertEqual(r.step(now: 302, event: .tlsReady(a)), [])
    }

    func testTheRelayCanWinThePairingRace() {
        var r = race([a], relay: true)
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 300, event: .tick)
        XCTAssertEqual(r.step(now: 500, event: .tlsReady(relay)), [.cancel(a), .win(relay)])
    }

    func testATLSFailureBeforeReadyOnEveryCandidateMeansTheWindowClosed() {
        var r = race([a, b])
        _ = r.step(now: 0, event: .begin)
        _ = r.step(now: 150, event: .tick)
        _ = r.step(now: 200, event: .failed(a, .tlsBeforeReady("alert")))
        XCTAssertEqual(r.step(now: 210, event: .failed(b, .network("refused"))), [.expired])
    }

    func testNoTLSAtAllRejectsAsUnreachableOrTimeout() {
        var r = race([a])
        _ = r.step(now: 0, event: .begin)
        XCTAssertEqual(r.step(now: 100, event: .failed(a, .network("no route"))), [.fail(.unreachable)])
    }

    func testMDNSIsNotConsultedWhilePairing() {
        var r = race([a])
        _ = r.step(now: 0, event: .begin)
        XCTAssertEqual(r.step(now: 10, event: .discovered([.direct("10.0.0.9", port: 7717)])), [])
    }
}
