import type { Policy, Rollout } from "@bindings/internal/goalpolicy";

export function reviewedRollout(policy: Policy, compatibility: string, order: string): Rollout | undefined {
  if (!policy.publish_pr) return undefined;
  const lines = (text: string) => text.split("\n").map(s => s.trim()).filter(Boolean);
  const requirements = lines(compatibility);
  const members = policy.members ?? [];
  const mergeOrder = lines(order);
  if (members.length === 1 && !requirements.length && !mergeOrder.length) return undefined;
  if (!requirements.length || requirements.length > 20) throw new Error("Add 1–20 compatibility requirements, one per line, before reviewing the rollout.");
  if (requirements.some(s => new TextEncoder().encode(s).length > 500 || /[\u0000-\u001f\u007f-\u009f]/.test(s))) throw new Error("Each compatibility requirement must be a single line of at most 500 bytes, without control characters.");
  const ids = new Set(members.map(m => m.id));
  if (mergeOrder.length !== members.length || new Set(mergeOrder).size !== members.length || mergeOrder.some(id => !ids.has(id))) throw new Error(`Human merge order must include each repository ID exactly once, one per line: ${members.map(m => m.id).join(", ")}.`);
  return { compatibility: requirements, merge_order: mergeOrder };
}
