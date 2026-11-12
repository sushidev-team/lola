// Pure helpers for the Team view: how the org board's rows split into the
// view's sections, and the display words and tones of their facts. No DOM and
// no store reads, so the rules are tested on their own (hubboard.test.ts).
//
// Nothing here decides what a person may do. `local.claimable` and
// `local.reason` are the daemon's answer for this Mac, and a claim is re-checked
// by the daemon when it is pressed.

import type { HubBoardItem, HubDeviceInfo, HubStatusData } from "@bindings/internal/protocol";

export type BoardSections = {
  /** Open rows: what the team still needs someone to take. */
  available: HubBoardItem[];
  /** Claimed rows this Mac holds the lease on. */
  mine: HubBoardItem[];
  /** Claimed rows a teammate's device holds. */
  teammates: HubBoardItem[];
  done: HubBoardItem[];
  failed: HubBoardItem[];
  escalated: HubBoardItem[];
};

const SEVERITY_RANK: Record<string, number> = { critical: 0, high: 1, moderate: 2, medium: 2, low: 3 };

function severityRank(it: HubBoardItem): number {
  return SEVERITY_RANK[(it.severity ?? "").toLowerCase()] ?? 4;
}

function time(s: string | undefined): number {
  const t = s ? Date.parse(s) : NaN;
  return Number.isFinite(t) ? t : 0;
}

/**
 * Split the board into the view's sections. Available is worst severity first,
 * then oldest first (a chore that has waited longest goes up). The finished
 * sections are newest first.
 */
export function boardSections(items: HubBoardItem[]): BoardSections {
  const out: BoardSections = { available: [], mine: [], teammates: [], done: [], failed: [], escalated: [] };
  for (const it of items) {
    switch (it.state) {
      case "open":
        out.available.push(it);
        break;
      case "claimed":
        (it.lease?.mine ? out.mine : out.teammates).push(it);
        break;
      case "done":
        out.done.push(it);
        break;
      case "failed":
        out.failed.push(it);
        break;
      case "escalated":
        out.escalated.push(it);
        break;
    }
  }
  out.available.sort((a, b) => severityRank(a) - severityRank(b) || time(a.firstSeen) - time(b.firstSeen));
  const newest = (a: HubBoardItem, b: HubBoardItem) => time(b.lastSeen) - time(a.lastSeen);
  out.done.sort(newest);
  out.failed.sort(newest);
  out.escalated.sort(newest);
  return out;
}

/** The chip classes for an advisory's severity. Theme tokens only. */
export function severityClass(severity: string | undefined): string {
  switch ((severity ?? "").toLowerCase()) {
    case "critical":
      return "border-bad/40 bg-bad/10 text-bad";
    case "high":
      return "border-orange/40 bg-orange/10 text-orange";
    case "moderate":
    case "medium":
      return "border-warn/40 bg-warn/10 text-warn";
    case "low":
      return "border-edge bg-pill-grey text-pill-grey-fg";
    default:
      return "border-edge bg-pill-grey text-pill-grey-fg";
  }
}

/** "Critical", "High", ... in Sentence case; "" for no severity. */
export function severityLabel(severity: string | undefined): string {
  const s = (severity ?? "").trim().toLowerCase();
  return s ? s[0].toUpperCase() + s.slice(1) : "";
}

/** The kind's words, matching HubService.Kinds(). */
export function kindLabel(kind: string): string {
  if (kind === "dep_advisory") return "Security advisory";
  if (kind === "red_bump_pr") return "Failing dependency bump";
  return kind;
}

/** The kind's drawn icon name (icons.ts). */
export function kindIcon(kind: string): "shield-check" | "git-pull-request" | "circle-dot" {
  if (kind === "dep_advisory") return "shield-check";
  if (kind === "red_bump_pr") return "git-pull-request";
  return "circle-dot";
}

/** The row's headline: the advisory id or the PR number, else lola's label. */
export function itemTitle(it: HubBoardItem): string {
  if (it.kind === "dep_advisory" && it.advisory) return it.advisory;
  if (it.kind === "red_bump_pr" && it.prNumber) return `PR #${it.prNumber}`;
  return it.label || it.id;
}

/** "lodash 4.17.20 → 4.17.21", "lodash" or "" when the hub named no package. */
export function packageLine(it: HubBoardItem): string {
  const pkg = it.package ?? "";
  if (!pkg) return "";
  const from = it.vulnerable ?? "";
  const to = it.fixed ?? "";
  if (from && to) return `${pkg} ${from} → ${to}`;
  if (to) return `${pkg} → ${to}`;
  if (from) return `${pkg} ${from}`;
  return pkg;
}

/** The PR outcome's delivery word for its chip, Sentence case. */
const DELIVERY_LABEL: Record<string, string> = {
  none: "No PR",
  draft: "Draft PR",
  ci_pending: "CI running",
  ci_failed: "CI failed",
  merge_conflict: "Merge conflict",
  changes_requested: "Changes requested",
  review_pending: "Review pending",
  approved: "Approved",
  merged: "Merged",
  closed: "Closed",
};

/** The PR outcome's delivery word for its chip (hubproto.Delivery), Sentence case. */
export function deliveryLabel(delivery: string | undefined): string {
  const d = delivery ?? "";
  if (!d) return "";
  if (DELIVERY_LABEL[d]) return DELIVERY_LABEL[d];
  const s = d.replace(/_/g, " ");
  return s[0].toUpperCase() + s.slice(1);
}

export function deliveryClass(delivery: string | undefined): string {
  switch (delivery ?? "") {
    case "merged":
    case "approved":
      return "bg-pill-done text-pill-done-fg";
    case "ci_failed":
    case "merge_conflict":
    case "closed":
      return "bg-pill-broken text-pill-broken-fg";
    case "changes_requested":
      return "bg-pill-urgent text-pill-urgent-fg";
    case "none":
      return "bg-pill-grey text-pill-grey-fg";
    default:
      return "bg-pill-work text-pill-work-fg";
  }
}

/**
 * The local session that works this item: the daemon's `local.session`, else
 * the status's session list (a release keeps the session but the board row may
 * already be open again).
 */
export function localSession(it: HubBoardItem, status: HubStatusData | null): string {
  if (it.local?.session) return it.local.session;
  return (status?.sessions ?? []).find((s) => s.itemId === it.id)?.session ?? "";
}

/** The projects a claim may name; [] when the daemon picks the one project itself. */
export function claimProjects(it: HubBoardItem): string[] {
  if (it.local?.project) return [];
  return it.local?.projects ?? [];
}

/** A device counts as online when the hub heard from it within the window. */
export const ONLINE_MS = 15 * 60_000;

export function deviceOnline(d: HubDeviceInfo, now = Date.now()): boolean {
  if (d.me) return true;
  const t = time(d.lastSeen);
  return t > 0 && now - t <= ONLINE_MS;
}

export function onlineCount(devices: HubDeviceInfo[], now = Date.now()): number {
  return devices.filter((d) => deviceOnline(d, now)).length;
}

/** The chip's words for a hub session: its label, and what changed about its lease. */
export function hubChipText(h: { label?: string; kind: string; leaseLost?: boolean; released?: boolean }): { label: string; state: "" | "released" | "lost"; tip: string } {
  const label = h.label || kindLabel(h.kind);
  const state = h.released ? "released" : h.leaseLost ? "lost" : "";
  const lines = [`Team chore: ${label}`, "This session came from the team hub."];
  if (state === "released") lines.push("Released to the team: the hub no longer hears from this session.");
  else if (state === "lost") lines.push("Lease lost: the hub may hand this chore to someone else.");
  return { label, state, tip: lines.join("\n") };
}

/** "Lease ends in 23m", "Lease ends in 2h", "Lease expired" or "" for no expiry. */
export function leaseLeft(expires: string | undefined, now = Date.now()): string {
  const t = time(expires);
  if (!t) return "";
  const d = t - now;
  if (d <= 0) return "Lease expired";
  if (d < 60_000) return "Lease ends in under a minute";
  if (d < 3_600_000) return `Lease ends in ${Math.floor(d / 60_000)}m`;
  return `Lease ends in ${Math.floor(d / 3_600_000)}h`;
}

/**
 * A daemon refusal for a row: drop the "hub: " source tags and start with a
 * capital ("hub: not claimed: X" → "Not claimed: X"). The words stay the daemon's.
 */
export function hubReason(msg: string): string {
  const s = msg.replace(/^(hub:\s*)+/i, "").trim();
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : msg;
}
