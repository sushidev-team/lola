import Foundation
import LolaTransportCore
import Network
import Security

/// `NWConnection` TCP with mutual TLS straight to the daemon.
final class DirectPath: LolaPath {
    let info: PathInfo
    private let stream: TLSStream

    init?(host: String, port: Int, pin: String, identity: SecIdentity, interface: NetworkInterface?) {
        guard let nwPort = NWEndpoint.Port(rawValue: UInt16(clamping: port)), port > 0 else { return nil }
        let observer = PinObserver()
        let connection = NWConnection(
            host: NWEndpoint.Host(host), port: nwPort,
            using: LolaTLS.parameters(pin: pin, identity: identity, observer: observer))
        stream = TLSStream(connection: connection, observer: observer)
        info = PathInfo(kind: .direct, host: host, port: port, interface: interface)
    }

    var daemonSPKIHash: Data? { stream.observer.presentedHash }

    func start(on queue: DispatchQueue, events: @escaping (PathEvent) -> Void) {
        stream.start(on: queue, reportsPathChanges: true, events: events)
    }

    func send(_ bytes: Data, completion: @escaping (NWError?) -> Void) { stream.send(bytes, completion: completion) }
    func receive(_ handler: @escaping (Data?, Bool, NWError?) -> Void) { stream.receive(handler) }
    func cancel() { stream.cancel() }
}
