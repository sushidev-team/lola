import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
const { setup, startDaemon, validate, themes, appearanceInit, store, doctorRun, openHelp, openURL } = vi.hoisted(() => ({
  setup: vi.fn(), startDaemon: vi.fn(), validate: vi.fn(), themes: vi.fn(), appearanceInit: vi.fn(),
  doctorRun: vi.fn(), openHelp: vi.fn(), openURL: vi.fn(),
  store: { hasConfig: false, alive: false, projects: [], startDaemon: vi.fn(), setFlash: vi.fn(), openURL: vi.fn() },
}));
vi.mock("@bindings/desktop", () => ({
  ConfigService: { Setup: setup, ValidateLinearKey: validate, Themes: themes, OpenLinearKeyHelp: openHelp },
  DoctorService: { Run: doctorRun },
}));
vi.mock("$lib/store.svelte", () => ({ store }));
vi.mock("$lib/theme-runtime.svelte", async () => {
  const palettes = await import("$lib/catppuccin");
  return { ...palettes, appearance: { init: appearanceInit } };
});
import { DEFAULT_THEME_ID } from "$lib/catppuccin";
import Setup from "./Setup.svelte";

beforeEach(() => {
  vi.resetAllMocks(); store.hasConfig = false; store.alive = false; store.startDaemon = startDaemon; store.openURL = openURL;
  doctorRun.mockResolvedValue({ results: [
    { id: "tmux", name: "tmux", ok: true, detail: "", critical: true },
    { id: "git", name: "git", ok: true, detail: "", critical: true },
  ], summary: "", ok: true });
  openHelp.mockResolvedValue(undefined);
  setup.mockResolvedValue({ keychainStored: true });
  appearanceInit.mockResolvedValue(undefined);
  themes.mockResolvedValue([DEFAULT_THEME_ID, "catppuccin-mocha", "catppuccin-latte"]);
});
afterEach(cleanup);

const enterKey = () => fireEvent.input(screen.getByLabelText("Linear API key"), { target: { value: "test-key" } });
const start = () => screen.getByRole("button", { name: "Start Lola" });
const defaults = { linearKey: "test-key", agent: "claude", theme: DEFAULT_THEME_ID, concurrencyCap: 2, globalCap: 4, pollInterval: "60s" };

describe("first-run setup", () => {
  it("requires only a Linear key and opens an empty workspace", async () => {
    render(Setup);
    expect(start()).toBeDisabled();
    expect(screen.queryByLabelText("Project path")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Choose folder…" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("Linear API key")).toHaveAttribute("type", "password");
    await enterKey(); expect(start()).toBeEnabled();
    await fireEvent.click(start());
    await waitFor(() => expect(setup).toHaveBeenCalledWith(defaults));
    expect(validate).toHaveBeenCalledWith("test-key");
    await waitFor(() => expect(startDaemon).toHaveBeenCalledOnce());
    expect(store.hasConfig).toBe(true);
    expect(appearanceInit).toHaveBeenCalledOnce();
    expect(screen.getByLabelText("Linear API key")).toHaveValue("");
  });

  it("opens the workspace even if the theme bridge never replies", async () => {
    appearanceInit.mockImplementationOnce(() => new Promise<void>(() => {}));
    render(Setup); await enterKey(); await fireEvent.click(start());
    await waitFor(() => expect(startDaemon).toHaveBeenCalledOnce());
    expect(store.hasConfig).toBe(true);
  });

  it("keeps saved setup complete if the theme refresh fails", async () => {
    appearanceInit.mockRejectedValueOnce(new Error("Theme bridge unavailable"));
    render(Setup); await enterKey(); await fireEvent.click(start());
    await waitFor(() => expect(startDaemon).toHaveBeenCalledOnce());
    expect(store.hasConfig).toBe(true);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("saves optional general settings", async () => {
    render(Setup); await enterKey();
    await fireEvent.change(screen.getByLabelText("Default agent"), { target: { value: "codex" } });
    await waitFor(() => expect(screen.getByRole("option", { name: "Latte" })).toBeInTheDocument());
    await fireEvent.change(screen.getByLabelText("Theme"), { target: { value: "catppuccin-latte" } });
    await fireEvent.input(screen.getByLabelText("Agents per project"), { target: { value: "3" } });
    await fireEvent.input(screen.getByLabelText("Total running agents"), { target: { value: "6" } });
    await fireEvent.change(screen.getByLabelText("Poll interval"), { target: { value: "2" } });
    await fireEvent.click(start());
    await waitFor(() => expect(setup).toHaveBeenCalledWith({ ...defaults, agent: "codex", theme: "catppuccin-latte", concurrencyCap: 3, globalCap: 6, pollInterval: "2m" }));
  });

  it("uses defaults if optional limits are cleared", async () => {
    render(Setup); await enterKey();
    for (const label of ["Agents per project", "Total running agents"]) {
      await fireEvent.input(screen.getByLabelText(label), { target: { value: "" } });
    }
    expect(start()).toBeEnabled(); await fireEvent.click(start());
    await waitFor(() => expect(setup).toHaveBeenCalledWith(defaults));
  });

  it.each(["0", "-1", "1.5"])("rejects invalid agent limit %s", async (value) => {
    render(Setup); await enterKey();
    await fireEvent.input(screen.getByLabelText("Total running agents"), { target: { value } });
    expect(start()).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent(/whole numbers/);
    expect(setup).not.toHaveBeenCalled();
  });

  it("keeps the key and general settings available after a failed save", async () => {
    setup.mockRejectedValueOnce(new Error("Configuration is not writable"));
    render(Setup); await enterKey(); await fireEvent.click(start());
    expect(await screen.findByRole("alert")).toHaveTextContent(/not writable/);
    expect(store.hasConfig).toBe(false);
    expect(startDaemon).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Linear API key")).toHaveValue("test-key");
    expect(start()).toBeEnabled();
  });

  it("rejects an invalid key before saving or starting", async () => {
    validate.mockRejectedValueOnce(new Error("Invalid Linear key"));
    render(Setup); await enterKey(); await fireEvent.click(start());
    expect(await screen.findByRole("alert")).toHaveTextContent(/Invalid Linear key/);
    expect(setup).not.toHaveBeenCalled(); expect(startDaemon).not.toHaveBeenCalled();
  });

  it("uses a successful explicit validation without repeating it", async () => {
    render(Setup); await enterKey();
    await fireEvent.click(screen.getByRole("button", { name: "Validate" }));
    await screen.findByText("Key is valid."); await fireEvent.click(start());
    await waitFor(() => expect(setup).toHaveBeenCalledOnce());
    expect(validate).toHaveBeenCalledOnce();
  });

  it("does not mark a changed key valid when an earlier validation finishes", async () => {
    let resolve!: () => void;
    validate.mockImplementationOnce(() => new Promise<void>((done) => { resolve = done; }));
    render(Setup); await enterKey();
    await fireEvent.click(screen.getByRole("button", { name: "Validate" }));
    await fireEvent.input(screen.getByLabelText("Linear API key"), { target: { value: "new-key" } });
    resolve();
    await waitFor(() => expect(screen.getByRole("button", { name: "Validate" })).toBeEnabled());
    expect(screen.queryByText("Key is valid.")).not.toBeInTheDocument();
  });

  it("holds further submission and freezes settings while connecting", async () => {
    let resolve!: () => void;
    validate.mockImplementationOnce(() => new Promise<void>((done) => { resolve = done; }));
    render(Setup); await enterKey(); await fireEvent.click(start());
    expect(screen.getByLabelText("Linear API key")).toBeDisabled();
    expect(screen.getByLabelText("Default agent")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Starting…" })).toBeDisabled();
    expect(store.hasConfig).toBe(false);
    resolve();
    await waitFor(() => expect(startDaemon).toHaveBeenCalledOnce());
    expect(setup).toHaveBeenCalledOnce();
  });

  it("keeps the theme defaults usable when the catalog cannot load", async () => {
    themes.mockRejectedValueOnce(new Error("offline"));
    render(Setup); await enterKey();
    expect(screen.getByLabelText("Theme")).toHaveValue(DEFAULT_THEME_ID);
    await fireEvent.click(start());
    await waitFor(() => expect(setup).toHaveBeenCalledWith(defaults));
  });

  it("says an invalid key next to the key and keeps it for fixing", async () => {
    validate.mockRejectedValueOnce(new Error("Invalid Linear key"));
    render(Setup); await enterKey(); await fireEvent.click(start());
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/Invalid Linear key/);
    // Inline under the key field, not the form-level error at the bottom.
    expect(alert.previousElementSibling?.querySelector("#setup-linear-key")).not.toBeNull();
    expect(screen.getByLabelText("Linear API key")).toHaveValue("test-key");
    expect(start()).toBeEnabled();
  });

  it("checks the key with Linear as soon as the field is left", async () => {
    render(Setup); await enterKey();
    await fireEvent.blur(screen.getByLabelText("Linear API key"));
    expect(await screen.findByText("Key is valid.")).toBeInTheDocument();
    expect(validate).toHaveBeenCalledWith("test-key");
  });

  it("waits for the check the blur started instead of asking twice", async () => {
    let resolve!: () => void;
    validate.mockImplementationOnce(() => new Promise<void>((done) => { resolve = done; }));
    render(Setup); await enterKey();
    await fireEvent.blur(screen.getByLabelText("Linear API key"));
    // A check in flight does not disable the button the click is landing on.
    expect(start()).toBeEnabled();
    await fireEvent.click(start());
    resolve();
    await waitFor(() => expect(setup).toHaveBeenCalledOnce());
    expect(validate).toHaveBeenCalledOnce();
  });

  it("opens Linear's key page through the app before any daemon runs", async () => {
    render(Setup);
    await fireEvent.click(screen.getByRole("button", { name: "Where do I get a Linear key?" }));
    expect(openHelp).toHaveBeenCalledOnce();
    expect(openURL).not.toHaveBeenCalled();
  });

  it("opens the key page through the daemon when one runs", async () => {
    store.alive = true;
    render(Setup);
    await fireEvent.click(screen.getByRole("button", { name: "Where do I get a Linear key?" }));
    expect(openURL).toHaveBeenCalledWith("https://linear.app/settings/account/security");
    expect(openHelp).not.toHaveBeenCalled();
  });

  it("names the page when no browser can be opened", async () => {
    openHelp.mockRejectedValueOnce(new Error("no app"));
    render(Setup);
    await fireEvent.click(screen.getByRole("button", { name: "Where do I get a Linear key?" }));
    expect(await screen.findByText(/linear\.app\/settings\/account\/security/)).toBeInTheDocument();
  });

  it("shows the setup checklist, with the key judged by this screen", async () => {
    render(Setup);
    const list = await screen.findByRole("list", { name: "Setup checklist" });
    expect(list.querySelector('[data-item="linear"]')).toHaveAttribute("data-state", "unknown");
    await enterKey();
    await fireEvent.click(screen.getByRole("button", { name: "Validate" }));
    await screen.findByText("Key is valid.");
    expect(list.querySelector('[data-item="linear"]')).toHaveAttribute("data-state", "ok");
    expect(list.querySelector('[data-item="poll"]')).toHaveTextContent(/After you start Lola/);
  });
});
