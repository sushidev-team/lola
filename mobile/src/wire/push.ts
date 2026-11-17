// Payload types of the push vocabulary that no desktop binding reaches.
//
// Mirrors internal/protocol/events.go and attach.go, field for field and in
// declaration order; `testdata/events.json` pins the bytes. Types a binding
// already reaches (TalkStateData, StatusData, ProjectsData, SessionInfo, ...)
// come from `@bindings/internal/protocol`, never from a second copy here.

import type {
  AgentFeedItem,
  Event,
  ProjectsData,
  SSHConnInfo,
  SSHTimelineItem,
  SessionInfo,
  StatusData,
  TalkStateData,
} from "@bindings/internal/protocol";

/** `protocol.WatchArgs`: 1..MAX_WATCH_TOPICS_PER_REQUEST canonical topics, no duplicates. */
export interface WatchArgs {
  topics: string[];
}

/** `protocol.UnwatchArgs`. */
export interface UnwatchArgs {
  topics?: string[];
  /** Every topic on this connection. */
  all?: boolean;
}

/** `protocol.WatchResult`: one topic's outcome. `code` is set when `ok` is false. */
export interface WatchResult {
  topic: string;
  ok: boolean;
  code?: string;
  message?: string;
}

/** `protocol.WatchData`: one result per requested topic; `[]` for `unwatch{all}`. */
export interface WatchData {
  topics: WatchResult[];
}

/** `protocol.OmittedItem`: an item too large to send; fetch it with its topic's pull command. */
export interface OmittedItem {
  id: string;
  seq: number;
}

/** `protocol.SessionsEvent`. Rows are the remote projection of SessionInfo. */
export interface SessionsEvent {
  /** Discard everything held; this event (with its parts) is the full set. */
  reset?: boolean;
  /** This reset continues in the next event. */
  more?: boolean;
  upsert?: SessionInfo[];
  remove?: string[];
  /** Ids whose row is too large to stream; fetch them with `sessions`. */
  omitted?: string[];
  /** The activity ring, whole; on a reset's first part and when it changed. */
  events?: Event[];
}

/** `protocol.FeedEvent`. `items` is never null. */
export interface FeedEvent {
  reset?: boolean;
  /** Reset only: older history exists; page it with `agentFeed{before}`. */
  older?: boolean;
  session: string;
  epoch: string;
  lastSeq: number;
  scanning?: boolean;
  health: string;
  items: AgentFeedItem[];
  omitted?: OmittedItem[];
}

/** `protocol.TimelineEvent`, built from the redacted session timeline only. */
export interface TimelineEvent {
  reset?: boolean;
  more?: boolean;
  /** Reset only: the snapshot is a newest window; page `sessionTimeline` from `after:0` up to `lastSeq`. */
  older?: boolean;
  session: string;
  lastSeq: number;
  items: SSHTimelineItem[];
  connections?: SSHConnInfo[];
  omitted?: OmittedItem[];
}

/** `protocol.DiffEvent`: a signal-only counter. */
export interface DiffEvent {
  rev: number;
  cause?: string;
}

/** The payload of each topic kind. */
export interface TopicPayloads {
  sessions: SessionsEvent;
  projects: ProjectsData;
  status: StatusData;
  talk: TalkStateData;
  feed: FeedEvent;
  timeline: TimelineEvent;
  diff: DiffEvent;
}

/** `protocol.AttachImageArgs`. `data` is base64 on the wire. */
export interface AttachImageArgs {
  session: string;
  /** The upload key `^[A-Za-z0-9_-]{8,64}$`, client-random; not a filename. */
  name: string;
  mime: string;
  data: string;
  part: number;
  last?: boolean;
}

/** `protocol.AttachImageData`. A non-last part reports `received` only. */
export interface AttachImageData {
  received: number;
  path?: string;
  thumb?: string;
}

/** `protocol.AttachmentImageArgs`. */
export interface AttachmentImageArgs {
  session: string;
  path: string;
  /** A multiple of ATTACH_CHUNK_BYTES. */
  offset?: number;
}

/** `protocol.AttachmentImageData`: one chunk, `data` base64. */
export interface AttachmentImageData {
  mime: string;
  size: number;
  offset: number;
  data: string;
  eof?: boolean;
}
