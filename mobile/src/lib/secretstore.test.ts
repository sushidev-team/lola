import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  clearEndpoint,
  forgetKey,
  isPersistent,
  findKey,
  loadEndpoint,
  maskKey,
  saveEndpoint,
  storeKey,
} from "./secretstore";

const ENDPOINT_KEY = "lola.mobile.endpoint";

/**
 * The NATIVE plugin's method list, as the bridge injects it.
 *
 * Every fixture here has to supply one, because that list — and never the
 * `Plugins` proxy — is what the module asks. See the test below for what goes
 * wrong when the two are confused.
 */
function headers(methods: string[] = ["secretSet", "secretHas", "secretDelete"]) {
  return [{ name: "LolaTransport", methods: methods.map((name) => ({ name })) }];
}

function installPlugin(available = ["secretSet", "secretHas", "secretDelete"]) {
  const store = new Map<string, string>();
  const p = {
    secretSet: vi.fn(async ({ key, value }: { key: string; value: string }) => {
      store.set(key, value);
    }),
    secretHas: vi.fn(async ({ key }: { key: string }) => ({ has: store.has(key) })),
    secretDelete: vi.fn(async ({ key }: { key: string }) => {
      store.delete(key);
    }),
  };
  (globalThis as { Capacitor?: unknown }).Capacitor = {
    Plugins: { LolaTransport: p },
    PluginHeaders: headers(available),
  };
  return { p, store };
}

/**
 * What a DESKTOP BROWSER, or any build whose plugin lacks the Keychain, really
 * looks like: `registerPlugin` has run, so `Capacitor.Plugins.LolaTransport` is
 * a Proxy that manufactures a function for every property name — and no native
 * header exists to back any of them.
 */
function installProxyOnly() {
  const p = new Proxy(
    {},
    { get: () => async () => ({}) },
  ) as Record<string, unknown>;
  (globalThis as { Capacitor?: unknown }).Capacitor = { Plugins: { LolaTransport: p } };
}

beforeEach(() => {
  delete (globalThis as { Capacitor?: unknown }).Capacitor;
  globalThis.localStorage?.clear();
});
afterEach(() => {
  delete (globalThis as { Capacitor?: unknown }).Capacitor;
  vi.restoreAllMocks();
});

describe("the bearer key", () => {
  it("goes to the native plugin, never to localStorage", async () => {
    // THE test that matters. localStorage inside the app container is plain,
    // unencrypted, backed up with the app and readable by anything in the
    // WebView; a shared secret does not belong there under any circumstances.
    const { store } = installPlugin();
    await storeKey("10.0.0.1:7717", "0123456789abcdef");
    expect(store.get("10.0.0.1:7717")).toBe("0123456789abcdef");

    const dump = JSON.stringify({ ...globalThis.localStorage });
    expect(dump).not.toContain("0123456789abcdef");
  });

  it("keeps nothing in localStorage even WITHOUT a plugin", async () => {
    // The fallback is memory. It is worse ergonomically — the key is retyped
    // each launch — and it must never quietly become storage instead.
    await storeKey("10.0.0.1:7717", "0123456789abcdef");
    const dump = JSON.stringify({ ...globalThis.localStorage });
    expect(dump).not.toContain("0123456789abcdef");
    expect(await findKey("10.0.0.1:7717")).toEqual({
      where: "memory",
      key: "0123456789abcdef",
    });
  });

  it("says honestly whether a key will survive a relaunch", () => {
    expect(isPersistent()).toBe(false);
    installPlugin();
    expect(isPersistent()).toBe(true);
  });

  it("is not fooled by Capacitor's method proxy", () => {
    // THE BUG THIS PINS, and it is the reason a phone came back from standby
    // asking to be paired again. `Capacitor.Plugins.X` is a Proxy whose `get`
    // handler returns a function for ANY property, so `typeof p.secretHas ===
    // "function"` is true against a plugin that has never implemented it. The
    // old probe therefore answered "yes, the key survives a relaunch" on a
    // build with no Keychain code at all: the write was rejected as not
    // implemented, the key fell into the volatile map, and the connect screen
    // had already promised otherwise.
    installProxyOnly();
    expect(isPersistent()).toBe(false);
  });

  it("refuses a plugin that implements only some of the three", () => {
    // A JavaScript bundle newer than the native binary it is running against.
    // Partial support is not support: a key that can be written and not deleted
    // makes "forget this Mac" a lie.
    installPlugin(["secretSet", "secretHas"]);
    expect(isPersistent()).toBe(false);
  });

  it("keeps the key in memory when there is no native store", async () => {
    // The fallback has to still WORK, not just be reported. A browser dev
    // session must be able to connect for as long as the page lives.
    installProxyOnly();
    expect(await storeKey("a:1", "0123456789abcdef")).toBe("memory");
    expect(await findKey("a:1")).toEqual({ where: "memory", key: "0123456789abcdef" });
    expect(JSON.stringify({ ...globalThis.localStorage })).not.toContain("0123456789abcdef");
  });

  it("files each daemon's key separately", async () => {
    installPlugin();
    await storeKey("10.0.0.1:7717", "keyforthefirstmac");
    await storeKey("10.0.0.2:7717", "keyforsecondmacxx");
    // The KEY never comes back across the bridge — see the header — so what is
    // asserted is that each endpoint is separately paired and that the plugin
    // was asked about the right account.
    expect(await findKey("10.0.0.1:7717")).toEqual({ where: "keychain", key: "" });
    expect(await findKey("10.0.0.3:7717")).toEqual({ where: "none", key: "" });
  });

  it("deletes rather than storing an empty key", async () => {
    // An empty Keychain entry reads back as "there is a key, and it is wrong",
    // which is the most confusing state the connect screen can be in.
    const { p } = installPlugin();
    await storeKey("a:1", "0123456789abcdef");
    await storeKey("a:1", "");
    expect(p.secretDelete).toHaveBeenCalledWith({ key: "a:1" });
    expect(await findKey("a:1")).toEqual({ where: "none", key: "" });
  });

  it("keeps the session alive when the Keychain refuses", async () => {
    (globalThis as { Capacitor?: unknown }).Capacitor = {
      Plugins: {
        LolaTransport: {
          secretSet: vi.fn().mockRejectedValue(new Error("locked")),
          secretHas: vi.fn().mockRejectedValue(new Error("locked")),
          secretDelete: vi.fn().mockRejectedValue(new Error("locked")),
        },
      },
      PluginHeaders: headers(),
    };
    // The fallback survives, and it REPORTS itself. `isPersistent()` answers
    // from the plugin's method list and would still say "this key will survive a
    // relaunch"; only the write knows that it did not, which is why the connect
    // screen's caption is driven by this value and not by that probe.
    expect(await storeKey("a:1", "0123456789abcdef")).toBe("memory");
    expect(await findKey("a:1")).toEqual({ where: "memory", key: "0123456789abcdef" });
  });

  it("reports a successful Keychain write as durable", async () => {
    installPlugin();
    expect(await storeKey("a:1", "0123456789abcdef")).toBe("keychain");
  });

  it("forgets a key on request", async () => {
    installPlugin();
    await storeKey("a:1", "0123456789abcdef");
    await forgetKey("a:1");
    expect(await findKey("a:1")).toEqual({ where: "none", key: "" });
  });

  it("forgets the in-memory copy too, so a fallback key is not left behind", async () => {
    installProxyOnly();
    await storeKey("a:1", "0123456789abcdef");
    await forgetKey("a:1");
    expect(await findKey("a:1")).toEqual({ where: "none", key: "" });
  });

  it("returns empty for an endpoint that was never stored", async () => {
    installPlugin();
    expect(await findKey("nothing:1")).toEqual({ where: "none", key: "" });
  });

  it("NEVER returns the key across the bridge, even when one is stored", async () => {
    // The leak this shape exists to close: `secretGet` resolved
    // `{ value: <the key> }`, and Capacitor's bridge logs every resolved
    // payload, so the bearer key was printed to the console on every launch of
    // every Debug build. A boolean cannot leak a credential.
    const { p } = installPlugin();
    await storeKey("a:1", "0123456789abcdef");
    const found = await findKey("a:1");
    expect(found.key).toBe("");
    expect(found.where).toBe("keychain");
    expect(p).not.toHaveProperty("secretGet");
  });
});

describe("maskKey", () => {
  it("shows length and nothing else — not even a prefix", () => {
    expect(maskKey("")).toBe("");
    expect(maskKey("abcd")).toBe("••••");
    expect(maskKey("abcd")).not.toContain("a");
  });

  it("stops growing for a long key, so the field cannot be measured by eye", () => {
    expect(maskKey("x".repeat(200))).toHaveLength(24);
  });
});

describe("the remembered endpoint", () => {
  it("keeps host, port and pin, which are all public", () => {
    // The pin is a hash of a public key and the daemon prints it in its own
    // startup log; remembering it means a returning user types only the secret.
    saveEndpoint({ host: "marsmac.local", port: 7717, spkiPin: "A".repeat(43) + "=" });
    expect(loadEndpoint()).toEqual({
      host: "marsmac.local",
      port: 7717,
      spkiPin: "A".repeat(43) + "=",
    });
  });

  it("returns null rather than throwing on junk", () => {
    globalThis.localStorage?.setItem(ENDPOINT_KEY, "{not json");
    expect(loadEndpoint()).toBeNull();
    globalThis.localStorage?.setItem(ENDPOINT_KEY, JSON.stringify({ nope: 1 }));
    expect(loadEndpoint()).toBeNull();
  });

  it("clears", () => {
    saveEndpoint({ host: "a", port: 1, spkiPin: "p" });
    clearEndpoint();
    expect(loadEndpoint()).toBeNull();
  });
});
