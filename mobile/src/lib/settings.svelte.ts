// What the Settings screen and the connection sheet show and do (spec §5.2).
//
// Invariants:
//  - Nothing here identifies or authenticates the Mac: a name is display only
//    and the pairing record, key and relay credential stay native.
//  - Unpairing goes through the shared confirm dialog, never a sheet, and
//    leaves the connection store to route: the last Mac gone leads to Connect.

import type { PathInfo, RelayState, StatusResult } from "lola-transport";
import { confirm } from "$lib/confirm.svelte";
import { transport } from "@mobile/native";
import pkg from "../../package.json";
import { copy, fill } from "./copy";
import { connection } from "./connection.svelte";
import { normalizeName } from "./daemonname";

export const settingsPhrases = {
  nameFor: "Name for this Mac",
  save: "Save",
  details: "Details",
  detailsTitle: "Connection",
  version: "Version",
  boot: "Last launch",
  followSystem: "Follow system",
} as const;

const INTERFACE: Record<string, string> = {
  wifi: "Wi-Fi",
  cellular: "Cellular",
  wired: "Wired",
};

/** "Direct · Wi-Fi", "Relay" or "Offline": the one phrase for the path in use. */
export function pathLabel(ready: boolean, path: PathInfo | null | undefined): string {
  if (!ready || !path) return copy.settings.path.offline;
  if (path.kind === "relay") return copy.settings.path.relay;
  const via = path.interface ? INTERFACE[path.interface] : undefined;
  return via ? `${copy.settings.path.direct} · ${via}` : copy.settings.path.direct;
}

/** "Direct", "Relay" or "Offline": the path in use as one word, without the interface. */
export function pathWord(ready: boolean, path: PathInfo | null | undefined): string {
  if (!ready || !path) return copy.settings.path.offline;
  return path.kind === "relay" ? copy.settings.path.relay : copy.settings.path.direct;
}

/** "Wi-Fi", "Cellular", "Wired" or "" for the interface a direct path runs on. */
export function interfaceLabel(path: PathInfo | null | undefined): string {
  return path?.interface ? (INTERFACE[path.interface] ?? "") : "";
}

/** The connection sheet's hero line: "Relay · 180 ms", or just the path word without a measured race. */
export function heroLine(ready: boolean, path: PathInfo | null | undefined, raceMs: number | null): string {
  const word = pathWord(ready, path);
  return ready && path && raceMs !== null ? `${word} · ${raceMs} ms` : word;
}

/** The hero glyph: globe for the relay, smartphone for a direct path or none (the icon set has no wifi mark). */
export function heroGlyph(ready: boolean, path: PathInfo | null | undefined): "globe" | "smartphone" {
  return ready && path?.kind === "relay" ? "globe" : "smartphone";
}

/** The "Via" row: the interface or relay a path uses, "—" when the path says nothing. */
export function viaLabel(ready: boolean, path: PathInfo | null | undefined): string {
  if (!ready || !path) return "—";
  if (path.kind === "relay") return copy.settings.path.relay;
  return interfaceLabel(path) || copy.settings.path.direct;
}

/** "3.4 KB, 12 frames": one direction of the traffic row. */
function traffic(frames: number, bytes: number): string {
  const kb = bytes / 1024;
  const size = bytes < 1024 ? `${bytes} B` : kb < 1024 ? `${kb.toFixed(1)} KB` : `${(kb / 1024).toFixed(1)} MB`;
  return `${size}, ${frames} frames`;
}

/** Diagnostics rows: Epoch, Last race, and Traffic collapsed into one row (frames and bytes, in and out). */
export function diagnosticRows(
  s: StatusResult | null,
  epoch: number,
  raceMs: number | null,
  trafficTemplate: string = copy.connection.trafficValue,
): DetailRow[] {
  const rows: DetailRow[] = [{ label: "Epoch", value: String(epoch) }];
  if (raceMs !== null) rows.push({ label: "Last race", value: `${raceMs} ms` });
  if (s) {
    rows.push({
      label: copy.connection.traffic,
      value: fill(trafficTemplate, { in: traffic(s.framesIn, s.bytesIn), out: traffic(s.framesOut, s.bytesOut) }),
    });
  }
  return rows;
}

const RELAY: Record<RelayState, string> = {
  none: "None",
  ok: "Ready",
  backoff: "Retrying",
  credential_dead: "Expired",
  hub_too_old: "Hub too old",
};

export interface DetailRow {
  label: string;
  value: string;
}

/** The rows of the connection sheet's Details disclosure. */
export function detailRows(
  s: StatusResult | null,
  epoch: number,
  path: PathInfo | null,
  raceMs: number | null = null,
): DetailRow[] {
  const rows: DetailRow[] = [{ label: "Path", value: pathLabel(path !== null, path) }];
  if (path?.host) rows.push({ label: "Address", value: path.port ? `${path.host}:${path.port}` : path.host });
  rows.push({ label: "Epoch", value: String(epoch) });
  if (raceMs !== null) rows.push({ label: "Last race", value: `${raceMs} ms` });
  if (s) {
    rows.push(
      { label: "Relay", value: RELAY[s.relay] ?? s.relay },
      { label: "Frames in", value: String(s.framesIn) },
      { label: "Frames out", value: String(s.framesOut) },
      { label: "Bytes in", value: String(s.bytesIn) },
      { label: "Bytes out", value: String(s.bytesOut) },
    );
  }
  return rows;
}

/** The plugin's live counters, or null when it cannot answer. */
export async function readStatus(): Promise<StatusResult | null> {
  try {
    return await transport.status();
  } catch {
    return null;
  }
}

/**
 * Cold start to the first painted Inbox, in ms: the `inbox-interactive` mark
 * against the plugin's `launchedAt` (epoch ms). Null until both exist.
 */
export function bootMs(launchedAt: number | undefined, now = Date.now()): number | null {
  if (!launchedAt || typeof performance === "undefined") return null;
  const mark = performance.getEntriesByName("inbox-interactive").at(-1);
  if (!mark) return null;
  const at = performance.timeOrigin + mark.startTime;
  const ms = Math.round(at - launchedAt);
  return ms >= 0 && at <= now ? ms : null;
}

export const appVersion: string = pkg.version;

/** Renames the Mac on this phone only. A name that normalizes to nothing is refused. */
export async function renameMac(raw: string): Promise<boolean> {
  const name = normalizeName(raw);
  if (name === "" || name === connection.label) return false;
  await connection.rename(name);
  return true;
}

/** Asks, then forgets the current Mac. Another paired Mac, if any, is connected next. */
export function confirmUnpair(): void {
  const id = connection.daemonId;
  confirm.ask({
    title: copy.settings.unpairConfirm.title,
    body: copy.settings.unpairConfirm.body,
    confirmLabel: copy.settings.unpairConfirm.action,
    onConfirm: () => {
      void (async () => {
        await connection.forget(id);
        if (connection.daemons.length > 0) void connection.connect();
      })();
    },
  });
}
