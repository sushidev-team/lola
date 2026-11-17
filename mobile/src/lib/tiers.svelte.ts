// The device's tiers (spec §5.4). Set from `connect()`'s `device.tiers` and
// replaced by each `device` event, which the plugin emits on every new epoch's
// `ready`. The client list is a courtesy; the daemon gate is the truth.

import { transport } from "@mobile/native";
import { TIER, type Tier } from "@mobile/wire/tiers";

/**
 * The tier of a `pty` write frame (`frameTier` in internal/remote/tiers.go). Frames are not
 * commands, so they are absent from TIER; this is the one frame tier the client names.
 */
export const PTY_WRITE_TIER: Tier = "shell";

class Tiers {
  held = $state<readonly string[]>([]);
  private epoch = 0;

  /** Replaces the set. An event from an older epoch than one already seen is ignored. */
  set(tiers: readonly string[], epoch = this.epoch): void {
    if (epoch < this.epoch) return;
    this.epoch = epoch;
    this.held = [...tiers];
  }

  clear(): void {
    this.epoch = 0;
    this.held = [];
  }

  /** True when the device holds the tier `cmd` needs; `never` and unknown commands are false. */
  can(cmd: string): boolean {
    const tier = TIER[cmd];
    return tier !== undefined && tier !== "never" && this.held.includes(tier);
  }
}

export const tiers = new Tiers();

export function can(cmd: string): boolean {
  return tiers.can(cmd);
}

/** True when this device may type into a pane. */
export function canWritePane(): boolean {
  return tiers.held.includes(PTY_WRITE_TIER);
}

/** Follows the plugin's `device` events. Returns the teardown. */
export function installTiers(): () => void {
  let live = true;
  let handle: { remove(): unknown } | undefined;
  void transport
    .addListener("device", (e) => tiers.set(e.device.tiers, e.epoch))
    .then((h) => {
      if (live) handle = h;
      else void h.remove();
    })
    .catch(() => {});
  return () => {
    live = false;
    void handle?.remove();
  };
}
