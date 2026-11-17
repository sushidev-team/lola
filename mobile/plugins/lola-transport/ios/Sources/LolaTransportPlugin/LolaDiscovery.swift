import Foundation
import Network

/// mDNS discovery of daemons on this network (`_lola._tcp`).
///
/// Results are UNAUTHENTICATED HINTS. Anything can advertise this service, so
/// the pin the phone already holds decides trust; a wrong candidate costs one
/// attempt. The connector starts a browse at t=0 of a race and appends results
/// as they arrive.
enum LolaDiscovery {
    struct Found: Equatable {
        let name: String
        let host: String
        let port: Int
    }

    static let serviceType = "_lola._tcp"

    /// Browses until `timeout` and reports each resolved service once. Returns
    /// a function that stops the browse.
    @discardableResult
    static func browse(timeout: TimeInterval, found: @escaping (Found) -> Void) -> () -> Void {
        let queue = DispatchQueue(label: "dev.sushi.lola.discovery")
        let parameters = NWParameters()
        parameters.includePeerToPeer = false
        let browser = NWBrowser(for: .bonjour(type: serviceType, domain: nil), using: parameters)

        var finished = false
        var seen = Set<String>()
        let stop = {
            guard !finished else { return }
            finished = true
            browser.cancel()
        }

        browser.browseResultsChangedHandler = { results, _ in
            for result in results {
                guard case let .service(name, type, domain, _) = result.endpoint, !finished else { continue }
                resolve(name: name, type: type, domain: domain, queue: queue) { host, port in
                    guard !finished, let host, let port, seen.insert("\(host):\(port)").inserted else { return }
                    found(Found(name: name, host: host, port: port))
                }
            }
        }
        browser.stateUpdateHandler = { state in
            switch state {
            case .failed, .cancelled: queue.async(execute: stop)
            default: break
            }
        }
        browser.start(queue: queue)
        queue.asyncAfter(deadline: .now() + timeout, execute: stop)
        return { queue.async(execute: stop) }
    }

    private static func resolve(
        name: String, type: String, domain: String, queue: DispatchQueue,
        completion: @escaping (String?, Int?) -> Void
    ) {
        let endpoint = NWEndpoint.service(name: name, type: type, domain: domain, interface: nil)
        let connection = NWConnection(to: endpoint, using: .tcp)
        var answered = false
        let answer: (String?, Int?) -> Void = { host, port in
            guard !answered else { return }
            answered = true
            connection.cancel()
            completion(host, port)
        }
        connection.stateUpdateHandler = { state in
            switch state {
            case .ready:
                guard case let .hostPort(host, port) = connection.currentPath?.remoteEndpoint else {
                    return answer(nil, nil)
                }
                answer(hostText(host), Int(port.rawValue))
            case .failed, .cancelled:
                answer(nil, nil)
            default:
                break
            }
        }
        connection.start(queue: queue)
    }

    private static func hostText(_ host: NWEndpoint.Host) -> String {
        switch host {
        case .ipv4(let v4): return "\(v4)"
        case .ipv6(let v6): return "\(v6)"
        case .name(let n, _): return n
        @unknown default: return "\(host)"
        }
    }
}
