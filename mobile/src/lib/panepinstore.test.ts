import { describe, it, expect, beforeEach } from "vitest";
import {
  OWN_SHELLS_MAX,
  clearPinState,
  forgetOwnShell,
  isOwnShell,
  loadOwnShells,
  loadPinEnabled,
  loadPinnedPanes,
  rememberOwnShell,
  savePinEnabled,
  savePinnedPanes,
} from "./panepinstore";

// Storage the pin leaves behind: the toggle, own shells and the breadcrumb.

beforeEach(() => {
  globalThis.localStorage?.clear();
});

describe("the toggle preference", () => {
  it("is off until it is turned on", () => {
    expect(loadPinEnabled()).toBe(false);
  });

  it("round-trips, and forgets", () => {
    savePinEnabled(true);
    expect(loadPinEnabled()).toBe(true);
    savePinEnabled(false);
    expect(loadPinEnabled()).toBe(false);
  });

  it("reads anything it did not write as OFF", () => {
    // The default has to be the one that leaves somebody else's screen alone,
    // so this is a fail-closed read rather than the usual tolerant one.
    globalThis.localStorage?.setItem("lola.mobile.pinPaneSize", "true");
    expect(loadPinEnabled()).toBe(false);
  });

  it("survives storage being unavailable", () => {
    const real = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      get() {
        throw new Error("storage is disabled in this WebView");
      },
    });
    try {
      expect(loadPinEnabled()).toBe(false);
      expect(loadPinnedPanes()).toEqual([]);
      expect(() => savePinEnabled(true)).not.toThrow();
      expect(() =>
        savePinnedPanes([{ session: "s", pane: "p" }]),
      ).not.toThrow();
      expect(() => clearPinState()).not.toThrow();
    } finally {
      if (real) Object.defineProperty(globalThis, "localStorage", real);
    }
  });
});

describe("the breadcrumb", () => {
  it("round-trips and clears", () => {
    savePinnedPanes([{ session: "lola-fe-42", pane: "lola-fe-42-shell-1" }]);
    expect(loadPinnedPanes()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42-shell-1" },
    ]);
    savePinnedPanes([]);
    expect(loadPinnedPanes()).toEqual([]);
  });

  it("holds more than one, because a failed release leaves one behind", () => {
    savePinnedPanes([
      { session: "lola-fe-42", pane: "lola-fe-42" },
      { session: "lola-fe-42", pane: "lola-fe-42-shell-1" },
    ]);
    expect(loadPinnedPanes()).toHaveLength(2);
  });

  it("still reads the single record the one-slot build wrote", () => {
    // An upgrade must not lose the pin the previous build was holding: the
    // window it squashed is on somebody's Mac either way.
    globalThis.localStorage?.setItem(
      "lola.mobile.pinnedPane",
      JSON.stringify({ session: "lola-fe-42", pane: "lola-fe-42-shell-2" }),
    );
    expect(loadPinnedPanes()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42-shell-2" },
    ]);
  });

  it("drops a half-formed record rather than naming an empty pane", () => {
    globalThis.localStorage?.setItem(
      "lola.mobile.pinnedPane",
      JSON.stringify({ session: "s" }),
    );
    expect(loadPinnedPanes()).toEqual([]);
    globalThis.localStorage?.setItem("lola.mobile.pinnedPane", "not json");
    expect(loadPinnedPanes()).toEqual([]);
    globalThis.localStorage?.setItem(
      "lola.mobile.pinnedPane",
      JSON.stringify([{ session: "s" }, { session: "s", pane: "p" }, 7]),
    );
    expect(loadPinnedPanes()).toEqual([{ session: "s", pane: "p" }]);
  });
});

// ---------------------------------------------------------------------------

describe("shells this device started", () => {
  // The pin defaults to off because it reshapes somebody else's screen. A shell
  // the PHONE created is the exception: nothing on the Mac is looking at it, and
  // its tmux window is born at a size that puts the prompt on row 0 with a void
  // beneath it.

  it("is not an own shell until one is recorded", () => {
    expect(isOwnShell("lola-fe-42-shell-1")).toBe(false);
    expect(loadOwnShells()).toEqual([]);
  });

  it("remembers one, idempotently", () => {
    rememberOwnShell("lola-fe-42-shell-1");
    rememberOwnShell("lola-fe-42-shell-1");
    expect(loadOwnShells()).toEqual(["lola-fe-42-shell-1"]);
    expect(isOwnShell("lola-fe-42-shell-1")).toBe(true);
  });

  it("forgets one without touching the others", () => {
    rememberOwnShell("a-shell-1");
    rememberOwnShell("b-shell-2");
    forgetOwnShell("a-shell-1");
    expect(loadOwnShells()).toEqual(["b-shell-2"]);
  });

  it("never claims a pane it did not record", () => {
    rememberOwnShell("lola-fe-42-shell-1");
    // The prefix trap the daemon's own matching is anchored against.
    expect(isOwnShell("lola-fe-420-shell-1")).toBe(false);
    expect(isOwnShell("")).toBe(false);
  });

  it("is bounded, dropping the oldest", () => {
    for (let i = 0; i < OWN_SHELLS_MAX + 5; i++)
      rememberOwnShell(`s-shell-${i}`);
    const all = loadOwnShells();
    expect(all).toHaveLength(OWN_SHELLS_MAX);
    expect(all).not.toContain("s-shell-0");
    expect(all).toContain(`s-shell-${OWN_SHELLS_MAX + 4}`);
  });

  it("reads a hand-edited value as nothing, so it cannot be made to pin", () => {
    globalThis.localStorage?.setItem(
      "lola.mobile.ownShells",
      '{"not":"an array"}',
    );
    expect(loadOwnShells()).toEqual([]);
    globalThis.localStorage?.setItem(
      "lola.mobile.ownShells",
      '["ok-shell-1", 7, "", null]',
    );
    expect(loadOwnShells()).toEqual(["ok-shell-1"]);
  });

  it("is cleared with the rest of the pin state", () => {
    rememberOwnShell("lola-fe-42-shell-1");
    clearPinState();
    expect(loadOwnShells()).toEqual([]);
  });
});
