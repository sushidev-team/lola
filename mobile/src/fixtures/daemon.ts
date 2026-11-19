// A scripted Mac for the preview page and for tests: it plays one `Fixture` over
// the same `ChannelTransport` the app uses, so every screen, store and shim
// runs unchanged against it.
//
// Invariants:
//  - It speaks the wire, not the stores: a `req` frame gets a `resp` on its id, a
//    `watch` gets its per-topic results and then the first event of each stream
//    with `reset`, a `sub` gets the pane's screen as its acknowledging resync.
//  - Replies are queued, never delivered inside `send`: the correlator has the
//    request registered before the answer exists.
//  - A command it does not script is refused `unknown_cmd` on its id, the way a
//    daemon refuses one it does not know. Writes are acknowledged and change
//    nothing: a preview is a look, not a session.
//  - Nothing here is secret or real: ids, hosts and names are literals.

import type { LolaTransportPlugin } from "lola-transport";
import type { HubStatusData } from "@bindings/internal/protocol";
import {
  FRAME_ERR,
  FRAME_EVENT,
  FRAME_RESP,
  FRAME_RESYNC,
  FRAME_SUB,
  FRAME_VERSION_CURRENT,
  FRAME_REQ,
  parseTopic,
  type Frame,
  type RequestFields,
  type WatchArgs,
  type WatchData,
  type WatchResult,
} from "../wire";
import { transport } from "../native";
import { ChannelTransport } from "../wailsshim/channeltransport";
import { FakeLink } from "../wailsshim/fakelink";
import { LinkError } from "../wailsshim/pluginerror";
import { talk } from "./build";
import { SCREENSHOT_MIME, SCREENSHOT_PNG_BASE64 } from "./screenshot";
import type { Fixture } from "./types";

/** Commands a preview acknowledges without doing anything. */
const WRITES: ReadonlySet<string> = new Set([
  "agentSay",
  "agentChoose",
  "agentStop",
  "agentQueueRelease",
  "agentSetting",
  "answer",
  "feedback",
  "mergePR",
  "broadcast",
  "planDecide",
  "renameSession",
  "kill",
  "revive",
  "resolveConflict",
  "addressPRFeedback",
  "switchAgent",
  "bestOf",
  "bestOfPick",
  "dev",
  "devFreePort",
  "enable",
  "disable",
  "review",
  "coderabbit",
  "restoreCheckpoint",
  "forkCheckpoint",
  "sessionTask",
  "shellCreate",
  "paneClose",
  "paneResize",
  "attachImage",
  "open",
  "openPr",
  "openManual",
  "openTicket",
  "pollOnce",
  "reviewPackageMark",
  "closeExitedTemporary",
]);

/** A joined Mac whose relay is up with one phone on it: the hubStatus a fixture gets by default. */
const HUB_STATUS: HubStatusData = {
  enabled: true,
  joined: true,
  membership: "joined",
  claimsToday: 0,
  maxClaimsPerDay: 0,
  pendingOutcomes: 0,
  relay: { state: "connected", activeConns: 1, rendezvous: 1 },
};

/** How long after the first snapshot a `down` link drops. */
export const DROP_AFTER_MS = 250;

type Args = Record<string, unknown>;

class Refused extends Error {
  constructor(readonly code: string) {
    super(code);
  }
}

export class PreviewDaemon extends FakeLink {
  constructor(private readonly fx: Fixture) {
    super();
    this.up = {
      epoch: 1,
      path: fx.path,
      device: { deviceId: "phone-preview", name: "iPhone", tiers: [...fx.tiers], host: fx.status.host ?? "mars.local" },
      raceMs: 180,
    };
    this.onSend = (f) => this.handle(f);
  }

  private later(fn: () => void): void {
    setTimeout(fn, 0);
  }

  private reply(f: Frame, data?: unknown): void {
    this.later(() =>
      this.deliver({ v: FRAME_VERSION_CURRENT, type: FRAME_RESP, id: f.id, payload: { ok: true, ...(data === undefined ? {} : { data }) } }),
    );
  }

  private refuse(f: Frame, code: string): void {
    this.later(() => this.deliver({ v: FRAME_VERSION_CURRENT, type: FRAME_ERR, id: f.id, payload: { code } }));
  }

  private handle(f: Frame): void {
    if (f.type === FRAME_SUB) return this.subscribe(f);
    if (f.type !== FRAME_REQ || !f.cmd) return;
    const fields = (f.payload ?? {}) as RequestFields;
    try {
      if (f.cmd === "watch") return this.watch(f, (fields.args as WatchArgs).topics);
      if (f.cmd === "unwatch") return this.reply(f, { topics: [] });
      this.reply(f, this.answer(f.cmd, fields));
    } catch (err) {
      if (err instanceof Refused) this.refuse(f, err.code);
      else throw err;
    }
  }

  private subscribe(f: Frame): void {
    const pane = f.pane ?? "";
    const screen = this.fx.screens[pane] ?? { cols: 80, rows: 24, cursorX: 0, cursorY: 0 };
    this.later(() => this.deliver({ v: FRAME_VERSION_CURRENT, type: FRAME_RESYNC, id: f.id, pane, seq: 1, payload: screen }));
  }

  // --- commands ------------------------------------------------------------

  private answer(cmd: string, fields: RequestFields): unknown {
    const fx = this.fx;
    const args = (fields.args ?? {}) as Args;
    const id = String(fields.session ?? args.session ?? "");
    switch (cmd) {
      case "sessions":
        return { sessions: fx.sessions, events: fx.events };
      case "projects":
        return { projects: fx.projects, groups: [] };
      case "status":
        return fx.status;
      case "talkState":
        return fx.talk[id] ?? talk(id);
      case "agentFeed":
        return this.feed(id);
      case "sessionTimeline":
        return this.timeline(id);
      case "diff":
        return fx.diffs[id] ?? { session: id, base: "main", mergeBase: "", files: [] };
      case "checkpoints":
        return fx.checkpoints[id] ?? { session: id, checkpoints: [], restorable: false };
      case "checkpointDiff":
        return this.checkpointDiff(id, Number(args.seq));
      case "bestOfCompare":
        return this.bestOfCompare(id);
      case "reviewPackageChecks":
        return { checks: [] };
      case "reviewPackages":
        return fx.packages[id] ?? { packages: [] };
      case "reviewPackageImage":
        return this.reviewImage(id, String(args.package ?? ""), String(args.artifact ?? ""), args.chunked === true);
      case "panes":
        return { session: id, panes: fx.panes[id] ?? this.agentPane(id), canCreateShell: true };
      case "prReadiness":
        if (!fx.readiness[id]) throw new Refused("unknown_session");
        return fx.readiness[id];
      case "prs":
        return fx.prs;
      case "tickets":
        return fx.tickets;
      case "endedSessions":
        return { sessions: fx.ended ?? [] };
      case "hubStatus":
        return fx.hubStatus ?? HUB_STATUS;
      default:
        if (WRITES.has(cmd)) return {};
        throw new Refused("unknown_cmd");
    }
  }

  /**
   * A screenshot a fixture's package names, as the daemon answers it: one
   * chunk when chunked (the bundled image is far under a chunk), else the
   * desktop's whole data URL. An artifact no package names is unknown.
   */
  private reviewImage(id: string, pkgID: string, artifact: string, chunked: boolean) {
    const pkg = this.fx.packages[id]?.packages?.find((p) => p.id === pkgID);
    if (!pkg?.screens?.some((s) => s.after === artifact || s.before === artifact)) throw new Refused("unknown_session");
    if (!chunked) return { dataURL: `data:${SCREENSHOT_MIME};base64,${SCREENSHOT_PNG_BASE64}` };
    const size = atob(SCREENSHOT_PNG_BASE64).length;
    return { dataURL: "", mime: SCREENSHOT_MIME, size, offset: 0, data: SCREENSHOT_PNG_BASE64, eof: true };
  }

  /** What one checkpoint changed: the first files of the session's own diff, one more file per turn. */
  private checkpointDiff(id: string, seq: number) {
    const d = this.fx.diffs[id];
    if (!d || !this.fx.checkpoints[id]?.checkpoints?.some((c) => c.seq === seq)) throw new Refused("unknown_session");
    return { ...d, files: (d.files ?? []).slice(0, Math.min(seq, 4)).map((f) => ({ ...f, patch: "" })) };
  }

  /** Two attempts at one issue: the session itself and a sibling that has not opened a PR. */
  private bestOfCompare(id: string) {
    const s = this.fx.sessions.find((x) => x.id === id);
    if (!s || !s.bestOf) throw new Refused("unknown_session");
    const a = { session: s.id, attempt: 1, agent: s.agent || "claude", branch: s.branch, display: "idle", delivery: s.delivery, prNumber: s.prNumber, files: 6, additions: 128, deletions: 41, age: s.age };
    const b = { session: `${s.id}-b2`, attempt: 2, agent: "codex", branch: `${s.branch}-b2`, display: "working", files: 4, additions: 87, deletions: 12, age: s.age };
    return { group: s.bestOf.group, issue: s.issue, title: s.title, project: s.project, attempts: [a, b], suggested: s.id };
  }

  private agentPane(id: string) {
    const tmux = this.fx.sessions.find((s) => s.id === id)?.tmuxName || id;
    return [{ name: tmux, kind: "agent", label: "agent" }];
  }

  private feed(id: string) {
    const items = this.fx.feeds[id] ?? [];
    return { session: id, epoch: "e1", lastSeq: items.length, health: "ok", items };
  }

  private timeline(id: string) {
    const t = this.fx.timelines[id] ?? { items: [], connections: [] };
    return { session: id, items: t.items, connections: t.connections, lastSeq: t.items.length };
  }

  // --- streams -------------------------------------------------------------

  /** One result per topic, then each live topic's first event with `reset`. */
  private watch(f: Frame, topics: string[]): void {
    const results: WatchResult[] = [];
    const first: Array<() => void> = [];
    for (const topic of topics) {
      const payload = this.snapshot(topic);
      if (payload === null) {
        results.push({ topic, ok: false, code: parseTopic(topic) ? "unknown_session" : "invalid_topic" });
        continue;
      }
      results.push({ topic, ok: true });
      first.push(() => {
        this.deliver({ v: FRAME_VERSION_CURRENT, type: FRAME_EVENT, id: f.id, topic, seq: 1, payload });
        if (topic === "sessions" && this.fx.link.kind === "down") {
          const { code } = this.fx.link;
          setTimeout(() => this.raiseDown(new LinkError(code, code)), DROP_AFTER_MS);
        }
      });
    }
    this.reply(f, { topics: results } satisfies WatchData);
    for (const send of first) this.later(send);
  }

  /** The first event of a topic, or null when the daemon would refuse it. */
  private snapshot(topic: string): unknown {
    const parsed = parseTopic(topic);
    if (!parsed) return null;
    const fx = this.fx;
    const known = fx.sessions.some((s) => s.id === parsed.session);
    switch (parsed.kind) {
      case "sessions":
        return { reset: true, upsert: fx.sessions, events: fx.events };
      case "projects":
        return { projects: fx.projects, groups: [] };
      case "status":
        return fx.status;
      default:
        if (!known) return null;
    }
    const id = parsed.session;
    switch (parsed.kind) {
      case "talk":
        return fx.talk[id] ?? talk(id);
      case "feed":
        return { reset: true, ...this.feed(id) };
      case "timeline":
        return { reset: true, ...this.timeline(id) };
      default:
        return { rev: 1 };
    }
  }
}

/**
 * The browser's stand-in plugin, completed with what a paired phone's plugin
 * would report. It patches the one object `@mobile/native` hands out, so every
 * module that already holds `transport` sees it.
 */
export function installPreviewPlugin(fx: Fixture): void {
  const never = <T>() => new Promise<T>(() => {});
  const patch: Partial<LolaTransportPlugin> = {
    daemons: async () => ({ daemons: fx.daemons }),
    scanCapability: async () => ({ available: true, authorization: "authorized" }),
    // The scanner stays open: the screen under test is the one around it.
    scanPairing: () => never(),
    forget: async () => {},
    rename: async () => {},
  };
  Object.assign(transport, patch);
}

/** The app's transport, wired to a scripted Mac. */
export function previewTransport(fx: Fixture): ChannelTransport {
  return new ChannelTransport({ open: async () => new PreviewDaemon(fx) });
}
