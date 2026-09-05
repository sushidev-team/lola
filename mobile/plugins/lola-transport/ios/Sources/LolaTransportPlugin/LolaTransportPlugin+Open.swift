import Capacitor
import Foundation
import UIKit

/// Handing a URL to the phone's browser.
///
/// WHY THIS EXISTS BESIDE `@capacitor/browser`. That plugin presents an
/// SFSafariViewController — in-app Safari with a Done button — and on this
/// app's dev-server links it RESOLVED WITHOUT PRESENTING ANYTHING: the JS saw
/// success, no error was reported, and no browser appeared. A promise that
/// resolves while nothing happens is the worst possible failure mode, because
/// every layer above it reports success.
///
/// `UIApplication.open` is the other mechanism: it hands the URL to the system,
/// which launches the real Safari app. It is the canonical "open outside my
/// app" call, it reports whether the system accepted the URL, and its failure
/// is observable rather than silent.
///
/// The two are not redundant. In-app Safari keeps the user inside the app,
/// which is nicer for a PR link; the system browser is what actually works for
/// a plain-http address on a private network. `openurl.ts` prefers this one and
/// keeps the other as a fallback.
extension LolaTransportPlugin {
    /// Open a URL in the phone's browser.
    ///
    /// RESOLVES with `{opened}` rather than rejecting on a URL the system will
    /// not take: "the phone declined this link" is an answer a caller renders,
    /// not an exception. It rejects only for a call with no usable URL at all,
    /// which is a programming error in the caller.
    ///
    /// The http(s) guard is applied HERE as well as in JavaScript. This method
    /// is reachable from any code in the web view, and the strings it is given
    /// come from terminal output and daemon answers; a `tel:` or a
    /// `shortcuts://` would otherwise be a small remote-action primitive.
    @objc func openURL(_ call: CAPPluginCall) {
        guard let raw = call.getString("url"), let url = URL(string: raw) else {
            call.reject("openURL needs a url")
            return
        }
        guard let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https" else {
            call.resolve(["opened": false, "reason": "scheme"])
            return
        }

        // UIApplication is main-thread only, and `open` is asynchronous: its
        // completion is the only honest report of whether the system took it.
        DispatchQueue.main.async {
            guard UIApplication.shared.canOpenURL(url) else {
                call.resolve(["opened": false, "reason": "cannot_open"])
                return
            }
            UIApplication.shared.open(url, options: [:]) { ok in
                call.resolve(["opened": ok, "reason": ok ? "" : "declined"])
            }
        }
    }
}
