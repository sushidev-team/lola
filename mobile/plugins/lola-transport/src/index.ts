import { registerPlugin } from '@capacitor/core';

import type { LolaTransportPlugin } from './definitions';

/**
 * `LolaTransport` is the jsName declared by `LolaTransportPlugin.swift`'s
 * `CAPBridgedPlugin` conformance; the two strings must agree or every call
 * rejects with "not implemented". The web fallback loads lazily so a native
 * build never pulls it into the bundle graph.
 */
const LolaTransport = registerPlugin<LolaTransportPlugin>('LolaTransport', {
  web: () => import('./web').then((m) => new m.LolaTransportWeb()),
});

export * from './definitions';
export { LolaTransport };
