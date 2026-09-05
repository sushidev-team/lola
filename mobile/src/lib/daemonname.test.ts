import { beforeEach, describe, expect, it } from "vitest";
import {
  NAME_MAX,
  customNames,
  daemonLabel,
  forgetDaemonName,
  hasCustomName,
  learnDaemonName,
  learnedNames,
  normalizeName,
  renameDaemon,
} from "./daemonname";

// What to call the Mac on the other end.
//
// The address is the least stable thing about it now that a phone can find the
// daemon by browsing: the same machine is one address at home and another at
// the office. These tests are about the FALLBACK CHAIN, because every link in
// it can be absent and each fallback still has to name something true.

const ID = "192.168.10.160:7717";

beforeEach(() => {
  globalThis.localStorage?.clear();
});

describe("the fallback chain", () => {
  it("names the address when nothing else is known", () => {
    expect(daemonLabel(ID, "192.168.10.160")).toBe("192.168.10.160");
  });

  it("says 'the daemon' when even the address is not known yet", () => {
    // The boot path, before a dial has a host to name.
    expect(daemonLabel(ID, "")).toBe("the daemon");
  });

  it("prefers the name the daemon reports over the address", () => {
    learnDaemonName(ID, "marvin");
    expect(daemonLabel(ID, "192.168.10.160")).toBe("marvin");
  });

  it("prefers a person's own name over the daemon's", () => {
    learnDaemonName(ID, "Martins-MacBook-Pro");
    renameDaemon(ID, "work");
    expect(daemonLabel(ID, "192.168.10.160")).toBe("work");
  });

  it("falls back to the daemon's name when the rename is cleared", () => {
    // Clearing is the UNDO, not a way to have no name.
    learnDaemonName(ID, "marvin");
    renameDaemon(ID, "work");
    renameDaemon(ID, "   ");
    expect(hasCustomName(ID)).toBe(false);
    expect(daemonLabel(ID, "192.168.10.160")).toBe("marvin");
  });
});

describe("names are per endpoint", () => {
  it("does not leak a name to another Mac", () => {
    const other = "10.0.0.5:7717";
    renameDaemon(ID, "work");
    learnDaemonName(other, "mini");
    expect(daemonLabel(other, "10.0.0.5")).toBe("mini");
    expect(daemonLabel(ID, "192.168.10.160")).toBe("work");
  });

  it("forgets both halves with the endpoint, leaving others alone", () => {
    const other = "10.0.0.5:7717";
    learnDaemonName(ID, "marvin");
    renameDaemon(ID, "work");
    learnDaemonName(other, "mini");

    forgetDaemonName(ID);
    expect(daemonLabel(ID, "192.168.10.160")).toBe("192.168.10.160");
    expect(daemonLabel(other, "10.0.0.5")).toBe("mini");
  });
});

describe("what the daemon reports", () => {
  it("is ignored when empty, so an older daemon cannot erase a learned name", () => {
    learnDaemonName(ID, "marvin");
    learnDaemonName(ID, "");
    expect(daemonLabel(ID, "192.168.10.160")).toBe("marvin");
  });

  it("is sanitized: it crossed a network and it is rendered", () => {
    learnDaemonName(ID, "  mar\x07vin\nlab  ");
    expect(learnedNames()[ID]).toBe("marvin lab");
  });

  it("is clipped rather than trusted to be sensible", () => {
    learnDaemonName(ID, "m".repeat(NAME_MAX + 40));
    expect(learnedNames()[ID]).toHaveLength(NAME_MAX);
  });
});

describe("storage", () => {
  it("drops entries that are not strings rather than rendering them", () => {
    globalThis.localStorage?.setItem(
      "lola.mobile.daemonNames",
      JSON.stringify({ [ID]: 7, "": "x", ok: "fine" }),
    );
    expect(learnedNames()).toEqual({ ok: "fine" });
  });

  it("reads a hand-edited value that is not an object as nothing", () => {
    globalThis.localStorage?.setItem("lola.mobile.daemonLabels", '["work"]');
    expect(customNames()).toEqual({});
  });

  it("normalizes a name to nothing when it carries no renderable character", () => {
    expect(normalizeName("\x00\x07 \n")).toBe("");
  });
});
