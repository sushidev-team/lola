// What a preview fixture is: the daemon's whole answer for one look at the app.
//
// Invariants:
//  - A fixture is data. Nothing here opens a socket, reads the device or reaches
//    a daemon; `daemon.ts` plays it through the same `ChannelTransport` the app
//    uses, so a screen cannot tell it from a real Mac.
//  - Keys of `talk`, `feeds`, `timelines`, `diffs`, `panes` and `packages` are
//    session ids; a session with no entry answers like a quiet, empty one.

import type { GitSnapshot } from "$lib/projectgit";
import type { FailureCode, PairedDaemon, PathInfo } from "lola-transport";
import type {
  AgentFeedItem,
  CheckpointsData,
  DiffData,
  EndedSession,
  Event as ActivityEvent,
  HubStatusData,
  PRReadinessData,
  ProjectInfo,
  PrsData,
  ReviewPackagesData,
  SSHConnInfo,
  SSHTimelineItem,
  SessionInfo,
  StatusData,
  TalkStateData,
  TicketsData,
} from "@bindings/internal/protocol";
import type { PaneInfo, ResyncPayload } from "../wire";

export const FIXTURE_NAMES = [
  "empty",
  "busy",
  "busy-grouped",
  "graph",
  "permission",
  "dialog",
  "question",
  "long-story",
  "composer",
  "diff",
  "merge-ready",
  "ci-red",
  "review-package",
  "terminal",
  "offline",
  "key-changed",
  "pairing",
  "settings",
] as const;
export type FixtureName = (typeof FIXTURE_NAMES)[number];

/** How the link behaves once it is up. */
export type LinkPlan =
  | { kind: "up" }
  /** The link drops after the first snapshot: the screen shows its last data under a banner. */
  | { kind: "down"; code: FailureCode };

export interface Fixture {
  name: FixtureName;
  /** Paths the fixture serves (`/inbox`, `/session/s1/conversation?sheet=…`). */
  routes: readonly string[];
  /** Paired daemons the plugin reports. Empty: this phone was never paired. */
  daemons: PairedDaemon[];
  /** The tiers `ready` granted this phone. */
  tiers: readonly string[];
  path: PathInfo;
  link: LinkPlan;

  sessions: SessionInfo[];
  projects: ProjectInfo[];
  status: StatusData;
  events: ActivityEvent[];

  talk: Record<string, TalkStateData>;
  feeds: Record<string, AgentFeedItem[]>;
  timelines: Record<string, { items: SSHTimelineItem[]; connections: SSHConnInfo[] }>;
  diffs: Record<string, DiffData>;
  checkpoints: Record<string, CheckpointsData>;
  packages: Record<string, ReviewPackagesData>;
  /** Pane tabs of a session; the first is its agent pane. */
  panes: Record<string, PaneInfo[]>;
  /** The screen a pane answers a subscription with. */
  screens: Record<string, ResyncPayload>;
  readiness: Record<string, PRReadinessData>;
  git?: Record<string, GitSnapshot>;
  prs: PrsData;
  tickets: TicketsData;
  /** The Mac's hubStatus; absent: joined, with the relay connected (daemon.ts). */
  hubStatus?: HubStatusData;
  /** Sessions that left the roster but stay readable; absent: none (daemon.ts). */
  ended?: EndedSession[];

  /** Webview-local state the shared stores read when they are first imported. */
  storage: Record<string, string>;
  /** Client state a shared store keeps in memory; runs after the stores exist, before the app mounts. */
  seed?: () => Promise<void>;
  /** Runs once the route has been applied. */
  after?: () => Promise<void>;
}
