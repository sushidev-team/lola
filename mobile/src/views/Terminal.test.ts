import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import Terminal from "./Terminal.svelte";
import { store } from "$lib/store.svelte";
import { nav } from "@mobile/lib/nav.svelte";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { bridge } from "@mobile/wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";
import type { SessionInfo } from "$lib/store.svelte";

// The terminal screen's HEADER. The pane itself is xterm on a canvas and is
// verified on a device; what is checked here is the three facts the header is
// supposed to state — which session, how it is doing, and whether there is a PR
// to open — and, above all, the case where there is no PR.
//
// The PR button is the one control on this screen that must be able to be
// ABSENT. A button that is drawn dead for most of a session's life teaches
// people not to look at that corner, which is the corner the app then wants
// them to look at.

// jsdom has no ResizeObserver, and MobileTerminal constructs one unconditionally
// while it boots — it is how the pane learns it has been resized by the soft
// keyboard, so it cannot be feature-probed away there the way edgefade.ts probes
// for one. Stubbed HERE rather than in the shared setup file: this is the first
// test that mounts the real terminal component, and a stub in the global setup
// would silently apply to every future test that would rather assert on a real
// observation. Nothing in these assertions depends on it firing.
class NoopResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
const g = globalThis as { ResizeObserver?: unknown };
g.ResizeObserver ??= NoopResizeObserver;

// THE PANE HAS TO BE ABLE TO ATTACH, because the header's subtitle now depends
// on whether it did. `connection` is a module singleton with its own private
// transport — installing one on the `bridge` (which is what the daemon commands
// below travel over) does not give it one — so without this every render in
// this file failed its subscribe, and after the branch reorder that is the
// state in which the status word is deliberately replaced by "No terminal".
// Switchable rather than fixed, because both outcomes are now behaviour worth
// pinning.
let attaches = true;

/**
 * Every pane-event listener the mounted terminal registered.
 *
 * Captured so a test can DELIVER an exit, which is the one fact this screen
 * learns and the tab strip cannot: it arrives on the subscription, which only
 * the screen holds. Reset per test in `beforeEach`.
 */
let paneListeners: ((e: unknown) => void)[] = [];

/** The little of `PaneSubscription` that MobileTerminal.attach touches. */
function fakeSubscription(pane: string) {
  return {
    pane,
    id: "sub-1",
    screen: null,
    lastSeq: 0,
    exited: false,
    write: async () => {},
    resize: async () => {},
    close: async () => {},
    onEvent: (fn: (e: unknown) => void) => {
      paneListeners.push(fn);
      return () => {};
    },
    onError: () => () => {},
  };
}

vi.mock("@mobile/lib/connection.svelte", () => ({
  connection: {
    get ready() {
      return attaches;
    },
    subscribe: (pane: string) =>
      attaches
        ? Promise.resolve(fakeSubscription(pane))
        : Promise.reject(new Error("unknown_pane: pane is not available")),
  },
}));

let ch: FakeChannel;
let replies: Record<
  string,
  { ok: true; data: unknown } | { ok: false; error: string }
>;

/** A session record with only the fields this screen reads. */
function session(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "lola-fe-42",
    issue: "NOR-401",
    status: "needs_input",
    prNumber: 0,
    prUrl: "",
    interpretedState: "",
    ...over,
  } as unknown as SessionInfo;
}

beforeEach(async () => {
  attaches = true;
  paneListeners = [];
  replies = {};
  ch = new FakeChannel();
  ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    const r = replies[String(f.cmd)] ?? { ok: true, data: {} };
    ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: r });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ host: "127.0.0.1", spkiPin: "pin" });
  bridge.installTransport(t);

  // The strip is a separate concern with its own tests; keep it out of the way
  // by answering with the inventory a session that has only an agent has.
  replies.panes = {
    ok: true,
    data: {
      session: "lola-fe-42",
      panes: [{ name: "lola-fe-42", kind: "agent", label: "agent" }],
      canCreateShell: true,
    },
  };

  nav.paneSession = "lola-fe-42";
  nav.pane = "lola-fe-42";
});

afterEach(() => {
  bridge.installTransport(null);
  store.sessions = [];
});

describe("Terminal header", () => {
  it("draws no PR button when the session has no PR", async () => {
    store.sessions = [session()];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(screen.queryByRole("button", { name: /pull request/i })).toBeNull();
  });

  it("draws no PR button when there is a number but no address for it", async () => {
    // Both halves come from the same gh fetch and they can disagree. A button
    // that opens nothing is worse than no button.
    store.sessions = [session({ prNumber: 401, prUrl: "" })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(screen.queryByRole("button", { name: /pull request/i })).toBeNull();
  });

  it("draws a PR button naming the number when there is one", async () => {
    store.sessions = [
      session({
        prNumber: 401,
        prUrl: "https://github.com/acme/nori/pull/401",
      }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    const btn = await screen.findByRole("button", {
      name: "Open pull request #401 in the browser",
    });
    expect(btn).toBeInTheDocument();
  });

  it("draws no Dev control for a project with no dev commands", async () => {
    // Absent rather than dead: a control that can never do anything teaches
    // people not to look at that corner.
    store.sessions = [session({ devCommands: [] })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(screen.queryByRole("button", { name: /dev commands/i })).toBeNull();
  });

  it("offers to RUN the dev commands when the project has them", async () => {
    store.sessions = [session({ devCommands: ["npm run dev"] })];
    render(Terminal, { props: { onback: () => {} } });

    const btn = await screen.findByRole("button", {
      name: "Run this session's dev commands here",
    });
    expect(btn).toHaveAttribute("aria-pressed", "false");
  });

  it("offers to STOP them when this session is the active one", async () => {
    store.sessions = [
      session({ devCommands: ["npm run dev"], devActive: true }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    const btn = await screen.findByRole("button", {
      name: "Stop this session's dev commands",
    });
    expect(btn).toHaveAttribute("aria-pressed", "true");
  });

  it("draws no link button until the daemon publishes an address", async () => {
    // devUrls alone are the MAC's loopback addresses; on a phone they reach
    // nothing, so only devForwards may draw this.
    store.sessions = [
      session({
        devActive: true,
        devUrls: ["http://127.0.0.1:8000"],
        devForwards: [],
      }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(screen.queryByRole("button", { name: /dev server/i })).toBeNull();
  });

  it("names the single published address on the link button", async () => {
    store.sessions = [
      session({ devActive: true, devForwards: ["http://192.168.20.3:52889"] }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    const btn = await screen.findByRole("button", {
      name: "Open the dev server at http://192.168.20.3:52889 on this phone",
    });
    expect(btn).toBeInTheDocument();
  });

  it("opens a sheet when several addresses are published, which is the normal case", async () => {
    // An app and a bundler print separate URLs; a button that guessed would
    // open the asset server about half the time.
    store.sessions = [
      session({
        devActive: true,
        devForwards: ["http://192.168.20.3:52889", "http://192.168.20.3:41005"],
      }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    const btn = await screen.findByRole("button", {
      name: "Open one of 2 dev server links on this phone",
    });
    await fireEvent.click(btn);
    expect(
      await screen.findByRole("dialog", { name: "Dev server links" }),
    ).toBeTruthy();
  });

  it("states the session status as text, in the shared vocabulary", async () => {
    // `needs_input` reads "needs you" on every surface — the word and the colour
    // come from $lib/theme, which is the port of Go's internal/state that
    // desktop/state_parity_test.go pins. A phone-side spelling would be a third
    // mirror of a list the repository keeps in exactly two.
    store.sessions = [session({ status: "needs_input" })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    const word = await screen.findByText("needs you");
    expect(word.className).toContain("text-orange");
  });

  it("steps the unnamed status family off the heading's ink", async () => {
    // theme.ts answers `text-ink` for every status it does not name, which is
    // correct inside a pill and wrong without one: with the pill gone, "review"
    // printed in exactly the ink of the title above it and read as emphasis
    // rather than as a state. Nothing about theme.ts changes — see statustone.
    store.sessions = [session({ status: "review_pending" })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    const word = await screen.findByText("review");
    expect(word.className).toContain("text-faint");
    expect(word.className).not.toContain("text-ink");
  });

  it("lets a pane error win the subtitle over the session's status", async () => {
    // The status describes the SESSION and the banner describes the PANE, and a
    // reader sees one screen. With the status branch first — and every session
    // the list still knows about taking it — the header could print "needs you"
    // directly above "This session's terminal is gone", which is the exact
    // contradiction humanError exists to remove. It also made the fallback
    // branch very nearly dead code.
    attaches = false;
    store.sessions = [session({ status: "needs_input" })];
    render(Terminal, { props: { onback: () => {} } });

    expect(await screen.findByText("No terminal")).toBeInTheDocument();
    expect(screen.queryByText("needs you")).toBeNull();
    // ...and the banner still says which failure it was, in English.
    expect(await screen.findByText(/terminal is gone/i)).toBeInTheDocument();
  });

  it("shows a refused shell in the daemon's own words, and lets it be dismissed", async () => {
    // The strip reports the refusal; this screen is what a person actually
    // reads. The sentence travels intact from the daemon through PaneTabs to
    // here — a generic "could not start a shell" would throw away the only half
    // anyone can act on.
    replies.shellCreate = {
      ok: false,
      error: 'session "lola-fe-42" has no worktree',
    };
    store.sessions = [session()];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));

    const banner = await screen.findByText(
      'session "lola-fe-42" has no worktree',
    );
    expect(banner).toBeInTheDocument();

    await fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    await waitFor(() =>
      expect(
        screen.queryByText('session "lola-fe-42" has no worktree'),
      ).toBeNull(),
    );
  });
});

describe("Terminal keeping the tab strip honest", () => {
  it("re-reads the pane inventory when the pane it is showing exits", async () => {
    // THE SEAM, end to end. A shell that exits ends its tmux session -- shells
    // get no `remain-on-exit`, only dev tabs do -- so `cmd=panes` stops listing
    // it immediately, while the app used to fetch that list once per screen and
    // never again. The exit arrives on the SUBSCRIPTION, which only this screen
    // holds, so the screen bumps a counter and the strip does the asking. The
    // strip's own half is covered in PaneTabs.test.ts; what is pinned here is
    // that the screen actually passes the signal along.
    store.sessions = [session()];
    replies.panes = {
      ok: true,
      data: {
        session: "lola-fe-42",
        panes: [
          { name: "lola-fe-42", kind: "agent", label: "agent" },
          {
            name: "lola-fe-42-shell-1",
            kind: "shell",
            label: "shell 1",
            index: 1,
          },
        ],
        canCreateShell: true,
      },
    };
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "shell 1" });
    const before = ch.sent.filter(
      (f) => f.type === "req" && f.cmd === "panes",
    ).length;
    expect(before).toBe(1);
    expect(paneListeners.length).toBeGreaterThan(0);

    // The shell is gone from the daemon's answer from here on.
    replies.panes = {
      ok: true,
      data: {
        session: "lola-fe-42",
        panes: [{ name: "lola-fe-42", kind: "agent", label: "agent" }],
        canCreateShell: true,
      },
    };
    // A real exit carries the final screen, and MobileTerminal PAINTS it before
    // it latches `exited` -- so a `null` here throws inside the paint instead of
    // exercising the wiring. Deliberately a 0x0 frame with no lines: any real
    // geometry makes xterm resize and schedule an animation frame into a
    // renderer jsdom has no canvas for, which surfaces as an unhandled error
    // that has nothing to do with what is under test. `geometryChanged` is
    // false for 0x0, so the paint is the erase sequence and nothing more.
    const lastFrame = {
      cols: 0,
      rows: 0,
      cursorX: 0,
      cursorY: 0,
      exited: true,
    };
    // xterm SCHEDULES ITS REPAINT through requestAnimationFrame, and in jsdom
    // that callback reaches a RenderService with no renderer behind it (there
    // is no canvas) and throws asynchronously, long after this test's
    // assertions. It is noise from the environment rather than a fault in the
    // wiring, so the frame is dropped for exactly the span that paints. Every
    // other test in this file mounts the terminal without ever writing to it,
    // which is why none of them needed this.
    const raf = globalThis.requestAnimationFrame;
    globalThis.requestAnimationFrame = (() => 0) as typeof raf;
    try {
      for (const fn of paneListeners)
        fn({ kind: "exit", screen: lastFrame, seq: 1 });
    } finally {
      globalThis.requestAnimationFrame = raf;
    }

    await waitFor(() =>
      expect(
        ch.sent.filter((f) => f.type === "req" && f.cmd === "panes"),
      ).toHaveLength(2),
    );
    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: "shell 1" })).toBeNull(),
    );
  });

  it("re-reads it when an attach is refused because the pane is already gone", async () => {
    // `unknown_pane` on the subscribe says the same thing one moment earlier:
    // the pane was gone before this screen ever asked for it. Matched on the
    // daemon's own wire code, the same prefix `humanError` translates for the
    // banner.
    attaches = false;
    store.sessions = [session()];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    await waitFor(() =>
      expect(
        ch.sent.filter((f) => f.type === "req" && f.cmd === "panes").length,
      ).toBeGreaterThan(1),
    );
    // ...and the banner still explains it in English, unchanged.
    expect(await screen.findByText(/terminal is gone/i)).toBeInTheDocument();
  });
});
