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
  remoteEnabled: false,
  remoteBind: "localhost",
  remotePort: 7717,
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
const {
  GetSettings,
  SaveSettings,
  PrioritySortKeys,
  RemoteBinds,
  ReviewKinds,
  Themes,
  SetTheme,
  WorkspaceLabels,
  TeamMeta,
  LinearKeyStatus,
  SetLinearKey,
  ValidateLinearKey,
  ConnectCode,
  setFlash,
  reload,
  closeOverlay,
} = vi.hoisted(() => ({
  GetSettings: vi.fn(),
  SaveSettings: vi.fn(),
  PrioritySortKeys: vi.fn(),
  RemoteBinds: vi.fn(),
  ReviewKinds: vi.fn(),
  Themes: vi.fn(),
  SetTheme: vi.fn(),
  WorkspaceLabels: vi.fn(),
  TeamMeta: vi.fn(),
  LinearKeyStatus: vi.fn(),
  SetLinearKey: vi.fn(),
  ValidateLinearKey: vi.fn(),
  ConnectCode: vi.fn(),
  setFlash: vi.fn(),
  reload: vi.fn(),
  closeOverlay: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    GetSettings: () => GetSettings(),
    SaveSettings: (dto: unknown) => SaveSettings(dto),
    PrioritySortKeys: () => PrioritySortKeys(),
    RemoteBinds: () => RemoteBinds(),
    ReviewKinds: () => ReviewKinds(),
    Themes: () => Themes(),
    SetTheme: (name: string) => SetTheme(name),
    LinearKeyStatus: () => LinearKeyStatus(),
    SetLinearKey: (k: string) => SetLinearKey(k),
    ValidateLinearKey: (k: string) => ValidateLinearKey(k),
    ConnectCode: () => ConnectCode(),
  },
  LinearService: {
    WorkspaceLabels: () => WorkspaceLabels(),
    TeamMeta: (...a: unknown[]) => TeamMeta(...a),
    Teams: vi.fn(),
  },
}));
vi.mock("$lib/store.svelte", () => ({ store: { setFlash, reload } }));
vi.mock("$lib/nav.svelte", () => ({ nav: { closeOverlay, overlayTab: "" } }));

import SettingsForm from "./SettingsForm.svelte";
import { nav } from "$lib/nav.svelte"; // the mock above — used to drive overlayTab
import { confirm } from "$lib/confirm.svelte"; // the real singleton — the guard uses it
// The real appearance store, not a mock: the preview's whole job is to repaint
// the document, and asserting on `data-theme` proves it actually happened
// rather than that a spy was called.
import { appearance, DEFAULT_THEME_ID, THEME_IDS } from "$lib/theme-runtime.svelte";

// The kind descriptors ConfigService.ReviewKinds() serves. The Review tab is
// rendered ENTIRELY from these — which kinds exist, what each is called, and
// which fields it has — so a review agent added on the Go side needs no change
// in the component. reviewKindsMatchGo below pins this list against the Go one.
const reviewKinds = [
  { kind: "coderabbit-cli", label: "coderabbit-cli — execs `coderabbit review` on PR-open", watch: false, cli: true, agent: "", requiresCommand: false, requiresAuthor: false },
  { kind: "custom-cli", label: "custom-cli — execs your own review CLI on PR-open", watch: false, cli: true, agent: "", requiresCommand: true, requiresAuthor: false },
  { kind: "coderabbit-watch", label: "coderabbit-watch — polls the PR for the CodeRabbit app's comments", watch: true, cli: false, agent: "", requiresCommand: false, requiresAuthor: false },
  { kind: "bot-watch", label: "bot-watch — polls the PR for any review bot's comments", watch: true, cli: false, agent: "", requiresCommand: false, requiresAuthor: true },
  { kind: "claude-session", label: "claude-session — headless `claude` review on PR-open", watch: false, cli: false, agent: "claude", requiresCommand: false, requiresAuthor: false },
  { kind: "codex-session", label: "codex-session — headless `codex` review on PR-open", watch: false, cli: false, agent: "codex", requiresCommand: false, requiresAuthor: false },
  { kind: "opencode-session", label: "opencode-session — headless `opencode` review on PR-open", watch: false, cli: false, agent: "opencode", requiresCommand: false, requiresAuthor: false },
];

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
    RemoteBinds.mockReset().mockResolvedValue(["off", "localhost", "lan", "all"]);
    ReviewKinds.mockReset().mockResolvedValue(reviewKinds.map((k) => ({ ...k })));
    Themes.mockReset().mockResolvedValue([...THEME_IDS]);
    SetTheme.mockReset().mockResolvedValue(undefined);
    TeamMeta.mockReset();
    LinearKeyStatus.mockReset().mockResolvedValue({
      configured: true,
      resolvable: true,
      source: "macOS Keychain (lola-linear)",
      detail: "",
    });
    SetLinearKey.mockReset().mockResolvedValue("key stored in the macOS Keychain (service lola-linear)");
    ValidateLinearKey.mockReset().mockResolvedValue(undefined);
    ConnectCode.mockReset().mockResolvedValue({
      code: "lola1.eyJhIjpbIjEyNy4wLjAuMSJdfQ",
      hosts: ["127.0.0.1"],
      port: 7717,
      pin: "C4td4uyeJMSyxfoAsB3i98Kd6JhkpOTf3Oxipiq+sxI=",
      key: "0123456789abcdef0123456789abcdef",
      insecure: true,
      problem: "",
    });
    setFlash.mockReset();
    reload.mockReset().mockResolvedValue(undefined);
    closeOverlay.mockReset();
    confirm.cancel(); // the confirm store is a singleton — clear it between tests
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
    // The provider-catalog tab is "Review", not "CodeRabbit": it holds every
    // [[review.provider]] kind, claude-session included.
    for (const t of ["Defaults", "Project defaults", "Notify", "Brain", "Review", "Remote", "Appearance"]) {
      expect(screen.getByRole("tab", { name: t })).toBeInTheDocument();
    }
    // Off-tab content isn't mounted…
    expect(screen.queryByText("No review pass configured.")).not.toBeInTheDocument();
    // …until its tab is picked. This DTO has no [[review.provider]] entries, so
    // the tab must explain itself rather than showing bare kind buttons.
    await fireEvent.click(screen.getByRole("tab", { name: "Review" }));
    expect(screen.getByText("No review pass configured.")).toBeInTheDocument();
    // Every kind the BACKEND offers is addable — the list is not hardcoded here
    // or in the component, so a new review agent shows up on both sides at once.
    await waitFor(() => expect(ReviewKinds).toHaveBeenCalled());
    for (const k of reviewKinds) {
      expect(await screen.findByRole("button", { name: k.kind })).toBeInTheDocument();
    }
  });

  it("clamps an unknown deep-linked tab id to Defaults instead of a blank pane", async () => {
    nav.overlayTab = "nope";
    try {
      render(SettingsForm);
      expect(await screen.findByDisplayValue("60s")).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Defaults" })).toHaveAttribute("aria-selected", "true");
    } finally {
      nav.overlayTab = "";
    }
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

  // --- remote ([remote], the phone listener) --------------------------------
  //
  // bind is a keyword OR an IP literal, and the picker can only offer the
  // keywords. These pin the half of that which is a data-loss bug rather than a
  // cosmetic one: a configured literal must survive a save of any other tab.

  it("offers the bind keywords the backend serves rather than a hardcoded list", async () => {
    RemoteBinds.mockResolvedValue(["off", "localhost", "lan", "all", "invented"]);
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await fireEvent.click(screen.getByRole("tab", { name: "Remote" }));

    await waitFor(() => expect(RemoteBinds).toHaveBeenCalled());
    const sel = screen.getByLabelText("Bind") as HTMLSelectElement;
    const opts = [...sel.options].map((o) => o.value);
    expect(opts).toEqual(["off", "localhost", "lan", "all", "invented", "__literal"]);
  });

  it("keeps a configured IP literal instead of coercing it to a keyword", async () => {
    GetSettings.mockResolvedValue({ ...fakeDto, remoteEnabled: true, remoteBind: "192.168.1.20" });
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await fireEvent.click(screen.getByRole("tab", { name: "Remote" }));

    // The picker cannot show the literal, so the row hands over to a text input
    // carrying the real value — it is never silently rewritten.
    await waitFor(() => expect(RemoteBinds).toHaveBeenCalled());
    expect(await screen.findByDisplayValue("192.168.1.20")).toBeInTheDocument();

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(SaveSettings).toHaveBeenCalledTimes(1));
    expect(SaveSettings.mock.calls[0][0]).toMatchObject({ remoteBind: "192.168.1.20" });
  });

  it("switches to a literal on request and saves what was typed", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await fireEvent.click(screen.getByRole("tab", { name: "Remote" }));
    await waitFor(() => expect(RemoteBinds).toHaveBeenCalled());

    await fireEvent.change(screen.getByLabelText("Bind"), { target: { value: "__literal" } });
    await fireEvent.input(await screen.findByPlaceholderText("192.168.1.20"), {
      target: { value: "10.0.0.5" },
    });

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(SaveSettings).toHaveBeenCalledTimes(1));
    expect(SaveSettings.mock.calls[0][0]).toMatchObject({ remoteBind: "10.0.0.5" });
  });

  it("saves the listener toggle and port", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await fireEvent.click(screen.getByRole("tab", { name: "Remote" }));
    await waitFor(() => expect(RemoteBinds).toHaveBeenCalled());

    await fireEvent.click(screen.getByRole("checkbox", { name: "Enabled" }));
    await fireEvent.input(screen.getByLabelText("Port"), { target: { value: "7800" } });

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(SaveSettings).toHaveBeenCalledTimes(1));
    expect(SaveSettings.mock.calls[0][0]).toMatchObject({ remoteEnabled: true, remotePort: 7800 });
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

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
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

  // The github transport is the only sink with two shapes, so the choice between
  // them appears only once github is actually selected — and defaults to the
  // resolvable-threads one.
  describe("review provider github shape", () => {
    const inlineBox = () => screen.queryByRole("checkbox", { name: /Inline PR threads/ });

    async function openClaudeProvider() {
      render(SettingsForm);
      await screen.findByDisplayValue("60s");
      await fireEvent.click(screen.getByRole("tab", { name: "Review" }));
      await fireEvent.click(screen.getByRole("button", { name: "claude-session" }));
    }

    it("hides the shape toggle until the github transport is picked", async () => {
      await openClaudeProvider();
      expect(inlineBox()).not.toBeInTheDocument();

      await fireEvent.click(screen.getByRole("checkbox", { name: /^github$/ }));
      expect(inlineBox()).toBeChecked();
    });

    it("saves the opt-out back to the provider", async () => {
      await openClaudeProvider();
      await fireEvent.click(screen.getByRole("checkbox", { name: /^github$/ }));
      await fireEvent.click(inlineBox()!);

      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
      await waitFor(() => expect(SaveSettings).toHaveBeenCalled());
      const saved = SaveSettings.mock.calls.at(-1)?.[0].reviewProviders ?? [];
      const claude = saved.find((p: any) => p.provider === "claude-session");
      expect(claude.transports).toContain("github");
      expect(claude.githubInline).toBe(false);
    });
  });

  // The fixture above stands in for ConfigService.ReviewKinds(). It is the ONE
  // hardcoded copy of the kind catalog left in the frontend, and it exists only
  // so the component can be rendered without a backend — so pin it against the
  // Go source, the same way the theme ids are pinned. Without this a review
  // agent added on the Go side would keep every test green while the app never
  // offered it.
  it("mocks exactly the provider kinds internal/config defines", () => {
    const go = readFileSync(process.cwd() + "/../../internal/config/reviewprovider.go", "utf8") as string;
    const block = /var provKinds = \[\]provKind\{([^}]*)\}/.exec(go);
    expect(block, "provKinds not found in internal/config/reviewprovider.go").not.toBeNull();
    // The list names the CONSTANTS, so resolve each to its string value.
    const consts = new Map(
      [...go.matchAll(/(prov[A-Za-z]+)\s+provKind = "([^"]+)"/g)].map((m) => [m[1], m[2]]),
    );
    const goKinds = block![1]
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean)
      .map((name) => consts.get(name));
    expect(goKinds).toEqual(reviewKinds.map((k) => k.kind));
  });

  // The form seeds a newly-added provider with three defaults copied from
  // internal/config. Pin them, or a changed Go default silently leaves the app
  // creating providers with the old one.
  it("seeds new providers with the defaults internal/config resolves", () => {
    const go = readFileSync(process.cwd() + "/../../internal/config/review.go", "utf8") as string;
    const num = (name: string) => {
      const m = new RegExp(`const ${name} = (\\d+)`).exec(go);
      expect(m, `${name} not found in internal/config/review.go`).not.toBeNull();
      return Number(m![1]);
    };
    const str = (name: string) => {
      const m = new RegExp(`const ${name} = "([^"]*)"`).exec(go);
      expect(m, `${name} not found in internal/config/review.go`).not.toBeNull();
      return m![1];
    };
    const svelte = readFileSync(process.cwd() + "/src/lib/views/SettingsForm.svelte", "utf8") as string;
    const ts = (name: string) => {
      const m = new RegExp(`const ${name} = "?([^";\n]+)"?;`).exec(svelte);
      expect(m, `${name} not found in SettingsForm.svelte`).not.toBeNull();
      return m![1];
    };
    expect(ts("DEFAULT_BASE_FLAG")).toBe(str("DefaultReviewBaseFlag"));
    expect(Number(ts("DEFAULT_PASS_TIMEOUT"))).toBe(num("DefaultReviewTimeoutSeconds"));
    expect(Number(ts("DEFAULT_AGENT_TIMEOUT"))).toBe(num("DefaultClaudeReviewTimeoutSeconds"));
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

  // A mis-click on the dim backdrop — or a stray Escape — after editing across
  // tabs must not silently drop the edits. All four close paths (backdrop, ✕,
  // Escape, this cancel button) run one requestClose, so the footer cancel stands
  // in for them. Dirty is keyed off the DTO; a theme-only preview is not counted
  // (it reverts on close on its own — see the appearance tests above).
  describe("unsaved-changes guard", () => {
    it("closes a pristine form immediately, with no prompt", async () => {
      render(SettingsForm);
      await screen.findByDisplayValue("60s"); // fully loaded

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));

      expect(closeOverlay).toHaveBeenCalledTimes(1);
      expect(confirm.request).toBeNull();
    });

    it("prompts before discarding an edited form and does NOT close until confirmed", async () => {
      render(SettingsForm);
      const poll = await screen.findByDisplayValue("60s");
      await fireEvent.input(poll, { target: { value: "30s" } });

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));

      expect(confirm.request?.title).toBe("Discard changes?");
      expect(confirm.request?.confirmLabel).toBe("Discard");
      expect(closeOverlay).not.toHaveBeenCalled();
    });

    it("closes once the discard is confirmed", async () => {
      render(SettingsForm);
      const poll = await screen.findByDisplayValue("60s");
      await fireEvent.input(poll, { target: { value: "30s" } });

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
      confirm.accept(); // the dialog's "Discard" button

      expect(closeOverlay).toHaveBeenCalledTimes(1);
    });
  });

  // A failed save used to vanish into the footer flash behind this backdrop; it
  // now stays visible inline until the next attempt.
  describe("save errors", () => {
    it("renders a rejected save inline and keeps the modal open", async () => {
      SaveSettings.mockRejectedValueOnce(new Error("SaveSettings: poll_interval is not a duration\n  (check [defaults])"));
      render(SettingsForm);
      await screen.findByDisplayValue("60s");

      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      expect(await screen.findByText(/poll_interval is not a duration/)).toBeInTheDocument();
      expect(closeOverlay).not.toHaveBeenCalled();
    });

    it("dismisses the inline error on request", async () => {
      SaveSettings.mockRejectedValueOnce(new Error("boom"));
      render(SettingsForm);
      await screen.findByDisplayValue("60s");

      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
      await screen.findByText(/boom/);
      await fireEvent.click(screen.getByRole("button", { name: /dismiss error/i }));

      expect(screen.queryByText(/boom/)).not.toBeInTheDocument();
    });
  });

  // The Linear API key. It was settable ONLY in the first-run wizard: neither
  // this form nor the TUI's had a field for it, so a hand-written config could
  // never gain a key and rotating one meant editing the Keychain by hand — while
  // a daemon without a key fails every poll.
  describe("Linear API key", () => {
    async function openLinearTab() {
      render(SettingsForm);
      await screen.findByDisplayValue("60s");
      await fireEvent.click(screen.getByRole("tab", { name: "Linear" }));
      return screen.findByLabelText("Linear API key");
    }

    it("reports where the key lives without ever showing it", async () => {
      await openLinearTab();
      expect(await screen.findByText("✓ Key configured")).toBeInTheDocument();
      expect(screen.getByText(/macOS Keychain \(lola-linear\)/)).toBeInTheDocument();
    });

    // The state the app exists to surface: a config naming a source that yields
    // nothing means every poll fails.
    it("calls out a configured-but-unreadable key", async () => {
      LinearKeyStatus.mockResolvedValue({
        configured: true,
        resolvable: false,
        source: "environment variable LINEAR_API_KEY",
        detail: "environment variable LINEAR_API_KEY is empty",
      });
      await openLinearTab();
      expect(await screen.findByText("✗ Key configured but unreadable")).toBeInTheDocument();
    });

    it("warns when no key is configured at all", async () => {
      LinearKeyStatus.mockResolvedValue({ configured: false, resolvable: false, source: "", detail: "" });
      await openLinearTab();
      expect(await screen.findByText("▲ No key configured")).toBeInTheDocument();
    });

    // A password input, not a text one: the value must not be readable over a
    // shoulder or in a screenshot of the settings overlay.
    it("masks the input", async () => {
      const input = await openLinearTab();
      expect(input).toHaveAttribute("type", "password");
    });

    it("validates a typed key against Linear before it is stored", async () => {
      const input = await openLinearTab();
      await fireEvent.input(input, { target: { value: "lin_api_secret" } });
      await fireEvent.click(screen.getByRole("button", { name: "Validate" }));

      expect(ValidateLinearKey).toHaveBeenCalledWith("lin_api_secret");
      expect(await screen.findByText("Key is valid.")).toBeInTheDocument();
      expect(SetLinearKey).not.toHaveBeenCalled(); // validating is not saving
    });

    it("surfaces a rejected key instead of storing it", async () => {
      ValidateLinearKey.mockRejectedValueOnce(new Error("401 unauthorized"));
      const input = await openLinearTab();
      await fireEvent.input(input, { target: { value: "bad" } });
      await fireEvent.click(screen.getByRole("button", { name: "Validate" }));

      expect(await screen.findByText(/401 unauthorized/)).toBeInTheDocument();
    });

    // Saved on its own, NOT through SaveSettings: a whole-form commit would carry
    // a secret through every unrelated save, and a validation failure on another
    // tab would silently drop the key just typed.
    it("saves the key on its own and reloads the daemon", async () => {
      const input = await openLinearTab();
      await fireEvent.input(input, { target: { value: "lin_api_secret" } });
      await fireEvent.click(screen.getByRole("button", { name: "Save key" }));

      await waitFor(() => expect(SetLinearKey).toHaveBeenCalledWith("lin_api_secret"));
      expect(SaveSettings).not.toHaveBeenCalled();
      // The daemon reads the key on start and on reload; without this the key is
      // stored but the running daemon keeps failing every poll.
      await waitFor(() => expect(reload).toHaveBeenCalled());
    });

    // Leaving a stored key in a DOM input keeps a live secret on screen for as
    // long as the overlay is open.
    it("clears the field once the key is stored", async () => {
      const input = (await openLinearTab()) as HTMLInputElement;
      await fireEvent.input(input, { target: { value: "lin_api_secret" } });
      await fireEvent.click(screen.getByRole("button", { name: "Save key" }));

      await waitFor(() => expect(input.value).toBe(""));
    });

    it("reports a failed store rather than claiming success", async () => {
      SetLinearKey.mockRejectedValueOnce(new Error("config.toml is not writable"));
      const input = await openLinearTab();
      await fireEvent.input(input, { target: { value: "lin_api_secret" } });
      await fireEvent.click(screen.getByRole("button", { name: "Save key" }));

      expect(await screen.findByText(/config.toml is not writable/)).toBeInTheDocument();
    });

    // Typing a key must not make the FORM dirty — it is not part of the DTO, and
    // a discard prompt about it would be about changes the form does not hold.
    it("does not arm the unsaved-changes guard", async () => {
      const input = await openLinearTab();
      await fireEvent.input(input, { target: { value: "lin_api_secret" } });

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
      expect(closeOverlay).toHaveBeenCalled();
    });
  });

  // --- connecting a phone: the reveal is a bearer credential ----------------

  it("hides a revealed connect code by itself after the reveal window", async () => {
    // The exposure this bounds is the one the app can actually control: a code
    // left up in a share, a recording, or in front of someone walking past. It
    // used to stay on screen until a human pressed Hide, which for a credential
    // with no TTL, no single use and no per-device revocation is the wrong
    // default in the wrong direction.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      render(SettingsForm);
      await screen.findByDisplayValue("60s");
      await fireEvent.click(screen.getByRole("tab", { name: "Remote" }));
      await fireEvent.click(screen.getByRole("button", { name: "Show code" }));

      await waitFor(() => expect(ConnectCode).toHaveBeenCalled());
      expect(await screen.findByText(/Hides in/)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Hide" })).toBeInTheDocument();

      await vi.advanceTimersByTimeAsync(91_000);
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Show code" })).toBeInTheDocument(),
      );
      expect(screen.queryByText(/Hides in/)).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("says the copy hands over the key, not just the code", async () => {
    render(SettingsForm);
    await screen.findByDisplayValue("60s");
    await fireEvent.click(screen.getByRole("tab", { name: "Remote" }));
    await fireEvent.click(screen.getByRole("button", { name: "Show code" }));
    await waitFor(() => expect(ConnectCode).toHaveBeenCalled());
    // The key row is still masked at this point, which used to imply the mask
    // was a barrier to more than shoulder-surfing.
    expect(await screen.findByRole("button", { name: /Copy code \(contains the key\)/ }))
      .toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show key" })).toBeInTheDocument();
  });
});