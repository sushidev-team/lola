import { describe, it, expect, beforeEach } from "vitest";
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
    nav.project = "acme";
    nav.scoped = false;
    nav.closeOverlay();
  });

  it("renders the status line, actions and empty live strip for a known project", () => {
    store.projects = [fakeProject()];
    render(ProjectDetail);

    expect(screen.getByText(/acme\/acme/)).toBeInTheDocument(); // repo in status line
    expect(screen.getByText("Open a PR")).toBeInTheDocument();
    expect(screen.getByText("Start a ticket")).toBeInTheDocument();
    expect(screen.getByText("New worktree")).toBeInTheDocument();
    expect(screen.getByText("Polls")).toBeInTheDocument();
    expect(screen.getByText("no live sessions in this project")).toBeInTheDocument();
  });

  it("dims Open a PR and shows the hint when no repo is configured", () => {
    store.projects = [fakeProject({ repoConfigured: false })];
    render(ProjectDetail);

    const btn = screen.getByText("Open a PR").closest("button");
    expect(btn).toBeDisabled();
    expect(screen.getByText("set a GitHub repo to list PRs")).toBeInTheDocument();
  });

  it("warns and keeps launch verbs navigable when the agent is unhealthy", () => {
    store.projects = [fakeProject({ agentOk: false, agentErr: "claude not on PATH" })];
    render(ProjectDetail);

    expect(screen.getByText(/agent not ready: claude not on PATH/)).toBeInTheDocument();
    // Per spec these actions navigate to pickers, so they stay enabled.
    expect(screen.getByText("Start a ticket").closest("button")).not.toBeDisabled();
    expect(screen.getByText("New worktree").closest("button")).not.toBeDisabled();
  });

  // "Polls" and "Edit project" are two doors into the same overlay now that a
  // project IS the poll unit — they differ only in the tab they land on.
  it("opens the one project overlay from both Polls and Edit project", async () => {
    store.projects = [fakeProject()];
    render(ProjectDetail);

    await fireEvent.click(screen.getByText("Polls").closest("button")!);
    expect(nav.overlay).toBe("project");
    expect(nav.overlayProject).toBe("acme");
    expect(nav.overlayTab).toBe("filter");

    nav.closeOverlay();
    await fireEvent.click(screen.getByText("Edit project").closest("button")!);
    expect(nav.overlay).toBe("project");
    expect(nav.overlayTab).toBe("repo");
  });

  it("falls back gracefully when the project is unknown", () => {
    nav.project = "ghost";
    render(ProjectDetail);
    expect(screen.getByText(/not found/)).toBeInTheDocument();
  });
});
