import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach } from "vitest";

// The service layer is the only external dependency PollForm touches. Mock the
// generated bindings (never a live daemon) so we can exercise load + save.
const getPoll = vi.fn();
const savePoll = vi.fn();

vi.mock("@bindings/desktop", () => ({
  // ConfigService is used directly by PollForm; DaemonService is pulled in by
  // the store module at import time but never called in this test.
  ConfigService: {
    GetPoll: (...a: unknown[]) => getPoll(...a),
    SavePoll: (...a: unknown[]) => savePoll(...a),
  },
  DaemonService: {},
}));

// The store subscribes to Wails push events on start(); stub the runtime so the
// import graph resolves under jsdom.
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
    projectId: "proj-uuid-2",
    cycleMode: "active",
    cycleId: "",
    stateIds: ["state-1", "state-2"],
    matchLabels: ["bug"],
    matchMode: "any",
    assigneeMode: "me",
    assigneeUserId: "",
    concurrencyCap: 3,
    dedupMode: "label",
    onSentSetLabel: "sent",
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

describe("PollForm", () => {
  beforeEach(() => {
    getPoll.mockReset();
    savePoll.mockReset();
    nav.overlayProject = "acme";
  });

  it("shows the UUID note and titles the modal by project", () => {
    getPoll.mockResolvedValue(sampleDto());
    render(PollForm);
    expect(getPoll).toHaveBeenCalledWith("acme");
    expect(screen.getByText(/Linear IDs are UUIDs/i)).toBeInTheDocument();
    expect(screen.getByText("polls: acme")).toBeInTheDocument();
  });

  it("loads the DTO into the form fields", async () => {
    getPoll.mockResolvedValue(sampleDto());
    render(PollForm);
    await waitFor(() => expect(screen.getByDisplayValue("team-uuid-1")).toBeInTheDocument());
    expect(screen.getByDisplayValue("proj-uuid-2")).toBeInTheDocument();
    // Array fields render one UUID per line in a textarea (read .value directly —
    // getByDisplayValue collapses the newline via whitespace normalization).
    const statesArea = screen.getByPlaceholderText(
      "one workflow-state UUID per line",
    ) as HTMLTextAreaElement;
    expect(statesArea.value).toBe("state-1\nstate-2");
    // Both group headers are present.
    expect(screen.getByText("Filter")).toBeInTheDocument();
    expect(screen.getByText("Write-back")).toBeInTheDocument();
  });

  it("saves a cleaned DTO via SavePoll", async () => {
    getPoll.mockResolvedValue(sampleDto());
    savePoll.mockResolvedValue(undefined);
    render(PollForm);
    await waitFor(() => expect(screen.getByDisplayValue("team-uuid-1")).toBeInTheDocument());

    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(savePoll).toHaveBeenCalledTimes(1));
    const arg = savePoll.mock.calls[0][0] as ReturnType<typeof sampleDto>;
    expect(arg.stateIds).toEqual(["state-1", "state-2"]);
    expect(arg.matchLabels).toEqual(["bug"]);
    expect(arg.concurrencyCap).toBe(3);
    expect(arg.prRequiresChecks).toBe(true);
  });
});
