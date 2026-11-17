import Foundation

/// The admit rule for WebKit script messages bound for Capacitor's `bridge`
/// handler. Pure so it is testable without WebKit.
///
/// A message reaches native code only when it comes from the top frame AND that
/// frame's security origin is the app's own origin. A sandboxed `data:` widget
/// frame fails both tests: it is not the main frame and its origin is opaque.
public enum BridgeOriginGuard {
    public typealias Origin = (scheme: String, host: String, port: Int)

    /// `port` is what WebKit reports for a security origin: 0 when the URL names
    /// no port. A configured default port (80 for http, 443 for https) is the
    /// same origin as no port.
    public static func admit(
        isMainFrame: Bool,
        scheme: String,
        host: String,
        port: Int,
        app: Origin
    ) -> Bool {
        guard isMainFrame else { return false }
        guard !scheme.isEmpty, !host.isEmpty else { return false }
        return scheme.lowercased() == app.scheme.lowercased()
            && host.lowercased() == app.host.lowercased()
            && effectivePort(scheme: scheme, port: port) == effectivePort(scheme: app.scheme, port: app.port)
    }

    private static func effectivePort(scheme: String, port: Int) -> Int {
        guard port == 0 else { return port }
        switch scheme.lowercased() {
        case "http": return 80
        case "https": return 443
        default: return 0
        }
    }
}
