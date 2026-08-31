// Where the bearer key lives, and — more importantly — where it does not.
//
// THE RULE: the key is never in this repository, never in a log line, never in a
// URL, and never in `localStorage`. The first three are obvious; the fourth is
// the one worth stating, because `localStorage` is the reflex and it is the
// wrong tool here. It is plain, unencrypted, per-origin storage inside the app
// container: readable by any code running in the WebView, backed up with the
// app, and not protected by the device passcode. `theme-runtime.svelte.ts` uses
// it for the last painted theme, which is exactly the class of thing it is for.
// A shared secret is not.
//
// So the key goes to the native side, into the Keychain, through the same plugin
// that owns the socket. That placement is not incidental either: the plugin is
// the only component that needs the plaintext, so the key can be written once
// and then referenced by NAME, and the WebView never has to hold it again.
//
// THE PLUGIN CONTRACT, which is the one thing here another agent owns. This
// module reaches `LolaTransport` through the Capacitor global rather than
// importing `lola-transport`, for two reasons: the plugin's `dist/` does not
// exist until it is built, so a hard import breaks `vite build` for anyone who
// has not built it; and a browser `npm run dev` session has no plugin at all and
// must still be able to run the UI. The three methods it looks for are:
//
//   secretSet({ key: string, value: string }) -> {}
//   secretHas({ key: string })                -> { has: boolean }
//   secretDelete({ key: string })             -> {}
//
// where `key` is an endpoint id (`host:port`), so two daemons never share one
// secret. If those names do not match what the plugin ships, this file is the
// only place to change.
//
// -------------------------------------------------------------------------
// THERE IS NO `secretGet`, AND THE MISSING ONE IS A FIX RATHER THAN A GAP.
//
// The obvious shape is a read: store the key, read it back next launch, hand it
// to `connect`. It shipped that way and it leaked. Capacitor's bridge logs
// every resolved payload — `CAPLog.print("TO JS", result.jsonPayload())` in
// CapacitorBridge.swift — so `secretGet` resolving `{ value: <key> }` printed
// the bearer key in cleartext to the app's console on every launch of every
// Debug build. That was verified on a simulator against the daemon's own
// `~/.lola/remote.key`, and Debug is the only configuration this project builds
// today. A second, quieter cost came with it: the plaintext then sat in a JS
// local, where an attached Safari Web Inspector — which this app ships with
// enabled — could read it.
//
// The read therefore moved to the side that needs the plaintext. `connect`
// takes a `keyRef` (an endpoint id, an address rather than a secret) and the
// plugin reads the Keychain in Swift, on the same side that puts the key on the
// wire. This module keeps the WRITE, the DELETE, and one boolean.
//
// The header at the top of this file promised exactly that — "the key can be
// written once and then referenced by NAME, and the WebView never has to hold
// it again" — and now it is true.
// -------------------------------------------------------------------------
//
// -------------------------------------------------------------------------
// THE PROBE READS `PluginHeaders`, NOT `Plugins`, AND THAT IS LOAD-BEARING.
//
// `Capacitor.Plugins.LolaTransport` is a JavaScript `Proxy` created by
// `registerPlugin`, and its `get` handler MANUFACTURES A FUNCTION FOR EVERY
// PROPERTY NAME (see `createPluginMethodWrapper` in @capacitor/core). So
// `typeof p.secretGet === "function"` is true for a method no build has ever
// implemented, true in a desktop browser, and true against a plugin binary
// older than this bundle. It is not a capability check; it is a check that
// `registerPlugin` has run, which is a race against the dynamic import in
// `main.ts` and nothing more.
//
// That mattered exactly as much as it sounds. The probe answered TRUE on a
// device with no Keychain code in the plugin at all: `storeKey` called
// `secretSet`, the bridge rejected it as not implemented, the catch below
// dropped the key into the volatile map — and `isPersistent()` had already told
// the connect screen the key would survive a relaunch. It did not. The phone
// came back from standby with an empty credential and asked to be paired again,
// which is also what a REVOKED device looks like.
//
// `Capacitor.PluginHeaders` is the list the NATIVE side injects, naming each
// plugin and the methods it actually implements. It is absent in a browser,
// present and honest on a device, and it shrinks when the plugin binary is
// older than the JavaScript. It is the only thing here that can answer the
// question this module is asking.
// -------------------------------------------------------------------------
//
// WITHOUT the plugin the key is held in memory for the life of the app run and
// nowhere else. That is deliberately worse ergonomically — the key is retyped on
// every launch — and never worse for safety. A silent downgrade to localStorage
// would be the opposite trade, so `isPersistent()` exists to let the UI say
// which one is happening rather than letting the user assume.

/** The subset of the native plugin this module needs. */
interface SecretCapablePlugin {
  secretSet?(o: { key: string; value: string }): Promise<unknown>;
  secretHas?(o: { key: string }): Promise<{ has?: boolean }>;
  secretDelete?(o: { key: string }): Promise<unknown>;
}

/** One entry of `Capacitor.PluginHeaders`, as much of it as this module reads. */
interface PluginHeader {
  name?: string;
  methods?: readonly { name?: string }[];
}

interface CapacitorGlobal {
  Plugins?: { LolaTransport?: SecretCapablePlugin };
  /** Injected by the native bridge. Absent in a browser. See the header. */
  PluginHeaders?: readonly PluginHeader[];
}

/** The three names that have to be present for a key to survive a relaunch. */
const REQUIRED = ["secretSet", "secretHas", "secretDelete"] as const;

/**
 * Whether the NATIVE plugin implements the secret store.
 *
 * Asked of `PluginHeaders` and never of the `Plugins` proxy, which answers yes
 * to everything — see the header. Fails closed: no headers, no entry for this
 * plugin, or a method missing from its list all mean "hold the key in memory
 * and say so".
 */
function nativeSecretStore(): boolean {
  const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
  const header = cap?.PluginHeaders?.find((h) => h?.name === "LolaTransport");
  if (!header?.methods) return false;
  const names = new Set(header.methods.map((m) => m?.name));
  return REQUIRED.every((n) => names.has(n));
}

/**
 * The plugin object to call, once `nativeSecretStore` has said there is
 * something behind it.
 */
function plugin(): SecretCapablePlugin | undefined {
  if (!nativeSecretStore()) return undefined;
  const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
  const p = cap?.Plugins?.LolaTransport;
  if (!p) return undefined;
  return typeof p.secretHas === "function" && typeof p.secretSet === "function" ? p : undefined;
}

/**
 * Whether a stored key will survive the app being closed.
 *
 * The UI shows this, because "you will have to type this again next time" is a
 * fact a user is entitled to before they decide how to store a secret — and
 * because the sentence is only worth printing if it is true in both directions.
 */
export function isPersistent(): boolean {
  return plugin() !== undefined;
}

/** The in-memory fallback. Cleared when the app run ends, which is the point. */
const volatile = new Map<string, string>();

/**
 * Where a key ended up, which is NOT the same question as `isPersistent()`.
 *
 * `isPersistent()` answers from the plugin's method list: it says the app is
 * capable of durable storage. It does not, and cannot, say that this particular
 * write succeeded — a locked or refusing Keychain drops the key into the
 * volatile map below and the connect screen went on promising the user it would
 * survive a relaunch. That is the same class of lie the probe at the top of
 * this file was rewritten to remove, one layer further in, so the WRITE reports
 * its outcome too and the caption is driven by what happened rather than by
 * what was possible.
 */
export type KeyStorage = "keychain" | "memory" | "none";

/**
 * Store the key for one endpoint.
 *
 * Storing an empty value DELETES rather than writing an empty secret: an empty
 * Keychain entry reads back as "there is a key, and it is wrong", which is the
 * most confusing possible state at the connect screen.
 */
export async function storeKey(endpointId: string, key: string): Promise<KeyStorage> {
  if (key === "") {
    await forgetKey(endpointId);
    return "none";
  }
  const p = plugin();
  if (p?.secretSet) {
    try {
      await p.secretSet({ key: endpointId, value: key });
      return "keychain";
    } catch {
      // Fall through: a Keychain that refuses must not lose the running session.
    }
  }
  volatile.set(endpointId, key);
  return "memory";
}

/**
 * Where a key for this endpoint actually is, if anywhere.
 *
 * "keychain" means the plugin holds it and it survives a relaunch; the caller
 * must pass `keyRef` to `connect` rather than a key, because the plaintext
 * deliberately has no way back across the bridge (see the header). "memory"
 * means the Keychain refused and it is in this page's own map, good until the
 * process dies. Never throws: an unreadable keychain is, for the purpose of
 * choosing between a reconnect and a form, one with nothing in it.
 */
export async function findKey(
  endpointId: string,
): Promise<{ where: KeyStorage; key: string }> {
  const p = plugin();
  if (p?.secretHas) {
    try {
      const r = await p.secretHas({ key: endpointId });
      if (r?.has === true) return { where: "keychain", key: "" };
    } catch {
      /* fall through to the volatile copy */
    }
  }
  const held = volatile.get(endpointId);
  return held ? { where: "memory", key: held } : { where: "none", key: "" };
}

/**
 * Remove a stored key. Never throws.
 *
 * Called from "Forget this Mac" on the disconnect confirmation, and whenever
 * the daemon refuses the stored key outright. For a long time it was called
 * from nowhere at all while its doc comment named a control that did not exist,
 * which meant a key, once stored, could not be removed by any action available
 * to the user: disconnecting set a process-local flag and left the credential
 * in place, so the one durable thing on the device was the credential and the
 * one volatile thing was the decision to stop using it.
 */
export async function forgetKey(endpointId: string): Promise<void> {
  volatile.delete(endpointId);
  const p = plugin();
  if (p?.secretDelete) {
    try {
      await p.secretDelete({ key: endpointId });
    } catch {
      /* nothing useful to report: the volatile copy is already gone */
    }
  }
}

/**
 * A key rendered for the screen: its length only, never any of its characters.
 *
 * Even a prefix is too much. The connect screen is the one place a user might
 * hand the phone to somebody, and a shoulder-visible prefix of a shared bearer
 * secret is a real leak for a key that is typed once and never rotated.
 */
export function maskKey(key: string): string {
  if (key === "") return "";
  return "•".repeat(Math.min(key.length, 24));
}

/**
 * Everything the connect screen needs to know about a NON-secret endpoint,
 * which is the only part that is safe to persist in the WebView.
 *
 * The host, the port and the SPKI pin are all public: the pin is printed in the
 * daemon's own startup log and is a hash of a public key. Keeping them in
 * localStorage means a returning user types nothing but the key, and keeping
 * them OUT of the Keychain keeps the secret store to exactly one item.
 */
export interface StoredEndpoint {
  host: string;
  port: number;
  spkiPin: string;
}

const ENDPOINT_KEY = "lola.mobile.endpoint";

export function loadEndpoint(): StoredEndpoint | null {
  try {
    const raw = globalThis.localStorage?.getItem(ENDPOINT_KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as Partial<StoredEndpoint>;
    if (typeof v.host !== "string" || typeof v.spkiPin !== "string") return null;
    return { host: v.host, port: typeof v.port === "number" ? v.port : 0, spkiPin: v.spkiPin };
  } catch {
    return null; // storage disabled, or something else wrote this key
  }
}

export function saveEndpoint(e: StoredEndpoint): void {
  try {
    globalThis.localStorage?.setItem(ENDPOINT_KEY, JSON.stringify(e));
  } catch {
    /* a remembered address is not worth failing a connection over */
  }
}

export function clearEndpoint(): void {
  try {
    globalThis.localStorage?.removeItem(ENDPOINT_KEY);
  } catch {
    /* nothing to do */
  }
}
