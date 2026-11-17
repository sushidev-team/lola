import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@mobile/native", () => ({
  transport: {
    status: vi.fn(async () => ({ epoch: 1, phase: "ready", relay: "none", framesIn: 0, framesOut: 0, bytesIn: 0, bytesOut: 0, launchedAt: 0 })),
    rename: vi.fn(async () => {}),
    forget: vi.fn(async () => {}),
    daemons: vi.fn(async () => ({ daemons: [] })),
    addListener: async () => ({ remove: async () => {} }),
  },
}));

const { HubStatus } = vi.hoisted(() => ({ HubStatus: vi.fn() }));
vi.mock("@mobile/wailsshim", () => ({ HubService: { Status: HubStatus } }));

import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import { DEFAULT_THEME_ID, appearance } from "$lib/theme-runtime.svelte";
import { connection } from "@mobile/lib/connection.svelte";
import { store } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";
import { transport } from "@mobile/native";
import Settings from "./Settings.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  store.status = null;
  appearance.preview({ theme: DEFAULT_THEME_ID, followSystem: false, lightTheme: "github-light", darkTheme: DEFAULT_THEME_ID });
  router.stack = [{ name: "tab", tab: "settings" }];
  router.sheet = "";
  connection.daemonId = "d1";
  connection.daemons = [{ daemonId: "d1", name: "Studio", pairedAt: 1, relay: "none", secureEnclave: false }];
  connection.phase = "ready";
  connection.path = { kind: "direct", interface: "wifi" };
  connection.device = { deviceId: "x", name: "Martin's iPhone", tiers: ["read"], host: "Studio" };
});

describe("Settings", () => {
  it("heads the Mac group with its name, labels the name row, and lists the phone in the first group", () => {
    render(Settings);
    expect(screen.getByRole("heading", { name: "Studio" })).toBeTruthy();
    expect(screen.getByText("Direct")).toBeTruthy();
    expect(screen.getByPlaceholderText("Studio")).toBeTruthy();
    expect(screen.queryByText("Mac name")).toBeNull();
    expect(screen.getByText("Name")).toBeTruthy();
    expect(screen.getByText("This phone")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "This phone" })).toBeNull();
    expect(screen.getByText("Martin's iPhone")).toBeTruthy();
    // The footnote form is gone everywhere.
    expect(screen.queryByText(/This phone:/)).toBeNull();
    expect(screen.getByRole("heading", { name: "Appearance" })).toBeTruthy();
  });

  it("shows the away-from-Wi-Fi row in the Mac group, with the Mac's reason", async () => {
    tiers.set(["read"]);
    HubStatus.mockResolvedValue({ enabled: false, joined: false, relay: { state: "off", reason: "not_joined", activeConns: 0, rendezvous: 0 } });
    render(Settings);
    expect(screen.getByText("Away from Wi-Fi")).toBeTruthy();
    await waitFor(() => expect(screen.getByText("Your Mac is not in a team hub.")).toBeTruthy());
  });

  it("hides name rows whose value is a placeholder", () => {
    connection.daemons = [];
    connection.device = null;
    render(Settings);
    expect(screen.queryByLabelText("Rename")).toBeNull();
    expect(screen.queryByText("This phone")).toBeNull();
    expect(screen.queryByText("your Mac")).toBeNull();
  });

  it("renames the Mac inline on Return", async () => {
    render(Settings);
    const input = screen.getByLabelText("Rename");
    await fireEvent.input(input, { target: { value: "Work" } });
    await fireEvent.submit(input.closest("form")!);
    await vi.waitFor(() => expect(transport.rename).toHaveBeenCalledWith({ daemonId: "d1", name: "Work" }));
  });

  it("opens the connection sheet and the unpair question", async () => {
    render(Settings);
    await fireEvent.click(screen.getByRole("button", { name: /Connection/ }));
    expect(router.sheet).toBe("connection");
    await fireEvent.click(screen.getByRole("button", { name: "Unpair Studio" }));
    expect(router.sheet).toBe("unpair");
  });

  it("expands the theme list inline and applies a flavor to this phone only", async () => {
    render(Settings);
    expect(screen.queryByRole("button", { name: /Latte/i })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: /Theme/ }));
    await fireEvent.click(screen.getByRole("button", { name: /Latte/i }));
    expect(appearance.id).toBe("catppuccin-latte");
    expect(router.top).toMatchObject({ name: "tab", tab: "settings" });
  });

  it("turns Match system on and off", async () => {
    render(Settings);
    await fireEvent.click(screen.getByRole("switch", { name: "Match iPhone" }));
    expect(appearance.preference.followSystem).toBe(true);
  });

  it("shows nothing for usage or health until the Mac has reported", () => {
    render(Settings);
    expect(screen.queryByRole("heading", { name: "Usage" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Mac health" })).toBeNull();
  });

  it("shows today's tokens, the subscription windows and their resets", () => {
    store.status = {
      runtimeOk: true,
      linearOk: true,
      polls: [],
      usage: {
        day: "d",
        tokens: 46_700_000,
        weighted: 38,
        todayUsd: 12,
        budgetTokens: 100,
        quotas: [{ agent: "claude", at: "x", windows: [{ label: "5h", usedPercent: 42, resetsAt: new Date(Date.now() + 2 * 3_600_000 + 60_000).toISOString() }] }],
      },
    };
    render(Settings);
    expect(screen.getByRole("heading", { name: "Usage" })).toBeTruthy();
    expect(screen.getByText("46.7M tokens")).toBeTruthy();
    expect(screen.getByText("38% of budget")).toBeTruthy();
    expect(screen.getByText("Claude 5-hour")).toBeTruthy();
    expect(screen.getByText("42%")).toBeTruthy();
    expect(screen.getByText(/Resets in 2h/)).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Mac health" })).toBeNull();
    expect(document.body.textContent).not.toContain("$");
  });

  it("shows a health group when the daemon reports runtime or Linear trouble", () => {
    store.status = { runtimeOk: false, runtimeErr: "tmux not found", linearOk: false, polls: [] };
    render(Settings);
    expect(screen.getByRole("heading", { name: "Mac health" })).toBeTruthy();
    expect(screen.getByText("tmux not found")).toBeTruthy();
    expect(screen.getByText("Agent runtime")).toBeTruthy();
    expect(screen.getByText("Linear")).toBeTruthy();
  });
});
