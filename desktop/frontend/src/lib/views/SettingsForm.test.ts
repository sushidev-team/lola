import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/svelte";

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

  // Project defaults — the [defaults] counterpart of each inheritable
  // [[project]] key.
  branchPrefix: "lola/",
  symlinks: [".env"],
  postCreate: ["make setup"],
  env: ["SHARED=1"],
  matchLabels: ["lab-1"],
  matchMode: "any",
  onSentSetLabel: "",
  blockedLabelId: "",
  dedupMode: "label",
  prioritySort: ["priority", "createdAt"],
  defaultsTeamId: "team-uuid-1",
};

// vi.mock factories are hoisted; keep their fns in vi.hoisted so they exist when
// the factories run.
const { GetSettings, SaveSettings, TeamMeta, setFlash, closeOverlay } = vi.hoisted(() => ({
  GetSettings: vi.fn(),
  SaveSettings: vi.fn(),
  TeamMeta: vi.fn(),
  setFlash: vi.fn(),
  closeOverlay: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    GetSettings: () => GetSettings(),
    SaveSettings: (dto: unknown) => SaveSettings(dto),
  },
  LinearService: {
    TeamMeta: (...a: unknown[]) => TeamMeta(...a),
    Teams: vi.fn(),
  },
}));
vi.mock("$lib/store.svelte", () => ({ store: { setFlash } }));
vi.mock("$lib/nav.svelte", () => ({ nav: { closeOverlay, overlayTab: "" } }));

import SettingsForm from "./SettingsForm.svelte";

const meta = {
  projects: [],
  cycles: [],
  activeCycleId: "",
  states: [],
  labels: [
    { id: "lab-1", label: "agent" },
    { id: "lab-2", label: "blocked" },
  ],
  members: [],
};

describe("SettingsForm", () => {
  beforeEach(() => {
    cleanup();
    GetSettings.mockReset().mockResolvedValue({ ...fakeDto });
    SaveSettings.mockReset().mockResolvedValue(undefined);
    TeamMeta.mockReset().mockResolvedValue(meta);
    setFlash.mockReset();
    closeOverlay.mockReset();
  });

  it("loads settings on mount and binds fields on the Defaults tab", async () => {
    render(SettingsForm);
    expect(await screen.findByDisplayValue("60s")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Defaults" })).toHaveAttribute("aria-selected", "true");
    // the active agent segment reflects the loaded value
    const codex = screen.getByRole("button", { name: "codex" });
    expect(codex.className).toContain("text-accent");
  });

  it("tabs the sections instead of stacking them", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    for (const t of ["Defaults", "Project defaults", "Notify", "Brain", "CodeRabbit"]) {
      expect(screen.getByRole("tab", { name: t })).toBeInTheDocument();
    }
    // Off-tab content isn't mounted…
    expect(screen.queryByText("CodeRabbit watch")).not.toBeInTheDocument();
    // …until its tab is picked.
    await fireEvent.click(screen.getByRole("tab", { name: "CodeRabbit" }));
    expect(screen.getByText("CodeRabbit review")).toBeInTheDocument();
    expect(screen.getByText("CodeRabbit watch")).toBeInTheDocument();
  });

  it("offers a real label picker on Project defaults when one team owns every poll", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await waitFor(() => expect(TeamMeta).toHaveBeenCalledWith("team-uuid-1", false));

    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));
    expect(screen.getByLabelText("Symlinks")).toHaveValue(".env");
    // matchLabels is a checkbox list built from the team's labels, not raw UUIDs.
    const agentLabel = screen.getByRole("checkbox", { name: "agent" }) as HTMLInputElement;
    expect(agentLabel.checked).toBe(true);
    expect((screen.getByRole("checkbox", { name: "blocked" }) as HTMLInputElement).checked).toBe(false);
    // the single-select label keys are selects too
    expect(screen.getByLabelText("Blocked label").tagName).toBe("SELECT");
  });

  it("falls back to raw UUID entry when polling projects span several teams", async () => {
    GetSettings.mockResolvedValueOnce({ ...fakeDto, defaultsTeamId: "" });
    render(SettingsForm);
    await screen.findByDisplayValue("60s");

    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));
    expect(TeamMeta).not.toHaveBeenCalled();
    expect(screen.getByText(/span more than one team/)).toBeInTheDocument();
    expect(screen.getByLabelText("Blocked label").tagName).toBe("INPUT");
    expect(screen.getByLabelText("Match labels")).toHaveValue("lab-1");
  });

  it("saves the dto with the list fields cleaned, flashes good, and closes the overlay", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));
    await fireEvent.input(screen.getByLabelText("Post-create"), { target: { value: " make setup \n\n npm ci\n" } });

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(SaveSettings).toHaveBeenCalledTimes(1));
    expect(SaveSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        globalCap: 5,
        agent: "codex",
        branchPrefix: "lola/",
        postCreate: ["make setup", "npm ci"], // trimmed, blanks dropped
        prioritySort: ["priority", "createdAt"],
      }),
    );
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
