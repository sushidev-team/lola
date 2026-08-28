import { WebPlugin } from '@capacitor/core';

import type {
  LolaConnectOptions,
  LolaConnectResult,
  LolaDisconnectOptions,
  LolaSendOptions,
  LolaStatusResult,
  LolaTransportPlugin,
} from './definitions';

/**
 * The web implementation refuses, and says exactly why.
 *
 * There is no browser API that can speak this protocol. The daemon's listener
 * is `tls.NewListener` over a plain TCP socket with a four-byte length prefix:
 * there is no HTTP upgrade for `WebSocket` to perform, `fetch` cannot hold a
 * bidirectional byte stream, and even if one of them could, the certificate is
 * self-signed and no browser exposes a per-connection pinning hook. This is not
 * a gap waiting to be filled with more JavaScript; it is the reason the plugin
 * exists.
 *
 * The consequence for the development loop is worth stating plainly, because it
 * is easy to lose an afternoon to: `npm run dev` in a browser cannot reach a
 * daemon. Running against a real daemon means a device or simulator build, or a
 * bridge process that terminates the TLS socket outside the browser (a Vite
 * dev-server plugin holding the connection in Node and relaying frames over a
 * WebSocket to the page) — which is a piece of development tooling, not part of
 * the shipped transport, and is deliberately not implemented here.
 */
export class LolaTransportWeb extends WebPlugin implements LolaTransportPlugin {
  async connect(_options: LolaConnectOptions): Promise<LolaConnectResult> {
    throw this.unavailable(
      'LolaTransport speaks raw TLS over TCP, which a browser cannot open. ' +
        'Run on a device or simulator, or put a bridge process outside the page.',
    );
  }

  async disconnect(_options?: LolaDisconnectOptions): Promise<void> {
    // Idempotent by definition: nothing was ever open.
  }

  async send(_options: LolaSendOptions): Promise<void> {
    throw this.unavailable('LolaTransport is not available in a browser.');
  }

  async status(): Promise<LolaStatusResult> {
    return {
      epoch: 0,
      phase: 'closed',
      framesIn: 0,
      framesOut: 0,
      bytesIn: 0,
      bytesOut: 0,
    };
  }
}
