import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within, cleanup } from "@testing-library/svelte";

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
  // Still on the Go DTO but no longer read by the UI: the [defaults] label keys
  // take workspace labels, which are not team-scoped. Left set here so the
  // tests prove it is ignored rather than merely absent.
  defaultsTeamId: "team-uuid-1",
};

// vi.mock factories are hoisted; keep their fns in vi.hoisted so they exist when
// the factories run.
const { GetSettings, SaveSettings, WorkspaceLabels, TeamMeta, setFlash, closeOverlay } = vi.hoisted(() => ({
  GetSettings: vi.fn(),
  SaveSettings: vi.fn(),
  WorkspaceLabels: vi.fn(),
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
    WorkspaceLabels: () => WorkspaceLabels(),
    TeamMeta: (...a: unknown[]) => TeamMeta(...a),
    Teams: vi.fn(),
  },
}));
vi.mock("$lib/store.svelte", () => ({ store: { setFlash } }));
vi.mock("$lib/nav.svelte", () => ({ nav: { closeOverlay, overlayTab: "" } }));

import SettingsForm from "./SettingsForm.svelte";

// Organisation-level labels: no team, so valid for a [defaults] key that
// projects on any team inherit.
const workspaceLabels = [
  { id: "lab-1", label: "agent" },
  { id: "lab-2", label: "blocked" },
];

describe("SettingsForm", () => {
  beforeEach(() => {
    cleanup();
    GetSettings.mockReset().mockResolvedValue({ ...fakeDto });
    SaveSettings.mockReset().mockResolvedValue(undefined);
    WorkspaceLabels.mockReset().mockResolvedValue(workspaceLabels);
    TeamMeta.mockReset();
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

  it("offers workspace-label pickers for all three [defaults] label keys", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    // Lazy: nothing is fetched until the tab that needs it is opened.
    expect(WorkspaceLabels).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));
    await waitFor(() => expect(WorkspaceLabels).toHaveBeenCalledTimes(1));

    expect(screen.getByLabelText("Symlinks")).toHaveValue(".env");
    // matchLabels is a checkbox list built from the workspace labels.
    expect((await screen.findByRole("checkbox", { name: "agent" })) as HTMLInputElement).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "blocked" })).not.toBeChecked();
    // …and the two single-select keys are real selects, with a "(none)" option.
    for (const caption of ["On-sent set label", "Blocked label"]) {
      const el = screen.getByLabelText(caption);
      expect(el.tagName).toBe("SELECT");
      expect(within(el).getByRole("option", { name: "(none)" })).toBeInTheDocument();
      expect(within(el).getByRole("option", { name: "agent" })).toBeInTheDocument();
    }
    // The team-scoped picker is never used for a workspace-wide default.
    expect(TeamMeta).not.toHaveBeenCalled();
  });

  it("toggling a workspace match label updates the saved list", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));

    await fireEvent.click(await screen.findByRole("checkbox", { name: "blocked" })); // add lab-2
    expect(screen.getByRole("checkbox", { name: "blocked" })).toBeChecked();

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(SaveSettings).toHaveBeenCalledTimes(1));
    expect(SaveSettings.mock.calls[0][0]).toMatchObject({ matchLabels: ["lab-1", "lab-2"] });
  });

  it("falls back to manual UUID entry when the workspace labels can't be loaded", async () => {
    WorkspaceLabels.mockRejectedValueOnce(new Error("no api key"));
    render(SettingsForm);
    await screen.findByDisplayValue("60s");

    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));

    expect(await screen.findByText(/couldn't load workspace labels.*no api key/)).toBeInTheDocument();
    expect(screen.getByLabelText("Blocked label").tagName).toBe("INPUT");
    expect(screen.getByLabelText("Match labels")).toHaveValue("lab-1"); // the textarea escape hatch
  });

  it("falls back to manual entry, and explains why, in a workspace with no organisation labels", async () => {
    WorkspaceLabels.mockResolvedValueOnce([]);
    render(SettingsForm);
    await screen.findByDisplayValue("60s");

    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));

    expect(await screen.findByText(/no organisation-level labels/)).toBeInTheDocument();
    expect(screen.getByLabelText("On-sent set label").tagName).toBe("INPUT");
  });

  it("loads the workspace labels once, not on every visit to the tab", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");

    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));
    await waitFor(() => expect(WorkspaceLabels).toHaveBeenCalledTimes(1));
    await fireEvent.click(screen.getByRole("tab", { name: "Notify" }));
    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));

    expect(WorkspaceLabels).toHaveBeenCalledTimes(1);
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
