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

import { fireEvent, render, screen } from "@testing-library/svelte";
import { DEFAULT_THEME_ID, appearance } from "$lib/theme-runtime.svelte";
import { connection } from "@mobile/lib/connection.svelte";
import { router } from "@mobile/lib/router.svelte";
import { transport } from "@mobile/native";
import Settings from "./Settings.svelte";

beforeEach(() => {
  vi.clearAllMocks();
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
});
