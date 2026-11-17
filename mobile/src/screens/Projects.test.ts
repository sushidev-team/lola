import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import { store } from "$lib/store.svelte";
import { project } from "@mobile/lib/projects.testkit";
import { router } from "@mobile/lib/router.svelte";
import Projects from "./Projects.svelte";

beforeEach(() => {
  store.connected = true;
  store.projects = [];
  store.groups = [];
  router.stack = [{ name: "tab", tab: "projects" }];
});

describe("Projects", () => {
  it("renders the display name, never the bare id", () => {
    store.projects = [project({ name: "nori-app", label: "Nori" })];
    render(Projects);
    expect(screen.getByText("Nori")).toBeTruthy();
    expect(screen.queryByText("nori-app")).toBeNull();
  });

  it("counts sessions and says Paused only when polling is off", () => {
    store.projects = [
      project({ name: "a", label: "A", pollsEnabled: 1, sessions: 2 }),
      project({ name: "b", label: "B", pollsEnabled: 0, sessions: 0 }),
    ];
    render(Projects);
    expect(screen.queryByText("Polling")).toBeNull();
    expect(screen.queryByText("Not polling")).toBeNull();
    expect(screen.getByText("2 sessions")).toBeTruthy();
    expect(screen.getByText("0 sessions · Paused")).toBeTruthy();
  });

  it("draws a group as a section counting its members", () => {
    store.groups = [{ name: "work", label: "Work", position: 1 }];
    store.projects = [project({ name: "a", label: "A", group: "work" }), project({ name: "b", label: "B", group: "work" })];
    render(Projects);
    expect(screen.getByRole("heading", { name: "Work 2" })).toBeTruthy();
  });

  it("opens the project's detail by NAME on a tap", async () => {
    store.projects = [project({ name: "nori-app", label: "Nori" })];
    render(Projects);
    await fireEvent.click(screen.getByRole("button", { name: /Nori/ }));
    expect(router.top).toEqual({ name: "tab", tab: "projects", project: "nori-app" });
  });

  it("tells an empty Mac from a push that has not landed, and offers no write", () => {
    store.connected = false;
    const first = render(Projects);
    expect(screen.getByText("Connecting…")).toBeTruthy();
    first.unmount();
    store.connected = true;
    render(Projects);
    expect(screen.getByText("No projects.")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
