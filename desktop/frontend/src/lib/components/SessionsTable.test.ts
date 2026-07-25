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
