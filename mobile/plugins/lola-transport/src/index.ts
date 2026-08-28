import { registerPlugin } from '@capacitor/core';

import type { LolaTransportPlugin } from './definitions';

/**
 * `LolaTransport` is the jsName declared by `LolaTransportPlugin.swift`'s
 * `CAPBridgedPlugin` conformance. The two strings must agree or the bridge
 * resolves nothing and every call rejects with "not implemented".
 *
 * The web fallback is loaded lazily so that a native build never pulls it into
 * the bundle graph at all. It does not implement the transport — a browser
 * cannot open a raw TLS socket — it explains why, which is more useful than a
 * silent no-op.
 */
const LolaTransport = registerPlugin<LolaTransportPlugin>('LolaTransport', {
  web: () => import('./web').then((m) => new m.LolaTransportWeb()),
});

export * from './definitions';
export { LolaTransport };
