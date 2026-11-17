// The waiting queue behind ⌘J, `n` and the sidebar's Waiting line. Pure.
//
// Two kinds of session wait on a person. A blocked one cannot go on until someone answers. A
// regressed one has a red PR with nothing working on it. "Ready to review" is deliberately
// not here: a resting agent is the normal state, and it would flood the queue until nobody
// used it.

import type { SessionInfo } from "@bindings/internal/protocol";
import { sshNeedsHuman } from "./ssh";
import { displayFor } from "./theme";
import { toMillis } from "./time";

export type WaitKind = "blocked" | "regressed";
export type WaitItem = { id: string; kind: WaitKind; since: number };

const REGRESSED = new Set(["ci_failed", "changes_requested", "merge_conflict"]);

function stamp(at: unknown): number {
  const t = toMillis(at as string);
  return Number.isNaN(t) ? 0 : t;
}

function classify(s: SessionInfo): WaitItem | null {
  if (displayFor(s.agentState) === "needs_you" || sshNeedsHuman(s)) {
    return { id: s.id, kind: "blocked", since: stamp(s.agentStateSince) };
  }
  // An agent already on it is not waiting on anybody, whatever colour the CI is.
  const busy = !s.agentState || s.agentState === "working" || s.agentState === "starting";
  if (REGRESSED.has(s.delivery ?? "") && !busy) {
    return { id: s.id, kind: "regressed", since: stamp(s.statusSince) };
  }
  return null;
}

/** Blocked before regressed, oldest first inside each. The id breaks a tie, so it never moves. */
export function waitingQueue(sessions: SessionInfo[]): WaitItem[] {
  const rank = (k: WaitKind) => (k === "blocked" ? 0 : 1);
  return sessions
    .map(classify)
    .filter((i): i is WaitItem => !!i)
    .sort((a, b) => rank(a.kind) - rank(b.kind) || a.since - b.since || a.id.localeCompare(b.id));
}

/** The item after the current one, wrapping. From a session not in the queue, the first. */
export function nextWaiting(sessions: SessionInfo[], current: string, dir: 1 | -1 = 1): WaitItem | undefined {
  const q = waitingQueue(sessions);
  if (!q.length) return undefined;
  const at = q.findIndex((i) => i.id === current);
  if (at < 0) return dir > 0 ? q[0] : q[q.length - 1];
  return q[(at + dir + q.length) % q.length];
}

export type Landing = "card" | "terminal" | "moment";

/**
 * Where a jump puts the keyboard. A modal takes only keys, so a card could not answer it. An
 * ssh approval lives in a card whatever the view preference says, because no terminal can
 * answer one. A session that opens on its terminal, by preference or because it has no
 * conversation, lands there. Regressed goes to the last thing that happened.
 */
export function landingFor(s: SessionInfo, item: WaitItem, opensOnTerminal: boolean): Landing {
  if (sshNeedsHuman(s)) return "card";
  if (s.inputReason === "dialog" || opensOnTerminal) return "terminal";
  return item.kind === "blocked" ? "card" : "moment";
}
