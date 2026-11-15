import { describe, it, expect, vi, beforeEach } from "vitest";

const listeners = new Map<string, (e: unknown) => void>();
vi.mock("@mobile/native", () => ({
  transport: {
    addListener: async (name: string, cb: (e: unknown) => void) => {
      listeners.set(name, cb);
      return { remove: async () => void listeners.delete(name) };
    },
  },
}));

import { applyDevLink, devLinkOf, installDevLink } from "./devlink";
import { CONNECT_STATES, SEGS, SHEETS, formatRoute, router, type Route } from "./router.svelte";

beforeEach(() => {
  router.stack = [{ name: "tab", tab: "inbox" }];
  router.sheet = "";
  listeners.clear();
});

const ROUTES: Route[] = [
  ...CONNECT_STATES.map((state): Route => ({ name: "connect", state })),
  { name: "tab", tab: "inbox", filter: "needs" },
  ...SEGS.map((seg): Route => ({ name: "session", id: "s1", seg, pane: seg === "terminal" ? "p1" : undefined })),
  { name: "tab", tab: "projects", project: "nori", pick: "tickets" },
  { name: "tab", tab: "activity" },
  { name: "tab", tab: "settings" },
  { name: "dev", page: "bridge-guard" },
];

describe("dev links", () => {
  it("parses and applies every route", () => {
    for (const route of ROUTES) {
      const link = devLinkOf({ source: "dev-url", route: formatRoute(route) }, false);
      expect(link.route).toEqual(route);
      router.stack = [{ name: "tab", tab: "inbox" }];
      applyDevLink(router, link);
      expect(router.top).toEqual(route);
    }
  });

  it("parses and applies every sheet, from the field and from the path", () => {
    for (const sheet of SHEETS) {
      const viaField = devLinkOf({ source: "dev-url", route: "/inbox", sheet }, false);
      const viaPath = devLinkOf({ source: "dev-url", route: `/inbox?sheet=${sheet}` }, false);
      for (const link of [viaField, viaPath]) {
        expect(link.sheet).toBe(sheet);
        router.sheet = "";
        applyDevLink(router, link);
        expect(router.sheet).toBe(sheet);
      }
    }
  });

  it("pushes a session over a tab", () => {
    applyDevLink(router, devLinkOf({ source: "dev-url", route: "/session/s1/conversation" }, false));
    expect(router.stack).toHaveLength(2);
  });

  it("ignores what is outside the grammar and keeps the screen", () => {
    const link = devLinkOf({ source: "dev-url", route: "/nope", sheet: "bogus" }, false);
    expect(link).toEqual({ source: "dev-url", route: null, sheet: "" });
    applyDevLink(router, link);
    expect(router.top).toEqual({ name: "tab", tab: "inbox" });
    expect(router.sheet).toBe("");
  });

  it("sends /connect/* to the inbox once paired", () => {
    expect(devLinkOf({ source: "dev-url", route: "/connect/expired" }, true).route).toEqual({
      name: "tab",
      tab: "inbox",
    });
  });

  it("never carries a pairRef for dev-url, only for dev-launch", () => {
    expect(devLinkOf({ source: "dev-url", route: "/inbox", pairRef: "r1" }, false).pairRef).toBeUndefined();
    expect(devLinkOf({ source: "dev-launch", route: "/inbox", pairRef: "r1" }, false).pairRef).toBe("r1");
  });

  it("delivers plugin events until torn down", async () => {
    const on = vi.fn();
    const off = installDevLink(on, () => false);
    await vi.waitFor(() => expect(listeners.has("devLink")).toBe(true));
    listeners.get("devLink")!({ source: "dev-url", route: "/activity" });
    expect(on).toHaveBeenCalledWith(expect.objectContaining({ route: { name: "tab", tab: "activity" } }));
    off();
    listeners.get("devLink")?.({ source: "dev-url", route: "/settings" });
    expect(on).toHaveBeenCalledOnce();
  });
});
