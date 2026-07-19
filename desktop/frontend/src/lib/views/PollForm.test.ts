import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock the bindings (never a live daemon/Linear). vi.hoisted so the fns exist
// when the hoisted vi.mock factories run.
const { getPoll, savePoll, teamsFn, teamMetaFn } = vi.hoisted(() => ({
  getPoll: vi.fn(),
  savePoll: vi.fn(),
  teamsFn: vi.fn(),
  teamMetaFn: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    GetPoll: (...a: unknown[]) => getPoll(...a),
    SavePoll: (...a: unknown[]) => savePoll(...a),
  },
  LinearService: {
    Teams: (...a: unknown[]) => teamsFn(...a),
    TeamMeta: (...a: unknown[]) => teamMetaFn(...a),
  },
  DaemonService: {},
}));

// The store imports the Wails runtime at module load; stub it under jsdom.
vi.mock("@wailsio/runtime", () => ({
  Events: { On: () => {}, Emit: () => {} },
  Call: {},
  Create: {},
  CancellablePromise: class {},
}));

import PollForm from "./PollForm.svelte";
import { nav } from "$lib/nav.svelte";

function sampleDto() {
  return {
    project: "acme",
    enabled: true,
    teamId: "team-uuid-1",
    projectId: "",
    cycleMode: "active",
    cycleId: "",
    stateIds: ["state-1"],
    matchLabels: [],
    matchMode: "any",
    assigneeMode: "me",
    assigneeUserId: "",
    concurrencyCap: 3,
    dedupMode: "label",
    onSentSetLabel: "",
    onSpawnStateId: "",
    onPrStateId: "",
    onMergedStateId: "",
    blockedLabelId: "",
    commentOnSpawn: false,
    commentOnPr: false,
    commentOnMerged: false,
    commentOnBlocked: false,
    prRequiresChecks: true,
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
  labels: [{ id: "lab-1", label: "bug" }],
  members: [],
};

describe("PollForm", () => {
  beforeEach(() => {
    getPoll.mockReset().mockResolvedValue(sampleDto());
    savePoll.mockReset().mockResolvedValue(undefined);
    teamsFn.mockReset().mockResolvedValue([{ id: "team-uuid-1", key: "ENG", name: "Engineering" }]);
    teamMetaFn.mockReset().mockResolvedValue(meta);
    nav.overlayProject = "acme";
  });

  it("titles the modal by project and loads the poll", async () => {
    render(PollForm);
    expect(getPoll).toHaveBeenCalledWith("acme");
    expect(await screen.findByText("polls: acme")).toBeInTheDocument();
    expect(screen.getByText("Filter")).toBeInTheDocument();
  });

  it("loads team metadata and renders workflow states as checkboxes, pre-checked from the DTO", async () => {
    render(PollForm);
    // Team metadata loads on mount because the DTO already has a teamId.
    await waitFor(() => expect(teamMetaFn).toHaveBeenCalledWith("team-uuid-1", false));
    const todo = (await screen.findByRole("checkbox", { name: "Todo" })) as HTMLInputElement;
    const doing = screen.getByRole("checkbox", { name: "Doing" }) as HTMLInputElement;
    expect(todo.checked).toBe(true); // state-1 is in dto.stateIds
    expect(doing.checked).toBe(false);
  });

  it("toggling a state and saving sends the cleaned DTO via SavePoll", async () => {
    render(PollForm);
    const doing = (await screen.findByRole("checkbox", { name: "Doing" })) as HTMLInputElement;
    await fireEvent.click(doing); // add state-2

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(savePoll).toHaveBeenCalledTimes(1));
    const arg = savePoll.mock.calls[0][0] as ReturnType<typeof sampleDto>;
    expect(arg.stateIds.sort()).toEqual(["state-1", "state-2"]);
    expect(arg.concurrencyCap).toBe(3);
    expect(arg.prRequiresChecks).toBe(true);
  });

  it("falls back to raw inputs when Linear metadata is unavailable", async () => {
    teamsFn.mockRejectedValueOnce(new Error("no api key"));
    teamMetaFn.mockRejectedValueOnce(new Error("no api key"));
    render(PollForm);
    // With no team list, the team field is a raw text input holding the UUID.
    await waitFor(() => expect(screen.getByDisplayValue("team-uuid-1")).toBeInTheDocument());
  });
});
