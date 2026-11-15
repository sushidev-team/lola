import Foundation
import LolaTransportCore
import Network

/// What a path reports while it runs (04 §1.3). Failures use Core's
/// `PathFailure` so the pure race classifies them.
enum PathEvent {
    /// The inner TLS handshake finished (pin matched, server `Finished`
    /// verified). `exporter` is `sec_protocol_metadata_create_secret`.
    case tlsReady(exporter: (String, Int) -> Data?)
    case failed(PathFailure)
    case viabilityChanged(Bool)
    case betterPathAvailable
}

/// One byte stream to the daemon: direct TCP or the relay's loopback bridge,
/// both carrying the same mutual TLS. Authentication (`ready` frame) is the
/// session's job, never the path's.
protocol LolaPath: AnyObject {
    var info: PathInfo { get }
    /// SHA-256 of the SPKI the daemon presented; set once the verify block ran.
    var daemonSPKIHash: Data? { get }
    func start(on queue: DispatchQueue, events: @escaping (PathEvent) -> Void)
    func send(_ bytes: Data, completion: @escaping (NWError?) -> Void)
    func receive(_ handler: @escaping (Data?, Bool, NWError?) -> Void)
    func cancel()
}
