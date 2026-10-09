import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import SessionsTable from "./SessionsTable.svelte";
import { store, type SessionInfo } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";

function fakeSession(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "acme-eng-1",
    issue: "ENG-1",
    title: "fix login",
    project: "acme",
    status: "working",
    reacting: "",
    age: "2m",
    prNumber: 0,
    ...over,
  } as SessionInfo;
}

// The sessions panel used to render one "no sessions observed" line whether the
// daemon was dead, still connecting, or genuinely idle — a dead daemon hid
// behind an empty queue. These pin the three states apart.
describe("SessionsTable empty states", () => {
  beforeEach(() => {
    cleanup();
    store.sessions = [];
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
  });

  it("shows a neutral 'connecting…' before the first push lands", () => {
    store.connected = false;
    store.alive = false;
    render(SessionsTable);
    expect(screen.getByText("connecting…")).toBeInTheDocument();
    // Not the offline call-to-action, and not the idle line.
    expect(screen.queryByText("Start the daemon")).not.toBeInTheDocument();
    expect(screen.queryByText("no sessions observed")).not.toBeInTheDocument();
  });

  it("shows a clear offline state with a start affordance when the daemon is down", () => {
    store.connected = true;
    store.alive = false;
    render(SessionsTable);
    expect(screen.getByText(/daemon isn't running/i)).toBeInTheDocument();
    expect(screen.getByText("Start the daemon")).toBeInTheDocument();
    expect(screen.queryByText("no sessions observed")).not.toBeInTheDocument();
  });

  it("keeps 'no sessions observed' for the genuinely-idle case", () => {
    store.connected = true;
    store.alive = true;
    render(SessionsTable);
    expect(screen.getByText("no sessions observed")).toBeInTheDocument();
    expect(screen.queryByText("Start the daemon")).not.toBeInTheDocument();
    expect(screen.queryByText("connecting…")).not.toBeInTheDocument();
  });

  it("renders rows and no empty state when there are sessions", () => {
    store.connected = true;
    store.alive = true;
    store.sessions = [fakeSession()];
    render(SessionsTable);
    expect(screen.getByText("ENG-1")).toBeInTheDocument();
    expect(screen.queryByText("no sessions observed")).not.toBeInTheDocument();
    expect(screen.queryByText("Start the daemon")).not.toBeInTheDocument();
  });
});

// The agent axis used to hang off the status pill as theme.ts's two-letter
// glyphs — "review ·wk" — which reads as a typo unless you already know the
// vocabulary. The pill IS the agent axis now, and <AgentActivity> carries the
// liveness pulse plus whatever prose the session has about itself. agentBadge
// and its showAgentBadge gate are deleted; these pin the glyphs out for good.
describe("SessionsTable agent activity", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    store.sessions = [];
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.triage = "";
  });

  it("never renders the two-letter agent badge next to the status pill", () => {
    store.sessions = [
      fakeSession({ status: "review_pending", agentState: "working", delivery: "review_pending" }),
    ];
    render(SessionsTable);
    expect(screen.queryByText(/·wk/)).not.toBeInTheDocument();
    expect(screen.queryByText(/·\?!/)).not.toBeInTheDocument();
    expect(screen.queryByText(/·en/)).not.toBeInTheDocument();
  });

  it("announces a running agent for screen readers rather than only animating", () => {
    store.sessions = [fakeSession({ agentState: "working" })];
    render(SessionsTable);
    expect(screen.getByText("agent running")).toBeInTheDocument();
  });

  it("shows the interpreter headline on the row, '≈'-marked as untrusted", () => {
    store.sessions = [fakeSession({ headline: "grepping for the sync button" })];
    render(SessionsTable);
    expect(screen.getByText(/≈ grepping for the sync button/)).toBeInTheDocument();
  });

  it("falls back to the agent's last notification when there is no headline", () => {
    // waiting_input, not idle: SetAgentState clears lastNotification on any
    // transition off waiting_input, so a non-empty message on an idle session is
    // a record the daemon cannot produce.
    store.sessions = [
      fakeSession({ agentState: "waiting_input", lastNotification: "Claude is waiting for your input" }),
    ];
    render(SessionsTable);
    expect(screen.getByText("Claude is waiting for your input")).toBeInTheDocument();
  });

  it("stays quiet when the agent is idle with nothing to report", () => {
    store.sessions = [fakeSession({ agentState: "idle" })];
    render(SessionsTable);
    expect(screen.queryByText("agent running")).not.toBeInTheDocument();
  });
});

// A column was once removed from the body but left in the header, so the table
// rendered 8 <th> over 7 <td> and every cell after the gap sat under the wrong
// heading. Nothing caught it: the empty-state and content tests query by text,
// which is blind to column alignment. This pins the two in step.
describe("SessionsTable column alignment", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    store.sessions = [];
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.triage = "";
  });

  it("renders exactly one body cell per column heading", () => {
    store.sessions = [fakeSession({ prNumber: 7, checks: "pass" })];
    const { container } = render(SessionsTable);
    const heads = container.querySelectorAll("thead th").length;
    const cells = container.querySelectorAll("tbody tr:first-child td").length;
    expect(cells).toBe(heads);
  });

  it("has no 'Reacting' heading — the posture rides with the status pill", () => {
    store.sessions = [fakeSession()];
    render(SessionsTable);
    expect(screen.queryByText("Reacting")).not.toBeInTheDocument();
  });
});

// The agent axis is an icon in the leading column, and attention shows where it
// belongs: an orange glyph + "Needs you" chip when the agent waits on a human, a
// red lifecycle chip when the delivered work regressed. Every state the
// attention predicate covers must still be visibly marked.
describe("SessionsTable agent glyph and attention", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    store.sessions = [];
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.triage = "";
  });

  const glyph = (c: HTMLElement) => c.querySelector("tbody tr:first-child td:first-child") as HTMLElement;
  const status = (c: HTMLElement) => c.querySelectorAll("tbody tr:first-child td")[4] as HTMLElement;

  it("animates only a working agent", () => {
    store.sessions = [fakeSession({ agentState: "working" })];
    const { container } = render(SessionsTable);
    expect(glyph(container).querySelector(".spin")).not.toBeNull();
    cleanup();
    store.sessions = [fakeSession({ agentState: "idle" })];
    const r = render(SessionsTable);
    expect(glyph(r.container).querySelector(".spin")).toBeNull();
    expect(glyph(r.container).textContent).toContain("idle");
  });

  it("names a blocked agent and what it is asking", () => {
    store.sessions = [fakeSession({ agentState: "waiting_input", inputReason: "permission_prompt", delivery: "none" })];
    const { container } = render(SessionsTable);
    expect(glyph(container).querySelector(".bg-orange")).not.toBeNull();
    expect(status(container).textContent).toContain("Needs you");
    expect(status(container).textContent).toContain("permission prompt");
  });

  it("marks a red build in red even though the agent is happily working", () => {
    store.sessions = [fakeSession({ agentState: "working", prNumber: 7, delivery: "ci_failed" })];
    const { container } = render(SessionsTable);
    expect(screen.getByText("CI failing").closest(".text-bad")).not.toBeNull();
    expect(status(container).textContent).not.toContain("Needs you");
  });

  it("folds lola's reaction posture into the chip's wording", () => {
    store.sessions = [
      fakeSession({ id: "a", issue: "ENG-1", prNumber: 7, delivery: "ci_failed", reacting: "escalated" }),
      fakeSession({ id: "b", issue: "ENG-2", prNumber: 8, delivery: "ci_failed", reacting: "ci retry 1/2" }),
    ];
    render(SessionsTable);
    expect(screen.getByText("CI failing · needs you").closest(".text-bad")).not.toBeNull();
    expect(screen.getByText("CI failing · ci retry 1/2").closest(".text-warn")).not.toBeNull();
    expect(screen.queryByText("escalated")).not.toBeInTheDocument();
  });

  it("marks requested changes and a conflicting branch, and the conflict resolves on click", async () => {
    const resolve = vi.spyOn(store, "resolveConflict").mockResolvedValue(undefined as never);
    store.sessions = [
      fakeSession({ id: "a", issue: "ENG-1", agentState: "working", prNumber: 7, delivery: "changes_requested" }),
      fakeSession({ id: "b", issue: "ENG-2", agentState: "idle", prNumber: 8, delivery: "merge_conflict" }),
    ];
    render(SessionsTable);
    expect(screen.getByText("Changes requested").closest(".text-orange")).not.toBeNull();
    await fireEvent.click(screen.getByText("Merge conflict").closest("button")!);
    expect(resolve).toHaveBeenCalledWith("b");
    expect(nav.selectedId).toBe("");
    resolve.mockRestore();
  });

  it("leaves a quiet session unalarmed", () => {
    store.sessions = [fakeSession({ agentState: "idle", prNumber: 7, delivery: "review_pending" })];
    const { container } = render(SessionsTable);
    expect(screen.getByText("Awaiting review")).toBeInTheDocument();
    expect(status(container).querySelector(".text-bad, .text-orange")).toBeNull();
  });
});

// The PR number was the most-pointed-at string in the app and a control on no
// surface at all: opening a PR needed the detail panel, the context menu, or
// knowing that `o` is bound.
describe("SessionsTable PR link", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    store.sessions = [];
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.triage = "";
  });

  it("opens the pull request without also selecting the row", async () => {
    const openURL = vi.spyOn(store, "openURL").mockResolvedValue(undefined);
    store.sessions = [
      fakeSession({ prNumber: 42, prUrl: "https://github.com/acme/eng/pull/42", delivery: "draft" }),
    ];
    render(SessionsTable);
    await fireEvent.click(screen.getByRole("button", { name: "#42" }));
    expect(openURL).toHaveBeenCalledWith("https://github.com/acme/eng/pull/42");
    expect(nav.selectedId).toBe("");
    openURL.mockRestore();
  });

  it("states the delivery state beside the number", () => {
    store.sessions = [fakeSession({ prNumber: 42, prUrl: "u", delivery: "ci_failed" })];
    render(SessionsTable);
    expect(screen.getByText("CI failing")).toBeInTheDocument();
  });
});

// Status, Plan, PR and Activity used to be four columns answering one question.
// They are one Status cell now: a fixed-width grey PR number, then ONE lifecycle
// chip — or, before a PR exists, the agent's own plan chip.
describe("SessionsTable lifecycle chip", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    store.sessions = [];
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.triage = "";
  });

  const plan = { percent: 40, done: 2, total: 5, hasProgress: true, progressDerived: true, updatedAt: new Date().toISOString() };
  const progressCell = (c: HTMLElement) => c.querySelectorAll("tbody tr:first-child td")[4];

  it("has one Status column, and no PR, Plan, Progress or Activity column", () => {
    store.sessions = [fakeSession({ prNumber: 7, checks: "pass" })];
    const { container } = render(SessionsTable);
    const heads = [...container.querySelectorAll("thead th")].map((th) => th.textContent?.trim());
    expect(heads).toEqual(["", "Issue", "Title", "Project", "Status", "Age"]);
  });

  it("shows the agent's plan before there is a PR", () => {
    store.sessions = [fakeSession({ prNumber: 0, board: plan } as never)];
    const { container } = render(SessionsTable);
    expect(progressCell(container).querySelector('[role="progressbar"]')).not.toBeNull();
  });

  it("lets the PR supersede the plan once it exists", () => {
    store.sessions = [fakeSession({ prNumber: 7, checks: "pass", board: plan } as never)];
    const { container } = render(SessionsTable);
    expect(progressCell(container).textContent).toContain("#7");
    expect(progressCell(container).querySelector('[role="progressbar"]')).toBeNull();
  });

  it("draws no em-dash placeholder when there is nothing to show", () => {
    store.sessions = [fakeSession({ prNumber: 0 })];
    const { container } = render(SessionsTable);
    expect(progressCell(container).textContent?.trim()).toBe("");
  });
});
