import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const Checkpoints = vi.fn();
const CheckpointDiff = vi.fn();
const RestoreCheckpoint = vi.fn();
const ForkCheckpoint = vi.fn();
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Checkpoints: (...a: unknown[]) => Checkpoints(...a),
    CheckpointDiff: (...a: unknown[]) => CheckpointDiff(...a),
    RestoreCheckpoint: (...a: unknown[]) => RestoreCheckpoint(...a),
    ForkCheckpoint: (...a: unknown[]) => ForkCheckpoint(...a),
    Alive: vi.fn().mockResolvedValue(false),
    Sessions: vi.fn(),
    Projects: vi.fn(),
    Status: vi.fn(),
  },
  ConfigService: {},
  TermService: {},
}));

const { default: CheckpointsView } = await import("./CheckpointsView.svelte");
const { default: CheckpointList } = await import("./CheckpointList.svelte");
const { confirm } = await import("$lib/confirm.svelte");
const { checkpoints } = await import("$lib/checkpoints.svelte");
const { terms, CHECKPOINTS } = await import("$lib/terms.svelte");

// The sidebar list and the main pane's diff share one store; render both, as
// SessionEmbed does, and load the list the way its effect would.
async function renderBoth() {
  render(CheckpointList, { sessionId: "s1" });
  render(CheckpointsView, { sessionId: "s1" });
  await checkpoints.load("s1");
}

const LIST = {
  session: "s1",
  restorable: true,
  checkpoints: [
    { seq: 1, sha: "aaaaaaaa1111", head: "h", label: "start", created: "2026-10-09T10:00:00Z" },
    { seq: 2, sha: "bbbbbbbb2222", head: "h", label: "turn 1", created: "2026-10-09T10:05:00Z" },
  ],
};

const DIFF = {
  session: "s1",
  base: "checkpoint #1",
  mergeBase: "aaaaaaaa1111",
  files: [{ path: "a.go", status: "modified", additions: 1, deletions: 1, patch: "@@ -1 +1 @@\n-old\n+new\n" }],
};

describe("checkpoints sidebar + diff pane", () => {
  beforeEach(() => {
    cleanup();
    confirm.cancel();
    Checkpoints.mockReset().mockResolvedValue(LIST);
    CheckpointDiff.mockReset().mockResolvedValue(DIFF);
    RestoreCheckpoint.mockReset().mockResolvedValue({ seq: 1, safety: 3, message: "restored checkpoint #1" });
    ForkCheckpoint.mockReset().mockResolvedValue({ sessionId: "fork-1", message: "forked" });
    checkpoints.select("s1", 0);
  });

  it("lists checkpoints newest first and shows the newest turn's diff", async () => {
    await renderBoth();
    const list = screen.getByRole("list", { name: "checkpoints" });
    await waitFor(() => expect(list.textContent?.indexOf("turn 1")).toBeLessThan(list.textContent!.indexOf("start")));
    await waitFor(() => expect(CheckpointDiff).toHaveBeenCalledWith("s1", 2));
    expect(await screen.findByText("new")).toBeInTheDocument();
    expect(screen.getByText(/against checkpoint #1/)).toBeInTheDocument();
  });

  it("a row opens that checkpoint's diff in the main pane", async () => {
    await renderBoth();
    await fireEvent.click(screen.getByRole("button", { name: /start/ }));
    expect(terms.activeTab("s1")).toBe(CHECKPOINTS);
    await waitFor(() => expect(CheckpointDiff).toHaveBeenLastCalledWith("s1", 1));
  });

  it("restores only after the confirmation, naming the checkpoint", async () => {
    await renderBoth();
    await screen.findByText("new");
    await fireEvent.click(screen.getByRole("button", { name: /start/ }));
    await fireEvent.click(screen.getAllByRole("button", { name: "Restore…" })[0]);
    expect(RestoreCheckpoint).not.toHaveBeenCalled();
    expect(confirm.request?.title).toBe("Restore checkpoint #1?");
    confirm.accept();
    await waitFor(() => expect(RestoreCheckpoint).toHaveBeenCalledWith("s1", 1));
  });

  it("disables restore while the agent is mid-turn", async () => {
    Checkpoints.mockResolvedValue({ ...LIST, restorable: false });
    await renderBoth();
    await screen.findByText("new");
    for (const b of screen.getAllByRole("button", { name: "Restore…" })) expect(b).toBeDisabled();
    for (const b of screen.getAllByRole("button", { name: "Fork…" })) expect(b).toBeEnabled();
  });

  it("forks from the selected checkpoint", async () => {
    await renderBoth();
    await screen.findByText("new");
    await fireEvent.click(screen.getAllByRole("button", { name: "Fork…" })[0]);
    confirm.accept();
    await waitFor(() => expect(ForkCheckpoint).toHaveBeenCalledWith("s1", 2, ""));
  });

  it("explains an empty list", async () => {
    Checkpoints.mockResolvedValue({ session: "s1", restorable: true, checkpoints: [] });
    await renderBoth();
    expect(await screen.findByText(/one is recorded each time the agent ends a turn/)).toBeInTheDocument();
    expect(CheckpointDiff).not.toHaveBeenCalled();
  });
});
