// Pure helpers for the Team view: how the org board's rows split into the
// view's sections, and the display words and tones of their facts. No DOM and
// no store reads, so the rules are tested on their own (hubboard.test.ts).
//
// Nothing here decides what a person may do. `local.claimable` and
// `local.reason` are the daemon's answer for this Mac, and a claim is re-checked
// by the daemon when it is pressed.
//
// Plain words: the UI never says "lease", "heartbeat", "offer" or "device key".
// A chore is "taken by" someone, a Mac was "last seen", and this machine is
// "this Mac". Every daemon or hub message on screen goes through ONE mapper,
// hubReason, so a raw "Error:" prefix or a wire tag never reaches a person.

import type {
  HubAttemptInfo,
  HubBoardItem,
  HubDeviceInfo,
  HubOfferSkip,
  HubRepoHealth,
  HubSessionInfo,
  HubStatusData,
} from "@bindings/internal/protocol";

export type BoardSections = {
  /** Open rows: what the team still needs someone to take. */
  available: HubBoardItem[];
  /** Claimed or in-review rows this Mac works. */
  mine: HubBoardItem[];
  /** Claimed or in-review rows a teammate's Mac works. */
  teammates: HubBoardItem[];
  done: HubBoardItem[];
  failed: HubBoardItem[];
  escalated: HubBoardItem[];
};

const SEVERITY_RANK: Record<string, number> = { critical: 0, high: 1, moderate: 2, medium: 2, low: 3 };

function severityRank(it: HubBoardItem): number {
  return SEVERITY_RANK[(it.severity ?? "").toLowerCase()] ?? 4;
}

function time(s: string | undefined | null): number {
  const t = s ? Date.parse(s) : NaN;
  return Number.isFinite(t) ? t : 0;
}

/**
 * Split the board into the view's sections. Available is worst severity first,
 * then oldest first (a chore that has waited longest goes up). An in-review
 * row belongs to whoever opened its pull request (the board's owner). The
 * finished sections are newest first.
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
      case "in_review":
        (it.owner?.mine ? out.mine : out.teammates).push(it);
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

/**
 * What a group's "Fix all" claims: every claimable row. Dependency advisories
 * go to ONE agent together (capped at MAX_GROUPED, the daemon's limit); a
 * failing dependency bump is its own pull request, so each gets its own claim.
 */
export function fixAllItems(g: BoardGroup): HubBoardItem[] {
  const { advisories, prs } = splitFix(g.claimable);
  return [...advisories.slice(0, MAX_GROUPED), ...prs];
}

/** The rows of a "Fix all" split by how they are claimed: grouped, or one by one. */
export function splitFix(items: HubBoardItem[]): { advisories: HubBoardItem[]; prs: HubBoardItem[] } {
  return {
    advisories: items.filter((it) => it.kind === "dep_advisory"),
    prs: items.filter((it) => it.kind !== "dep_advisory"),
  };
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

/**
 * The bar fill for a severity bucket. Theme tokens only. Unrated is a text
 * tier (ink-2), not the edge colour: an edge-coloured segment on the panel
 * fell under 3:1 and read as an empty track.
 */
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
      return "bg-ink-2";
  }
}

/** Who works (or worked) a row: the person's label first, the Mac second. */
export type Who = { name: string; device: string; mine: boolean };

/**
 * The person and Mac behind a row: the current holder for a claimed row, the
 * owner for one in review or finished, else the newest history entry. Name
 * is the member label when the admin set one, otherwise the Mac's name.
 */
export function whoOf(it: HubBoardItem): Who | null {
  const src = it.state === "claimed" && it.lease ? it.lease : (it.owner ?? it.lease ?? null);
  if (src) return { name: src.member || src.deviceName, device: src.deviceName, mine: !!src.mine };
  const last = [...(it.history ?? [])].reverse().find((h) => h.deviceName);
  return last ? { name: last.member || last.deviceName || "", device: last.deviceName || "", mine: false } : null;
}

/** "under a minute", "23m", "2h", "3d": a span in the largest whole unit. */
export function spanText(ms: number): string {
  if (ms < 60_000) return "under a minute";
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m`;
  if (ms < 86_400_000) return `${Math.floor(ms / 3_600_000)}h`;
  return `${Math.floor(ms / 86_400_000)}d`;
}

/** When the row went into review: the newest in_review history entry, else the claim time. */
export function reviewSince(it: HubBoardItem): string {
  const h = [...(it.history ?? [])].reverse().find((a) => a.action === "in_review");
  return h?.at || it.claimedAt || "";
}

/**
 * "Working for 2h" from the claim time, "In review for 3d" once the pull
 * request is open; "" when the board does not say when.
 */
export function workingFor(it: HubBoardItem, now = Date.now()): string {
  if (it.state === "in_review") {
    const t = time(reviewSince(it));
    return t ? `In review for ${spanText(Math.max(0, now - t))}` : "In review";
  }
  const t = time(it.claimedAt);
  return t ? `Working for ${spanText(Math.max(0, now - t))}` : "";
}

/** Chores being worked: this Mac's per session, a teammate's per Mac and repository. */
export type WorkGroup = {
  key: string;
  mine: boolean;
  /** This Mac's session ("" for a teammate, or when it is not known here). */
  session: string;
  /** The person's label, else the Mac's name. */
  who: string;
  /** The Mac working the group. */
  device: string;
  repo: string;
  items: HubBoardItem[];
  /** The earliest claim time in the group ("" = unknown). */
  since: string;
};

export function workGroups(mine: HubBoardItem[], theirs: HubBoardItem[], status: HubStatusData | null): WorkGroup[] {
  const byKey = new Map<string, WorkGroup>();
  const add = (it: HubBoardItem, isMine: boolean) => {
    const session = isMine ? localSession(it, status) : "";
    const w = whoOf(it);
    const device = w?.device || (isMine ? "This Mac" : "A teammate");
    const key = isMine ? `s:${session || it.id}` : `d:${device}|${it.repo}`;
    let g = byKey.get(key);
    if (!g) {
      g = { key, mine: isMine, session, who: isMine ? "This Mac" : w?.name || device, device, repo: it.repo, items: [], since: "" };
      byKey.set(key, g);
    }
    g.items.push(it);
    const at = it.claimedAt ?? "";
    if (at && (!g.since || time(at) < time(g.since))) g.since = at;
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

/** The pull request a row points at: its outcome, else the newest one in its history. */
export function prURL(it: HubBoardItem): string {
  if (it.outcome?.prUrl) return it.outcome.prUrl;
  return [...(it.history ?? [])].reverse().find((h) => h.prUrl)?.prUrl ?? "";
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

/** A Mac counts as online when the hub heard from it within the window. */
export const ONLINE_MS = 15 * 60_000;

export function deviceOnline(d: HubDeviceInfo, now = Date.now()): boolean {
  if (d.me) return true;
  const t = time(d.lastSeen);
  return t > 0 && now - t <= ONLINE_MS;
}

export function onlineCount(devices: HubDeviceInfo[], now = Date.now()): number {
  return devices.filter((d) => deviceOnline(d, now)).length;
}

/** The chip's words for a hub session: its label, and what changed about its claim. */
export type ChipState = "" | "released" | "lost" | "taken" | "review";

export function hubChipText(h: {
  label?: string;
  kind: string;
  leaseLost?: boolean;
  leaseTaken?: boolean;
  inReview?: boolean;
  released?: boolean;
}): { label: string; state: ChipState; note: string; tip: string } {
  const label = h.label || kindLabel(h.kind);
  const state: ChipState = h.released ? "released" : h.leaseTaken ? "taken" : h.leaseLost ? "lost" : h.inReview ? "review" : "";
  const note = { "": "", released: "Given back", taken: "Taken by another Mac", lost: "No longer claimed", review: "In review" }[state];
  const lines = [`Team chore: ${label}`, "This session came from the team hub."];
  if (state === "released") lines.push("Given back to the team: the hub no longer hears from this session.");
  else if (state === "taken") lines.push("Another Mac has taken this chore. The session keeps running.");
  else if (state === "lost") lines.push("The hub no longer counts this Mac as working it; someone else may take it.");
  else if (state === "review") lines.push("The pull request is open and waits for review.");
  lines.push("Open it in the Team view.");
  return { label, state, note, tip: lines.join("\n") };
}

// --- Plain words for the hub's reasons ------------------------------------

/** hubproto.FailReason → why a chore failed, as a person would say it. */
const FAIL_WORDS: Record<string, string> = {
  spawn_failed: "the session could not start",
  agent_gave_up: "the agent stopped without a pull request",
  ci_failed: "the pull request's checks stayed red",
  pr_closed: "the pull request was closed without merging",
  timeout: "it ran out of time",
  refused: "this Mac's settings turned it down",
  sandbox_required: "the project is not sandboxed",
  not_reproducible: "the problem no longer shows up",
};

export function failWords(reason: string | undefined): string {
  const r = reason ?? "";
  return FAIL_WORDS[r] ?? r.replace(/_/g, " ");
}

/** hubproto.AttemptAction → a history line's verb. */
const ACTION_WORDS: Record<string, string> = {
  claimed: "Took it",
  in_review: "Opened a pull request",
  completed: "Finished it",
  failed: "Failed",
  released: "Gave it back",
  expired: "Stopped answering",
  reopened: "Put back on the list",
  closed_stale: "Closed: no longer reported",
};

export function actionWords(action: string): string {
  return ACTION_WORDS[action] ?? action.replace(/_/g, " ");
}

/** One history entry: "Ana · studio-mac" and "Failed: the agent stopped without a pull request". */
export function attemptLine(a: HubAttemptInfo): { who: string; what: string } {
  const who = a.member && a.deviceName ? `${a.member} · ${a.deviceName}` : a.member || a.deviceName || "The hub";
  const what = a.reason ? `${actionWords(a.action)}: ${failWords(a.reason)}` : actionWords(a.action);
  return { who, what };
}

/** "2 attempts · last: the agent stopped without a pull request". */
export function whyLine(it: HubBoardItem): string {
  const parts: string[] = [];
  if (it.attempts > 0) parts.push(`${it.attempts} ${it.attempts === 1 ? "attempt" : "attempts"}`);
  if (it.lastFail) parts.push(`last: ${failWords(it.lastFail)}`);
  const back = it.reopened ?? 0;
  if (back > 0) parts.push(`back on the list ${back === 1 ? "once" : `${back} times`}`);
  return parts.join(" · ");
}

const DOCKER_DOWN_RE = /docker/i;
const DOCKER_DOWN_DETAIL_RE = /not running|cannot connect|is the docker daemon running|daemon is not|unavailable|connection refused/i;

/** True for a message that says Docker is not running: a person can fix that. */
export function dockerDown(msg: string): boolean {
  return DOCKER_DOWN_RE.test(msg) && DOCKER_DOWN_DETAIL_RE.test(msg);
}

/**
 * The daemon's fixed reason words (internal/hub/accept.go) as a person would
 * read them. Matched on the whole reason or its start; the daemon may add a
 * detail after a colon, which is kept.
 */
const REASON_WORDS: [string, string][] = [
  ["a claim for it is already in flight", "A claim for it is already on its way"],
  ["already working it", "This Mac already works it"],
  ["ended here recently", "It ended on this Mac recently, so this Mac waits before trying again"],
  ["the repository's hub pull request comes first", "The repository's open team pull request comes first"],
  ["a session already works the branch", "A session already works that branch"],
  ["dispatch held", "New sessions are on hold on this Mac"],
  ["runtime unavailable", "tmux, git or the agent is missing on this Mac"],
  ["the sandboxed agent has no login yet", "The sandboxed agent is not logged in yet"],
  ["capped", "This Mac already runs as many sessions as allowed"],
  ["GitHub does not confirm it", "GitHub no longer shows the problem"],
  ["claim refused", "The hub turned the claim down"],
  ["spawn failed", "The session could not start"],
  ["cannot persist the claim count", "This Mac could not save its count of chores"],
  ["daily claim cap reached", "This Mac reached its daily number of chores"],
  ["project is not sandboxed (hub.require_sandbox)", "The project is not sandboxed, and team chores need the sandbox"],
  ["project has not opted in (hub_chores)", "The project does not take team chores"],
  ["kind not accepted", "This Mac does not take this kind of chore"],
  ["item repo does not match the project", "The chore's repository is not this project's"],
  ["stacks take no hub chores", "Stacks do not take team chores"],
  ["no project", "No project on this Mac works this repository"],
  ["hub disabled", "Team hub is off on this Mac"],
  ["not claimed", "Not claimed"],
];

/** Wire words a person should never read, and what to say instead. */
const JARGON: [RegExp, string][] = [
  [/\blease[_ ]lost\b/gi, "taken by another Mac"],
  [/\bleases\b/gi, "claims"],
  [/\blease\b/gi, "claim"],
  [/\bheartbeats?\b/gi, "check-in"],
  [/\bdevice key\b/gi, "this Mac's key"],
  [/\boffers\b/gi, "chores"],
  [/\boffer\b/gi, "chore"],
];

/**
 * Every hub message the UI shows goes through here: "Error:" and "hub:" tags
 * are dropped, Docker being down and the daemon's fixed reasons become plain
 * sentences, wire jargon is swapped for plain words, and the result starts with
 * a capital. Anything else stays the daemon's words.
 */
export function hubReason(msg: string | undefined | null): string {
  let s = String(msg ?? "").trim();
  for (;;) {
    const next = s.replace(/^(error|hub)\s*:\s*/i, "");
    if (next === s) break;
    s = next;
  }
  if (!s) return "";
  if (dockerDown(s)) return "Docker is not running. Start Docker, then try again.";
  // "not claimed: <reason>" names a reason worth translating on its own.
  const inner = s.replace(/^not claimed:\s*/i, "");
  for (const [wire, words] of REASON_WORDS) {
    if (inner.toLowerCase() === wire.toLowerCase()) return `${words}.`;
    if (inner.toLowerCase().startsWith(`${wire.toLowerCase()}:`)) return `${words}: ${inner.slice(wire.length + 1).trim()}`;
  }
  for (const [re, words] of JARGON) s = s.replace(re, words);
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** What a person can do about an unclaimed chore, keyed on the daemon's reason. */
const SKIP_FIX: [string, string][] = [
  ["the sandboxed agent has no login yet", "Log the agent in from the project's sandbox settings."],
  ["dispatch held", "Wait for usage or load to drop, or raise the budget in Settings."],
  ["runtime unavailable", "Open the doctor to see what is missing."],
  ["capped", "Finish or stop a session, or raise the session limit in Settings."],
  ["the repository's hub pull request comes first", "Merge or close the open team pull request first."],
  ["daily claim cap reached", "Wait until tomorrow, or raise the daily number in Team hub settings."],
  ["project is not sandboxed", "Turn on the Docker sandbox for the project."],
  ["project has not opted in", "Turn on team chores for the project in Team hub settings."],
  ["kind not accepted", "Choose which chores this Mac takes in Team hub settings."],
  ["no project", "Add a project for the repository."],
  ["cannot persist the claim count", "Check that lola's folder in your home folder is writable."],
];

/** An unclaimed chore's reason and its fix in words. */
export function skipLine(sk: HubOfferSkip): { text: string; fix: string } {
  const r = (sk.reason ?? "").toLowerCase();
  const fix = SKIP_FIX.find(([k]) => r.startsWith(k.toLowerCase()))?.[1] ?? "";
  return { text: hubReason(sk.reason), fix: dockerDown(sk.reason ?? "") ? "Start Docker." : fix };
}

/** A repository the last scan could not fully read, with the fix in words; null when it read fine. */
export function healthLine(h: HubRepoHealth): { repo: string; text: string; fix: string } | null {
  if (h.alerts === "no_access")
    return {
      repo: h.repo,
      text: "No access to Dependabot alerts",
      fix: "Turn on Dependabot alerts for the repository, or ask an admin to let your GitHub account read security alerts.",
    };
  if (h.alerts === "error")
    return { repo: h.repo, text: "Could not read Dependabot alerts", fix: "Check that the GitHub CLI is signed in (open the doctor), then sync again." };
  if (h.pullRequests === "error")
    return { repo: h.repo, text: "Could not read pull requests", fix: "Check that the GitHub CLI is signed in (open the doctor), then sync again." };
  return null;
}

// --- Needs you --------------------------------------------------------------

/** How long a pull request may wait in review before it needs a person. */
export const REVIEW_STALE_MS = 3 * 86_400_000;

export type NeedsKind = "taken" | "failed" | "escalated" | "review" | "critical";

export type NeedsRow = {
  kind: NeedsKind;
  /** The board row ("" item id never happens; a taken session may have no row). */
  id: string;
  item: HubBoardItem | null;
  title: string;
  repo: string;
  /** Why it is here, in plain words. */
  why: string;
  session: string;
  pr: string;
  issue: string;
};

const NEEDS_ORDER: NeedsKind[] = ["taken", "failed", "escalated", "review", "critical"];

function mineByHistory(it: HubBoardItem, status: HubStatusData | null): boolean {
  const me = status?.deviceName;
  if (!me) return false;
  const last = [...(it.history ?? [])].reverse().find((h) => h.deviceName);
  return last?.deviceName === me;
}

/**
 * The rows that need a person: chores this Mac lost to another Mac, this
 * Mac's failed chores, escalated chores, this Mac's pull requests in review
 * for over three days, and critical chores nobody took. One row per item, in
 * that order. Display only: what a person may DO about a row is still the
 * daemon's answer (`local.claimable`).
 */
export function needsYou(items: HubBoardItem[], status: HubStatusData | null, now = Date.now()): NeedsRow[] {
  const byId = new Map(items.map((it) => [it.id, it]));
  const rows = new Map<string, NeedsRow>();
  const add = (kind: NeedsKind, id: string, it: HubBoardItem | null, why: string, s?: HubSessionInfo) => {
    if (rows.has(id)) return;
    rows.set(id, {
      kind,
      id,
      item: it,
      title: it ? itemTitle(it) : s?.summary || id,
      repo: it?.repo ?? "",
      why,
      session: s?.session || (it ? localSession(it, status) : ""),
      pr: it ? prURL(it) : "",
      issue: it?.escalation?.url ?? "",
    });
  };
  for (const s of status?.sessions ?? []) {
    if (s.leaseTaken && !s.released) add("taken", s.itemId, byId.get(s.itemId) ?? null, "Another Mac has taken it. The session here keeps running.", s);
  }
  for (const it of items) {
    if (it.state === "failed" && (it.owner?.mine || !!localSession(it, status) || mineByHistory(it, status))) {
      add("failed", it.id, it, ["Failed", whyLine(it)].filter(Boolean).join(" · "));
    }
  }
  for (const it of items) {
    if (it.state === "escalated") {
      const where = it.escalation?.identifier ? `handed to a person in ${it.escalation.identifier}` : it.escalation?.pending ? "a Linear issue is on its way" : "handed to a person";
      add("escalated", it.id, it, ["Escalated", whyLine(it), where].filter(Boolean).join(" · "));
    }
  }
  for (const it of items) {
    const since = time(reviewSince(it));
    if (it.state === "in_review" && it.owner?.mine && since && now - since > REVIEW_STALE_MS) {
      add("review", it.id, it, `In review for ${spanText(now - since)}${it.outcome?.delivery ? ` · ${deliveryLabel(it.outcome.delivery)}` : ""}`);
    }
  }
  for (const it of items) {
    if (it.state === "open" && (it.severity ?? "").toLowerCase() === "critical") {
      add("critical", it.id, it, ["Critical and nobody has taken it", whyLine(it)].filter(Boolean).join(" · "));
    }
  }
  return [...rows.values()].sort((a, b) => NEEDS_ORDER.indexOf(a.kind) - NEEDS_ORDER.indexOf(b.kind));
}

// --- Off-screen notices -----------------------------------------------------

export type HubNotice = { text: string; kind: "good" | "warn" | "bad"; itemId: string };

/**
 * What changed between two status reads that a person should hear about while
 * looking elsewhere: a chore this Mac took on its own, a chore that failed,
 * and a chore another Mac took over. `claimedHere` are the items a person
 * just claimed from the UI (those already got their own message). Nothing is
 * said on the first read.
 */
export function hubNotices(prev: HubStatusData | null, next: HubStatusData | null, claimedHere: Set<string> = new Set()): HubNotice[] {
  if (!prev?.joined || !next?.joined) return [];
  const before = new Map((prev.sessions ?? []).map((s) => [s.itemId, s]));
  const out: HubNotice[] = [];
  for (const s of next.sessions ?? []) {
    const name = s.summary || kindLabel(s.kind);
    const was = before.get(s.itemId);
    if (!was) {
      if (!claimedHere.has(s.itemId)) out.push({ text: `This Mac took a team chore: ${name}`, kind: "good", itemId: s.itemId });
      continue;
    }
    if ((s.reported ?? "").startsWith("fail") && !(was.reported ?? "").startsWith("fail")) {
      const reason = (s.reported ?? "").split(":")[1] ?? "";
      out.push({ text: `Team chore failed: ${name}${reason ? ` (${failWords(reason)})` : ""}`, kind: "bad", itemId: s.itemId });
    } else if (s.leaseTaken && !was.leaseTaken) {
      out.push({ text: `Another Mac took ${name}. The session keeps running.`, kind: "warn", itemId: s.itemId });
    }
  }
  return out;
}

/** "3 fixed this week · 2h on average"; "" when there is nothing to say. */
export function statsLine(s: { available: boolean; fixedThisWeek: number; avgFixSeconds: number } | null | undefined): string {
  if (!s?.available) return "";
  const fixed = `${s.fixedThisWeek} fixed this week`;
  if (!s.fixedThisWeek || !s.avgFixSeconds) return fixed;
  return `${fixed} · ${spanText(s.avgFixSeconds * 1000)} to fix on average`;
}
