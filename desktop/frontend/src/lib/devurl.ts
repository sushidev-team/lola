// Presentation for the local testing addresses the daemon scraped out of a dev
// tab's pane (internal/devurl ships them on SessionInfo.devUrls).

/**
 * The label for a dev URL chip: the address a human recognises, without the
 * scheme they never type and without a trailing slash. `http://127.0.0.1:8001`
 * → `127.0.0.1:8001`, `https://nori-app.test/` → `nori-app.test`.
 *
 * The host is NOT rewritten — 127.0.0.1 stays 127.0.0.1. The chip has to match
 * what the pane printed, or it stops being verifiable at a glance.
 */
export function devUrlLabel(url: string): string {
  const bare = url.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
  return bare || url;
}

/**
 * How many address chips a session row shows. `composer dev` prints an app
 * server AND a bundler; both are worth one click. Anything past that is a log,
 * and the row is not one.
 */
export const MAX_URL_CHIPS = 2;

type DevApp = { name: string; label?: string; ports?: number[] | null; devUrls?: string[] | null };
type DevGroup = { name: string; label: string; urls: string[] };

/** Stack snapshots carry addresses by app. Older daemons carry only the
 * ranked session list and each member's lease, which can identify a local port. */
export function devUrlGroups(urls: string[], apps: DevApp[]): DevGroup[] {
  if (!apps.length) return [{ name: "", label: "", urls }];
  const groups = apps.map((app) => ({
    name: app.name, label: app.label || app.name, urls: [...(app.devUrls ?? [])],
  }));
  const reported = new Set(groups.flatMap((group) => group.urls));
  const other: DevGroup = { name: "", label: "Other addresses", urls: [] };
  for (const url of urls) {
    if (reported.has(url)) continue;
    let port = 0;
    try {
      const parsed = new URL(url);
      port = Number(parsed.port || (parsed.protocol === "https:" ? 443 : 80));
    } catch { /* Keep an unmatched legacy address visible. */ }
    const owners = apps.filter((app) => app.ports?.includes(port));
    const group = owners.length === 1 ? groups.find((group) => group.name === owners[0].name) : undefined;
    (group ?? other).urls.push(url);
  }
  return [...groups, other].filter((group) => group.urls.length);
}
