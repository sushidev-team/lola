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

type DevForward = { url: string; from: string };

function hostPort(url: string): string {
  try {
    const u = new URL(/^[a-z]+:\/\//i.test(url) ? url : `http://${url}`);
    const host = u.hostname.toLowerCase().replace(/^localhost$|^\[::1\]$/, "127.0.0.1");
    return `${host}:${u.port || (u.protocol === "https:" ? 443 : 80)}`;
  } catch {
    return "";
  }
}

/**
 * Where a PHONE opens a review page: the session's forward of that page's dev
 * server, with the page's own path. A page whose server is not forwarded has no
 * phone address (undefined) — the loopback address names the Mac, and nothing
 * here may guess another one. `localhost` and `127.0.0.1` are the same server.
 */
export function forwardedPageURL(pageUrl: string, forwards: readonly DevForward[] | null | undefined): string | undefined {
  const want = hostPort(pageUrl);
  if (!want) return undefined;
  const forward = (forwards ?? []).find((f) => hostPort(f.from) === want);
  if (!forward) return undefined;
  try {
    const page = new URL(pageUrl);
    const out = new URL(forward.url);
    out.pathname = page.pathname;
    out.search = page.search;
    out.hash = page.hash;
    return out.toString();
  } catch {
    return undefined;
  }
}
