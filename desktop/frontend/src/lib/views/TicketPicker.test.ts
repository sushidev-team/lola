import { render, screen, fireEvent } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach } from "vitest";

// TicketRow-shaped fixtures (camelCase json field names from the generated model).
const issues = [
  {
    identifier: "NOR-373",
    uuid: "u373",
    title: "Invoices: No extra card",
    branch: "lola/nor-373",
    priority: 2,
    state: "In Progress",
    stateType: "started",
    assignee: "Ada",
    labels: ["bug"],
    estimate: 3,
    updated: "2h05m",
    alreadyLive: true,
  },
  {
    identifier: "NOR-372",
    uuid: "u372",
    title: "Timezone issues",
    branch: "lola/nor-372",
    priority: 4,
    state: "Backlog",
    stateType: "backlog",
    assignee: "",
    labels: [],
    updated: "6d3h",
    alreadyLive: false,
  },
];

const { ticketsMock, openTicketMock, setFlashMock, goCockpitMock, goDetailMock } = vi.hoisted(() => ({
  ticketsMock: vi.fn(async () => ({
    team: "ace69aca-dd39-4c63-91dc-36bbf48b62c7",
    teamName: "Nori",
    teamKey: "NOR",
    issues,
  })),
  openTicketMock: vi.fn(async () => ({})),
  setFlashMock: vi.fn(),
  goCockpitMock: vi.fn(),
  goDetailMock: vi.fn(),
}));

vi.mock("$lib/store.svelte", () => ({
  store: {
    alive: true,
    tickets: ticketsMock,
    openTicket: openTicketMock,
    setFlash: setFlashMock,
  },
}));

vi.mock("$lib/nav.svelte", () => ({
  nav: { project: "demo", goCockpit: goCockpitMock, goDetail: goDetailMock },
}));

import TicketPicker from "./TicketPicker.svelte";

describe("TicketPicker", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists issues with the facts you pick by", async () => {
    render(TicketPicker);
    expect(ticketsMock).toHaveBeenCalledWith("demo", "mine");
    expect(await screen.findByText("Invoices: No extra card")).toBeInTheDocument();
    // Workflow state, staleness and labels ride the row.
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.getByText("2h05m")).toBeInTheDocument();
    expect(screen.getByText("bug")).toBeInTheDocument();
    expect(screen.getByText("High")).toBeInTheDocument();
  });

  it("names the team instead of printing its UUID, and drops the scope caption", async () => {
    render(TicketPicker);
    expect(await screen.findByText("Nori (NOR)")).toBeInTheDocument();
    expect(screen.queryByText(/ace69aca/)).not.toBeInTheDocument();
    // The Mine/Team switcher IS the scope label; no caption restates it.
    expect(screen.queryByText(/scope/i)).not.toBeInTheDocument();
  });

  it("has no back button of its own — the breadcrumb owns that", async () => {
    render(TicketPicker);
    await screen.findByText("Invoices: No extra card");
    expect(screen.queryByRole("button", { name: /back to project/i })).not.toBeInTheDocument();
    expect(goDetailMock).not.toHaveBeenCalled();
  });

  it("filters over state, labels and assignee, not just the title", async () => {
    render(TicketPicker);
    const box = await screen.findByLabelText("Filter issues");

    await fireEvent.input(box, { target: { value: "backlog" } });
    expect(screen.queryByText("Invoices: No extra card")).not.toBeInTheDocument();
    expect(screen.getByText("Timezone issues")).toBeInTheDocument();

    await fireEvent.input(box, { target: { value: "ada" } });
    expect(screen.getByText("Invoices: No extra card")).toBeInTheDocument();
    expect(screen.queryByText("Timezone issues")).not.toBeInTheDocument();

    await fireEvent.input(box, { target: { value: "nothing here" } });
    expect(screen.getByText(/No issue matches/)).toBeInTheDocument();
  });

  it("shows the assignee column only in team scope", async () => {
    render(TicketPicker);
    await screen.findByText("Invoices: No extra card");
    expect(screen.queryByText("Assignee")).not.toBeInTheDocument();

    await fireEvent.click(screen.getByRole("button", { name: "Team" }));
    expect(ticketsMock).toHaveBeenLastCalledWith("demo", "team");
    expect(await screen.findByText("Assignee")).toBeInTheDocument();
    expect(screen.getByText("Ada")).toBeInTheDocument();
  });

  it("starts an issue on row click and refuses one a session already holds", async () => {
    render(TicketPicker);
    await fireEvent.click(await screen.findByText("Timezone issues"));
    expect(openTicketMock).toHaveBeenCalledWith({
      project: "demo",
      identifier: "NOR-372",
      uuid: "u372",
      branch: "lola/nor-372",
      title: "Timezone issues",
    });
    expect(goCockpitMock).toHaveBeenCalledWith("demo");

    openTicketMock.mockClear();
    await fireEvent.click(screen.getByText("Invoices: No extra card"));
    expect(openTicketMock).not.toHaveBeenCalled();
    expect(setFlashMock).toHaveBeenCalled();
  });
});
