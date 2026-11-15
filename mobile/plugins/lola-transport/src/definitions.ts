import type { PluginListenerHandle } from '@capacitor/core';

/**
 * The one typed JS surface of the native transport (spec 04 §1.2).
 *
 * Invariant: no method, listener or frame batch carries a secret. The pairing
 * secret, the QR's `tok`, every relay bearer and the private key stay native,
 * because Capacitor logs every resolved payload and listener event in Debug
 * builds. The app reaches this interface only through `mobile/src/native`.
 */

/** base64url(SHA-256(daemon SPKI)), no padding. */
export type DaemonId = string;
/** Client-side path name; `direct` is the daemon's `ReadyData.via` `lan`. */
export type PathKind = 'direct' | 'relay';
export type Phase = 'idle' | 'racing' | 'ready' | 'migrating' | 'closed' | 'failed';
export type FailureCode =
  /** NWPathMonitor reports no usable interface. */
  | 'offline'
  /** Every candidate failed (TCP, WebSocket, TLS before ready, or a wrong key). */
  | 'unreachable'
  | 'timeout'
  /** Every candidate that finished TLS showed the same non-pinned key. */
  | 'key_changed'
  /** `not_paired` err, or a TLS alert after the pinned handshake completed. */
  | 'not_paired'
  | 'unsupported_version'
  /** Relay was the only candidate and the hub answered 503 offline/busy or 504. */
  | 'relay_unavailable'
  | 'protocol'
  | 'peer_closed'
  | 'client_closed'
  | 'backgrounded'
  | 'internal';

export type RelayState = 'none' | 'ok' | 'backoff' | 'credential_dead' | 'hub_too_old';
export type ScanAuthorization = 'notDetermined' | 'authorized' | 'denied' | 'restricted';
export type ScanUnavailableReason = 'no_camera' | 'denied' | 'restricted' | 'unsupported';
export type PickSource = 'camera' | 'library';
export type ImageMime = 'image/jpeg' | 'image/png';
export type HapticKind = 'light' | 'medium' | 'rigid' | 'selection' | 'success' | 'warning' | 'error';
export type PairingOutcome = 'paired' | 'refused' | 'expired' | 'failed' | 'dropped';
export type ScanInvalid = 'not_lola' | 'old_format' | 'malformed';
export type InterfaceKind = 'wifi' | 'cellular' | 'wired' | 'other';

export interface PathInfo {
  kind: PathKind;
  host?: string;
  port?: number;
  interface?: InterfaceKind;
}

export interface DeviceInfo {
  deviceId: string;
  name: string;
  tiers: string[];
  host: string;
}

export interface PairedDaemon {
  daemonId: DaemonId;
  name: string;
  pairedAt: number;
  relay: RelayState;
  secureEnclave: boolean;
  lastPath?: PathKind;
}

export type ScanResult =
  | { cancelled: true }
  | { cancelled: false; pairRef: string; daemonId: DaemonId; addrs: number; relay: boolean }
  | { cancelled: false; invalid: ScanInvalid };

export type PickImageResult =
  | { cancelled: true }
  | { cancelled: false; mime: ImageMime; data: string; width: number; height: number };

export type ClipboardImageResult =
  | { present: false }
  | { present: true; mime: ImageMime; data: string };

export interface StatusResult {
  epoch: number;
  phase: Phase;
  path?: PathInfo;
  relay: RelayState;
  framesIn: number;
  framesOut: number;
  bytesIn: number;
  bytesOut: number;
  launchedAt: number;
}

export interface StateEvent {
  epoch: number;
  phase: Phase;
  /** On `ready` and `migrating`. */
  path?: PathInfo;
  /** On `failed` and `closed`. */
  code?: FailureCode;
  /** The daemon's `ErrPayload.code` when it spoke. */
  daemonCode?: string;
  /** With `unsupported_version`. */
  minV?: number;
  maxV?: number;
  /** On `ready`. */
  raceMs?: number;
  relay?: RelayState;
}

export interface PairingEvent {
  pairRef: string;
  outcome: PairingOutcome;
  daemonId?: DaemonId;
  name?: string;
  reason?: string;
}

export interface DevLinkEvent {
  source: 'dev-url' | 'dev-launch';
  route?: string;
  sheet?: string;
  pairRef?: string;
  daemonId?: DaemonId;
}

export interface FramesEvent {
  epoch: number;
  frames: string[];
}

export interface DeviceEvent {
  epoch: number;
  device: DeviceInfo;
}

export interface AppStateEvent {
  active: boolean;
}

export interface LolaTransportPlugin {
  // --- pairing ------------------------------------------------------------
  scanCapability(): Promise<{
    available: boolean;
    authorization: ScanAuthorization;
    reason?: ScanUnavailableReason;
  }>;
  /** Opens the scanner. A `lola2.` payload is held NATIVELY; only its public half comes back. */
  scanPairing(opts?: { prompt?: string }): Promise<ScanResult>;
  /** Races to one TLS session, sends `remote.pair`, resolves right after the write with the locally computed code. */
  pairBegin(opts: { pairRef: string; deviceName: string }): Promise<{ code: string; path: PathInfo }>;
  /** Abandons a pairing: closes its socket and drops the held payload. */
  pairCancel(opts: { pairRef: string }): Promise<void>;

  // --- paired daemons -----------------------------------------------------
  daemons(): Promise<{ daemons: PairedDaemon[] }>;
  /** Renames locally only (display). */
  rename(opts: { daemonId: DaemonId; name: string }): Promise<void>;
  /** Deletes key, certificate, pairing record and relay credentials. Idempotent. */
  forget(opts: { daemonId: DaemonId }): Promise<void>;

  // --- connection ---------------------------------------------------------
  /** Races every known path. Resolves when one sent `ready`. */
  connect(opts: { daemonId: DaemonId }): Promise<{
    epoch: number;
    path: PathInfo;
    raceMs: number;
    device: DeviceInfo;
  }>;
  disconnect(opts?: { reason?: string }): Promise<void>;
  /**
   * Complete JSON bodies; the plugin adds the length prefix. Rejects on a
   * closed epoch, and rejects a whole batch that touches native state (an `id`
   * starting `lola-` or `cmd == "relayCred"`).
   */
  send(opts: { epoch: number; frames: string[] }): Promise<void>;
  /**
   * Accepts a migration the plugin announced with `migrating`. Rejects with
   * `client_closed` when that epoch did not become live (its socket died
   * first); the `ready` state event, not this call, is what makes it current.
   */
  adopt(opts: { epoch: number }): Promise<void>;
  status(): Promise<StatusResult>;

  // --- device -------------------------------------------------------------
  pickImage(opts: { source: PickSource }): Promise<PickImageResult>;
  readClipboardImage(): Promise<ClipboardImageResult>;
  haptic(opts: { kind: HapticKind }): Promise<void>;
  /** http/https, or exactly `app-settings:` (this app's Settings page); enforced natively. */
  openURL(opts: { url: string }): Promise<void>;
  /** Keeps the screen awake while a pairing waits for the Mac. */
  setIdleTimerDisabled(opts: { disabled: boolean }): Promise<void>;

  addListener(
    event: 'frames',
    listener: (ev: FramesEvent) => void,
  ): Promise<PluginListenerHandle>;
  addListener(
    event: 'state',
    listener: (ev: StateEvent) => void,
  ): Promise<PluginListenerHandle>;
  addListener(
    event: 'device',
    listener: (ev: DeviceEvent) => void,
  ): Promise<PluginListenerHandle>;
  addListener(
    event: 'pairing',
    listener: (ev: PairingEvent) => void,
  ): Promise<PluginListenerHandle>;
  addListener(
    event: 'appState',
    listener: (ev: AppStateEvent) => void,
  ): Promise<PluginListenerHandle>;
  addListener(
    event: 'devLink',
    listener: (ev: DevLinkEvent) => void,
  ): Promise<PluginListenerHandle>;
  removeAllListeners(): Promise<void>;
}

/** = `protocol.MaxFrameBytes`. */
export const LOLA_MAX_FRAME_BYTES = 1 << 20;
/** = `config.DefaultRemotePort`. */
export const LOLA_DEFAULT_PORT = 7717;

/** Mirrors the Swift `Limits` enum; `wire/limits.test.ts` asserts they agree. */
export const MAX_PAIR_PAYLOAD_CHARS = 1024;
export const PAIR_HOLD_MS = 300000;
export const MAX_PICK_LONG_EDGE_PX = 2048;
export const MAX_PICK_BYTES = 4 * 1024 * 1024;
export const MAX_CLIPBOARD_BYTES = 8 * 1024 * 1024;
export const FLUSH_INTERVAL_MS = 16;
export const MAX_BATCH_BYTES = 262144;
export const HAPTIC_MIN_GAP_MS = 50;
