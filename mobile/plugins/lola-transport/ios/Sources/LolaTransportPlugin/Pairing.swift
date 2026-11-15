import Foundation
import LolaTransportCore

/// A pairing's outcome, as JS sees it on the `pairing` listener.
struct PairingNotice {
    enum Outcome: String { case paired, refused, expired, failed, dropped }
    var pairRef: String
    var outcome: Outcome
    var daemonId: String?
    var name: String?
    var reason: String?
}

struct PairingFailure: Error {
    var code: String
    var message: String
}

/// The native half of pairing (04 §1.5). A scanned payload is held in memory
/// under a random `pairRef` (the secret and the relay token never cross the
/// bridge); `begin` races to ONE TLS session, writes `remote.pair` on it only,
/// resolves with the locally computed code, then reads the daemon's single
/// answer. Everything runs on `queue`.
final class Pairing {
    /// What `begin` needs after the secret is gone.
    private struct Summary {
        var daemonId: String
        var pin: String
        var addrs: [String]
        var port: Int
        var relayURL: String?
    }

    private struct Held {
        var payload: PairPayload
        var expiry: DispatchWorkItem
    }

    private final class Flow {
        let summary: Summary
        let created: ClientIdentity.Created
        var runner: RaceRunner?
        var path: LolaPath?
        var winner: RaceCandidate?
        var completion: ((Result<(code: String, path: PathInfo), PairingFailure>) -> Void)?
        var timeout: DispatchWorkItem?
        init(summary: Summary, created: ClientIdentity.Created) {
            self.summary = summary
            self.created = created
        }
    }

    private let queue: DispatchQueue
    private let monitor: PathMonitor
    private var held: [String: Held] = [:]
    private var flows: [String: Flow] = [:]

    var onNotice: ((PairingNotice) -> Void)?

    init(queue: DispatchQueue, monitor: PathMonitor) {
        self.queue = queue
        self.monitor = monitor
    }

    // MARK: Held payloads

    /// Holds a scanned payload for `PAIR_HOLD_MS` and returns its `pairRef`.
    func hold(_ payload: PairPayload) -> String {
        let ref = UUID().uuidString.lowercased()
        let expiry = DispatchWorkItem { [weak self] in self?.held.removeValue(forKey: ref) }
        held[ref] = Held(payload: payload, expiry: expiry)
        queue.asyncAfter(deadline: .now() + .milliseconds(Limits.pairHoldMs), execute: expiry)
        return ref
    }

    func cancel(pairRef: String) {
        if let entry = held.removeValue(forKey: pairRef) { entry.expiry.cancel() }
        guard let flow = flows.removeValue(forKey: pairRef) else { return }
        flow.timeout?.cancel()
        flow.runner?.cancel()
        flow.path?.cancel()
        ClientIdentity.discard(daemonId: flow.summary.daemonId)
        let completion = flow.completion
        flow.completion = nil
        completion?(.failure(PairingFailure(code: "client_closed", message: "pairing cancelled")))
    }

    // MARK: Begin

    func begin(
        pairRef: String, deviceName: String,
        completion: @escaping (Result<(code: String, path: PathInfo), PairingFailure>) -> Void
    ) {
        guard let payload = held[pairRef]?.payload, flows[pairRef] == nil else {
            return completion(.failure(PairingFailure(code: "expired", message: "the scanned code is no longer held")))
        }
        let daemonId = payload.daemonId
        // One pending identity per daemon: a newer scan of the same Mac wins.
        for (ref, other) in flows where other.summary.daemonId == daemonId { cancel(pairRef: ref) }
        let created: ClientIdentity.Created
        do {
            created = try ClientIdentity.generatePending(daemonId: daemonId)
        } catch {
            return completion(.failure(PairingFailure(code: "internal", message: "the phone key could not be created")))
        }
        let flow = Flow(
            summary: Summary(
                daemonId: daemonId, pin: payload.pin, addrs: payload.addrs, port: payload.port,
                relayURL: payload.relay?.url),
            created: created)
        flow.completion = completion
        flows[pairRef] = flow

        let snapshot = monitor.current
        let plan = RacePlan(
            mode: .pairing,
            direct: RacePlan.directCandidates(lastGood: nil, addrs: payload.addrs, port: payload.port),
            hasRelay: payload.relay != nil, relayURL: payload.relay?.url,
            hasWifiOrWired: snapshot.hasWifiOrWired, monitorSatisfied: snapshot.satisfied)

        flow.runner = RaceRunner(
            plan: plan, queue: queue,
            make: { [weak self] candidate in
                self?.attempt(for: candidate, pairRef: pairRef, flow: flow, interface: snapshot.interface)
            },
            outcome: { [weak self] outcome, _ in self?.raced(outcome, pairRef: pairRef, deviceName: deviceName, flow: flow) })
        flow.runner?.begin()
    }

    private func attempt(
        for candidate: RaceCandidate, pairRef: String, flow: Flow, interface: NetworkInterface?
    ) -> RaceAttempt? {
        guard let payload = held[pairRef]?.payload else { return nil }
        let path: LolaPath?
        if candidate.isRelay {
            guard let relay = payload.relay, let url = URL(string: relay.url) else { return nil }
            path = RelayPath(
                url: url, rv: relay.rv, bearer: relay.tok, pin: payload.pin, identity: flow.created.identity,
                queue: queue)
        } else {
            path = DirectPath(
                host: candidate.host, port: candidate.port, pin: payload.pin, identity: flow.created.identity,
                interface: interface)
        }
        return path.map { PairAttempt(path: $0, queue: queue) }
    }

    private func raced(_ outcome: RaceOutcome, pairRef: String, deviceName: String, flow: Flow) {
        guard flows[pairRef] === flow else { return }
        switch outcome {
        case .failed(let code):
            // The secret was not presented, so the payload stays held for a retry.
            reject(pairRef, flow, code: code == .timeout ? "timeout" : "unreachable", message: "the Mac did not answer")
        case .expired:
            held.removeValue(forKey: pairRef)?.expiry.cancel()
            reject(pairRef, flow, code: "expired", message: "the pairing window is closed")
        case .won(let candidate, let attempt):
            guard let pairAttempt = attempt as? PairAttempt, let payload = held[pairRef]?.payload else {
                return reject(pairRef, flow, code: "internal", message: "pairing state lost")
            }
            write(pairAttempt, candidate: candidate, payload: payload, pairRef: pairRef, deviceName: deviceName, flow: flow)
        }
    }

    private func write(
        _ attempt: PairAttempt, candidate: RaceCandidate, payload: PairPayload, pairRef: String,
        deviceName: String, flow: Flow
    ) {
        guard
            let proofSecret = attempt.exporter?(PairHandshake.proofExporterLabel, PairHandshake.exporterLength),
            let codeSecret = attempt.exporter?(PairHandshake.codeExporterLabel, PairHandshake.exporterLength),
            let daemonSPKI = attempt.path.daemonSPKIHash,
            let framed = try? FrameEncoder.encode(
                body: PairHandshake.body(
                    name: deviceName, proof: PairHandshake.proof(secret: payload.secret, exporter: proofSecret)))
        else {
            attempt.cancel()
            return reject(pairRef, flow, code: "internal", message: "the pairing proof could not be built")
        }
        let code = PairHandshake.code(exporterCode: codeSecret, daemonSPKI: daemonSPKI, clientSPKI: flow.created.spkiHash)
        flow.path = attempt.path
        flow.winner = candidate

        // The secret and the relay token are dropped when the write is issued,
        // success or not.
        held.removeValue(forKey: pairRef)?.expiry.cancel()
        attempt.path.send(framed) { [weak self] error in
            guard let self, self.flows[pairRef] === flow else { return }
            if error != nil {
                return self.reject(pairRef, flow, code: "unreachable", message: "the Mac closed the connection")
            }
            let completion = flow.completion
            flow.completion = nil
            completion?(.success((code, attempt.path.info)))
            self.awaitReply(pairRef: pairRef, flow: flow, decoder: FrameDecoder())
        }
    }

    // MARK: The daemon's single answer

    private func awaitReply(pairRef: String, flow: Flow, decoder: FrameDecoder) {
        if flow.timeout == nil {
            let timeout = DispatchWorkItem { [weak self] in self?.finish(pairRef, flow, .dropped, reason: "no answer") }
            flow.timeout = timeout
            queue.asyncAfter(deadline: .now() + .milliseconds(Limits.pairHoldMs), execute: timeout)
        }
        var decoder = decoder
        flow.path?.receive { [weak self] data, eof, error in
            guard let self, self.flows[pairRef] === flow else { return }
            if let data { decoder.push(data) }
            if let body = try? decoder.next() {
                return self.answered(PairHandshake.classify(body), pairRef: pairRef, flow: flow)
            }
            if error != nil || eof || decoder.poisoned {
                return self.finish(pairRef, flow, .dropped, reason: "the Mac closed the connection")
            }
            self.awaitReply(pairRef: pairRef, flow: flow, decoder: decoder)
        }
    }

    private func answered(_ reply: PairReply, pairRef: String, flow: Flow) {
        switch reply {
        case .paired(let result): paired(result, pairRef: pairRef, flow: flow)
        case .rejected: finish(pairRef, flow, .refused)
        case .expired, .notPaired: finish(pairRef, flow, .expired)
        case .failed(let reason): finish(pairRef, flow, .failed, reason: reason)
        case .unrecognized: finish(pairRef, flow, .failed, reason: "unexpected reply")
        }
    }

    private func paired(_ result: PairResult, pairRef: String, flow: Flow) {
        let summary = flow.summary
        let name = result.host.isEmpty ? "Mac" : result.host
        let direct = flow.winner.flatMap { $0.isRelay ? nil : $0.hostPort }
        let record = PairingRecord(
            daemonId: summary.daemonId, name: name, pin: summary.pin, deviceId: result.deviceId,
            addrs: summary.addrs, port: summary.port, lastGood: direct,
            relayURL: result.relay == nil ? nil : summary.relayURL,
            relayState: result.relay == nil ? .none : .ok,
            pairedAt: Date(), secureEnclave: flow.created.secureEnclave)
        do {
            // Credentials of an earlier pairing name the old device's rv and
            // never survive a new one. Without a hub URL to save the fresh ones
            // under, `relayCred` supplies them on the first connection.
            if let relay = result.relay, let url = summary.relayURL {
                // A fresh rv and bearer: never the QR's (03 §10.6).
                try RelayCredentials(hubURL: url, rv: relay.rv, bearer: relay.bearer, storedAt: Date())
                    .save(daemonId: summary.daemonId)
            } else {
                try RelayCredentials.delete(daemonId: summary.daemonId)
            }
            try record.save()
            try ClientIdentity.promote(daemonId: summary.daemonId)
        } catch {
            return finish(pairRef, flow, .failed, reason: "the pairing could not be saved")
        }
        PairingStore.setLastPath(flow.winner?.isRelay == true ? .relay : .direct, daemonId: summary.daemonId)
        finish(pairRef, flow, .paired, name: name)
    }

    // MARK: Outcomes

    private func reject(_ pairRef: String, _ flow: Flow, code: String, message: String) {
        guard flows[pairRef] === flow else { return }
        flows.removeValue(forKey: pairRef)
        flow.timeout?.cancel()
        flow.runner?.cancel()
        flow.path?.cancel()
        ClientIdentity.discard(daemonId: flow.summary.daemonId)
        let completion = flow.completion
        flow.completion = nil
        completion?(.failure(PairingFailure(code: code, message: message)))
    }

    private func finish(
        _ pairRef: String, _ flow: Flow, _ outcome: PairingNotice.Outcome, reason: String? = nil, name: String? = nil
    ) {
        guard flows[pairRef] === flow else { return }
        flows.removeValue(forKey: pairRef)
        flow.timeout?.cancel()
        flow.path?.cancel()
        if outcome != .paired { ClientIdentity.discard(daemonId: flow.summary.daemonId) }
        onNotice?(
            PairingNotice(
                pairRef: pairRef, outcome: outcome,
                daemonId: outcome == .paired ? flow.summary.daemonId : nil, name: name, reason: reason))
    }
}

/// The pairing race's attempt: a path that reports its TLS handshake and keeps
/// the exporter. Nothing is written on it until it wins.
private final class PairAttempt: RaceAttempt {
    let path: LolaPath
    private let queue: DispatchQueue
    private(set) var exporter: ((String, Int) -> Data?)?

    init(path: LolaPath, queue: DispatchQueue) {
        self.path = path
        self.queue = queue
    }

    func start(report: @escaping (AttemptReport) -> Void) {
        path.start(on: queue) { [weak self] event in
            switch event {
            case .tlsReady(let exporter):
                self?.exporter = exporter
                report(.tlsReady)
            case .failed(let failure):
                report(.failed(failure))
            case .viabilityChanged, .betterPathAvailable:
                break
            }
        }
    }

    func cancel() { path.cancel() }
}
