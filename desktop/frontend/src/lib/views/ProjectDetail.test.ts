import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import ProjectBroadcast from "$lib/components/ProjectBroadcast.svelte";
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
    nav.asideOpen = true;
    store.projects = [];
    store.sessions = [];
    nav.goGit("acme");
    nav.sidebarQuery = "";
    store.status = null;
    store.alive = true;
    store.connected = false;
    nav.sidebarOpen = true;
    nav.scoped = false;
    nav.closeOverlay();
  });

  it("opens the Git workspace directly even with the old Overview preference", () => {
    localStorage.setItem("lola.project.tab", "overview");
    store.projects = [fakeProject()];
    render(ProjectDetail);
    expect(screen.getByTestId("project-git")).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Overview" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Project sessions" })).toBeNull();
  });

  it("keeps waiting and daemon alerts accessible in the compact Git toolbar", async () => {
    store.projects = [fakeProject()];
    store.sessions = [{ id: "waiting", project: "acme", agentState: "waiting_input" }] as typeof store.sessions;
    store.connected = true;
    store.alive = false;
    nav.sidebarOpen = false;
    render(ProjectDetail);
    expect(screen.getByRole("button", { name: "Show sidebar" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "1 waiting on you" })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Daemon down" }));
    expect(nav.overlay).toBe("doctor");
  });

  it("offers a toggle to bring the right sidebar back", async () => {
    store.projects = [fakeProject()];
    nav.asideOpen = false;
    render(ProjectDetail);
    const toggle = screen.getByRole("button", { name: "Right sidebar" });
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    await fireEvent.click(toggle);
    expect(nav.asideOpen).toBe(true);
    expect(toggle).toHaveAttribute("aria-pressed", "true");
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
    expect(nav.overlay).toBe("broadcast");
    expect(nav.overlayProject).toBe("acme");
    render(ProjectBroadcast, { project: nav.overlayProject });
    const box = screen.getByLabelText("Broadcast message");
    await fireEvent.input(box, { target: { value: "  rebase on main  " } });
    await fireEvent.click(screen.getByRole("button", { name: /Send to agents/ }));

    expect(spy).toHaveBeenCalledWith("acme", "rebase on main");
    expect(await screen.findByText("delivered to 1, deferred for 1 (sent when idle)")).toBeInTheDocument();
    expect((box as HTMLTextAreaElement).value).toBe("");
  });
});
