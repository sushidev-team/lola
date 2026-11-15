import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { store } from "$lib/store.svelte";
import { ScriptedDaemon, project } from "@mobile/lib/projects.testkit";
import { router } from "@mobile/lib/router.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import ProjectDetail from "./ProjectDetail.svelte";

const d = new ScriptedDaemon();

beforeEach(async () => {
  await d.install();
  d.replies = {};
  tiers.set(["read", "answer", "control"]);
  store.connected = true;
  store.alive = true;
  store.projects = [project()];
  store.sessions = [];
  router.stack = [{ name: "tab", tab: "projects", project: "nori-app" }];
});
afterEach(() => {
  d.uninstall();
  tiers.clear();
});

describe("ProjectDetail", () => {
  it("shows Agent and Branch under the display name, and not Path or Repo", () => {
    render(ProjectDetail);
    expect(screen.getByRole("heading", { name: "Nori" })).toBeTruthy();
    expect(screen.getByText("Agent")).toBeTruthy();
    expect(screen.getByText("Branch")).toBeTruthy();
    expect(screen.queryByText("/Volumes/Git/nori")).toBeNull();
    expect(screen.queryByText("Path")).toBeNull();
    expect(screen.queryByText("Repo")).toBeNull();
  });

  it("goes back to Projects", async () => {
    router.stack = [{ name: "tab", tab: "projects" }, { name: "tab", tab: "projects", project: "nori-app" }];
    render(ProjectDetail);
    await fireEvent.click(screen.getByRole("button", { name: /Projects/ }));
    expect(router.top).toEqual({ name: "tab", tab: "projects" });
  });

  it("draws Sentence-case chips for non-zero counts only", () => {
    store.projects = [project({ liveCounted: 2, needsYou: 1, ciRed: 0 })];
    render(ProjectDetail);
    expect(screen.getByText("2 working")).toBeTruthy();
    expect(screen.getByText("1 need you")).toBeTruthy();
    expect(screen.queryByText(/CI failed/)).toBeNull();
    expect(screen.queryByText("Agent ready")).toBeNull();
    expect(screen.queryByText("Polling")).toBeNull();
  });

  it("folds the PR and ticket rows into the one New session row", () => {
    render(ProjectDetail);
    expect(screen.queryByRole("button", { name: /Start from/ })).toBeNull();
    expect(screen.getAllByRole("button", { name: /New session/ })).toHaveLength(1);
  });

  it("hides the Branch row when the branch is unknown and names the resolved agent", () => {
    store.projects = [project({ defaultBranch: "", agent: "" })];
    render(ProjectDetail);
    expect(screen.queryByText("Branch")).toBeNull();
    expect(screen.queryByText("—")).toBeNull();
    expect(screen.getByText("Claude")).toBeTruthy();
  });

  it("opens the newSession sheet for a worktree", async () => {
    render(ProjectDetail);
    await fireEvent.click(screen.getByRole("button", { name: /New session/ }));
    expect(router.sheet).toBe("newSession");
  });

  it("does not draw a row the tiers lack", () => {
    tiers.set(["read"]);
    render(ProjectDetail);
    expect(screen.queryByRole("button", { name: /New session/ })).toBeNull();
  });

  it("disables the launch row with the reason when the agent is not ready", () => {
    store.projects = [project({ agentOk: false })];
    render(ProjectDetail);
    const row = screen.getByRole("button", { name: /New session/ }) as HTMLButtonElement;
    expect(row.disabled).toBe(true);
    expect(row.textContent).toContain("Agent not ready.");
  });

  it("polls once and says what came back", async () => {
    d.replies.pollOnce = { ok: true, data: { matches: [{}, {}] } };
    render(ProjectDetail);
    await fireEvent.click(screen.getByRole("button", { name: /Check tickets/ }));
    expect(await screen.findByText("Polled: 2 matches.")).toBeTruthy();
    expect(d.payloadOf("pollOnce")).toMatchObject({ cmd: "pollOnce" });
  });

  it("shows a refusal as the daemon worded it", async () => {
    d.replies.disable = { ok: false, error: "no such poll" };
    render(ProjectDetail);
    await fireEvent.click(screen.getByRole("button", { name: /Pause/ }));
    expect(await screen.findByText(/no such poll/)).toBeTruthy();
  });

  it("opens a session of the project and caps the list until Show all", async () => {
    store.sessions = Array.from({ length: 8 }, (_, i) => ({
      id: `s${i}`, project: "nori-app", title: `Task ${i}`, status: "working",
    })) as never;
    render(ProjectDetail);
    expect(screen.queryByText("Task 7")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Show all" }));
    await waitFor(() => expect(screen.getByText("Task 7")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: /Task 7/ }));
    expect(router.top).toMatchObject({ name: "session", seg: "conversation" });
  });

  it("says so when the project left the config", () => {
    router.stack = [{ name: "tab", tab: "projects", project: "gone" }];
    render(ProjectDetail);
    expect(screen.getByText("Project not found.")).toBeTruthy();
  });
});
