// Fluid timestamps: as exact as the age needs, as short as it can be.
//
//   < 1 min   "now"
//   < 1 h     "5m"
//   < 1 day   "3h"          — the minutes inside an hours-old stamp are noise
//   < 7 days  "2d"
//   older     "Mar 4"       — a count of days stops meaning anything
//   other yr  "Mar 4, 2025"
//
// The daemon's formatAgo (internal/daemon/server.go) emits the same units for
// clients that only get the string (mobile, TUI); TimeAgo.svelte renders this
// one live and puts the exact moment (exactTime) in a hover card.

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

/** Milliseconds since the epoch, or NaN for an empty / unparsable stamp. */
export function toMillis(at: string | number | Date | undefined | null): number {
  if (at == null || at === "") return NaN;
  if (at instanceof Date) return at.getTime();
  if (typeof at === "number") return at;
  // Go's zero time ("0001-01-01T00:00:00Z") is "never", not two millennia ago.
  const t = Date.parse(at);
  return t <= 0 ? NaN : t;
}

/** The concise age of `at` relative to `now`; "" for an unknown time. A
 * relative unit form ("5m") is `rel`, a calendar date is not — callers append
 * "ago" only to the former. */
export function fluidAgo(at: string | number | Date | undefined | null, now: number = Date.now()): { text: string; rel: boolean } {
  const t = toMillis(at);
  if (Number.isNaN(t)) return { text: "", rel: false };
  const d = Math.max(0, now - t);
  if (d < MIN) return { text: "now", rel: false };
  if (d < HOUR) return { text: `${Math.floor(d / MIN)}m`, rel: true };
  if (d < DAY) return { text: `${Math.floor(d / HOUR)}h`, rel: true };
  if (d < 7 * DAY) return { text: `${Math.floor(d / DAY)}d`, rel: true };
  const date = new Date(t);
  const sameYear = date.getFullYear() === new Date(now).getFullYear();
  return {
    text: date.toLocaleDateString(undefined, sameYear ? { month: "short", day: "numeric" } : { month: "short", day: "numeric", year: "numeric" }),
    rel: false,
  };
}

/** The phrase form: "just now", "5m ago", "Mar 4". */
export function agoPhrase(at: string | number | Date | undefined | null, now: number = Date.now()): string {
  const { text, rel } = fluidAgo(at, now);
  if (!text) return "";
  if (text === "now") return "just now";
  return rel ? `${text} ago` : text;
}

/** The exact moment for a hover card: "Sat, Oct 11, 2026, 11:56:44". */
export function exactTime(at: string | number | Date | undefined | null): string {
  const t = toMillis(at);
  if (Number.isNaN(t)) return "";
  return new Date(t).toLocaleString(undefined, {
    weekday: "short",
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/** How long until the displayed text can next change, so a live label ticks
 * only as often as it needs to (every minute under an hour, then hourly). */
export function nextTickIn(at: string | number | Date | undefined | null, now: number = Date.now()): number {
  const t = toMillis(at);
  if (Number.isNaN(t)) return HOUR;
  const d = Math.max(0, now - t);
  const step = d < HOUR ? MIN : d < DAY ? HOUR : DAY;
  return step - (d % step) + 50;
}
