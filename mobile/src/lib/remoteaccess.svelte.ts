// The "Away from Wi-Fi" row of Settings and the connection sheet: can this
// phone reach the Mac through the team hub relay, and if not, why.
//
// Two sources, combined here and nowhere else:
//  - this phone's relay state for the Mac (the plugin's, `RelayState`): does it
//    hold a credential, and did the hub refuse it;
//  - while connected, the Mac's own relay state (`hubStatus.relay`, tier read):
//    is the Mac in a team hub, is the relay on, does the hub have it.
// The Mac's answer explains more, so it is asked first. Every sentence is
// fixed copy; nothing from the hub or the daemon's free-text error is shown,
// and no credential ever reaches this module (it stays native).

import type { RelayState, StatusResult } from "lola-transport";
import type { HubRelayInfo } from "@bindings/internal/protocol";
import { HubService } from "@mobile/wailsshim";
import { copy } from "./copy";
import { connection } from "./connection.svelte";
import { readStatus } from "./settings.svelte";
import { can } from "./tiers.svelte";

export type RemoteAccessKey = Exclude<keyof typeof copy.remoteAccess, "label">;

/**
 * Which sentence applies. `phone` is this phone's relay state for the Mac
 * (undefined when unknown); `mac` is the Mac's relay as hubStatus reports it,
 * undefined when it was not read (not connected, refused, an older daemon).
 */
export function remoteAccessKey(phone: RelayState | undefined, mac: HubRelayInfo | null | undefined): RemoteAccessKey {
  if (mac) {
    switch (mac.state) {
      case "connected":
        // The Mac serves the relay; a phone with no credential for it is issued
        // one on connect, so having none yet is a step in progress, not a re-pair.
        return phone === "none" ? "settingUp" : "available";
      case "syncing":
      case "connecting":
        return "connecting";
      case "backoff":
        return "retrying";
      case "unsupported":
        return "hubUpdate";
      case "refused":
        return "refused";
    }
    switch (mac.reason) {
      case "not_joined":
        return "notJoined";
      case "hub_off":
        return "hubOff";
      case "revoked":
        return "refused";
      case "disabled":
      case "remote_off":
        return "turnedOff";
      case "config_error":
        return "configError";
      case "no_phones":
        // The Mac issues this phone a credential on connect; only a phone that
        // already holds one is a contradiction worth a new pairing.
        return phone === "none" || phone === undefined ? "settingUp" : "pairAgain";
    }
    return "turnedOff";
  }
  switch (phone) {
    case "ok":
      return "ready";
    case "backoff":
      return "retrying";
    case "hub_too_old":
      return "hubUpdate";
    case "credential_dead":
      return "expired";
    default:
      return "notSetUp";
  }
}

/** The row's short value and one-sentence detail. */
export function remoteAccessRow(key: RemoteAccessKey): { value: string; detail: string } {
  const c = copy.remoteAccess[key];
  return { value: c.value, detail: c.body };
}

/**
 * The live plugin state while a native session is up, else the state the
 * pairing record keeps for the current Mac.
 */
export function phoneRelay(status: StatusResult | null, stored: RelayState | undefined): RelayState | undefined {
  return status && status.phase === "ready" ? status.relay : stored;
}

class RemoteAccess {
  phone = $state<RelayState | undefined>(undefined);
  mac = $state<HubRelayInfo | null | undefined>(undefined);

  // The Mac's last answer is ignored while the connection is not ready: an
  // unreachable Mac must never read "Available".
  key = $derived<RemoteAccessKey>(remoteAccessKey(this.phone, connection.ready ? this.mac : undefined));
  row = $derived(remoteAccessRow(this.key));

  /** Reads both sources once. Never throws; a source that cannot answer is unknown. */
  async load(): Promise<void> {
    const stored = connection.daemons.find((d) => d.daemonId === connection.daemonId)?.relay;
    this.phone = phoneRelay(await readStatus(), stored);
    if (!connection.ready || !can("hubStatus")) {
      this.mac = undefined;
      return;
    }
    try {
      this.mac = (await HubService.Status()).relay ?? null;
    } catch {
      this.mac = undefined;
    }
  }
}

export const remoteAccess = new RemoteAccess();
