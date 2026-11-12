import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";

const { HubProjects, SaveHubProject } = vi.hoisted(() => ({ HubProjects: vi.fn(), SaveHubProject: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ ConfigService: { HubProjects, SaveHubProject } }));

import Projects from "./HubProjects.svelte";

const web = { name: "web", label: "Web", repo: "acme/web", suggestedRepo: "", hubChores: true, sandboxed: true, stack: "" };
const api = { name: "api", label: "API", repo: "", suggestedRepo: "acme/api", hubChores: false, sandboxed: false, stack: "shop" };
const row = (name: string) => document.querySelector(`[data-hub-project="${name}"]`) as HTMLElement;
const findRow = (name: string) =>
  waitFor(() => {
    const r = row(name);
    if (!r) throw new Error(`no row ${name}`);
    return r;
  });

beforeEach(() => {
  HubProjects.mockResolvedValue([web, api]);
  SaveHubProject.mockResolvedValue(undefined);
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("HubProjects", () => {
  it("lists projects with their repo, stack and sandbox", async () => {
    render(Projects);
    expect(await screen.findByText("1 of 2 take chores")).toBeInTheDocument();
    expect(within(row("web")).getByText("acme/web")).toBeInTheDocument();
    expect(within(row("web")).getByText("Docker")).toBeInTheDocument();
    expect(within(row("api")).getByText("in shop")).toBeInTheDocument();
    // No repo: the chores box waits for one.
    expect(within(row("api")).getByRole("checkbox", { name: "Team chores for API" })).toBeDisabled();
  });

  it("takes the checkout's repo in one click and saves each change at once", async () => {
    render(Projects);
    await fireEvent.click(await within(await findRow("api")).findByRole("button", { name: "Use acme/api" }));
    await waitFor(() => expect(SaveHubProject).toHaveBeenCalledWith({ name: "api", repo: "acme/api", hubChores: null, sandboxed: null }));
    await fireEvent.click(within(row("web")).getByRole("checkbox", { name: "Team chores for Web" }));
    await waitFor(() => expect(SaveHubProject).toHaveBeenCalledWith({ name: "web", repo: null, hubChores: false, sandboxed: null }));
    expect(HubProjects).toHaveBeenCalledTimes(3);
  });

  it("offers Docker only while sandboxed chores are required", async () => {
    const { unmount } = render(Projects, { requireSandbox: true });
    await fireEvent.click(await within(await findRow("api")).findByRole("button", { name: "Use Docker" }));
    await waitFor(() => expect(SaveHubProject).toHaveBeenCalledWith({ name: "api", repo: null, hubChores: null, sandboxed: true }));
    unmount();
    render(Projects, { requireSandbox: false });
    await findRow("api");
    expect(within(row("api")).queryByRole("button", { name: "Use Docker" })).toBeNull();
  });

  it("edits a repo inline, checks its shape, and shows the save error", async () => {
    render(Projects);
    await fireEvent.click(await within(await findRow("web")).findByRole("button", { name: "Edit repository for Web" }));
    const input = screen.getByLabelText("Repository for Web") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "nope" } });
    await fireEvent.click(within(row("web")).getByRole("button", { name: "Save" }));
    expect(within(row("web")).getByRole("alert")).toHaveTextContent("owner/name");
    expect(SaveHubProject).not.toHaveBeenCalled();
    SaveHubProject.mockRejectedValueOnce(new Error("saved, but the running daemon rejected it"));
    await fireEvent.input(input, { target: { value: "acme/site" } });
    await fireEvent.click(within(row("web")).getByRole("button", { name: "Save" }));
    expect(await within(row("web")).findByRole("alert")).toHaveTextContent("daemon rejected it");
  });

  it("draws an empty state without projects", async () => {
    HubProjects.mockResolvedValue([]);
    render(Projects);
    expect(await screen.findByText("No projects yet")).toBeInTheDocument();
  });
});
