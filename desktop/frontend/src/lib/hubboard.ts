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

/** A row group of the Available section: one repository, or one advisory. */
export type BoardGroup = {
  /** The repository ("owner/name") or the advisory id. */
  key: string;
  items: HubBoardItem[];
  /** The worst severity in the group ("" when none is known). */
  worst: string;
  /** Rows this Mac may claim now. */
  claimable: HubBoardItem[];
  /** The one reason no row can be claimed here, when every row shares it. */
  reason: string;
};

/** Grouped claims take at most this many advisories (config.MaxGroupedChores). */
export const MAX_GROUPED = 12;

function groupBy(items: HubBoardItem[], keyOf: (it: HubBoardItem) => string): BoardGroup[] {
  const byKey = new Map<string, HubBoardItem[]>();
  for (const it of items) {
    const k = keyOf(it);
    byKey.set(k, [...(byKey.get(k) ?? []), it]);
  }
  const out: BoardGroup[] = [];
  for (const [key, rows] of byKey) {
    rows.sort((a, b) => severityRank(a) - severityRank(b) || time(a.firstSeen) - time(b.firstSeen));
    const claimable = rows.filter((it) => it.local?.claimable);
    const reasons = new Set(rows.map((it) => (it.local?.claimable ? "" : (it.local?.reason ?? ""))));
    const reason = claimable.length === 0 && reasons.size === 1 ? [...reasons][0] : "";
    out.push({ key, items: rows, worst: rows[0]?.severity ?? "", claimable, reason });
  }
  // Worst severity first, then the bigger group.
  out.sort((a, b) => severityRank(a.items[0]) - severityRank(b.items[0]) || b.items.length - a.items.length || a.key.localeCompare(b.key));
  return out;
}

/** Available rows per repository: the view's default. */
export function groupByRepo(items: HubBoardItem[]): BoardGroup[] {
  return groupBy(items, (it) => it.repo || "");
}

/** Available rows per advisory (a PR chore is its own group). */
export function groupByAdvisory(items: HubBoardItem[]): BoardGroup[] {
  return groupBy(items, (it) => (it.kind === "dep_advisory" && it.advisory ? it.advisory : itemTitle(it)));
}

/** The advisories "Fix all" claims together: claimable dependency advisories, capped. */
export function fixAllItems(g: BoardGroup): HubBoardItem[] {
  return g.claimable.filter((it) => it.kind === "dep_advisory").slice(0, MAX_GROUPED);
}

/** "3 high · 1 moderate": a group's severities, worst first. */
export function severityCounts(items: HubBoardItem[]): string {
  const counts = new Map<string, number>();
  for (const it of items) {
    const sev = (it.severity ?? "").toLowerCase();
    if (sev) counts.set(sev, (counts.get(sev) ?? 0) + 1);
  }
  return [...counts.entries()]
    .sort((a, b) => (SEVERITY_RANK[a[0]] ?? 4) - (SEVERITY_RANK[b[0]] ?? 4))
    .map(([sev, n]) => `${n} ${sev}`)
    .join(" · ");
}

/** Severity buckets worst first, with how many rows each holds (empty ones left out). */
export function severityTally(items: HubBoardItem[]): { severity: string; count: number }[] {
  const order = ["critical", "high", "moderate", "low", ""];
  const counts = new Map<string, number>();
  for (const it of items) {
    let sev = (it.severity ?? "").toLowerCase();
    if (sev === "medium") sev = "moderate";
    if (!order.includes(sev)) sev = "";
    counts.set(sev, (counts.get(sev) ?? 0) + 1);
  }
  return order.filter((s) => counts.get(s)).map((severity) => ({ severity, count: counts.get(severity)! }));
}

/** The bar fill for a severity bucket. Theme tokens only. */
export function severityFill(severity: string): string {
  switch (severity) {
    case "critical":
      return "bg-bad";
    case "high":
      return "bg-orange";
    case "moderate":
      return "bg-warn";
    case "low":
      return "bg-faint";
    default:
      return "bg-edge";
  }
}

/** Chores being worked: this Mac's per session, a teammate's per device and repository. */
export type WorkGroup = {
  key: string;
  mine: boolean;
  /** This Mac's session ("" for a teammate, or when it is not known here). */
  session: string;
  /** The device holding the leases. */
  device: string;
  repo: string;
  items: HubBoardItem[];
  /** The soonest lease end in the group. */
  expires: string;
};

export function workGroups(mine: HubBoardItem[], theirs: HubBoardItem[], status: HubStatusData | null): WorkGroup[] {
  const byKey = new Map<string, WorkGroup>();
  const add = (it: HubBoardItem, isMine: boolean) => {
    const session = isMine ? localSession(it, status) : "";
    const device = it.lease?.deviceName || (isMine ? "This Mac" : "A teammate");
    const key = isMine ? `s:${session || it.id}` : `d:${device}|${it.repo}`;
    let g = byKey.get(key);
    if (!g) {
      g = { key, mine: isMine, session, device, repo: it.repo, items: [], expires: "" };
      byKey.set(key, g);
    }
    g.items.push(it);
    const exp = it.lease?.expires ?? "";
    if (exp && (!g.expires || time(exp) < time(g.expires))) g.expires = exp;
  };
  mine.forEach((it) => add(it, true));
  theirs.forEach((it) => add(it, false));
  const out = [...byKey.values()];
  for (const g of out) g.items.sort((a, b) => severityRank(a) - severityRank(b));
  out.sort((a, b) => Number(b.mine) - Number(a.mine) || b.items.length - a.items.length || a.key.localeCompare(b.key));
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
