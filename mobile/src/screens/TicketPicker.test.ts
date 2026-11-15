import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { store } from "$lib/store.svelte";
import { ScriptedDaemon, project } from "@mobile/lib/projects.testkit";
import { router } from "@mobile/lib/router.svelte";
import TicketPicker from "./TicketPicker.svelte";

const d = new ScriptedDaemon();
const issue = (over: Record<string, unknown> = {}) => ({
  identifier: "NOR-1", uuid: "u1", title: "Add dark mode", branch: "nor-1", priority: 2,
  state: "In Progress", stateType: "started", assignee: "carol", updated: "2h", alreadyLive: false, ...over,
});
const tickets = (rows: unknown[]) => ({
  ok: true as const,
  data: { team: "t", teamName: "Nori", teamKey: "NOR", issues: rows },
});

beforeEach(async () => {
  await d.install();
  d.replies = {};
  store.projects = [project()];
  store.sessions = [];
  router.stack = [{ name: "tab", tab: "projects", project: "nori-app", pick: "tickets" }];
});
afterEach(() => d.uninstall());

describe("TicketPicker", () => {
  it("asks for the mine scope first and shows the team", async () => {
    d.replies.tickets = tickets([issue()]);
    render(TicketPicker);
    expect(await screen.findByText("Add dark mode")).toBeTruthy();
    expect(d.payloadOf("tickets")).toEqual({ cmd: "tickets", args: { project: "nori-app", scope: "mine" } });
    expect(screen.getByRole("heading", { name: "Pick a ticket" })).toBeTruthy();
    expect(screen.queryByText(/Nori \(NOR\)/)).toBeNull();
    expect(screen.getByRole("tablist")).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Mine" }).getAttribute("aria-selected")).toBe("true");
  });

  it("re-fetches on a scope change and names assignees only for the team", async () => {
    d.replies.tickets = tickets([issue()]);
    render(TicketPicker);
    await screen.findByText("Add dark mode");
    expect(screen.queryByText("carol")).toBeNull();
    await fireEvent.click(screen.getByRole("tab", { name: "Team" }));
    expect(await screen.findByText("carol")).toBeTruthy();
    expect(d.count("tickets")).toBe(2);
  });

  it("starts an issue and lands on its session", async () => {
    d.replies.tickets = tickets([issue()]);
    d.replies.openTicket = { ok: true, data: { sessionId: "nori-app-1" } };
    render(TicketPicker);
    await fireEvent.click(await screen.findByRole("button", { name: /Add dark mode/ }));
    await waitFor(() => expect(router.top).toMatchObject({ name: "session", id: "nori-app-1" }));
  });

  it("marks a running issue and never sends openTicket for it", async () => {
    store.sessions = [{ id: "nori-app-7", project: "nori-app", issue: "NOR-1" } as never];
    d.replies.tickets = tickets([issue({ alreadyLive: true })]);
    render(TicketPicker);
    expect(await screen.findByText("Running")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: /Add dark mode/ }));
    expect(d.count("openTicket")).toBe(0);
    expect(router.top).toMatchObject({ name: "session", id: "nori-app-7" });
  });

  it("draws a running and a started ticket in one tone", async () => {
    d.replies.tickets = tickets([
      issue({ identifier: "NOR-1", uuid: "u1", title: "Live one", alreadyLive: true }),
      issue({ identifier: "NOR-2", uuid: "u2", title: "Started one" }),
    ]);
    render(TicketPicker);
    const running = await screen.findByText("Running");
    const started = screen.getByText("In Progress");
    expect(running.className).toBe(started.className);
    expect(running.className).not.toContain("text-good");
  });

  it("offers the team when mine is empty", async () => {
    d.replies.tickets = tickets([]);
    render(TicketPicker);
    await fireEvent.click(await screen.findByRole("button", { name: "Show team" }));
    await waitFor(() => expect(d.count("tickets")).toBe(2));
  });

  it("prints the daemon's failure verbatim", async () => {
    d.replies.tickets = { ok: false, error: "no Linear team" };
    render(TicketPicker);
    expect(await screen.findByText("no Linear team")).toBeTruthy();
  });
});
