import { render, screen, fireEvent, waitFor, within, cleanup } from "@testing-library/svelte";
import { tick } from "svelte";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mock the bindings (never a live daemon/Linear). vi.hoisted so the fns exist
// when the hoisted vi.mock factories run.
const { getProject, saveProject, removeProject, getSettings, inspectPath, pickFolder, teamsFn, teamMetaFn, renameProject } = vi.hoisted(
  () => ({
    getProject: vi.fn(),
    saveProject: vi.fn(),
    renameProject: vi.fn(),
    removeProject: vi.fn(),
    getSettings: vi.fn(),
    inspectPath: vi.fn(),
    pickFolder: vi.fn(),
    teamsFn: vi.fn(),
    teamMetaFn: vi.fn(),
  }),
);

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    GetProject: (...a: unknown[]) => getProject(...a),
    SaveProject: (...a: unknown[]) => saveProject(...a),
    RemoveProject: (...a: unknown[]) => removeProject(...a),
    GetSettings: () => getSettings(),
    InspectPath: (...a: unknown[]) => inspectPath(...a),
    PickFolder: (...a: unknown[]) => pickFolder(...a),
  },
  LinearService: {
    Teams: (...a: unknown[]) => teamsFn(...a),
    TeamMeta: (...a: unknown[]) => teamMetaFn(...a),
  },
  DaemonService: {
    RenameProject: (...a: unknown[]) => renameProject(...a),
  },
}));

// The store imports the Wails runtime at module load; stub it under jsdom.
vi.mock("@wailsio/runtime", () => ({
  Events: { On: () => {}, Emit: () => {} },
  Call: {},
  Create: {},
  CancellablePromise: class {},
}));

import ProjectForm from "./ProjectForm.svelte";
import { nav } from "$lib/nav.svelte";
import { confirm } from "$lib/confirm.svelte";

// A project that overrides post-create/env/blocked-label but inherits
// symlinks/match-labels/match-mode from [defaults] — so one form exercises both
// sides of the inherit chip.
function sampleDto() {
  return {
    name: "acme",
    label: "",
    path: "/Users/me/code/acme",
    repo: "acme/acme",
    defaultBranch: "main",
    branchPrefix: "acme/",
    agent: "claude",
    symlinks: ["inherited-link"],
    postCreate: ["npm ci"],
    env: ["KEY=own"],

    enabled: true,
    teamId: "team-uuid-1",
    projectId: "proj-1",
    cycleMode: "active",
    cycleId: "",
    stateIds: ["state-1"],
    matchLabels: ["lab-default"],
    matchMode: "all",
    assigneeMode: "user",
    assigneeUserId: "user-1",
    concurrencyCap: 3,
    dedupMode: "label",
    onSentSetLabel: "",

    onSpawnStateId: "state-2",
    onPrStateId: "",
    onMergedStateId: "",
    blockedLabelId: "lab-1",
    commentOnSpawn: false,
    commentOnPr: false,
    commentOnMerged: false,
    commentOnBlocked: false,
    prRequiresChecks: true,

    inherits: {
      symlinks: true,
      postCreate: false,
      env: false,
      matchLabels: true,
      matchMode: true,
      onSentSetLabel: true,
      blockedLabelId: false,
      dedupMode: false,
      prioritySort: true,
    },
    isNew: false,
  };
}

// [defaults] — what a "revert to inherit" must refill the control with.
function settingsDto() {
  return {
    symlinks: ["inherited-link"],
    postCreate: ["make setup", "make build"],
    env: ["SHARED=1"],
    matchLabels: ["lab-default"],
    matchMode: "any",
    onSentSetLabel: "lab-sent",
    blockedLabelId: "lab-blocked",
    dedupMode: "seen",
    prioritySort: ["priority"],
    branchPrefix: "lola/",
    defaultsTeamId: "team-uuid-1",
  };
}

const meta = {
  projects: [{ id: "proj-1", label: "Platform" }],
  cycles: [],
  activeCycleId: "",
  states: [
    { id: "state-1", label: "Todo" },
    { id: "state-2", label: "Doing" },
  ],
  labels: [
    { id: "lab-1", label: "bug" },
    { id: "lab-default", label: "agent" },
  ],
  members: [{ id: "user-1", label: "Ada" }],
};

/** One InspectPath answer; overrides express "this checkout, but …". */
function pathInfo(over: Record<string, unknown> = {}) {
  return {
    path: "",
    isRepo: false,
    repo: "",
    defaultBranch: "",
    branches: [],
    suggestedLabel: "",
    suggestedId: "",
    ...over,
  };
}

/** The grid row that owns a control, so a chip can be found next to its label. */
function rowOf(control: HTMLElement): HTMLElement {
  return control.closest("div.grid") as HTMLElement;
}

describe("ProjectForm", () => {
  beforeEach(() => {
    cleanup();
    getProject.mockReset().mockResolvedValue(sampleDto());
    saveProject.mockReset().mockResolvedValue(undefined);
    renameProject.mockReset().mockResolvedValue({ from: "", to: "", blockers: [] });
    removeProject.mockReset().mockResolvedValue(undefined);
    getSettings.mockReset().mockResolvedValue(settingsDto());
    inspectPath.mockReset().mockResolvedValue(pathInfo());
    // The folder chooser is only auto-opened for a NEW project; "" is a cancel.
    pickFolder.mockReset().mockResolvedValue("");
    teamsFn.mockReset().mockResolvedValue([
      { id: "team-uuid-1", key: "ENG", name: "Engineering" },
      { id: "team-uuid-2", key: "OPS", name: "Operations" },
    ]);
    teamMetaFn.mockReset().mockResolvedValue(meta);
    confirm.cancel(); // the confirm store is a singleton — clear it between tests
    nav.overlayProject = "acme";
    nav.overlayTab = "";
  });

  it("loads the whole project and opens on the Repo tab", async () => {
    render(ProjectForm);
    expect(getProject).toHaveBeenCalledWith("acme");
    expect(await screen.findByLabelText("Path")).toHaveValue("/Users/me/code/acme");
    expect(screen.getByText("project: acme")).toBeInTheDocument();
    expect(screen.queryByLabelText("Branch prefix")).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole("tab", { name: "Worktree setup" }));
    expect(screen.getByLabelText("Custom Branch prefix")).toHaveValue("acme/");
    // Every tab of the merged overlay is reachable.
    for (const t of ["General", "Worktree setup", "Issue pickup", "Linear updates"]) {
      expect(screen.getByRole("tab", { name: t })).toBeInTheDocument();
    }
  });

  it("saves explicit defaults for agent and concurrency", async () => {
    render(ProjectForm);
    await fireEvent.change(await screen.findByLabelText("Agent"), { target: { value: "" } });
    await fireEvent.click(screen.getByRole("tab", { name: "Issue pickup" }));
    await fireEvent.change(screen.getByLabelText("Concurrency"), { target: { value: "default" } });
    expect(screen.queryByLabelText("Concurrency cap")).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    expect(saveProject.mock.calls[0][0]).toMatchObject({ agent: "", concurrencyCap: 0 });
  });

  it("keeps a custom limit editable while cleared and rejects invalid limits", async () => {
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Issue pickup" }));
    const cap = screen.getByLabelText("Concurrency cap");
    for (const value of ["", "0", "1.5"]) {
      await fireEvent.input(cap, { target: { value } });
      expect(screen.getByLabelText("Concurrency cap")).toBe(cap);
      expect(screen.getByRole("button", { name: /^save$/i })).toBeDisabled();
    }
    await fireEvent.input(cap, { target: { value: "7" } });
    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    expect(saveProject.mock.calls[0][0].concurrencyCap).toBe(7);
  });

  it("keeps old label links working and shows labels beside the team filter", async () => {
    nav.overlayTab = "labels";
    render(ProjectForm);
    expect(await screen.findByRole("tab", { name: "Issue pickup" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("Team")).toBeInTheDocument();
    expect(screen.getByText("Match labels")).toBeInTheDocument();
  });

  it("honours a deep link to a tab (nav.overlayTab)", async () => {
    nav.overlayTab = "filter";
    render(ProjectForm);
    expect(await screen.findByRole("tab", { name: "Issue pickup" })).toHaveAttribute("aria-selected", "true");
  });

  it("loads team metadata and renders workflow states as checkboxes, pre-checked from the DTO", async () => {
    render(ProjectForm);
    await waitFor(() => expect(teamMetaFn).toHaveBeenCalledWith("team-uuid-1", false));
    await fireEvent.click(screen.getByRole("tab", { name: "Issue pickup" }));

    const todo = (await screen.findByRole("checkbox", { name: "Todo" })) as HTMLInputElement;
    const doing = screen.getByRole("checkbox", { name: "Doing" }) as HTMLInputElement;
    expect(todo.checked).toBe(true); // state-1 is in dto.stateIds
    expect(doing.checked).toBe(false);
  });

  it("toggling a state and saving sends the cleaned DTO via SaveProject", async () => {
    render(ProjectForm);
    await screen.findByRole("tab", { name: "General" }); // the form is loaded
    await fireEvent.click(screen.getByRole("tab", { name: "Issue pickup" }));

    const doing = (await screen.findByRole("checkbox", { name: "Doing" })) as HTMLInputElement;
    await fireEvent.click(doing); // add state-2

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    const arg = saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>;
    expect([...arg.stateIds].sort()).toEqual(["state-1", "state-2"]);
    expect(arg.concurrencyCap).toBe(3);
    expect(arg.prRequiresChecks).toBe(true);
    // Repo-tab fields ride along — it is one project, one save.
    expect(arg.path).toBe("/Users/me/code/acme");
    // prioritySort has no control; its inherit bit is passed through untouched.
    expect(arg.inherits.prioritySort).toBe(true);
  });

  it("falls back to raw inputs when Linear metadata is unavailable", async () => {
    teamsFn.mockRejectedValueOnce(new Error("no api key"));
    teamMetaFn.mockRejectedValueOnce(new Error("no api key"));
    render(ProjectForm);
    await screen.findByRole("tab", { name: "General" }); // the form is loaded
    await fireEvent.click(screen.getByRole("tab", { name: "Issue pickup" }));

    // With no team list the team field is a raw text input holding the UUID.
    await waitFor(() => expect(screen.getByLabelText("Team")).toHaveValue("team-uuid-1"));
  });

  it("clears the team-scoped IDs when the team changes, but leaves inherited ones alone", async () => {
    render(ProjectForm);
    await screen.findByRole("tab", { name: "General" }); // the form is loaded
    await fireEvent.click(screen.getByRole("tab", { name: "Issue pickup" }));

    const team = await screen.findByLabelText("Team");
    await fireEvent.change(team, { target: { value: "team-uuid-2" } });
    await waitFor(() => expect(teamMetaFn).toHaveBeenCalledWith("team-uuid-2", false));

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    const arg = saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>;

    expect(arg.teamId).toBe("team-uuid-2");
    // A UUID from the old team matches nothing, so every dependent ID is dropped.
    expect(arg.projectId).toBe("");
    expect(arg.stateIds).toEqual([]);
    expect(arg.assigneeUserId).toBe("");
    expect(arg.onSpawnStateId).toBe("");
    expect(arg.blockedLabelId).toBe(""); // overridden here → cleared
    // …except keys whose value belongs to [defaults], not this project.
    expect(arg.matchLabels).toEqual(["lab-default"]);
    expect(arg.inherits.matchLabels).toBe(true);
  });

  it("marks an inherited field and offers an explicit customization action", async () => {
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Worktree setup" }));
    const symlinks = await screen.findByLabelText("Symlinks");
    expect(symlinks.className).toContain("border-dashed");
    expect(within(rowOf(symlinks)).getByRole("button", { name: /^Customize / })).toBeInTheDocument();

    // An overridden neighbour on the same tab chips the other way.
    const postCreate = screen.getByLabelText("Post-create");
    expect(postCreate.className).not.toContain("border-dashed");
    expect(within(rowOf(postCreate)).getByRole("button", { name: /^Use default for / })).toBeInTheDocument();
  });

  it("edits dev_commands as a plain per-project list — no inherit chip", async () => {
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Worktree setup" }));
    const dev = await screen.findByLabelText("Dev commands");
    // dev_commands has no [defaults] counterpart on purpose, so the row carries
    // neither the ghost nor an Inherited/Override chip.
    expect(dev.className).not.toContain("border-dashed");
    expect(within(rowOf(dev)).queryByRole("button", { name: /^Customize / })).not.toBeInTheDocument();
    expect(within(rowOf(dev)).queryByRole("button", { name: /^Use default for / })).not.toBeInTheDocument();

    await fireEvent.input(dev, { target: { value: "composer dev\nnpm run dev" } });
    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    const arg = saveProject.mock.calls.at(-1)![0];
    expect(arg.devCommands).toEqual(["composer dev", "npm run dev"]);
  });

  it("promotes an inherited field to an override when it is edited", async () => {
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Worktree setup" }));
    const symlinks = await screen.findByLabelText("Symlinks");

    await fireEvent.input(symlinks, { target: { value: "own-link" } });

    expect(within(rowOf(symlinks)).getByRole("button", { name: /^Use default for / })).toBeInTheDocument();
    expect(symlinks.className).not.toContain("border-dashed");

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    const arg = saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>;
    expect(arg.inherits.symlinks).toBe(false);
    expect(arg.symlinks).toEqual(["own-link"]);
  });

  it("promotes an inherited field when Customize is clicked", async () => {
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Worktree setup" }));
    const symlinks = await screen.findByLabelText("Symlinks");

    await fireEvent.click(within(rowOf(symlinks)).getByRole("button", { name: /^Customize / }));

    expect(within(rowOf(symlinks)).getByRole("button", { name: /^Use default for / })).toBeInTheDocument();
  });

  it("reverting an override refills the control from [defaults]", async () => {
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Worktree setup" }));
    const postCreate = await screen.findByLabelText("Post-create");
    expect(postCreate).toHaveValue("npm ci");

    await fireEvent.click(within(rowOf(postCreate)).getByRole("button", { name: /^Use default for / }));

    // The field now shows what defaults will actually apply.
    expect(postCreate).toHaveValue("make setup\nmake build");
    expect(postCreate.className).toContain("border-dashed");
    expect(within(rowOf(postCreate)).getByRole("button", { name: /^Customize / })).toBeInTheDocument();

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    const arg = saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>;
    expect(arg.inherits.postCreate).toBe(true);
    expect(arg.postCreate).toEqual(["make setup", "make build"]);
  });

  it("still reverts when [defaults] can't be read, keeping the shown value", async () => {
    getSettings.mockRejectedValueOnce(new Error("no config"));
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Worktree setup" }));
    const postCreate = await screen.findByLabelText("Post-create");

    await fireEvent.click(within(rowOf(postCreate)).getByRole("button", { name: /^Use default for / }));

    expect(within(rowOf(postCreate)).getByRole("button", { name: /^Customize / })).toBeInTheDocument();
    expect(postCreate).toHaveValue("npm ci");
  });

  it("restores a custom draft after comparing it with defaults", async () => {
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Worktree setup" }));
    const input = screen.getByLabelText("Post-create");
    await fireEvent.input(input, { target: { value: "composer install\nnpm ci" } });
    await fireEvent.click(screen.getByRole("button", { name: "Use default for Post-create" }));
    expect(input).toHaveValue("make setup\nmake build");
    await fireEvent.click(screen.getByRole("button", { name: "Customize Post-create" }));
    expect(input).toHaveValue("composer install\nnpm ci");
    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    expect(saveProject.mock.calls[0][0]).toMatchObject({ postCreate: ["composer install", "npm ci"], inherits: { postCreate: false } });
  });

  it("retries team metadata without losing manual IDs or selections", async () => {
    teamMetaFn.mockRejectedValueOnce(new Error("offline"));
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Issue pickup" }));
    await screen.findByRole("button", { name: "Retry team options" });
    await fireEvent.input(screen.getByLabelText("Project"), { target: { value: "manual-project" } });
    await fireEvent.click(screen.getByRole("button", { name: "Retry team options" }));
    await screen.findByRole("checkbox", { name: "Todo" });
    expect(screen.getByLabelText("Project")).toHaveValue("manual-project");
    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    expect(saveProject.mock.calls[0][0]).toMatchObject({ projectId: "manual-project", stateIds: ["state-1"] });
  });

  it("ignores an old team's response after switching teams", async () => {
    let resolveOld!: (value: typeof meta) => void;
    teamMetaFn.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; }));
    teamMetaFn.mockResolvedValue({ ...meta, states: [{ id: "ops-state", label: "Ops ready" }] });
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Issue pickup" }));
    await waitFor(() => expect(teamMetaFn).toHaveBeenCalledTimes(1));
    await fireEvent.change(screen.getByLabelText("Team"), { target: { value: "team-uuid-2" } });
    await screen.findByRole("checkbox", { name: "Ops ready" });
    resolveOld(meta);
    await tick();
    await waitFor(() => expect(screen.queryByRole("checkbox", { name: "Todo" })).not.toBeInTheDocument());
    expect(screen.getByRole("checkbox", { name: "Ops ready" })).toBeInTheDocument();
  });

  it("keeps format hints visible and retains hidden label settings across pickup modes", async () => {
    getProject.mockResolvedValue({ ...sampleDto(), onSentSetLabel: "lab-1", inherits: { ...sampleDto().inherits, onSentSetLabel: false } });
    render(ProjectForm);
    await fireEvent.click(await screen.findByRole("tab", { name: "Worktree setup" }));
    expect(screen.getByLabelText("Env")).toHaveAccessibleDescription("KEY=value, one per line.");
    expect(screen.getByLabelText("Dev commands")).toHaveAccessibleDescription("One command per line.");
    await fireEvent.click(screen.getByRole("tab", { name: "Issue pickup" }));
    await fireEvent.change(screen.getByLabelText("Repeat pickup"), { target: { value: "seen" } });
    expect(screen.queryByLabelText("After pickup label")).not.toBeInTheDocument();
    await fireEvent.change(screen.getByLabelText("Repeat pickup"), { target: { value: "label" } });
    expect(screen.getByLabelText("After pickup label")).toHaveValue("lab-1");
  });

  // One InspectPath pass fills the whole Repo tab: the folder is the only thing
  // the user has to supply.
  describe("folder-driven autofill", () => {
    it("fills an empty repo from the checkout when Path is set", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), repo: "", path: "" });
      inspectPath.mockResolvedValue(pathInfo({ path: "/tmp/web", isRepo: true, repo: "acme/web" }));
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.input(path, { target: { value: "/tmp/web" } });
      await fireEvent.blur(path);

      await waitFor(() => {
        expect(inspectPath).toHaveBeenCalledWith("/tmp/web");
        expect(screen.getByLabelText("Repo")).toHaveValue("acme/web");
      });
      await fireEvent.click(screen.getByRole("button", { name: "More about Repo" }));
      expect(screen.getByRole("note")).toHaveTextContent("detected from the checkout");
    });

    it("never overwrites a repo the user already set", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), repo: "mine/web", path: "" });
      inspectPath.mockResolvedValue(pathInfo({ path: "/tmp/web", isRepo: true, repo: "acme/web" }));
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.input(path, { target: { value: "/tmp/web" } });
      await fireEvent.blur(path);

      await waitFor(() => expect(inspectPath).toHaveBeenCalled());
      expect(screen.getByLabelText("Repo")).toHaveValue("mine/web");
    });

    it("leaves the field empty when the checkout has no GitHub remote", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), repo: "", path: "" });
      inspectPath.mockResolvedValue(pathInfo({ path: "/tmp/plain", isRepo: true })); // fail-closed
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.input(path, { target: { value: "/tmp/plain" } });
      await fireEvent.blur(path);

      await waitFor(() => expect(inspectPath).toHaveBeenCalled());
      expect(screen.getByLabelText("Repo")).toHaveValue("");
      expect(screen.queryByText(/detected from the checkout/)).not.toBeInTheDocument();
    });

    it("says so when the path is not a git checkout", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), path: "" });
      inspectPath.mockResolvedValue(pathInfo({ path: "/tmp/plain", isRepo: false }));
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.input(path, { target: { value: "/tmp/plain" } });
      await fireEvent.blur(path);

      await waitFor(() => expect(screen.getByText(/not a git checkout/)).toBeInTheDocument());
    });

    it("opens the native chooser and adopts the checkout root", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), isNew: true, name: "", label: "", path: "", repo: "", defaultBranch: "main" });
      pickFolder.mockResolvedValue("/Users/me/code/nori-app/src");
      inspectPath.mockResolvedValue(
        pathInfo({
          path: "/Users/me/code/nori-app",
          isRepo: true,
          repo: "acme/nori-app",
          defaultBranch: "develop",
          branches: ["develop", "main"],
          suggestedLabel: "Nori App",
          suggestedId: "nori-app",
        }),
      );
      render(ProjectForm);

      // A NEW project opens straight into the chooser — the folder is the first
      // decision and everything else falls out of it.
      await waitFor(() => expect(pickFolder).toHaveBeenCalled());
      await waitFor(() => expect(screen.getByLabelText("Path")).toHaveValue("/Users/me/code/nori-app"));
      expect(screen.getByLabelText("Label")).toHaveValue("Nori App");
      expect(screen.getByLabelText("ID")).toHaveValue("nori-app");
      expect(screen.getByLabelText("Repo")).toHaveValue("acme/nori-app");
      expect(screen.getByLabelText("Default branch")).toHaveDisplayValue("develop");
    });

    it("does not open the chooser for an existing project", async () => {
      render(ProjectForm);
      await screen.findByLabelText("Path");
      expect(pickFolder).not.toHaveBeenCalled();
    });

    it("keeps an existing project's configured branch and label", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), label: "", defaultBranch: "release", path: "/tmp/web" });
      inspectPath.mockResolvedValue(
        pathInfo({ path: "/tmp/web", isRepo: true, defaultBranch: "main", suggestedLabel: "Web", suggestedId: "web" }),
      );
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.blur(path);
      await waitFor(() => expect(inspectPath).toHaveBeenCalled());
      expect(screen.getByLabelText("Custom Default branch")).toHaveValue("release");
      expect(screen.getByLabelText("Label")).toHaveValue("");
    });
  });

  // The default branch offers the checkout's branches while staying free text.
  describe("default branch", () => {
    it("offers the checkout's branches as suggestions", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), path: "/tmp/web" });
      inspectPath.mockResolvedValue(pathInfo({ path: "/tmp/web", isRepo: true, branches: ["main", "develop"] }));
      render(ProjectForm);

      const branch = await screen.findByLabelText("Default branch");
      await fireEvent.focus(branch);

      await waitFor(() => expect(inspectPath).toHaveBeenCalledWith("/tmp/web"));
      await waitFor(() => {
        const labels = Array.from(branch.querySelectorAll("option")).map((o) => o.textContent);
        expect(labels).toEqual(["main", "develop", "Custom…"]);
      });
    });

    it("stays typable when the path is not a checkout", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), path: "/tmp/plain", defaultBranch: "" });
      inspectPath.mockResolvedValue(pathInfo({ path: "/tmp/plain" }));
      render(ProjectForm);

      const branch = await screen.findByLabelText("Default branch");
      await fireEvent.focus(branch);
      const custom = screen.getByLabelText("Custom Default branch");
      await fireEvent.input(custom, { target: { value: "trunk" } });
      expect(custom).toHaveValue("trunk");
    });
  });

  // A project has two names: `label` is free text nothing keys by, `name` is the
  // id baked into worktree paths and tmux session names. Editing the label is an
  // ordinary save; editing the id is a rename only the daemon may perform.
  describe("label and id", () => {
    it("derives the id from the label on a NEW project", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), name: "", label: "", isNew: true });
      render(ProjectForm);

      const label = await screen.findByLabelText("Label");
      await fireEvent.input(label, { target: { value: "Nori App" } });

      expect(screen.getByLabelText("ID")).toHaveValue("nori-app");
    });

    it("stops deriving once the id is typed by hand", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), name: "", label: "", isNew: true });
      render(ProjectForm);

      const label = await screen.findByLabelText("Label");
      await fireEvent.input(label, { target: { value: "Nori" } });
      const id = screen.getByLabelText("ID");
      await fireEvent.input(id, { target: { value: "nori2" } });
      await fireEvent.input(label, { target: { value: "Nori App" } });

      expect(screen.getByLabelText("ID")).toHaveValue("nori2");
    });

    it("slugs the id as it is typed, keeping a trailing hyphen typable", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), isNew: true });
      render(ProjectForm);

      const id = await screen.findByLabelText("ID");
      await fireEvent.input(id, { target: { value: "My Repo" } });
      expect(id).toHaveValue("my-repo");
      // A trailing separator must survive, or a hyphenated id cannot be entered.
      await fireEvent.input(id, { target: { value: "my-repo-" } });
      expect(id).toHaveValue("my-repo-");
    });

    it("does NOT rename when only the label changes", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), label: "Acme" });
      render(ProjectForm);

      const label = await screen.findByLabelText("Label");
      await fireEvent.input(label, { target: { value: "Acme Web" } });
      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
      expect(renameProject).not.toHaveBeenCalled();
      const arg = saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>;
      expect(arg.label).toBe("Acme Web");
      expect(arg.name).toBe("acme");
    });

    it("drops a label identical to the id", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), label: "" });
      render(ProjectForm);

      const label = await screen.findByLabelText("Label");
      await fireEvent.input(label, { target: { value: "acme" } });
      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
      expect((saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>).label).toBe("");
    });

    it("renames via the daemon BEFORE saving when the id changes", async () => {
      getProject.mockResolvedValue(sampleDto());
      render(ProjectForm);

      const id = await screen.findByLabelText("ID");
      await fireEvent.input(id, { target: { value: "acme-two" } });
      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      await waitFor(() => expect(renameProject).toHaveBeenCalledWith("acme", "acme-two"));
      await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
      // The field save must target the NEW id, not the one the form opened on.
      expect((saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>).name).toBe("acme-two");
      expect(renameProject.mock.invocationCallOrder[0]).toBeLessThan(
        saveProject.mock.invocationCallOrder[0],
      );
    });

    it("aborts the whole save when the daemon refuses the rename", async () => {
      getProject.mockResolvedValue(sampleDto());
      renameProject.mockRejectedValue(
        new Error('renameProject: "acme" still has 1 session (lola-acme-eng-1)'),
      );
      render(ProjectForm);

      const id = await screen.findByLabelText("ID");
      await fireEvent.input(id, { target: { value: "acme-two" } });
      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      await waitFor(() => expect(renameProject).toHaveBeenCalledTimes(1));
      // No partial write: the fields must not be saved against either id.
      expect(saveProject).not.toHaveBeenCalled();
      // ...and the form stays open and re-savable rather than wedging on `saving`.
      expect(screen.getByRole("button", { name: /^save$/i })).toBeEnabled();
    });

    it("removes the project by the id on disk, not a half-typed rename", async () => {
      getProject.mockResolvedValue(sampleDto());
      render(ProjectForm);

      const id = await screen.findByLabelText("ID");
      await fireEvent.input(id, { target: { value: "acme-typo" } });
      await fireEvent.click(screen.getByRole("button", { name: /^remove$/i }));
      await fireEvent.click(screen.getByRole("button", { name: /confirm|yes|remove/i }));

      await waitFor(() => expect(removeProject).toHaveBeenCalledWith("acme"));
    });
  });

  // A mis-click on the big dim backdrop — or a stray Escape — after editing
  // several tabs must not silently drop the edits. Every close path (backdrop, ✕,
  // Escape, this cancel button) runs the same requestClose, so the footer cancel
  // exercises the guard for all of them.
  describe("unsaved-changes guard", () => {
    afterEach(() => vi.restoreAllMocks());

    it("closes a pristine form immediately, with no prompt", async () => {
      render(ProjectForm);
      await screen.findByLabelText("Path"); // fully loaded
      const close = vi.spyOn(nav, "closeOverlay");

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));

      expect(close).toHaveBeenCalledTimes(1);
      expect(confirm.request).toBeNull();
    });

    it("prompts before discarding an edited form and does NOT close until confirmed", async () => {
      render(ProjectForm);
      const label = await screen.findByLabelText("Label");
      await fireEvent.input(label, { target: { value: "Acme Web" } });
      const close = vi.spyOn(nav, "closeOverlay");

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));

      // The confirm dialog is up and the overlay is still open.
      expect(confirm.request?.title).toBe("Discard changes?");
      expect(confirm.request?.confirmLabel).toBe("Discard");
      expect(close).not.toHaveBeenCalled();
    });

    it("closes once the discard is confirmed", async () => {
      render(ProjectForm);
      const label = await screen.findByLabelText("Label");
      await fireEvent.input(label, { target: { value: "Acme Web" } });
      const close = vi.spyOn(nav, "closeOverlay");

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
      confirm.accept(); // the dialog's "Discard" button

      expect(close).toHaveBeenCalledTimes(1);
    });

    it("does not prompt when only the id was normalized back to the same value", async () => {
      // Re-typing the label to what it already was leaves the DTO unchanged, so a
      // close is instant — the guard keys off value, not focus.
      getProject.mockResolvedValue({ ...sampleDto(), label: "Acme" });
      render(ProjectForm);
      const label = await screen.findByLabelText("Label");
      await fireEvent.input(label, { target: { value: "Acme" } });
      const close = vi.spyOn(nav, "closeOverlay");

      await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));

      expect(confirm.request).toBeNull();
      expect(close).toHaveBeenCalledTimes(1);
    });
  });

  // A failed save used to vanish into the footer flash behind this backdrop; it
  // now stays visible inline until the next attempt.
  describe("save errors", () => {
    it("renders a rejected save inline and keeps the modal open", async () => {
      saveProject.mockRejectedValueOnce(new Error("SaveProject: repo owner/name is invalid\n  (line 2)"));
      render(ProjectForm);
      await screen.findByLabelText("Path");
      const close = vi.spyOn(nav, "closeOverlay");

      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

      expect(await screen.findByText(/repo owner\/name is invalid/)).toBeInTheDocument();
      expect(close).not.toHaveBeenCalled();
      // Re-savable, not wedged on `saving`.
      expect(screen.getByRole("button", { name: /^save$/i })).toBeEnabled();

      vi.restoreAllMocks();
    });

    it("dismisses the inline error on request", async () => {
      saveProject.mockRejectedValueOnce(new Error("boom"));
      render(ProjectForm);
      await screen.findByLabelText("Path");

      await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
      await screen.findByText(/boom/);
      await fireEvent.click(screen.getByRole("button", { name: /dismiss error/i }));

      expect(screen.queryByText(/boom/)).not.toBeInTheDocument();
    });
  });
});
