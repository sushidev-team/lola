import { describe, it, expect, beforeEach, vi } from "vitest";
import { DaemonError } from "@mobile/wire";
import {
  OWN_SHELLS_MAX,
  PIN_MAX_DIM,
  PIN_STUCK_MESSAGE,
  PanePin,
  clearPinState,
  forgetOwnShell,
  isOwnShell,
  loadOwnShells,
  loadPinEnabled,
  loadPinnedPanes,
  rememberOwnShell,
  savePinEnabled,
  savePinnedPanes,
  type PinTarget,
} from "./panepin";

// The pin lifecycle, exercised where it actually lives.
//
// EVERY RELEASE PATH IS A TEST HERE, one per path, because the release is the
// half that can fail silently and leave a developer's tmux window squashed to
// phone width with nothing on the Mac saying why. The screen wiring is checked
// separately (src/views/TerminalPin.test.ts) — that file proves each of the
// screen's exits reaches this controller; this one proves what the controller
// does when it is reached, including the cases a component test cannot stage:
// a request that throws, a breadcrumb from a run that is over, and two panes
// racing for the pin.

/** A recording seam in the shape of DaemonService.PaneResize. */
function seam() {
  const calls: { session: string; pane: string; cols: number; rows: number }[] =
    [];
  let fail = false;
  /**
   * When set, failures are refusals the DAEMON answered rather than requests
   * that never arrived. The two mean opposite things to the pin — see the
   * `#send` catch — so the seam has to be able to stage both.
   */
  let refuse = false;
  let refusal = "";
  /** Per-request refusal, for the case where ONE call fails and the next does not. */
  let failIf: (pane: string, cols: number) => boolean = () => false;
  return {
    calls,
    failFrom(on: boolean) {
      fail = on;
    },
    /** Fail the way a daemon that answered `ok: false` fails. */
    refuseFrom(on: boolean, message = 'unknown cmd "paneResize"') {
      fail = on;
      refuse = on;
      refusal = message;
    },
    /** Fail only the calls this predicate picks. Cleared by passing nothing. */
    failWhen(fn?: (pane: string, cols: number) => boolean) {
      failIf = fn ?? (() => false);
    },
    /** Only the releases, which is what most of this file is about. */
    releases() {
      return calls.filter((c) => c.cols === 0 && c.rows === 0);
    },
    pins() {
      return calls.filter((c) => c.cols > 0);
    },
    resize: vi.fn(
      async (session: string, pane: string, cols: number, rows: number) => {
        calls.push({ session, pane, cols, rows });
        if (fail || failIf(pane, cols)) {
          throw refuse
            ? new DaemonError("paneResize", refusal)
            : new Error("not connected");
        }
        return { session, pane, pinned: cols > 0, cols, rows };
      },
    ),
  };
}

const target = (over: Partial<PinTarget> = {}): PinTarget => ({
  session: "lola-fe-42",
  pane: "lola-fe-42",
  cols: 50,
  rows: 20,
  ...over,
});

/** No debounce: the settle window is behaviour of its own and is tested apart. */
function make(o: { report?: (m: string) => void; settleMs?: number } = {}) {
  const s = seam();
  return { s, pin: new PanePin({ resize: s.resize, settleMs: 0, ...o }) };
}

beforeEach(() => {
  globalThis.localStorage?.clear();
});

// ---------------------------------------------------------------------------

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

describe("pinning", () => {
  it("sends the phone's own size, not the Mac's grid", async () => {
    const { s, pin } = make();
    await pin.set(target({ cols: 50, rows: 20 }));
    expect(s.calls).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42", cols: 50, rows: 20 },
    ]);
  });

  it("records the breadcrumb BEFORE the request, so a kill mid-flight is recoverable", async () => {
    const s = seam();
    let crumbAtRequest: unknown = "never asked";
    const pin = new PanePin({
      settleMs: 0,
      resize: async (session, pane, cols, rows) => {
        crumbAtRequest = loadPinnedPanes();
        return s.resize(session, pane, cols, rows);
      },
    });
    await pin.set(target());
    expect(crumbAtRequest).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42" },
    ]);
  });

  it("does not re-send a pin that is already in force", async () => {
    const { s, pin } = make();
    await pin.set(target());
    await pin.set(target());
    expect(s.calls).toHaveLength(1);
  });

  it("re-sends when the size moves, which is what the soft keyboard does", async () => {
    const { s, pin } = make();
    await pin.set(target({ rows: 20 }));
    await pin.set(target({ rows: 9 }));
    expect(s.pins()).toHaveLength(2);
    expect(s.pins()[1].rows).toBe(9);
    // The pane never changed, so nothing was handed back in between.
    expect(s.releases()).toHaveLength(0);
  });

  it("refuses to send an unmeasured box, because a zero is the RELEASE encoding", async () => {
    const { s, pin } = make();
    await pin.set(target({ cols: 0, rows: 0 }));
    expect(s.calls).toHaveLength(0);
    expect(pin.held()).toEqual([]);
  });

  it("clamps to the daemon's bound rather than being refused by it", async () => {
    const { s, pin } = make();
    await pin.set(target({ cols: 9000, rows: 9000 }));
    expect(s.calls[0]).toMatchObject({ cols: PIN_MAX_DIM, rows: PIN_MAX_DIM });
  });
});

// ---------------------------------------------------------------------------
// The exits. One test per path, and the paths are the point.

describe("release paths", () => {
  it("releases on a plain release()", async () => {
    const { s, pin } = make();
    await pin.set(target());
    await pin.release();
    expect(s.releases()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42", cols: 0, rows: 0 },
    ]);
    expect(pin.held()).toEqual([]);
    expect(loadPinnedPanes()).toEqual([]);
  });

  it("releases when the screen stops wanting anything (leaving, exiting, disconnecting)", async () => {
    // Every one of those exits reaches the controller as the same call: the
    // screen's one effect yields null. That is deliberate — a single expression
    // covering all of them is what keeps a case from being forgotten — so the
    // controller sees one path and the screen test pins that each exit takes it.
    const { s, pin } = make();
    await pin.set(target());
    await pin.set(null);
    expect(s.releases()).toHaveLength(1);
  });

  it("releases the old pane BEFORE pinning a new one, so two are never pinned at once", async () => {
    const { s, pin } = make();
    await pin.set(target({ pane: "lola-fe-42" }));
    await pin.set(target({ pane: "lola-fe-42-shell-1" }));
    expect(s.calls.map((c) => [c.pane, c.cols])).toEqual([
      ["lola-fe-42", 50],
      ["lola-fe-42", 0],
      ["lola-fe-42-shell-1", 50],
    ]);
  });

  it("never holds two panes at once, however fast the tabs are switched", async () => {
    // The screen does not await anything: a tab switch is a synchronous
    // reassignment and the requests it causes overlap. The guarantee is not
    // about the number of calls, it is that replaying them never leaves two
    // windows pinned — so it is asserted by replaying them.
    //
    // A SLOW SEAM is the point of this test. With an instant one the switch is
    // decided before the first request is even issued and the overlap the
    // guarantee is about never happens.
    const s = seam();
    const slow = new PanePin({
      settleMs: 0,
      resize: async (session, pane, cols, rows) => {
        await new Promise((r) => setTimeout(r, 1));
        return s.resize(session, pane, cols, rows);
      },
    });

    void slow.set(target({ pane: "lola-fe-42" }));
    void slow.set(target({ pane: "lola-fe-42-shell-1" }));
    void slow.set(target({ pane: "lola-fe-42-shell-2" }));
    void slow.release();
    void slow.set(target({ pane: "lola-fe-42-review" }));
    await slow.settled();

    const held = new Set<string>();
    for (const c of s.calls) {
      if (c.cols > 0) held.add(c.pane);
      else held.delete(c.pane);
      expect(
        held.size,
        `two panes pinned after ${c.pane}:${c.cols}`,
      ).toBeLessThanOrEqual(1);
    }
    expect([...held]).toEqual(["lola-fe-42-review"]);
  });

  it("hands the old window back before taking the new one", async () => {
    const { s, pin } = make();
    await pin.set(target({ pane: "lola-fe-42" }));
    void pin.set(target({ pane: "lola-fe-42-shell-1" }));
    await pin.settled();
    expect(s.calls.map((c) => `${c.pane}:${c.cols}`)).toEqual([
      "lola-fe-42:50",
      "lola-fe-42:0",
      "lola-fe-42-shell-1:50",
    ]);
  });

  it("is idempotent: a second release sends nothing and does not throw", async () => {
    const { s, pin } = make();
    await pin.set(target());
    await pin.release();
    await pin.release();
    await pin.release();
    expect(s.releases()).toHaveLength(1);
  });

  it("sends nothing at all when it was never pinned", async () => {
    const { s, pin } = make();
    await pin.release();
    expect(s.calls).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------

describe("a release that cannot be sent", () => {
  it("says so plainly rather than leaving a pin behind in silence", async () => {
    const report = vi.fn();
    const { s, pin } = make({ report });
    await pin.set(target());
    s.failFrom(true);
    await pin.release();
    expect(report).toHaveBeenLastCalledWith(PIN_STUCK_MESSAGE);
  });

  it("keeps the breadcrumb, so the pin is still findable next time", async () => {
    const { s, pin } = make();
    await pin.set(target());
    s.failFrom(true);
    await pin.release();
    expect(loadPinnedPanes()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42" },
    ]);
    expect(pin.held()).toHaveLength(1);
  });

  it("withdraws the warning once a later request gets through", async () => {
    // Backgrounding loses this race routinely, so a permanent banner about a pin
    // that was undone seconds later would be worse than none.
    const report = vi.fn();
    const { s, pin } = make({ report });
    await pin.set(target());
    s.failFrom(true);
    await pin.release();
    s.failFrom(false);
    await pin.reassert();
    expect(report).toHaveBeenLastCalledWith("");
    expect(loadPinnedPanes()).toEqual([]);
  });

  it("assumes a PIN whose request threw may have landed, and can still undo it", async () => {
    // The safe direction: forgetting a pin that did land squashes a window
    // forever, while releasing one that never landed is a no-op on the Mac.
    const { s, pin } = make();
    s.failFrom(true);
    await pin.set(target());
    expect(pin.held()).toEqual([{ session: "lola-fe-42", pane: "lola-fe-42" }]);
    s.failFrom(false);
    await pin.release();
    expect(s.releases()).toHaveLength(1);
    expect(loadPinnedPanes()).toEqual([]);
  });
});

// ---------------------------------------------------------------------------

describe("a pin the daemon refused", () => {
  // The opposite of the case above, and the distinction is what the daemon
  // ANSWERED. A request that threw on the way out may have been applied; a
  // request the daemon received and declined was not. Every refusal on the pin
  // path leaves the window untouched — an unknown command, an unknown session
  // or pane, a size out of range, and a tmux failure, which undoes its own
  // half-applied option before answering (internal/tmux/client.go).

  it("is not believed held, because it never landed", async () => {
    const { s, pin } = make();
    s.refuseFrom(true);
    await pin.set(target());
    expect(pin.held()).toEqual([]);
    expect(loadPinnedPanes()).toEqual([]);
  });

  it("does not warn about a window that was never resized", async () => {
    // The bug this exists for, end to end: a phone newer than the daemon it is
    // talking to. `cmd=paneResize` came back "unknown cmd", so nothing on the
    // Mac was ever pinned — and the app told its user a developer's window was
    // still squashed, which is the one situation where the warning cannot be
    // true.
    const report = vi.fn();
    const { s, pin } = make({ report });
    s.refuseFrom(true);
    await pin.set(target());
    await pin.release();
    expect(report).not.toHaveBeenCalledWith(PIN_STUCK_MESSAGE);
    expect(pin.held()).toEqual([]);
  });

  it("spends no release on a pane the daemon never pinned", async () => {
    const { s, pin } = make();
    s.refuseFrom(true);
    await pin.set(target());
    s.refuseFrom(false);
    await pin.release();
    expect(s.releases()).toHaveLength(0);
  });

  it("still pins the next pane, so one refusal does not disable the feature", async () => {
    const { s, pin } = make();
    s.refuseFrom(true);
    await pin.set(target());
    s.refuseFrom(false);
    await pin.set(target({ pane: "lola-fe-42-shell-1" }));
    expect(s.pins().map((c) => c.pane)).toEqual([
      "lola-fe-42",
      "lola-fe-42-shell-1",
    ]);
    expect(pin.held()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42-shell-1" },
    ]);
  });

  it("keeps holding a RELEASE the daemon refused, which may still be in force", async () => {
    // A refused release is the opposite case: the daemon already forgives a
    // release of a pane that is genuinely gone, so a refusal here describes a
    // pin that may well still be holding somebody's window.
    const report = vi.fn();
    const { s, pin } = make({ report });
    await pin.set(target());
    s.refuseFrom(true);
    await pin.release();
    expect(pin.held()).toEqual([{ session: "lola-fe-42", pane: "lola-fe-42" }]);
    expect(loadPinnedPanes()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42" },
    ]);
    expect(report).toHaveBeenLastCalledWith(PIN_STUCK_MESSAGE);
  });
});

// ---------------------------------------------------------------------------

describe("recovery from a breadcrumb", () => {
  it("releases a pin an earlier run left behind", async () => {
    savePinnedPanes([{ session: "lola-fe-42", pane: "lola-fe-42-shell-2" }]);
    const { s, pin } = make();
    await pin.recover();
    expect(s.releases()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42-shell-2", cols: 0, rows: 0 },
    ]);
    expect(loadPinnedPanes()).toEqual([]);
  });

  it("costs one storage read when there is nothing to undo", async () => {
    const { s, pin } = make();
    await pin.recover();
    expect(s.calls).toHaveLength(0);
  });

  it("leaves the pane it is about to pin alone", async () => {
    // Reopening the same pane with the toggle on would otherwise release it and
    // re-pin it a moment later, flapping the Mac's window for nothing.
    savePinnedPanes([{ session: "lola-fe-42", pane: "lola-fe-42" }]);
    const { s, pin } = make();
    await pin.recover({ session: "lola-fe-42", pane: "lola-fe-42" });
    expect(s.calls).toHaveLength(0);
    expect(loadPinnedPanes()).toHaveLength(1);
  });

  it("leaves a pane that is pinned right now alone", async () => {
    const { s, pin } = make();
    await pin.set(target());
    await pin.recover();
    expect(s.releases()).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------

describe("a stray pin: a release that failed while another pane took the pin", () => {
  // THE BUG THIS SECTION EXISTS FOR. The controller kept ONE slot for what it
  // believed pinned, and one breadcrumb under it. A release that failed while
  // the pin that followed succeeded overwrote both, so the first pane stayed
  // squashed on a developer's Mac with nothing believing it, nothing recording
  // it and nothing that would ever release it -- the single failure this module
  // exists to prevent, arriving through the mechanism meant to prevent it.

  const A = { session: "lola-fe-42", pane: "lola-fe-42" };
  const B = { session: "lola-fe-42", pane: "lola-fe-42-shell-1" };

  /** Pin A, then switch to B with A's release refused. */
  async function strand(report?: (m: string) => void) {
    const { s, pin } = make(report ? { report } : {});
    await pin.set(target({ pane: A.pane }));
    s.failWhen((pane, cols) => pane === A.pane && cols === 0);
    await pin.set(target({ pane: B.pane }));
    return { s, pin };
  }

  it("keeps believing the pane it could not release", async () => {
    const { pin } = await strand();
    expect(pin.held()).toEqual([A, B]);
  });

  it("keeps RECORDING it, so a later run can still undo it", async () => {
    const { pin } = await strand();
    void pin;
    expect(loadPinnedPanes()).toEqual([A, B]);
  });

  it("does not withdraw the warning because a DIFFERENT pane pinned cleanly", async () => {
    const report = vi.fn();
    await strand(report);
    expect(report).toHaveBeenLastCalledWith(PIN_STUCK_MESSAGE);
  });

  it("retries the release on every later apply, and lets it go when one lands", async () => {
    const report = vi.fn();
    const { s, pin } = await strand(report);
    expect(s.releases().filter((c) => c.pane === A.pane)).toHaveLength(1);

    await pin.reassert();
    expect(s.releases().filter((c) => c.pane === A.pane)).toHaveLength(2);

    s.failWhen();
    await pin.reassert();
    expect(pin.held()).toEqual([B]);
    expect(loadPinnedPanes()).toEqual([B]);
    expect(report).toHaveBeenLastCalledWith("");
  });

  it("does not lose it when the whole socket is down, which fails BOTH calls", async () => {
    // The pin that follows a failed release throws too, and is assumed to have
    // landed. Both panes are then outstanding and both have to be remembered.
    const { s, pin } = make();
    await pin.set(target({ pane: A.pane }));
    s.failFrom(true);
    await pin.set(target({ pane: B.pane }));
    expect(pin.held()).toEqual([A, B]);

    s.failFrom(false);
    await pin.release();
    expect(pin.held()).toEqual([]);
    expect(
      s
        .releases()
        .map((c) => c.pane)
        .filter((n) => n === A.pane),
    ).toHaveLength(2);
  });
});

// ---------------------------------------------------------------------------

describe("retiring a record for a pane that no longer exists", () => {
  // A release of a pane whose tmux window is GONE is refused by the daemon,
  // which validates the pane by name convention and then asks tmux to resize
  // something that is not there. Nothing is squashed, so warning about it is a
  // lie that never comes down -- hence the inventory being allowed to retire a
  // record. See forgetMissing.

  const A = { session: "lola-fe-42", pane: "lola-fe-42-shell-1" };
  const B = { session: "lola-fe-42", pane: "lola-fe-42" };

  it("drops it, withdraws the warning, and sends nothing to do it", async () => {
    const report = vi.fn();
    const { s, pin } = make({ report });
    await pin.set(target({ pane: A.pane }));
    s.failWhen((pane, cols) => pane === A.pane && cols === 0);
    await pin.set(target({ pane: B.pane }));
    expect(report).toHaveBeenLastCalledWith(PIN_STUCK_MESSAGE);

    const before = s.calls.length;
    await pin.forgetMissing("lola-fe-42", [B.pane]);
    expect(pin.held()).toEqual([B]);
    expect(loadPinnedPanes()).toEqual([B]);
    expect(report).toHaveBeenLastCalledWith("");
    expect(s.calls).toHaveLength(before);
  });

  it("never retires the pane that is WANTED, and so sends nothing about it", async () => {
    // Two loads for one session can resolve out of order, so an answer that
    // predates the pin is not hypothetical. Retiring the wanted pane would be
    // undone at once by the apply that follows -- which is the point: the cost
    // is a redundant pin, and a redundant reflow of the agent's TUI on the Mac,
    // on every inventory load that races it.
    const { s, pin } = make();
    await pin.set(target({ pane: B.pane }));
    const before = s.calls.length;
    await pin.forgetMissing("lola-fe-42", []);
    expect(s.calls).toHaveLength(before);
    expect(pin.held()).toEqual([B]);
    expect(loadPinnedPanes()).toEqual([B]);
  });

  it("says nothing about another session's panes", async () => {
    savePinnedPanes([{ session: "lola-api-7", pane: "lola-api-7-shell-3" }]);
    const { pin } = make();
    await pin.recover();
    // The recover above releases it; re-strand it to prove the filter, not the
    // sweep.
    savePinnedPanes([{ session: "lola-api-7", pane: "lola-api-7-shell-3" }]);
    const { s, pin: p2 } = make();
    s.failWhen(() => true);
    await p2.recover();
    expect(p2.held()).toHaveLength(1);
    await p2.forgetMissing("lola-fe-42", []);
    expect(p2.held()).toEqual([
      { session: "lola-api-7", pane: "lola-api-7-shell-3" },
    ]);
  });

  it("ignores an empty session, which names nothing", async () => {
    const { pin } = make();
    await pin.set(target({ pane: B.pane }));
    await pin.forgetMissing("", []);
    expect(pin.held()).toEqual([B]);
  });
});

// ---------------------------------------------------------------------------

describe("recover merging rather than replacing", () => {
  it("still sweeps a leftover pane when a new pin got in first", async () => {
    // The one-slot version read the crumb at recover time, by which point a pin
    // that had already landed had overwritten it -- so the older pane was never
    // swept at all. Merging makes the order stop mattering.
    savePinnedPanes([{ session: "lola-fe-42", pane: "lola-fe-42-shell-9" }]);
    const { s, pin } = make();
    await pin.set(target());
    await pin.recover();
    expect(s.releases()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42-shell-9", cols: 0, rows: 0 },
    ]);
    expect(loadPinnedPanes()).toEqual([
      { session: "lola-fe-42", pane: "lola-fe-42" },
    ]);
  });
});

describe("reasserting over a new socket", () => {
  it("re-sends the pin, because the daemon may never have seen the last one", async () => {
    const { s, pin } = make();
    await pin.set(target());
    await pin.reassert();
    expect(s.pins()).toHaveLength(2);
  });

  it("re-sends the release when nothing is wanted any more", async () => {
    const { s, pin } = make();
    await pin.set(target());
    s.failFrom(true);
    await pin.release();
    s.failFrom(false);
    await pin.reassert();
    expect(s.releases()).toHaveLength(2);
    expect(pin.held()).toEqual([]);
  });

  it("does nothing when there is nothing to assert", async () => {
    const { s, pin } = make();
    await pin.reassert();
    expect(s.calls).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------

describe("the settle window", () => {
  it("debounces a size change but never a pane change or a release", async () => {
    vi.useFakeTimers();
    try {
      const s = seam();
      const pin = new PanePin({ resize: s.resize, settleMs: 400 });

      pin.want(target());
      await pin.settled();
      expect(s.pins()).toHaveLength(1);

      // A size-only change waits.
      pin.want(target({ rows: 9 }));
      await pin.settled();
      expect(s.pins()).toHaveLength(1);
      vi.advanceTimersByTime(400);
      await pin.settled();
      expect(s.pins()).toHaveLength(2);

      // A release does not.
      pin.want(null);
      await pin.settled();
      expect(s.releases()).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("drops a pending settle on stop(), so a torn-down screen cannot pin", async () => {
    vi.useFakeTimers();
    try {
      const s = seam();
      const pin = new PanePin({ resize: s.resize, settleMs: 400 });
      pin.want(target());
      await pin.settled();
      pin.want(target({ rows: 9 }));
      pin.stop();
      vi.advanceTimersByTime(1000);
      await pin.settled();
      expect(s.pins()).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
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
