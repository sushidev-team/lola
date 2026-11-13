import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import Projects from "./Projects.svelte";
import { store } from "$lib/store.svelte";
import { nav } from "@mobile/lib/nav.svelte";
import type { ProjectInfo } from "@bindings/internal/protocol";

// The projects screen: the configured projects, in the arrangement the Mac
// shows them in, and one thing a tap can do — open the project's detail.

function p(over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    name: "nori-app",
    label: "",
    group: "",
    path: "/Volumes/Git/nori",
    repo: "sushidev-team/nori",
    defaultBranch: "main",
    agent: "claude",
    agentBin: "claude",
    agentOk: true,
    pathOk: true,
    repoConfigured: true,
    pollCount: 1,
    pollsEnabled: 1,
    lastRun: "",
    sessions: 4,
    liveCounted: 2,
    needsYou: 1,
    ciRed: 0,
    openPrs: 1,
    ...over,
  } as unknown as ProjectInfo;
}

beforeEach(() => {
  store.connected = true;
  store.projects = [];
  store.groups = [];
  nav.tab = "sessions";
  nav.project = "";
  nav.pick = "";
  nav.query = "";
  nav.triage = "";
});

describe("Projects", () => {
  it("renders the DISPLAY name, never the bare id", () => {
    // A project has two names — `Name` is identity (paths, tmux, every protocol
    // field) and `Label` is display — and CLAUDE.md's rule is that a UI renders
    // the display one.
    store.projects = [p({ name: "nori-app", label: "Nori" })];
    render(Projects);
    expect(screen.getByText("Nori")).toBeTruthy();
    expect(screen.queryByText("nori-app")).toBeNull();
  });

  it("falls back to the id for a project with no label", () => {
    store.projects = [p({ name: "nori-app", label: "" })];
    render(Projects);
    expect(screen.getByText("nori-app")).toBeTruthy();
  });

  it("says whether polling is on, and how many sessions are live", () => {
    store.projects = [
      p({ name: "nori-app", label: "Nori", pollsEnabled: 1, liveCounted: 2 }),
      p({ name: "lola", label: "lola", pollsEnabled: 0, liveCounted: 0 }),
    ];
    render(Projects);

    expect(screen.getByText("Polling")).toBeTruthy();
    expect(screen.getByText("Not polling")).toBeTruthy();
    // `liveCounted`, not `sessions`: the total includes merged and dead ones,
    // and "4 sessions" when all four are finished says the opposite of what a
    // glance is asking.
    expect(screen.getByText("2 live")).toBeTruthy();
    expect(screen.getByText("0 live")).toBeTruthy();
  });

  it("draws a group as a section, counting its members", () => {
    store.groups = [{ name: "work", label: "Work", position: 1 }];
    store.projects = [
      p({ name: "loose", label: "Loose" }),
      p({ name: "nori-app", label: "Nori", group: "work" }),
      p({ name: "lola", label: "lola", group: "work" }),
    ];
    render(Projects);

    // The arrangement is `buildRows`, the desktop's own pure layout function:
    // ungrouped projects in config order with each folder spliced in at its
    // own position. An EMPTY folder is renderable too, which is why groups
    // ride the push rather than being derived from their members.
    const heading = screen.getByRole("heading", { name: "Work 2" });
    expect(heading.tagName).toBe("H2");
  });

  it("drills into the project rather than filtering the sessions list", async () => {
    store.projects = [p({ name: "nori-app", label: "Nori" })];
    render(Projects);

    await fireEvent.click(screen.getByRole("button", { name: /Nori/ }));

    // The NAME, not the label: `Name` is identity in this repository — the
    // worktree path segment, the tmux prefix, the value every session carries
    // in its `project` field — and it is what `nav.project` holds.
    expect(nav.project).toBe("nori-app");
    // Still the Projects tab: the detail is a DEPTH inside it, not a screen of
    // its own, so the bottom bar stays drawn and stays lit.
    expect(nav.tab).toBe("projects");
    expect(nav.screen).toBe("sessions");
    // No picker is opened over it, and the sessions list is left exactly as it
    // was — filtering it is now the detail's own "Sessions" action, and doing
    // it here as well would rewrite a filter nobody asked to change.
    expect(nav.pick).toBe("");
    expect(nav.query).toBe("");
  });

  it("tells a Mac with nothing configured apart from a push that has not landed", () => {
    store.connected = false;
    const { unmount } = render(Projects);
    expect(screen.getByText("Connecting…")).toBeTruthy();
    unmount();

    store.connected = true;
    render(Projects);
    expect(screen.getByText("No projects")).toBeTruthy();
    // And no "add a project" action, because that writes config.toml.
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("gives every row the 44pt minimum height", () => {
    store.projects = [p()];
    render(Projects);
    expect(screen.getByRole("button").className).toContain("tap-row");
  });
});
