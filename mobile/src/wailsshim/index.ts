// The shim's public surface, for mobile-only code.
//
// The SHARED components never import this module: they import
// `@bindings/desktop` and `@wailsio/runtime`, which vite.config.ts points at
// desktop.ts and runtime.ts, and that indirection is exactly what lets them be
// reused without an edit. This barrel is for the mobile app's own shell — the
// connect screen, the pane view's touch adapter, the tests — which legitimately
// know they are talking to a shim.

export { ChannelTransport, type ChannelFactory, type ChannelTransportOptions } from "./channeltransport";
export { ShimBridge, bridge, DEFAULT_POLL_INTERVAL_MS, type BridgeOptions } from "./bridge";
export { useTransport } from "./bridge";
export { ShimNotConnectedError, UnsupportedOnMobileError, unsupported } from "./errors";
export { renderResync, renderResyncBase64, utf8ToBase64 } from "./screen";
export * as Events from "./events";
export { emit } from "./events";
export type { WailsEvent, WailsEventCallback } from "./events";

// The service namespaces, for a mobile view that would rather call one directly
// than pretend to be a Wails binding.
export {
  ConfigService,
  DaemonService,
  DoctorService,
  LinearService,
  TermService,
  UpdateService,
} from "./desktop";

// capacitorchannel.ts is deliberately NOT re-exported. It is the one file that
// imports @capacitor/core and the native plugin, and a test importing this
// barrel would otherwise pull both into a jsdom environment that has no bridge
// for them. `main.ts` imports it directly, which is the only place that should.
