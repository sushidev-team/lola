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
    onEvent: () => () => {},
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
let replies: Record<string, { ok: true; data: unknown } | { ok: false; error: string }>;

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
      session({ prNumber: 401, prUrl: "https://github.com/acme/nori/pull/401" }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    const btn = await screen.findByRole("button", { name: "Open pull request #401 in the browser" });
    expect(btn).toBeInTheDocument();
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
    replies.shellCreate = { ok: false, error: 'session "lola-fe-42" has no worktree' };
    store.sessions = [session()];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));

    const banner = await screen.findByText('session "lola-fe-42" has no worktree');
    expect(banner).toBeInTheDocument();

    await fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    await waitFor(() =>
      expect(screen.queryByText('session "lola-fe-42" has no worktree')).toBeNull(),
    );
  });
});
