// Every numeric limit the client shares with the daemon, in one place.
//
// Each value MUST equal the Go constant named beside it: a client that
// disagrees is not refused, it loses its socket. `limits.test.ts` reads the Go
// sources and fails on drift. This file imports nothing.

// --- framing (internal/protocol/frame.go) ---

/** `protocol.MaxFrameBytes`: the largest frame BODY, either direction. */
export const MAX_FRAME_BYTES = 1 << 20;

// --- listener (internal/config, internal/remote, internal/tmux, internal/panebus) ---

/** `config.DefaultRemotePort`. */
export const DEFAULT_REMOTE_PORT = 7717;
/** `remote.reqConcurrency`: requests in flight before a non-fatal `rate_limited`. */
export const MAX_REQUESTS_IN_FLIGHT = 4;
/** `remote.paneQueueDepth`: overflowing the ordered pane-input queue is fatal. */
export const PANE_QUEUE_DEPTH = 256;
/** `tmux.MaxScrollLines`: one scroll request's clamp. */
export const MAX_SCROLL_LINES = 500;
/** `remote.maxPaneName` = `panebus.MaxPaneNameLen`, in UTF-8 bytes. */
export const MAX_PANE_NAME = 128;

// --- push (internal/protocol/events.go, docs/mobile/02-events.md §9) ---

/** `protocol.MaxTopicBytes`. */
export const MAX_TOPIC_BYTES = 160;
/** `protocol.MaxWatchIDBytes`: a watch id is `^[A-Za-z0-9_-]{1,64}$`. */
export const MAX_WATCH_ID_BYTES = 64;
/** `protocol.EnvelopeMaxBytes`: an event frame minus its payload. */
export const ENVELOPE_MAX_BYTES = 1 << 10;
/** `protocol.MaxWatchTopicsPerRequest`. */
export const MAX_WATCH_TOPICS_PER_REQUEST = 32;
/** `protocol.MaxWatchTopicsPerConn`. */
export const MAX_WATCH_TOPICS_PER_CONN = 32;
/** `protocol.EventMaxBytes`. */
export const EVENT_MAX_BYTES = 768 << 10;
/** `protocol.EventItemMaxBytes`: a larger item arrives as `omitted`. */
export const EVENT_ITEM_MAX_BYTES = 256 << 10;
/** `protocol.FeedSnapshotItems` / `FeedDeltaItems`. */
export const FEED_SNAPSHOT_ITEMS = 300;
export const FEED_DELTA_ITEMS = 300;
/** `protocol.TimelineSnapshotItems` / `TimelineDeltaItems`. */
export const TIMELINE_SNAPSHOT_ITEMS = 500;
export const TIMELINE_DELTA_ITEMS = 200;
/** `protocol.TimelineSnapshotMaxBytes`. */
export const TIMELINE_SNAPSHOT_MAX_BYTES = 2 << 20;
/** `protocol.WatchRetryAfter`, in milliseconds. */
export const WATCH_RETRY_AFTER_MS = 30_000;
/** `remote.maxWatchingConns`: a ninth watching connection gets `limit`. */
export const MAX_WATCHING_CONNS = 8;

// --- attachments (internal/protocol/attach.go) ---

/** `protocol.AttachChunkBytes`: raw bytes per part. */
export const ATTACH_CHUNK_BYTES = 512 << 10;
/** `protocol.AttachMaxBytes`. */
export const ATTACH_MAX_BYTES = 20 << 20;
