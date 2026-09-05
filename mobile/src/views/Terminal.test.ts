import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import Terminal from "./Terminal.svelte";
import { store } from "$lib/store.svelte";
import { nav } from "@mobile/lib/nav.svelte";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { bridge } from "@mobile/wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";
import type { SessionInfo } from "$lib/store.svelte";

// The terminal screen's CHROME. The pane itself is xterm on a canvas and is
// verified on a device; what is checked here is what surrounds it — which
// session this is, how it is doing, what its PR is doing, and which actions are
// offered — and, above all, the cases where something is ABSENT.
//
// Absence is the theme of this file because it is the property the screen keeps
// getting wrong. A control drawn dead for most of a session's life teaches
// people not to look at that corner, which is the corner the app then wants
// them to look at; an empty card above a terminal costs the pane rows for
// nothing; a status word printed over a banner that contradicts it is worse
// than no status at all.
//
// THE ACTIONS MOVED. The Dev toggle, the dev-server link and the PR button used
// to be glyph buttons in the header row and are now rows in a sheet behind the
// overflow button. Their handlers, their accessible names and their
// absent-rather-than-disabled rule are unchanged, which is exactly what the
// tests below are shaped to prove: every one of them still asks for the same
// accessible name, having opened the menu first.

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

/**
 * Open the header's overflow sheet, which is where every action on this screen
 * now lives.
 *
 * It waits for the tab strip first for the reason every test in this file does:
 * the strip's `cmd=panes` round trip is the last thing the screen does on mount,
 * so a tab is the cheapest proof that the render has settled.
 */
async function openMenu(): Promise<void> {
  await screen.findByRole("tab", { name: "agent" });
  await fireEvent.click(screen.getByRole("button", { name: "Session actions" }));
  await screen.findByRole("dialog", { name: "Session actions" });
}

describe("Terminal header", () => {
  // ONE BUTTON, AND WHAT IT INHERITED. The header used to carry a view-settings
  // glyph beside the session menu's — 88 points of controls on the row where the
  // issue key was the only item allowed to shorten, on the one screen where the
  // key is the whole answer to "which session is this". The two were merged.
  //
  // The view settings were not just a way in: that trigger wore a warn dot and
  // carried the live column range in its accessible name whenever the pane was
  // clipped, and a phone showing 55 of a developer's 200 columns makes a clipped
  // pane look exactly like an agent that stopped writing mid-line. The rule is
  // pinned in ViewSettings.test.ts (`viewClippingNotice`) and the BUTTON
  // spending it in TerminalPin.test.ts, which is the file with a measured grid —
  // the terminal here reports no geometry at all, so a clipping assertion in
  // this file would pass against zeros and prove nothing.
  it("puts the view settings inside the one sheet, ahead of the actions", async () => {
    // The controls that used to hang off the second header button. What is
    // asserted here is that they ARRIVED — the readout's own numbers need a
    // measured grid and are pinned in TerminalPin.test.ts — and that they come
    // FIRST: a text size and a column readout are adjusted while reading, while
    // the dev toggle and the PR link are done once.
    store.sessions = [session()];
    render(Terminal, { props: { onback: () => {} } });
    await openMenu();

    expect(screen.getByRole("switch")).toBeTruthy(); // the pane-size pin
    expect(screen.getByRole("button", { name: "Larger text" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Smaller text" })).toBeTruthy();

    // By accessible NAME, not by aria-label: "Done" has none — its name is its
    // text — so an aria-label lookup answers -1 and the comparison passes for
    // the wrong reason.
    const dialog = screen.getByRole("dialog", { name: "Session actions" });
    const order = [...dialog.querySelectorAll("button")].map(
      (b) => b.getAttribute("aria-label") || b.textContent!.trim(),
    );
    expect(order.indexOf("Larger text")).toBeGreaterThanOrEqual(0);
    expect(order.indexOf("Larger text")).toBeLessThan(order.indexOf("Done"));
  });

  it("names the session by its issue key, with its title on the line under it", async () => {
    // The key leads HERE and the title leads in the list, which is not an
    // inconsistency: a list is about telling sessions apart, and a person who
    // has already tapped one is asking which ticket they are in.
    store.sessions = [session({ title: "Forward the dev servers to the phone" })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(screen.getByText("NOR-401")).toBeInTheDocument();
    expect(
      screen.getByText("Forward the dev servers to the phone"),
    ).toBeInTheDocument();
  });

  it("draws no subtitle at all for a record that carries no title", async () => {
    // `title` is "" for older and adopted records. The tmux name used to fill
    // the gap, which led a screen about a ticket with `lola-fe-42`.
    store.sessions = [session({ title: "" })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(screen.queryByText("lola-fe-42")).toBeNull();
  });

  it("draws no PR badge and offers no PR row when the session has no PR", async () => {
    store.sessions = [session()];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
    expect(screen.queryByRole("button", { name: /pull request/i })).toBeNull();
    expect(screen.queryByText("#401")).toBeNull();
  });

  it("keeps the badge but offers no PR row when there is a number and no address", async () => {
    // Both halves come from the same gh fetch and they can disagree. A button
    // that opens nothing is worse than no button — but the NUMBER is a fact
    // about the session and survives a missing URL, which is why the badge and
    // the action are gated differently.
    store.sessions = [session({ prNumber: 401, prUrl: "" })];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
    expect(screen.queryByRole("button", { name: /pull request/i })).toBeNull();
    expect(screen.getByText("#401")).toBeInTheDocument();
  });

  it("offers the PR by number in the overflow menu when there is an address", async () => {
    store.sessions = [
      session({
        prNumber: 401,
        prUrl: "https://github.com/acme/nori/pull/401",
      }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
    const btn = await screen.findByRole("button", {
      name: "Open pull request #401 in the browser",
    });
    expect(btn).toBeInTheDocument();
  });

  it("names the PR badge for a screen reader, which sees only a number", async () => {
    // "#401" on its own is a loose number belonging to nothing. The badge is a
    // <span> rather than a control — the action is in the menu — so the only
    // thing that can carry the context is the text itself.
    store.sessions = [session({ prNumber: 401 })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(screen.getByText(/Pull request/)).toBeInTheDocument();
  });

  it("draws no Dev control for a project with no dev commands", async () => {
    // Absent rather than dead: a control that can never do anything teaches
    // people not to look at that corner. Checked with the MENU OPEN, so this is
    // a statement about the whole screen rather than about the header alone.
    store.sessions = [session({ devCommands: [] })];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
    expect(screen.queryByRole("button", { name: /dev commands/i })).toBeNull();
  });

  it("offers to RUN the dev commands when the project has them", async () => {
    store.sessions = [session({ devCommands: ["npm run dev"] })];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
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

    await openMenu();
    const btn = await screen.findByRole("button", {
      name: "Stop this session's dev commands",
    });
    expect(btn).toHaveAttribute("aria-pressed", "true");
  });

  it("says in the menu that starting the dev commands stops another session's", async () => {
    // The sentence is the whole reason these moved. It is a MOVE, not a toggle —
    // only one session per project may bind the ports — and the header glyph
    // carried that fact in an aria-label, so it reached VoiceOver users and
    // nobody else.
    store.sessions = [session({ devCommands: ["npm run dev"] })];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
    expect(
      screen.getByText(/stops\s+another session's servers/i),
    ).toBeInTheDocument();
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

    await openMenu();
    expect(screen.queryByRole("button", { name: /dev server/i })).toBeNull();
  });

  it("names the single published address on the link button", async () => {
    store.sessions = [
      session({
        devActive: true,
        devForwards: [
          { url: "http://192.168.20.3:52889", from: "127.0.0.1:8000" },
        ],
      }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
    const btn = await screen.findByRole("button", {
      name: "Open the dev server at 127.0.0.1:8000 on this phone",
    });
    expect(btn).toBeInTheDocument();
  });

  it("offers the link even when the daemon is too old to report dev commands", async () => {
    // `devCommands` arrived after `devForwards`, so a session can publish an
    // address while the toggle cannot be offered at all. The link is still
    // worth having, which is why it has a branch of its own.
    store.sessions = [
      session({
        devActive: true,
        devCommands: [],
        devForwards: [
          { url: "http://192.168.20.3:52889", from: "127.0.0.1:8000" },
        ],
      }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
    expect(
      await screen.findByRole("button", {
        name: "Open the dev server at 127.0.0.1:8000 on this phone",
      }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /dev commands/i })).toBeNull();
  });

  it("swaps the menu for the link sheet when several addresses are published", async () => {
    // An app and a bundler print separate URLs; a button that guessed would
    // open the asset server about half the time. The menu has to CLOSE on the
    // way — Sheet is not stacked anywhere else in this app and there is no
    // z-order story for two of them.
    store.sessions = [
      session({
        devActive: true,
        devForwards: [
          { url: "http://192.168.20.3:52889", from: "127.0.0.1:8000" },
          { url: "http://192.168.20.3:41005", from: "127.0.0.1:5175" },
        ],
      }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    await openMenu();
    const btn = await screen.findByRole("button", {
      name: "Open one of 2 dev server links on this phone",
    });
    await fireEvent.click(btn);
    expect(
      await screen.findByRole("dialog", { name: "Dev server links" }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Session actions" }),
      ).toBeNull(),
    );
  });

  it("states the session status in the shared vocabulary, as a word under the key", async () => {
    // `needs_input` reads "needs you" on every surface — the word comes from
    // $lib/theme, which is the port of Go's internal/state that
    // desktop/state_parity_test.go pins, and the TONE from `statusTone`, the one
    // phone-local rule. A phone-side spelling or a second colour table would be
    // a third mirror of a list the repository keeps in exactly two.
    //
    // A WORD, NOT A CHIP, AND NOT ON THE IDENTITY ROW. It used to be a
    // <StatusChip> between the issue key and the spacer — up to 126 points of
    // filled badge in the middle of the row whose remaining space the key was
    // competing for, on the one screen where the key is the only thing saying
    // WHICH session this is. It now leads the title line underneath, which is
    // the same shape the compact row in the list uses, so a person tapping a row
    // meets the same two facts in the same order one screen further in.
    store.sessions = [session({ status: "needs_input" })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    const word = await screen.findByText("needs you");
    expect(word.className).toContain("text-orange");
    // No chip ground anywhere near it.
    expect(word.className).not.toContain("bg-pill-urgent-soft");
  });

  it("keeps the quiet half of the vocabulary quiet", async () => {
    // Everything that is true but not news — review_pending, approved, merged,
    // working — takes the faint tier rather than a colour of its own, which is
    // `statusTone`'s single rule: theme.ts answers `text-ink` for the family it
    // does not name, and an ink status word beside a faint title would be the
    // loudest thing on a line that is not the news.
    store.sessions = [session({ status: "review_pending" })];
    render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    const word = await screen.findByText("review");
    // `text-faint`, and that is statustone.ts's whole rule: theme.ts answers
    // `text-ink` for the family it does not name, which is right inside a pill
    // and wrong for a bare word — an ink status beside a faint title would be
    // the loudest thing on a line that is not the news.
    expect(word.className).toContain("text-faint");
    expect(word.className).not.toContain("text-orange");
  });

  it("lets a pane error win the chip over the session's status", async () => {
    // The status describes the SESSION and the banner describes the PANE, and a
    // reader sees one screen. With the status branch first — and every session
    // the list still knows about taking it — the header could print "needs you"
    // directly above "This session's terminal is gone", which is the exact
    // contradiction humanError exists to remove. It also made the fallback
    // branch very nearly dead code.
    attaches = false;
    store.sessions = [session({ status: "needs_input" })];
    render(Terminal, { props: { onback: () => {} } });

    expect(await screen.findByText("no terminal")).toBeInTheDocument();
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

describe("Terminal context card", () => {
  // The strip between the tabs and the pane. Its whole design is about what it
  // does NOT draw, so most of what is pinned here is absence.

  it("draws no card at all when the session has nothing to say", async () => {
    // Not an empty card — no card. This is the commonest state in the app (no
    // PR, no interpreter judgement, no notification) and every row of chrome
    // above a terminal comes out of the pane. Queried by the element rather
    // than by its text, because an empty card has no text either and the two
    // states would be indistinguishable.
    store.sessions = [session({ status: "working" })];
    const { container } = render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(container.querySelector("[data-context-card]")).toBeNull();
  });

  it("marks the interpreter's headline as the approximation it is", async () => {
    // The "≈" is the TUI's statusPillFor marker and both list components repeat
    // it. It matters most here: the pane directly below is the deterministic
    // truth, so an LLM's guess printed above it without a mark reads as a
    // reading of that pane rather than as a judgement about it.
    store.sessions = [
      session({ headline: "running the migration tests", headlineAgo: "2m" }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    expect(
      await screen.findByText("≈ running the migration tests"),
    ).toBeInTheDocument();
    expect(screen.getByText("2m ago")).toBeInTheDocument();
  });

  it("leaves the agent's own notification unmarked, and unstamped", async () => {
    // A notification is the agent's own words, not an interpretation, so it
    // gets no "≈". And `headlineAgo` ages the JUDGEMENT — with no headline there
    // is nothing for it to be the age of, so the stamp stays away rather than
    // dating a sentence it does not describe.
    store.sessions = [
      session({
        lastNotification: "Claude needs your permission to run git push",
        headlineAgo: "2m",
      }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    expect(
      await screen.findByText("Claude needs your permission to run git push"),
    ).toBeInTheDocument();
    expect(screen.queryByText("2m ago")).toBeNull();
  });

  it("renders an activity line as text, never as markup", async () => {
    // Rule 6: headline and lastNotification are derived from pane text, which an
    // issue description or a dependency's README can write into. Svelte escapes
    // by default and this is the assertion that keeps it that way.
    store.sessions = [
      session({ lastNotification: "<img src=x onerror=alert(1)>done" }),
    ];
    const { container } = render(Terminal, { props: { onback: () => {} } });

    expect(
      await screen.findByText("<img src=x onerror=alert(1)>done"),
    ).toBeInTheDocument();
    expect(container.querySelector("img")).toBeNull();
  });

  it("states why the agent is blocked, in the shared vocabulary", async () => {
    // "needs you" is a status; "permission prompt" is an instruction, and the
    // difference is whether the reader has to open the pane to find out what is
    // being asked. The wording is theme.ts's `inputReasonLabel`.
    store.sessions = [
      session({ status: "needs_input", inputReason: "permission_prompt" }),
    ];
    render(Terminal, { props: { onback: () => {} } });

    expect(await screen.findByText("permission prompt")).toBeInTheDocument();
  });

  it("says nothing about an input reason the vocabulary retired", async () => {
    // `idle_notification` was 90% of the old needs_input traffic and 0% of its
    // questions; the daemon no longer files it under waiting_input at all, but a
    // snapshot written before that change still carries it. No chip beats an
    // explanation that is not true any more.
    store.sessions = [
      session({ status: "needs_input", inputReason: "idle_notification" }),
    ];
    const { container } = render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(container.querySelector("[data-context-card]")).toBeNull();
  });

  it("carries the checks rollup and the dev state as facts", async () => {
    store.sessions = [session({ checks: "pass", devActive: true })];
    render(Terminal, { props: { onback: () => {} } });

    expect(await screen.findByText("✓ CI pass")).toBeInTheDocument();
    expect(screen.getByText("dev running")).toBeInTheDocument();
  });

  it("draws no checks chip for a PR that has none", async () => {
    // "none" is what a repository with no checks configured reports. It is not
    // a fact about this session, and "no checks" reads as the app failing to
    // find them.
    store.sessions = [session({ prNumber: 401, checks: "none" })];
    const { container } = render(Terminal, { props: { onback: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(container.querySelector("[data-context-card]")).toBeNull();
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
