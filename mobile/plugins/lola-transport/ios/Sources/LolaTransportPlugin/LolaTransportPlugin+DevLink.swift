import Capacitor
import Foundation
import LolaTransportCore
import UIKit

// The bridge half of the development link. Read the header of LolaDevLink.swift
// first: it explains the fences and why this is not the pairing mechanism.

#if LOLA_DEV_LINK

    extension LolaTransportPlugin {
        /// Called once from `load()`. Idempotent.
        func startDevLinkObservation() {
            LolaDevLinkObserver.shared.onPayload = { [weak self] payload in self?.deliver(payload) }
            LolaDevLinkObserver.shared.install()
        }

        private func deliver(_ payload: LolaDevLink.Payload) {
            var data: [String: Any] = ["source": payload.arrival.rawValue]
            if let route = payload.route { data["route"] = route }
            if let sheet = payload.sheet { data["sheet"] = sheet }

            guard let pair = payload.pair else { return emit(data) }
            queue.async {
                guard let decoded = try? PairPayload.decode(pair) else {
                    LolaLog.warn("dev link pair ignored: not a usable payload")
                    return self.emit(data)
                }
                let ref = self.pairing.hold(decoded)
                data["pairRef"] = ref
                data["daemonId"] = decoded.daemonId
                self.emit(data)
                // Only a `dev-launch` link may pair. A `dev-url` link fills the
                // Connect screen and waits for a human.
                guard payload.arrival == .launch else { return }
                self.pairing.begin(pairRef: ref, deviceName: "Simulator") { result in
                    // The code is a display value shown on the phone, not a secret.
                    if case .success(let begun) = result { LolaLog.devLinkNote("pair-code \(begun.code)") }
                }
            }
        }

        /// Retained until consumed: a cold launch delivers the link while the
        /// web view is still loading, and Capacitor replays a retained event to
        /// the first listener that registers.
        private func emit(_ data: [String: Any]) {
            notifyListeners("devLink", data: data, retainUntilConsumed: true)
            LolaLog.info("dev link posted to the bridge")
        }
    }

    /// Watches for a `lola-dev://` URL and hands the parsed result over once.
    /// A singleton because an extension cannot add stored properties and the
    /// notifications it observes are global.
    final class LolaDevLinkObserver {
        static let shared = LolaDevLinkObserver()

        var onPayload: ((LolaDevLink.Payload) -> Void)?

        private var installed = false
        private var lastURL: String?
        private var lastAt = Date.distantPast

        /// A cold launch posts `capacitorSceneWillConnect`, a running app
        /// `capacitorSceneOpenURL`, and a host routing the older way
        /// `capacitorOpenURL`.
        func install() {
            guard !installed else { return }
            installed = true
            let names: [Notification.Name] = [.capacitorSceneOpenURL, .capacitorSceneWillConnect, .capacitorOpenURL]
            for name in names {
                NotificationCenter.default.addObserver(forName: name, object: nil, queue: .main) { [weak self] note in
                    self?.handle(note.object, arrival: .url)
                }
            }
            if let launched = LolaDevLink.launchURL() { handle(launched, arrival: .launch) }
            // The cold-launch race from the other side: the proxy records the
            // URL it saw, so ask it once on the way in.
            handle(nil, arrival: .url)
        }

        private func handle(_ object: Any?, arrival: LolaDevLink.Arrival) {
            guard let url = url(from: object) ?? SceneDelegateProxy.shared.lastURL,
                url.scheme?.lowercased() == LolaDevLink.scheme
            else { return }

            // One arrival can fire two notifications; an identical URL seen again
            // at once is the same event, while a deliberate re-run minutes later
            // is a new intent.
            let text = url.absoluteString
            let now = Date()
            if text == lastURL, now.timeIntervalSince(lastAt) < 2 { return }
            lastURL = text
            lastAt = now

            guard var payload = LolaDevLink.parse(url) else {
                LolaLog.warn("dev link ignored: not a usable lola-dev://open URL")
                return
            }
            payload.arrival = arrival
            LolaLog.info(
                "dev link accepted via=\(arrival.rawValue) route=\(payload.route != nil) sheet=\(payload.sheet != nil) pair=\(payload.pair != nil)"
            )
            onPayload?(payload)
        }

        private func url(from object: Any?) -> URL? {
            switch object {
            case let url as URL: return url
            case let context as UIOpenURLContext: return context.url
            case let contexts as Set<UIOpenURLContext>: return contexts.first?.url
            case let options as UIScene.ConnectionOptions: return options.urlContexts.first?.url
            case let info as [String: Any]: return (info["url"] as? URL) ?? (info["url"] as? String).flatMap(URL.init(string:))
            default: return nil
            }
        }
    }

#else

    extension LolaTransportPlugin {
        /// Compiled out of a release build: a testing affordance for a Simulator
        /// that has no camera must not exist where it is not being tested.
        func startDevLinkObservation() {}
    }

#endif
