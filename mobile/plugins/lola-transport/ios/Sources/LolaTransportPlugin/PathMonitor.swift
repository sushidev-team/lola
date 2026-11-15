import Foundation
import LolaTransportCore
import Network

/// `NWPathMonitor` with a debounced change callback (`PATH_DEBOUNCE_MS`).
///
/// `current` is updated at once, so a race planned right after a change sees
/// it; `onChange` fires once the path has been stable for the debounce window.
final class PathMonitor {
    struct Snapshot: Equatable {
        var satisfied = true
        var hasWifiOrWired = true
        var interface: NetworkInterface?
        /// Interface names and gateways: a Wi-Fi network switch keeps every
        /// other field and changes this.
        var signature = ""
    }

    private let monitor = NWPathMonitor()
    private let queue: DispatchQueue
    private var debounce: DispatchWorkItem?
    private var notified = Snapshot()
    private var started = false
    private var seenFirst = false
    private(set) var current = Snapshot()
    var onChange: ((Snapshot) -> Void)?

    /// `queue` must be serial; every callback runs on it.
    init(queue: DispatchQueue) {
        self.queue = queue
    }

    func start() {
        guard !started else { return }
        started = true
        monitor.pathUpdateHandler = { [weak self] path in self?.update(path) }
        monitor.start(queue: queue)
    }

    func stop() {
        guard started else { return }
        started = false
        debounce?.cancel()
        monitor.cancel()
    }

    private func update(_ path: NWPath) {
        let wifi = path.usesInterfaceType(.wifi)
        let wired = path.usesInterfaceType(.wiredEthernet)
        let interface: NetworkInterface? =
            wifi ? .wifi : wired ? .wired : path.usesInterfaceType(.cellular) ? .cellular
            : path.status == .satisfied ? .other : nil
        let signature = path.availableInterfaces.map(\.name).joined(separator: ",") + "|"
            + path.gateways.map { "\($0)" }.joined(separator: ",")
        current = Snapshot(
            satisfied: path.status == .satisfied, hasWifiOrWired: wifi || wired,
            interface: interface, signature: signature)
        // The first report is the starting state, not a change.
        if !seenFirst {
            seenFirst = true
            notified = current
            return
        }

        debounce?.cancel()
        let work = DispatchWorkItem { [weak self] in
            guard let self, self.current != self.notified else { return }
            self.notified = self.current
            self.onChange?(self.current)
        }
        debounce = work
        queue.asyncAfter(deadline: .now() + .milliseconds(Limits.pathDebounceMs), execute: work)
    }
}
