import Foundation
import LolaTransportCore
import Network
import Security

/// A `LolaPath` over the hub's relay: the leg and loopback bridge carry the
/// same inner mutual TLS as a direct path. While pairing, `bearer` is the QR's
/// window token held in memory; otherwise it is the stored relay credential.
final class RelayPath: LolaPath {
    let info = PathInfo(kind: .relay)
    private let bridge: LoopbackBridge
    private let observer: PinObserver
    private var stream: TLSStream?

    init(url: URL, rv: String, bearer: String, pin: String, identity: SecIdentity, queue: DispatchQueue) {
        let observer = PinObserver()
        self.observer = observer
        bridge = LoopbackBridge(
            leg: RelayLeg(request: RelayWire.request(url: url, rv: rv, bearer: bearer), queue: queue),
            innerParameters: LolaTLS.parameters(pin: pin, identity: identity, observer: observer),
            queue: queue)
    }

    var daemonSPKIHash: Data? { observer.presentedHash }

    func start(on queue: DispatchQueue, events: @escaping (PathEvent) -> Void) {
        bridge.start(
            onInner: { [weak self, observer] connection in
                let stream = TLSStream(connection: connection, observer: observer)
                self?.stream = stream
                stream.attach(reportsPathChanges: false, events: events)
            },
            onFailure: { [weak self] failure in
                // After the inner handshake the cancelled inner connection ends
                // the session; only an attempt still racing needs the event.
                if self?.stream?.isReady != true { events(.failed(failure)) }
            })
    }

    func send(_ bytes: Data, completion: @escaping (NWError?) -> Void) {
        guard let stream else { return completion(.posix(.ENOTCONN)) }
        stream.send(bytes, completion: completion)
    }

    func receive(_ handler: @escaping (Data?, Bool, NWError?) -> Void) {
        guard let stream else { return handler(nil, true, nil) }
        stream.receive(handler)
    }

    func cancel() {
        stream?.cancel()
        bridge.cancel()
    }
}
