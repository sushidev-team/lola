import Capacitor
import Foundation
import LolaTransportCore
import os
import WebKit

/// Capacitor registers its `bridge` script handler for every frame, and its
/// handler dispatches a message without looking at the frame it came from, so
/// an agent-written widget iframe could call any plugin as the paired human.
/// This replaces that registration with one that admits only the top frame of
/// the app's own origin (`BridgeOriginGuard`). Every message type (`message`,
/// `js.error`, `cordova`) arrives through the one handler, so all pass the rule.
final class MainFrameOnlyHandler: NSObject, WKScriptMessageHandler {
    private weak var inner: WebViewDelegationHandler?
    private let appOrigin: BridgeOriginGuard.Origin

    init(inner: WebViewDelegationHandler, appOrigin: BridgeOriginGuard.Origin) {
        self.inner = inner
        self.appOrigin = appOrigin
    }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        let origin = message.frameInfo.securityOrigin
        guard BridgeOriginGuard.admit(
            isMainFrame: message.frameInfo.isMainFrame,
            scheme: origin.protocol,
            host: origin.host,
            port: origin.port,
            app: appOrigin
        ) else {
            bridgeGuardLog.warning("bridge-guard dropped")
            return
        }
        inner?.userContentController(controller, didReceive: message)
    }
}

private let bridgeGuardLog = Logger(subsystem: "dev.sushi.lola.mobile", category: "transport")

/// Installs the guard on Capacitor's script handler. Returns false when it
/// cannot, in which case the caller must reject every plugin method.
///
/// Called first in `LolaTransportPlugin.load()`, which `CapacitorBridge`
/// runs while registering plugins, before the web view loads any page script.
/// The guard is read against Capacitor iOS 8.5.0 (see capacitorguard.test.ts).
@discardableResult
func installBridgeGuard(bridge: CAPBridgeProtocol?) -> Bool {
    guard let capacitor = bridge as? CapacitorBridge else { return false }
    let url = capacitor.config.serverURL
    guard let scheme = url.scheme, let host = url.host else { return false }
    let handler = capacitor.webViewDelegationHandler
    let app: BridgeOriginGuard.Origin = (scheme, host, url.port ?? 0)
    handler.contentController.removeScriptMessageHandler(forName: "bridge")
    handler.contentController.add(MainFrameOnlyHandler(inner: handler, appOrigin: app), name: "bridge")
    return true
}
