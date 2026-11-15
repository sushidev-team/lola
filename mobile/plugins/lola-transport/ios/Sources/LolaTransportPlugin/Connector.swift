import Foundation
import LolaTransportCore
import Network

#if canImport(UIKit)
    import UIKit
#endif

/// Owns the connection to one paired daemon (04 §1.4, §1.6, §1.7): races every
/// known path, keeps the winner as the live epoch, re-races on its own while
/// the app is foreground, and migrates to a better path without JS noticing
/// more than an `adopt`.
///
/// The decisions live in Core (`PathRace`, `ReconnectPolicy`, `RelayPolicy`);
/// this type feeds them real attempts and clocks. Everything runs on `queue`.
///
/// An EPOCH is bumped at every race start and every migration. Network
/// framework delivers callbacks already scheduled when a connection is
/// cancelled, so every callback is tied to a session object that is checked
/// against the live one before it acts.
final class Connector {
    struct Connected {
        let epoch: Int
        let path: PathInfo
        let raceMs: Int
        let device: ReadyInfo
    }

    struct Failure: Error {
        var code: FailureCode
        var reason: String
        var daemonCode: String?
        var minV: Int?
        var maxV: Int?
    }

    struct Status {
        var epoch: Int
        var phase: ConnectionPhase
        var path: PathInfo?
        var relay: RelayState
        var framesIn: Int
        var framesOut: Int
        var bytesIn: Int
        var bytesOut: Int
        var launchedAt: Int
    }

    private struct Active {
        var epoch: Int
        var session: Session
        var candidate: RaceCandidate
        var info: PathInfo
    }

    private final class Cold {
        let epoch: Int
        let startedAt = RaceRunner.now()
        var runner: RaceRunner?
        var sessions: [Session] = []
        var stopDiscovery: (() -> Void)?
        var completion: ((Result<Connected, Failure>) -> Void)?
        init(epoch: Int) { self.epoch = epoch }
    }

    private final class Migration {
        var runner: RaceRunner?
        var sessions: [Session] = []
        var stopDiscovery: (() -> Void)?
        var pending: (session: Session, epoch: Int, candidate: RaceCandidate)?
        var adoptTimer: DispatchWorkItem?
    }

    let queue: DispatchQueue
    private let monitor: PathMonitor
    private let launchedAt = Int(Date().timeIntervalSince1970 * 1000)

    /// Batched inbound frames and the epoch they belong to.
    var onFrames: (([String], Int) -> Void)?
    var onState: ((ConnectionState) -> Void)?
    var onDevice: ((Int, ReadyInfo) -> Void)?
    /// The APP's foreground/background transitions, not the socket's. The
    /// plugin reports and the app decides what they mean.
    var onAppState: ((Bool) -> Void)?

    private var target: String?
    private var record: PairingRecord?
    private var credentials: RelayCredentials?
    private var policy = RelayPolicy()
    private var epoch = 0
    private var phase: ConnectionPhase = .idle
    private var foreground = true
    private var active: Active?
    private var cold: Cold?
    private var migration: Migration?
    private var reconnect = ReconnectPolicy()
    private var retry: DispatchWorkItem?
    private var lastMigrationAt = Int.min / 2
    private var observers: [NSObjectProtocol] = []

    init(queue: DispatchQueue, monitor: PathMonitor) {
        self.queue = queue
        self.monitor = monitor
        monitor.onChange = { [weak self] snapshot in self?.pathChanged(snapshot) }
        monitor.start()
        observeLifecycle()
    }

    deinit {
        for token in observers { NotificationCenter.default.removeObserver(token) }
    }

    // MARK: Public surface

    func connect(daemonId: String, completion: @escaping (Result<Connected, Failure>) -> Void) {
        queue.async {
            self.stopEverything(closeCode: .clientClosed)
            guard
                let record = (try? PairingRecord.load(daemonId: daemonId)) ?? nil,
                ClientIdentity.identity(daemonId: daemonId) != nil
            else {
                return completion(
                    .failure(Failure(code: .notPaired, reason: "this phone is not paired with that Mac")))
            }
            self.target = daemonId
            self.record = record
            self.credentials = (try? RelayCredentials.load(daemonId: daemonId)) ?? nil
            self.policy = RelayPolicy(
                state: record.relay == .none ? .ok : record.relay, retryAt: record.relayRetryAt)
            self.reconnect.reset()
            self.beginCold(completion)
        }
    }

    func disconnect(reason: String?) {
        queue.async {
            self.target = nil
            self.stopEverything(closeCode: .clientClosed)
        }
    }

    func send(epoch: Int, bodies: [String], completion: @escaping (Result<Void, Failure>) -> Void) {
        queue.async {
            guard let active = self.active, active.epoch == epoch else {
                return completion(.failure(Failure(code: .clientClosed, reason: "that connection is closed")))
            }
            active.session.send(bodies: bodies) { result in
                switch result {
                case .success: completion(.success(()))
                case .failure(let end): completion(.failure(Failure(code: end.code, reason: end.reason)))
                }
            }
        }
    }

    /// JS accepted a migration the plugin announced. `completion` says whether
    /// that epoch is now the live one; a migration that died before it was
    /// adopted leaves the old epoch live and answers false.
    func adopt(epoch: Int, completion: @escaping (Bool) -> Void) {
        queue.async {
            self.adoptPending(epoch: epoch)
            completion(self.active?.epoch == epoch)
        }
    }

    func status(_ completion: @escaping (Status) -> Void) {
        queue.async {
            let session = self.active?.session
            completion(
                Status(
                    epoch: self.active?.epoch ?? self.epoch, phase: self.phase, path: self.active?.info,
                    relay: self.relayState, framesIn: session?.framesIn ?? 0, framesOut: session?.framesOut ?? 0,
                    bytesIn: session?.bytesIn ?? 0, bytesOut: session?.bytesOut ?? 0, launchedAt: self.launchedAt))
        }
    }

    /// Closes the connection if it is to this daemon (a forgotten pairing).
    func forget(daemonId: String, completion: @escaping () -> Void) {
        queue.async {
            if self.target == daemonId {
                self.target = nil
                self.stopEverything(closeCode: .clientClosed)
            }
            completion()
        }
    }

    // MARK: Cold race

    private var relayState: RelayState { credentials == nil ? .none : policy.state }

    private func beginCold(_ completion: ((Result<Connected, Failure>) -> Void)?) {
        guard let record, let identity = ClientIdentity.identity(daemonId: record.daemonId) else {
            return failCold(
                epoch: epoch, code: .notPaired, refusal: nil, completion: completion)
        }
        epoch += 1
        let cold = Cold(epoch: epoch)
        cold.completion = completion
        self.cold = cold
        phase = .racing
        emit(ConnectionState(epoch: epoch, phase: .racing, relay: relayState))

        let runner = makeRunner(
            mode: .cold, directOnly: false, record: record, identity: identity,
            track: { cold.sessions.append($0) },
            outcome: { [weak self] outcome, policy in self?.coldFinished(cold, outcome, policy) })
        cold.runner = runner
        cold.stopDiscovery = discover(into: runner)
        runner.begin()
    }

    private func coldFinished(_ cold: Cold, _ outcome: RaceOutcome, _ newPolicy: RelayPolicy) {
        guard self.cold === cold else { return }
        self.cold = nil
        cold.stopDiscovery?()
        policy = newPolicy
        persistRelay()
        switch outcome {
        case .won(let candidate, let attempt):
            guard let session = attempt as? Session, session.ready != nil else {
                return failCold(epoch: cold.epoch, code: .internalError, refusal: nil, completion: cold.completion)
            }
            guard session.isOpen else {
                return failCold(epoch: cold.epoch, code: .peerClosed, refusal: nil, completion: cold.completion)
            }
            activate(
                session, candidate: candidate, epoch: cold.epoch, raceMs: RaceRunner.now() - cold.startedAt,
                completion: cold.completion)
        case .failed(let code):
            failCold(
                epoch: cold.epoch, code: code, refusal: cold.sessions.compactMap(\.refusal).first,
                completion: cold.completion)
        case .expired:
            failCold(epoch: cold.epoch, code: .notPaired, refusal: nil, completion: cold.completion)
        }
    }

    private func failCold(
        epoch: Int, code: FailureCode, refusal: SessionRefusal?,
        completion: ((Result<Connected, Failure>) -> Void)?
    ) {
        phase = .failed
        emit(
            ConnectionState(
                epoch: epoch, phase: .failed, code: code, daemonCode: refusal?.code, minV: refusal?.minV,
                maxV: refusal?.maxV, relay: relayState))
        guard let completion else { return scheduleRetry(after: code) }
        // A failed `connect()` belongs to its caller, which has already let go
        // of this connection: the plugin keeps no target to retry for nobody.
        target = nil
        completion(
            .failure(
                Failure(
                    code: code, reason: Self.reason(code), daemonCode: refusal?.code, minV: refusal?.minV,
                    maxV: refusal?.maxV)))
    }

    /// Retries on the ladder while the app is foreground; never in the
    /// background, and never for a terminal code.
    private func scheduleRetry(after code: FailureCode) {
        retry?.cancel()
        retry = nil
        guard code.isRetryable, foreground, target != nil else { return }
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.retry = nil
            guard self.foreground, self.target != nil, self.active == nil, self.cold == nil else { return }
            self.beginCold(nil)
        }
        retry = work
        queue.asyncAfter(deadline: .now() + .milliseconds(reconnect.nextConnectDelayMs()), execute: work)
    }

    // MARK: Winner

    private func activate(
        _ session: Session, candidate: RaceCandidate, epoch: Int, raceMs: Int?,
        completion: ((Result<Connected, Failure>) -> Void)?
    ) {
        guard let ready = session.ready, session.isOpen else { return }
        let info = Self.pathInfo(session.path.info, candidate: candidate)
        active = Active(epoch: epoch, session: session, candidate: candidate, info: info)
        phase = .ready
        reconnect.reset()
        session.onFrames = { [weak self, weak session] frames in
            guard let self, let session, self.active?.session === session else { return }
            self.onFrames?(frames, epoch)
        }
        recordSuccess(candidate)
        emit(ConnectionState(epoch: epoch, phase: .ready, path: info, raceMs: raceMs, relay: relayState))
        onDevice?(epoch, ready)
        completion?(.success(Connected(epoch: epoch, path: info, raceMs: raceMs ?? 0, device: ready)))
        session.activate()
    }

    private func recordSuccess(_ candidate: RaceCandidate) {
        guard let daemonId = record?.daemonId else { return }
        PairingStore.setLastPath(candidate.isRelay ? .relay : .direct, daemonId: daemonId)
        persist { record in
            if !candidate.isRelay { record.lastGood = candidate.hostPort }
        }
        persistRelay()
    }

    // MARK: Session events

    private func sessionEnded(_ session: Session, _ end: SessionEnd) {
        if let pending = migration?.pending, pending.session === session {
            clearMigration()
            // The announced epoch never became live; the old one still is.
            emit(ConnectionState(epoch: pending.epoch, phase: .closed, code: end.code, relay: relayState))
            return
        }
        guard let current = active, current.session === session else { return }
        active = nil
        clearMigration()
        let retryable = end.code.isRetryable
        phase = retryable ? .closed : .failed
        emit(ConnectionState(epoch: current.epoch, phase: phase, code: end.code, relay: relayState))
        // A dropped live connection re-races at once.
        reconnect.reset()
        scheduleRetry(after: end.code)
    }

    private func sessionPathChanged(_ session: Session) {
        guard let current = active, current.session === session, !current.candidate.isRelay else { return }
        startMigration(directOnly: false)
    }

    private func storeCredentials(_ credentials: RelayCredentials) {
        guard let daemonId = record?.daemonId else { return }
        do { try credentials.save(daemonId: daemonId) } catch {
            LolaLog.warn("relay credential could not be saved")
            return
        }
        self.credentials = credentials
        policy.credentialStored()
        persistRelay()
    }

    // MARK: Migration

    private func pathChanged(_ snapshot: PathMonitor.Snapshot) {
        guard let current = active else { return retryOnPathChange(snapshot) }
        guard current.candidate.isRelay, snapshot.hasWifiOrWired else { return }
        startMigration(directOnly: true)
    }

    /// A usable network came back while the plugin waits to retry: the ladder
    /// starts over and the race runs now (04 §1.6).
    private func retryOnPathChange(_ snapshot: PathMonitor.Snapshot) {
        guard snapshot.satisfied, foreground, target != nil, cold == nil, retry != nil else { return }
        retry?.cancel()
        retry = nil
        reconnect.reset()
        beginCold(nil)
    }

    private func startMigration(directOnly: Bool) {
        let now = RaceRunner.now()
        guard
            foreground, active != nil, migration == nil, now - lastMigrationAt >= Limits.migrateDrainMs,
            let record, let identity = ClientIdentity.identity(daemonId: record.daemonId)
        else { return }
        lastMigrationAt = now
        let migration = Migration()
        self.migration = migration
        let runner = makeRunner(
            mode: .migration, directOnly: directOnly, record: record, identity: identity,
            track: { migration.sessions.append($0) },
            outcome: { [weak self] outcome, policy in self?.migrationFinished(migration, outcome, policy) })
        migration.runner = runner
        migration.stopDiscovery = discover(into: runner)
        runner.begin()
    }

    private func migrationFinished(_ migration: Migration, _ outcome: RaceOutcome, _ newPolicy: RelayPolicy) {
        guard self.migration === migration else { return }
        migration.stopDiscovery?()
        policy = newPolicy
        persistRelay()
        guard case .won(let candidate, let attempt) = outcome, let session = attempt as? Session,
            session.ready != nil, session.isOpen
        else {
            self.migration = nil
            return
        }
        epoch += 1
        let next = epoch
        migration.pending = (session, next, candidate)
        emit(
            ConnectionState(
                epoch: next, phase: .migrating, path: Self.pathInfo(session.path.info, candidate: candidate),
                relay: relayState))
        // JS adopts once its mutating requests have drained; if it stays
        // silent the plugin adopts on its own.
        let timer = DispatchWorkItem { [weak self] in self?.adoptPending(epoch: next) }
        migration.adoptTimer = timer
        queue.asyncAfter(deadline: .now() + .milliseconds(Limits.migrateDrainMs), execute: timer)
    }

    private func adoptPending(epoch adopted: Int) {
        guard let migration, let pending = migration.pending, pending.epoch == adopted else { return }
        guard pending.session.isOpen else { return clearMigration() }
        migration.adoptTimer?.cancel()
        self.migration = nil
        let old = active?.session
        active = nil
        old?.cancel()
        activate(pending.session, candidate: pending.candidate, epoch: adopted, raceMs: nil, completion: nil)
    }

    private func clearMigration() {
        guard let migration else { return }
        self.migration = nil
        migration.adoptTimer?.cancel()
        migration.runner?.cancel()
        migration.stopDiscovery?()
        migration.pending?.session.cancel()
    }

    // MARK: Building a race

    private func makeRunner(
        mode: RacePlan.Mode, directOnly: Bool, record: PairingRecord, identity: SecIdentity,
        track: @escaping (Session) -> Void, outcome: @escaping (RaceOutcome, RelayPolicy) -> Void
    ) -> RaceRunner {
        let snapshot = monitor.current
        let credentials = self.credentials
        let plan = RacePlan(
            mode: mode,
            direct: RacePlan.directCandidates(lastGood: record.lastGood, addrs: record.addrs, port: record.port),
            hasRelay: credentials != nil && !directOnly, relayPolicy: policy, relayStoredAt: credentials?.storedAt,
            relayURL: credentials?.hubURL, wallClock: Date(), hasWifiOrWired: snapshot.hasWifiOrWired,
            monitorSatisfied: snapshot.satisfied)
        return RaceRunner(
            plan: plan, queue: queue,
            make: { [weak self] candidate in
                guard let self,
                    let session = self.makeSession(
                        candidate, record: record, identity: identity, credentials: credentials,
                        interface: snapshot.interface)
                else { return nil }
                track(session)
                return session
            },
            outcome: outcome)
    }

    private func makeSession(
        _ candidate: RaceCandidate, record: PairingRecord, identity: SecIdentity, credentials: RelayCredentials?,
        interface: NetworkInterface?
    ) -> Session? {
        let path: LolaPath?
        if candidate.isRelay {
            guard let credentials, let url = URL(string: credentials.hubURL) else { return nil }
            path = RelayPath(
                url: url, rv: credentials.rv, bearer: credentials.bearer, pin: record.pin, identity: identity,
                queue: queue)
        } else {
            path = DirectPath(
                host: candidate.host, port: candidate.port, pin: record.pin, identity: identity, interface: interface)
        }
        guard let path else { return nil }
        let session = Session(
            candidate: candidate, path: path, queue: queue, daemonId: record.daemonId,
            filter: InboundFilter(stored: credentials, credentialDead: policy.state == .credentialDead))
        session.onCredentials = { [weak self] in self?.storeCredentials($0) }
        session.onEnded = { [weak self, weak session] end in
            guard let session else { return }
            self?.sessionEnded(session, end)
        }
        session.onPathChange = { [weak self, weak session] in
            guard let session else { return }
            self?.sessionPathChanged(session)
        }
        return session
    }

    /// mDNS runs for the length of a race; results are hints appended as they arrive.
    private func discover(into runner: RaceRunner) -> () -> Void {
        LolaDiscovery.browse(timeout: TimeInterval(RaceTiming.raceTimeoutMs) / 1000) { [weak self, weak runner] found in
            self?.queue.async { runner?.discovered([.direct(found.host, port: found.port)]) }
        }
    }

    // MARK: Teardown

    /// Cancels every race and closes the live session, reporting the close.
    private func stopEverything(closeCode: FailureCode) {
        retry?.cancel()
        retry = nil
        if let cold {
            self.cold = nil
            cold.runner?.cancel()
            cold.stopDiscovery?()
            cold.completion?(.failure(Failure(code: closeCode, reason: Self.reason(closeCode))))
        }
        clearMigration()
        if let current = active {
            active = nil
            current.session.cancel()
            phase = .closed
            emit(ConnectionState(epoch: current.epoch, phase: .closed, code: closeCode, relay: relayState))
        } else if phase != .idle {
            phase = .closed
        }
    }

    // MARK: App lifecycle

    private func observeLifecycle() {
        #if canImport(UIKit)
            let center = NotificationCenter.default
            observers.append(
                center.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: nil) {
                    [weak self] _ in
                    guard let self else { return }
                    self.onAppState?(false)
                    self.queue.async {
                        self.foreground = false
                        // The target stays: `foreground` resumes it. An NWConnection
                        // has no background privileges, so a socket kept across
                        // suspension would only lie about being usable.
                        self.stopEverything(closeCode: .backgrounded)
                    }
                })
            // `willEnterForeground`, not `didBecomeActive`: the latter also
            // fires when a system dialog is dismissed, a moment the socket was
            // never touched.
            observers.append(
                center.addObserver(forName: UIApplication.willEnterForegroundNotification, object: nil, queue: nil) {
                    [weak self] _ in
                    guard let self else { return }
                    self.onAppState?(true)
                    self.queue.async {
                        self.foreground = true
                        guard self.target != nil, self.active == nil, self.cold == nil else { return }
                        self.reconnect.reset()
                        self.beginCold(nil)
                    }
                })
        #endif
    }

    // MARK: Helpers

    private func emit(_ state: ConnectionState) { onState?(state) }

    private func persist(_ change: (inout PairingRecord) -> Void) {
        guard var updated = record else { return }
        change(&updated)
        guard updated != record else { return }
        record = updated
        do { try updated.save() } catch { LolaLog.warn("pairing record could not be saved") }
    }

    private func persistRelay() {
        let state = relayState
        let retryAt = policy.retryAt
        let url = credentials?.hubURL
        persist { record in
            record.relayState = state.rawValue
            record.relayRetryAt = retryAt
            record.relayURL = url ?? record.relayURL
        }
    }

    /// The path the app sees: the candidate's address for direct, bare for relay.
    private static func pathInfo(_ base: PathInfo, candidate: RaceCandidate) -> PathInfo {
        candidate.isRelay ? PathInfo(kind: .relay) : base
    }

    static func reason(_ code: FailureCode) -> String {
        switch code {
        case .offline: return "this phone is offline"
        case .unreachable: return "no path to the Mac answered"
        case .timeout: return "the Mac did not answer in time"
        case .keyChanged: return "the Mac presented a different key"
        case .notPaired: return "this phone is not paired with the Mac"
        case .unsupportedVersion: return "the Mac speaks another protocol version"
        case .relayUnavailable: return "the relay could not reach the Mac"
        case .protocolViolation: return "the Mac sent an invalid frame"
        case .peerClosed: return "the Mac closed the connection"
        case .clientClosed: return "the connection was closed by the app"
        case .backgrounded: return "the app entered the background"
        case .internalError: return "internal error"
        }
    }
}
