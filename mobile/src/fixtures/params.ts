// The preview page's query, read once.
//
// Invariant: `route`, `sheet`, `theme` and `kb` are never trusted. An unknown
// fixture, a non-number or an out-of-range inset is ignored and the default stands.

import { isFixtureName, type FixtureName } from "./index";

export interface PreviewParams {
  /** The path to open, with any `?sheet=` it carries folded in. "" keeps the app's own first screen. */
  path: string;
  fixture: FixtureName | null;
  theme: string | undefined;
  /** Fake keyboard inset in px; 0 when absent or out of range. */
  kb: number;
}

const MAX_KB = 600;

/** Reads the page's query. Pure. */
export function readParams(search: string): PreviewParams {
  const q = new URLSearchParams(search);
  const route = q.get("route") ?? "";
  const sheet = q.get("sheet") ?? "";
  const fixture = q.get("fixture") ?? "";
  const kb = Number(q.get("kb"));
  const joined = route !== "" && sheet !== "" && !route.includes("sheet=") ? `${route}${route.includes("?") ? "&" : "?"}sheet=${encodeURIComponent(sheet)}` : route;
  return {
    path: joined,
    fixture: isFixtureName(fixture) ? fixture : null,
    theme: q.get("theme") ?? undefined,
    kb: Number.isFinite(kb) && kb > 0 ? Math.min(kb, MAX_KB) : 0,
  };
}
