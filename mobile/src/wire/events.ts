// The client half of server push: topics and the per-topic stream state
// machine of docs/mobile/02-events.md §6.2. Pure: no DOM, no timers, no I/O.
//
// The caller owns the watch id (the id of the `watch` request) and the state
// per topic; every function here maps (state, input) to (state, actions).

import { MAX_TOPIC_BYTES, MAX_WATCH_ID_BYTES } from "./limits";
import type { Frame } from "./protocol";

export type TopicKind = "sessions" | "projects" | "status" | "talk" | "feed" | "timeline" | "diff";

/** A canonical topic: a global name or `<kind>:<session>`. */
export type Topic =
  | "sessions"
  | "projects"
  | "status"
  | `talk:${string}`
  | `feed:${string}`
  | `timeline:${string}`
  | `diff:${string}`;

const SESSION_KINDS = new Set(["talk", "feed", "timeline", "diff"]);
const GLOBAL_KINDS = new Set(["sessions", "projects", "status"]);
const MAX_TOPIC_SESSION = 128;

/**
 * Mirrors `protocol.ParseTopic`: null for anything outside the grammar. It
 * never normalises, so a topic that parses is already canonical.
 */
export function parseTopic(t: string): { kind: TopicKind; session: string } | null {
  if (t.length === 0 || utf8Length(t) > MAX_TOPIC_BYTES) return null;
  if (GLOBAL_KINDS.has(t)) return { kind: t as TopicKind, session: "" };
  const i = t.indexOf(":");
  if (i < 0) return null;
  const kind = t.slice(0, i);
  const session = t.slice(i + 1);
  if (!SESSION_KINDS.has(kind) || !validTopicSession(session)) return null;
  return { kind: kind as TopicKind, session };
}

/** Mirrors `validTopicSession`: no dot-leading names, separators, lane ids or control characters. */
function validTopicSession(id: string): boolean {
  if (id === "" || utf8Length(id) > MAX_TOPIC_SESSION) return false;
  if (id.startsWith(".")) return false;
  for (const ch of id) {
    const c = ch.codePointAt(0)!;
    if (c < 0x20 || c === 0x7f || ch === "/" || ch === "\\" || ch === "~") return false;
  }
  return true;
}

function utf8Length(s: string): number {
  return new TextEncoder().encode(s).length;
}

/** `^[A-Za-z0-9_-]{1,64}$`: what a `watch` request's frame id must match. */
export function isWatchId(id: string): boolean {
  return id.length >= 1 && id.length <= MAX_WATCH_ID_BYTES && /^[A-Za-z0-9_-]+$/.test(id);
}

// ---------------------------------------------------------------------------
// Watch outcomes
// ---------------------------------------------------------------------------

/**
 * Why a topic is not streaming. The first four are `WatchResult` codes;
 * `bad_request` is a refused `watch` request as a whole (or an unknown code);
 * the last three end a live stream with an `err` frame.
 */
export type WatchCode =
  | "invalid_topic"
  | "unknown_session"
  | "limit"
  | "unavailable"
  | "bad_request"
  | "frame_too_large"
  | "rate_limited"
  | "denied";

const WATCH_CODES: ReadonlySet<string> = new Set<WatchCode>([
  "invalid_topic",
  "unknown_session",
  "limit",
  "unavailable",
  "bad_request",
  "frame_too_large",
  "rate_limited",
  "denied",
]);

/** A closed list: an unknown code is a failed request, never interpreted. */
export function toWatchCode(code: unknown): WatchCode {
  return typeof code === "string" && WATCH_CODES.has(code) ? (code as WatchCode) : "bad_request";
}

/** When a failed topic may be watched again (02 §6.2). */
export type WatchRetry = "never" | "after_unwatch" | "after_delay" | "next_epoch";

export function retryOf(code: WatchCode): WatchRetry {
  switch (code) {
    case "unknown_session":
    case "frame_too_large":
    case "denied":
      return "never";
    case "limit":
      return "after_unwatch";
    case "rate_limited":
      return "after_delay";
    default:
      return "next_epoch";
  }
}

// ---------------------------------------------------------------------------
// The state machine
// ---------------------------------------------------------------------------

export type TopicState =
  | { s: "idle" }
  | { s: "awaiting"; watchId: string }
  /** `pending`: the parts of an open `more` reset, in arrival order. */
  | { s: "live"; watchId: string; seq: number; pending?: Record<string, unknown>[] }
  | { s: "failed"; code: WatchCode };

export type StreamAction =
  /** One complete event; a `more` reset is assembled first and delivered with `reset: true`. */
  | { a: "deliver"; payload: unknown; reset: boolean }
  /** A gap or a wrong first event: send `watch` for this topic again. */
  | { a: "rewatch" }
  | { a: "fail"; code: WatchCode }
  | { a: "drop" };

export type Transition = [TopicState, StreamAction[]];

export const IDLE: TopicState = { s: "idle" };

/** The state after a `watch` carrying `watchId` was sent for this topic. */
export function startWatch(watchId: string): TopicState {
  return { s: "awaiting", watchId };
}

export interface EventFrame extends Frame {
  type: "event";
  id: string;
  topic: string;
  seq: number;
}

export interface ErrFrame extends Frame {
  type: "err";
}

/**
 * The outcome of the `watch` request `watchId` for this topic. A refusal
 * fails the topic; success changes nothing (the stream is live on its first
 * event, which may already have arrived). A result for any other watch id is
 * stale and ignored.
 */
export function onWatchResult(
  st: TopicState,
  watchId: string,
  r: { ok: boolean; code?: string },
): Transition {
  if (!holds(st, watchId)) return [st, []];
  if (r.ok) return [st, []];
  const code = toWatchCode(r.code);
  return [{ s: "failed", code }, [{ a: "fail", code }]];
}

/** An `err` frame whose id is the topic's watch id ends the stream. */
export function onStreamErr(st: TopicState, f: ErrFrame): Transition {
  if (!f.id || !holds(st, f.id)) return [st, [{ a: "drop" }]];
  const code = toWatchCode((f.payload as { code?: unknown } | undefined)?.code);
  return [{ s: "failed", code }, [{ a: "fail", code }]];
}

/** Every desired topic goes back to idle when the connection goes away. */
export function onDisconnect(): TopicState {
  return IDLE;
}

export function onEvent(st: TopicState, topic: Topic, f: EventFrame): Transition {
  if (st.s !== "awaiting" && st.s !== "live") return [st, [{ a: "drop" }]];
  if (f.id !== st.watchId) return [st, [{ a: "drop" }]];

  const kind = parseTopic(topic)?.kind;
  const payload = (f.payload ?? {}) as Record<string, unknown>;
  const delta = kind === "sessions" || kind === "feed" || kind === "timeline";

  if (st.s === "awaiting") {
    if (f.seq !== 1 || (delta && payload.reset !== true)) return [IDLE, [{ a: "rewatch" }]];
    return accept({ s: "live", watchId: st.watchId, seq: 1 }, payload, kind);
  }

  if (f.seq !== st.seq + 1) return [IDLE, [{ a: "rewatch" }]];
  if (st.pending && payload.reset === true) return [IDLE, [{ a: "rewatch" }]];
  return accept({ ...st, seq: f.seq }, payload, kind);
}

function holds(st: TopicState, watchId: string): boolean {
  return (st.s === "awaiting" || st.s === "live") && st.watchId === watchId;
}

/** Applies one in-sequence event: assemble a split reset, else deliver as is. */
function accept(
  st: Extract<TopicState, { s: "live" }>,
  payload: Record<string, unknown>,
  kind: TopicKind | undefined,
): Transition {
  const splits = kind === "sessions" || kind === "timeline";
  if (!splits) {
    return [{ ...st, pending: undefined }, [{ a: "deliver", payload, reset: payload.reset === true }]];
  }
  const pending = st.pending ?? (payload.reset === true && payload.more === true ? [] : undefined);
  if (!pending) {
    return [{ ...st, pending: undefined }, [{ a: "deliver", payload, reset: payload.reset === true }]];
  }
  const parts = [...pending, payload];
  if (payload.more === true) return [{ ...st, pending: parts }, []];
  return [{ ...st, pending: undefined }, [{ a: "deliver", payload: mergeParts(kind, parts), reset: true }]];
}

/**
 * Folds the parts of one split reset into the single event it stands for:
 * rows/items concatenated, `omitted` unioned, the first part's `events` /
 * `connections` / `older` kept, the last part's `lastSeq`. `more` is dropped.
 */
function mergeParts(kind: "sessions" | "timeline", parts: Record<string, unknown>[]): Record<string, unknown> {
  const first = parts[0];
  const out: Record<string, unknown> = { ...first, reset: true };
  delete out.more;
  const rowKey = kind === "sessions" ? "upsert" : "items";
  const rows: unknown[] = [];
  const omitted: unknown[] = [];
  for (const p of parts) {
    if (Array.isArray(p[rowKey])) rows.push(...(p[rowKey] as unknown[]));
    if (Array.isArray(p.omitted)) omitted.push(...(p.omitted as unknown[]));
  }
  out[rowKey] = rows;
  if (omitted.length > 0) out.omitted = omitted;
  if (kind === "timeline") out.lastSeq = parts[parts.length - 1].lastSeq;
  return out;
}
