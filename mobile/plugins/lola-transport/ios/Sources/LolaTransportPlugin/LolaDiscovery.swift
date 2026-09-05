import Foundation
import Network

/// Finding the daemon on whatever network this phone is on, by NAME rather than
/// by an address someone typed once.
///
/// WHY THIS EXISTS. The credentials a paired phone holds — the bearer key and
/// the SPKI pin — identify the DAEMON, not an address. Only the address goes
/// stale: the connect code carries the addresses the Mac had at pairing time, so
/// a phone paired at home finds nothing at the office, on a hotspot, or after
/// the router hands out a different lease. Browsing closes exactly that gap.
///
/// WHAT A RESULT IS, AND IS NOT. It is a CANDIDATE. Anything on the network can
/// advertise `_lola._tcp`, so a result is never trusted for being found: the
/// caller keeps the pin it already has and the TLS handshake rejects everything
/// else. The advertised pin is carried through only so an impostor can be
/// dropped before a socket is opened, which is a courtesy, not the check.
///
/// The whole API is one bounded call. A browser that lives across screens would
/// be a background radio nobody switched on, and iOS asks for local-network
/// permission the moment one starts — so it runs when a human is waiting for an
/// answer, and stops.
enum LolaDiscovery {
    /// One advertised daemon.
    struct Found {
        /// The service instance name, for a picker: "lola on marvin".
        let name: String
        /// A dialable host: an IPv4/IPv6 literal, or a `.local` name.
        let host: String
        let port: Int
        /// The SPKI pin from the TXT record, or "" when it carried none.
        let pin: String
    }

    /// The TXT key the daemon publishes its pin under. Mirrors
    /// `internal/mdns.TXTPin`.
    static let txtPinKey = "pin"

    /// The service type, mirroring `internal/mdns.ServiceType`. It is part of
    /// the contract with the daemon: changing one side alone means a phone that
    /// finds nothing.
    static let serviceType = "_lola._tcp"

    /// Browse for daemons, resolving each to an address, for at most `timeout`.
    ///
    /// Resolves with whatever was found when the window closes — an empty list
    /// is an ANSWER, not an error. Discovery is an optimisation over the stored
    /// addresses, and a caller that treated "nothing here" as a failure would
    /// turn a network without multicast into a broken app.
    static func browse(timeout: TimeInterval, completion: @escaping ([Found]) -> Void) {
        let queue = DispatchQueue(label: "dev.sushi.lola.discovery")
        let params = NWParameters()
        params.includePeerToPeer = false

        let browser = NWBrowser(
            for: .bonjourWithTXTRecord(type: serviceType, domain: nil),
            using: params
        )

        // One shared box: several connections resolve in parallel and the
        // deadline can fire while they do.
        let box = ResultBox()
        var finished = false
        let finish = {
            guard !finished else { return }
            finished = true
            browser.cancel()
            completion(box.take())
        }

        browser.browseResultsChangedHandler = { results, _ in
            for result in results {
                guard case let .service(name, type, domain, _) = result.endpoint else { continue }
                let pin = txtPin(from: result.metadata)
                resolve(name: name, type: type, domain: domain, queue: queue) { host, port in
                    guard let host, let port else { return }
                    box.add(Found(name: name, host: host, port: port, pin: pin))
                }
            }
        }

        browser.stateUpdateHandler = { state in
            switch state {
            case .failed, .cancelled:
                // A browser that cannot start is a network without multicast,
                // or a permission the user declined. Both mean "no candidates",
                // which the stored addresses already cover.
                queue.async(execute: finish)
            default:
                break
            }
        }

        browser.start(queue: queue)
        queue.asyncAfter(deadline: .now() + timeout, execute: finish)
    }

    /// Read the pin out of a service's TXT record, or "" when it has none.
    private static func txtPin(from metadata: NWBrowser.Result.Metadata) -> String {
        guard case let .bonjour(record) = metadata else { return "" }
        return record[txtPinKey] ?? ""
    }

    /// Turn a service name into an address by opening a connection far enough
    /// to learn one.
    ///
    /// NWBrowser reports SERVICES; only a connection resolves one to an
    /// endpoint. The connection is cancelled the moment it is ready — this is a
    /// name lookup, not the app's transport, which has its own pinned TLS.
    private static func resolve(
        name: String,
        type: String,
        domain: String,
        queue: DispatchQueue,
        completion: @escaping (String?, Int?) -> Void
    ) {
        let endpoint = NWEndpoint.service(name: name, type: type, domain: domain, interface: nil)
        let conn = NWConnection(to: endpoint, using: .tcp)
        var answered = false
        let answer: (String?, Int?) -> Void = { host, port in
            guard !answered else { return }
            answered = true
            conn.cancel()
            completion(host, port)
        }
        conn.stateUpdateHandler = { state in
            switch state {
            case .ready:
                guard case let .hostPort(host, port) = conn.currentPath?.remoteEndpoint else {
                    answer(nil, nil)
                    return
                }
                answer(hostText(host), Int(port.rawValue))
            case .failed, .cancelled:
                answer(nil, nil)
            default:
                break
            }
        }
        conn.start(queue: queue)
    }

    /// A dialable string for a resolved host.
    ///
    /// An IPv6 link-local address is meaningless without its zone, and
    /// `NWEndpoint.Host` renders it with one ("fe80::1%en0"), which is exactly
    /// what a later connection needs — so it is kept rather than stripped.
    private static func hostText(_ host: NWEndpoint.Host) -> String {
        switch host {
        case .ipv4(let v4):
            return "\(v4)"
        case .ipv6(let v6):
            return "\(v6)"
        case .name(let n, _):
            return n
        @unknown default:
            return "\(host)"
        }
    }

    /// Results collected off several queues, deduplicated by address.
    private final class ResultBox {
        private let lock = NSLock()
        private var found: [String: Found] = [:]

        func add(_ f: Found) {
            lock.lock()
            defer { lock.unlock() }
            found["\(f.host):\(f.port)"] = f
        }

        func take() -> [Found] {
            lock.lock()
            defer { lock.unlock() }
            // Sorted so the app's list does not reorder itself between browses
            // for no reason a user could see.
            return found.values.sorted { ($0.name, $0.host) < ($1.name, $1.host) }
        }
    }
}
