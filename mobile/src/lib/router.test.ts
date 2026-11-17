import { describe, it, expect, beforeEach } from "vitest";
import {
  CONNECT_STATES,
  SEGS,
  SHEETS,
  formatLocation,
  formatRoute,
  parseLocation,
  parseRoute,
  router,
  type Route,
} from "./router.svelte";

const ctx = { debug: true };

const GRAMMAR: Route[] = [
  ...CONNECT_STATES.map((state): Route => ({ name: "connect", state })),
  { name: "tab", tab: "inbox" },
  { name: "tab", tab: "inbox", filter: "needs" },
  { name: "tab", tab: "inbox", filter: "working" },
  { name: "tab", tab: "inbox", filter: "review" },
  { name: "tab", tab: "inbox", filter: "done" },
  ...SEGS.map((seg): Route => ({ name: "session", id: "s1", seg })),
  { name: "session", id: "lola-a~web", seg: "terminal", pane: "lola-a-shell-1" },
  { name: "tab", tab: "projects" },
  { name: "tab", tab: "projects", project: "nori-app" },
  { name: "tab", tab: "projects", project: "nori", pick: "prs" },
  { name: "tab", tab: "projects", project: "nori", pick: "tickets" },
  { name: "tab", tab: "settings" },
  { name: "dev", page: "bridge-guard" },
];

beforeEach(() => {
  router.stack = [{ name: "tab", tab: "inbox" }];
  router.sheet = "";
});

describe("path grammar", () => {
  it.each(GRAMMAR)("round-trips %j", (r) => {
    expect(parseRoute(formatRoute(r), ctx)).toEqual(r);
  });

  it("round-trips every sheet", () => {
    for (const sheet of SHEETS) {
      const loc = { route: { name: "tab", tab: "inbox", filter: "needs" } as Route, sheet };
      expect(parseLocation(formatLocation(loc), ctx)).toEqual(loc);
      const bare = { route: { name: "tab", tab: "settings" } as Route, sheet };
      expect(parseLocation(formatLocation(bare), ctx)).toEqual(bare);
    }
  });

  it.each([
    "",
    "inbox",
    "/",
    "/nope",
    "/inbox/x",
    "/inbox?filter=bogus",
    "/inbox?sheet=bogus",
    "/session",
    "/session/s1",
    "/session/s1/nope",
    "/session/s1/terminal/a/b",
    "/session//terminal",
    "/projects/a/nope",
    "/projects/a/prs/x",
    "/connect/nope",
    "/connect/scan",
    "/settings/x",
    "/dev/other",
    "/session/%E0%A4%A/terminal",
  ])("rejects %j", (p) => {
    expect(parseRoute(p, ctx)).toBeNull();
  });

  it("carries a sheet's tab, and only with a sheet", () => {
    const loc = { route: { name: "session", id: "s5", seg: "review" } as Route, sheet: "reviewPackage" as const, tab: "visuals" };
    expect(formatLocation(loc)).toBe("/session/s5/review?sheet=reviewPackage&tab=visuals");
    expect(parseLocation(formatLocation(loc), ctx)).toEqual(loc);
    expect(parseLocation("/session/s5/review?tab=visuals", ctx)).toBeNull();
    expect(parseLocation("/session/s5/review?sheet=reviewPackage&tab=Visuals!", ctx)).toBeNull();
  });

  it("opens a sheet on a tab and forgets the tab on close", () => {
    router.open("reviewPackage", "visuals");
    expect(router.sheetTab).toBe("visuals");
    router.close();
    expect(router.sheetTab).toBe("");
  });

  it("sends /connect/* to the inbox while paired", () => {
    expect(parseRoute("/connect", { paired: true, debug: true })).toEqual({ name: "tab", tab: "inbox" });
    expect(parseRoute("/connect/expired", { paired: true, debug: true })).toEqual({ name: "tab", tab: "inbox" });
    expect(parseRoute("/connect/expired", ctx)).toEqual({ name: "connect", state: "expired" });
  });

  it("refuses /dev/* in a release build", () => {
    expect(parseRoute("/dev/bridge-guard", { debug: false })).toBeNull();
  });
});

describe("router", () => {
  it("pushes a session over a tab and back returns to it", () => {
    router.push({ name: "session", id: "s1", seg: "conversation" });
    expect(router.stack).toHaveLength(2);
    router.back();
    expect(router.top).toEqual({ name: "tab", tab: "inbox" });
  });

  it("replaces the top route", () => {
    router.push({ name: "session", id: "s1", seg: "conversation" });
    router.replace({ name: "session", id: "s1", seg: "changes" });
    expect(router.stack).toHaveLength(2);
    expect(router.top).toMatchObject({ seg: "changes" });
  });

  it("climbs a tab: pick, then project, then list", () => {
    router.replace({ name: "tab", tab: "projects", project: "p", pick: "prs" });
    router.back();
    expect(router.top).toEqual({ name: "tab", tab: "projects", project: "p", pick: undefined });
    router.back();
    expect(router.top).toEqual({ name: "tab", tab: "projects" });
    router.back();
    expect(router.top).toEqual({ name: "tab", tab: "projects" });
  });

  it("never pops to connect", () => {
    router.stack = [
      { name: "connect", state: "not-paired" },
      { name: "tab", tab: "inbox" },
    ];
    router.back();
    expect(router.top).toEqual({ name: "tab", tab: "inbox" });
    expect(router.stack).toHaveLength(2);
  });

  it("closes the sheet on any navigation", () => {
    router.open("filter");
    router.push({ name: "session", id: "s1", seg: "review" });
    expect(router.sheet).toBe("");
    router.open("merge");
    router.close();
    expect(router.sheet).toBe("");
  });
});
