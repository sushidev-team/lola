import CryptoKit
import Foundation
import LolaTransportCore
import Network
import Security

/// What the verify block saw, shared with the stream that reports it.
///
/// The verify block runs on its own queue, so this is the one piece of shared
/// mutable state in the transport and the one place a lock is warranted.
final class PinObserver {
    private let lock = NSLock()
    private var presented: Data?
    private var mismatched: Data?

    func record(presentedHash: Data, matched: Bool) {
        lock.lock()
        defer { lock.unlock() }
        presented = presentedHash
        mismatched = matched ? nil : presentedHash
    }

    /// SHA-256 of the SPKI the peer presented.
    var presentedHash: Data? {
        lock.lock()
        defer { lock.unlock() }
        return presented
    }

    /// Set only when the presented key was not the pinned one.
    var mismatchedHash: Data? {
        lock.lock()
        defer { lock.unlock() }
        return mismatched
    }
}

/// The one `NWParameters` builder (04 §1.3): the direct path, the relay's
/// loopback bridge and pairing all dial through it, so the ClientHello is the
/// same on every path and the pin and the client identity are applied once.
enum LolaTLS {
    /// Fixed SNI on both paths so the relay's inner handshake is
    /// indistinguishable from a direct one.
    static let serverName = "lola-daemon"
    private static let verifyQueue = DispatchQueue(label: "dev.sushi.lola.transport.verify")

    static func parameters(
        pin: String, identity: SecIdentity, observer: PinObserver = PinObserver()
    ) -> NWParameters {
        let tls = NWProtocolTLS.Options()
        let sec = tls.securityProtocolOptions

        // The daemon is TLS 1.3 only and disables session tickets, so a
        // downgrade is a connect failure rather than a negotiation.
        sec_protocol_options_set_min_tls_protocol_version(sec, .TLSv13)
        sec_protocol_options_set_max_tls_protocol_version(sec, .TLSv13)
        sec_protocol_options_set_tls_server_name(sec, serverName)
        if let local = sec_identity_create(identity) {
            sec_protocol_options_set_local_identity(sec, local)
        }

        // Replacing the verify block replaces system trust evaluation entirely,
        // on purpose: the daemon's certificate is self-signed and valid for ten
        // years, so ordinary evaluation cannot succeed. The pin is the anchor.
        sec_protocol_options_set_verify_block(
            sec,
            { _, trustRef, complete in
                let trust = sec_trust_copy_ref(trustRef).takeRetainedValue()
                guard
                    let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate],
                    let leaf = chain.first,
                    let spki = try? SPKIPin.subjectPublicKeyInfo(inCertificateDER: SecCertificateCopyData(leaf) as Data)
                else {
                    complete(false)
                    return
                }
                let hash = Data(SHA256.hash(data: spki))
                let matched = SPKIPin.matches(hash.base64EncodedString(), expected: pin)
                observer.record(presentedHash: hash, matched: matched)
                complete(matched)
            }, verifyQueue)

        let tcp = NWProtocolTCP.Options()
        // Keystrokes: Nagle would hold a single-byte write waiting for company.
        tcp.noDelay = true
        // No post-handshake read deadline exists on either side, so keepalive
        // (the daemon's own 30 s / 10 s / 3) is what notices a silent peer.
        tcp.enableKeepalive = true
        tcp.keepaliveIdle = 30
        tcp.keepaliveInterval = 10
        tcp.keepaliveCount = 3
        return NWParameters(tls: tls, tcp: tcp)
    }

    /// TLS exporter secret of an established connection, nil before `.ready`.
    static func exporter(of connection: NWConnection) -> (String, Int) -> Data? {
        { label, length in
            guard
                let meta = connection.metadata(definition: NWProtocolTLS.definition) as? NWProtocolTLS.Metadata
            else { return nil }
            let secret = label.withCString {
                sec_protocol_metadata_create_secret(meta.securityProtocolMetadata, label.utf8.count, $0, length)
            }
            // dispatch_data_t bridges to DispatchData.
            return secret.map { Data($0 as DispatchData) }
        }
    }
}

/// An `NWConnection` running `LolaTLS`, reporting `PathEvent`s. Shared by the
/// direct path and the relay's inner connection.
final class TLSStream {
    let connection: NWConnection
    let observer: PinObserver
    private var tlsReady = false
    private var reportedFailure = false
    private var cancelled = false

    init(connection: NWConnection, observer: PinObserver) {
        self.connection = connection
        self.observer = observer
    }

    var isReady: Bool { tlsReady }

    /// Installs the handlers without starting; the relay bridge starts its
    /// inner connection itself once the listener port is known.
    func attach(reportsPathChanges: Bool, events: @escaping (PathEvent) -> Void) {
        connection.stateUpdateHandler = { [weak self] state in
            guard let self, !self.cancelled else { return }
            switch state {
            case .ready:
                guard !self.tlsReady else { return }
                self.tlsReady = true
                events(.tlsReady(exporter: LolaTLS.exporter(of: self.connection)))
            case .waiting(let error):
                // A refused connection has been reached and a path change will
                // not alter the answer. Every other waiting reason (no route
                // yet, the local-network prompt) can resolve and keeps the
                // race's own timeout.
                if case .posix(let code) = error, code == .ECONNREFUSED { self.fail(error, events) }
            case .failed(let error):
                self.fail(error, events)
            default:
                break
            }
        }
        guard reportsPathChanges else { return }
        connection.viabilityUpdateHandler = { [weak self] viable in
            guard let self, !self.cancelled else { return }
            events(.viabilityChanged(viable))
        }
        connection.betterPathUpdateHandler = { [weak self] better in
            guard let self, !self.cancelled, better else { return }
            events(.betterPathAvailable)
        }
    }

    func start(on queue: DispatchQueue, reportsPathChanges: Bool, events: @escaping (PathEvent) -> Void) {
        attach(reportsPathChanges: reportsPathChanges, events: events)
        connection.start(queue: queue)
    }

    func send(_ bytes: Data, completion: @escaping (NWError?) -> Void) {
        connection.send(content: bytes, completion: .contentProcessed { completion($0) })
    }

    func receive(_ handler: @escaping (Data?, Bool, NWError?) -> Void) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { data, _, complete, error in
            handler(data, complete, error)
        }
    }

    func cancel() {
        cancelled = true
        connection.stateUpdateHandler = nil
        connection.viabilityUpdateHandler = nil
        connection.betterPathUpdateHandler = nil
        connection.cancel()
    }

    private func fail(_ error: NWError, _ events: (PathEvent) -> Void) {
        guard !reportedFailure else { return }
        reportedFailure = true
        events(.failed(classify(error)))
    }

    /// A TLS alert before `.ready` proves nothing (an on-path host can send one
    /// under keys it negotiated itself); after `.ready` the stack has verified
    /// `CertificateVerify` and the server `Finished`, so an alert is the daemon's.
    private func classify(_ error: NWError) -> PathFailure {
        let text = Self.describe(error)
        if !tlsReady {
            if let hash = observer.mismatchedHash { return .keyMismatch(spki: hash) }
            switch error {
            case .tls: return .tlsBeforeReady(text)
            case .posix(let code) where code == .ETIMEDOUT: return .timeout
            default: return .network(text)
            }
        }
        return Self.classifyAfterReady(error)
    }

    /// A failure of a stream that already reached `.ready`: the stack verified
    /// the daemon's `CertificateVerify` and `Finished`, so a TLS error is the
    /// pinned daemon's alert. A nil error is a plain EOF.
    static func classifyAfterReady(_ error: NWError?) -> PathFailure {
        guard let error else { return .network("closed before ready") }
        if case .tls = error { return .alertAfterReady(describe(error)) }
        return .network(describe(error))
    }

    static func describe(_ error: NWError) -> String {
        // NWError is non-frozen and gains cases; `default` keeps this building on every SDK.
        switch error {
        case .posix(let code): return "POSIX \(code.rawValue)"
        case .dns(let status): return "DNS \(status)"
        case .tls(let status): return "TLS \(status)"
        default: return "unknown"
        }
    }
}
