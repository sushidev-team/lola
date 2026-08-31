import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import PaneTabs from "./PaneTabs.svelte";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { bridge } from "@mobile/wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";

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

/** Every `req` frame this test produced, in order. */
function reqs(): Frame[] {
  return ch.sent.filter((f) => f.type === "req");
}

beforeEach(async () => {
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
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });

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
    expect(screen.getByRole("tab", { name: "agent" })).toHaveAttribute("aria-selected", "false");

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
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });

    const tabs = await screen.findAllByRole("tab");
    expect(tabs.map((t) => t.textContent?.trim())).toEqual(["agent", "audit 1"]);
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
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect } });

    await screen.findByRole("tab", { name: "agent" });
    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));

    await waitFor(() => expect(onselect).toHaveBeenCalledWith("lola-fe-42-shell-3"));

    const created = reqs().filter((f) => f.cmd === "shellCreate");
    expect(created).toHaveLength(1);
    expect(created[0].payload).toEqual({ cmd: "shellCreate", session: "lola-fe-42" });
    // No pane name on the wire, in any shape. This is the whole rule.
    expect(JSON.stringify(created[0].payload)).not.toContain("shell-3");
  });

  it("reloads the inventory after a shell is created", async () => {
    // The strip it drew a moment ago is now wrong by exactly one tab, and the
    // inventory is derived per call rather than pushed.
    replies.panes = { ok: true, data: inventory() };
    replies.shellCreate = {
      ok: true,
      data: { session: "lola-fe-42", pane: "lola-fe-42-shell-3", index: 3 },
    };
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(1);

    await fireEvent.click(screen.getByRole("button", { name: "New shell" }));
    await waitFor(() => expect(reqs().filter((f) => f.cmd === "panes")).toHaveLength(2));
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
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });

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
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {}, onnotice },
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
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });

    await screen.findByRole("tab", { name: "agent" });
    const plus = screen.getByRole("button", { name: /not available for this session/i });
    expect(plus).toBeDisabled();
    expect(plus).toHaveAccessibleName(/no worktree, or it has reached the shell limit/i);

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
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });

    expect(await screen.findByText(/newer lola on the Mac/i)).toBeInTheDocument();
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
    replies.panes = { ok: false, error: 'unknown_pane: session "lola-fe-42" is not available' };
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });

    await waitFor(() => expect(reqs().some((f) => f.cmd === "panes")).toBe(true));
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
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect: (p: string) => picked.push(p) },
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
    await fireEvent.keyDown(screen.getByRole("tab", { name: "shell 1" }), { key: "End" });
    expect(picked[picked.length - 1]).toBe("lola-fe-42-review");

    await fireEvent.keyDown(screen.getByRole("tab", { name: "shell 1" }), { key: "Home" });
    expect(picked[picked.length - 1]).toBe("lola-fe-42");
  });

  it("leaves modified arrow keys alone", async () => {
    // Cmd+Arrow and friends belong to the OS and to whatever the terminal below
    // does with them; a tab strip that swallowed them would be a strip that
    // steals a text-editing chord on an iPad keyboard.
    replies.panes = { ok: true, data: inventory() };
    const picked: string[] = [];
    render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect: (p: string) => picked.push(p) },
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
    render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });

    const plus = await screen.findByRole("button", { name: "New shell" });
    expect(screen.getByRole("tablist").contains(plus)).toBe(false);
  });

  it("asks about the session it was given, and re-asks when that changes", async () => {
    replies.panes = { ok: true, data: inventory() };
    const { rerender } = render(PaneTabs, {
      props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} },
    });
    await screen.findByRole("tab", { name: "agent" });
    expect(reqs()[reqs().length - 1].payload).toEqual({ cmd: "panes", session: "lola-fe-42" });

    replies.panes = {
      ok: true,
      data: {
        session: "lola-be-7",
        panes: [{ name: "lola-be-7", kind: "agent", label: "agent" }],
        canCreateShell: true,
      },
    };
    await rerender({ session: "lola-be-7", active: "lola-be-7", onselect: () => {} });

    await waitFor(() =>
      expect(reqs().filter((f) => f.cmd === "panes").map((f) => f.payload)).toContainEqual({
        cmd: "panes",
        session: "lola-be-7",
      }),
    );
    await waitFor(() => expect(screen.getAllByRole("tab")).toHaveLength(1));
  });
});
