// What a session is offered and where it opens: the pure half of the session view.
//
// Nothing here touches a store, the DOM or a binding, so every rule can be read and tested
// on its own. The stores (terms, ssh) and the frame components call it.
//
// The glyphs and line 2 of a roster row live here too, because the roster, the footer and
// the Glance share one vocabulary and a copy in each would drift.

import type { SessionInfo } from "@bindings/internal/protocol";
import { isEnded, sshNeedsHuman, sshTabs, type SSHConnInfo } from "./ssh";
import { displayFor } from "./theme";
import { agoPhrase, toMillis } from "./time";

/**
 * Every agent session gets a Conversation. Kept as a named constant, and as the default of
 * the `forAll` arguments below, so a test can state the rule it is about.
 */
export const CONVERSATION_FOR_ALL = true;

export type SessionViewKind = "conversation" | "terminal";

type Shape = Partial<SessionInfo>;

/** No agent runs here: a temporary folder, or a shell in a project. */
export function isAgentless(s: Shape): boolean {
  return s.location === "temporary" || s.status === "shell" || s.agentState === "shell";
}

/**
 * Agents whose log lola can read. Mirrors `feedCap` in internal/agent/caps_feed.go, because
 * the first paint has no talkState to ask. Unverified means not offered, so this grows only
 * after a real capture of that agent.
 */
const FEED_AGENTS = new Set(["", "claude"]);

export function hasFeedReader(agent: string | undefined): boolean {
  return FEED_AGENTS.has(agent ?? "");
}

/** `s.ssh` alone proves nothing: a session that posted one narration line has one. */
export function hasConnections(s: Shape): boolean {
  return (s.ssh?.connections?.length ?? 0) > 0;
}

export function conversationOffered(s: Shape, forAll: boolean = CONVERSATION_FOR_ALL): boolean {
  if (isAgentless(s)) return false;
  return s.location === "ssh" || hasConnections(s) || forAll;
}

/** lola's own prefix when a project and the defaults name none. Mirrors config.DefaultBranchPrefix. */
export const DEFAULT_BRANCH_PREFIX = "lola/";

/**
 * A new session's branch, so a prompt alone is enough to start one: the prefix, then a slug of the
 * first six words. Lowercase ascii, digits and hyphens only, because a git ref refuses more than
 * the config slug does (`..`, a trailing `.`). "" when nothing usable is left, which leaves the
 * Options field for a person to fill.
 */
export function branchFromPrompt(prompt: string, prefix: string = DEFAULT_BRANCH_PREFIX): string {
  const words = prompt.trim().split(/\s+/).slice(0, 6).join(" ");
  const tail = words.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  return tail ? `${prefix}${tail}` : "";
}

/** A goal's attempt has no diff to review, and a direct session has no checkout. */
export function changesOffered(s: Shape): boolean {
  return !!s.worktree && s.location !== "ssh" && !s.goalId && !isAgentless(s);
}

/**
 * Where a session opens when nothing this run has chosen yet. The order is the rule, so read
 * it top down. `feedKnown` is false when the last health this client saw was bad. A view that
 * learns that after it painted stays where it is, which is why the caller pins the answer.
 */
export function defaultView(
  s: Shape,
  sticky: SessionViewKind | null | undefined,
  globalPref: SessionViewKind | "" | null | undefined,
  feedKnown?: boolean,
  forAll: boolean = CONVERSATION_FOR_ALL,
): SessionViewKind {
  if (isAgentless(s) || s.agentState === "orphaned") return "terminal";
  if (s.goalId) return "terminal";
  if (sticky === "terminal") return "terminal";
  if (sticky === "conversation" && conversationOffered(s, forAll)) return "conversation";
  // The default of Conversation decides nothing here, or the rules below would never run.
  if (globalPref === "terminal") return "terminal";
  // Its story comes from the store, so this holds whatever the agent is.
  if (s.location === "ssh" || hasConnections(s)) return "conversation";
  if (!hasFeedReader(s.agent) || feedKnown === false) return "terminal";
  return forAll ? "conversation" : "terminal";
}

/** Whether a session has a Report to open: its board, or what the agent posted. */
export function hasReport(hasBoardChip: boolean, brief: { summary?: unknown; plan?: unknown; findings: unknown[]; theories: { open: number; confirmed: number; refuted: number } }): boolean {
  return hasBoardChip || !!brief.summary || !!brief.plan || brief.findings.length > 0 || brief.theories.open + brief.theories.confirmed + brief.theories.refuted > 0;
}

/** The health a client treats as "this log cannot be read", for `feedKnown`. */
export function feedKnownFrom(health: string | undefined): boolean | undefined {
  if (health === "unreadable" || health === "unsupported") return false;
  return health ? true : undefined;
}

// --- the strip ----------------------------------------------------------------

export type StripCell =
  | { key: "conversation"; kind: "conversation" }
  | { key: "terminal"; kind: "terminal" }
  | { key: "changes"; kind: "changes" }
  | { key: string; kind: "host"; conn: SSHConnInfo; label: string }
  | { key: string; kind: "shell" | "review" | "dev"; name: string };

export function stripCells(s: Shape, shells: string[], forAll: boolean = CONVERSATION_FOR_ALL): { views: StripCell[]; machines: StripCell[] } {
  const views: StripCell[] = [];
  if (conversationOffered(s, forAll)) views.push({ key: "conversation", kind: "conversation" });
  views.push({ key: "terminal", kind: "terminal" });
  if (changesOffered(s)) views.push({ key: "changes", kind: "changes" });
  const machines: StripCell[] = [];
  for (const t of sshTabs(s.ssh?.connections)) {
    if (!isEnded(t.conn)) machines.push({ key: `host:${t.conn.id}`, kind: "host", conn: t.conn, label: t.label });
  }
  for (const name of shells) {
    // Dev controls live in the overview, not in the view strip or its keyboard ring.
    if (/-dev-\d+$/.test(name)) continue;
    machines.push({ key: name, kind: name.endsWith("-review") ? "review" : "shell", name });
  }
  return { views, machines };
}

/** An agentless session has one thing to show until it has a second terminal. */
export function stripShown(s: Shape, machines: number): boolean {
  return !isAgentless(s) || machines > 0;
}

// --- the state vocabulary ------------------------------------------------------

export type StateGlyph = { glyph: string; cls: string; label: string; mono?: boolean };

/** The agent axis, for a record from a daemon that predates it. */
function axisOf(s: Shape): string {
  if (s.agentState) return s.agentState;
  switch (s.status) {
    case "dead":
    case "session_ended":
      return "dead";
    case "shell":
      return "shell";
    case "needs_input":
      return "waiting_input";
    case "idle":
      return "idle";
    default:
      return "";
  }
}

/** `seenAt` is when the human last had this session open, 0 if never. */
export function stateGlyph(s: Shape, seenAt: number = 0): StateGlyph {
  if (sshNeedsHuman(s as { ssh?: SessionInfo["ssh"] })) return { glyph: "◉", cls: "text-orange", label: "Needs you" };
  switch (displayFor(axisOf(s))) {
    case "needs_you":
      return { glyph: "◉", cls: "text-orange", label: "Needs you" };
    case "idle": {
      const since = toMillis(s.agentStateSince as unknown as string);
      return !Number.isNaN(since) && since > seenAt
        ? { glyph: "●", cls: "text-ink-2", label: "Finished a turn" }
        : { glyph: "○", cls: "text-faint", label: "Resting" };
    }
    case "gone":
      return { glyph: "✕", cls: "text-faint", label: "Gone" };
    case "orphaned":
      return { glyph: "✕", cls: "text-faint", label: "Orphaned" };
    case "shell":
      return { glyph: "$", cls: "text-faint", label: "Shell", mono: true };
    default:
      return { glyph: "◐", cls: "text-info", label: "Working" };
  }
}

export type RosterLine = { text: string; tone: "orange" | "bad" | "faint" };

const BAD_DELIVERY: Record<string, string> = {
  ci_failed: "CI failed",
  changes_requested: "Changes requested",
  merge_conflict: "Conflict",
};

const ASKS: Record<string, string> = {
  permission_prompt: "Wants permission",
  question: "Has a question",
  dialog: "A screen needs a key",
};

function folderName(path: string | undefined): string {
  return path?.replace(/\/+$/, "").split("/").pop() ?? "";
}

/**
 * Whether the last turn ended on a person's Stop, from what is known of its story. The two kinds
 * here are the only rows that end a turn. A session nobody has viewed has no story here, and the
 * agent axis alone cannot tell a rest from a stop, so it reads as a rest.
 */
export function lastTurnStopped(items: readonly { system?: { kind?: string } | null }[] | undefined): boolean {
  for (let i = (items?.length ?? 0) - 1; i >= 0; i--) {
    const kind = items?.[i].system?.kind;
    if (kind === "stopped") return true;
    if (kind === "checkpoint") return false;
  }
  return false;
}

/** Line 2 of a roster row: the first of these that applies. */
export function rosterLine(s: Shape, now: number = Date.now(), stopped = false): RosterLine {
  const next = s.ssh?.nextApproval;
  if (next?.host) return { text: `Approve on ${next.host}`, tone: "orange" };
  if (s.ssh?.needsHuman) return { text: s.ssh.needsHumanWhy || "Needs you", tone: "orange" };
  const axis = axisOf(s);
  if (displayFor(axis) === "needs_you") return { text: ASKS[s.inputReason ?? ""] ?? "Needs you", tone: "orange" };
  const bad = BAD_DELIVERY[s.delivery ?? ""];
  if (bad) return { text: bad, tone: "bad" };
  const d = displayFor(axis);
  if (d === "shell") {
    const folder = folderName(s.worktree);
    return { text: folder && folder !== s.title ? folder : "Shell", tone: "faint" };
  }
  if (d === "working" && axis !== "") {
    const age = agoPhrase(s.agentStateSince as unknown as string, now).replace(/ ago$/, "");
    const what = s.currentTool || s.ssh?.connections?.[0]?.host || "Working";
    return { text: age && age !== "just now" ? `${what} · ${age}` : what, tone: "faint" };
  }
  if (s.board?.blocked) return { text: `Says blocked: ${s.board.blocked}`, tone: "faint" };
  const pr = s.prNumber ? `#${s.prNumber}` : "";
  const when = agoPhrase(s.agentStateSince as unknown as string, now);
  const word = d === "gone" ? "Exited" : d === "orphaned" ? "Orphaned" : d === "working" ? "" : stopped ? "Stopped" : "Done";
  const parts = [word && when ? `${word} ${when}` : word, pr].filter(Boolean);
  return { text: parts.join(" · "), tone: "faint" };
}

/** Line 2 of a roster row in the Ended group: why it ended, or how long ago when lola has no word. */
export function endedLine(e: { reason?: string; endedAt?: string }, now: number = Date.now()): string {
  return e.reason || agoPhrase(e.endedAt, now);
}

/** A running clock's shape: 41s, 3m 12s, 1h 4m. */
export function elapsedWords(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ${sec % 60}s`;
  return `${Math.floor(min / 60)}h ${min % 60}m`;
}

// --- the roster ----------------------------------------------------------------

export type RosterGroup = { key: string; kind: "project" | "ssh" | "temporary"; label: string; rows: SessionInfo[] };

function stableOrder(a: SessionInfo, b: SessionInfo): number {
  const at = toMillis(a.firstSeen as unknown as string);
  const bt = toMillis(b.firstSeen as unknown as string);
  const d = (Number.isNaN(at) ? 0 : at) - (Number.isNaN(bt) ? 0 : bt);
  return d !== 0 ? d : a.id.localeCompare(b.id);
}

/**
 * Config project order, then when a session first appeared, then its id. Never state and
 * never recency: a row that moves by itself is the one thing a person cannot learn to find.
 * Projects unknown to the config (removed, or not yet pushed) follow the configured ones.
 */
export function rosterGroups(sessions: SessionInfo[], projects: string[], label: (name: string) => string): RosterGroup[] {
  const byProject = new Map<string, SessionInfo[]>();
  const direct: SessionInfo[] = [];
  const temporary: SessionInfo[] = [];
  for (const s of sessions) {
    if (s.location === "ssh") direct.push(s);
    else if (s.location === "temporary") temporary.push(s);
    else {
      const list = byProject.get(s.project) ?? [];
      list.push(s);
      byProject.set(s.project, list);
    }
  }
  const known = projects.filter((p) => byProject.has(p));
  const rest = [...byProject.keys()].filter((p) => !projects.includes(p)).sort();
  const groups: RosterGroup[] = [...known, ...rest].map((p) => ({
    key: `p:${p}`,
    kind: "project",
    label: label(p) || "Other",
    rows: [...byProject.get(p)!].sort(stableOrder),
  }));
  groups.push({ key: "ssh", kind: "ssh", label: "SSH", rows: direct.sort(stableOrder) });
  groups.push({ key: "temporary", kind: "temporary", label: "Temporary", rows: temporary.sort(stableOrder) });
  return groups;
}
