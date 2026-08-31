import { WebPlugin } from '@capacitor/core';

import type {
  LolaConnectOptions,
  LolaConnectResult,
  LolaDisconnectOptions,
  LolaScanCapabilityResult,
  LolaScanOptions,
  LolaScanResult,
  LolaSecretGetOptions,
  LolaSecretGetResult,
  LolaSecretSetOptions,
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

  /**
   * There is no browser secret store worth the name, and this refuses rather
   * than reaching for `localStorage`.
   *
   * That is the whole point of the refusal. `localStorage` is plain,
   * unencrypted, per-origin storage that any script in the page can read; a
   * silent downgrade to it would be strictly worse for safety while LOOKING
   * like the feature works, which is the trade that must never be made
   * quietly. `secretstore.ts` probes for these methods, finds them throwing
   * here, and keeps the key in memory for the life of the page instead — worse
   * ergonomically, never worse for safety — and `isPersistent()` reports which
   * one is happening so the UI can say so.
   */
  async secretSet(_options: LolaSecretSetOptions): Promise<void> {
    throw this.unavailable(
      'LolaTransport has no secret store in a browser. Run on a device or simulator.',
    );
  }

  /**
   * Resolves `null` rather than throwing: "there is nothing stored" is the
   * honest answer for a browser and is exactly what the caller does with it.
   */
  async secretGet(_options: LolaSecretGetOptions): Promise<LolaSecretGetResult> {
    return { value: null };
  }

  async secretDelete(_options: LolaSecretGetOptions): Promise<void> {
    throw this.unavailable(
      'LolaTransport has no secret store in a browser. Run on a device or simulator.',
    );
  }

  /**
   * `unsupported` rather than `no_camera`: a browser may well have a camera,
   * and saying it does not would send someone looking at their hardware. What
   * is missing is the native scanner, and the caller's correct response is the
   * same either way - do not draw the button.
   */
  async scanCapability(): Promise<LolaScanCapabilityResult> {
    return { available: false, authorization: 'denied', reason: 'unsupported' };
  }

  async scanQR(_options?: LolaScanOptions): Promise<LolaScanResult> {
    throw this.unavailable(
      'LolaTransport has no scanner in a browser. Run on a device or simulator.',
    );
  }
}
