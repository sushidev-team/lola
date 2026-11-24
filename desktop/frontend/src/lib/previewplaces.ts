// Dev routes follow the project or stack member to its current server.
// External sites retain their absolute address.
type Group = { name: string; urls: string[] };
export type PreviewPlace = { address: string; app?: string };
export type PreviewPlaces = { bookmarks: PreviewPlace[]; recent: PreviewPlace[] };

function webURL(raw: string) {
  try {
    const url = new URL(raw);
    if (["http:", "https:"].includes(url.protocol) && !url.username && !url.password) return url;
  } catch { /* Incomplete or stale entry. */ }
}
function serves(group: Group, origin?: string) {
  return !!origin && group.urls.some(raw => webURL(raw)?.origin === origin);
}

export function previewPlace(raw: string, groups: Group[]): PreviewPlace | undefined {
  const url = webURL(raw);
  if (!url) return;
  const group = groups.find(g => serves(g, url.origin));
  return group ? { address: url.pathname + url.search + url.hash, app: group.name } : { address: url.href };
}

export function resolvePlace(place: PreviewPlace, groups: Group[], current: string): string | undefined {
  if (place.app === undefined) return webURL(place.address)?.href;
  const group = groups.find(g => g.name === place.app);
  if (!group || !place.address.startsWith("/")) return;
  const currentOrigin = webURL(current)?.origin;
  const origin = serves(group, currentOrigin) ? currentOrigin : webURL(group.urls[0])?.origin;
  // Join onto a known origin, including for paths that start with two slashes.
  if (origin) return origin + place.address;
}

export function readPlaces(key: string): PreviewPlaces {
  const clean = (list: unknown): PreviewPlace[] => Array.isArray(list) ? list.flatMap(p => {
    if (!p || (p.app !== undefined && typeof p.app !== "string")) return [];
    if (typeof p.address === "string" && (p.app === undefined ? webURL(p.address) : p.address.startsWith("/"))) return [p];
    // Earlier development builds saved local bookmarks with the original port.
    const url = typeof p.href === "string" && webURL(p.href);
    if (!url) return [];
    return [p.app === undefined ? { address: url.href } : { app: p.app, address: url.pathname + url.search + url.hash }];
  }) : [];
  try {
    const saved = JSON.parse(localStorage.getItem(key) || "{}");
    return { bookmarks: clean(saved.bookmarks), recent: clean(saved.recent).slice(0, 20) };
  } catch { return { bookmarks: [], recent: [] }; }
}

export function savePlaces(key: string, places: PreviewPlaces) {
  try { localStorage.setItem(key, JSON.stringify(places)); } catch { /* Still available for this visit. */ }
}
