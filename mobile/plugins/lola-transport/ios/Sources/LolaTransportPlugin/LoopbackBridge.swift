import Foundation
import LolaTransportCore
import Network

/// Connects the relay leg to an ordinary TLS `NWConnection` (03 §13.2).
///
/// Network.framework's TLS cannot run over a byte stream the app supplies, so
/// the inner mutual TLS dials a one-shot listener on `127.0.0.1` and this type
/// copies bytes between that socket and the WebSocket. The loopback hop carries
/// ciphertext only.
///
/// The listener accepts ONLY the inner connection's own socket: any other
/// local connection is cancelled at once and the listener keeps listening, so
/// a local process that connects first wastes nothing. While the inner local
/// port is unknown, accepted connections are held unread. Where the platform
/// never reports that port before the handshake (the iOS 26 Simulator), the
/// sole connection is taken after a short grace and two candidates fail the
/// attempt (`pair()`).
final class LoopbackBridge {
    private let leg: RelayLeg
    private let innerParameters: NWParameters
    private let queue: DispatchQueue
    private var listener: NWListener?
    private var inner: NWConnection?
    private var innerLocalPort: NWEndpoint.Port?
    private var held: [NWConnection] = []
    private var matched: NWConnection?
    private var matchTimer: DispatchWorkItem?
    private var firstHeldAt: DispatchTime?
    /// How long a sole accepted connection waits for a rival before it is
    /// taken as the inner one, when the inner local port is not reported.
    private static let soleCandidateGraceMs = 50
    private var onFailure: ((PathFailure) -> Void)?
    private var finished = false

    init(leg: RelayLeg, innerParameters: NWParameters, queue: DispatchQueue) {
        self.leg = leg
        self.innerParameters = innerParameters
        self.queue = queue
    }

    /// `onInner` receives the inner connection to attach handlers to; the
    /// bridge starts it. `onFailure` reports a refused upgrade or a bridge
    /// fault; after the inner handshake the inner connection's own failure is
    /// the signal, and the bridge ending cancels it.
    func start(onInner: @escaping (NWConnection) -> Void, onFailure: @escaping (PathFailure) -> Void) {
        self.onFailure = onFailure
        leg.onClosed = { [weak self] in self?.teardown(.network("the relay leg closed")) }
        leg.open { [weak self] result in
            guard let self, !self.finished else { return }
            switch result {
            case .failure(.refused(let refusal)): self.teardown(.relay(refusal))
            case .failure(.network(let text)): self.teardown(.network(text))
            case .success: self.listen(onInner)
            }
        }
    }

    func cancel() {
        guard !finished else { return }
        finished = true
        onFailure = nil
        release()
    }

    // MARK: Listener

    private func listen(_ onInner: @escaping (NWConnection) -> Void) {
        let parameters = NWParameters.tcp
        // Bound to 127.0.0.1, so only this device can reach it; `pair()` then
        // admits only the inner connection's own socket. No `acceptLocalOnly`:
        // with it the listener resets every inbound connection on the iOS 26
        // Simulator (verified with an NWListener probe run by `simctl spawn`:
        // plain TCP and TLS clients both get ECONNRESET, nothing is accepted),
        // and on a loopback-bound socket it restricts nothing further.
        parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        guard let listener = try? NWListener(using: parameters) else {
            return teardown(.network("the loopback listener could not be created"))
        }
        self.listener = listener
        listener.newConnectionHandler = { [weak self] connection in self?.accepted(connection) }
        listener.stateUpdateHandler = { [weak self] state in
            guard let self, !self.finished else { return }
            switch state {
            case .ready:
                guard self.inner == nil, let port = listener.port else { return }
                self.dialInner(port: port, onInner)
            case .failed:
                self.teardown(.network("the loopback listener failed"))
            default:
                break
            }
        }
        listener.start(queue: queue)
    }

    private func dialInner(port: NWEndpoint.Port, _ onInner: (NWConnection) -> Void) {
        innerParameters.requiredInterfaceType = .loopback
        let connection = NWConnection(host: "127.0.0.1", port: port, using: innerParameters)
        inner = connection
        connection.pathUpdateHandler = { [weak self] path in
            guard let self, !self.finished else { return }
            if case .hostPort(_, let local)? = path.localEndpoint, self.innerLocalPort != local {
                self.innerLocalPort = local
                self.pair()
            }
        }
        onInner(connection)
        connection.start(queue: queue)

        let timer = DispatchWorkItem { [weak self] in
            guard let self, self.matched == nil else { return }
            self.teardown(.network("the loopback peer did not arrive"))
        }
        matchTimer = timer
        queue.asyncAfter(deadline: .now() + .milliseconds(Limits.loopbackMatchTimeoutMs), execute: timer)
    }

    private func accepted(_ connection: NWConnection) {
        guard !finished, matched == nil else { return connection.cancel() }
        connection.start(queue: queue)
        held.append(connection)
        pair()
    }

    /// The inner socket's local port, when Network.framework reports it. The
    /// first path update arrives before the socket is bound and none follows
    /// once TCP connects (the path did not change); `currentPath` carries the
    /// port on macOS once TCP is up, but on the iOS 26 Simulator not before
    /// the inner TLS is `.ready`, which needs this bridge to be pumping. A
    /// requested `requiredLocalEndpoint` port is not honoured either (probe:
    /// reserved N, connected from N+1). Verified live against a relay.
    private func refreshInnerLocalPort() {
        guard innerLocalPort == nil,
            case .hostPort(_, let local)? = inner?.currentPath?.localEndpoint
        else { return }
        innerLocalPort = local
    }

    /// Picks the connection whose remote port is the inner connection's local
    /// port and cancels every other.
    private func pair() {
        guard !finished, matched == nil else { return }
        refreshInnerLocalPort()
        let match: NWConnection?
        if let local = innerLocalPort {
            match = held.first { candidate in
                if case .hostPort(_, let remote) = candidate.endpoint { return remote == local }
                return false
            }
        } else {
            // The port is unknowable before the handshake on this platform.
            // Take the ONE connection that arrived; give a racing second one a
            // short window to show itself, and if there is more than one,
            // fail the attempt rather than guess. A local process that won the
            // race gets ciphertext it cannot use (completing the inner TLS
            // needs this phone's key) and costs one attempt, the outcome the
            // port match also left it.
            guard !held.isEmpty else { return }
            if held.count > 1 {
                return teardown(.network("more than one loopback peer arrived"))
            }
            if firstHeldAt == nil {
                firstHeldAt = .now()
                queue.asyncAfter(deadline: .now() + .milliseconds(Self.soleCandidateGraceMs)) { [weak self] in self?.pair() }
                return
            }
            guard let since = firstHeldAt, DispatchTime.now() >= since + .milliseconds(Self.soleCandidateGraceMs) else { return }
            match = held.first
        }
        for other in held where other !== match { other.cancel() }
        held = []
        guard let match else { return }
        matched = match
        matchTimer?.cancel()
        listener?.cancel()
        listener = nil
        pumpLoopbackToLeg(match)
        pumpLegToLoopback(match)
    }

    // MARK: Pipe (one receive in flight per direction: end-to-end backpressure)

    private func pumpLoopbackToLeg(_ loopback: NWConnection) {
        loopback.receive(minimumIncompleteLength: 1, maximumLength: 16384) { [weak self] data, _, complete, error in
            guard let self, !self.finished else { return }
            if let data, !data.isEmpty {
                self.leg.send(data) { [weak self] sendError in
                    guard let self, !self.finished else { return }
                    if sendError != nil || complete || error != nil {
                        self.teardown(.network("the relay leg closed"))
                    } else {
                        self.pumpLoopbackToLeg(loopback)
                    }
                }
            } else {
                self.teardown(.network("the loopback socket closed"))
            }
        }
    }

    private func pumpLegToLoopback(_ loopback: NWConnection) {
        leg.receive { [weak self] result in
            guard let self, !self.finished else { return }
            guard case .success(let data) = result else {
                return self.teardown(.network("the relay leg closed"))
            }
            loopback.send(content: data, completion: .contentProcessed { [weak self] error in
                guard let self, !self.finished else { return }
                if error != nil {
                    self.teardown(.network("the loopback socket closed"))
                } else {
                    self.pumpLegToLoopback(loopback)
                }
            })
        }
    }

    // MARK: Teardown

    /// Any side ending cancels all three. The bridge never reconnects: the
    /// connector owns retries.
    private func teardown(_ failure: PathFailure) {
        guard !finished else { return }
        finished = true
        let report = onFailure
        onFailure = nil
        release()
        report?(failure)
    }

    private func release() {
        matchTimer?.cancel()
        listener?.cancel()
        listener = nil
        for connection in held { connection.cancel() }
        held = []
        matched?.cancel()
        matched = nil
        inner?.pathUpdateHandler = nil
        inner?.cancel()
        inner = nil
        leg.cancel()
    }
}
