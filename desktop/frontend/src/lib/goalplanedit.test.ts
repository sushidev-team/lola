import { describe, expect, it } from "vitest";
import type { ProposedTask, Record as Proposal } from "@bindings/internal/goalplan";
import { graphProblems, moveTask, removeTask, reorderTasks, outputSignature } from "./goalplanedit";
const task = (title: string, deps: number[] = []): ProposedTask => ({ title, description: "", acceptance: ["Test passes"], apps: ["api"], paths: { api: ["src"] }, covers: [1], blocked_by: deps });
describe("proposal graph editing", () => {
  it("keeps references attached through a valid reorder without mutating the saved graph", () => {
    const tasks = [task("A"), task("B"), task("C", [1])];
    const result = moveTask(tasks, 0, 1)!;
    expect(result.map(t => t.title)).toEqual(["B", "A", "C"]);
    expect(result[2].blocked_by).toEqual([2]);
    expect(tasks[2].blocked_by).toEqual([1]);
  });
  it("refuses a move across a dependency and removal of a referenced prerequisite", () => {
    const tasks = [task("A"), task("B", [1])];
    expect(moveTask(tasks, 0, 1)).toBeNull();
    expect(moveTask(tasks, 1, -1)).toBeNull();
    expect(removeTask(tasks, 0)).toBeNull();
    expect(removeTask([task("A")], 0)).toBeNull();
  });
  it("renumbers surviving dependencies when an unrelated task is removed", () => {
    const result = removeTask([task("Unrelated"), task("A"), task("B", [2])], 0)!;
    expect(result[1].blocked_by).toEqual([1]);
    expect(result.map(t => t.title)).toEqual(["A", "B"]);
  });
  it("rejects malformed orderings", () => {
    const tasks = [task("A"), task("B")];
    for (const order of [[], [0,0], [0,2], [0,0.5]]) expect(reorderTasks(tasks, order)).toBeNull();
  });
  it("requires complete goal coverage and writable deliverables", () => {
    const proposal = { input: { request: { max_tasks: 3, acceptance: ["A", "B"] } } } as Proposal;
    expect(graphProblems({ tasks: [{ ...task("A"), paths: { api: [] } }] }, proposal)).toEqual([
      "Task 1: declare at least one writable path.", "Goal criterion 2 needs a task.",
    ]);
    expect(graphProblems({ tasks: [task("A"), { ...task("B", [1]), covers: [2] }] }, proposal)).toEqual([]);
  });
  it("treats omitted optional arrays and path object order consistently", () => {
    expect(outputSignature({ tasks: [{ ...task("A"), paths: { api: [], ui: ["src"] }, blocked_by: undefined }] }))
      .toBe(outputSignature({ tasks: [{ ...task("A"), paths: { ui: ["src"], api: [] }, label_ids: [] }], unknowns: [] }));
  });
});
