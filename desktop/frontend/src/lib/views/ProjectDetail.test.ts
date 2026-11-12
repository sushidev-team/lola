import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import ProjectDetail from "./ProjectDetail.svelte";
import { store, type ProjectInfo } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";

function fakeProject(over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    name: "acme",
    path: "/Users/me/code/acme",
    repo: "acme/acme",
    defaultBranch: "main",
    agent: "claude",
    agentBin: "claude",
    agentOk: true,
    pathOk: true,
    repoConfigured: true,
    pollCount: 1,
    pollsEnabled: 1,
    lastRun: "",
    sessions: 0,
    liveCounted: 0,
    needsYou: 0,
    ciRed: 0,
    openPrs: 0,
    ...over,
  } as ProjectInfo;
}

describe("ProjectDetail", () => {
  beforeEach(() => {
    cleanup();
    store.projects = [];
    store.sessions = [];
    nav.goDetail("acme", "overview");
    nav.sidebarQuery = "";
    store.status = null;
    store.alive = true;
    store.connected = false;
    nav.sidebarOpen = true;
    nav.scoped = false;
    nav.closeOverlay();
  });

  it("renders project information, actions and empty sessions for a known project", () => {
    store.projects = [fakeProject()];
    render(ProjectDetail);

    expect(screen.getAllByText(/acme\/acme/)[0]).toBeInTheDocument(); // repository information
    expect(screen.getByText("Open a PR")).toBeInTheDocument();
    expect(screen.getByText("Start a ticket")).toBeInTheDocument();
    expect(screen.getAllByText("New session…")[0]).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Polls", hidden: true })).toBeInTheDocument();
    expect(screen.getByText("No sessions yet")).toBeInTheDocument();
  });

  it("keeps waiting and daemon alerts accessible in the compact Git toolbar", async () => {
    store.projects = [fakeProject()];
    store.sessions = [{ id: "waiting", project: "acme", agentState: "waiting_input" }] as typeof store.sessions;
    store.connected = true;
    store.alive = false;
    nav.sidebarOpen = false;
    nav.projectTab = "git";
    render(ProjectDetail);
    expect(screen.getByRole("button", { name: "Show sidebar" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "1 waiting on you" })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Daemon down" }));
    expect(nav.overlay).toBe("doctor");
  });

  it("dims Open a PR and shows the hint when no repo is configured", () => {
    store.projects = [fakeProject({ repoConfigured: false })];
    render(ProjectDetail);

    const btn = screen.getByText("Open a PR").closest("button");
    expect(btn).toBeDisabled();
    expect(btn).toHaveAttribute("title", "set a GitHub repo to list PRs");
  });

  it("warns and keeps launch verbs navigable when the agent is unhealthy", () => {
    store.projects = [fakeProject({ agentOk: false, agentErr: "claude not on PATH" })];
    render(ProjectDetail);

    expect(screen.getByText(/agent not ready: claude not on PATH/)).toBeInTheDocument();
    // Per spec these actions navigate to pickers, so they stay enabled.
    expect(screen.getByText("Start a ticket").closest("button")).not.toBeDisabled();
    expect(screen.getAllByText("New session…")[0].closest("button")).not.toBeDisabled();
  });

  // "Polls" and "Edit project" are two doors into the same overlay now that a
  // project IS the poll unit — they differ only in the tab they land on.
  it("opens the one project overlay from both Polls and Edit project", async () => {
    store.projects = [fakeProject()];
    render(ProjectDetail);

    await fireEvent.click(screen.getByRole("menuitem", { name: "Polls", hidden: true }).closest("button")!);
    expect(nav.overlay).toBe("project");
    expect(nav.overlayProject).toBe("acme");
    expect(nav.overlayTab).toBe("filter");

    nav.closeOverlay();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Edit project", hidden: true }).closest("button")!);
    expect(nav.overlay).toBe("project");
    expect(nav.overlayTab).toBe("repo");
  });

  it("falls back gracefully when the project is unknown", () => {
    nav.project = "ghost";
    render(ProjectDetail);
    expect(screen.getByText(/not found/)).toBeInTheDocument();
  });

  it("opens the shared session creator scoped to the project", async () => {
    store.projects = [fakeProject()];
    render(ProjectDetail);
    await fireEvent.click(screen.getAllByText("New session…")[0]);
    expect(nav.overlay).toBe("newsession");
    expect(nav.overlayProject).toBe("acme");
  });

  it("opens a session in the session panel and switches the sidebar", async () => {
    nav.sidebarQuery = "unrelated search";
    store.projects = [fakeProject({ liveCounted: 1, needsYou: 1, openPrs: 2 })];
    store.sessions = [{ id: "s1", project: "acme", title: "Implement login", branch: "feature/login", agent: "claude", agentState: "working", delivery: "none", prNumber: 0 }] as typeof store.sessions;
    render(ProjectDetail);
    expect(screen.getByRole("region", { name: "Project sessions" })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: /Implement login/ }));
    expect(nav.view).toBe("cockpit");
    expect(nav.focusedTerm).toBe("s1");
    expect(nav.sidebarTab).toBe("sessions");
    expect(nav.project).toBe("acme");
    expect(nav.sidebarQuery).toBe("");
  });

  it("opens Git from repository information and supports keyboard tabs", async () => {
    store.projects = [fakeProject()];
    render(ProjectDetail);
    await fireEvent.click(screen.getByRole("button", { name: "Open Git" }));
    expect(nav.projectTab).toBe("git");
    expect(screen.getByRole("tab", { name: "Git" })).toHaveAttribute("aria-selected", "true");
    await fireEvent.keyDown(screen.getByRole("tab", { name: "Git" }), { key: "ArrowLeft" });
    expect(nav.projectTab).toBe("overview");
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveFocus();
    await fireEvent.keyDown(screen.getByRole("tab", { name: "Overview" }), { key: "ArrowRight" });
    expect(screen.getByRole("tab", { name: "Git" })).toHaveFocus();
    expect(screen.getAllByRole("banner", { name: "Project toolbar" })).toHaveLength(1);
  });

  it("shows Overview for a stack without overwriting the saved Git preference", () => {
    store.projects = [fakeProject({ kind: "stack", path: "" })];
    nav.projectTab = "git";
    render(ProjectDetail);
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByRole("tab", { name: "Git" })).toBeNull();
    expect(nav.projectTab).toBe("git");
    expect(localStorage.getItem("lola.project.tab")).toBe("git");
  });

  it("opens stack member repositories in their Git workspace", async () => {
    store.projects = [fakeProject({ kind: "stack", path: "" }), fakeProject({ name: "frontend", stack: "acme", repo: "acme/frontend" })];
    render(ProjectDetail);
    expect(screen.queryByRole("tab", { name: "Git" })).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: /frontend acme\/frontend/ }));
    expect(nav.project).toBe("frontend");
    expect(nav.projectTab).toBe("git");
  });

  it("broadcasts one message to the project's agents and shows the delivered/deferred split", async () => {
    const spy = vi
      .spyOn(store, "broadcast")
      .mockResolvedValue({
        delivered: ["a"],
        queued: ["b"],
        message: "delivered to 1, deferred for 1 (sent when idle)",
      });
    store.projects = [fakeProject()];
    render(ProjectDetail);

    await fireEvent.click(screen.getByRole("menuitem", { name: "Broadcast", hidden: true }).closest("button")!);
    const box = screen.getByLabelText("Broadcast message");
    await fireEvent.input(box, { target: { value: "  rebase on main  " } });
    await fireEvent.click(screen.getByRole("button", { name: /Send to agents/ }));

    expect(spy).toHaveBeenCalledWith("acme", "rebase on main");
    expect(await screen.findByText("delivered to 1, deferred for 1 (sent when idle)")).toBeInTheDocument();
    expect((box as HTMLTextAreaElement).value).toBe("");
  });
});
