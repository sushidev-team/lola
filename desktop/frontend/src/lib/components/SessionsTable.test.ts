import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/svelte";
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
// vocabulary. It moved to <AgentActivity> as a live pulse plus plain language.
// theme.ts still exports agentBadge (it is a pinned port of the TUI's theme.go),
// so nothing stops a future edit from rendering it again; these pin it out.
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
