import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import PaneTabs from "./PaneTabs.svelte";
import PaneTabsHarness from "./PaneTabsHarness.test.svelte";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { bridge } from "@mobile/wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";
import { LONG_PRESS_MS } from "@mobile/lib/keygesture";
import { loadPaneLabels, savePaneLabel } from "@mobile/lib/prefs";

// The tab strip, driven over the ORDINARY request path: the same bridge, the
// same ChannelTransport and the same correlator the app uses on a device, with
// only the bytes faked. A component test that stubbed `DaemonService` instead
// would pass with a shim that sends the wrong command.
//
// Nothing here is a screenshot. What a strip LOOKS like is checked on the
// Simulator; what it DOES — which tabs, in which order, what "+" sends, and
// what a refusal says — is checked here, where there is no device and no
// pointer.

let ch: FakeChannel;

/** What the scripted daemon answers, per command. */
type Reply = { ok: true; data: unknown } | { ok: false; error: string };
let replies: Record<string, Reply>;

/** The inventory a session with two shells, a dev tab and a review pane has. */
function inventory(canCreateShell = true) {
  return {
    session: "lola-fe-42",
    panes: [
      { name: "lola-fe-42", kind: "agent", label: "agent" },
      { name: "lola-fe-42-shell-1", kind: "shell", label: "shell 1", index: 1 },
      { name: "lola-fe-42-shell-2", kind: "shell", label: "shell 2", index: 2 },
      { name: "lola-fe-42-dev-1", kind: "dev", label: "dev 1", index: 1 },
      { name: "lola-fe-42-review", kind: "review", label: "review" },
    ],
    review: { name: "lola-fe-42-review", kind: "review", label: "review" },
    canCreateShell,
  };
}

// ---------------------------------------------------------------------------
// The long press
// ---------------------------------------------------------------------------
//
// jsdom HAS NO `PointerEvent`, so `fireEvent.pointerDown(el, {clientX})` falls
// back to a plain Event and silently drops the coordinates -- which would make
// every drag look like a still finger and the "a scroll must not open the menu"
// test pass for the wrong reason. A MouseEvent carries clientX/clientY and is
// dispatched under whatever type it is given, so the handler sees exactly what
// a real pointer would.
function pointer(
  el: Element,
  type: string,
  x: number,
  y: number,
  over: { isPrimary?: boolean; button?: number } = {},
): Promise<boolean> {
  const e = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    clientX: x,
    clientY: y,
    button: over.button ?? 0,
  });
  // MouseEvent has no `isPrimary`, and the strip's guard is deliberately
  // written to treat a missing one as primary -- so it is only defined when a
  // test is about the second finger.
  if (over.isPrimary !== undefined) {
    Object.defineProperty(e, "isPrimary", { value: over.isPrimary });
  }
  return fireEvent(el, e);
}

/** Press, hold past the threshold, and lift -- the whole gesture. */
async function longPress(el: Element, x = 20, y = 20): Promise<void> {
  await pointer(el, "pointerdown", x, y);
  await vi.advanceTimersByTimeAsync(LONG_PRESS_MS + 10);
  await pointer(el, "pointerup", x, y);
}

/** The inventory after "shell 1" has been closed or has exited. */
function withoutShell1() {
  const inv = inventory();
  inv.panes = inv.panes.filter((p) => p.name !== "lola-fe-42-shell-1");
  return inv;
}

/** Every `req` frame this test produced, in order. */
function reqs(): Frame[] {
  return ch.sent.filter((f) => f.type === "req");
}

beforeEach(async () => {
  globalThis.localStorage?.clear();
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
});

afterEach(() => bridge.installTransport(null));

describe("PaneTabs", () => {
  it("draws every pane the daemon listed, in the daemon's own order", async () => {
    // The ORDER IS THE CONTRACT — agent, then shells and dev tabs by index,
    // then review — and it is derived from tmux on every call. A client that
    // re-sorts is a client that disagrees with the Mac about which tab is
    // which.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    const tabs = await screen.findAllByRole("tab");
    expect(tabs.map((t) => t.textContent?.trim())).toEqual([
      "agent",
      "shell 1",
      "shell 2",
      "dev 1",
      "review",
    ]);
  });

  it("marks the attached pane, and attaches another when it is tapped", async () => {
    replies.panes = { ok: true, data: inventory() };
    const onselect = vi.fn();
    render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42-shell-1", onselect },
    });

    const shell1 = await screen.findByRole("tab", { name: "shell 1" });
    expect(shell1).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "agent" })).toHaveAttribute(
      "aria-selected",
      "false",
    );

    await fireEvent.click(screen.getByRole("tab", { name: "review" }));
    expect(onselect).toHaveBeenCalledWith("lola-fe-42-review");
  });

  it("renders a kind it has never heard of as a plain tab", async () => {
    // A phone on the App Store outlives the Mac's daemon build, so a fifth pane
    // kind will eventually reach a client that has never seen one. Dropping it
    // would hide a pane that exists; the label is the daemon's and is all a tab
    // needs.
    replies.panes = {
      ok: true,
      data: {
        session: "lola-fe-42",
        panes: [
          { name: "lola-fe-42", kind: "agent", label: "agent" },
          { name: "lola-fe-42-audit-1", kind: "audit", label: "audit 1" },
        ],
        canCreateShell: true,
      },
    };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    const tabs = await screen.findAllByRole("tab");
    expect(tabs.map((t) => t.textContent?.trim())).toEqual([
      "agent",
      "audit 1",
    ]);
  });

  it("sends cmd=shellCreate with the session and attaches the pane the daemon named", async () => {
    // The client must NOT invent a name: the index is allocated daemon-side
    // because two phones and a desktop can be racing for "-shell-2".
    replies.panes = { ok: true, data: inventory() };
    replies.shellCreate = {
      ok: true,
      data: { session: "lola-fe-42", pane: "lola-fe-42-shell-3", index: 3 },
    };
    const onselect = vi.fn();
    render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect },
    });

    await screen.findByRole("tab", { name: "agent" });
    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));

    await waitFor(() =>
      expect(onselect).toHaveBeenCalledWith("lola-fe-42-shell-3"),
    );

    const created = reqs().filter((f) => f.cmd === "shellCreate");
    expect(created).toHaveLength(1);
    expect(created[0].payload).toEqual({
      cmd: "shellCreate",
      session: "lola-fe-42",
    });
    // No pane name on the wire, in any shape. This is the whole rule.
    expect(JSON.stringify(created[0].payload)).not.toContain("shell-3");
  });

  it("sends the phone's own size, so the shell is born at it rather than reflowed", async () => {
    // tmux gives an unattached session about 157x37. A tab created at that size
    // and pinned a moment later redraws itself line by line in front of the
    // user for several seconds; created at the phone's capacity there is
    // nothing to redraw.
    replies.panes = { ok: true, data: inventory() };
    replies.shellCreate = {
      ok: true,
      data: { session: "lola-fe-42", pane: "lola-fe-42-shell-3", index: 3 },
    };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        capacity: { cols: 52, rows: 24 },
        onselect: () => {},
      },
    });

    await screen.findByRole("tab", { name: "agent" });
    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));

    await waitFor(() =>
      expect(reqs().some((f) => f.cmd === "shellCreate")).toBe(true),
    );
    const created = reqs().filter((f) => f.cmd === "shellCreate");
    expect(created[0].payload).toEqual({
      cmd: "shellCreate",
      session: "lola-fe-42",
      cols: 52,
      rows: 24,
    });
  });

  it("omits a size it does not have, which is the daemon's old behaviour", async () => {
    replies.panes = { ok: true, data: inventory() };
    replies.shellCreate = {
      ok: true,
      data: { session: "lola-fe-42", pane: "lola-fe-42-shell-3", index: 3 },
    };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        capacity: { cols: 0, rows: 0 },
        onselect: () => {},
      },
    });

    await screen.findByRole("tab", { name: "agent" });
    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));

    await waitFor(() =>
      expect(reqs().some((f) => f.cmd === "shellCreate")).toBe(true),
    );
    const created = reqs().filter((f) => f.cmd === "shellCreate");
    expect(created[0].payload).toEqual({
      cmd: "shellCreate",
      session: "lola-fe-42",
    });
  });

  it("reloads the inventory after a shell is created", async () => {
    // The strip it drew a moment ago is now wrong by exactly one tab, and the
    // inventory is derived per call rather than pushed.
    replies.panes = { ok: true, data: inventory() };
    replies.shellCreate = {
      ok: true,
      data: { session: "lola-fe-42", pane: "lola-fe-42-shell-3", index: 3 },
    };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await screen.findByRole("tab", { name: "agent" });
    expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(1);

    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));
    await waitFor(() =>
      expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(2),
    );
  });

  it("marks the + busy while the create is in flight, without fading it", async () => {
    // The "+" is a real TouchButton rather than a hand-rolled square: the tabs
    // beside it are hand-rolled because Button sets `aria-pressed` for
    // `selected`, which is invalid on a role="tab", and that reason does not
    // reach an ordinary action outside the tablist. What the shared control
    // brings is asserted here — `aria-busy` and a disable that is NOT the 40%
    // fade of a dead control, which is the rule Button's `loading` exists for.
    //
    // The shellCreate frame is HELD rather than answered, because the state
    // under test only exists between the send and the reply. The default
    // harness answers synchronously, so there would be no such moment.
    replies.panes = { ok: true, data: inventory() };
    let held: Frame | undefined;
    const answerImmediately = ch.onSend!;
    ch.onSend = (f: Frame, c: FakeChannel) => {
      if (f.type === "req" && f.cmd === "shellCreate") {
        held = f;
        return;
      }
      answerImmediately(f, c);
    };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await screen.findByRole("tab", { name: "agent" });
    const plus = screen.getByRole("button", { name: "New shell" });
    await fireEvent.click(plus);

    await waitFor(() => expect(held).toBeDefined());
    await waitFor(() => expect(plus).toHaveAttribute("aria-busy", "true"));
    // Disabled, so the non-idempotent create cannot be fired twice...
    expect(plus).toBeDisabled();
    // ...but not wearing the fade of a control that cannot be used at all.
    expect(plus.className).toContain("disabled:opacity-100!");
  });

  it("surfaces a refusal in the daemon's own words", async () => {
    // "session X already has 16 shells, which is the cap" is the only
    // actionable half of that failure. A generic "could not start a shell"
    // throws it away and reads as a broken button.
    replies.panes = { ok: true, data: inventory() };
    replies.shellCreate = {
      ok: false,
      error: 'session "lola-fe-42" already has 16 shells, which is the cap',
    };
    const onnotice = vi.fn();
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
        onnotice,
      },
    });

    await screen.findByRole("tab", { name: "agent" });
    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));

    await waitFor(() =>
      expect(onnotice).toHaveBeenCalledWith(
        'session "lola-fe-42" already has 16 shells, which is the cap',
      ),
    );
  });

  it("disables the + when the daemon says no, and its name gives the reason", async () => {
    // `canCreateShell` is a single boolean because the daemon folds a cap and a
    // missing worktree into it, so the accessible name names both rather than
    // guessing — and never re-derives the answer from a count.
    replies.panes = { ok: true, data: inventory(false) };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await screen.findByRole("tab", { name: "agent" });
    const plus = screen.getByRole("button", {
      name: /not available for this session/i,
    });
    expect(plus).toBeDisabled();
    expect(plus).toHaveAccessibleName(
      /no worktree, or it has reached the shell limit/i,
    );

    await fireEvent.click(plus);
    expect(reqs().some((f) => f.cmd === "shellCreate")).toBe(false);
  });

  it("says so when the Mac's daemon has never heard of cmd=panes", async () => {
    // A phone outlives the Mac's daemon build: `cmd=panes` landed after the
    // rest of the remote protocol, so an older `lola run` answers `unknown cmd
    // "panes"`. This used to render nothing at all — which is indistinguishable
    // from a feature that is broken, and cost a reviewer a whole pass looking
    // for a strip that could not exist. It is a fixable misconfiguration, so it
    // gets a sentence.
    replies.panes = { ok: false, error: 'unknown cmd "panes"' };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    expect(
      await screen.findByText(/newer lola on the Mac/i),
    ).toBeInTheDocument();
    // No tabs to draw, and no live "+" either: cmd=shellCreate shipped in the
    // same commit, so a daemon that refuses one refuses the other. A button
    // whose only possible outcome is a second refusal is worse than a disabled
    // one that names the reason.
    expect(screen.queryByRole("tablist")).toBeNull();
    const plus = screen.getByRole("button", { name: /newer lola on the Mac/i });
    expect(plus).toBeDisabled();
  });

  it("stays silent for every other failure, which the terminal already explains", async () => {
    // A session that is gone is already stated by the terminal's own banner,
    // and a second sentence about it in the tab row is noise. Only the
    // capability gap draws.
    replies.panes = {
      ok: false,
      error: 'unknown_pane: session "lola-fe-42" is not available',
    };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await waitFor(() =>
      expect(reqs().some((f) => f.cmd === "panes")).toBe(true),
    );
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByText(/newer lola/i)).toBeNull();
  });

  it("walks the strip with the arrow keys, and wraps at neither end", async () => {
    // The tabs carry a roving tabindex — only the selected one is in the tab
    // order — which is the right pattern and was, on its own, a trap: with no
    // key handler beside it every other pane was unreachable by a hardware
    // keyboard, which iPads have and which this app already handles elsewhere.
    replies.panes = { ok: true, data: inventory() };
    const picked: string[] = [];
    const { rerender } = render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: (p: string) => picked.push(p),
      },
    });

    const agent = await screen.findByRole("tab", { name: "agent" });
    // At the left end, ArrowLeft moves nothing rather than wrapping round to
    // the review pane — a strip is a line, and wrapping surprises a walk.
    await fireEvent.keyDown(agent, { key: "ArrowLeft" });
    expect(picked).toEqual([]);

    await fireEvent.keyDown(agent, { key: "ArrowRight" });
    expect(picked).toEqual(["lola-fe-42-shell-1"]);

    // The parent owns `active`, exactly as it does for a tap.
    await rerender({
      session: "lola-fe-42",
      active: "lola-fe-42-shell-1",
      onselect: (p: string) => picked.push(p),
    });
    await fireEvent.keyDown(screen.getByRole("tab", { name: "shell 1" }), {
      key: "End",
    });
    expect(picked[picked.length - 1]).toBe("lola-fe-42-review");

    await fireEvent.keyDown(screen.getByRole("tab", { name: "shell 1" }), {
      key: "Home",
    });
    expect(picked[picked.length - 1]).toBe("lola-fe-42");
  });

  it("leaves modified arrow keys alone", async () => {
    // Cmd+Arrow and friends belong to the OS and to whatever the terminal below
    // does with them; a tab strip that swallowed them would be a strip that
    // steals a text-editing chord on an iPad keyboard.
    replies.panes = { ok: true, data: inventory() };
    const picked: string[] = [];
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: (p: string) => picked.push(p),
      },
    });

    const agent = await screen.findByRole("tab", { name: "agent" });
    await fireEvent.keyDown(agent, { key: "ArrowRight", metaKey: true });
    await fireEvent.keyDown(agent, { key: "ArrowRight", altKey: true });
    expect(picked).toEqual([]);
  });

  it("ties the tabs to the region they switch", async () => {
    // Without aria-controls a screen reader announces a tab list and,
    // separately, a terminal, with nothing saying the second is what the first
    // switches.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        panelId: "session-pane",
        onselect: () => {},
      },
    });

    const tab = await screen.findByRole("tab", { name: "agent" });
    expect(tab.getAttribute("aria-controls")).toBe("session-pane");
  });

  it("keeps the + reachable by pinning it outside the scroller", async () => {
    // The lesson the accessory bar's chevron already taught: an action that is
    // the last item of a scrolling strip is unreachable exactly when the strip
    // is long enough to need it.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    const plus = await screen.findByRole("button", { name: "New shell" });
    expect(screen.getByRole("tablist").contains(plus)).toBe(false);
  });

  it("asks about the session it was given, and re-asks when that changes", async () => {
    replies.panes = { ok: true, data: inventory() };
    const { rerender } = render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });
    await screen.findByRole("tab", { name: "agent" });
    expect(reqs()[reqs().length - 1].payload).toEqual({
      cmd: "panes",
      session: "lola-fe-42",
    });

    replies.panes = {
      ok: true,
      data: {
        session: "lola-be-7",
        panes: [{ name: "lola-be-7", kind: "agent", label: "agent" }],
        canCreateShell: true,
      },
    };
    await rerender({
      session: "lola-be-7",
      active: "lola-be-7",
      onselect: () => {},
    });

    await waitFor(() =>
      expect(
        reqs()
          .filter((f) => f.cmd === "panes")
          .map((f) => f.payload),
      ).toContainEqual({
        cmd: "panes",
        session: "lola-be-7",
      }),
    );
    await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(1));
  });
});

// ---------------------------------------------------------------------------
// A closed tab has to LEAVE
// ---------------------------------------------------------------------------
//
// DRIVEN THROUGH THE HARNESS, not through `rerender`. Testing Library's
// `rerender` re-runs the strip's load effect even for byte-identical props, so
// every assertion here would hold whether or not `refreshKey` were read -- and
// a strip that ignores it goes straight back to leaving a dead tab on screen
// forever, which is the whole bug. The harness bumps the counter with a button,
// through ordinary reactivity, and follows `onselect` the way the screen's
// `attach` does. See PaneTabsHarness.test.svelte.

/** Bump the counter the screen bumps when the pane stream reports an exit. */
function bump(): Promise<boolean> {
  return fireEvent.click(screen.getByRole("button", { name: "bump refresh" }));
}

describe("PaneTabs keeping up with panes that go", () => {
  it("re-reads the inventory when the screen bumps refreshKey, and drops the tab", async () => {
    // A shell that exits ENDS ITS TMUX SESSION -- shells get no
    // `remain-on-exit`, only dev tabs do, so that a crashed dev server stays
    // readable -- and `cmd=panes` derives from tmux, so the daemon has already
    // stopped listing it. The app used to fetch the list once and never again,
    // which left the dead tab on screen forever. The exit arrives on the pane
    // SUBSCRIPTION, which only the screen holds, so it is handed over as one
    // number rather than by giving the strip a second data source.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabsHarness);
    await screen.findByRole("tab", { name: "shell 1" });
    expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(1);

    replies.panes = { ok: true, data: withoutShell1() };
    await bump();

    await waitFor(() =>
      expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(2),
    );
    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: "shell 1" })).toBeNull(),
    );
  });

  it("does NOT poll", async () => {
    // The rejected alternative, pinned: a timer costs a tmux listing every few
    // seconds for a screen that is usually showing one pane and usually right.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      replies.panes = { ok: true, data: inventory() };
      render(PaneTabsHarness);
      await screen.findByRole("tab", { name: "agent" });
      await vi.advanceTimersByTimeAsync(60_000);
      expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("moves the user off the pane they were reading when it disappears, and says so", async () => {
    // Leaving a dead terminal under a live strip is the app claiming the tab
    // still means something. The replacement is the tab that INHERITED the
    // position -- same index, clamped -- so losing the second of several shells
    // lands on the shell that is now second, not at the far end of the strip.
    replies.panes = { ok: true, data: inventory() };
    const { component } = render(PaneTabsHarness, {
      props: { pane: "lola-fe-42-shell-1" },
    });
    await screen.findByRole("tab", { name: "shell 1" });
    expect(component.selected).toEqual([]);

    replies.panes = { ok: true, data: withoutShell1() };
    await bump();

    // "shell 1" sat at index 1; index 1 of what is left is "shell 2".
    await waitFor(() =>
      expect(component.selected).toEqual(["lola-fe-42-shell-2"]),
    );
    await waitFor(() =>
      expect(component.notices).toContain(
        "That pane closed. Showing shell 2 instead.",
      ),
    );
    // ...and the strip agrees, because the harness moved `active` the way the
    // screen's `attach` does.
    await waitFor(() =>
      expect(screen.getByRole("tab", { name: "shell 2" })).toHaveAttribute(
        "aria-selected",
        "true",
      ),
    );
  });

  it("leaves the user alone when some OTHER pane went", async () => {
    // A redrawn strip is fine; a navigation nobody asked for is not.
    replies.panes = { ok: true, data: inventory() };
    const { component } = render(PaneTabsHarness);
    await screen.findByRole("tab", { name: "shell 1" });

    replies.panes = { ok: true, data: withoutShell1() };
    await bump();

    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: "shell 1" })).toBeNull(),
    );
    expect(component.selected).toEqual([]);
  });

  it("navigates NOWHERE when the session has no panes left", async () => {
    // The last frame plus the terminal's own "this session ended" banner is the
    // only artefact left. Yanking the reader off it destroys the one thing
    // still worth reading, so an empty inventory moves nobody.
    replies.panes = { ok: true, data: inventory() };
    const { component } = render(PaneTabsHarness);
    await screen.findByRole("tab", { name: "agent" });

    replies.panes = {
      ok: true,
      data: { session: "lola-fe-42", panes: [], canCreateShell: false },
    };
    await bump();

    await waitFor(() => expect(screen.queryByRole("tab")).toBeNull());
    expect(component.selected).toEqual([]);
    expect(component.notices).toEqual([]);
  });

  it("reports the live inventory, which is what retires a stray size pin", async () => {
    // The pin holds somebody's Mac window at phone size and can only hand it
    // back by naming the pane; a release of a pane whose tmux window is already
    // gone is REFUSED by the daemon, so without an authoritative "these are the
    // panes that exist" the app warns forever about a window that is not
    // squashed. This strip is the only thing that asks.
    replies.panes = { ok: true, data: inventory() };
    const h = render(PaneTabsHarness);
    const inv = (
      h.component as unknown as {
        inventories: { session: string; names: string[] }[];
      }
    ).inventories;
    await screen.findByRole("tab", { name: "shell 1" });
    expect(inv).toEqual([
      {
        session: "lola-fe-42",
        names: [
          "lola-fe-42",
          "lola-fe-42-shell-1",
          "lola-fe-42-shell-2",
          "lola-fe-42-dev-1",
          "lola-fe-42-review",
        ],
      },
    ]);

    replies.panes = { ok: true, data: withoutShell1() };
    await bump();
    await waitFor(() => expect(inv).toHaveLength(2));
    expect(inv[1]?.names).not.toContain("lola-fe-42-shell-1");
  });

  it("reports NOTHING when the inventory could not be read", async () => {
    // A refusal means the inventory is unknown, not empty -- and "no panes
    // exist" is the one answer that must never be inferred from a failure, or
    // every outstanding pin would be retired on a blip and its window left
    // squashed with no record.
    replies.panes = { ok: true, data: inventory() };
    const h = render(PaneTabsHarness);
    const inv = (
      h.component as unknown as {
        inventories: { session: string; names: string[] }[];
      }
    ).inventories;
    await screen.findByRole("tab", { name: "shell 1" });
    expect(inv).toHaveLength(1);

    replies.panes = { ok: false, error: "no such session" };
    await bump();
    await waitFor(() =>
      expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(2),
    );
    expect(inv).toHaveLength(1);
  });

  it("drops an inventory answer that a newer one has already overtaken", async () => {
    // TWO LOADS FOR ONE SESSION OVERLAP ROUTINELY -- a reconnect landing beside
    // a refreshKey bump, a create followed by an exit -- and nothing makes a
    // socket answer them in order. The session guard cannot see this: both
    // answers are for the right session. The older one arriving last redrew the
    // strip from a list that predates a shell it does not mention, resurrecting
    // a tab the daemon had killed, pruning that shell's nickname, and telling
    // the size pin a pane it is holding no longer exists -- which retires the
    // record of a window that is genuinely squashed.
    const held: Frame[] = [];
    ch.onSend = (f: Frame) => {
      if (f.type !== "req") return;
      held.push(f);
    };
    const h = render(PaneTabsHarness);
    const inv = (
      h.component as unknown as {
        inventories: { session: string; names: string[] }[];
      }
    ).inventories;

    await waitFor(() => expect(held).toHaveLength(1));
    await bump();
    await waitFor(() => expect(held).toHaveLength(2));

    // The NEWER request answers first, with the shell gone...
    ch.deliver({
      v: 1,
      type: FRAME_RESP,
      id: held[1]!.id,
      payload: { ok: true, data: withoutShell1() },
    });
    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: "shell 1" })).toBeNull(),
    );

    // ...and the older one lands afterwards, still listing it.
    ch.deliver({
      v: 1,
      type: FRAME_RESP,
      id: held[0]!.id,
      payload: { ok: true, data: inventory() },
    });
    await new Promise((r) => setTimeout(r, 20));

    expect(screen.queryByRole("tab", { name: "shell 1" })).toBeNull();
    // And nothing was reported that would retire a live pin.
    expect(inv).toHaveLength(1);
    expect(inv[0]?.names).not.toContain("lola-fe-42-shell-1");
  });

  it("does not move anyone when the inventory could not be read at all", async () => {
    // A refused `cmd=panes` means the inventory is UNKNOWN, not empty. Treating
    // it as empty would move a user off a perfectly live pane because the
    // socket hiccupped.
    replies.panes = { ok: true, data: inventory() };
    const { component } = render(PaneTabsHarness, {
      props: { pane: "lola-fe-42-shell-1" },
    });
    await screen.findByRole("tab", { name: "shell 1" });

    replies.panes = { ok: false, error: "not connected" };
    await bump();

    await waitFor(() =>
      expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(2),
    );
    expect(component.selected).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// The long press
// ---------------------------------------------------------------------------

describe("PaneTabs long-press menu", () => {
  beforeEach(() => {
    // `shouldAdvanceTime` so the daemon round trips and testing-library's own
    // waits still run on real time while the hold timer can be jumped.
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => vi.useRealTimers());

  it("opens the menu for the tab that was held", async () => {
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await longPress(await screen.findByRole("tab", { name: "shell 2" }));

    const sheet = await screen.findByRole("dialog");
    expect(sheet).toHaveAccessibleName("Options for shell 2");
    // The tmux name is on screen: it is the identity every command sends, and
    // it is what somebody at the Mac can actually see.
    expect(screen.getByText("lola-fe-42-shell-2")).toBeInTheDocument();
  });

  it("does NOT open it when the press turns into a scroll", async () => {
    // THE HAZARD THIS EXISTS FOR. The strip is `overflow-x-auto` with
    // wall-to-wall tabs, so a sideways swipe to reach the review tab
    // necessarily begins on some other tab. The accessory bar learned this the
    // hard way -- a drag begun on its ^Z key suspended a live Claude Code
    // session -- and its 8px gate is reused rather than a second one written.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    const tab = await screen.findByRole("tab", { name: "shell 2" });
    await pointer(tab, "pointerdown", 20, 20);
    await pointer(tab, "pointermove", 60, 22); // well past KEY_SLOP_PX
    await vi.advanceTimersByTimeAsync(LONG_PRESS_MS + 10);
    await pointer(tab, "pointerup", 60, 22);

    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("still opens after a wobble smaller than the slop", async () => {
    // A finger on glass is never perfectly still, and a gate that fired on
    // jitter would make the menu unreachable for anybody but a robot.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    const tab = await screen.findByRole("tab", { name: "shell 2" });
    await pointer(tab, "pointerdown", 20, 20);
    await pointer(tab, "pointermove", 23, 21);
    await vi.advanceTimersByTimeAsync(LONG_PRESS_MS + 10);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("does not also select the tab it was held on", async () => {
    // A real lift still produces a click. Without the suppression a long press
    // would open the menu AND attach the pane behind it.
    replies.panes = { ok: true, data: inventory() };
    const onselect = vi.fn();
    render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect },
    });

    const tab = await screen.findByRole("tab", { name: "shell 2" });
    await longPress(tab);
    await fireEvent.click(tab);

    expect(onselect).not.toHaveBeenCalled();
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("lets an ordinary tap through unchanged", async () => {
    replies.panes = { ok: true, data: inventory() };
    const onselect = vi.fn();
    render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect },
    });

    const tab = await screen.findByRole("tab", { name: "shell 2" });
    await pointer(tab, "pointerdown", 20, 20);
    await pointer(tab, "pointerup", 20, 20);
    await fireEvent.click(tab);

    expect(onselect).toHaveBeenCalledWith("lola-fe-42-shell-2");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("taps normally on the NEXT press after a menu was dismissed", async () => {
    // The suppression is cleared by the next press rather than by the click it
    // swallows: the menu is usually dismissed on the sheet's backdrop, so that
    // click never arrives and a flag cleared only there would eat the following
    // genuine tap.
    replies.panes = { ok: true, data: inventory() };
    const onselect = vi.fn();
    render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect },
    });

    const tab = await screen.findByRole("tab", { name: "shell 2" });
    await longPress(tab);
    await fireEvent.click(
      await screen.findByRole("button", { name: "Close the pane menu" }),
    );

    await pointer(tab, "pointerdown", 20, 20);
    await pointer(tab, "pointerup", 20, 20);
    await fireEvent.click(tab);
    expect(onselect).toHaveBeenCalledWith("lola-fe-42-shell-2");
  });

  it("ignores a SECOND finger, so one thumb cannot hijack the other's press", async () => {
    // Two thumbs on a strip that scrolls sideways is ordinary. The second
    // pointer used to replace the first one's gesture wholesale -- clearing the
    // pending hold and resetting the click suppression, so the first finger's
    // lift SELECTED the tab whose menu it had just opened.
    replies.panes = { ok: true, data: inventory() };
    const onselect = vi.fn();
    render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect },
    });

    const held = await screen.findByRole("tab", { name: "shell 2" });
    await pointer(held, "pointerdown", 20, 20, { isPrimary: true });
    await vi.advanceTimersByTimeAsync(LONG_PRESS_MS + 10);
    await screen.findByRole("dialog");

    // A second finger lands elsewhere while the first is still down.
    await pointer(
      await screen.findByRole("tab", { name: "shell 1" }),
      "pointerdown",
      120,
      20,
      { isPrimary: false },
    );
    await pointer(held, "pointerup", 20, 20, { isPrimary: true });
    await fireEvent.click(held);

    expect(onselect).not.toHaveBeenCalled();
  });

  it("does not swallow the first KEYBOARD activation after a menu", async () => {
    // The click suppression is armed by a hold and was cleared only by the next
    // press. Enter on a focused tab produces a click with no pointer before it,
    // so the first keystroke after any long press went nowhere.
    replies.panes = { ok: true, data: inventory() };
    const onselect = vi.fn();
    render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect },
    });

    const tab = await screen.findByRole("tab", { name: "shell 2" });
    await longPress(tab);
    await screen.findByRole("dialog");
    // The lift's own click is the one the hold owes an answer to.
    await fireEvent.click(tab);
    expect(onselect).not.toHaveBeenCalled();

    // Anything after it is the user's.
    await fireEvent.click(tab);
    expect(onselect).toHaveBeenCalledWith("lola-fe-42-shell-2");
  });

  it("abandons a press when the strip is torn down under it", async () => {
    // The timer assigns `menuPane`, which the terminal screen binds to `nav` --
    // so a press begun a moment before Back opened a pane sheet over the
    // sessions list, for a pane on a screen that no longer existed.
    replies.panes = { ok: true, data: inventory() };
    const h = render(PaneTabsHarness);
    const menu = (h.component as unknown as { menu: { pane: string } }).menu;

    await pointer(
      await screen.findByRole("tab", { name: "shell 2" }),
      "pointerdown",
      20,
      20,
    );
    h.unmount();
    await vi.advanceTimersByTimeAsync(LONG_PRESS_MS + 50);

    expect(menu.pane).toBe("");
  });

  it("opens from the keyboard too, which cannot make a long press at all", async () => {
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    const agent = await screen.findByRole("tab", { name: "agent" });
    await fireEvent.keyDown(agent, { key: "ContextMenu" });
    expect(await screen.findByRole("dialog")).toHaveAccessibleName(
      "Options for agent",
    );
  });
});

// ---------------------------------------------------------------------------
// Closing a pane
// ---------------------------------------------------------------------------

describe("PaneTabs closing a pane", () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => vi.useRealTimers());

  it("sends cmd=paneClose in its args envelope and re-reads what is left", async () => {
    // `paneClose` takes a NESTED `args` object, unlike `panes` and
    // `shellCreate`, because its Go handler is declared over
    // `protocol.PaneCloseArgs` rather than over the request's top-level fields.
    // And the reload is what makes the close honest: the tabs are drawn from
    // `cmd=panes`, so without it the strip keeps showing a tab the daemon has
    // already killed.
    replies.panes = { ok: true, data: inventory() };
    replies.paneClose = {
      ok: true,
      data: { session: "lola-fe-42", pane: "lola-fe-42-shell-1", closed: true },
    };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await longPress(await screen.findByRole("tab", { name: "shell 1" }));
    replies.panes = { ok: true, data: withoutShell1() };
    await fireEvent.click(
      await screen.findByRole("button", { name: "Close this pane" }),
    );

    await waitFor(() =>
      expect(reqs().some((f) => f.cmd === "paneClose")).toBe(true),
    );
    const sent = reqs().filter((f) => f.cmd === "paneClose");
    expect(sent).toHaveLength(1);
    expect(sent[0].payload).toEqual({
      cmd: "paneClose",
      args: { session: "lola-fe-42", pane: "lola-fe-42-shell-1" },
    });

    await waitFor(() =>
      expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(2),
    );
    await waitFor(() =>
      expect(screen.queryByRole("tab", { name: "shell 1" })).toBeNull(),
    );
    // The menu cannot outlive the tab it was about.
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("offers NO close on the agent tab, and names what does end a session", async () => {
    // `handlePaneClose` refuses the agent pane outright, because that pane IS
    // the session -- closing it would end the work and leave a record pointing
    // at nothing. A control whose only possible outcome is a refusal is worse
    // than an absent one, so the button is not drawn at all.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await longPress(await screen.findByRole("tab", { name: "agent" }));

    await screen.findByRole("dialog");
    expect(
      screen.queryByRole("button", { name: "Close this pane" }),
    ).toBeNull();
    expect(screen.getByText(/cannot be closed here/i)).toBeInTheDocument();
    expect(screen.getByText(/lola kill/)).toBeInTheDocument();
  });

  it("draws the close as destructive AT REST, not only on hover", async () => {
    // The shared Button's `danger` variant is `text-faint` until hovered, which
    // is a desktop affordance and is worth nothing on a phone -- it rendered
    // this row in the same grey as "Done". The trailing `!` is the rule
    // CLAUDE.md states: a plain `text-bad` ties with the variant's own
    // `text-faint` and the winner would be whatever order Tailwind compiled.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await longPress(await screen.findByRole("tab", { name: "shell 1" }));
    const close = await screen.findByRole("button", {
      name: "Close this pane",
    });
    expect(close.className).toContain("text-bad!");
  });

  it("still offers a close on a pane kind it has never heard of", async () => {
    // A phone on the App Store outlives the Mac's daemon build. An unrecognised
    // kind is definitionally NOT the agent pane, so withholding the control
    // there would hide a working action; the daemon is the one that refuses.
    replies.panes = {
      ok: true,
      data: {
        session: "lola-fe-42",
        panes: [
          { name: "lola-fe-42", kind: "agent", label: "agent" },
          { name: "lola-fe-42-audit-1", kind: "audit", label: "audit 1" },
        ],
        canCreateShell: true,
      },
    };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await longPress(await screen.findByRole("tab", { name: "audit 1" }));
    expect(
      await screen.findByRole("button", { name: "Close this pane" }),
    ).toBeInTheDocument();
  });

  it("surfaces a refusal in the daemon's own words", async () => {
    // "pane X does not belong to session Y" is actionable; "could not close
    // that pane" is not, and reads as a broken button.
    replies.panes = { ok: true, data: inventory() };
    replies.paneClose = {
      ok: false,
      error:
        'pane "lola-fe-42-shell-1" does not belong to session "lola-fe-42"',
    };
    const onnotice = vi.fn();
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
        onnotice,
      },
    });

    await longPress(await screen.findByRole("tab", { name: "shell 1" }));
    await fireEvent.click(
      await screen.findByRole("button", { name: "Close this pane" }),
    );

    await waitFor(() =>
      expect(onnotice).toHaveBeenCalledWith(
        'pane "lola-fe-42-shell-1" does not belong to session "lola-fe-42"',
      ),
    );
  });

  it("moves the user off the pane they just closed, without narrating it", async () => {
    // They pressed the button; being told that the pane they closed has closed
    // is noise, and landing on the neighbour is self-evidently what they did.
    replies.panes = { ok: true, data: inventory() };
    replies.paneClose = {
      ok: true,
      data: { session: "lola-fe-42", pane: "lola-fe-42-shell-1", closed: true },
    };
    const onselect = vi.fn();
    const onnotice = vi.fn();
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42-shell-1",
        onselect,
        onnotice,
      },
    });

    await longPress(await screen.findByRole("tab", { name: "shell 1" }));
    replies.panes = { ok: true, data: withoutShell1() };
    await fireEvent.click(
      await screen.findByRole("button", { name: "Close this pane" }),
    );

    await waitFor(() =>
      expect(onselect).toHaveBeenCalledWith("lola-fe-42-shell-2"),
    );
    expect(onnotice).not.toHaveBeenCalledWith(
      expect.stringContaining("That pane closed"),
    );
  });
});

// ---------------------------------------------------------------------------
// Renaming, which never leaves the phone
// ---------------------------------------------------------------------------

describe("PaneTabs renaming a tab", () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => vi.useRealTimers());

  it("draws the nickname, keeps the daemon's name in the accessible one, and stores it", async () => {
    // The tmux name is the pane's IDENTITY -- the daemon anchors on it, parses
    // an index out of it and matches its suffix at teardown -- so a rename is a
    // nickname on this device and nothing more. The accessible name carries
    // both, because the nickname is this phone's and the label is what anybody
    // at the Mac can see.
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await longPress(await screen.findByRole("tab", { name: "shell 2" }));
    await fireEvent.input(screen.getByLabelText(/Name for this pane/i), {
      target: { value: "notes" },
    });
    await fireEvent.click(
      screen.getByRole("button", { name: "Save the name" }),
    );

    const tab = await screen.findByRole("tab", { name: "notes (shell 2)" });
    expect(tab.textContent?.trim()).toBe("notes");
    expect(loadPaneLabels()).toEqual({ "lola-fe-42-shell-2": "notes" });
    // NOTHING on the wire. This is the whole rule.
    expect(reqs().some((f) => f.cmd === "paneRename")).toBe(false);
    expect(JSON.stringify(reqs().map((f) => f.payload))).not.toContain("notes");
  });

  it("shows a nickname that was stored before this mount", async () => {
    savePaneLabel("lola-fe-42-shell-2", "notes");
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    const tabs = await screen.findAllByRole("tab");
    expect(tabs.map((t) => t.textContent?.trim())).toEqual([
      "agent",
      "shell 1",
      "notes",
      "dev 1",
      "review",
    ]);
  });

  it("gives the tab its daemon name back", async () => {
    savePaneLabel("lola-fe-42-shell-2", "notes");
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await longPress(
      await screen.findByRole("tab", { name: "notes (shell 2)" }),
    );
    await fireEvent.click(
      await screen.findByRole("button", { name: "Use the default name" }),
    );

    await screen.findByRole("tab", { name: "shell 2" });
    expect(loadPaneLabels()).toEqual({});
  });

  it("offers no undo on a tab that was never renamed", async () => {
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabs, {
      props: {
        session: "lola-fe-42",
        active: "lola-fe-42",
        onselect: () => {},
      },
    });

    await longPress(await screen.findByRole("tab", { name: "shell 2" }));
    await screen.findByRole("dialog");
    expect(
      screen.queryByRole("button", { name: "Use the default name" }),
    ).toBeNull();
    // And nothing to save until something is typed.
    expect(
      screen.getByRole("button", { name: "Save the name" }),
    ).toBeDisabled();
  });

  it("FORGETS a nickname when its pane disappears", async () => {
    // The daemon allocates the lowest free shell index, so the next
    // `shellCreate` after a close reuses the name that just went. Without this
    // the shell somebody opens tomorrow comes up called "notes".
    savePaneLabel("lola-fe-42-shell-1", "notes");
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabsHarness);
    await screen.findByRole("tab", { name: "notes (shell 1)" });

    replies.panes = { ok: true, data: withoutShell1() };
    await bump();

    await waitFor(() => expect(loadPaneLabels()).toEqual({}));
  });

  it("keeps every nickname when the inventory could not be read", async () => {
    // A refused or unsupported `cmd=panes` means the inventory is UNKNOWN, not
    // empty. Pruning against nothing would wipe every nickname on the phone the
    // moment the Mac's daemon was a build too old.
    savePaneLabel("lola-fe-42-shell-1", "notes");
    replies.panes = { ok: true, data: inventory() };
    render(PaneTabsHarness);
    await screen.findByRole("tab", { name: "notes (shell 1)" });

    replies.panes = { ok: false, error: 'unknown cmd "panes"' };
    await bump();

    await screen.findByText(/newer lola on the Mac/i);
    expect(loadPaneLabels()).toEqual({ "lola-fe-42-shell-1": "notes" });
  });
});
