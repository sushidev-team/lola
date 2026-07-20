import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within, cleanup } from "@testing-library/svelte";
// The Go list of accepted theme ids is read straight off disk to prove the two
// sides agree. `@types/node` is deliberately not a dependency, so the builtin is
// asserted in rather than typed — same pattern as catppuccin.test.ts.
// @ts-expect-error node builtin, available under vitest, untyped here
import { readFileSync } from "node:fs";
declare const process: { cwd(): string };

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
const { GetSettings, SaveSettings, PrioritySortKeys, Themes, SetTheme, WorkspaceLabels, TeamMeta, setFlash, closeOverlay } =
  vi.hoisted(() => ({
    GetSettings: vi.fn(),
    SaveSettings: vi.fn(),
    PrioritySortKeys: vi.fn(),
    Themes: vi.fn(),
    SetTheme: vi.fn(),
    WorkspaceLabels: vi.fn(),
    TeamMeta: vi.fn(),
    setFlash: vi.fn(),
    closeOverlay: vi.fn(),
  }));

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    GetSettings: () => GetSettings(),
    SaveSettings: (dto: unknown) => SaveSettings(dto),
    PrioritySortKeys: () => PrioritySortKeys(),
    Themes: () => Themes(),
    SetTheme: (name: string) => SetTheme(name),
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
// The real appearance store, not a mock: the preview's whole job is to repaint
// the document, and asserting on `data-theme` proves it actually happened
// rather than that a spy was called.
import { appearance, DEFAULT_THEME_ID, THEME_IDS } from "$lib/theme-runtime.svelte";

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
    PrioritySortKeys.mockReset().mockResolvedValue(["priority", "createdAt"]);
    Themes.mockReset().mockResolvedValue([...THEME_IDS]);
    SetTheme.mockReset().mockResolvedValue(undefined);
    TeamMeta.mockReset();
    setFlash.mockReset();
    closeOverlay.mockReset();
    // `appearance` is a module singleton, so a preview would otherwise leak into
    // the next test. Reset it to the persisted-default state the app boots in.
    appearance.id = DEFAULT_THEME_ID;
    appearance.paint();
  });

  it("loads settings on mount and binds fields on the Defaults tab", async () => {
    render(SettingsForm);
    expect(await screen.findByDisplayValue("60s")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Defaults" })).toHaveAttribute("aria-selected", "true");
    // the active agent segment reflects the loaded value
    const codex = screen.getByRole("button", { name: "codex" });
    expect(codex.className).toContain("text-accent-ink");
  });

  it("tabs the sections instead of stacking them", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    for (const t of ["Defaults", "Project defaults", "Notify", "Brain", "CodeRabbit", "Appearance"]) {
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

  // priority_sort is an ORDERED chain over lola's own sort keys — not Linear
  // priorities, and nothing is fetched from Linear for it.
  it("picks priority-sort keys in order", async () => {
    // Start with no chain so the clicks below ADD in order rather than clearing
    // the fixture's existing selection.
    GetSettings.mockResolvedValue({ ...fakeDto, prioritySort: [] });
    render(SettingsForm);
    await screen.findByDisplayValue("60s"); // the form only renders once loaded
    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));

    await waitFor(() => expect(PrioritySortKeys).toHaveBeenCalled());
    // Click createdAt first, then priority — the reverse of the default.
    await fireEvent.click(await screen.findByRole("button", { name: /createdAt/ }));
    await fireEvent.click(await screen.findByRole("button", { name: /priority/ }));

    await fireEvent.click(screen.getByRole("button", { name: "save" }));
    await waitFor(() => expect(SaveSettings).toHaveBeenCalled());
    expect(SaveSettings.mock.calls.at(-1)?.[0].prioritySort).toEqual(["createdAt", "priority"]);
  });

  it("falls back to text entry when the sort keys cannot be read", async () => {
    PrioritySortKeys.mockRejectedValue(new Error("nope"));
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await fireEvent.click(screen.getByRole("tab", { name: "Project defaults" }));

    await waitFor(() => expect(PrioritySortKeys).toHaveBeenCalled());
    expect(await screen.findByLabelText("Priority sort")).toBeInTheDocument();
  });

  // The theme is the only setting with a live preview, and the only one that is
  // not carried on the SettingsDTO: [ui] is presentation rather than a
  // [defaults] key, and ConfigService.SetTheme is its sole writer.
  describe("appearance", () => {
    const swatch = (label: RegExp) => screen.getByRole("button", { name: label });

    async function openAppearance() {
      render(SettingsForm);
      await screen.findByDisplayValue("60s");
      await fireEvent.click(screen.getByRole("tab", { name: "Appearance" }));
      await waitFor(() => expect(Themes).toHaveBeenCalled());
    }

    it("offers every flavor by name, drawn in its own palette, with the live one marked", async () => {
      await openAppearance();
      for (const label of ["Mocha", "Macchiato", "Frappé", "Latte"]) {
        expect(screen.getByRole("button", { name: new RegExp(label) })).toBeInTheDocument();
      }
      // Mocha is DEFAULT_THEME_ID, so it is the live flavor on boot.
      expect(swatch(/Mocha/)).toHaveAttribute("aria-pressed", "true");
      expect(swatch(/Latte/)).toHaveAttribute("aria-pressed", "false");
      // Each option previews itself: latte's card is painted latte base, not the
      // app's current (mocha) surface, and carries a row of colour chips.
      // (jsdom normalises the hex we write into rgb() when parsing `style`.)
      expect((swatch(/Latte/) as HTMLElement).style.backgroundColor).toBe("rgb(239, 241, 245)"); // #eff1f5
      expect(swatch(/Latte/).querySelectorAll("span[style*='background']")).toHaveLength(9);
    });

    it("previewing a flavor repaints the app immediately and persists nothing", async () => {
      await openAppearance();
      await fireEvent.click(swatch(/Latte/));

      expect(appearance.id).toBe("catppuccin-latte");
      expect(document.documentElement.dataset.theme).toBe("catppuccin-latte");
      expect(document.documentElement.style.getPropertyValue("--color-panel")).toBe("#eff1f5");
      expect(swatch(/Latte/)).toHaveAttribute("aria-pressed", "true");
      // The point of a preview: config.toml is untouched until save.
      expect(SetTheme).not.toHaveBeenCalled();
      expect(SaveSettings).not.toHaveBeenCalled();
    });

    it("saves the previewed flavor through SetTheme, never as a settings field", async () => {
      await openAppearance();
      await fireEvent.click(swatch(/Frappé/));
      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      await waitFor(() => expect(SetTheme).toHaveBeenCalledWith("catppuccin-frappe"));
      expect(SaveSettings.mock.calls[0][0]).not.toHaveProperty("theme");
      expect(setFlash).toHaveBeenCalledWith("settings saved", "good");
      expect(closeOverlay).toHaveBeenCalledTimes(1);
    });

    it("caches the saved flavor so the next launch paints it on the first frame", async () => {
      // The regression: save used to call ConfigService.SetTheme directly,
      // which wrote config.toml but left the localStorage cache on the OLD
      // flavor. appearance.init() paints from that cache before the bridge can
      // answer, so the launch immediately after a theme change — the one where
      // it matters — flashed the previous colours. Routing the save through
      // appearance.commit() is what closes it.
      localStorage.setItem("lola.theme", DEFAULT_THEME_ID);
      await openAppearance();
      await fireEvent.click(swatch(/Frappé/));
      expect(localStorage.getItem("lola.theme")).toBe(DEFAULT_THEME_ID); // preview caches nothing

      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
      await waitFor(() => expect(localStorage.getItem("lola.theme")).toBe("catppuccin-frappe"));
    });

    it("caches nothing when the write fails, so the cache cannot lead config.toml", async () => {
      localStorage.setItem("lola.theme", DEFAULT_THEME_ID);
      SetTheme.mockRejectedValueOnce(new Error("read-only config"));
      await openAppearance();
      await fireEvent.click(swatch(/Frappé/));
      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      await waitFor(() => expect(setFlash).toHaveBeenCalledWith(expect.stringContaining("read-only config"), "bad"));
      expect(localStorage.getItem("lola.theme")).toBe(DEFAULT_THEME_ID);
      expect(closeOverlay).not.toHaveBeenCalled();
    });

    it("writes no theme at all when the appearance tab was never touched", async () => {
      await openAppearance();
      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      await waitFor(() => expect(SaveSettings).toHaveBeenCalled());
      expect(SetTheme).not.toHaveBeenCalled();
    });

    it("cancel reverts the preview to the persisted flavor", async () => {
      await openAppearance();
      await fireEvent.click(swatch(/Macchiato/));
      expect(appearance.id).toBe("catppuccin-macchiato");

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));

      expect(appearance.id).toBe(DEFAULT_THEME_ID);
      expect(document.documentElement.dataset.theme).toBe(DEFAULT_THEME_ID);
      expect(SetTheme).not.toHaveBeenCalled();
      expect(closeOverlay).toHaveBeenCalledTimes(1);
    });

    it("reverts on any close path, not just the cancel button", async () => {
      // Escape, the backdrop and the ✕ close the overlay too, so the revert
      // hangs off the lifecycle — none of them can strand a preview.
      const { unmount } = render(SettingsForm);
      await screen.findByDisplayValue("60s");
      await fireEvent.click(screen.getByRole("tab", { name: "Appearance" }));
      await waitFor(() => expect(Themes).toHaveBeenCalled());
      await fireEvent.click(swatch(/Latte/));
      expect(appearance.id).toBe("catppuccin-latte");

      unmount();

      expect(appearance.id).toBe(DEFAULT_THEME_ID);
      expect(document.documentElement.dataset.theme).toBe(DEFAULT_THEME_ID);
    });

    it("offers only what the daemon accepts, in the frontend's own dark→light order", async () => {
      // config.UIThemes is the authority on MEMBERSHIP — offering an id it drops
      // would build a picker that fails on save. Order stays ours: the Go list
      // runs light→dark, and adopting it would reflow the grid mid-open.
      Themes.mockResolvedValueOnce(["catppuccin-latte", "catppuccin-mocha", "catppuccin-unknown"]);
      await openAppearance();

      expect(screen.queryByRole("button", { name: /Macchiato/ })).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /unknown/i })).not.toBeInTheDocument();
      const following = swatch(/Mocha/).compareDocumentPosition(swatch(/Latte/));
      expect(following & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    it("falls back to the built-in list when the bridge cannot enumerate", async () => {
      // A desktop binary predating ConfigService.Themes answers `unknown cmd`;
      // an empty grid would be a worse outcome than a slightly stale list.
      Themes.mockRejectedValueOnce(new Error("unknown cmd"));
      await openAppearance();

      for (const label of ["Mocha", "Macchiato", "Frappé", "Latte"]) {
        expect(screen.getByRole("button", { name: new RegExp(label) })).toBeInTheDocument();
      }
    });

    it("offers exactly the ids internal/config accepts", async () => {
      // The picker enumerates over the bridge at runtime, but the fallback list
      // and every flavor's palette are compiled in, so the two sides can still
      // drift within a build. Compare against the Go source directly.
      const go = readFileSync(process.cwd() + "/../../internal/config/ui.go", "utf8") as string;
      const block = /var UIThemes = \[\]string\{([^}]*)\}/.exec(go);
      expect(block, "config.UIThemes not found in internal/config/ui.go").not.toBeNull();
      const goIds = [...block![1].matchAll(/"([^"]+)"/g)].map((m) => m[1]);
      expect(goIds.slice().sort()).toEqual(THEME_IDS.slice().sort());
    });
  });
});
