import Capacitor
import Foundation
import UIKit

// The bridge half of the development URL hand-off. Read the header of
// LolaDevLink.swift first: it explains why this exists, why it is not the
// pairing mechanism, and why it is not in a shipped build.

#if LOLA_DEV_LINK

extension LolaTransportPlugin {
    /// Called once from `load()`. Idempotent.
    func startDevLinkObservation() {
        LolaDevLinkObserver.shared.onPayload = { [weak self] payload in
            self?.emitDevLink(payload)
        }
        LolaDevLinkObserver.shared.install()
    }

    private func emitDevLink(_ payload: LolaDevLink.Payload) {
        var data: [String: Any] = [
            // The stamp the app's banner keys off, and the one fact that
            // decides whether the app may connect on its own. It is in the
            // payload rather than inferred at the call site so that there is
            // exactly one thing to test, and so that a scan and a development
            // link can never be confused for one another by whoever renders
            // them. `dev-url` may only fill the form; `dev-launch` may connect.
            // See LolaDevLink.Arrival for why those are different doors.
            "source": payload.arrival.rawValue,
            "host": payload.host,
        ]
        if let port = payload.port { data["port"] = port }
        if let pin = payload.spkiPin { data["spkiPin"] = pin }
        if let key = payload.insecureKey { data["insecureKey"] = key }
        // A destination, not a capability: the app opens this pane once it is
        // connected, and a link that never connects cannot use it. It is what
        // makes the terminal — the screen this whole app is a bet on — reachable
        // by a script, since the Simulator exposes no way to tap a list row.
        if let pane = payload.pane { data["pane"] = pane }
        if let session = payload.session { data["session"] = session }
        // The rest of the destination, for the same reason and behind the same
        // fence: which filter the list arrives under, which tab is showing, and
        // which sheet is open when it does. Every one of those surfaces — the
        // three tab screens beyond the session list, the filter overlay, the
        // connection settings, the terminal's view settings and its session
        // menu — is reachable only by a tap, which is the one thing a Simulator
        // cannot be asked to perform.
        if let triage = payload.triage { data["triage"] = triage }
        if let query = payload.query { data["query"] = query }
        if let sheet = payload.sheet { data["sheet"] = sheet }
        if let tab = payload.tab { data["tab"] = tab }
        // And the Projects tab's own depth: which project is drilled into and
        // which picker is open over it. Two more taps deep than the tab itself,
        // and therefore two more screens no script could otherwise reach.
        if let project = payload.project { data["project"] = project }
        if let pick = payload.pick { data["pick"] = pick }

        // Retained until consumed. A cold launch delivers the URL while the
        // WebView is still loading, so an ordinary event would be posted to no
        // listeners at all and the automation path would work only when the app
        // happened to be running already. Capacitor holds a retained event and
        // replays it to the first listener that registers.
        notifyListeners("devLink", data: data, retainUntilConsumed: true)

        // Separate from the "accepted" line in the observer, because the two
        // failures they distinguish look identical from the outside: a URL that
        // parsed but reached no plugin, and one that reached the bridge and is
        // waiting for the app to register a listener.
        LolaLog.info("dev link posted to the bridge")
    }
}

/// Watches for a `lola-dev://` URL and hands the parsed result over once.
///
/// A singleton rather than plugin state because a Swift extension cannot add
/// stored properties, and because the notifications it observes are global.
final class LolaDevLinkObserver {
    static let shared = LolaDevLinkObserver()

    var onPayload: ((LolaDevLink.Payload) -> Void)?

    private var installed = false
    private var lastURL: String?
    private var lastAt = Date.distantPast

    /// Two notifications describe the same arrival, and which one fires depends
    /// on whether the app was already running:
    ///
    ///   - cold launch: the URL rides in the scene's connection options, and
    ///     Capacitor posts `capacitorSceneWillConnect`;
    ///   - already running: `capacitorSceneOpenURL`.
    ///
    /// `capacitorOpenURL` is the pre-scene application-delegate path, observed
    /// as well because it costs one line and covers a host application that
    /// routes URLs the older way.
    func install() {
        guard !installed else { return }
        installed = true

        let center = NotificationCenter.default
        let names: [Notification.Name] = [
            .capacitorSceneOpenURL,
            .capacitorSceneWillConnect,
            .capacitorOpenURL,
        ]
        for name in names {
            center.addObserver(forName: name, object: nil, queue: .main) { [weak self] note in
                self?.handle(note.object, arrival: .url)
            }
        }

        // A launch environment or argv, read once. This is the path an agent
        // actually uses; see `LolaDevLink.environmentVariable` for why the URL
        // alone is not enough on a current iOS.
        if let launched = LolaDevLink.launchURL() {
            handle(launched, arrival: .launch)
        }

        // The cold-launch race, from the other side. `load()` runs while the
        // bridge view controller is being built, which the scene delegate does
        // BEFORE it forwards to Capacitor's proxy - so the notification above
        // should always land after this observer exists. Should is not is: the
        // proxy also records the URL it saw, so ask it once on the way in.
        handle(nil, arrival: .url)
    }

    /// Resolves a URL out of whatever shape the notification carried, falling
    /// back to the proxy's record, then parses and delivers it.
    private func handle(_ object: Any?, arrival: LolaDevLink.Arrival) {
        guard let url = url(from: object) ?? SceneDelegateProxy.shared.lastURL else { return }
        guard url.scheme?.lowercased() == LolaDevLink.scheme else { return }

        // One arrival can fire two of the notifications above, so an identical
        // URL seen again immediately is the same event. A deliberate re-run
        // minutes later is a different intent and must still work, which is why
        // this is a short window and not a permanent "seen" set.
        let text = url.absoluteString
        let now = Date()
        if text == lastURL, now.timeIntervalSince(lastAt) < 2 {
            return
        }
        lastURL = text
        lastAt = now

        guard var payload = LolaDevLink.parse(url) else {
            LolaLog.warn("dev link ignored: not a usable lola-dev://connect URL")
            return
        }
        // Stamped HERE rather than inside parse, because the parser sees only a
        // URL and the route is a property of how it reached this process.
        payload.arrival = arrival

        // Host and port are addresses, not secrets, and the two booleans are
        // what makes a failed automation run diagnosable without printing a
        // single byte of the pin or the key.
        LolaLog.info(
            "dev link accepted for \(payload.host):\(payload.port ?? 7717)"
                + " via=\(arrival.rawValue)"
                + " pin=\(payload.spkiPin != nil) key=\(payload.insecureKey != nil)"
                + " pane=\(payload.pane != nil)")

        onPayload?(payload)
    }

    private func url(from object: Any?) -> URL? {
        switch object {
        case let url as URL:
            return url
        case let context as UIOpenURLContext:
            return context.url
        case let contexts as Set<UIOpenURLContext>:
            return contexts.first?.url
        case let options as UIScene.ConnectionOptions:
            return options.urlContexts.first?.url
        case let info as [String: Any]:
            if let url = info["url"] as? URL { return url }
            if let text = info["url"] as? String { return URL(string: text) }
            return nil
        default:
            return nil
        }
    }
}

#else

extension LolaTransportPlugin {
    /// Compiled out of a release build. See the header of LolaDevLink.swift:
    /// this is a testing affordance for a Simulator that has no camera, not the
    /// pairing mechanism, and it must not exist where it is not being tested.
    func startDevLinkObservation() {}
}

#endif
