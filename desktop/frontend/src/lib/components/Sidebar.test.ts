import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import Sidebar from "./Sidebar.svelte";
import { nav } from "$lib/nav.svelte";
import { store } from "$lib/store.svelte";

const project = { name: "lola", label: "Lola", path: "/r/lola", repo: "o/lola", kind: "repo" };

describe("Sidebar workspace switch", () => {
  beforeEach(() => {
    store.projects = [project] as never;
    nav.goCockpit("");
    nav.projectTab = "overview";
  });
  afterEach(cleanup);

  it("shows projects beside sessions at once", () => {
    render(Sidebar);
    expect(screen.getByRole("navigation", { name: "Projects" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Filter sessions" })).toBeInTheDocument();
  });

  it("switches between Code and Git beside the logo and returns to the same scope", async () => {
    nav.goCockpit("lola");
    render(Sidebar);
    const code = screen.getByRole("button", { name: "Code" });
    const git = screen.getByRole("button", { name: "Git" });
    expect(code).toHaveAttribute("aria-pressed", "true");
    await fireEvent.click(git);
    expect(nav.inGit).toBe(true);
    expect(git).toHaveAttribute("aria-pressed", "true");
    await fireEvent.click(code);
    expect(nav.inGit).toBe(false);
    expect(nav.view).toBe("cockpit");
    expect(nav.scoped).toBe(true);
    expect(nav.project).toBe("lola");
  });
});

describe("Sidebar narrow layout", () => {
  afterEach(cleanup);
  it("offers Projects and Sessions as tabs below the split width", async () => {
    render(Sidebar, { width: 320 });
    const tab = screen.getByRole("tab", { name: "Projects" });
    await fireEvent.click(tab);
    expect(nav.sidebarTab).toBe("projects");
    await fireEvent.click(screen.getByRole("tab", { name: "Sessions" }));
    expect(nav.sidebarTab).toBe("sessions");
  });
  it("shows no tabs when wide", () => {
    render(Sidebar, { width: 600 });
    expect(screen.queryByRole("tab", { name: "Projects" })).toBeNull();
  });
});

describe("Sidebar in Git mode", () => {
  afterEach(cleanup);
  it("hides the sessions column", async () => {
    store.projects = [{ name: "lola", label: "Lola", path: "/r/lola", repo: "o/lola", kind: "repo" }] as never;
    nav.goCockpit("");
    render(Sidebar, { width: 540 });
    const col = () => document.querySelector("[data-sidebar-sessions]")!;
    expect(col()).not.toHaveClass("hidden");
    await fireEvent.click(screen.getByRole("button", { name: "Git" }));
    expect(col()).toHaveClass("hidden");
    await fireEvent.click(screen.getByRole("button", { name: "Code" }));
    expect(col()).not.toHaveClass("hidden");
  });
});
