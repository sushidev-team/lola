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
    expect(screen.getByRole("textbox", { name: "Search sessions" })).toBeInTheDocument();
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
