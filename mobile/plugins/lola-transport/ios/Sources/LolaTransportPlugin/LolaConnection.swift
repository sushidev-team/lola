import Foundation
import LolaTransportCore
import Network
import Security

#if canImport(UIKit)
    import UIKit
#endif

/// One TLS connection to a lola daemon, and the state machine around it.
///
/// Everything in this type runs on ONE serial queue, including every Network
/// framework callback, because `NWConnection.start(queue:)` delivers its state
/// updates and its reads on the queue it was given. That is what makes the
/// mutable state below safe without a lock, and it is why the frame decoder can
/// be a value type: there is exactly one thread that ever touches it.
///
/// Three behaviours here are not obvious and were chosen deliberately.
///
/// The connection carries an EPOCH, bumped on every `connect`. Network
/// framework does not unqueue callbacks that are already scheduled when a
/// connection is cancelled, so a replaced connection can still deliver a state
/// change or a read afterwards; every callback checks its epoch and a stale one
/// returns without touching anything. Without it, tearing down and immediately
/// reconnecting delivers the old socket's death as the new socket's.
///
/// There is NO read deadline after the handshake. An attached pane is
/// legitimately silent for minutes — an agent parked at its prompt writes
/// nothing at all — and the daemon likewise clears its read deadline once the
/// handshake is done. Liveness comes from TCP keepalive, configured below to
/// match the daemon's own posture, and from the write watchdog.
///
/// Backgrounding tears the connection DOWN rather than trying to keep it. An
/// `NWConnection` has no background privileges: suspension stops the queue, the
/// peer eventually resets, and on resume `connection.state` still reads `.ready`
/// until a send fails. A socket that lies about being usable is worse than one
/// that is honestly gone, so the plugin closes it and lets the app reconnect —
/// which is cheap, because `panebus` answers a fresh subscribe with a full
/// resync frame.
final class LolaConnection {

    struct Config {
        var host: String
        var port: UInt16
        var spkiPin: String?
        var allowUnpinned: Bool
        var insecureKey: String?
        var connectTimeoutMs: Int
        var handshakeTimeoutMs: Int
        var writeTimeoutMs: Int
        var flushIntervalMs: Int
        var maxBatchBytes: Int
    }

    struct Outcome {
        let epoch: Int
        let host: String
        let port: UInt16
        let spkiPin: String
        let pinned: Bool
    }

    struct Snapshot {
        let epoch: Int
        let phase: LolaConnectionPhase
        let host: String?
        let port: UInt16?
        let pinned: Bool?
        let framesIn: Int
        let framesOut: Int
        let bytesIn: Int
        let bytesOut: Int
    }

    struct Failure: Error {
        let code: LolaFailureCode
        let reason: String
        var daemonCode: String?
        var minV: Int?
        var maxV: Int?
    }

    /// Batched inbound frame bodies, with the epoch they belong to.
    var onFrames: (([String], Int) -> Void)?

    /// Lifecycle transitions.
    var onState: ((LolaConnectionEvent) -> Void)?

    private let queue = DispatchQueue(label: "dev.sushi.lola.transport.connection")

    /// The certificate check runs on its own queue rather than on the
    /// connection's. Network framework calls the verify block during the
    /// handshake, and putting a synchronous hash on the queue that also drives
    /// every read means one is waiting on the other for no reason.
    private let verifyQueue = DispatchQueue(label: "dev.sushi.lola.transport.verify")

    private var connection: NWConnection?
    private var config: Config?
    private var pinBox: PinBox?
    private var decoder = FrameDecoder()

    private var epoch = 0
    private var phase: LolaConnectionPhase = .closed
    private var active = false
    private var pendingConnect: ((Result<Outcome, Failure>) -> Void)?

    private var batch: [String] = []
    private var batchBytes = 0
    private var flushScheduled = false

    private var outstandingWrites = 0
    private var writeWatchdog = 0

    private var framesIn = 0
    private var framesOut = 0
    private var bytesIn = 0
    private var bytesOut = 0

    /// The last error Network framework reported while `.waiting`. That state is
    /// non-terminal and can persist indefinitely — a refused connection sits
    /// there forever — so the reason is kept here for the connect timeout to
    /// report, otherwise the timeout would say only "timed out" for the very
    /// common case of nothing listening.
    private var lastWaitingError: NWError?

    private var lifecycleObservers: [NSObjectProtocol] = []

    init() {
        observeAppLifecycle()
    }

    deinit {
        for token in lifecycleObservers {
            NotificationCenter.default.removeObserver(token)
        }
    }

    // MARK: - Public surface

    func connect(_ config: Config, completion: @escaping (Result<Outcome, Failure>) -> Void) {
        queue.async { self.startConnect(config, completion: completion) }
    }

    func disconnect(reason: String?) {
        queue.async {
            self.terminate(
                phase: .closed,
                code: .clientClosed,
                reason: reason ?? "closed by the app"
            )
        }
    }

    func send(bodies: [String], completion: @escaping (Result<Void, Failure>) -> Void) {
        queue.async {
            guard self.active, self.phase == .connected, let connection = self.connection else {
                completion(
                    .failure(
                        Failure(
                            code: .clientClosed,
                            reason: "no connection is established"
                        )))
                return
            }
            guard !bodies.isEmpty else {
                completion(.success(()))
                return
            }

            let framed: Data
            do {
                framed = try FrameEncoder.encode(jsonBodies: bodies)
            } catch {
                // A caller's bug, not the peer's: refuse the call and leave the
                // connection alone. Truncating an oversized frame would put a
                // body on the wire the daemon is guaranteed to refuse fatally.
                completion(
                    .failure(
                        Failure(
                            code: .protocolViolation,
                            reason: Self.describe(encodeError: error)
                        )))
                return
            }

            self.framesOut += bodies.count
            self.bytesOut += framed.count
            self.outstandingWrites += 1
            self.armWriteWatchdog()

            let myEpoch = self.epoch
            connection.send(
                content: framed,
                completion: .contentProcessed { [weak self] error in
                    guard let self else { return }
                    // `.contentProcessed` is delivered on the connection's own
                    // queue, so no hop is needed and none is taken.
                    guard self.epoch == myEpoch else { return }
                    self.outstandingWrites -= 1
                    if self.outstandingWrites == 0 { self.writeWatchdog += 1 }
                    if let error {
                        let (code, reason) = self.classify(error)
                        completion(.failure(Failure(code: code, reason: reason)))
                        self.terminate(phase: .failed, code: code, reason: reason)
                        return
                    }
                    completion(.success(()))
                })
        }
    }

    func snapshot(_ completion: @escaping (Snapshot) -> Void) {
        queue.async {
            completion(
                Snapshot(
                    epoch: self.epoch,
                    phase: self.phase,
                    host: self.config?.host,
                    port: self.config?.port,
                    pinned: self.config.map { $0.spkiPin != nil },
                    framesIn: self.framesIn,
                    framesOut: self.framesOut,
                    bytesIn: self.bytesIn,
                    bytesOut: self.bytesOut
                ))
        }
    }

    // MARK: - Connect

    private func startConnect(
        _ config: Config, completion: @escaping (Result<Outcome, Failure>) -> Void
    ) {
        // Every argument is validated BEFORE the existing connection is touched.
        // A rejected option set must not cost a connection that was working:
        // the caller gets an error and keeps what it had.
        //
        // The pin is the daemon's whole identity on this transport, so its
        // absence is a decision the caller has to have made out loud. An
        // omitted option is indistinguishable from a typo'd one, and a check
        // that vanishes on a typo is not a check.
        if config.spkiPin == nil && !config.allowUnpinned {
            completion(
                .failure(
                    Failure(
                        code: .tls,
                        reason:
                            "no spkiPin was supplied. Pass the daemon's SPKI pin, or set allowUnpinned to accept and report whatever certificate the peer presents."
                    )))
            return
        }
        if let key = config.insecureKey, key.count < HelloHandshake.minimumKeyLength {
            // The daemon's listener refuses to start below this length, so the
            // handshake could only ever be denied. Saying so before a socket is
            // opened beats a "denied" that names nothing.
            completion(
                .failure(
                    Failure(
                        code: .rejected,
                        reason:
                            "the bearer key is shorter than the \(HelloHandshake.minimumKeyLength) characters the daemon requires"
                    )))
            return
        }
        guard let port = NWEndpoint.Port(rawValue: config.port) else {
            completion(.failure(Failure(code: .network, reason: "the port is out of range")))
            return
        }

        if active {
            terminate(
                phase: .closed, code: .clientClosed, reason: "replaced by a new connection")
        }

        epoch += 1
        let myEpoch = epoch
        self.config = config
        decoder.reset()
        batch.removeAll()
        batchBytes = 0
        flushScheduled = false
        outstandingWrites = 0
        lastWaitingError = nil
        pendingConnect = completion
        active = true
        phase = .connecting

        let box = PinBox()
        pinBox = box

        let parameters = Self.parameters(for: config, pinBox: box, verifyQueue: verifyQueue)
        let endpoint = NWEndpoint.hostPort(host: NWEndpoint.Host(config.host), port: port)
        let connection = NWConnection(to: endpoint, using: parameters)
        self.connection = connection

        connection.stateUpdateHandler = { [weak self] state in
            self?.handle(state: state, epoch: myEpoch)
        }
        connection.viabilityUpdateHandler = { [weak self] viable in
            // A non-viable connection stays `.ready` and reports no error and no
            // timeout, so a client that ignores this waits forever on a path
            // that is already gone. The policy is ours to set, and for a socket
            // a human is typing into the honest policy is to close it.
            // The epoch guard matters here as much as on the state handler:
            // clearing the handlers on teardown does not unqueue a callback
            // that is already scheduled, and a dead connection reporting its
            // path as unusable must not close the live one that replaced it.
            guard let self, self.epoch == myEpoch, !viable else { return }
            self.terminate(
                phase: .failed,
                code: .network,
                reason: "the network path became unusable")
        }
        connection.betterPathUpdateHandler = { better in
            // TCP does not migrate. A better path is only worth a log line here;
            // acting on it would mean building a second connection and moving
            // every pane subscription across it, which is the app's decision,
            // not the transport's.
            if better { LolaLog.info("a better network path is available") }
        }

        emit(LolaConnectionEvent(epoch: myEpoch, phase: .connecting))
        armConnectTimeout(epoch: myEpoch, milliseconds: config.connectTimeoutMs)
        connection.start(queue: queue)
    }

    private func handle(state: NWConnection.State, epoch stateEpoch: Int) {
        guard epoch == stateEpoch, active else { return }
        switch state {
        case .ready:
            receive(epoch: stateEpoch)
            beginHandshake(epoch: stateEpoch)
        case .waiting(let error):
            // Non-terminal by design: Network framework keeps retrying until
            // the path changes. The connect timeout is what turns a permanent
            // wait into a failure.
            lastWaitingError = error
            LolaLog.info("connection waiting: \(Self.shortDescription(of: error))")
        case .failed(let error):
            let (code, reason) = classify(error)
            terminate(phase: .failed, code: code, reason: reason)
        case .cancelled:
            terminate(phase: .closed, code: .clientClosed, reason: "the connection was cancelled")
        case .setup, .preparing:
            break
        @unknown default:
            break
        }
    }

    // MARK: - Handshake

    private func beginHandshake(epoch handshakeEpoch: Int) {
        guard let config, phase == .connecting else { return }

        guard let key = config.insecureKey else {
            // No bearer key means a daemon built without `-tags lola_insecure`,
            // where the peer is authenticated by mutual TLS during the
            // handshake that has already succeeded. There is nothing in band to
            // do, so the connection is usable now.
            succeed(epoch: handshakeEpoch)
            return
        }

        phase = .handshaking
        emit(LolaConnectionEvent(epoch: handshakeEpoch, phase: .handshaking))
        armHandshakeTimeout(epoch: handshakeEpoch, milliseconds: config.handshakeTimeoutMs)

        let body = HelloHandshake.body(key: key)
        guard let framed = try? FrameEncoder.encode(body: body), let connection else {
            terminate(
                phase: .failed, code: .internalError,
                reason: "the handshake frame could not be encoded")
            return
        }
        bytesOut += framed.count
        framesOut += 1
        connection.send(
            content: framed,
            completion: .contentProcessed { [weak self] error in
                guard let self, self.epoch == handshakeEpoch, let error else { return }
                let (code, reason) = self.classify(error)
                self.terminate(phase: .failed, code: code, reason: reason)
            })
    }

    /// Consumes the daemon's single handshake reply. The reply is never
    /// forwarded to JavaScript: it belongs to a handshake the app does not know
    /// exists, and delivering it would put a correlation id in the shim's lap
    /// that the shim never issued.
    private func handleHandshakeReply(_ body: Data, epoch replyEpoch: Int) {
        switch HelloHandshake.classify(reply: body) {
        case .authenticated:
            succeed(epoch: replyEpoch)

        case .refused(let daemonCode, let message, let minV, let maxV):
            // Every M1 refusal is the same opaque "denied": the daemon says
            // nothing about which part was wrong, so neither does this.
            let code: LolaFailureCode = daemonCode == "denied" ? .rejected : .protocolViolation
            var reason = "the daemon refused the connection (\(daemonCode))"
            if let message, !message.isEmpty { reason += ": \(message)" }
            terminate(
                phase: .failed, code: code, reason: reason,
                daemonCode: daemonCode, minV: minV, maxV: maxV)

        case .unexpected(let why):
            terminate(phase: .failed, code: .protocolViolation, reason: why)
        }
    }

    private func succeed(epoch successEpoch: Int) {
        guard let config else { return }
        phase = .connected

        let observed = pinBox?.observedPin ?? ""
        let pinned = config.spkiPin != nil
        if !pinned {
            // Loud on purpose, and it says the milestone out loud. M2 is what
            // gives the pin a distribution channel (the pairing QR), and this
            // branch is deleted with the bearer key it accompanies.
            LolaLog.warn(
                "connected WITHOUT a certificate pin: the daemon's identity was accepted unverified and only recorded (SPKI \(observed)). This is acceptable only in M1; M2's pairing makes the pin mandatory."
            )
        }

        emit(
            LolaConnectionEvent(
                epoch: successEpoch,
                phase: .connected,
                spkiPin: observed,
                pinned: pinned))

        let outcome = Outcome(
            epoch: successEpoch,
            host: config.host,
            port: config.port,
            spkiPin: observed,
            pinned: pinned)
        let completion = pendingConnect
        pendingConnect = nil
        completion?(.success(outcome))
    }

    // MARK: - Read

    private func receive(epoch readEpoch: Int) {
        guard let connection, epoch == readEpoch, active else { return }
        connection.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) {
            [weak self] data, _, isComplete, error in
            guard let self, self.epoch == readEpoch, self.active else { return }

            if let data, !data.isEmpty {
                guard self.ingest(data, epoch: readEpoch) else { return }
            }
            if let error {
                let (code, reason) = self.classify(error)
                self.terminate(phase: .failed, code: code, reason: reason)
                return
            }
            if isComplete {
                // A clean close from the peer. Whatever was already decoded has
                // been delivered by `ingest`; flush it before announcing the
                // close, because the last frame before a close is routinely the
                // one that explains it.
                self.flushNow()
                self.terminate(
                    phase: .closed, code: .peerClosed, reason: "the daemon closed the connection")
                return
            }
            self.receive(epoch: readEpoch)
        }
    }

    /// Feeds bytes to the decoder and routes whatever comes out. Returns false
    /// when the connection has been torn down and the caller must not re-arm
    /// the read.
    private func ingest(_ data: Data, epoch ingestEpoch: Int) -> Bool {
        bytesIn += data.count
        decoder.push(data)

        var delivered: [String] = []
        var framingFailure: Error?

        decodeLoop: while true {
            let body: Data?
            do {
                body = try decoder.next()
            } catch {
                framingFailure = error
                break decodeLoop
            }
            guard let body else { break decodeLoop }

            framesIn += 1
            guard let text = String(data: body, encoding: .utf8) else {
                // `encoding/json` always emits valid UTF-8, so this is a
                // corrupt stream rather than a protocol variation.
                enqueue(delivered)
                flushNow()
                terminate(
                    phase: .failed, code: .protocolViolation,
                    reason: "the daemon sent a frame that is not valid UTF-8")
                return false
            }

            if phase == .handshaking {
                handleHandshakeReply(body, epoch: ingestEpoch)
                if phase != .connected { return false }
                continue
            }
            delivered.append(text)
        }

        enqueue(delivered)

        if let framingFailure {
            // The frames decoded BEFORE the violation are still handed over,
            // and they go out immediately rather than on the next tick: the
            // daemon answers a bad prefix with a best-effort refusal frame and
            // then closes, so the explanation is usually the last thing in the
            // buffer.
            flushNow()
            terminate(
                phase: .failed, code: .protocolViolation,
                reason: Self.describe(encodeError: framingFailure))
            return false
        }
        return true
    }

    // MARK: - Batching

    /// Frames are coalesced on a short tick before crossing the bridge.
    ///
    /// This is the one piece of performance work the plugin genuinely has to
    /// do. Capacitor's `notifyListeners` does not pass arguments to JavaScript:
    /// it serializes the payload, interpolates it into a string of JavaScript
    /// SOURCE, hops to the main thread and has WebKit parse it. One bridge
    /// crossing per socket read on a busy pane would put that cost in the path
    /// of every burst of agent output.
    ///
    /// The window matches the daemon's own `panebus.DefaultFlushInterval` of 16
    /// ms rather than inventing a second cadence. The daemon has already
    /// coalesced its output on that tick, so a matching window adds at most one
    /// more frame time; a shorter one would spend bridge crossings on frames
    /// the daemon batched anyway, and a longer one would be felt as keystroke
    /// echo latency.
    private func enqueue(_ frames: [String]) {
        guard !frames.isEmpty, let config else { return }
        batch.append(contentsOf: frames)
        for frame in frames { batchBytes += frame.utf8.count }

        if config.flushIntervalMs <= 0 || batchBytes >= config.maxBatchBytes {
            flushNow()
            return
        }
        guard !flushScheduled else { return }
        flushScheduled = true
        let myEpoch = epoch
        queue.asyncAfter(deadline: .now() + .milliseconds(config.flushIntervalMs)) {
            [weak self] in
            guard let self, self.epoch == myEpoch else { return }
            self.flushScheduled = false
            self.flushNow()
        }
    }

    private func flushNow() {
        guard !batch.isEmpty else { return }
        let out = batch
        batch = []
        batchBytes = 0
        onFrames?(out, epoch)
    }

    // MARK: - Deadlines

    private func armConnectTimeout(epoch timeoutEpoch: Int, milliseconds: Int) {
        guard milliseconds > 0 else { return }
        queue.asyncAfter(deadline: .now() + .milliseconds(milliseconds)) { [weak self] in
            guard let self, self.epoch == timeoutEpoch, self.active,
                self.phase == .connecting
            else { return }
            var reason = "the daemon did not answer within \(milliseconds) ms"
            if let waiting = self.lastWaitingError {
                reason += " (\(Self.shortDescription(of: waiting)))"
            }
            self.terminate(phase: .failed, code: .timeout, reason: reason)
        }
    }

    private func armHandshakeTimeout(epoch timeoutEpoch: Int, milliseconds: Int) {
        guard milliseconds > 0 else { return }
        queue.asyncAfter(deadline: .now() + .milliseconds(milliseconds)) { [weak self] in
            guard let self, self.epoch == timeoutEpoch, self.active,
                self.phase == .handshaking
            else { return }
            self.terminate(
                phase: .failed, code: .timeout,
                reason: "the daemon did not answer the handshake within \(milliseconds) ms")
        }
    }

    /// The write watchdog is per connection, not per write: it fires when
    /// SOMETHING has been outstanding for the whole budget without the
    /// transport accepting anything. Arming it on every send resets the
    /// deadline, so a connection that is making progress never trips it, and a
    /// connection whose peer has stopped reading does — which is the case that
    /// matters, since the daemon bounds its own writes at the same 15 seconds
    /// and tears the connection down from its side too.
    private func armWriteWatchdog() {
        guard let config, config.writeTimeoutMs > 0 else { return }
        writeWatchdog += 1
        let generation = writeWatchdog
        let myEpoch = epoch
        queue.asyncAfter(deadline: .now() + .milliseconds(config.writeTimeoutMs)) { [weak self] in
            guard let self, self.epoch == myEpoch, self.writeWatchdog == generation,
                self.outstandingWrites > 0
            else { return }
            self.terminate(
                phase: .failed, code: .timeout,
                reason: "a write was not accepted within \(config.writeTimeoutMs) ms")
        }
    }

    // MARK: - Teardown

    private func terminate(
        phase newPhase: LolaConnectionPhase,
        code: LolaFailureCode,
        reason: String,
        daemonCode: String? = nil,
        minV: Int? = nil,
        maxV: Int? = nil
    ) {
        guard active else { return }
        active = false
        phase = newPhase

        let myEpoch = epoch
        let connection = self.connection
        self.connection = nil
        self.pinBox = nil
        decoder.reset()
        batch.removeAll()
        batchBytes = 0
        outstandingWrites = 0
        writeWatchdog += 1

        // Clearing the handlers before cancelling stops the cancellation from
        // arriving back through `handle(state:)` as a second terminal event.
        connection?.stateUpdateHandler = nil
        connection?.viabilityUpdateHandler = nil
        connection?.betterPathUpdateHandler = nil
        connection?.cancel()

        let completion = pendingConnect
        pendingConnect = nil
        completion?(
            .failure(
                Failure(
                    code: code, reason: reason, daemonCode: daemonCode, minV: minV, maxV: maxV)))

        emit(
            LolaConnectionEvent(
                epoch: myEpoch,
                phase: newPhase,
                code: code,
                reason: reason,
                daemonCode: daemonCode,
                minV: minV,
                maxV: maxV))
    }

    private func emit(_ event: LolaConnectionEvent) {
        onState?(event)
    }

    // MARK: - App lifecycle

    private func observeAppLifecycle() {
        #if canImport(UIKit)
            let token = NotificationCenter.default.addObserver(
                forName: UIApplication.didEnterBackgroundNotification,
                object: nil,
                queue: nil
            ) { [weak self] _ in
                guard let self else { return }
                // Posted on the main thread; every mutation belongs on the
                // connection's queue.
                self.queue.async {
                    guard self.active else { return }
                    self.terminate(
                        phase: .closed, code: .backgrounded,
                        reason: "the app entered the background")
                }
            }
            lifecycleObservers.append(token)
        #endif
    }

    // MARK: - Errors

    private func classify(_ error: NWError) -> (LolaFailureCode, String) {
        // The pin decision is consulted FIRST. Network framework reports a
        // rejected certificate as an ordinary TLS error, so without this a
        // mismatched pin — the one TLS failure with a specific remedy — would
        // read as "TLS handshake failed" and send somebody looking at their
        // cipher suites.
        if let recorded = pinBox?.failureReason {
            return (.pinMismatch, recorded)
        }

        switch error {
        case .tls(let status):
            return (.tls, "the TLS handshake failed (OSStatus \(status))")
        case .posix(let code):
            switch code {
            case .ETIMEDOUT:
                return (.timeout, "the connection timed out")
            case .ECONNREFUSED:
                return (
                    .network,
                    "the daemon refused the connection. Check that lola is running and that [remote] is enabled."
                )
            case .EHOSTUNREACH, .ENETUNREACH, .ENETDOWN, .EHOSTDOWN:
                return (
                    .network,
                    "the daemon is not reachable from this network. If this is the first connection, check Settings, Privacy and Security, Local Network."
                )
            case .ECONNRESET, .EPIPE, .ENOTCONN:
                return (.peerClosed, "the connection was reset")
            default:
                return (.network, "the connection failed (POSIX \(code.rawValue))")
            }
        case .dns(let status):
            return (.network, "the host could not be resolved (DNS \(status))")
        @unknown default:
            return (.network, "the connection failed")
        }
    }

    private static func describe(encodeError: Error) -> String {
        guard let error = encodeError as? FrameCodecError else {
            return "the frame could not be processed"
        }
        switch error {
        case .emptyFrame:
            return "a zero-length frame is not valid on this protocol"
        case .frameTooLarge(let size, let max):
            return "the daemon announced a \(size) byte frame; the limit is \(max)"
        case .bodyTooLarge(let size, let max):
            return "a \(size) byte frame body exceeds the \(max) byte limit"
        case .decoderPoisoned:
            return "the frame stream cannot be resynchronised"
        case .invalidUTF8:
            return "a frame body was not valid UTF-8"
        }
    }

    private static func shortDescription(of error: NWError) -> String {
        switch error {
        case .posix(let code): return "POSIX \(code.rawValue)"
        case .dns(let status): return "DNS \(status)"
        case .tls(let status): return "TLS \(status)"
        @unknown default: return "unknown"
        }
    }

    // MARK: - TLS

    private static func parameters(
        for config: Config, pinBox: PinBox, verifyQueue: DispatchQueue
    ) -> NWParameters {
        let tls = NWProtocolTLS.Options()
        let sec = tls.securityProtocolOptions

        // The daemon is TLS 1.3 only (`identity.go`'s `TLSConfig` sets
        // `MinVersion: tls.VersionTLS13`), and it disables session tickets so
        // there is no resumption to replay a frame against. Pinning the client
        // to the same single version means a downgrade is a connect failure
        // rather than a negotiation.
        sec_protocol_options_set_min_tls_protocol_version(sec, .TLSv13)
        sec_protocol_options_set_max_tls_protocol_version(sec, .TLSv13)

        // No ALPN is set, because the daemon sets none: `NextProtos` is unset
        // in `TLSConfig`, so proposing a protocol would only invite a mismatch.

        let expected = config.spkiPin
        sec_protocol_options_set_verify_block(
            sec,
            { _, trustRef, complete in
                // Replacing the verify block replaces system trust evaluation
                // ENTIRELY, which is the point. The daemon's certificate is
                // self-signed, is in no trust store, and carries
                // `DNSNames: ["lola"]` with loopback IP SANs, so ordinary
                // evaluation against a LAN address cannot succeed even in
                // principle. Note in particular that nothing here builds a
                // `SecPolicyCreateSSL` policy or calls `SecTrustEvaluate`: that
                // path rejects a certificate whose SAN does not match the host
                // and any validity window longer than 398 days, and this
                // certificate is deliberately valid for ten years. The pin is
                // the trust anchor.
                guard let trust = sec_trust_copy_ref(trustRef)?.takeRetainedValue() else {
                    pinBox.record(failure: "the peer presented no certificate chain")
                    complete(false)
                    return
                }
                guard
                    let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate],
                    let leaf = chain.first
                else {
                    pinBox.record(failure: "the peer presented an empty certificate chain")
                    complete(false)
                    return
                }

                let der = SecCertificateCopyData(leaf) as Data
                guard let observed = try? SPKIPin.pin(forCertificateDER: der) else {
                    pinBox.record(failure: "the peer's certificate could not be parsed")
                    complete(false)
                    return
                }
                pinBox.record(pin: observed)

                guard let expected else {
                    // Accept and record. See the warning logged on success and
                    // the `allowUnpinned` gate that is required to reach here.
                    complete(true)
                    return
                }
                if SPKIPin.matches(observed, expected: expected) {
                    complete(true)
                } else {
                    pinBox.record(
                        failure:
                            "the daemon presented a different public key than the pin expects. Either the pin is wrong, or this is not the daemon it claims to be."
                    )
                    complete(false)
                }
            }, verifyQueue)

        let tcp = NWProtocolTCP.Options()
        // Keystrokes. Nagle would hold a single-byte write waiting for company.
        tcp.noDelay = true
        // The daemon sets no post-handshake read deadline and neither does this
        // client, so keepalive is the only thing that notices a peer that went
        // away without a FIN. These match the server's 30 s idle, 10 s
        // interval, 3 probes.
        tcp.enableKeepalive = true
        tcp.keepaliveIdle = 30
        tcp.keepaliveInterval = 10
        tcp.keepaliveCount = 3

        return NWParameters(tls: tls, tcp: tcp)
    }
}

/// Carries the verify block's findings back to the connection.
///
/// The verify block runs on its own queue, so this is the one piece of shared
/// mutable state in the file and the one place a lock is warranted. It is a
/// class rather than a captured closure over `self` on purpose: capturing the
/// connection inside TLS options would keep it alive for as long as Network
/// framework holds the parameters, which outlives the connection object.
private final class PinBox {
    private let lock = NSLock()
    private var pin: String?
    private var failure: String?

    func record(pin value: String) {
        lock.lock()
        defer { lock.unlock() }
        pin = value
    }

    func record(failure value: String) {
        lock.lock()
        defer { lock.unlock() }
        failure = value
    }

    var observedPin: String? {
        lock.lock()
        defer { lock.unlock() }
        return pin
    }

    var failureReason: String? {
        lock.lock()
        defer { lock.unlock() }
        return failure
    }
}
