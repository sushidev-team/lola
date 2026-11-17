// The seam through which the phone drives the desktop stores from daemon events. Structural types
// only, so the desktop imports nothing from mobile. The desktop never installs a source, and every
// store then runs its own polling path unchanged.

import type { AgentFeedItem, SSHTimelineData, TalkStateData } from "@bindings/internal/protocol";

export type PushError =
  | "invalid_topic"
  | "unknown_session"
  | "limit"
  | "unavailable"
  | "bad_request"
  | "frame_too_large"
  | "rate_limited"
  | "denied";

export type FeedPush = {
  reset: boolean;
  older: boolean;
  epoch: string;
  lastSeq: number;
  items: AgentFeedItem[];
  scanning: boolean;
  health: string;
};

export interface PushSource {
  talk(session: string, on: (d: TalkStateData) => void, onError: (e: PushError) => void): () => void;
  feed(session: string, on: (p: FeedPush) => void, onError: (e: PushError) => void): () => void;
  /** `d` is a whole SSHTimelineData on reset, a delta (`items` to upsert, `connections` when changed) otherwise. */
  timeline(session: string, on: (d: SSHTimelineData, reset: boolean) => void, onError: (e: PushError) => void): () => void;
  diff(session: string, on: (rev: number) => void, onError: (e: PushError) => void): () => void;
}

let current: PushSource | null = null;

/** Mobile installs one at boot. The desktop never does, so its stores poll as before. */
export function setPushSource(s: PushSource | null): void {
  current = s;
}

export function pushSource(): PushSource | null {
  return current;
}
