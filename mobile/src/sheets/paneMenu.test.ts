import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import PaneTabs from "@mobile/lib/components/PaneTabs.svelte";
import PaneMenu from "./paneMenu.svelte";
import { router } from "@mobile/lib/router.svelte";
import { paneTabs } from "@mobile/lib/panetabs.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { bridge } from "@mobile/wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";

let ch: FakeChannel;
let panes: { name: string; kind: string; label: string }[];

beforeEach(async () => {
  globalThis.localStorage?.clear();
  panes = [
    { name: "lola-fe-42", kind: "agent", label: "agent" },
    { name: "lola-fe-42-shell-1", kind: "shell", label: "shell 1" },
  ];
  tiers.set(["read", "shell"]);
  router.stack = [
    { name: "tab", tab: "inbox" },
    { name: "session", id: "lola-fe-42", seg: "terminal" },
  ];
  ch = new FakeChannel();
  ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    const data =
      f.cmd === "panes" ? { session: "lola-fe-42", panes, canCreateShell: true } : {};
    ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: true, data } });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ daemonId: "d" });
  bridge.installTransport(t);
  // The strip loads the inventory the sheet reads.
  render(PaneTabs, { props: { session: "lola-fe-42", active: "lola-fe-42", onselect: () => {} } });
  await screen.findByRole("tab", { name: "Agent" });
});

afterEach(() => {
  router.close();
  tiers.clear();
  bridge.installTransport(null);
});

describe("paneMenu sheet", () => {
  it("is about the pane that was held", async () => {
    paneTabs.menuPane = "lola-fe-42-shell-1";
    router.open("paneMenu");
    render(PaneMenu);
    expect(await screen.findByRole("dialog", { name: "Options for Shell 1" })).toBeInTheDocument();
  });

  it("closes at once when asked for the agent pane, which has no menu", async () => {
    router.open("paneMenu");
    render(PaneMenu);
    await waitFor(() => expect(router.sheet).toBe(""));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("titles the sheet Rename, with the default name as placeholder and no tmux footnote", async () => {
    paneTabs.menuPane = "lola-fe-42-shell-1";
    router.open("paneMenu");
    render(PaneMenu);
    await screen.findByRole("dialog");
    expect(screen.getByText("Rename")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Shell 1")).toBeInTheDocument();
    expect(screen.queryByText("lola-fe-42-shell-1")).toBeNull();
  });

  it("offers no close without the shell tier, and no agent sentence for a shell", async () => {
    tiers.set(["read"]);
    paneTabs.menuPane = "lola-fe-42-shell-1";
    router.open("paneMenu");
    render(PaneMenu);
    await screen.findByRole("dialog");
    expect(screen.queryByRole("button", { name: "Close this pane" })).toBeNull();
    expect(screen.queryByText(/the session itself/i)).toBeNull();
  });

  it("closes itself when its pane leaves the inventory", async () => {
    paneTabs.menuPane = "lola-fe-42-shell-1";
    router.open("paneMenu");
    render(PaneMenu);
    await screen.findByRole("dialog");

    panes = panes.filter((p) => p.name !== "lola-fe-42-shell-1");
    await paneTabs.load("lola-fe-42");
    await waitFor(() => expect(router.sheet).toBe(""));
  });

  it("closes after a rename, and clears the model's menu pane when it goes", async () => {
    paneTabs.menuPane = "lola-fe-42-shell-1";
    router.open("paneMenu");
    const { unmount } = render(PaneMenu);
    await fireEvent.input(await screen.findByLabelText(/Name for this pane/i), {
      target: { value: "notes" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(router.sheet).toBe("");
    unmount();
    expect(paneTabs.menuPane).toBe("");
  });
});
