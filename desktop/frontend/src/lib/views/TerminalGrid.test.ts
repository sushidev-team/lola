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
