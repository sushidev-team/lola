import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import * as L from "./limits";

const root = resolve(__dirname, "../../..");
const read = (p: string) => readFileSync(resolve(root, p), "utf8");

/** The value of `Name = <expr>` in a Go source, for `1 << 20`, `768 << 10`, plain ints and `30 * time.Second`. */
function goConst(src: string, name: string): number {
  const m = new RegExp(`^\\s*(?:const\\s+)?${name}\\s*=\\s*([^/\\n]+)`, "m").exec(src);
  if (!m) throw new Error(`${name} not found`);
  const expr = m[1].trim();
  const shift = /^(\d+)\s*<<\s*(\d+)$/.exec(expr);
  if (shift) return Number(shift[1]) * 2 ** Number(shift[2]);
  const secs = /^(\d+)\s*\*\s*time\.Second$/.exec(expr);
  if (secs) return Number(secs[1]) * 1000;
  if (/^\d+$/.test(expr)) return Number(expr);
  throw new Error(`${name}: unsupported expression ${expr}`);
}

describe("limits equal the Go constants", () => {
  const frame = read("internal/protocol/frame.go");
  const events = read("internal/protocol/events.go");
  const attach = read("internal/protocol/attach.go");

  it("framing and listener", () => {
    expect(L.MAX_FRAME_BYTES).toBe(goConst(frame, "MaxFrameBytes"));
    expect(L.DEFAULT_REMOTE_PORT).toBe(goConst(read("internal/config/remote.go"), "DefaultRemotePort"));
    expect(L.MAX_REQUESTS_IN_FLIGHT).toBe(goConst(read("internal/remote/server.go"), "reqConcurrency"));
    expect(L.PANE_QUEUE_DEPTH).toBe(goConst(read("internal/remote/server.go"), "paneQueueDepth"));
    expect(L.MAX_SCROLL_LINES).toBe(goConst(read("internal/tmux/client.go"), "MaxScrollLines"));
    expect(L.MAX_PANE_NAME).toBe(goConst(read("internal/remote/pane.go"), "maxPaneName"));
    expect(L.MAX_PANE_NAME).toBe(goConst(read("internal/panebus/panebus.go"), "MaxPaneNameLen"));
  });

  it("push", () => {
    expect(L.MAX_TOPIC_BYTES).toBe(goConst(events, "MaxTopicBytes"));
    expect(L.MAX_WATCH_ID_BYTES).toBe(goConst(events, "MaxWatchIDBytes"));
    expect(L.ENVELOPE_MAX_BYTES).toBe(goConst(events, "EnvelopeMaxBytes"));
    expect(L.MAX_WATCH_TOPICS_PER_REQUEST).toBe(goConst(events, "MaxWatchTopicsPerRequest"));
    expect(L.MAX_WATCH_TOPICS_PER_CONN).toBe(goConst(events, "MaxWatchTopicsPerConn"));
    expect(L.EVENT_MAX_BYTES).toBe(goConst(events, "EventMaxBytes"));
    expect(L.EVENT_ITEM_MAX_BYTES).toBe(goConst(events, "EventItemMaxBytes"));
    expect(L.FEED_SNAPSHOT_ITEMS).toBe(goConst(events, "FeedSnapshotItems"));
    expect(L.FEED_DELTA_ITEMS).toBe(goConst(events, "FeedDeltaItems"));
    expect(L.TIMELINE_SNAPSHOT_ITEMS).toBe(goConst(events, "TimelineSnapshotItems"));
    expect(L.TIMELINE_DELTA_ITEMS).toBe(goConst(events, "TimelineDeltaItems"));
    expect(L.TIMELINE_SNAPSHOT_MAX_BYTES).toBe(goConst(events, "TimelineSnapshotMaxBytes"));
    expect(L.WATCH_RETRY_AFTER_MS).toBe(goConst(events, "WatchRetryAfter"));
    expect(L.MAX_WATCHING_CONNS).toBe(goConst(read("internal/remote/events.go"), "maxWatchingConns"));
  });

  it("attachments", () => {
    expect(L.ATTACH_CHUNK_BYTES).toBe(goConst(attach, "AttachChunkBytes"));
    expect(L.ATTACH_MAX_BYTES).toBe(goConst(attach, "AttachMaxBytes"));
  });

  it("matches the Swift frame cap the plugin enforces", () => {
    expect(read("mobile/plugins/lola-transport/ios/Sources/LolaTransportCore/FrameCodec.swift")).toMatch(
      /maxFrameBytes\s*=\s*1\s*<<\s*20/,
    );
  });

  it("keeps a full event inside one frame", () => {
    expect(L.EVENT_MAX_BYTES + L.ENVELOPE_MAX_BYTES + 4).toBeLessThanOrEqual(L.MAX_FRAME_BYTES);
  });
});
