import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const BestOfCompare = vi.fn();
const BestOfPick = vi.fn();
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    BestOfCompare: (...a: unknown[]) => BestOfCompare(...a),
    BestOfPick: (...a: unknown[]) => BestOfPick(...a),
    Alive: vi.fn().mockResolvedValue(false),
    Sessions: vi.fn(),
    Projects: vi.fn(),
    Status: vi.fn(),
    OpenURL: vi.fn(),
  },
  ConfigService: {},
  TermService: {},
}));

const { default: BestOfOverlay } = await import("./BestOfOverlay.svelte");
const { bestOf } = await import("$lib/bestof.svelte");
const { confirm } = await import("$lib/confirm.svelte");

const GROUP = {
  group: "fe-5-x",
  issue: "FE-5",
  title: "Fix the login flow",
  project: "web",
  suggested: "lola-web-fe-5-b2",
  attempts: [
    {
      session: "lola-web-fe-5-b1",
      attempt: 1,
      agent: "claude",
      model: "opus",
      branch: "lola/fe-5-b1",
      display: "working",
      files: 0,
      additions: 0,
      deletions: 0,
      diffError: "no merge-base",
    },
    {
      session: "lola-web-fe-5-b2",
      attempt: 2,
      agent: "codex",
      branch: "lola/fe-5-b2",
      display: "idle",
      delivery: "draft",
      prNumber: 12,
      prUrl: "https://github.com/acme/web/pull/12",
      prState: "OPEN",
      draft: true,
      checks: "pass",
      files: 3,
      additions: 40,
      deletions: 2,
      review: { passes: 1, blocker: 0, major: 0, minor: 0, other: 0 },
    },
    {
      session: "lola-web-fe-5-b3",
      attempt: 3,
      agent: "claude",
      branch: "lola/fe-5-b3",
      display: "gone",
      prNumber: 13,
      prUrl: "https://github.com/acme/web/pull/13",
      prState: "OPEN",
      draft: true,
      checks: "fail",
      files: 9,
      additions: 300,
      deletions: 20,
      review: { passes: 1, blocker: 2, major: 1, minor: 3, other: 0 },
    },
  ],
};

describe("BestOfOverlay", () => {
  beforeEach(() => {
    cleanup();
    confirm.cancel();
    BestOfCompare.mockReset().mockResolvedValue(GROUP);
    BestOfPick.mockReset().mockResolvedValue({ winner: "lola-web-fe-5-b2", pr: 12, message: "kept lola-web-fe-5-b2" });
  });

  it("lays the attempts side by side and marks the suggestion", async () => {
    bestOf.open("lola-web-fe-5-b1");
    render(BestOfOverlay);
    await screen.findByText("claude@opus");
    expect(BestOfCompare).toHaveBeenCalledWith("lola-web-fe-5-b1");
    expect(screen.getByText("clean")).toBeInTheDocument();
    expect(screen.getByText("2B")).toBeInTheDocument();
    expect(screen.getByText("gone")).toBeInTheDocument();
    expect(screen.getByTitle(/suggested/)).toBeInTheDocument();
    expect(screen.getByTitle("no merge-base")).toBeInTheDocument();
  });

  it("keeps an attempt only after the confirmation", async () => {
    bestOf.open("lola-web-fe-5-b1");
    render(BestOfOverlay);
    await screen.findByText("claude@opus");
    const keeps = screen.getAllByRole("button", { name: "Keep" });
    await fireEvent.click(keeps[1]);
    expect(BestOfPick).not.toHaveBeenCalled();
    expect(confirm.request?.title).toBe("Keep this attempt?");
    expect(confirm.request?.detail).toContain("#13");
    confirm.accept();
    await waitFor(() => expect(BestOfPick).toHaveBeenCalledWith("lola-web-fe-5-b2"));
  });

  it("hides the Keep buttons once a pick was made", async () => {
    BestOfCompare.mockResolvedValue({
      ...GROUP,
      picked: "lola-web-fe-5-b2",
      attempts: GROUP.attempts.map((a) => ({ ...a, winner: a.attempt === 2 })),
    });
    bestOf.open("lola-web-fe-5-b2");
    render(BestOfOverlay);
    await screen.findByText(/Kept lola-web-fe-5-b2/);
    expect(screen.queryByRole("button", { name: "Keep" })).toBeNull();
  });
});
