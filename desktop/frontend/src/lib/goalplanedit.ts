import type { Output, ProposedTask, Record as Proposal } from "@bindings/internal/goalplan";

export const splitLines = (value: string) => value.split("\n").map(s => s.trim()).filter(Boolean);

export function outputSignature(output: Output): string {
  return JSON.stringify({ tasks: (output.tasks ?? []).map(t => ({
    title: t.title.trim(), description: t.description, acceptance: t.acceptance ?? [],
    apps: t.apps ?? [], label_ids: t.label_ids ?? [], blocked_by: t.blocked_by ?? [], covers: t.covers ?? [],
    paths: Object.fromEntries(Object.entries(t.paths ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([id, paths]) => [id, paths ?? []])),
  })), unknowns: output.unknowns ?? [] });
}

// References are positional on the wire. Remap them atomically with the order;
// never silently remove an edge or move a prerequisite after its dependent.
export function reorderTasks<T extends ProposedTask>(tasks: T[], order: number[]): T[] | null {
  if (!order.length || new Set(order).size !== order.length || order.some(i => !Number.isInteger(i) || i < 0 || i >= tasks.length)) return null;
  const positions = new Map(order.map((old, next) => [old + 1, next + 1]));
  if (order.some((old, next) => (tasks[old].blocked_by ?? []).some(dep => !positions.has(dep) || positions.get(dep)! > next))) return null;
  return order.map(old => ({ ...tasks[old], blocked_by: (tasks[old].blocked_by ?? []).map(dep => positions.get(dep)!).sort((a,b) => a-b) }));
}

export function moveTask<T extends ProposedTask>(tasks: T[], index: number, delta: number): T[] | null {
  const target = index + delta;
  if (index < 0 || index >= tasks.length || target < 0 || target >= tasks.length) return null;
  const order = tasks.map((_, i) => i);
  [order[index], order[target]] = [order[target], order[index]];
  return reorderTasks(tasks, order);
}

export function removeTask<T extends ProposedTask>(tasks: T[], index: number): T[] | null {
  if (tasks.length <= 1 || index < 0 || index >= tasks.length) return null;
  return reorderTasks(tasks, tasks.map((_, i) => i).filter(i => i !== index));
}

export function graphProblems(output: Output, proposal: Proposal): string[] {
  const tasks = output.tasks ?? [];
  const problems: string[] = [];
  if (!tasks.length || tasks.length > proposal.input.request.max_tasks) problems.push(`Use 1–${proposal.input.request.max_tasks} tasks.`);
  const covered = new Set<number>();
  tasks.forEach((task, i) => {
    const prefix = `Task ${i + 1}: `;
    if (!task.title.trim()) problems.push(prefix + "add a title.");
    if (!task.acceptance?.length) problems.push(prefix + "add an acceptance criterion.");
    if (!task.covers?.length) problems.push(prefix + "select the goal criteria it covers.");
    task.covers?.forEach(n => covered.add(n));
    if (!task.apps?.length) problems.push(prefix + "select a repository.");
    if (!task.apps?.some(id => task.paths?.[id]?.length)) problems.push(prefix + "declare at least one writable path.");
    if ((task.blocked_by ?? []).some(n => n < 1 || n > i)) problems.push(prefix + "dependencies must refer to earlier tasks.");
  });
  proposal.input.request.acceptance?.forEach((_, i) => {
    if (!covered.has(i + 1)) problems.push(`Goal criterion ${i + 1} needs a task.`);
  });
  if ((output.unknowns?.length ?? 0) > 20) problems.push("Keep at most 20 open questions.");
  return problems;
}
