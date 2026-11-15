import { WebPlugin } from '@capacitor/core';

import type { LolaTransportPlugin } from './definitions';

const NO_TRANSPORT = 'no native transport';

/**
 * The browser implementation refuses everything that needs the device.
 *
 * Invariant: no browser API speaks this protocol (raw TLS over TCP, a pinned
 * self-signed key, a Secure Enclave identity), so a browser build has no
 * transport rather than a degraded one. It also never stores a secret: there is
 * no browser store worth the name, and a silent `localStorage` downgrade would
 * look like the feature works.
 */
export class LolaTransportWeb extends WebPlugin implements LolaTransportPlugin {
  private refuse(): never {
    throw this.unavailable(NO_TRANSPORT);
  }

  async scanCapability(): ReturnType<LolaTransportPlugin['scanCapability']> {
    return { available: false, authorization: 'denied', reason: 'unsupported' };
  }

  async scanPairing(): ReturnType<LolaTransportPlugin['scanPairing']> {
    return this.refuse();
  }

  async pairBegin(): ReturnType<LolaTransportPlugin['pairBegin']> {
    return this.refuse();
  }

  async pairCancel(): Promise<void> {
    // Nothing was ever held.
  }

  async daemons(): ReturnType<LolaTransportPlugin['daemons']> {
    return { daemons: [] };
  }

  async rename(): Promise<void> {
    this.refuse();
  }

  async forget(): Promise<void> {
    // Idempotent: nothing is stored.
  }

  async connect(): ReturnType<LolaTransportPlugin['connect']> {
    return this.refuse();
  }

  async disconnect(): Promise<void> {
    // Idempotent: nothing was ever open.
  }

  async send(): Promise<void> {
    this.refuse();
  }

  async adopt(): Promise<void> {
    this.refuse();
  }

  async status(): ReturnType<LolaTransportPlugin['status']> {
    return {
      epoch: 0,
      phase: 'closed',
      relay: 'none',
      framesIn: 0,
      framesOut: 0,
      bytesIn: 0,
      bytesOut: 0,
      launchedAt: 0,
    };
  }

  async pickImage(): ReturnType<LolaTransportPlugin['pickImage']> {
    return this.refuse();
  }

  async readClipboardImage(): ReturnType<LolaTransportPlugin['readClipboardImage']> {
    return { present: false };
  }

  async haptic(): Promise<void> {
    // No-op: a browser has nothing to tap.
  }

  async openURL(): Promise<void> {
    this.refuse();
  }

  async setIdleTimerDisabled(): Promise<void> {
    // No-op: a browser tab owns its own screen lock.
  }
}
