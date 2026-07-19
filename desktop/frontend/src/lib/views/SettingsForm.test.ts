import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

// Fake settings returned by the mocked ConfigService.GetSettings().
const fakeDto = {
  globalCap: 5,
  concurrencyCap: 2,
  pollInterval: "60s",
  agent: "codex",
  notifyDesktop: true,
  slackWebhookEnv: "LOLA_SLACK",
  brainEnabled: false,
  brainModel: "claude-x",
  brainTimeout: 30,
  brainSummarizeEscalation: false,
  brainSummarizeApproved: false,
  reviewEnabled: true,
  reviewCommand: "coderabbit review",
  reviewOnPrOpen: true,
  reviewSendToAgent: false,
  reviewCommentOnLinear: false,
  reviewTimeout: 120,
  crEnabled: false,
  crAuthor: "coderabbitai[bot]",
  crNotify: false,
  crSendToAgent: false,
  crCommentOnLinear: false,
};

// vi.mock factories are hoisted; keep their fns in vi.hoisted so they exist when
// the factories run.
const { GetSettings, SaveSettings, setFlash, closeOverlay } = vi.hoisted(() => ({
  GetSettings: vi.fn(),
  SaveSettings: vi.fn(),
  setFlash: vi.fn(),
  closeOverlay: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    GetSettings: () => GetSettings(),
    SaveSettings: (dto: unknown) => SaveSettings(dto),
  },
}));
vi.mock("$lib/store.svelte", () => ({ store: { setFlash } }));
vi.mock("$lib/nav.svelte", () => ({ nav: { closeOverlay } }));

import SettingsForm from "./SettingsForm.svelte";

describe("SettingsForm", () => {
  beforeEach(() => {
    GetSettings.mockReset().mockResolvedValue({ ...fakeDto });
    SaveSettings.mockReset().mockResolvedValue(undefined);
    setFlash.mockReset();
    closeOverlay.mockReset();
  });

  it("loads settings on mount and binds fields", async () => {
    render(SettingsForm);
    expect(await screen.findByDisplayValue("60s")).toBeInTheDocument();
    // section headers present
    expect(screen.getByText("Defaults")).toBeInTheDocument();
    expect(screen.getByText("CodeRabbit review")).toBeInTheDocument();
    expect(screen.getByText("CodeRabbit watch")).toBeInTheDocument();
    // the active agent segment reflects the loaded value
    const codex = screen.getByRole("button", { name: "codex" });
    expect(codex.className).toContain("text-accent");
  });

  it("saves the dto, flashes good, and closes the overlay", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(SaveSettings).toHaveBeenCalledTimes(1));
    expect(SaveSettings).toHaveBeenCalledWith(expect.objectContaining({ globalCap: 5, agent: "codex" }));
    expect(setFlash).toHaveBeenCalledWith("settings saved", "good");
    expect(closeOverlay).toHaveBeenCalledTimes(1);
  });

  it("flashes bad and stays open when save fails", async () => {
    SaveSettings.mockRejectedValueOnce(new Error("boom"));
    render(SettingsForm);
    await screen.findByDisplayValue("60s");

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(setFlash).toHaveBeenCalledWith(expect.stringContaining("boom"), "bad"));
    expect(closeOverlay).not.toHaveBeenCalled();
  });
});
