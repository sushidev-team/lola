import { render, screen, fireEvent, waitFor, within, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock the bindings (never a live daemon/Linear). vi.hoisted so the fns exist
// when the hoisted vi.mock factories run.
const { getProject, saveProject, removeProject, getSettings, detectRepo, branchesFn, teamsFn, teamMetaFn, renameProject } = vi.hoisted(
  () => ({
    getProject: vi.fn(),
    saveProject: vi.fn(),
    renameProject: vi.fn(),
    removeProject: vi.fn(),
    getSettings: vi.fn(),
    detectRepo: vi.fn(),
    branchesFn: vi.fn(),
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
    DetectRepo: (...a: unknown[]) => detectRepo(...a),
    Branches: (...a: unknown[]) => branchesFn(...a),
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
    detectRepo.mockReset().mockResolvedValue("");
    branchesFn.mockReset().mockResolvedValue([]);
    teamsFn.mockReset().mockResolvedValue([
      { id: "team-uuid-1", key: "ENG", name: "Engineering" },
      { id: "team-uuid-2", key: "OPS", name: "Operations" },
    ]);
    teamMetaFn.mockReset().mockResolvedValue(meta);
    nav.overlayProject = "acme";
    nav.overlayTab = "";
  });

  it("loads the whole project and opens on the Repo tab", async () => {
    render(ProjectForm);
    expect(getProject).toHaveBeenCalledWith("acme");
    expect(await screen.findByLabelText("Path")).toHaveValue("/Users/me/code/acme");
    expect(screen.getByText("project: acme")).toBeInTheDocument();
    expect(screen.getByLabelText("Branch prefix")).toHaveValue("acme/");
    // Every tab of the merged overlay is reachable.
    for (const t of ["Repo", "Filter", "Labels", "Write-back"]) {
      expect(screen.getByRole("tab", { name: t })).toBeInTheDocument();
    }
  });

  it("honours a deep link to a tab (nav.overlayTab)", async () => {
    nav.overlayTab = "filter";
    render(ProjectForm);
    expect(await screen.findByRole("tab", { name: "Filter" })).toHaveAttribute("aria-selected", "true");
  });

  it("loads team metadata and renders workflow states as checkboxes, pre-checked from the DTO", async () => {
    render(ProjectForm);
    await waitFor(() => expect(teamMetaFn).toHaveBeenCalledWith("team-uuid-1", false));
    await fireEvent.click(screen.getByRole("tab", { name: "Filter" }));

    const todo = (await screen.findByRole("checkbox", { name: "Todo" })) as HTMLInputElement;
    const doing = screen.getByRole("checkbox", { name: "Doing" }) as HTMLInputElement;
    expect(todo.checked).toBe(true); // state-1 is in dto.stateIds
    expect(doing.checked).toBe(false);
  });

  it("toggling a state and saving sends the cleaned DTO via SaveProject", async () => {
    render(ProjectForm);
    await screen.findByRole("tab", { name: "Repo" }); // the form is loaded
    await fireEvent.click(screen.getByRole("tab", { name: "Filter" }));

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
    await screen.findByRole("tab", { name: "Repo" }); // the form is loaded
    await fireEvent.click(screen.getByRole("tab", { name: "Filter" }));

    // With no team list the team field is a raw text input holding the UUID.
    await waitFor(() => expect(screen.getByLabelText("Team")).toHaveValue("team-uuid-1"));
  });

  it("clears the team-scoped IDs when the team changes, but leaves inherited ones alone", async () => {
    render(ProjectForm);
    await screen.findByRole("tab", { name: "Repo" }); // the form is loaded
    await fireEvent.click(screen.getByRole("tab", { name: "Filter" }));

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

  it("ghosts an inherited field and chips it 'inherited'", async () => {
    render(ProjectForm);
    const symlinks = await screen.findByLabelText("Symlinks");
    expect(symlinks.className).toContain("opacity-55");
    expect(within(rowOf(symlinks)).getByRole("button", { name: "inherited" })).toBeInTheDocument();

    // An overridden neighbour on the same tab chips the other way.
    const postCreate = screen.getByLabelText("Post-create");
    expect(postCreate.className).not.toContain("opacity-55");
    expect(within(rowOf(postCreate)).getByRole("button", { name: "override" })).toBeInTheDocument();
  });

  it("promotes an inherited field to an override when it is edited", async () => {
    render(ProjectForm);
    const symlinks = await screen.findByLabelText("Symlinks");

    await fireEvent.input(symlinks, { target: { value: "own-link" } });

    expect(within(rowOf(symlinks)).getByRole("button", { name: "override" })).toBeInTheDocument();
    expect(symlinks.className).not.toContain("opacity-55");

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    const arg = saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>;
    expect(arg.inherits.symlinks).toBe(false);
    expect(arg.symlinks).toEqual(["own-link"]);
  });

  it("promotes an inherited field when its chip is clicked", async () => {
    render(ProjectForm);
    const symlinks = await screen.findByLabelText("Symlinks");

    await fireEvent.click(within(rowOf(symlinks)).getByRole("button", { name: "inherited" }));

    expect(within(rowOf(symlinks)).getByRole("button", { name: "override" })).toBeInTheDocument();
  });

  it("reverting an override refills the control from [defaults]", async () => {
    render(ProjectForm);
    const postCreate = await screen.findByLabelText("Post-create");
    expect(postCreate).toHaveValue("npm ci");

    await fireEvent.click(within(rowOf(postCreate)).getByRole("button", { name: "override" }));

    // The ghost now shows what [defaults] will actually apply.
    expect(postCreate).toHaveValue("make setup\nmake build");
    expect(postCreate.className).toContain("opacity-55");
    expect(within(rowOf(postCreate)).getByRole("button", { name: "inherited" })).toBeInTheDocument();

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(saveProject).toHaveBeenCalledTimes(1));
    const arg = saveProject.mock.calls[0][0] as ReturnType<typeof sampleDto>;
    expect(arg.inherits.postCreate).toBe(true);
    expect(arg.postCreate).toEqual(["make setup", "make build"]);
  });

  it("still reverts when [defaults] can't be read, keeping the shown value", async () => {
    getSettings.mockRejectedValueOnce(new Error("no config"));
    render(ProjectForm);
    const postCreate = await screen.findByLabelText("Post-create");

    await fireEvent.click(within(rowOf(postCreate)).getByRole("button", { name: "override" }));

    expect(within(rowOf(postCreate)).getByRole("button", { name: "inherited" })).toBeInTheDocument();
    expect(postCreate).toHaveValue("npm ci");
  });

  // Repo auto-detection: filling in Path resolves the checkout's GitHub remote
  // so owner/name does not have to be copied by hand.
  describe("repo auto-detection", () => {
    it("fills an empty repo from the checkout when Path is set", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), repo: "", path: "" });
      detectRepo.mockResolvedValue("acme/web");
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.input(path, { target: { value: "/tmp/web" } });
      await fireEvent.blur(path);

      await waitFor(() => {
        expect(detectRepo).toHaveBeenCalledWith("/tmp/web");
        expect(screen.getByLabelText("Repo")).toHaveValue("acme/web");
      });
      expect(screen.getByText(/detected from the checkout/)).toBeInTheDocument();
    });

    it("never overwrites a repo the user already set", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), repo: "mine/web", path: "" });
      detectRepo.mockResolvedValue("acme/web");
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.input(path, { target: { value: "/tmp/web" } });
      await fireEvent.blur(path);

      await waitFor(() => expect(screen.getByLabelText("Repo")).toHaveValue("mine/web"));
      expect(detectRepo).not.toHaveBeenCalled();
    });

    it("leaves the field empty when the checkout has no GitHub remote", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), repo: "", path: "" });
      detectRepo.mockResolvedValue(""); // fail-closed
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.input(path, { target: { value: "/tmp/plain" } });
      await fireEvent.blur(path);

      await waitFor(() => expect(detectRepo).toHaveBeenCalled());
      expect(screen.getByLabelText("Repo")).toHaveValue("");
      expect(screen.queryByText(/detected from the checkout/)).not.toBeInTheDocument();
    });

    it("resolves a given path only once", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), repo: "", path: "" });
      detectRepo.mockResolvedValue("");
      render(ProjectForm);

      const path = await screen.findByLabelText("Path");
      await fireEvent.input(path, { target: { value: "/tmp/web" } });
      await fireEvent.blur(path);
      await waitFor(() => expect(detectRepo).toHaveBeenCalledTimes(1));
      await fireEvent.blur(path);
      expect(detectRepo).toHaveBeenCalledTimes(1);
    });
  });

  // The default branch offers the checkout's branches while staying free text.
  describe("default branch", () => {
    it("offers the checkout's branches as suggestions", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), path: "/tmp/web" });
      branchesFn.mockResolvedValue(["main", "develop"]);
      render(ProjectForm);

      const branch = await screen.findByLabelText("Default branch");
      await fireEvent.focus(branch);

      await waitFor(() => expect(branchesFn).toHaveBeenCalledWith("/tmp/web"));
      await waitFor(() => {
        const list = document.getElementById("lola-branches");
        const values = Array.from(list?.querySelectorAll("option") ?? []).map((o) => o.value);
        expect(values).toEqual(["main", "develop"]);
      });
    });

    it("stays typable when the path is not a checkout", async () => {
      getProject.mockResolvedValue({ ...sampleDto(), path: "/tmp/plain", defaultBranch: "" });
      branchesFn.mockResolvedValue([]);
      render(ProjectForm);

      const branch = await screen.findByLabelText("Default branch");
      await fireEvent.focus(branch);
      await fireEvent.input(branch, { target: { value: "trunk" } });
      expect(branch).toHaveValue("trunk");
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
});
