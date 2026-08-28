import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  clearEndpoint,
  forgetKey,
  isPersistent,
  loadEndpoint,
  loadKey,
  maskKey,
  saveEndpoint,
  storeKey,
} from "./secretstore";

const ENDPOINT_KEY = "lola.mobile.endpoint";

function installPlugin() {
  const store = new Map<string, string>();
  const p = {
    secretSet: vi.fn(async ({ key, value }: { key: string; value: string }) => {
      store.set(key, value);
    }),
    secretGet: vi.fn(async ({ key }: { key: string }) => ({ value: store.get(key) ?? null })),
    secretDelete: vi.fn(async ({ key }: { key: string }) => {
      store.delete(key);
    }),
  };
  (globalThis as { Capacitor?: unknown }).Capacitor = { Plugins: { LolaTransport: p } };
  return { p, store };
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
    expect(await loadKey("10.0.0.1:7717")).toBe("0123456789abcdef");
  });

  it("says honestly whether a key will survive a relaunch", () => {
    expect(isPersistent()).toBe(false);
    installPlugin();
    expect(isPersistent()).toBe(true);
  });

  it("files each daemon's key separately", async () => {
    installPlugin();
    await storeKey("10.0.0.1:7717", "keyforthefirstmac");
    await storeKey("10.0.0.2:7717", "keyforsecondmacxx");
    expect(await loadKey("10.0.0.1:7717")).toBe("keyforthefirstmac");
    expect(await loadKey("10.0.0.2:7717")).toBe("keyforsecondmacxx");
  });

  it("deletes rather than storing an empty key", async () => {
    // An empty Keychain entry reads back as "there is a key, and it is wrong",
    // which is the most confusing state the connect screen can be in.
    const { p } = installPlugin();
    await storeKey("a:1", "0123456789abcdef");
    await storeKey("a:1", "");
    expect(p.secretDelete).toHaveBeenCalledWith({ key: "a:1" });
    expect(await loadKey("a:1")).toBe("");
  });

  it("keeps the session alive when the Keychain refuses", async () => {
    (globalThis as { Capacitor?: unknown }).Capacitor = {
      Plugins: {
        LolaTransport: {
          secretSet: vi.fn().mockRejectedValue(new Error("locked")),
          secretGet: vi.fn().mockRejectedValue(new Error("locked")),
        },
      },
    };
    await storeKey("a:1", "0123456789abcdef");
    expect(await loadKey("a:1")).toBe("0123456789abcdef");
  });

  it("forgets a key on request", async () => {
    installPlugin();
    await storeKey("a:1", "0123456789abcdef");
    await forgetKey("a:1");
    expect(await loadKey("a:1")).toBe("");
  });

  it("returns empty for an endpoint that was never stored", async () => {
    installPlugin();
    expect(await loadKey("nothing:1")).toBe("");
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
