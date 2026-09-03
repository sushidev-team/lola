// Finding the daemon on a network whose addresses this phone has never seen.
//
// WHAT IS ACTUALLY STALE. A paired phone holds a bearer key and an SPKI pin,
// and neither mentions an address: both identify the DAEMON. What goes stale is
// only where to dial — the connect code carries the addresses the Mac had at
// pairing time, so a phone paired at home finds nothing at the office, on a
// hotspot, or after a new DHCP lease. The daemon advertises itself with
// `dns-sd` (internal/mdns) and this module is the phone's half of that.
//
// A RESULT IS A CANDIDATE, NEVER AN AUTHORITY. Anything on a network can
// advertise `_lola._tcp`, so nothing here is trusted for having been found: the
// pinned TLS handshake is what decides, exactly as it does for a typed address.
// The advertised pin is used ONLY to drop an obvious mismatch before a socket
// is opened — a cheap courtesy, not the check. A service advertising no pin at
// all is kept for the same reason: an older daemon is not an impostor, and the
// handshake will judge it either way.
//
// THE PLUGIN CONTRACT, reached through the Capacitor global rather than an
// import, for the two reasons scan.ts gives: the plugin's `dist/` does not
// exist until it is built, and a browser `npm run dev` session has no plugin at
// all and must still render the UI.
//
//	discover({ timeoutMs }) -> { services: [{ name, host, port, pin }] }
//
// NOTHING HERE IS AN ERROR PATH. No plugin, a declined local-network
// permission, a network that blocks multicast, a browse that finds nothing —
// all of them mean "no candidates", and the stored addresses are still tried.
// A caller that treated any of them as a failure would turn an ordinary
// corporate network into a broken app.

/** One advertised daemon, as the plugin reports it. */
export interface Discovered {
  /** The service instance name, for a picker: "lola on marvin". */
  name: string;
  host: string;
  port: number;
  /** The advertised SPKI pin, or "" when the service carried none. */
  pin: string;
}

/** An address worth trying, in the shape the connect flow already walks. */
export interface Candidate {
  host: string;
  port: number;
  name: string;
}

/** How long a browse may take. A human is waiting; mDNS answers in well under a second. */
export const DISCOVER_TIMEOUT_MS = 2000;

/** The slice of `LolaTransportPlugin` this module needs. */
interface DiscoveryCapablePlugin {
  discover?(o?: { timeoutMs?: number }): Promise<{ services?: unknown }>;
}

interface CapacitorGlobal {
  Plugins?: { LolaTransport?: DiscoveryCapablePlugin };
}

function plugin(): DiscoveryCapablePlugin | undefined {
  const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
  const p = cap?.Plugins?.LolaTransport;
  return p && typeof p.discover === "function" ? p : undefined;
}

/** Whether this build can browse at all. The connect screen asks before offering it. */
export function canDiscover(): boolean {
  return plugin() !== undefined;
}

/**
 * Validate one entry from the plugin.
 *
 * Everything crossing this boundary came off the NETWORK — an instance name and
 * a TXT record are written by whoever is advertising — so each field is checked
 * rather than trusted. A malformed entry is DROPPED rather than repaired: the
 * only thing done with one is opening a socket, and a repaired address is a
 * guess about somebody else's machine.
 */
function parseService(v: unknown): Discovered | null {
  if (typeof v !== "object" || v === null) return null;
  const o = v as Record<string, unknown>;
  const host = typeof o.host === "string" ? o.host.trim() : "";
  const port = typeof o.port === "number" ? Math.trunc(o.port) : 0;
  if (host === "" || port < 1 || port > 65535) return null;
  return {
    name: typeof o.name === "string" ? o.name.trim() : "",
    host,
    port,
    pin: typeof o.pin === "string" ? o.pin.trim() : "",
  };
}

/**
 * Browse for daemons. Resolves with [] whenever there is nothing to report,
 * including every failure — see the header.
 */
export async function discover(
  timeoutMs = DISCOVER_TIMEOUT_MS,
): Promise<Discovered[]> {
  const p = plugin();
  if (!p?.discover) return [];
  try {
    const r = await p.discover({ timeoutMs });
    const raw = Array.isArray(r?.services) ? r.services : [];
    const out: Discovered[] = [];
    for (const item of raw) {
      const s = parseService(item);
      if (s) out.push(s);
    }
    return out;
  } catch {
    return [];
  }
}

/**
 * Turn what was found into addresses worth trying, given the pin this phone
 * already trusts.
 *
 * Three rules, in order of how much each one saves:
 *
 *   - A service advertising a DIFFERENT pin is dropped. It is somebody else's
 *     daemon (or an impostor), and dialing it costs a TLS handshake that can
 *     only fail. A service advertising NO pin is kept: an older daemon is not
 *     an impostor and the handshake judges it either way.
 *   - The ADVERTISED port is used, not the stored one. The daemon publishes the
 *     port it actually bound, which is the whole point of asking it.
 *   - Anything already in `known` is dropped, because the caller has just tried
 *     it. Discovery exists to add addresses nobody knew, not to repeat a
 *     timeout.
 *
 * Order is preserved, so a picker does not reshuffle between browses.
 */
export function candidates(
  found: readonly Discovered[],
  pin: string,
  known: readonly string[] = [],
): Candidate[] {
  const want = pin.trim();
  const seen = new Set(
    known.map((h) => h.trim().toLowerCase()).filter((h) => h !== ""),
  );
  const out: Candidate[] = [];
  for (const s of found) {
    if (s.pin !== "" && want !== "" && s.pin !== want) continue;
    const key = s.host.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ host: s.host, port: s.port, name: s.name });
  }
  return out;
}
