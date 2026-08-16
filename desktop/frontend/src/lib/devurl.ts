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
