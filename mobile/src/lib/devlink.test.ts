import { describe, it, expect, afterEach, vi } from "vitest";
import { devLinkSource, devLinkTarget, devLinkToPayload, installDevLink } from "./devlink";
import { DEFAULT_REMOTE_PORT } from "./endpoint";

const PIN = "C4td4uyeJMSyxfoAsB3i98Kd6JhkpOTf3Oxipiq+sxI=";
const PIN_URL = "C4td4uyeJMSyxfoAsB3i98Kd6JhkpOTf3Oxipiq-sxI";

function install(p: Record<string, unknown> | undefined): void {
  (globalThis as Record<string, unknown>).Capacitor = p
    ? { Plugins: { LolaTransport: p } }
    : undefined;
}

afterEach(() => install(undefined));

describe("devLinkToPayload", () => {
  it("maps the plugin's fields onto a payload", () => {
    expect(
      devLinkToPayload({
        source: "dev-url",
        host: "127.0.0.1",
        port: 7717,
        spkiPin: PIN,
        insecureKey: "0123456789abcdef",
      }),
    ).toEqual({ addrs: ["127.0.0.1"], port: 7717, pin: PIN, key: "0123456789abcdef" });
  });

  it("applies the default port when the URL omitted one", () => {
    expect(devLinkToPayload({ host: "127.0.0.1" })?.port).toBe(DEFAULT_REMOTE_PORT);
    expect(devLinkToPayload({ host: "127.0.0.1", port: 0 })?.port).toBe(DEFAULT_REMOTE_PORT);
  });

  it("normalises a base64url pin to the spelling the transport compares", () => {
    expect(devLinkToPayload({ host: "127.0.0.1", spkiPin: PIN_URL })?.pin).toBe(PIN);
  });

  it("refuses an event with no host, which is the only structural requirement", () => {
    expect(devLinkToPayload({ host: "" })).toBeNull();
    expect(devLinkToPayload({ host: "   " })).toBeNull();
    expect(devLinkToPayload({})).toBeNull();
    expect(devLinkToPayload(null)).toBeNull();
  });

  it("passes a short key through, so the FORM says what is wrong with it", () => {
    // Refusing here would trade a labelled field error for a link that
    // silently does nothing, which is the harder of the two to debug from a
    // phone.
    const p = devLinkToPayload({ host: "127.0.0.1", insecureKey: "short" });
    expect(p?.key).toBe("short");
  });

  it("leaves a missing pin empty rather than inventing one", () => {
    expect(devLinkToPayload({ host: "127.0.0.1" })?.pin).toBe("");
  });
});

describe("installDevLink", () => {
  it("does nothing at all when there is no plugin", () => {
    install(undefined);
    const seen = vi.fn();
    const off = installDevLink(seen);
    off();
    expect(seen).not.toHaveBeenCalled();
  });

  it("delivers a payload for a well-formed event", async () => {
    let fire: ((e: unknown) => void) | undefined;
    install({
      addListener: async (_: string, cb: (e: unknown) => void) => {
        fire = cb;
        return { remove: () => {} };
      },
    });
    const seen = vi.fn();
    installDevLink(seen);
    await Promise.resolve();
    await Promise.resolve();
    fire?.({ source: "dev-url", host: "10.0.0.4", port: 7717, spkiPin: PIN, insecureKey: "k".repeat(16) });
    expect(seen).toHaveBeenCalledTimes(1);
    expect(seen.mock.calls[0][0]).toMatchObject({ addrs: ["10.0.0.4"], port: 7717 });
  });

  it("drops an event with nothing connectable in it", async () => {
    let fire: ((e: unknown) => void) | undefined;
    install({
      addListener: async (_: string, cb: (e: unknown) => void) => {
        fire = cb;
        return { remove: () => {} };
      },
    });
    const seen = vi.fn();
    installDevLink(seen);
    await Promise.resolve();
    await Promise.resolve();
    fire?.({ source: "dev-url", host: "" });
    expect(seen).not.toHaveBeenCalled();
  });

  it("stops delivering after teardown", async () => {
    let fire: ((e: unknown) => void) | undefined;
    const remove = vi.fn();
    install({
      addListener: async (_: string, cb: (e: unknown) => void) => {
        fire = cb;
        return { remove };
      },
    });
    const seen = vi.fn();
    const off = installDevLink(seen);
    await Promise.resolve();
    await Promise.resolve();
    off();
    fire?.({ source: "dev-url", host: "10.0.0.4" });
    expect(seen).not.toHaveBeenCalled();
    expect(remove).toHaveBeenCalled();
  });

  it("survives a plugin whose addListener rejects", async () => {
    install({
      addListener: async () => {
        throw new Error("no such event in a release build");
      },
    });
    const off = installDevLink(vi.fn());
    await Promise.resolve();
    await Promise.resolve();
    expect(() => off()).not.toThrow();
  });
});

describe("devLinkSource", () => {
  // The one function that decides whether the app may connect unattended. Every
  // branch is pinned, because "unrecognised means probably fine" is exactly the
  // reading that would turn the OS URL router — which any app on the device can
  // drive — into an auto-connect.
  it("lets only the launch route dial", () => {
    expect(devLinkSource({ source: "dev-launch", host: "127.0.0.1" })).toBe("launch");
  });

  it("holds a routed URL at the form", () => {
    expect(devLinkSource({ source: "dev-url", host: "127.0.0.1" })).toBe("link");
  });

  it("fails closed on anything it does not recognise", () => {
    // A plugin older than this app, a field that did not survive the bridge, a
    // value somebody invented. All of them are the door that waits for a human.
    for (const source of [undefined, "", "dev-launch ", "DEV-LAUNCH", "launch", "scan"]) {
      expect(devLinkSource({ source, host: "127.0.0.1" } as never)).toBe("link");
    }
    expect(devLinkSource(null)).toBe("link");
    expect(devLinkSource(undefined)).toBe("link");
  });
});

describe("devLinkTarget", () => {
  // The terminal is the screen the whole app is a bet on and it was the one
  // screen no reviewer could produce a screenshot of: it is reached only by
  // tapping a session row, `simctl` has no gesture API, and the Simulator's
  // device window is absent from the accessibility tree, so a synthetic click
  // does not land either. A pane in the launch link is what makes it
  // screenshottable by a script.

  it("reads a pane and defaults the session to it", () => {
    // The daemon's own paneTarget uses the tmux session name, which IS the
    // session id for an agent's own pane — so one value is usually both.
    expect(devLinkTarget({ pane: "lola-fe-42" })).toEqual({
      pane: "lola-fe-42",
      session: "lola-fe-42",
    });
  });

  it("keeps an explicit session, for an aux pane", () => {
    expect(devLinkTarget({ pane: "lola-fe-42-shell-1", session: "lola-fe-42" })).toEqual({
      pane: "lola-fe-42-shell-1",
      session: "lola-fe-42",
    });
  });

  it("is null when no pane was named, which lands on the list", () => {
    expect(devLinkTarget({ host: "127.0.0.1" })).toBeNull();
    expect(devLinkTarget({ pane: "   " })).toBeNull();
    expect(devLinkTarget(null)).toBeNull();
  });

  it("reaches the listener beside the payload and the source", async () => {
    const listeners: Array<(e: unknown) => void> = [];
    install({
      addListener: (_: string, cb: (e: unknown) => void) => {
        listeners.push(cb);
        return Promise.resolve({ remove: () => {} });
      },
    });
    const seen: Array<[string, unknown]> = [];
    const off = installDevLink((_p, source, target) => seen.push([source, target]));
    await Promise.resolve();
    await Promise.resolve();
    listeners[0]?.({
      source: "dev-launch",
      host: "127.0.0.1",
      port: 7717,
      spkiPin: PIN,
      insecureKey: "0123456789abcdef",
      pane: "lola-fe-42",
    });
    expect(seen).toEqual([["launch", { pane: "lola-fe-42", session: "lola-fe-42" }]]);
    off();
  });
});
