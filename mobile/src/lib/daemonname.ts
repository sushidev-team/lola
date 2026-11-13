// What to call the Mac on the other end.
//
// WHY AN ADDRESS IS THE WRONG NAME FOR IT. Now that a phone can find the daemon
// by browsing (see discovery.ts), the address is the least stable thing about
// it: the same Mac is 192.168.10.160 at home, something else at the office, and
// something else again on a hotspot. "Connecting to 192.168.10.160…" describes a
// network rather than the machine somebody left work running on, and after a
// move it describes a network that is not even there any more.
//
// TWO SOURCES, AND THE USER'S WINS. The daemon reports its own hostname on an
// AUTHENTICATED answer (`cmd=status` → `host`), which is the sensible default
// and needs no typing. A person may then rename it, because hostnames are
// frequently neither chosen nor readable — "Martins-MacBook-Pro" is a fine name
// for a machine and a poor one for a UI, and somebody with two Macs will want
// "work" and "home" rather than two variations on their own name.
//
// PER ENDPOINT, not global. A phone can be paired with more than one Mac, and
// the name has to follow the one it belongs to; the key is the same endpoint id
// the Keychain entry uses, so both halves of "which daemon" agree.
//
// The three rules prefs.ts states are followed exactly: a namespaced key,
// try/catch on both sides because a WKWebView can have storage disabled or
// partitioned, and a tolerant read that validates rather than trusting what it
// finds. The learned half is validated for a second reason — it crossed a
// network, and it is rendered.

const LEARNED_KEY = "lola.mobile.daemonNames";
const CUSTOM_KEY = "lola.mobile.daemonLabels";

/**
 * The longest name that will be stored or shown.
 *
 * A name is rendered into sentences ("Disconnect from …") and into an
 * accessible label, so an unbounded one is a layout problem rather than a
 * security one — but it arrives over a network, so it is bounded here rather
 * than trusted to be sensible.
 */
export const NAME_MAX = 40;

/**
 * Make a name safe to store and to render.
 *
 * Strips control characters (this text is interpolated into UI copy), collapses
 * whitespace, and clips. Returns "" for anything left empty, which every caller
 * treats as "no name" rather than as an empty label.
 */
export function normalizeName(v: string): string {
  let out = "";
  for (const ch of v) {
    const code = ch.codePointAt(0) ?? 0;
    // A control character that SEPARATES words becomes a space; every other one
    // is dropped. Deleting a newline outright would weld two words together
    // ("marvin\nlab" -> "marvinlab"), which is a worse answer than either
    // keeping it or refusing the name.
    if (
      ch === "\n" ||
      ch === "\r" ||
      ch === "\t" ||
      ch === "\f" ||
      ch === "\v"
    ) {
      out += " ";
      continue;
    }
    if (code < 0x20 || code === 0x7f) continue;
    out += ch;
  }
  return out.replace(/\s+/g, " ").trim().slice(0, NAME_MAX);
}

function readMap(key: string): Record<string, string> {
  try {
    const raw = globalThis.localStorage?.getItem(key);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed))
      return {};
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (k === "" || typeof v !== "string") continue;
      const name = normalizeName(v);
      if (name !== "") out[k] = name;
    }
    return out;
  } catch {
    return {};
  }
}

function writeMap(key: string, m: Record<string, string>): void {
  try {
    if (Object.keys(m).length === 0) globalThis.localStorage?.removeItem(key);
    else globalThis.localStorage?.setItem(key, JSON.stringify(m));
  } catch {
    /* a name is not worth failing a screen over */
  }
}

/** What the daemon last said its machine is called, per endpoint. */
export function learnedNames(): Record<string, string> {
  return readMap(LEARNED_KEY);
}

/** What a person renamed it to, per endpoint. */
export function customNames(): Record<string, string> {
  return readMap(CUSTOM_KEY);
}

/**
 * Record what the daemon reported. Silently ignores an empty name, so a daemon
 * too old to send one cannot erase a name already learned.
 */
export function learnDaemonName(endpoint: string, host: string): void {
  const name = normalizeName(host);
  if (endpoint === "" || name === "") return;
  const all = learnedNames();
  if (all[endpoint] === name) return;
  all[endpoint] = name;
  writeMap(LEARNED_KEY, all);
}

/**
 * Rename it, or clear the override with "".
 *
 * Clearing falls back to the daemon's own name rather than to the address,
 * which is what makes the rename feel undoable rather than destructive.
 */
export function renameDaemon(endpoint: string, name: string): void {
  if (endpoint === "") return;
  const all = customNames();
  const clean = normalizeName(name);
  if (clean === "") delete all[endpoint];
  else all[endpoint] = clean;
  writeMap(CUSTOM_KEY, all);
}

/** Forget both names for an endpoint. For `forget()`, which unpairs it. */
export function forgetDaemonName(endpoint: string): void {
  for (const key of [LEARNED_KEY, CUSTOM_KEY]) {
    const all = readMap(key);
    if (!(endpoint in all)) continue;
    delete all[endpoint];
    writeMap(key, all);
  }
}

/**
 * What to call this daemon, in order: the user's name, the daemon's own, then
 * the address it is being dialled at, then a last-resort phrase.
 *
 * The fallback chain is the whole design. Every earlier link can be absent —
 * nobody renamed it, the daemon predates the `host` field, the address is not
 * known yet during a boot — and each fallback still names something true.
 */
export function daemonLabel(endpoint: string, address = ""): string {
  const custom = customNames()[endpoint];
  if (custom) return custom;
  const learned = learnedNames()[endpoint];
  if (learned) return learned;
  const addr = normalizeName(address);
  return addr !== "" ? addr : "the daemon";
}

/** Whether a person has renamed this one, so a form can offer to undo it. */
export function hasCustomName(endpoint: string): boolean {
  return !!customNames()[endpoint];
}
