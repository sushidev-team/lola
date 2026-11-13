import Capacitor
import Foundation

/// The bridge half of local-network discovery. Everything with a radio in it
/// lives in `LolaDiscovery.swift`; this file only translates.
extension LolaTransportPlugin {
    /// The longest a browse may run, and the default when the caller names none.
    /// A human is waiting for this, and mDNS answers in well under a second on
    /// a network that has any answer at all.
    private static let discoveryDefaultMs = 2000
    private static let discoveryMaxMs = 10000

    /// Browse for daemons on this network.
    ///
    /// RESOLVES WITH AN EMPTY LIST rather than rejecting when nothing is found.
    /// Discovery is an optimisation over the addresses a phone already has: a
    /// network that blocks multicast, or a declined local-network permission,
    /// must read as "no candidates" and let the stored addresses be tried, not
    /// as an error a screen has to render.
    ///
    /// Each result is a CANDIDATE. Anything can advertise this service type, so
    /// the pin the caller already holds is what decides trust; the advertised
    /// pin is passed through only so an obvious impostor can be dropped without
    /// opening a socket.
    @objc func discover(_ call: CAPPluginCall) {
        let requested = call.getInt("timeoutMs") ?? Self.discoveryDefaultMs
        let ms = min(max(requested, 100), Self.discoveryMaxMs)

        LolaDiscovery.browse(timeout: TimeInterval(ms) / 1000) { found in
            call.resolve([
                "services": found.map { f in
                    [
                        "name": f.name,
                        "host": f.host,
                        "port": f.port,
                        "pin": f.pin,
                    ]
                }
            ])
        }
    }
}
