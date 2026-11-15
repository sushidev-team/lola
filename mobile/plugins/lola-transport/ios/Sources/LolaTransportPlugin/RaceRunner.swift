import Foundation
import LolaTransportCore

/// What an attempt reports to the race.
enum AttemptReport {
    case tlsReady
    case ready
    case failed(PathFailure)
    case refused(DaemonRefusal)
}

/// One candidate's connection attempt: a `Session` when connecting, a pairing
/// attempt when pairing.
protocol RaceAttempt: AnyObject {
    func start(report: @escaping (AttemptReport) -> Void)
    func cancel()
}

enum RaceOutcome {
    case won(RaceCandidate, RaceAttempt)
    case failed(FailureCode)
    case expired
}

/// Drives a pure `PathRace` with real attempts: feeds it events, applies its
/// actions, and wakes it when its next deadline falls due. Everything runs on
/// `queue`, a serial queue shared with the attempts.
final class RaceRunner {
    private var race: PathRace
    private let queue: DispatchQueue
    private let make: (RaceCandidate) -> RaceAttempt?
    private var outcome: ((RaceOutcome, RelayPolicy) -> Void)?
    private var attempts: [RaceCandidate: RaceAttempt] = [:]
    private var timer: DispatchWorkItem?
    private var begun = false

    /// `make` returns nil for a candidate that cannot start (missing credential).
    init(
        plan: RacePlan, queue: DispatchQueue, make: @escaping (RaceCandidate) -> RaceAttempt?,
        outcome: @escaping (RaceOutcome, RelayPolicy) -> Void
    ) {
        race = PathRace(plan: plan)
        self.queue = queue
        self.make = make
        self.outcome = outcome
    }

    static func now() -> Int { Int(DispatchTime.now().uptimeNanoseconds / 1_000_000) }

    func begin() {
        guard !begun else { return }
        begun = true
        feed(.begin)
    }

    func discovered(_ candidates: [RaceCandidate]) { feed(.discovered(candidates)) }

    /// Ends the race silently: attempts are cancelled, no outcome is delivered.
    func cancel() {
        outcome = nil
        timer?.cancel()
        for attempt in attempts.values { attempt.cancel() }
        attempts = [:]
    }

    private func feed(_ event: RaceEvent) {
        guard outcome != nil else { return }
        var failed: [RaceCandidate] = []
        for action in race.step(now: Self.now(), event: event) {
            switch action {
            case .start(let candidate):
                guard let attempt = make(candidate) else {
                    failed.append(candidate)
                    continue
                }
                attempts[candidate] = attempt
                attempt.start { [weak self, weak attempt] report in
                    guard let self, let attempt, self.attempts[candidate] === attempt else { return }
                    self.feed(Self.event(for: candidate, report))
                }
            case .cancel(let candidate):
                attempts.removeValue(forKey: candidate)?.cancel()
            case .win(let candidate):
                guard let attempt = attempts.removeValue(forKey: candidate) else { continue }
                finish(.won(candidate, attempt))
            case .fail(let code):
                finish(.failed(code))
            case .expired:
                finish(.expired)
            }
        }
        for candidate in failed {
            queue.async { [weak self] in self?.feed(.failed(candidate, .network("unavailable"))) }
        }
        schedule()
    }

    private static func event(for candidate: RaceCandidate, _ report: AttemptReport) -> RaceEvent {
        switch report {
        case .tlsReady: return .tlsReady(candidate)
        case .ready: return .ready(candidate)
        case .failed(let failure): return .failed(candidate, failure)
        case .refused(let refusal): return .refused(candidate, refusal)
        }
    }

    private func finish(_ result: RaceOutcome) {
        guard let deliver = outcome else { return }
        outcome = nil
        timer?.cancel()
        for attempt in attempts.values { attempt.cancel() }
        attempts = [:]
        deliver(result, race.relayPolicy)
    }

    private func schedule() {
        timer?.cancel()
        guard outcome != nil, let wake = race.nextWake else { return }
        let work = DispatchWorkItem { [weak self] in self?.feed(.tick) }
        timer = work
        queue.asyncAfter(deadline: .now() + .milliseconds(max(0, wake - Self.now())), execute: work)
    }
}
