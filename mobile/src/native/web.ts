import type { LolaTransportPlugin } from "lola-transport";

/**
 * The browser and dev-server stand-in for the native transport.
 *
 * Invariant: it holds nothing and reaches nothing. Capability probes answer
 * "unsupported", every method that needs the device rejects, and `haptic` is a
 * no-op, so a screen renders in a plain browser without any call succeeding by
 * accident.
 */
export const NO_NATIVE_TRANSPORT = "no native transport";

const refuse = (): Promise<never> => Promise.reject(new Error(NO_NATIVE_TRANSPORT));

export const webTransport: LolaTransportPlugin = {
  scanCapability: async () => ({ available: false, authorization: "denied", reason: "unsupported" }),
  scanPairing: refuse,
  pairBegin: refuse,
  pairCancel: async () => {},
  daemons: async () => ({ daemons: [] }),
  rename: refuse,
  forget: async () => {},
  connect: refuse,
  disconnect: async () => {},
  send: refuse,
  adopt: refuse,
  status: async () => ({
    epoch: 0,
    phase: "closed",
    relay: "none",
    framesIn: 0,
    framesOut: 0,
    bytesIn: 0,
    bytesOut: 0,
    launchedAt: 0,
  }),
  pickImage: refuse,
  readClipboardImage: async () => ({ present: false }),
  haptic: async () => {},
  openURL: refuse,
  setIdleTimerDisabled: async () => {},
  addListener: async () => ({ remove: async () => {} }),
  removeAllListeners: async () => {},
};
