import type { BriefContract, ProgressUpdate } from "@bindings/internal/goal";

export const recoveryLabels: Record<string, string> = {
  "sync-brief": "Refresh Linear brief",
  "accept-brief": "Reconcile brief",
  "dismiss-progress": "Dismiss progress update",
  "delivery-evidence": "Attach evidence URL",
  "retry-verification": "Retry verification",
  "retry-replan": "Retry replanning",
};
export const recoveryConsequences: Record<string, string> = {
  "sync-brief": "Read Linear and retain its current brief locally. This can hold execution if the brief or placement changed. It does not overwrite Linear, accept a new brief, or resume workers.",
  "accept-brief": "Accept this clarification against the retained task plan. Tasks, permissions and budgets stay fixed. Previous whole-goal verification is archived; authorized work remains paused until you resume it separately.",
  "dismiss-progress": "Retain this update and your reason in history, allowing later progress updates. This neither deletes the Linear comment nor claims it was delivered, and does not resend this update.",
  "delivery-evidence": "Attach a shared HTTPS artifact link before final PR descriptions are reserved. This does not upload files or establish verification. Published descriptions cannot be replaced here.",
  "retry-verification": "Start a fresh verification round within the accepted budgets. Earlier checks and reviews remain in history. This may run checks and paid specialist calls; the daemon first reconciles any unfinished work.",
  "retry-replan": "Reserve a new paid coordinator call within the accepted budgets. Retain the previous run and its evidence. This does not expand task scope, permissions or limits.",
};
const bytes = (s: string) => new TextEncoder().encode(s).length;
export function recoveryProblem(action: string, reason: string, contract: BriefContract, url: string): string {
  if (["accept-brief", "dismiss-progress"].includes(action) && !reason.trim()) return "Describe why this decision is appropriate before reviewing it.";
  if (bytes(reason) > 2000) return "Keep the reason within 2000 bytes.";
  if (action === "delivery-evidence") {
    try {
      const u = new URL(url);
      if (u.protocol !== "https:" || !u.hostname || u.username || u.password || bytes(url) > 2000 || /[\r\n<>\\]/.test(url)) throw Error();
    } catch { return "Use a shared HTTPS artifact URL without credentials, up to 2000 bytes."; }
  }
  if (action === "accept-brief") {
    if (!contract.title.trim() || bytes(contract.title) > 240) return "Enter a brief title within 240 bytes.";
    if (!contract.outcome.trim() || bytes(contract.outcome) > 16000 || contract.outcome.includes("<!-- lola-goal:")) return "Enter an outcome within 16000 bytes, without reserved goal markers.";
    if (!contract.acceptance?.length || contract.acceptance.length > 50) return "Enter 1–50 acceptance criteria, one per line.";
    if ((contract.non_goals?.length ?? 0) > 50) return "Keep at most 50 non-goals.";
    if ([...contract.acceptance, ...(contract.non_goals ?? [])].some(s => !s.trim() || bytes(s) > 2000 || s.includes("<!-- lola-goal:"))) return "Keep each criterion or non-goal within 2000 bytes, without reserved goal markers.";
  }
  return "";
}
export function progressState(update: ProgressUpdate): string {
  const set = (s: string | undefined) => !!s && !s.startsWith("0001-");
  if (set(update.dismissed_at)) return "Dismissed; remote outcome not asserted";
  if (set(update.confirmed_at)) return "Confirmed in Linear";
  if (set(update.sent_at)) return "Send reserved; delivery unconfirmed";
  if (set(update.attempted_at)) return "Reconciliation attempted; delivery unconfirmed";
  return "Pending; no send recorded";
}
