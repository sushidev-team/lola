// Agent-assisted SSH: every decision the workspace makes that needs no DOM.
//
// docs/ssh-sessions-ux.md is the spec for what a human reads; this file is where
// its tables live, so tests can pin them and the components stay about layout.
//
// The vocabulary below mirrors internal/protocol/ssh.go with no compiler in
// common, and desktop/ssh_parity_test.go reads this file as text to pin it, the
// same pattern as state_parity_test.go and theme.ts. A sentence that exists as a
// constant in Go is read from here, never retyped at a call site.
//
// FACTS and CLAIMS are different on the wire and must look different on screen.
// Nothing in this file turns a claim into a fact: an element never produces a
// plate, a solid marker or a green tick, and severity never raises attention.

import type {
  SSHBlock,
  SSHConnInfo,
  SSHPromptInfo,
  SSHElement,
  SSHHostInfo,
  SSHHumanMessage,
  SSHSessionInfo,
  SSHTimelineItem,
} from "@bindings/internal/protocol";
import { toMillis } from "./time";

export type { SSHBlock, SSHConnInfo, SSHElement, SSHPromptInfo, SSHHostInfo, SSHHumanMessage, SSHSessionInfo, SSHTimelineItem };

// --- vocabulary (pinned by desktop/ssh_parity_test.go) ----------------------

export const SSH_ELEMENT_KINDS: string[] = [
  "summary", "narration", "hypothesis", "finding", "plan", "metric", "evidence", "recommendation", "proposal", "question", "widget",
];
export const SSH_SEVERITIES: string[] = ["info", "low", "medium", "high", "critical"];
export const SSH_STEP_STATES: string[] = ["pending", "active", "done", "skipped", "failed"];
export const SSH_CONN_STATES: string[] = [
  "requested", "denied", "connecting", "authenticating", "connected", "disconnected", "failed", "closed",
];
export const SSH_BLOCK_STATUSES: string[] = [
  "pending_approval", "queued", "running", "denied", "expired", "refused", "succeeded", "failed", "timed_out", "stopped", "lost",
];
export const SSH_EVENT_KINDS: string[] = [
  "requested", "go", "denied", "connected", "disconnected", "failed", "wheel_taken", "handed_back", "grant_created", "grant_ended", "closed",
  "session_started", "session_ended", "agent_exited", "stopped", "checkpoint", "pr", "ci", "review", "handoff",
];

export const SSH_DATA_WARNING = "Everything the agent reads here is sent to its model provider.";
export const SSH_REMOTE_OUTLIVES = "Stopping ends the local ssh. The remote process may keep running.";
export const SSH_USER_IS_THE_LIMIT = "Lola does not filter commands. The remote user's permissions are the real limit.";
export const SSH_UNSANDBOXED_NOTE =
  "This agent runs unsandboxed as you. The approval gate stops accidents and well-behaved agents, not a determined one.";
export const SSH_DRIVING_NOTE = "Your commands appear in the timeline, visible only to you until you share them.";
export const SSH_DROVE_NOTE = "Anything inside a nested shell, or a shell lola could not read, is not in this record.";
export const SSH_ENDED_OUTLIVES = "The remote process may keep running if any were still going.";
export const SSH_TRANSCRIPT_WARNING = "This text goes to the agent and its model provider. Remove anything you would not paste there.";

export const SSH_MAX_SAY_BYTES = 131072;
export const SSH_MAX_HANDBACK_BYTES = 8192;
export const SSH_MAX_POLL_BYTES = 262144;

/** Lola keeps this much of a stream's end when its middle was dropped. */
export const SSH_TAIL_BYTES = 64 * 1024;
/** A summary older than this fades: a confident stale summary is worse than none. */
export const SSH_STALE_MS = 15 * 60 * 1000;
/** How long a confirm control stays dead after a card appears or its command changes. */
export const SSH_ARM_MS = 800;

// --- names ------------------------------------------------------------------

/** The agent's display name in copy. */
export function agentName(kind: string | undefined): string {
  switch (kind) {
    case "codex":
      return "Codex";
    case "opencode":
      return "OpenCode";
    default:
      return "Claude";
  }
}

/** A host's display name: its label, else its catalog name. */
export function hostName(c: Pick<SSHConnInfo, "host" | "label"> | undefined): string {
  return c?.label || c?.host || "";
}

/**
 * "What your ssh config says this alias means": resolved facts first, then what
 * config said, then the bare alias. Returns "" only when there is nothing at all.
 */
export function identity(c: Pick<SSHConnInfo, "alias" | "user" | "resolvedUser" | "resolvedHostname">): string {
  if (c.resolvedHostname) return c.resolvedUser ? `${c.resolvedUser}@${c.resolvedHostname}` : c.resolvedHostname;
  if (c.user) return `${c.user}@${c.alias}`;
  return c.alias ?? "";
}

// --- tier -------------------------------------------------------------------

export type Tier = "production" | "staging" | "dev";

/** Unset and unknown read as production: a host nobody classified is the strictest. */
export function tierOf(tier: string | undefined): Tier {
  return tier === "staging" || tier === "dev" ? tier : "production";
}

export function tierWord(tier: string | undefined): string {
  return tierOf(tier).toUpperCase();
}

export function tierShort(tier: string | undefined): string {
  return { production: "PROD", staging: "STG", dev: "DEV" }[tierOf(tier)];
}

export function tierTagClass(tier: string | undefined): string {
  return {
    production: "border-bad/60 text-bad",
    staging: "border-warn/50 text-warn",
    dev: "border-edge text-faint",
  }[tierOf(tier)];
}

/** The 2px band across the workspace and every card. "" means no band: dev has none. */
export function tierBandClass(tier: string | undefined): string {
  return { production: "bg-bad/70", staging: "bg-warn/50", dev: "" }[tierOf(tier)];
}

/**
 * The tier the band above the host bar shows: the host in scope, or under All the highest tier
 * with a connection that is up or coming up. A host that has only asked is not live yet.
 */
export function bandTier(conns: readonly Pick<SSHConnInfo, "id" | "tier" | "state">[], scope: string): string | undefined {
  const picked = conns.find((c) => c.id === scope);
  if (picked) return picked.tier;
  const live = conns.filter((c) => !isEnded(c) && c.state !== "requested");
  for (const tier of ["production", "staging", "dev"] as const) {
    const hit = live.find((c) => tierOf(c.tier) === tier);
    if (hit) return hit.tier;
  }
  return "dev";
}

// --- numbers ----------------------------------------------------------------

export function formatBytes(n: number | undefined): string {
  const v = n ?? 0;
  if (v < 1024) return `${v} B`;
  if (v < 1024 * 1024) return `${Math.round(v / 1024)} KB`;
  const mb = v / (1024 * 1024);
  return `${mb < 10 ? mb.toFixed(1) : Math.round(mb)} MB`;
}

export function formatMs(ms: number | undefined): string {
  const v = Math.max(0, ms ?? 0);
  if (v < 60_000) {
    const s = v / 1000;
    return `${s < 10 ? s.toFixed(1) : Math.round(s)}s`;
  }
  const m = Math.floor(v / 60_000);
  const s = Math.floor((v % 60_000) / 1000);
  return s ? `${m}m ${s}s` : `${m}m`;
}

/** A short whole-unit duration: "2m", "45s", "1h 5m". */
export function formatSeconds(sec: number | undefined): string {
  const v = Math.max(0, Math.round(sec ?? 0));
  if (v < 60) return `${v}s`;
  if (v < 3600) {
    const s = v % 60;
    return s ? `${Math.floor(v / 60)}m ${s}s` : `${Math.floor(v / 60)}m`;
  }
  const m = Math.floor((v % 3600) / 60);
  return m ? `${Math.floor(v / 3600)}h ${m}m` : `${Math.floor(v / 3600)}h`;
}

/**
 * How long an approval has left. Whole minutes while there is plenty, so a screen with
 * several pending rows stays still, and seconds only for the last 5 minutes.
 */
export function expiryWords(sec: number): string {
  const v = Math.max(0, sec);
  return v >= 300 ? `${Math.ceil(v / 60)} min` : formatSeconds(v);
}

/** A duration inside a sentence: "15 minutes", "1 hour", "45 seconds". */
export function spokenSeconds(sec: number | undefined): string {
  const v = Math.max(0, Math.round(sec ?? 0));
  if (v < 60) return plural(v, "second");
  if (v < 3600) return plural(Math.round(v / 60), "minute");
  return plural(Math.round(v / 3600), "hour");
}

/** A countdown or a running clock: "4:12". */
export function mmss(ms: number): string {
  const v = Math.max(0, Math.floor(ms / 1000));
  return `${Math.floor(v / 60)}:${String(v % 60).padStart(2, "0")}`;
}

/** Whole minutes between two instants, at least 1 once any time has passed. */
export function minutesBetween(from: string | undefined, to: string | undefined): number {
  const a = toMillis(from);
  const b = toMillis(to);
  if (Number.isNaN(a) || Number.isNaN(b)) return 0;
  return Math.max(1, Math.round((b - a) / 60_000));
}

export function clockTime(at: string | undefined): string {
  const t = toMillis(at);
  if (Number.isNaN(t)) return "";
  return new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export function clockSeconds(at: string | undefined): string {
  const t = toMillis(at);
  if (Number.isNaN(t)) return "";
  return new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length;
}

// --- connection state -------------------------------------------------------

export type ConnView = {
  /** The word in the header. */
  word: string;
  /** Classes for the word. */
  text: string;
  /** Classes for the dot. Hollow means nothing is up. */
  dot: string;
  /** A hollow dot rather than a solid one. */
  hollow: boolean;
  /** Only "connecting" may pulse: motion means happening now. */
  pulse: boolean;
};

/** Section 12's header column. An unknown state renders its own word, never a false "Connected". */
export function connView(c: Pick<SSHConnInfo, "state" | "stateDetail" | "tabAlive" | "connectedAt">, now: number = Date.now()): ConnView {
  switch (c.state) {
    case "requested":
      return { word: "Waiting for your go", text: "text-orange", dot: "border-orange", hollow: true, pulse: false };
    case "denied":
      return { word: "Denied", text: "text-faint", dot: "border-faint", hollow: true, pulse: false };
    case "connecting":
      return { word: "Connecting…", text: "text-info", dot: "bg-info", hollow: false, pulse: true };
    case "authenticating":
      return { word: "Signing in", text: "text-orange", dot: "bg-orange", hollow: false, pulse: false };
    case "connected": {
      const t = toMillis(c.connectedAt);
      const age = Number.isNaN(t) ? "" : ` ${connectedAge(now - t)}`;
      return { word: `Connected${age}`, text: "text-ink", dot: "bg-good", hollow: false, pulse: false };
    }
    case "disconnected":
      return isIdleDrop(c)
        ? { word: "Idle, connection closed", text: "text-faint", dot: "border-faint", hollow: true, pulse: false }
        : { word: "Disconnected", text: "text-warn", dot: "border-warn", hollow: true, pulse: false };
    case "failed":
      return { word: "Could not connect", text: "text-bad", dot: "border-bad", hollow: true, pulse: false };
    case "closed":
      return { word: "Closed", text: "text-faint", dot: "border-faint", hollow: true, pulse: false };
    default:
      return { word: c.state || "Unknown", text: "text-faint", dot: "border-faint", hollow: true, pulse: false };
  }
}

function connectedAge(ms: number): string {
  const min = Math.max(0, Math.floor(ms / 60_000));
  if (min < 1) return "now";
  if (min < 60) return `${min}m`;
  const h = Math.floor(min / 60);
  return `${h}h ${min % 60}m`;
}

/** The daemon says so in StateDetail. The word is its own sentence's. */
export function isIdleDrop(c: Pick<SSHConnInfo, "state" | "stateDetail">): boolean {
  return c.state === "disconnected" && /\bidle\b/i.test(c.stateDetail ?? "");
}

export function isLive(c: Pick<SSHConnInfo, "state">): boolean {
  return c.state === "connected";
}

export function isEnded(c: Pick<SSHConnInfo, "state">): boolean {
  return c.state === "closed" || c.state === "denied";
}

export function grantActive(c: Pick<SSHConnInfo, "grant">, now: number = Date.now()): boolean {
  const t = toMillis(c.grant?.expiresAt);
  return !Number.isNaN(t) && t > now;
}

// --- the chip (section 15) --------------------------------------------------

export type ChipFact = { words: string; cls: string };

/**
 * The first of section 15's table that applies. null means no chip: a connection
 * that is closed or denied is finished business. Its order is the urgency order
 * of the contract: authentication, approval, go.
 */
export function chipFact(c: SSHConnInfo, now: number = Date.now()): ChipFact | null {
  if (c.prompt) return { words: "asking", cls: "text-orange" };
  if (c.state === "authenticating") return { words: "sign in", cls: "text-orange" };
  if ((c.pendingApprovals ?? 0) > 0) return { words: `${c.pendingApprovals} to approve`, cls: "text-orange" };
  if (c.state === "requested") return { words: "wants to connect", cls: "text-orange" };
  if (isEnded(c)) return null;
  if (c.wheel === "human") return { words: "you're driving", cls: "text-accent-ink" };
  if (grantActive(c, now)) return { words: `grant ${mmss(toMillis(c.grant?.expiresAt) - now)}`, cls: "text-warn" };
  if (c.state === "connecting") return { words: "connecting…", cls: "text-faint" };
  if (c.state === "disconnected") return { words: "disconnected", cls: "text-warn" };
  if (c.state === "failed") return { words: "could not connect", cls: "text-bad" };
  if ((c.runningBlocks ?? 0) > 0) return { words: "running", cls: "text-info" };
  return { words: "connected", cls: "text-faint" };
}

function chipRank(c: SSHConnInfo, now: number): number {
  const f = chipFact(c, now);
  if (!f) return 99;
  const order = ["asking", "sign in", "to approve", "wants to connect", "you're driving", "grant", "connecting", "disconnected", "could not connect", "running", "connected"];
  const i = order.findIndex((w) => f.words.includes(w));
  return i < 0 ? 98 : i;
}

export type Chip = { conn: SSHConnInfo; fact: ChipFact; more: number };

/** The most urgent connection, and how many others have a chip of their own. */
export function chipFor(ssh: SSHSessionInfo | null | undefined, now: number = Date.now()): Chip | null {
  const conns = (ssh?.connections ?? []).filter((c) => chipFact(c, now));
  if (!conns.length) return null;
  const sorted = [...conns].sort((a, b) => chipRank(a, now) - chipRank(b, now));
  const conn = sorted[0];
  return { conn, fact: chipFact(conn, now)!, more: conns.length - 1 };
}

/** ORs into every attention surface. Set only by daemon facts, never by anything posted. */
export function sshNeedsHuman(s: { ssh?: SSHSessionInfo | null } | null | undefined): boolean {
  return !!s?.ssh?.needsHuman;
}

/** The count on the strip's tab: approvals plus connections awaiting a go. */
export function tabCount(conns: SSHConnInfo[] | null | undefined): number {
  return (conns ?? []).reduce((n, c) => n + (c.pendingApprovals ?? 0) + (c.state === "requested" ? 1 : 0), 0);
}

/** A connection's own count, for its host chip. */
export function connCount(c: SSHConnInfo): number {
  return (c.pendingApprovals ?? 0) + (c.state === "requested" ? 1 : 0);
}

export type SSHTabRow = { conn: SSHConnInfo; label: string; count: number };

/**
 * One tab per connection, named after its host, with a number on a second tab to the same
 * one. A denied request never became a connection, so it has no tab.
 */
export function sshTabs(conns: SSHConnInfo[] | null | undefined): SSHTabRow[] {
  const seen = new Map<string, number>();
  return (conns ?? [])
    .filter((c) => c.state !== "denied")
    .map((conn) => {
      const n = (seen.get(conn.host) ?? 0) + 1;
      seen.set(conn.host, n);
      return { conn, label: n > 1 ? `${hostName(conn)} ${n}` : hostName(conn), count: connCount(conn) + (conn.prompt ? 1 : 0) };
    });
}

/** The connection an item belongs to, "" for one that shows in every tab. */
export function itemConn(i: SSHTimelineItem): string {
  return i.block?.conn ?? i.element?.conn ?? i.system?.conn ?? i.human?.conn ?? "";
}

/** Which connection a jump should land on: authentication, approval, go, then a live one. */
export function urgentConn(conns: SSHConnInfo[] | null | undefined): SSHConnInfo | undefined {
  const list = conns ?? [];
  return (
    list.find((c) => !!c.prompt) ??
    list.find((c) => c.state === "authenticating") ??
    list.find((c) => (c.pendingApprovals ?? 0) > 0) ??
    list.find((c) => c.state === "requested") ??
    list.find((c) => c.state === "connected") ??
    list.find((c) => !isEnded(c)) ??
    list[list.length - 1]
  );
}

/** "n commands" with the plural the copy asks for. */
export function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

// --- blocks (section 7) -----------------------------------------------------

export type MarkerKind = "pass" | "fail" | "running" | "blocked" | "pending" | "refused" | "unknown";

export type BlockView = {
  marker: MarkerKind;
  /** The marker's colour when it is not the marker's own. */
  markerCls: string;
  /** Line 1's right side. */
  result: string;
  resultCls: string;
  /** Line 2, or the detail row when expanded: lola's own sentence. */
  detail: string;
  /** Plate tint and border. Every string is a literal for Tailwind's scanner. */
  plate: string;
  /** Faded: this never ran. */
  muted: boolean;
  /** Line 2 is the output preview rather than a sentence. */
  showsPreview: boolean;
};

export function isTerminal(status: string | undefined): boolean {
  return !["pending_approval", "queued", "running"].includes(status ?? "");
}

export function neverRan(status: string | undefined): boolean {
  return ["denied", "expired", "refused"].includes(status ?? "");
}

/** Elapsed run time: the daemon's own figure once it ended, the clock while it runs. */
export function blockElapsedMs(b: Pick<SSHBlock, "durationMs" | "startedAt" | "endedAt">, now: number = Date.now()): number {
  if (b.durationMs) return b.durationMs;
  const s = toMillis(b.startedAt);
  if (Number.isNaN(s)) return 0;
  const e = toMillis(b.endedAt);
  return Math.max(0, (Number.isNaN(e) ? now : e) - s);
}

/** The whole status table of section 7.1. An unknown status renders words, never a false pass. */
export function blockView(b: SSHBlock, now: number = Date.now()): BlockView {
  const size = formatBytes((b.stdoutBytes ?? 0) + (b.stderrBytes ?? 0));
  const hasSize = (b.stdoutBytes ?? 0) + (b.stderrBytes ?? 0) > 0;
  const t = formatMs(blockElapsedMs(b, now));
  const base = { markerCls: "", resultCls: "text-faint", detail: "", plate: "border-edge/60", muted: false, showsPreview: false };
  switch (b.status) {
    case "pending_approval": {
      const left = toMillis(b.approvalExpiresAt) - now;
      return {
        ...base,
        marker: "blocked",
        result: "Waiting for you",
        resultCls: "text-orange",
        // The card below says the rest. A row only needs to say so when time is short.
        detail: Number.isNaN(left) || left > 300_000 ? "" : `Expires in ${expiryWords(left / 1000)}`,
        plate: "border-orange/40",
      };
    }
    case "queued":
      return { ...base, marker: "pending", result: "Queued", detail: "Approved. Waiting for a free slot." };
    case "running":
      return {
        ...base,
        marker: "running",
        result: b.timeoutSeconds ? `${t} of ${formatSeconds(b.timeoutSeconds)}` : t,
        plate: "border-info/40",
        showsPreview: true,
      };
    case "succeeded":
      return { ...base, marker: "pass", result: `exit ${b.exitCode ?? 0} · ${t}${hasSize ? ` · ${size}` : ""}` };
    case "failed":
      return {
        ...base,
        marker: "fail",
        result: `exit ${b.exitCode ?? "?"} · ${t}${hasSize ? ` · ${size}` : ""}`,
        resultCls: "text-bad",
        plate: "border-bad/35 bg-[color-mix(in_oklab,var(--color-bad)_6%,var(--color-panel))]!",
        showsPreview: true,
      };
    case "denied":
      return { ...base, marker: "refused", result: "Denied", detail: "You denied this.", muted: true };
    case "expired":
      return {
        ...base,
        marker: "refused",
        result: "Expired",
        detail: b.statusDetail || "Nobody answered in time. It did not run.",
        muted: true,
      };
    case "refused":
      return { ...base, marker: "refused", result: "Did not run", detail: b.statusDetail ?? "", muted: true };
    case "timed_out":
      return {
        ...base,
        marker: "fail",
        markerCls: "text-warn",
        result: `Timed out · ${t}`,
        resultCls: "text-warn",
        detail: b.statusDetail || `Stopped locally at the ${t} limit. The remote process may still be running.`,
        plate: "border-warn/35 bg-[color-mix(in_oklab,var(--color-warn)_6%,var(--color-panel))]!",
      };
    case "stopped":
      return {
        ...base,
        marker: "fail",
        markerCls: "text-warn",
        result: `Stopped · ${t}`,
        resultCls: "text-warn",
        detail: [b.statusDetail, b.startedAt ? SSH_REMOTE_OUTLIVES : ""].filter(Boolean).join(" "),
        plate: "border-warn/35 bg-[color-mix(in_oklab,var(--color-warn)_6%,var(--color-panel))]!",
      };
    case "lost":
      return {
        ...base,
        marker: "unknown",
        markerCls: "text-warn",
        result: `Result unknown · ${t}`,
        resultCls: "text-warn",
        detail: b.statusDetail || "The connection dropped. The remote process may still be running.",
        plate: "border-warn/35 bg-[color-mix(in_oklab,var(--color-warn)_6%,var(--color-panel))]!",
      };
    default:
      return { ...base, marker: "unknown", result: b.status || "Unknown", detail: b.statusDetail ?? "" };
  }
}

/** The last non-empty line of a block's preview: what its second row shows. */
export function previewLine(b: Pick<SSHBlock, "preview">): string {
  const lines = (b.preview ?? "").split("\n");
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].trim()) return lines[i];
  }
  return "";
}

/** Which approval tag, if any, sits on line 1. The usual per-command path has none. */
export function blockTag(b: Pick<SSHBlock, "actor" | "approval">): { text: string; cls: string } | null {
  if (b.actor === "human") return { text: "You", cls: "text-accent-ink" };
  if (b.approval?.mode === "grant") return { text: "grant", cls: "text-warn" };
  return null;
}

/** The expanded block's "Allowed" row. */
export function allowedLine(b: Pick<SSHBlock, "actor" | "approval" | "status">, conn?: Pick<SSHConnInfo, "grant">): string {
  if (b.actor === "human" || b.approval?.mode === "human") return "You ran this";
  if (b.approval?.mode === "grant") {
    const until = clockTime(conn?.grant?.expiresAt);
    return until ? `Grant, until ${until}` : "Grant";
  }
  if (b.approval?.granted) return `You approved at ${clockTime(b.approval.at)}`.trim();
  return b.status === "pending_approval" ? "Waiting for you" : "Not allowed";
}

export type Stream = "stdout" | "stderr";

/** Opens on stderr when the block failed and it has bytes, otherwise stdout. */
export function defaultStream(b: Pick<SSHBlock, "status" | "stderrBytes">): Stream {
  return b.status === "failed" && (b.stderrBytes ?? 0) > 0 ? "stderr" : "stdout";
}

/** The viewer's empty and absent states. "" means there is text to show. */
export function outputNotice(
  b: Pick<SSHBlock, "status" | "stdoutBytes" | "stderrBytes" | "droppedBytes" | "outputPruned">,
  loadedBytes: number,
  totalBytes: number,
): string {
  if (b.outputPruned) {
    return "Lola removed this output to stay within its storage limit. The command, exit code and timing stay on record.";
  }
  if (neverRan(b.status)) return "This command did not run, so there is no output.";
  if (loadedBytes > 0 || totalBytes > 0) return "";
  if ((b.droppedBytes ?? 0) > 0) {
    return `Lola could not store this output. ${formatBytes(b.droppedBytes)} were lost. The exit code is still accurate.`;
  }
  if (b.status === "running" || b.status === "queued" || b.status === "pending_approval") return "No output yet.";
  return "No output.";
}

export const URL_RE = /https?:\/\/[^\s<>"'`]+/g;

export type OutputPart = { text: string; url: boolean };

/** Text nodes only. A token matching http(s) becomes a link through the daemon; nothing else is interactive. */
export function linkify(text: string): OutputPart[] {
  const parts: OutputPart[] = [];
  let at = 0;
  for (const m of text.matchAll(URL_RE)) {
    const raw = m[0].replace(/[.,;:!?)\]}]+$/, "");
    const start = m.index ?? 0;
    if (start > at) parts.push({ text: text.slice(at, start), url: false });
    parts.push({ text: raw, url: true });
    at = start + raw.length;
  }
  if (at < text.length) parts.push({ text: text.slice(at), url: false });
  return parts;
}

/** Tokens in agent text: bare block ids that name a real block, and URLs. */
export type Token = { kind: "text" | "url" | "block"; text: string };

export function tokenize(text: string, known: (id: string) => boolean): Token[] {
  const out: Token[] = [];
  const push = (kind: Token["kind"], t: string) => {
    if (!t) return;
    const last = out[out.length - 1];
    if (kind === "text" && last?.kind === "text") last.text += t;
    else out.push({ kind, text: t });
  };
  for (const part of linkify(text)) {
    if (part.url) {
      push("url", part.text);
      continue;
    }
    let at = 0;
    for (const m of part.text.matchAll(/\bb[0-9]+\b/g)) {
      if (!known(m[0])) continue;
      const start = m.index ?? 0;
      push("text", part.text.slice(at, start));
      push("block", m[0]);
      at = start + m[0].length;
    }
    push("text", part.text.slice(at));
  }
  return out;
}

/** Case-insensitive match offsets in loaded text, for the viewer's find. */
export function findOffsets(text: string, query: string, max = 5000): number[] {
  if (!query) return [];
  const hay = text.toLowerCase();
  const needle = query.toLowerCase();
  const out: number[] = [];
  let i = hay.indexOf(needle);
  while (i >= 0 && out.length < max) {
    out.push(i);
    i = hay.indexOf(needle, i + needle.length);
  }
  return out;
}

// --- the stream (section 6) -------------------------------------------------

export type Filter = "everything" | "facts" | "findings";
export const FILTERS: { id: Filter; label: string }[] = [
  { id: "everything", label: "Everything" },
  { id: "facts", label: "Facts" },
  { id: "findings", label: "Findings" },
];

const FINDING_KINDS = new Set(["finding", "hypothesis", "evidence", "recommendation", "proposal"]);

/** Facts: blocks and system events, with every claim gone. Findings: the conclusion view. */
export function matchesFilter(item: SSHTimelineItem, filter: Filter): boolean {
  if (filter === "facts") return item.kind === "block" || item.kind === "system";
  if (filter === "findings") return item.kind === "element" && FINDING_KINDS.has(item.element?.kind ?? "");
  return true;
}

/** Upsert by ID, sorted by the fixed Order. Seq moves on every change; Order never does. */
export function mergeItems(current: SSHTimelineItem[], incoming: SSHTimelineItem[] | null | undefined): SSHTimelineItem[] {
  if (!incoming?.length) return current;
  const byId = new Map(current.map((i) => [i.id, i]));
  for (const it of incoming) byId.set(it.id, it);
  return [...byId.values()].sort((a, b) => a.order - b.order);
}

/** Every block id some element's evidence points at: those must stay visible. */
export function evidenceTargets(items: SSHTimelineItem[]): Set<string> {
  const out = new Set<string>();
  for (const i of items) {
    for (const e of i.element?.evidence ?? []) out.add(e.block);
    if (i.element?.outcome?.block) out.add(i.element.outcome.block);
  }
  return out;
}

export type Row =
  | { kind: "item"; item: SSHTimelineItem }
  | { kind: "fold"; id: string; items: SSHTimelineItem[]; totalMs: number };

/**
 * Three or more consecutive blocks that all succeeded, with nothing between them,
 * fold into one plate. Never a failed, refused, stopped or running block, one a
 * human ran, or one an element's evidence points at, because a jump must always
 * land on something visible. The Facts filter folds nothing: it is the audit view.
 */
export function foldRows(items: SSHTimelineItem[], filter: Filter, opened: ReadonlySet<string> = new Set(), keep: ReadonlySet<string> = evidenceTargets(items)): Row[] {
  const rows: Row[] = [];
  if (filter === "facts") return items.map((item) => ({ kind: "item", item }));
  let run: SSHTimelineItem[] = [];
  const flush = () => {
    if (run.length >= 3) {
      const id = `fold-${run[0].id}`;
      if (opened.has(id)) for (const item of run) rows.push({ kind: "item", item });
      else rows.push({ kind: "fold", id, items: run, totalMs: run.reduce((n, i) => n + (i.block?.durationMs ?? 0), 0) });
    } else {
      for (const item of run) rows.push({ kind: "item", item });
    }
    run = [];
  };
  for (const item of items) {
    const b = item.block;
    if (item.kind === "block" && b?.status === "succeeded" && b.actor !== "human" && !keep.has(b.id)) run.push(item);
    else {
      flush();
      rows.push({ kind: "item", item });
    }
  }
  flush();
  return rows;
}

export type RailKind = "agent" | "human" | "down";

/**
 * The rail records the connection: a fact drawn as a line. Solid while connected,
 * accent while the human drove, dashed otherwise. Walked from the daemon's own
 * system events, then the current wheel covers a session that started driving.
 * conn narrows to one connection; "" follows them all.
 */
export function railKinds(items: SSHTimelineItem[], conn: Pick<SSHConnInfo, "id" | "wheel" | "state"> | undefined): RailKind[] {
  let up = false;
  let driving = false;
  const startedDriving = conn?.wheel === "human";
  const taken = items.some((i) => i.system?.kind === "wheel_taken" && (!conn || !i.system?.conn || i.system.conn === conn.id));
  const out: RailKind[] = [];
  for (const i of items) {
    const sys = i.system;
    if (sys && (!conn || !sys.conn || sys.conn === conn.id)) {
      switch (sys.kind) {
        case "connected":
          up = true;
          if (startedDriving && !taken) driving = true;
          break;
        case "disconnected":
        case "failed":
        case "closed":
        case "denied":
        case "requested":
          up = false;
          break;
        case "wheel_taken":
          driving = true;
          break;
        case "handed_back":
          driving = false;
          break;
      }
    }
    out.push(driving ? "human" : up ? "agent" : "down");
  }
  return out;
}

/** The "You drove for 12m" label beside a handback. */
export function droveFor(items: SSHTimelineItem[], handback: SSHTimelineItem): string {
  const idx = items.findIndex((i) => i.id === handback.id);
  for (let i = idx - 1; i >= 0; i--) {
    if (items[i].system?.kind === "wheel_taken" && (!handback.system?.conn || items[i].system?.conn === handback.system?.conn)) {
      return `You drove for ${minutesBetween(items[i].at, handback.at)}m. ${SSH_DROVE_NOTE}`;
    }
  }
  return SSH_DROVE_NOTE;
}

/** The fold over a handback's transcript: "Your terminal since 14:02 · 4.1 KB". */
export function transcriptLabel(items: SSHTimelineItem[], handback: SSHTimelineItem): string {
  const size = formatBytes(new TextEncoder().encode(handback.human?.transcript ?? "").length);
  const idx = items.findIndex((i) => i.id === handback.id);
  for (let i = idx - 1; i >= 0; i--) {
    const sys = items[i].system;
    if (sys?.kind === "wheel_taken" && (!handback.human?.conn || sys.conn === handback.human.conn)) {
      return `Your terminal since ${clockTime(items[i].at)} · ${size}`;
    }
  }
  return `Your terminal · ${size}`;
}

/** Does this item match the timeline's find? Output has its own find in the viewer. */
export function itemMatches(item: SSHTimelineItem, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return false;
  const e = item.element;
  const parts = [
    item.block?.command,
    item.block?.why,
    item.system?.text,
    item.system?.note,
    item.human?.text,
    e?.title,
    e?.body,
    e?.command,
    ...(e?.steps ?? []).map((s) => s.text),
    ...(e?.metrics ?? []).map((m) => `${m.name} ${m.value}`),
    ...(e?.choices ?? []),
  ];
  return parts.some((p) => p?.toLowerCase().includes(q));
}

/** Elements are claims, and a run of them names its author once. */
export function isClaim(item: SSHTimelineItem | undefined): boolean {
  return item?.kind === "element";
}

// --- the Brief (section 5) --------------------------------------------------

export type Brief = {
  summary?: { title: string; at: string; id: string; stale: boolean };
  plan?: { id: string; title: string; done: number; total: number; current: string };
  theories: { confirmed: number; open: number; refuted: number };
  findings: { severity: string; word: string; cls: string; count: number }[];
};

const SEVERITY_ORDER = ["critical", "high", "medium", "low", "info"];

export function severityView(sev: string | undefined): { word: string; ring: string; text: string } {
  switch (sev) {
    case "critical":
      return { word: "Critical", ring: "border-2 border-bad", text: "text-bad" };
    case "high":
      return { word: "High", ring: "border border-orange", text: "text-orange" };
    case "medium":
      return { word: "Medium", ring: "border border-warn", text: "text-warn" };
    case "low":
      return { word: "Low", ring: "border border-info", text: "text-faint" };
    default:
      return { word: "Note", ring: "border border-faint", text: "text-faint" };
  }
}

/** Plan progress in ▰▱. Never green: this is a claim of progress. */
export function planBar(done: number, total: number): string {
  if (total <= 0) return "";
  return "▰".repeat(Math.min(done, total)) + "▱".repeat(Math.max(0, total - done));
}

/** Derived by the client from the items: the agent's standing account. */
export function deriveBrief(items: SSHTimelineItem[], now: number = Date.now()): Brief {
  const brief: Brief = { theories: { confirmed: 0, open: 0, refuted: 0 }, findings: [] };
  const counts: Record<string, number> = {};
  for (const i of items) {
    const e = i.element;
    if (!e) continue;
    if (e.kind === "summary") {
      const at = e.updatedAt || e.createdAt || i.at || "";
      brief.summary = { title: e.title || e.body || "", at, id: i.id, stale: now - toMillis(at) > SSH_STALE_MS };
    } else if (e.kind === "plan") {
      const steps = e.steps ?? [];
      brief.plan = {
        id: i.id,
        title: e.title ?? "",
        done: steps.filter((s) => s.state === "done").length,
        total: steps.length,
        current: steps.find((s) => s.state === "active")?.text ?? "",
      };
    } else if (e.kind === "hypothesis") {
      if (e.state === "confirmed") brief.theories.confirmed++;
      else if (e.state === "refuted") brief.theories.refuted++;
      else brief.theories.open++;
    } else if (e.kind === "finding") {
      const sev = SEVERITY_ORDER.includes(e.severity ?? "") ? (e.severity as string) : "info";
      counts[sev] = (counts[sev] ?? 0) + 1;
    }
  }
  for (const severity of SEVERITY_ORDER) {
    if (counts[severity]) {
      const v = severityView(severity);
      brief.findings.push({ severity, word: v.word.toLowerCase(), cls: v.ring, count: counts[severity] });
    }
  }
  return brief;
}

/** A claim's ring. Severity may colour it; nothing may ever fill it or box it. */
export function elementRing(e: Pick<SSHElement, "kind" | "state" | "severity">): string {
  switch (e.kind) {
    case "summary":
      return "border border-ink";
    case "finding":
      return severityView(e.severity).ring;
    case "hypothesis":
      return e.state === "confirmed" ? "border border-ink" : "border border-faint";
    default:
      return "border border-faint";
  }
}

/**
 * A theory's state as a word on a ring. "Agent says" is required: a bare
 * "Confirmed" would read as lola's verdict, and a green tick is a daemon's word.
 */
export function hypothesisView(state: string | undefined): { word: string; wordCls: string; titleCls: string } {
  switch (state) {
    case "confirmed":
      return { word: "Agent says confirmed", wordCls: "text-ink", titleCls: "text-ink" };
    case "refuted":
      return { word: "Agent says refuted", wordCls: "text-faint", titleCls: "line-through decoration-edge text-faint" };
    default:
      return { word: "Open", wordCls: "text-faint", titleCls: "text-ink" };
  }
}

/** A plan step: hollow shapes only. Steps are not green. */
export function stepView(state: string | undefined): { ring: string; text: string; suffix: string } {
  switch (state) {
    case "active":
      return { ring: "border-ink", text: "text-ink", suffix: "" };
    case "done":
      return { ring: "border-faint", text: "line-through decoration-edge text-faint", suffix: "" };
    case "skipped":
      return { ring: "border-edge", text: "text-faint", suffix: " (skipped)" };
    case "failed":
      return { ring: "border-bad", text: "text-ink", suffix: "" };
    default:
      return { ring: "border-edge", text: "text-faint", suffix: "" };
  }
}

/**
 * The agent may have answered in its own pane rather than on the timeline: the
 * message has been in front of it for a minute, it has posted nothing since, and
 * its axis went idle. A hint, never a claim about what it did.
 */
export function maybeAnsweredInPane(items: SSHTimelineItem[], item: SSHTimelineItem, agentState: string | undefined, now: number = Date.now()): boolean {
  const h = item.human;
  if (!h?.delivered || h.recalled) return false;
  if (agentState !== "idle" && agentState !== "waiting_input") return false;
  const at = toMillis(h.deliveredAt);
  if (Number.isNaN(at) || now - at < 60_000) return false;
  return !items.some((i) => i.order > item.order && i.kind === "element");
}

export type AgentAxis = { agentState?: string; inputReason?: string; agent?: string };

/**
 * Whether a waiting agent is held by something in its OWN pane: a permission
 * prompt, a question or a modal. The agent's idle nudge is not that, it is just
 * waiting, and a bare `waiting_input` with no reason reads the same.
 */
export function parkedOnPrompt(reason: string | undefined): boolean {
  return !!reason && reason !== "idle_notification";
}

export type NowRow = {
  text: string;
  /** A row carrying an exact command, drawn in mono. */
  command?: string;
  tail?: string;
  cls: string;
  block?: SSHBlock;
  action?: "stop" | "show" | "revive";
  pulse?: boolean;
};

/** The Brief's one fact row. Facts only: nothing here comes from an element. */
export function nowRow(items: SSHTimelineItem[], axis: AgentAxis, now: number = Date.now()): NowRow {
  const agent = agentName(axis.agent);
  const pending = items.filter((i) => i.block?.status === "pending_approval").length;
  const running = items.filter((i) => i.block?.status === "running").map((i) => i.block as SSHBlock);
  const queued = items.filter((i) => i.block?.status === "queued").length;
  if (pending > 0) {
    return { text: `Waiting for you to approve ${plural(pending, "command")}`, cls: "text-orange" };
  }
  if (running.length > 0 && queued > 0) {
    return { text: `Running ${running.length}, ${queued} queued`, cls: "text-faint" };
  }
  if (running.length > 0) {
    const b = running[running.length - 1];
    return { text: "Running", command: b.command, tail: `· ${formatMs(blockElapsedMs(b, now))}`, cls: "text-info", block: b, action: "stop" };
  }
  switch (axis.agentState) {
    case "working":
    case "starting":
      return { text: `${agent} is working`, cls: "text-faint", pulse: true };
    case "waiting_input":
      if (!parkedOnPrompt(axis.inputReason)) return { text: `${agent} is waiting`, cls: "text-faint" };
      return { text: `${agent} is waiting on a prompt in its own terminal.`, cls: "text-orange", action: "show" };
    case "exited":
    case "dead":
      return { text: `${agent} has exited`, cls: "text-faint", action: "revive" };
    default:
      return { text: `${agent} is waiting`, cls: "text-faint" };
  }
}

// --- the composer (section 11) ----------------------------------------------

export type ComposerLine = {
  line: string;
  placeholder: string;
  cls: string;
  action?: "show" | "revive";
  pulse?: boolean;
  /** The driving bar replaces this line. */
  driving?: boolean;
};

/** Derived from facts, so the line tells the truth about what happens to what you say. */
export function composerLine(axis: AgentAxis, wheel: string | undefined): ComposerLine {
  const a = agentName(axis.agent);
  if (wheel === "human") {
    return { line: "", placeholder: `Ask ${a} to carry on…`, cls: "text-faint", driving: true };
  }
  switch (axis.agentState) {
    case "working":
      return { line: `${a} is working. It gets your message at its next command.`, placeholder: `Talk to ${a}…`, cls: "text-faint", pulse: true };
    case "starting":
      return { line: `${a} is starting.`, placeholder: `Talk to ${a}…`, cls: "text-faint", pulse: true };
    case "waiting_input":
      if (parkedOnPrompt(axis.inputReason)) {
        return {
          line: `${a} is waiting on a prompt in its own terminal. It will get this after that.`,
          placeholder: `Talk to ${a}…`,
          cls: "text-orange",
          action: "show",
        };
      }
      return { line: `${a} is waiting.`, placeholder: `Tell ${a} what to do…`, cls: "text-faint" };
    case "idle":
      return { line: `${a} is waiting.`, placeholder: `Tell ${a} what to do…`, cls: "text-faint" };
    case "exited":
    case "dead":
      return { line: `${a} has exited. Your message waits until it is back.`, placeholder: `Talk to ${a}…`, cls: "text-faint", action: "revive" };
    default:
      return { line: `${a} is waiting.`, placeholder: `Talk to ${a}…`, cls: "text-faint" };
  }
}

/** Under a message: Delivered, never Read or Seen, because lola cannot know that. */
export function deliveryLine(h: SSHHumanMessage, agent: string, gone: boolean): { text: string; canRecall: boolean } {
  if (h.recalled) return { text: "Recalled", canRecall: false };
  if (h.delivered) {
    const at = clockTime(h.deliveredAt);
    const when = at ? ` · ${at}` : "";
    if (h.via === "prompt") return { text: `Delivered at its prompt${when}`, canRecall: false };
    return { text: `Delivered with its last command${when}`, canRecall: false };
  }
  return { text: gone ? `Queued. ${agentName(agent)} has exited.` : "Queued", canRecall: true };
}

// --- the dock (section 9) ---------------------------------------------------

export type DockCard =
  | { kind: "approval"; id: string; conn: SSHConnInfo; block: SSHBlock; index: number; of: number }
  | { kind: "go"; id: string; conn: SSHConnInfo; index: number; of: number }
  | { kind: "reconnect"; id: string; conn: SSHConnInfo; running: number }
  | { kind: "failed"; id: string; conn: SSHConnInfo };

/**
 * One card at a time: authentication first, then the oldest approval, then a go.
 * A dropped or failed connection comes last, and only for the connection the
 * header is showing, so a drop never shoulders a real decision out of the way.
 */
export function dockCards(conns: SSHConnInfo[], items: SSHTimelineItem[], activeConn: string): DockCard[] {
  const out: DockCard[] = [];
  const connOf = (id: string) => conns.find((c) => c.id === id);
  const approvals = items.filter((i) => i.block?.status === "pending_approval" && connOf(i.block.conn));
  const goes = conns.filter((c) => c.state === "requested");
  const of = approvals.length + goes.length;
  let n = 0;
  for (const i of approvals) {
    const block = i.block as SSHBlock;
    out.push({ kind: "approval", id: `approval-${block.id}`, conn: connOf(block.conn) as SSHConnInfo, block, index: ++n, of });
  }
  for (const c of goes) out.push({ kind: "go", id: `go-${c.id}`, conn: c, index: ++n, of });
  const active = connOf(activeConn);
  if (active?.state === "disconnected") {
    const running = items.filter((i) => i.block?.conn === active.id && i.block?.status === "lost").length;
    out.push({ kind: "reconnect", id: `reconnect-${active.id}`, conn: active, running });
  }
  if (active?.state === "failed") out.push({ kind: "failed", id: `failed-${active.id}`, conn: active });
  return out;
}

/** The grant durations offered, up to the host's limit. */
export function grantOptions(max: number | undefined): number[] {
  const m = max ?? 0;
  if (m <= 0) return [];
  return [5, 15, 30, 60, 120].filter((v) => v <= m);
}

/**
 * The grant control exists only below production. The wire's GrantMaxMinutes is
 * not trusted on its own: production never renders it, whatever that number says.
 */
export function canGrant(c: Pick<SSHConnInfo, "tier" | "grantMaxMinutes">): boolean {
  return tierOf(c.tier) !== "production" && grantOptions(c.grantMaxMinutes).length > 0;
}

export function tierAskLine(c: Pick<SSHConnInfo, "tier" | "grantMaxMinutes">): string {
  if (tierOf(c.tier) === "production") return "Production asks for every command.";
  return canGrant(c) ? "" : "This host asks for every command.";
}

export function goCommandLine(c: Pick<SSHConnInfo, "tier" | "grantMaxMinutes">): string {
  return canGrant(c) ? "Each command will ask, unless you open a grant." : "Each command will still ask for approval.";
}

/** Past this a command scrolls, and a note says so. */
export const COMMAND_SCROLL_LINES = 12;

export function commandLineCount(cmd: string): number {
  return cmd.split("\n").length;
}

/** The "Connects" row. A missing resolve is said out loud rather than left blank. */
export function connectsLine(c: Pick<SSHConnInfo, "alias" | "user" | "resolvedUser" | "resolvedHostname">): { text: string; known: boolean } {
  if (c.resolvedHostname) {
    const who = c.resolvedUser ? `${c.resolvedUser}@${c.resolvedHostname}` : c.resolvedHostname;
    return { text: `${who} (alias ${c.alias})`, known: true };
  }
  if (c.user) return { text: `${c.user}@${c.alias} (alias ${c.alias})`, known: true };
  return { text: "Lola could not read this from your ssh config.", known: false };
}

// --- the new-session flow (section 13) --------------------------------------

export type HostRow = {
  host: SSHHostInfo;
  name: string;
  who: string;
  refused: boolean;
  /** The right column. */
  note: string;
  noteCls: string;
  openIn?: { id: string; title: string };
};

type OpenSession = { id: string; title?: string; location?: string; ssh?: SSHSessionInfo | null };

/** Host rows in config order, reading the daemon's own Direct* answers. */
export function hostRows(hosts: SSHHostInfo[] | null | undefined, sessions: OpenSession[]): HostRow[] {
  return (hosts ?? []).map((host) => {
    const open = sessions.find(
      (s) => s.location === "ssh" && (s.ssh?.connections ?? []).some((c) => c.host === host.name && !isEnded(c)),
    );
    const refused = !!host.directRefused;
    const here = !refused && host.directSandbox !== "docker";
    const note = refused
      ? "Cannot start"
      : open
        ? `Open in ${open.title || "a session"} · ${here ? "on this Mac" : "in a container"}`
        : here
          ? "Agent runs on this Mac"
          : "Agent runs in a container";
    return {
      host,
      name: host.label || host.name,
      who: host.user ? `${host.user}@${host.alias}` : host.alias,
      refused,
      note,
      noteCls: refused ? "text-faint" : here && tierOf(host.tier) === "production" ? "text-warn" : "text-faint",
      openIn: open ? { id: open.id, title: open.title || "a session" } : undefined,
    };
  });
}

export type ConnectRow = {
  host: SSHHostInfo;
  name: string;
  who: string;
  /** Why this session may not connect, "" when it may. Mirrors SSHAdmit; the daemon decides. */
  refused: string;
  /** This session's live connection to the host, if it has one. */
  live?: SSHConnInfo;
  note: string;
};

/** The hosts an existing session could connect to, as that session sees them. */
export function connectRows(hosts: SSHHostInfo[] | null | undefined, session: { sandbox?: string; ssh?: SSHSessionInfo | null } | undefined): ConnectRow[] {
  return (hosts ?? []).map((host) => {
    const live = (session?.ssh?.connections ?? []).find((c) => c.host === host.name && !isEnded(c) && c.state !== "requested");
    const refused =
      tierOf(host.tier) === "production" && !host.allowUnsandboxed && !session?.sandbox
        ? "Production needs a sandboxed session. Use a project with a sandbox, or set allow_unsandboxed on this host."
        : "";
    return {
      host,
      name: host.label || host.name,
      who: host.user ? `${host.user}@${host.alias}` : host.alias,
      refused,
      live,
      note: refused ? "Cannot connect" : live ? "Uses the open login" : "",
    };
  });
}

/** The list row's key for a session with no issue. A direct session's is its host. */
export function sessionKey(s: { issue?: string; id: string; location?: string; ssh?: SSHSessionInfo | null }): string {
  if (s.issue) return s.issue;
  if (s.location !== "ssh") return s.id.slice(0, 8);
  const c = s.ssh?.connections?.[0];
  return c ? hostName(c) : "";
}

/** Up to one usable host selected: the one asked for when it is usable, else the first. */
export function initialHost(rows: HostRow[], wanted = ""): string {
  const asked = rows.find((r) => r.host.name === wanted && !r.refused);
  return (asked ?? rows.find((r) => !r.refused))?.host.name ?? "";
}

/** Does the session with these sessions wait on a human anywhere? The oldest wins. */
export function nextApprovalTarget(sessions: { id: string; ssh?: SSHSessionInfo | null }[]): { session: string; conn?: string; block?: string } | undefined {
  let best: { at: number; session: string; conn?: string; block?: string } | undefined;
  for (const s of sessions) {
    const b = s.ssh?.nextApproval;
    if (b) {
      const at = toMillis(b.createdAt);
      const when = Number.isNaN(at) ? Number.MAX_SAFE_INTEGER : at;
      if (!best || when < best.at) best = { at: when, session: s.id, conn: b.conn, block: b.id };
    }
  }
  if (best) return best;
  for (const s of sessions) {
    const c = (s.ssh?.connections ?? []).find((x) => x.state === "authenticating" || x.state === "requested");
    if (c) return { session: s.id, conn: c.id };
  }
  return undefined;
}

// --- the agent's prose -------------------------------------------------------

export type CalloutTone = "ok" | "note" | "warn" | "bad";

/** One row of a conversation's label column. Only answerblocks.ts makes these. */
export type LedgerRow = {
  label: string;
  text: string;
  tone: "" | "warn" | "bad";
  /** The label is too long for the column, so it sits above its text. */
  stacked: boolean;
  status?: "ok" | "warn" | "bad";
  subs?: string[];
};

export type RichBlock =
  | { kind: "lead"; text: string }
  | { kind: "p"; text: string }
  | { kind: "heading"; text: string }
  | { kind: "ul" | "ol"; items: string[]; /** An item's indented children, by the item's index. */ subs?: Record<number, string[]> }
  | { kind: "code"; text: string; /** What followed the opening fence. */ lang?: string }
  | { kind: "callout"; tone: CalloutTone; text: string; /** The word it opened with, in a conversation. */ word?: string }
  | { kind: "ledger"; rows: LedgerRow[] }
  | { kind: "question"; text: string }
  | { kind: "table"; head: string[]; rows: string[][] }
  | { kind: "fold"; title: string; blocks: RichBlock[] };

const TONES: Record<string, CalloutTone> = { OK: "ok", NOTE: "note", WARN: "warn", WARNING: "warn", BAD: "bad", ERROR: "bad" };

const cells = (line: string) =>
  line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => c.trim());
const isTableRule = (line: string) => /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(line);

/**
 * The small Markdown agents are taught to write (lola ssh guide answers), as data. It
 * builds no HTML, so a line of it can only ever render as text. Anything it does not
 * recognize stays a paragraph, so a formatter can never eat an answer. A first line
 * followed by more becomes the headline.
 */
export function richBlocks(text: string): RichBlock[] {
  const blob = splitBlob(text);
  if (blob) return blob;
  const out: RichBlock[] = parseRich(text.split("\n"));
  if (out.length > 1 && out[0].kind === "p" && !out[0].text.includes("\n")) out[0] = { kind: "lead", text: out[0].text };
  else if (out.length === 1 && out[0].kind === "p" && out[0].text.includes("\n")) {
    const [first, ...rest] = out[0].text.split("\n");
    return [{ kind: "lead", text: first }, { kind: "p", text: rest.join("\n") }];
  }
  return out;
}

const BLOB_RUNES = 200;

/**
 * An answer that arrived as one long paragraph, from before the daemon refused them:
 * first sentence as the headline, the rest as a list, and a leading "Disk:" as a label.
 * Every word stays, in order, so a poor split costs looks and never content.
 */
function splitBlob(text: string): RichBlock[] | undefined {
  const t = text.trim();
  if (t.length <= BLOB_RUNES || t.includes("\n")) return undefined;
  const parts = t
    .replace(/(?<!\b(?:e\.g|i\.e|vs|etc|approx|cf))([.!?])\s+/g, "$1\n")
    .split("\n")
    .map((p) => p.trim())
    .filter(Boolean);
  if (parts.length < 2) return undefined;
  const items = parts.slice(1).map((p) => p.replace(/^([A-Z][\w /-]{1,24}):\s+/, "**$1:** "));
  return [
    { kind: "lead", text: parts[0] },
    { kind: "ul", items },
  ];
}

export function parseRich(lines: string[]): RichBlock[] {
  const out: RichBlock[] = [];
  let para: string[] = [];
  const flush = () => {
    if (para.length) out.push({ kind: "p", text: para.join("\n") });
    para = [];
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = /^\s*```\s*(.*)$/.exec(line);
    if (fence) {
      flush();
      const code: string[] = [];
      for (i++; i < lines.length && !/^\s*```\s*$/.test(lines[i]); i++) code.push(lines[i]);
      out.push(fence[1] ? { kind: "code", text: code.join("\n"), lang: fence[1] } : { kind: "code", text: code.join("\n") });
      continue;
    }
    const fold = /^\s*\+\+\+\s*(.*)$/.exec(line);
    if (fold) {
      flush();
      const inner: string[] = [];
      for (i++; i < lines.length && !/^\s*\+\+\+\s*$/.test(lines[i]); i++) inner.push(lines[i]);
      out.push({ kind: "fold", title: fold[1].trim() || "More", blocks: parseRich(inner) });
      continue;
    }
    if (line.includes("|") && i + 1 < lines.length && isTableRule(lines[i + 1])) {
      flush();
      const head = cells(line);
      const rows: string[][] = [];
      for (i += 2; i < lines.length && lines[i].includes("|"); i++) rows.push(cells(lines[i]));
      i--;
      out.push({ kind: "table", head, rows });
      continue;
    }
    const quote = /^\s*>\s?(.*)$/.exec(line);
    if (quote) {
      flush();
      const tag = /^\[!(\w+)\]\s*(.*)$/.exec(quote[1]);
      const tone = (tag && TONES[tag[1].toUpperCase()]) || "note";
      const text = [tag && TONES[tag[1].toUpperCase()] ? tag[2] : quote[1]];
      while (i + 1 < lines.length && /^\s*>\s?/.test(lines[i + 1]) && !/^\s*>\s?\[!/.test(lines[i + 1])) {
        i++;
        text.push(lines[i].replace(/^\s*>\s?/, ""));
      }
      out.push({ kind: "callout", tone, text: text.join("\n").trim() });
      continue;
    }
    const heading = /^#{1,6}\s+(.+)$/.exec(line);
    const bullet = /^\s*[-*•]\s+(.+)$/.exec(line);
    const numbered = /^\s*\d+[.)]\s+(.+)$/.exec(line);
    if (heading) {
      flush();
      out.push({ kind: "heading", text: heading[1] });
    } else if (bullet || numbered) {
      // An indented item belongs to the item above it, whichever kind of list that is, or a nested
      // bullet would end a numbered list and restart its count.
      const hadPara = para.length > 0;
      flush();
      const kind = bullet ? "ul" : "ol";
      const last = out[out.length - 1];
      const item = (bullet ?? numbered)![1];
      if (!hadPara && /^\s{2,}/.test(line) && (last?.kind === "ul" || last?.kind === "ol")) {
        const at = last.items.length - 1;
        last.subs = { ...last.subs, [at]: [...(last.subs?.[at] ?? []), item] };
      } else if (last?.kind === kind) last.items.push(item);
      else out.push({ kind, items: [item] });
    } else if (!line.trim()) {
      flush();
    } else {
      para.push(line);
    }
  }
  flush();
  return out;
}

/** A list item's status mark: "[ok] docker active". */
export function itemStatus(item: string): { tone?: "ok" | "warn" | "bad"; text: string } {
  const m = /^\[(ok|warn|bad)\]\s*(.*)$/i.exec(item);
  return m ? { tone: m[1].toLowerCase() as "ok" | "warn" | "bad", text: m[2] } : { text: item };
}

export type InlineSpan =
  | { kind: "text" | "code" | "pr"; text: string }
  | { kind: "bold" | "em"; text: string; /** Present only when the text holds markup of its own. */ spans?: InlineSpan[] }
  | { kind: "link"; text: string; href: string };

// Each alternative of the bold's body starts on a different character, so a line with an unclosed
// `**` and many code spans cannot backtrack its way into a freeze.
const INLINE = new RegExp(
  [
    /`([^`\n]+)`/.source,
    /\*\*((?:`[^`\n]+`|`(?![^`\n]*`)|[^*`\n]|\*(?!\*))+?)\*\*/.source,
    /(?<![*\w])\*(?![\s*])([^*`\n]+?)(?<![\s*])\*(?![*\w])/.source,
    /\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/.source,
    /(?<![\w/#])(?<![Ii]mage )(?<!Bild )#\d{1,6}(?![\w-])/.source,
  ].join("|"),
  "g",
);

function nested(kind: "bold" | "em", text: string): InlineSpan {
  const spans = inlineSpans(text);
  return spans.length === 1 && spans[0].kind === "text" ? { kind, text } : { kind, text, spans };
}

/**
 * `code`, **bold**, *em*, [links](http...) and #123 inside a line. Bold and em recurse, so code inside
 * bold is code. Anything unmatched stays literal text, and a link that is not http(s) is not a link.
 */
export function inlineSpans(text: string): InlineSpan[] {
  const out: InlineSpan[] = [];
  const re = new RegExp(INLINE);
  let at = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m.index > at) out.push({ kind: "text", text: text.slice(at, m.index) });
    if (m[1] !== undefined) out.push({ kind: "code", text: m[1] });
    else if (m[2] !== undefined) out.push(nested("bold", m[2]));
    else if (m[3] !== undefined) out.push(nested("em", m[3]));
    else if (m[4] !== undefined) out.push({ kind: "link", text: m[4], href: m[5] });
    else out.push({ kind: "pr", text: m[0] });
    at = m.index + m[0].length;
  }
  if (at < text.length) out.push({ kind: "text", text: text.slice(at) });
  return out;
}

/** A narration that is an answer rather than a status line reads at full strength. */
export function isAnswer(text: string | undefined): boolean {
  return !!text && (text.includes("\n") || text.length > 80);
}

// --- the composer's two destinations ------------------------------------------------

/** Exec channels have no TTY, so these would hang or refuse. They need the terminal. */
const NEEDS_TTY = /^\s*(sudo\s+(-i|-s)(\s|$)|(sudo\s+)?(vim?|nvim|nano|emacs|top|htop|btop|less|more|man|tmux|screen|ssh|su|passwd|mysql|psql|redis-cli|mongosh|bash|sh|zsh|fish|python3?|node|irb|php\s+-a|docker\s+(exec|run)\s+(-\w*\s+)*-\w*[it]\w*)(\s|$))/;

export function needsTerminal(command: string): boolean {
  if (/^\s*(python3?|node|bash|sh|zsh|fish|mysql|psql)\s+\S/.test(command) && !/^\s*(mysql|psql)\s/.test(command)) return false;
  return NEEDS_TTY.test(command);
}

/** Where a Run command will go, in a sentence. The daemon makes the same choice. */
export function runRoute(c: SSHConnInfo | undefined, agent: string): { inShell: boolean; note: string } {
  const only = `Only you can see it until you share it with ${agent}.`;
  if (c?.termElevated) return { inShell: false, note: `Your shell is inside ${c.termElevated}, so this runs separately, without those rights.` };
  if (c?.termTracked && c.termAtPrompt) return { inShell: true, note: `Runs in your shell, so exports and sudo stick. ${only}` };
  if (c?.termTracked) return { inShell: false, note: `Your shell is busy, so this runs separately. ${only}` };
  return { inShell: false, note: `Runs separately, so exports do not stick. ${only}` };
}

/** The prompt line above the run field. */
export function runPrompt(c: SSHConnInfo | undefined): string {
  if (!c) return "";
  return `${identity(c)}:${c.cwd || "~"}`;
}

// --- widgets -----------------------------------------------------------------

export const WIDGET_SANDBOX = "allow-scripts";

/**
 * Nothing leaves the frame and nothing comes in, by policy as well as by sandbox. Scripts
 * and styles may only be inline, and data can only arrive as a message from the parent.
 */
export const WIDGET_CSP =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; form-action 'none'; base-uri 'none'";

const THEME_TOKENS = ["canvas:bg", "panel:panel", "edge:edge", "ink:ink", "faint:faint", "accent:accent", "good:good", "bad:bad", "warn:warn", "info:info", "orange:orange", "magenta:magenta"];

/** A page that has said nothing by now gets reported. Generous, because timers lag under load. */
export const WIDGET_READY_MS = 5000;
/** How much of a page's own error text leaves it. */
export const WIDGET_ERROR_CAP = 300;
/**
 * A page that has not spoken by this long after its first load is not the page we wrote. Only a
 * moment, as a posted message can land just after the load event.
 */
export const WIDGET_BOOT_GRACE_MS = 400;
/**
 * The first document answers a ping over its channel, and no other document can. A frame shares
 * the app's thread, so a busy page holds up the ping too and cannot read as gone.
 */
export const WIDGET_PING_MS = 1000;
export const WIDGET_PING_MISSES = 3;

/**
 * The script runs before the agent's code, so a throw here kills every widget, silently, and
 * only in the app: WKWebView's window.webkit is read-only and cannot be replaced. Measured in
 * a real WKWebView, the hide below therefore does nothing there and is only kept for engines
 * where it can work. It stays in its own script and its own try, so nothing can stop the SDK.
 */
const WIDGET_HIDE = `try{Object.defineProperty(window,"webkit",{value:undefined,configurable:false})}catch(e){}`;

/**
 * lola, inside the frame. Joined without line breaks on purpose: with nothing but the first
 * line ahead of the agent's page, a line number in an error is a line number in their file.
 */
function widgetSdk(seed: string, reducedMotion: boolean): string {
  return [
    `(function(){`,
    // The channel comes first, before any of the agent's code can run. It is the only way in
    // or out from here on, because a port belongs to this document: a frame that navigates has
    // a new document with no port, however it got there. parent.postMessage has no way to name
    // an opaque document, so sending on it would hand the data to whatever is in the frame now.
    `var ch=new MessageChannel(),port=ch.port1;parent.postMessage({lola:"boot"},"*",[ch.port2]);`,
    // Errors come next, so that everything after them is covered.
    `var left=5;var post=function(m){port.postMessage(m)};`,
    `var str=function(x){try{return String(x)}catch(e){return "unprintable"}};`,
    `var fail=function(kind,message,line){if(left<1)return;left--;post({lola:"error",kind:kind,message:str(message).slice(0,${WIDGET_ERROR_CAP}),line:Number(line)||0})};`,
    `var lineOf=function(e){if(!e)return 0;if(e.line)return e.line;var m=/:(\\d+):\\d+/.exec(str(e.stack));return m?Number(m[1]):0};`,
    `addEventListener("error",function(e){var t=e.target;if(t&&t!==window){fail("blocked","Could not load <"+str(t.tagName).toLowerCase()+"> "+str(t.src||t.href||""));return}fail("script",e.message||"Script error",e.lineno)},true);`,
    `addEventListener("unhandledrejection",function(e){fail("script","Unhandled rejection: "+str(e.reason),lineOf(e.reason))});`,
    `addEventListener("securitypolicyviolation",function(e){fail("blocked",e.violatedDirective+" blocked "+(e.blockedURI||"inline code"),e.lineNumber)});`,
    // One bad listener must not stop the rest of the page, so each call is caught and reported.
    // A tab in the background stays alive, but its animation frames wait until it is on show.
    `var shown=true,seers=[],held=[],raf=window.requestAnimationFrame,caf=window.cancelAnimationFrame;`,
    `window.requestAnimationFrame=function(f){if(shown)return raf.call(window,f);held.push(f);return -held.length};`,
    `window.cancelAnimationFrame=function(i){if(i<0)held[-i-1]=null;else caf.call(window,i)};`,
    `var listeners=[];var call=function(f){try{f(lola.data)}catch(e){fail("script",str(e),lineOf(e))}};`,
    // lola.el: text is always a text node, so a value from the data can never become markup.
    `var NS={svg:1,g:1,path:1,circle:1,rect:1,line:1,polyline:1,polygon:1,text:1};`,
    `var add=function(n,c){if(c==null||c===false||c===true)return;if(Array.isArray(c)){c.forEach(function(x){add(n,x)});return}n.append(c instanceof Node?c:String(c))};`,
    `var isChild=function(p){return typeof p!=="object"||p===null||Array.isArray(p)||p instanceof Node};`,
    `var el=function(tag,props){var n=NS[tag]?document.createElementNS("http://www.w3.org/2000/svg",tag):document.createElement(tag);var from=2;`,
    `if(props!=null&&isChild(props)){from=1}else{for(var k in props||{}){var v=props[k];if(v==null||v===false)continue;`,
    `if(typeof v==="function"&&k.slice(0,2)==="on")n.addEventListener(k.slice(2),v);`,
    `else if(k==="style"&&typeof v==="object"){for(var s in v)n.style.setProperty(s,v[s])}`,
    `else n.setAttribute(k==="className"?"class":k,v===true?"":v)}}`,
    `for(var i=from;i<arguments.length;i++)add(n,arguments[i]);return n};`,
    `window.lola={data:${seed},reducedMotion:${reducedMotion},`,
    `onData:function(f){listeners.push(f);call(f)},`,
    `ask:function(t){post({lola:"ask",text:String(t)})},`,
    `run:function(t){post({lola:"run",text:String(t)})},`,
    // Kept so an older page does not throw. A widget now fills its panel.
    `resize:function(){},`,
    `get visible(){return shown},`,
    `onVisible:function(f){seers.push(f)},`,
    `el:el,`,
    // The whole of a simple widget: draw from the data, and say something when there is none.
    `view:function(fn,o){o=o||{};var root=el("div",{"class":"lola-view"});document.body.append(root);lola.onData(function(d){root.replaceChildren();add(root,d==null?el("div",{"class":"lola-empty"},o.empty||"No data yet"):fn(d))})},`,
    `rows:function(r){var a=Array.isArray(r)?r:Object.keys(r||{}).map(function(k){return[k,r[k]]});return el("div",{"class":"lola-rows"},a.map(function(x){return el("div",{"class":"lola-row"},el("span",null,x[0]),el("b",{style:x[2]?{color:"var(--"+x[2]+")"}:null},x[1]))}))},`,
    `meter:function(v,o){o=o||{};var max=o.max||100,n=Number(v)||0,p=Math.max(0,Math.min(100,n/max*100));`,
    `var c=o.bad!=null&&n>=o.bad?"var(--bad)":o.warn!=null&&n>=o.warn?"var(--warn)":o.color||"var(--accent)";`,
    `return el("div",{"class":"lola-meter"},o.label!=null&&el("div",{"class":"lola-row"},el("span",null,o.label),el("b",null,o.detail!=null?o.detail:Math.round(p)+"%")),`,
    `el("div",{"class":"lola-track",role:"meter","aria-valuenow":n,"aria-valuemin":0,"aria-valuemax":max,"aria-label":o.label||"Meter"},el("div",{"class":"lola-fill",style:{width:p+"%",background:c}})))},`,
    `spark:function(values,o){o=o||{};var a=(values||[]).map(Number).filter(function(x){return isFinite(x)}),w=o.width||160,h=o.height||28,c=o.color||"var(--accent)";`,
    `var lo=Math.min.apply(null,a),hi=Math.max.apply(null,a);var y=function(v){return hi===lo?h/2:h-2-(v-lo)/(hi-lo)*(h-4)};`,
    `var pts=a.map(function(v,i){return(i/Math.max(1,a.length-1)*(w-4)+2)+","+y(v)}).join(" ");`,
    `return el("svg",{"class":"lola-spark",viewBox:"0 0 "+w+" "+h,width:w,height:h,role:"img","aria-label":o.label||(a.length?"Latest value "+a[a.length-1]:"No values")},`,
    `a.length>1?el("polyline",{points:pts,fill:"none",stroke:c,"stroke-width":1.5,"stroke-linejoin":"round"}):a.length===1?el("circle",{cx:w/2,cy:y(a[0]),r:2,fill:c}):null)}};`,
    `if(lola.reducedMotion)document.documentElement.setAttribute("data-still","");`,
    `var show=function(v){if(v===shown)return;shown=v;document.documentElement.toggleAttribute("data-background",!v);if(v){var h=held;held=[];h.forEach(function(f){if(f)raf.call(window,f)})}seers.forEach(function(f){try{f(v)}catch(e){fail("script",str(e),lineOf(e))}})};`,
    `var theme=function(m){var r=document.documentElement;for(var k in m.vars)r.style.setProperty("--"+k,m.vars[k]);r.style.colorScheme=m.scheme==="light"?"light":"dark"};`,
    `port.onmessage=function(e){var m=e.data;if(!m)return;if(m.lola==="ping"){post({lola:"pong"});return}if(m.lola==="visible"){show(!!m.visible);return}if(m.lola==="theme"){theme(m);return}if(m.lola!=="data")return;lola.data=m.data;listeners.forEach(call)};`,
    // Ready means the page ran to its end. A page with nothing to see is a failure too, but
    // only a moment later, as a script may still be drawing.
    `addEventListener("load",function(){post({lola:"ready"});setTimeout(function(){var b=document.body;if(b&&b.getBoundingClientRect().height<1&&!(b.innerText||"").trim())fail("empty","The page drew nothing")},1500)});`,
    `})();`,
  ].join("");
}

const WIDGET_CSS =
  `html,body{margin:0;padding:0;background:var(--panel);color:var(--ink);font:13px/1.45 -apple-system,BlinkMacSystemFont,"Inter",sans-serif}` +
  // Zero specificity, so a page's own button rule always wins. The hover changes only the
  // background and stays near the resting one, because a page that sets its own hover color
  // and nothing else must not end up with text the color of the background.
  `:where(button){font:inherit;color:var(--ink);background:var(--edge);border:0;border-radius:5px;padding:4px 10px;cursor:pointer}:where(button:hover){background:color-mix(in srgb,var(--edge) 75%,var(--ink))}` +
  `[data-still] *,[data-still] *::before,[data-still] *::after{animation:none!important;transition:none!important}` +
  `[data-background] *,[data-background] *::before,[data-background] *::after{animation-play-state:paused!important}` +
  `.lola-empty{color:var(--faint)}.lola-row{display:flex;justify-content:space-between;gap:12px}.lola-row>span{color:var(--faint)}.lola-row>b{font-weight:500;font-variant-numeric:tabular-nums}` +
  `.lola-meter{margin:4px 0}.lola-track{height:6px;border-radius:3px;background:var(--edge);overflow:hidden}.lola-fill{height:100%;border-radius:3px;transition:width .3s}.lola-spark{display:block;max-width:100%}`;

/** The theme as a page sees it, by CSS variable name without the dashes. */
export function widgetThemeVars(read: (token: string) => string): Record<string, string> {
  return Object.fromEntries(
    THEME_TOKENS.map((t) => {
      const [token, name] = t.split(":");
      return [name, read(`--color-${token}`).replace(/[;<>{}\s]+/g, " ").trim()];
    }),
  );
}

/** The widget's page. It runs in an opaque origin, so this is its whole world. */
export function widgetDoc(html: string, data: string | undefined, read: (token: string) => string, reducedMotion: boolean, colorScheme = "dark"): string {
  // The frame must match the app's scheme: a mismatch makes the browser paint it opaque white.
  const scheme = colorScheme === "light" ? "light" : "dark";
  const vars = Object.entries(widgetThemeVars(read))
    .map(([name, value]) => `--${name}:${value}`)
    .join(";");
  const seed = JSON.stringify(data ? safeParse(data) : null).replace(/</g, "\\u003c");
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${WIDGET_CSP}"><style>:root{${vars};color-scheme:${scheme}}${WIDGET_CSS}</style><script>${WIDGET_HIDE}<\/script><script>${widgetSdk(seed, reducedMotion)}<\/script></head><body>${html}</body></html>`;
}

/**
 * The frame's address. A data: URL rather than srcdoc, and the reason is the error reports:
 * WebKit reduces an uncaught error in a srcdoc frame with an opaque origin to "Script error."
 * with no line, which tells an agent nothing, and it keeps the details for a data: document.
 * Measured in a real WKWebView. The origin is opaque either way, and the app has no CSP that
 * a srcdoc frame would have inherited.
 */
export function widgetSrc(doc: string): string {
  return "data:text/html;charset=utf-8," + encodeURIComponent(doc);
}

/** What can go wrong with a widget. The human's copy is lola's own, never the page's text. */
export const WIDGET_FAULTS = {
  script: "Widget error",
  blocked: "Widget blocked",
  stuck: "Widget did not finish loading",
  empty: "Widget drew nothing",
  navigated: "Widget tried to open another page",
} as const;
export type WidgetFaultKind = keyof typeof WIDGET_FAULTS;
export interface WidgetFault {
  kind: WidgetFaultKind;
  message: string;
  line: number;
}

/**
 * One error message from a frame, checked. Agent code can post anything here, so this goes to
 * the framed line for the agent and nowhere else: the panel shows the kind, never the text.
 */
export function widgetFault(m: { kind?: unknown; message?: unknown; line?: unknown }): WidgetFault | null {
  const kind = m.kind;
  if (typeof kind !== "string" || !Object.hasOwn(WIDGET_FAULTS, kind)) return null;
  const line = Number(m.line);
  return { kind: kind as WidgetFaultKind, message: widgetText(m.message).replace(/\s+/g, " ").slice(0, WIDGET_ERROR_CAP), line: Number.isInteger(line) && line > 0 ? line : 0 };
}

function safeParse(data: string): unknown {
  try {
    return JSON.parse(data);
  } catch {
    return null;
  }
}

/** Text a widget asks to put in the composer: one bounded, displayable string. */
export function widgetText(text: unknown): string {
  if (typeof text !== "string") return "";
  // eslint-disable-next-line no-control-regex
  return text.replace(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f​-‏‪-‮⁦-⁩]/g, "").slice(0, 4096).trim();
}
