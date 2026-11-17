import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import GoalPlanEditor from "./GoalPlanEditor.svelte";
import type { Record as Proposal } from "@bindings/internal/goalplan";
const api = vi.hoisted(() => ({ GoalEditPlan: vi.fn(), GoalProposal: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ DaemonService: api }));
function sample(): Proposal {
  return { id: "proposal-id", goal_id: "goal-id", agent: "codex", image: "image", input_sha256: "digest", started_at: "2026-10-21T12:00:00Z", revision: 7, state: "proposed", input: { team_id: "team", linear_project_id: "linear", stack: false, knowledge: { lessons: [], references: [] }, request: { id: "proposal-id", title: "Invitations", outcome: "Invite a colleague", timeout_seconds: 180, project: "nori", acceptance: ["Recipient accepts"], max_tasks: 10 }, members: [{ id: "api", repo: "org/api", routing_labels: [], snapshot: { member: "api", source: "main", target: "main", commit: "abc", tree: [], files: [] } }], labels: [{ id: "label-id", name: "Backend" }] }, output: { tasks: [{ title: "Invitation API", description: "Implement invitations", acceptance: ["Acceptance test passes"], apps: ["api"], paths: { api: ["src/invitations"] }, covers: [1] }] } } as Proposal;
}
beforeEach(() => { api.GoalEditPlan.mockReset().mockResolvedValue({ ...sample(), revision: 8 }); api.GoalProposal.mockReset().mockResolvedValue(sample()); });
afterEach(cleanup);
async function edit() {
  await fireEvent.input(await screen.findByLabelText("Task 1 title"), { target: { value: "Invitation service" } });
  await fireEvent.input(screen.getByLabelText("Reason for this revision"), { target: { value: "Clarify the service boundary" } });
}
async function review() { await edit(); await fireEvent.click(screen.getByRole("button", { name: "Review revision" })); }
async function save() {
  await fireEvent.click(screen.getByRole("checkbox", { name: /I reviewed this revision/ }));
  await fireEvent.click(screen.getByRole("button", { name: "Save proposal revision" }));
}
describe("GoalPlanEditor", () => {
  it("freezes a reviewed, identified revision before sending it once", async () => {
    const updated = vi.fn(); render(GoalPlanEditor, { proposal: sample(), onupdated: updated, oncancel: vi.fn() });
    await edit();
    await fireEvent.input(screen.getByLabelText("Task 1 acceptance criteria"), { target: { value: "Test one\n\nTest two\n" } });
    expect(screen.getByLabelText("Task 1 acceptance criteria")).toHaveValue("Test one\n\nTest two\n");
    await fireEvent.click(screen.getByRole("checkbox", { name: "Backend" }));
    await fireEvent.click(screen.getByRole("button", { name: "Review revision" }));
    const id = screen.getByText(/^Edit ID:/).textContent!.slice(9);
    expect(id).toMatch(/^[a-f0-9-]{36}$/);
    expect(screen.queryByLabelText("Task 1 title")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save proposal revision" })).toBeDisabled();
    expect(api.GoalEditPlan).not.toHaveBeenCalled(); await save();
    await waitFor(() => expect(updated).toHaveBeenCalledTimes(1));
    expect(api.GoalEditPlan).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: "proposal-id", project: "nori", revision: 7, edit_id: id, reason: "Clarify the service boundary", output: { tasks: [expect.objectContaining({ title: "Invitation service", acceptance: ["Test one", "Test two"], label_ids: ["label-id"], paths: { api: ["src/invitations"] } })], unknowns: [] } }));
  });
  it("keeps an uncertain edit visible and only recovers through a read", async () => {
    api.GoalEditPlan.mockRejectedValue(new Error("socket timeout"));
    const updated = vi.fn(); render(GoalPlanEditor, { proposal: sample(), onupdated: updated, oncancel: vi.fn() });
    await review(); const identity = screen.getByText(/^Edit ID:/).textContent; await save();
    await screen.findByRole("alert");
    expect(screen.queryByRole("button", { name: "Back to editing" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save proposal revision" })).not.toBeInTheDocument();
    expect(screen.getByText(/^Edit ID:/).textContent).toBe(identity);
    api.GoalProposal.mockRejectedValueOnce(new Error("offline"));
    await fireEvent.click(screen.getByRole("button", { name: "Refresh saved proposal" }));
    await screen.findByText(/Could not refresh/); expect(updated).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "Refresh saved proposal" }));
    await waitFor(() => expect(updated).toHaveBeenCalledWith(sample()));
    expect(api.GoalEditPlan).toHaveBeenCalledTimes(1);
  });
  it("blocks duplicate submissions and stale callbacks after leaving the project", async () => {
    let resolve!: (p: Proposal) => void;
    api.GoalEditPlan.mockImplementation(() => new Promise<Proposal>(r => resolve = r));
    const updated = vi.fn(); const view = render(GoalPlanEditor, { proposal: sample(), onupdated: updated, oncancel: vi.fn() });
    await review(); await save(); await screen.findByRole("status");
    expect(screen.getByRole("button", { name: "Back to editing" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save proposal revision" })).toBeDisabled();
    view.unmount(); resolve(sample()); await Promise.resolve(); expect(updated).not.toHaveBeenCalled();
  });
  it("requires changed content, covered criteria, repository scope and a write path", async () => {
    render(GoalPlanEditor, { proposal: sample(), onupdated: vi.fn(), oncancel: vi.fn() });
    await fireEvent.input(screen.getByLabelText("Reason for this revision"), { target: { value: "Explain the change" } });
    expect(screen.getByRole("button", { name: "Review revision" })).toBeDisabled();
    await edit();
    await fireEvent.click(screen.getByRole("checkbox", { name: "1. Recipient accepts" }));
    await screen.findByText("Goal criterion 1 needs a task.");
    expect(screen.getByRole("button", { name: "Review revision" })).toBeDisabled();
    await fireEvent.click(screen.getByRole("checkbox", { name: "1. Recipient accepts" }));
    await fireEvent.input(screen.getByLabelText("Writable paths in api"), { target: { value: "" } });
    await screen.findByText("Task 1: declare at least one writable path.");
    await fireEvent.click(screen.getByRole("checkbox", { name: "api" }));
    await screen.findByText("Task 1: select a repository."); expect(api.GoalEditPlan).not.toHaveBeenCalled();
  });
  it("retains fields on back, requires review again, and confirms discard", async () => {
    const cancel = vi.fn(); render(GoalPlanEditor, { proposal: sample(), onupdated: vi.fn(), oncancel: cancel });
    await review(); const oldID = screen.getByText(/^Edit ID:/).textContent;
    await fireEvent.click(screen.getByRole("checkbox", { name: /I reviewed this revision/ }));
    await fireEvent.click(screen.getByRole("button", { name: "Back to editing" }));
    expect(screen.getByLabelText("Task 1 title")).toHaveValue("Invitation service");
    await fireEvent.click(screen.getByRole("button", { name: "Review revision" }));
    expect(screen.getByText(/^Edit ID:/).textContent).not.toBe(oldID);
    expect(screen.getByRole("checkbox", { name: /I reviewed this revision/ })).not.toBeChecked();
    await fireEvent.click(screen.getByRole("button", { name: "Back to editing" }));
    await fireEvent.click(screen.getByRole("button", { name: "Cancel editing" }));
    expect(cancel).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(cancel).toHaveBeenCalledTimes(1);
  });
});
