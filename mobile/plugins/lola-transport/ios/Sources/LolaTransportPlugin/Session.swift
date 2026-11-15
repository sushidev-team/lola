import Foundation
import LolaTransportCore
import Network

/// A daemon's refusal read from an `err` frame before `ready`.
struct SessionRefusal: Equatable {
    var code: String
    var minV: Int?
    var maxV: Int?
}

/// Why an authenticated session ended.
struct SessionEnd: Error {
    var code: FailureCode
    var reason: String
}

/// One byte stream to the daemon, from TLS to the end of the connection: it
/// frames, authenticates (the first frame must be `ready`), keeps the native
/// frames native (`InboundFilter`), coalesces inbound frames for the bridge and
/// watches writes. It is also the race attempt for one candidate.
///
/// A winner that is not yet adopted (a migration target) holds its inbound
/// frames until `activate()`, so no frame reaches JS under an epoch JS has not
/// accepted. Everything runs on one serial queue.
final class Session: RaceAttempt {
    private static let writeTimeoutMs = 15_000
    private static let maxHeldBytes = 8 << 20

    let candidate: RaceCandidate
    let path: LolaPath
    private let queue: DispatchQueue
    private let daemonId: String
    private var filter: InboundFilter
    private var decoder = FrameDecoder()
    private var report: ((AttemptReport) -> Void)?
    private var closed = false
    private var active = false

    private(set) var ready: ReadyInfo?
    /// False once the session ended or was cancelled; a closed session never wins.
    var isOpen: Bool { !closed }
    private(set) var refusal: SessionRefusal?
    private(set) var framesIn = 0
    private(set) var framesOut = 0
    private(set) var bytesIn = 0
    private(set) var bytesOut = 0

    private var batch: [String] = []
    private var batchBytes = 0
    private var flushScheduled = false
    private var outstandingWrites = 0
    private var writeWatchdog = 0

    /// Delivered frames (batched), only after `activate()`.
    var onFrames: (([String]) -> Void)?
    /// A credential the daemon issued; the owner saves it.
    var onCredentials: ((RelayCredentials) -> Void)?
    /// The session ended after authentication.
    var onEnded: ((SessionEnd) -> Void)?
    /// A direct path lost viability or a better one exists.
    var onPathChange: (() -> Void)?

    init(candidate: RaceCandidate, path: LolaPath, queue: DispatchQueue, daemonId: String, filter: InboundFilter) {
        self.candidate = candidate
        self.path = path
        self.queue = queue
        self.daemonId = daemonId
        self.filter = filter
    }

    // MARK: RaceAttempt

    func start(report: @escaping (AttemptReport) -> Void) {
        self.report = report
        path.start(on: queue) { [weak self] event in self?.handle(event) }
    }

    /// Silent: no event is delivered for a session its owner closed.
    func cancel() {
        guard !closed else { return }
        closed = true
        report = nil
        onEnded = nil
        onFrames = nil
        writeWatchdog += 1
        path.cancel()
    }

    // MARK: Path events

    private func handle(_ event: PathEvent) {
        guard !closed else { return }
        switch event {
        case .tlsReady:
            report?(.tlsReady)
            receive()
        case .failed(let failure):
            guard ready != nil else {
                report?(.failed(failure))
                return
            }
            if case .alertAfterReady = failure {
                end(.notPaired, "the daemon refused the certificate")
            } else {
                end(.peerClosed, "the connection failed")
            }
        case .viabilityChanged(let viable):
            if !viable, ready != nil { onPathChange?() }
        case .betterPathAvailable:
            if ready != nil { onPathChange?() }
        }
    }

    // MARK: Read

    private func receive() {
        path.receive { [weak self] data, eof, error in
            guard let self, !self.closed else { return }
            if let data, !data.isEmpty {
                self.ingest(data)
                guard !self.closed else { return }
            }
            if error != nil || eof {
                self.flushNow()
                self.connectionEnded(TLSStream.classifyAfterReady(error))
                return
            }
            self.receive()
        }
    }

    /// Reads start after `.tlsReady`, so a TLS error here is the pinned
    /// daemon's alert (an unpaired or revoked key); a bare EOF before `ready`
    /// is not authenticated and fails this candidate only.
    private func connectionEnded(_ failure: PathFailure) {
        guard ready != nil else {
            report?(.failed(failure))
            return
        }
        if case .alertAfterReady = failure {
            end(.notPaired, "the daemon refused the certificate")
        } else {
            end(.peerClosed, "the daemon closed the connection")
        }
    }

    private func ingest(_ data: Data) {
        bytesIn += data.count
        decoder.push(data)
        var bodies: [Data] = []
        var framingFailure = false
        while true {
            do {
                guard let body = try decoder.next() else { break }
                bodies.append(body)
            } catch {
                framingFailure = true
                break
            }
        }
        framesIn += bodies.count

        for body in bodies {
            if ready == nil {
                guard authenticate(body) else { return }
            } else if !route([body]) {
                return
            }
        }
        if framingFailure {
            flushNow()
            if ready == nil {
                report?(.failed(.network("framing error before ready")))
            } else {
                end(.protocolViolation, "the daemon sent a malformed frame")
            }
        }
    }

    /// The first frame is `ready` or a refusal. Returns false when the session
    /// must stop reading.
    private func authenticate(_ body: Data) -> Bool {
        if let error = Self.refusal(in: body) {
            refusal = error
            switch error.code {
            case "not_paired": report?(.refused(.notPaired))
            case "unsupported_version": report?(.refused(.unsupportedVersion))
            default: report?(.failed(.network("refused: \(error.code)")))
            }
            return false
        }
        // Nothing else may precede `ready`: an unauthenticated frame never
        // reaches JS.
        guard Self.frameType(of: body) == "ready" else {
            report?(.failed(.network("unexpected first frame")))
            return false
        }
        return route([body])
    }

    private static func frameType(of body: Data) -> String? {
        struct Head: Decodable { let type: String }
        return (try? JSONDecoder().decode(Head.self, from: body))?.type
    }

    /// Runs frames through the filter. Returns false when the session ended.
    private func route(_ bodies: [Data]) -> Bool {
        let output = filter.filter(bodies, now: Date())
        for effect in output.effects {
            switch effect {
            case .device(let info):
                ready = info
                report?(.ready)
            case .saveCredentials(let credentials):
                onCredentials?(credentials)
            case .send(let request):
                if let framed = try? FrameEncoder.encode(body: request) {
                    framesOut += 1
                    write(framed) { _ in }
                }
            }
            if closed { return false }
        }
        var texts: [String] = []
        for frame in output.frames {
            guard let text = String(data: frame, encoding: .utf8) else {
                end(.protocolViolation, "the daemon sent a frame that is not valid UTF-8")
                return false
            }
            texts.append(text)
        }
        enqueue(texts)
        return !closed
    }

    private static func refusal(in body: Data) -> SessionRefusal? {
        struct Frame: Decodable {
            struct Payload: Decodable { let code: String?; let minV: Int?; let maxV: Int? }
            let type: String
            let payload: Payload?
        }
        guard let frame = try? JSONDecoder().decode(Frame.self, from: body), frame.type == "err" else { return nil }
        return SessionRefusal(code: frame.payload?.code ?? "unknown", minV: frame.payload?.minV, maxV: frame.payload?.maxV)
    }

    // MARK: Delivery (coalesced on the daemon's own 16 ms tick)

    /// The winner starts delivering. Frames held until now go out first.
    func activate() {
        guard !closed else { return }
        active = true
        flushNow()
    }

    private func enqueue(_ frames: [String]) {
        guard !frames.isEmpty else { return }
        batch.append(contentsOf: frames)
        for frame in frames { batchBytes += frame.utf8.count }
        guard active else {
            if batchBytes > Self.maxHeldBytes { end(.protocolViolation, "too many frames before adoption") }
            return
        }
        if batchBytes >= Limits.maxBatchBytes {
            flushNow()
        } else if !flushScheduled {
            flushScheduled = true
            queue.asyncAfter(deadline: .now() + .milliseconds(Limits.flushIntervalMs)) { [weak self] in
                guard let self, !self.closed else { return }
                self.flushScheduled = false
                self.flushNow()
            }
        }
    }

    private func flushNow() {
        guard active, !batch.isEmpty else { return }
        let out = batch
        batch = []
        batchBytes = 0
        onFrames?(out)
    }

    // MARK: Write

    func send(bodies: [String], completion: @escaping (Result<Void, SessionEnd>) -> Void) {
        guard !closed, ready != nil else {
            return completion(.failure(SessionEnd(code: .clientClosed, reason: "no connection is established")))
        }
        guard !bodies.isEmpty else { return completion(.success(())) }
        let framed: Data
        do {
            framed = try FrameEncoder.encode(jsonBodies: bodies)
        } catch {
            // A caller's bug: refuse the call and leave the connection alone.
            return completion(.failure(SessionEnd(code: .protocolViolation, reason: "the frames cannot be encoded")))
        }
        framesOut += bodies.count
        write(framed) { [weak self] error in
            if let error {
                completion(.failure(SessionEnd(code: .peerClosed, reason: TLSStream.describe(error))))
                self?.end(.peerClosed, "a write failed")
            } else {
                completion(.success(()))
            }
        }
    }

    private func write(_ framed: Data, completion: @escaping (NWError?) -> Void) {
        bytesOut += framed.count
        outstandingWrites += 1
        armWriteWatchdog()
        path.send(framed) { [weak self] error in
            guard let self, !self.closed else { return }
            self.outstandingWrites -= 1
            if self.outstandingWrites == 0 { self.writeWatchdog += 1 }
            completion(error)
        }
    }

    /// Per session, not per write: fires when something has been outstanding for
    /// the whole budget without the transport accepting anything.
    private func armWriteWatchdog() {
        writeWatchdog += 1
        let generation = writeWatchdog
        queue.asyncAfter(deadline: .now() + .milliseconds(Self.writeTimeoutMs)) { [weak self] in
            guard let self, !self.closed, self.writeWatchdog == generation, self.outstandingWrites > 0 else { return }
            self.end(.timeout, "a write was not accepted in time")
        }
    }

    // MARK: End

    /// A session whose race is still open reports the end to it as well, so an
    /// attempt that sent `ready` and then died can never be declared the winner.
    private func end(_ code: FailureCode, _ reason: String) {
        guard !closed else { return }
        flushNow()
        let callback = onEnded
        let race = report
        cancel()
        race?(.failed(Self.raceFailure(code, reason)))
        callback?(SessionEnd(code: code, reason: reason))
    }

    private static func raceFailure(_ code: FailureCode, _ reason: String) -> PathFailure {
        switch code {
        case .notPaired: return .alertAfterReady(reason)
        case .timeout: return .timeout
        default: return .network(reason)
        }
    }
}
