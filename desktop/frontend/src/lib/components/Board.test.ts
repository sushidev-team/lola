import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/svelte";
import BoardChip from "./BoardChip.svelte";
import BoardPanel from "./BoardPanel.svelte";
import AgentActivity from "./AgentActivity.svelte";
import ClaimFlag from "./ClaimFlag.svelte";
import type { SessionInfo } from "$lib/store.svelte";
import type { BoardInfo } from "$lib/board";
import { boardStale, hasChip, chipRelevant, progressText, BOARD_STALE_MS } from "$lib/board";

const fresh = () => new Date().toISOString();

function board(over: Partial<BoardInfo> = {}): BoardInfo {
  return { percent: 0, done: 0, total: 0, updatedAt: fresh(), updatedAgo: "1m", ...over } as BoardInfo;
}

function sess(b?: BoardInfo, over: Partial<SessionInfo> = {}): SessionInfo {
  return { id: "s1", agentState: "idle", board: b, ...over } as SessionInfo;
}

describe("board helpers", () => {
  it("fades a report once it is old, or when it has no timestamp", () => {
    const now = Date.now();
    expect(boardStale(board({ updatedAt: new Date(now - 60_000).toISOString() }), now)).toBe(false);
    expect(boardStale(board({ updatedAt: new Date(now - BOARD_STALE_MS - 1).toISOString() }), now)).toBe(true);
    expect(boardStale(board({ updatedAt: undefined }), now)).toBe(true);
  });

  it("counts a plan-derived bar and a percentage differently", () => {
    expect(progressText(board({ progressDerived: true, done: 2, total: 5, percent: 40 }))).toBe("2/5");
    expect(progressText(board({ percent: 60 }))).toBe("60%");
  });

  it("drops the plan chip once a PR exists, but never a blocker", () => {
    expect(chipRelevant(board({ hasProgress: true, percent: 100 }), 0)).toBe(true);
    expect(chipRelevant(board({ hasProgress: true, percent: 100 }), 7)).toBe(false);
    expect(chipRelevant(board({ blocked: "need a key" }), 7)).toBe(true);
  });

  it("has no chip for a board that only carries a note", () => {
    expect(hasChip(board({ note: "hi" }))).toBe(false);
    expect(hasChip(undefined)).toBe(false);
  });
});

describe("BoardChip", () => {
  beforeEach(() => cleanup());

  it("leads with the blocker, the one thing a human must act on", () => {
    render(BoardChip, { session: sess(board({ blocked: "need a key", hasProgress: true, percent: 50 })) });
    expect(screen.getByText("Blocked")).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("draws the bar with its value and count", () => {
    render(BoardChip, { session: sess(board({ hasProgress: true, progressDerived: true, percent: 40, done: 2, total: 5 })) });
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "40");
    expect(screen.getByText("2/5")).toBeInTheDocument();
  });

  it("renders nothing without a board", () => {
    const { container } = render(BoardChip, { session: sess() });
    expect(container.textContent?.trim()).toBe("");
  });
});

describe("BoardPanel", () => {
  beforeEach(() => cleanup());

  it("shows the whole report, labelled as the agent's", () => {
    render(BoardPanel, {
      session: sess(
        board({
          phase: "testing",
          hasProgress: true,
          progressDerived: true,
          percent: 50,
          done: 1,
          total: 2,
          todos: [
            { text: "build it", state: "done" },
            { text: "test it", state: "active" },
          ],
          checks: [{ name: "lint", state: "fail", summary: "3 issues" }],
          note: "almost there",
          blocked: "need the staging key",
        }),
      ),
    });
    expect(screen.getByRole("region", { name: "Agent report" })).toBeInTheDocument();
    expect(screen.getByText("testing")).toBeInTheDocument();
    expect(screen.getByText("need the staging key")).toBeInTheDocument();
    expect(screen.getByText("build it")).toBeInTheDocument();
    expect(screen.getByText("(active)")).toBeInTheDocument();
    expect(screen.getByText("3 issues")).toBeInTheDocument();
    expect(screen.getByText("almost there")).toBeInTheDocument();
  });

  it("renders text, never markup", () => {
    render(BoardPanel, { session: sess(board({ note: "<b>bold</b>" })) });
    expect(screen.getByText("<b>bold</b>")).toBeInTheDocument();
  });

  it("is absent without a board", () => {
    render(BoardPanel, { session: sess() });
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
  });
});

describe("AgentActivity with a report", () => {
  beforeEach(() => cleanup());

  it("puts a reported blocker ahead of the interpreter's headline", () => {
    render(AgentActivity, { session: sess(board({ blocked: "need a key" }), { headline: "editing files" }) });
    expect(screen.getByText(/need a key/)).toBeInTheDocument();
    expect(screen.queryByText(/editing files/)).not.toBeInTheDocument();
  });

  it("falls back to the current plan item when nothing else has a sentence", () => {
    render(AgentActivity, { session: sess(board({ current: "write the migration" })) });
    expect(screen.getByText(/write the migration/)).toBeInTheDocument();
  });
});

describe("BoardPanel motion", () => {
  beforeEach(() => cleanup());
  const plan = () =>
    board({ todos: [{ text: "build", state: "active" }], hasProgress: true, progressDerived: true, percent: 50, done: 0, total: 1 });

  it("animates only while the agent is working on a fresh report", () => {
    const { container } = render(BoardPanel, { session: sess(plan(), { agentState: "working" }) });
    expect(container.querySelector(".breathe")).not.toBeNull();
    expect(container.querySelector(".shimmer")).not.toBeNull();
  });

  it("sits still for an idle agent", () => {
    const { container } = render(BoardPanel, { session: sess(plan(), { agentState: "idle" }) });
    expect(container.querySelector(".breathe, .ripple, .shimmer")).toBeNull();
  });

  it("sits still for a stale report even mid-turn", () => {
    const old = { ...plan(), updatedAt: new Date(Date.now() - 60 * 60 * 1000).toISOString() };
    const { container } = render(BoardPanel, { session: sess(old, { agentState: "working" }) });
    expect(container.querySelector(".breathe, .ripple, .shimmer")).toBeNull();
  });
});

describe("claim audit", () => {
  beforeEach(() => cleanup());

  const unverified = () =>
    board({
      checks: [
        {
          name: "tests",
          state: "pass",
          evidence: "unverified",
          evidenceNote: "no test command ran before the claim",
        },
      ],
      mismatches: ['"tests" claimed pass, but no test command ran before the claim'],
    });

  it("flags a row whose claim the evidence does not back, even with a PR open", () => {
    render(ClaimFlag, { session: sess(unverified(), { prNumber: 7 }) });
    expect(screen.getByText("Unverified claim")).toBeInTheDocument();
  });

  it("stays silent when nothing disagrees", () => {
    const { container } = render(ClaimFlag, { session: sess(board({ checks: [{ name: "tests", state: "pass" }] })) });
    expect(container.textContent?.trim()).toBe("");
  });

  it("names the mismatch and tags the check in the sidebar", () => {
    render(BoardPanel, { session: sess(unverified()) });
    expect(screen.getByText("Claim not backed by evidence")).toBeInTheDocument();
    expect(screen.getByText('"tests" claimed pass, but no test command ran before the claim')).toBeInTheDocument();
    expect(screen.getByText("· no run found")).toBeInTheDocument();
  });
});
