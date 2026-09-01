import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

// The grid captures panes over the Wails bridge, absent under jsdom. Stub it so
// the component mounts; CaptureMany resolving {} just leaves tiles frame-less.
const shells = vi.fn(async (_id: string): Promise<string[]> => []);
vi.mock("@bindings/desktop", () => ({
  DaemonService: {},
  ConfigService: {},
  TermService: {
    CaptureMany: vi.fn(async () => ({})),
    Shells: (id: string) => shells(id),
  },
}));

import TerminalGrid from "./TerminalGrid.svelte";
import { store, type SessionInfo } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";
import { terms, AGENT } from "$lib/terms.svelte";

function fakeSession(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "acme-eng-1",
    issue: "ENG-1",
    title: "fix login",
    project: "acme",
    status: "working",
    tmuxName: "acme-eng-1",
    reacting: "",
    age: "2m",
    prNumber: 0,
    ...over,
  } as SessionInfo;
}

describe("TerminalGrid dead-tile marking", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.focusedTerm = "";
    nav.lens = "grid";
    nav.returnLens = "";
    shells.mockResolvedValue([]);
  });

  // A dead / ended session keeps its last captured frame, which looks live. The
  // tile must mark it and offer revive; a live session gets neither.
  it("offers revive only for a dead or ended session, not a working one", () => {
    store.sessions = [
      fakeSession({ id: "acme-dead", issue: "ENG-1", status: "dead", tmuxName: "acme-dead" }),
      fakeSession({ id: "acme-work", issue: "ENG-2", status: "working", tmuxName: "acme-work" }),
    ];
    render(TerminalGrid);

    // Both sessions are tiled…
    expect(screen.getByText("ENG-1")).toBeInTheDocument();
    expect(screen.getByText("ENG-2")).toBeInTheDocument();
    // …but exactly one carries a revive affordance (the dead one).
    expect(screen.getAllByText("Revive")).toHaveLength(1);
  });

  it("marks an ended session too", () => {
    store.sessions = [
      fakeSession({ id: "acme-end", issue: "ENG-3", status: "session_ended", tmuxName: "acme-end" }),
    ];
    render(TerminalGrid);
    expect(screen.getByText("Revive")).toBeInTheDocument();
  });

  it("shows no revive when every tile is live", () => {
    store.sessions = [fakeSession({ status: "working" })];
    render(TerminalGrid);
    expect(screen.queryByText("Revive")).not.toBeInTheDocument();
  });
});

// A session's shells and its review pane are real tmux sessions beside the agent
// pane; the tile lets you point the preview at any of them, and the choice is the
// SAME state the detail panel switches (so opening the terminal lands there).
describe("TerminalGrid tab strip", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.focusedTerm = "";
    nav.lens = "grid";
  });

  it("shows no strip when the agent pane is the session's only one", async () => {
    shells.mockResolvedValue([]);
    store.sessions = [fakeSession()];
    render(TerminalGrid);
    await waitFor(() => expect(shells).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Agent" })).not.toBeInTheDocument();
  });

  it("offers the discovered shells and the review pane, and switching persists in terms", async () => {
    shells.mockResolvedValue(["acme-eng-1-shell-1", "acme-eng-1-review"]);
    store.sessions = [fakeSession()];
    render(TerminalGrid);

    const shell = await screen.findByRole("button", { name: "Shell 1" });
    expect(screen.getByRole("button", { name: "Agent" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Review" })).toBeInTheDocument();
    expect(terms.activeTab("acme-eng-1")).toBe(AGENT);

    await fireEvent.click(shell);
    // The tab store is what the detail panel reads too, so the preview and the
    // terminal you open from it are the same pane.
    expect(terms.activeTab("acme-eng-1")).toBe("acme-eng-1-shell-1");
    // …and the click must not have opened the fullscreen terminal.
    expect(nav.focusedTerm).toBe("");
  });
});

// The grid is the lens you sit and WATCH a build from, and it was the one lens
// that never told you a PR existed — let alone that its CI had gone red.
describe("TerminalGrid PR chip", () => {
  beforeEach(() => {
    cleanup();
    store.connected = true;
    store.alive = true;
    nav.scoped = false;
    nav.project = "";
    nav.selectedId = "";
    nav.focusedTerm = "";
    nav.lens = "grid";
    nav.returnLens = "";
    shells.mockResolvedValue([]);
  });

  it("shows the PR and its delivery state on the tile", () => {
    store.sessions = [
      fakeSession({ prNumber: 42, prUrl: "https://github.com/acme/eng/pull/42", delivery: "ci_failed" }),
    ];
    render(TerminalGrid);
    expect(screen.getByText("#42")).toBeInTheDocument();
    expect(screen.getByText(/ci failed/)).toBeInTheDocument();
  });

  it("shows nothing where there is no PR", () => {
    store.sessions = [fakeSession({ prNumber: 0 })];
    render(TerminalGrid);
    expect(screen.queryByText("—")).not.toBeInTheDocument();
  });

  it("opens the PR without also opening the terminal underneath", async () => {
    const openURL = vi.spyOn(store, "openURL").mockResolvedValue(undefined);
    store.sessions = [
      fakeSession({ prNumber: 42, prUrl: "https://github.com/acme/eng/pull/42", delivery: "draft" }),
    ];
    render(TerminalGrid);
    await fireEvent.click(screen.getByRole("button", { name: "#42" }));
    expect(openURL).toHaveBeenCalledWith("https://github.com/acme/eng/pull/42");
    expect(nav.focusedTerm).toBe("");
    openURL.mockRestore();
  });

  it("names the AGENT axis on the tile, not the PR's", () => {
    // The tile's pill used to be the rolled-up status, so a session whose agent
    // was mid-turn under an open PR read as its delivery state and nothing else.
    store.sessions = [
      fakeSession({ agentState: "working", delivery: "review_pending", status: "review_pending" }),
    ];
    render(TerminalGrid);
    expect(screen.getByText("working")).toBeInTheDocument();
  });
});
