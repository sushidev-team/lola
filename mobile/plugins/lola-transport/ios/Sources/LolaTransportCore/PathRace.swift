import Foundation

/// One way to reach the daemon.
public struct RaceCandidate: Hashable, Sendable, CustomStringConvertible {
    public enum Kind: Hashable, Sendable { case direct, relay }

    public let kind: Kind
    public let host: String
    public let port: Int

    public static func direct(_ host: String, port: Int) -> RaceCandidate {
        RaceCandidate(kind: .direct, host: host, port: port)
    }

    public static let relay = RaceCandidate(kind: .relay, host: "", port: 0)

    public var isRelay: Bool { kind == .relay }

    /// `host:port`, IPv6 in brackets; empty for the relay.
    public var hostPort: String {
        isRelay ? "" : (host.contains(":") ? "[\(host)]:\(port)" : "\(host):\(port)")
    }

    public init(kind: Kind, host: String, port: Int) {
        self.kind = kind
        self.host = host
        self.port = port
    }

    /// Parses `hostPort` back (`PairingRecord.lastGood`).
    public init?(hostPort: String) {
        let host: Substring
        let port: Substring
        if hostPort.hasPrefix("["), let close = hostPort.firstIndex(of: "]") {
            host = hostPort[hostPort.index(after: hostPort.startIndex)..<close]
            let rest = hostPort[hostPort.index(after: close)...]
            guard rest.hasPrefix(":") else { return nil }
            port = rest.dropFirst()
        } else {
            let parts = hostPort.split(separator: ":", omittingEmptySubsequences: false)
            guard parts.count == 2 else { return nil }
            host = parts[0]
            port = parts[1]
        }
        guard !host.isEmpty, let n = Int(port), (1...65535).contains(n) else { return nil }
        self.init(kind: .direct, host: String(host), port: n)
    }

    public var description: String { isRelay ? "relay" : hostPort }
}

/// What a path reports when it fails (04 §1.3).
public enum PathFailure: Equatable, Sendable {
    /// TCP, DNS or WebSocket transport.
    case network(String)
    /// Any TLS failure or alert before the stack reached `.ready`: proves
    /// nothing, because an on-path host can send one.
    case tlsBeforeReady(String)
    /// The verify block saw a certificate whose SPKI hash is not the pin.
    case keyMismatch(spki: Data)
    /// A TLS alert after `.ready`: the pinned daemon said no.
    case alertAfterReady(String)
    case relay(RelayRefusal)
    case timeout
}

/// An authenticated daemon's refusal, read from an `err` frame.
public enum DaemonRefusal: Equatable, Sendable {
    case notPaired
    case unsupportedVersion
}

public struct RacePlan: Equatable, Sendable {
    public enum Mode: Equatable, Sendable {
        /// A fresh connection.
        case cold
        /// A background race while an epoch is live.
        case migration
        /// Pairing: the first TLS handshake wins; nothing is authenticated.
        case pairing
    }

    public var mode: Mode
    /// Direct candidates in attempt order (`lastGood`, then ranked QR addresses).
    public var direct: [RaceCandidate]
    /// The daemon has relay credentials (or, pairing, a QR `relay`).
    public var hasRelay: Bool
    public var relayPolicy: RelayPolicy
    public var relayStoredAt: Date?
    public var relayURL: String?
    /// Wall-clock time of `begin`, to evaluate `relayPolicy`.
    public var wallClock: Date
    public var hasWifiOrWired: Bool
    /// The path monitor reports a satisfied path.
    public var monitorSatisfied: Bool

    public init(
        mode: Mode, direct: [RaceCandidate], hasRelay: Bool,
        relayPolicy: RelayPolicy = RelayPolicy(), relayStoredAt: Date? = nil, relayURL: String? = nil,
        wallClock: Date = Date(), hasWifiOrWired: Bool = true, monitorSatisfied: Bool = true
    ) {
        self.mode = mode
        self.direct = direct
        self.hasRelay = hasRelay
        self.relayPolicy = relayPolicy
        self.relayStoredAt = relayStoredAt
        self.relayURL = relayURL
        self.wallClock = wallClock
        self.hasWifiOrWired = hasWifiOrWired
        self.monitorSatisfied = monitorSatisfied
    }

    /// `lastGood` first, then the ranked QR addresses, deduplicated.
    public static func directCandidates(lastGood: String?, addrs: [String], port: Int) -> [RaceCandidate] {
        var out: [RaceCandidate] = []
        if let lastGood, let c = RaceCandidate(hostPort: lastGood) { out.append(c) }
        for host in AddressRank.rank(addrs) where port > 0 {
            let c = RaceCandidate.direct(host, port: port)
            if !out.contains(c) { out.append(c) }
        }
        return out
    }
}

public enum RaceEvent: Equatable, Sendable {
    /// Starts the race; every other event is ignored before it.
    case begin
    /// The clock moved; timeouts and staggered starts fire.
    case tick
    /// mDNS results; deduplicated and capped (cold and migration only).
    case discovered([RaceCandidate])
    /// The attempt's TLS handshake completed (pin matched).
    case tlsReady(RaceCandidate)
    /// The attempt received the daemon's `ready` frame.
    case ready(RaceCandidate)
    case failed(RaceCandidate, PathFailure)
    case refused(RaceCandidate, DaemonRefusal)
}

public enum RaceAction: Equatable, Sendable {
    case start(RaceCandidate)
    case cancel(RaceCandidate)
    case win(RaceCandidate)
    case fail(FailureCode)
    /// Pairing only: a handshake failed before `.ready`, which on a pairing
    /// candidate means the window closed or a slot was refused.
    case expired
}

/// The racing connector's decisions (04 §1.4), pure: all time is an injected
/// monotonic millisecond clock, all I/O is events in and actions out.
///
/// Rules in one place:
/// - direct candidate *n* starts at `n * 150` ms, relay at 300 ms (at once with
///   no direct candidate or no Wi-Fi/wired interface); at most 3 attempts in
///   flight cold, 2 in migration and pairing, and at most 2 direct;
/// - the winner is the first authenticated `ready`; a relay `ready` waits 250 ms
///   for a direct attempt already at `.tlsReady` and loses a tie;
/// - pairing has no `ready`: the first `.tlsReady` wins;
/// - only an authenticated refusal ends the race early.
public struct PathRace {
    private struct Attempt {
        var startedAt: Int
        var tlsReady = false
    }

    private let plan: RacePlan
    private let relayIncluded: Bool
    public private(set) var relayPolicy: RelayPolicy
    public private(set) var finished = false

    private var begun = false
    private var t0 = 0
    private var winner: RaceCandidate?
    private var queue: [(candidate: RaceCandidate, dueAt: Int)] = []
    private var inFlight: [RaceCandidate: Attempt] = [:]
    private var startOrder: [RaceCandidate] = []
    private var known = Set<RaceCandidate>()
    private var directCount = 0
    private var discovered = 0
    private var failures: [RaceCandidate: PathFailure] = [:]
    private var anyTimeout = false
    private var attempted = 0
    private var relayReadySince: Int?
    /// Some candidate's verify block accepted the pinned key, so the daemon's
    /// key did not change, whatever another candidate showed.
    private var pinProven = false

    public init(plan: RacePlan) {
        self.plan = plan
        self.relayPolicy = plan.relayPolicy
        self.relayIncluded = plan.hasRelay && plan.relayPolicy.allowsAttempt(now: plan.wallClock, url: plan.relayURL)
    }

    private var maxParallel: Int {
        switch plan.mode {
        case .cold: return RaceTiming.maxParallelAttempts
        case .migration: return RaceTiming.maxMigrationAttempts
        case .pairing: return RaceTiming.pairMaxParallel
        }
    }

    /// The next time `step(now:event: .tick)` has work to do; nil once finished.
    public var nextWake: Int? {
        guard begun, !finished else { return nil }
        var wakes = [t0 + RaceTiming.raceTimeoutMs]
        wakes += queue.map(\.dueAt)
        wakes += inFlight.map { $0.value.startedAt + timeout(for: $0.key) }
        if let since = relayReadySince { wakes.append(since + RaceTiming.preferDirectGraceMs) }
        return wakes.min()
    }

    public mutating func step(now: Int, event: RaceEvent) -> [RaceAction] {
        var actions: [RaceAction] = []

        if finished {
            switch event {
            case .tlsReady(let c), .ready(let c):
                if c != winner { actions.append(.cancel(c)) }
            default: break
            }
            return actions
        }
        if event == .begin {
            guard !begun else { return [] }
            begin(now: now)
        }
        guard begun else { return [] }

        switch event {
        case .begin, .tick:
            break
        case .discovered(let found):
            addDiscovered(found, now: now)
        case .tlsReady(let c):
            handleTLSReady(c, into: &actions)
        case .ready(let c):
            handleReady(c, now: now, into: &actions)
        case .failed(let c, let failure):
            handleFailure(c, failure, now: now, into: &actions)
        case .refused(let c, let refusal):
            if inFlight[c] != nil {
                finish(&actions, with: .fail(refusal == .notPaired ? .notPaired : .unsupportedVersion))
            }
        }
        if finished { return actions }

        expireTimeouts(now: now, into: &actions)
        if finished { return actions }

        if let since = relayReadySince, now >= since + RaceTiming.preferDirectGraceMs {
            win(.relay, into: &actions)
            return actions
        }

        pump(now: now, into: &actions)
        settleIfExhausted(now: now, into: &actions)
        return actions
    }

    // MARK: Start

    private mutating func begin(now: Int) {
        begun = true
        t0 = now
        for c in plan.direct where known.insert(c).inserted {
            queue.append((c, t0 + directCount * RaceTiming.directStaggerMs))
            directCount += 1
        }
        if relayIncluded {
            let immediate = plan.direct.isEmpty || !plan.hasWifiOrWired
            queue.append((.relay, t0 + (immediate ? 0 : RaceTiming.relayDelayMs)))
            known.insert(.relay)
        }
    }

    private mutating func addDiscovered(_ found: [RaceCandidate], now: Int) {
        guard plan.mode != .pairing else { return }
        for c in found where !c.isRelay && discovered < RaceTiming.maxMdnsCandidates && known.insert(c).inserted {
            discovered += 1
            queue.append((c, max(now, t0 + directCount * RaceTiming.directStaggerMs)))
            directCount += 1
        }
    }

    private mutating func pump(now: Int, into actions: inout [RaceAction]) {
        var index = 0
        while index < queue.count {
            let item = queue[index]
            let directInFlight = inFlight.keys.filter { !$0.isRelay }.count
            let blocked = inFlight.count >= maxParallel || (!item.candidate.isRelay && directInFlight >= RaceTiming.maxDirectParallel)
            if item.dueAt <= now && !blocked {
                queue.remove(at: index)
                inFlight[item.candidate] = Attempt(startedAt: now)
                startOrder.append(item.candidate)
                attempted += 1
                actions.append(.start(item.candidate))
            } else {
                index += 1
            }
        }
    }

    // MARK: Events

    private mutating func handleTLSReady(_ c: RaceCandidate, into actions: inout [RaceAction]) {
        guard inFlight[c] != nil else { return }
        pinProven = true
        if plan.mode == .pairing {
            win(c, into: &actions)
        } else {
            inFlight[c]?.tlsReady = true
        }
    }

    private mutating func handleReady(_ c: RaceCandidate, now: Int, into actions: inout [RaceAction]) {
        guard inFlight[c] != nil else { return }
        pinProven = true
        if !c.isRelay {
            win(c, into: &actions)
        } else if inFlight.contains(where: { !$0.key.isRelay && $0.value.tlsReady }) {
            relayReadySince = now
        } else {
            win(c, into: &actions)
        }
    }

    private mutating func handleFailure(_ c: RaceCandidate, _ failure: PathFailure, now: Int, into actions: inout [RaceAction]) {
        guard inFlight.removeValue(forKey: c) != nil else { return }
        startOrder.removeAll { $0 == c }
        if case .alertAfterReady = failure {
            finish(&actions, with: plan.mode == .pairing ? .expired : .fail(.notPaired))
            return
        }
        record(c, failure, now: now)
        // A relay that sent `ready` and then died is no longer a winner.
        if c.isRelay { relayReadySince = nil }
        // The direct attempt a relay `ready` was waiting for is gone.
        if relayReadySince != nil, !inFlight.contains(where: { !$0.key.isRelay && $0.value.tlsReady }) {
            win(.relay, into: &actions)
        }
    }

    private mutating func record(_ c: RaceCandidate, _ failure: PathFailure, now: Int) {
        failures[c] = failure
        if failure == .timeout { anyTimeout = true }
        guard c.isRelay else { return }
        let refusal: RelayRefusal
        switch failure {
        case .relay(let r): refusal = r
        case .timeout: refusal = .timeout
        default: refusal = .other(0)
        }
        if refusal == .timeout { anyTimeout = true }
        let wall = plan.wallClock.addingTimeInterval(TimeInterval(now - t0) / 1000)
        relayPolicy.record(refusal, now: wall, storedAt: plan.relayStoredAt, url: plan.relayURL)
    }

    private func timeout(for c: RaceCandidate) -> Int {
        c.isRelay ? RaceTiming.relayAttemptTimeoutMs : RaceTiming.directAttemptTimeoutMs
    }

    private mutating func expireTimeouts(now: Int, into actions: inout [RaceAction]) {
        if now >= t0 + RaceTiming.raceTimeoutMs {
            for c in inFlight.keys { failures[c] = failures[c] ?? .timeout }
            if !inFlight.isEmpty { anyTimeout = true }
            let exhausted = classifyExhausted()
            finish(&actions, with: exhausted)
            return
        }
        for c in startOrder where inFlight[c].map({ now >= $0.startedAt + timeout(for: c) }) == true {
            inFlight.removeValue(forKey: c)
            actions.append(.cancel(c))
            record(c, .timeout, now: now)
            if c.isRelay { relayReadySince = nil }
        }
        startOrder.removeAll { inFlight[$0] == nil }
        if relayReadySince != nil, !inFlight.contains(where: { !$0.key.isRelay && $0.value.tlsReady }) {
            win(.relay, into: &actions)
        }
    }

    // MARK: Outcomes

    private mutating func win(_ c: RaceCandidate, into actions: inout [RaceAction]) {
        winner = c
        if c.isRelay { relayPolicy.recordSuccess() }
        for other in startOrder where other != c && inFlight[other] != nil { actions.append(.cancel(other)) }
        inFlight = [:]
        startOrder = []
        queue = []
        relayReadySince = nil
        finished = true
        actions.append(.win(c))
    }

    private mutating func finish(_ actions: inout [RaceAction], with outcome: RaceAction) {
        for c in startOrder where inFlight[c] != nil { actions.append(.cancel(c)) }
        inFlight = [:]
        startOrder = []
        queue = []
        relayReadySince = nil
        finished = true
        actions.append(outcome)
    }

    private mutating func settleIfExhausted(now: Int, into actions: inout [RaceAction]) {
        guard inFlight.isEmpty, queue.isEmpty, attempted > 0 else { return }
        finish(&actions, with: classifyExhausted())
    }

    /// Every candidate failed (or the race timed out).
    private func classifyExhausted() -> RaceAction {
        if plan.mode == .pairing, failures.values.contains(where: { if case .tlsBeforeReady = $0 { return true } else { return false } }) {
            return .expired
        }

        // Every candidate that reached the certificate stage showed the same
        // wrong key, and the relay is among them whenever the daemon has one.
        var mismatched: [RaceCandidate: Data] = [:]
        for (c, f) in failures { if case .keyMismatch(let spki) = f { mismatched[c] = spki } }
        if !pinProven, !mismatched.isEmpty, Set(mismatched.values).count == 1, !plan.hasRelay || mismatched[.relay] != nil {
            return .fail(.keyChanged)
        }
        if !plan.monitorSatisfied { return .fail(.offline) }
        if relayIncluded, plan.direct.isEmpty, directCount == 0, case .relay(let r)? = failures[.relay] {
            switch r {
            case .daemonOffline, .busy, .timeout: return .fail(.relayUnavailable)
            default: break
            }
        }
        return .fail(anyTimeout ? .timeout : .unreachable)
    }
}
