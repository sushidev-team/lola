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

  const NOWHERE = {
    pane: "",
    session: "",
    triage: "",
    query: "",
    sheet: "",
    tab: "",
    project: "",
    pick: "",
  };

  it("reads a pane and defaults the session to it", () => {
    // The daemon's own paneTarget uses the tmux session name, which IS the
    // session id for an agent's own pane — so one value is usually both.
    expect(devLinkTarget({ pane: "lola-fe-42" })).toEqual({
      ...NOWHERE,
      pane: "lola-fe-42",
      session: "lola-fe-42",
    });
  });

  it("keeps an explicit session, for an aux pane", () => {
    expect(devLinkTarget({ pane: "lola-fe-42-shell-1", session: "lola-fe-42" })).toEqual({
      ...NOWHERE,
      pane: "lola-fe-42-shell-1",
      session: "lola-fe-42",
    });
  });

  it("is null when the link names no destination at all", () => {
    expect(devLinkTarget({ host: "127.0.0.1" })).toBeNull();
    expect(devLinkTarget({ pane: "   " })).toBeNull();
    expect(devLinkTarget(null)).toBeNull();
  });

  // The filter overlay, the connection settings and the terminal's view
  // settings are reachable only by a tap, and a tap is the one thing the
  // Simulator cannot be asked to perform — so those three screens could be
  // tested but never photographed. These fields are what put them at the end of
  // a link.

  it("takes a filter with no pane, which lands on the list", () => {
    expect(devLinkTarget({ triage: "Needs You" })).toEqual({
      ...NOWHERE,
      triage: "Needs You",
    });
  });

  it("matches a triage bucket case-insensitively against the real vocabulary", () => {
    // A bucket title is display text with a capital and a space; a link is
    // typed on a command line. The list itself stays in theme.ts — spelling one
    // into the plugin would be a third copy of it.
    expect(devLinkTarget({ triage: "needs you" })?.triage).toBe("Needs You");
    expect(devLinkTarget({ triage: "in review" })?.triage).toBe("In Review");
  });

  it("drops a triage bucket that is not one, rather than showing an empty list", () => {
    // Handing an unmatched value to `triaged` would match no session at all, so
    // the link would silently show nothing. Failing closed shows everything.
    expect(devLinkTarget({ triage: "wharrgarbl" })).toBeNull();
    expect(devLinkTarget({ pane: "lola-fe-42", triage: "wharrgarbl" })?.triage).toBe("");
  });

  it("carries a free-text query", () => {
    expect(devLinkTarget({ query: "nori" })).toEqual({ ...NOWHERE, query: "nori" });
  });

  it("takes a sheet from the vocabulary and refuses anything else", () => {
    expect(devLinkTarget({ sheet: "filter" })?.sheet).toBe("filter");
    expect(devLinkTarget({ sheet: "Menu" })?.sheet).toBe("menu"); // case-folded
    expect(devLinkTarget({ pane: "lola-fe-42", sheet: "menu" })?.sheet).toBe("menu");
    // Fails closed: a sheet this build has never heard of opens nothing.
    expect(devLinkTarget({ pane: "lola-fe-42", sheet: "wharrgarbl" })?.sheet).toBe("");
    expect(devLinkTarget({ sheet: "wharrgarbl" })).toBeNull();
  });

  it("takes a tab from the vocabulary and refuses anything else", () => {
    // Activity, Projects and Settings are reachable only by tapping the bottom
    // bar, so without this field they are three screens a script cannot
    // photograph — the same hole `pane` was added to close for the terminal.
    expect(devLinkTarget({ tab: "activity" })?.tab).toBe("activity");
    // Lowercased first, because a link is typed by hand and "Projects" is what
    // somebody writes when they are naming the screen rather than the token.
    expect(devLinkTarget({ tab: "Projects" })?.tab).toBe("projects");
    // Fails closed: a tab this build has never heard of leaves the current one
    // alone rather than landing somewhere unexpected.
    expect(devLinkTarget({ pane: "lola-fe-42", tab: "wharrgarbl" })?.tab).toBe("");
    expect(devLinkTarget({ tab: "wharrgarbl" })).toBeNull();
  });

  it("takes a project name as written, because there is no list to match it against", () => {
    // Every other narrowed field here has a vocabulary; this one does not. The
    // projects are whatever config.toml on that Mac says, the push carrying
    // them has not landed when a launch link is applied, and a name that turns
    // out not to exist is a real state the detail screen draws by name. So it
    // is trimmed and carried, and the screen is what reports the miss.
    expect(devLinkTarget({ project: "nori-app" })).toEqual({
      ...NOWHERE,
      project: "nori-app",
    });
    expect(devLinkTarget({ project: "  nori-app  " })?.project).toBe("nori-app");
    expect(devLinkTarget({ project: "   " })).toBeNull();
  });

  it("takes a picker from the vocabulary and refuses anything else", () => {
    // The pull-request and issue lists sit two taps deep — a project row, then
    // an action — so without this field they are two more screens a script can
    // test and never photograph.
    expect(devLinkTarget({ project: "nori-app", pick: "prs" })?.pick).toBe("prs");
    // Lowercased first: the vocabulary is lowercase and a link is typed by hand.
    expect(devLinkTarget({ project: "nori-app", pick: "Tickets" })?.pick).toBe("tickets");
    // Fails closed: a picker this build has never heard of opens nothing, and
    // the detail underneath it still shows.
    expect(devLinkTarget({ project: "nori-app", pick: "wharrgarbl" })?.pick).toBe("");
    expect(devLinkTarget({ pick: "wharrgarbl" })).toBeNull();
  });

  it("carries a picker with no project, and lets the shell decide", () => {
    // Deliberately not refused here. A pick is a valid picker name; what it
    // lacks is something to open over, and App.svelte routes the Projects tab
    // by depth with the project outermost — so this lands on the project list
    // rather than on a picker that could only ask the daemon about "".
    expect(devLinkTarget({ pick: "prs" })).toEqual({ ...NOWHERE, pick: "prs" });
  });

  it("leaves the session empty when there is no pane to own it", () => {
    // A session id with no pane names nothing that can be attached to, and
    // carrying it would make `applyTarget` open a terminal on "".
    expect(devLinkTarget({ session: "lola-fe-42", triage: "In Review" })?.session).toBe("");
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
    expect(seen).toEqual([
      ["launch", { ...NOWHERE, pane: "lola-fe-42", session: "lola-fe-42" }],
    ]);
    off();
  });
});
