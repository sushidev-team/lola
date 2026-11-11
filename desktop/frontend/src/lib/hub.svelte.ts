// The team hub's client state: the daemon's hub status, the org board and the
// org's devices, plus the in-flight flag of whichever hub action a person just
// pressed. Every daemon call goes through here (HubService, one socket
// round-trip per method); components read and call.
//
// Rules this store keeps:
//   - It POLLS only while somebody looks. A view that shows hub data calls
//     `watch()` on mount and the returned release on destroy; with no watcher
//     there is no timer and no socket traffic. The daemon caches the board
//     (20s) and the org (60s) on its side too, so a poll never floods the hub.
//   - Reads go out together through Promise.allSettled: one failing read (an
//     unreachable hub, an older daemon) never blanks the others.
//   - An older daemon answers `unknown cmd "hubStatus"`. That is not an error to
//     show verbatim: `outdated` turns into one sentence telling the person what
//     to do (OUTDATED_MESSAGE).
//   - The enrollment token is an ARGUMENT of join() and is never kept here. A
//     form clears its own field after a successful join.
//   - Nothing here decides policy. The daemon re-checks every claim and release
//     and its refusal comes back in its own words.

import { HubService } from "@bindings/desktop";
import type {
  HubBoardData,
  HubBoardItem,
  HubClaimData,
  HubJoinData,
  HubLeaveData,
  HubOrgData,
  HubReleaseData,
  HubStatusData,
} from "@bindings/internal/protocol";

/** An older daemon predates the hub commands. */
export const HUB_PREDATES_RE = /unknown cmd/i;
/** The daemon's words for a hub that is not set up yet: not a fault to show. */
const NOT_JOINED_RE = /hub: not (enabled|joined)/i;
export const OUTDATED_MESSAGE = "Update the lola daemon to use Team hub";
/** Poll cadence while a hub view is on screen. */
export const HUB_POLL_MS = 15_000;

export type BoardState = "" | "open" | "claimed" | "done" | "failed" | "escalated";

export type HubResult<T> = { ok: true; data: T } | { ok: false; error: string };

function message(err: unknown): string {
  const s = err instanceof Error ? err.message : String(err);
  return s.replace(/^Error:\s*/, "");
}

export class HubStore {
  status = $state<HubStatusData | null>(null);
  statusError = $state("");
  board = $state<HubBoardData | null>(null);
  boardError = $state("");
  org = $state<HubOrgData | null>(null);
  orgError = $state("");
  /** The daemon predates the hub commands. */
  outdated = $state(false);
  /** The first status read has answered (either way). */
  loaded = $state(false);
  /** The board's state filter ("" = every state). */
  boardState = $state<BoardState>("");
  /** The action in flight: "join" | "leave" | "sync" | "claim:<id>" | "release:<id>" | "". */
  busy = $state("");

  /** The device name a join uses by default (this Mac's hostname). */
  defaultName = $state("");

  #statusWatchers = 0;
  #boardWatchers = 0;
  #timer: ReturnType<typeof setInterval> | null = null;
  #refreshing: Promise<void> | null = null;

  get joined(): boolean {
    return !!this.status?.joined;
  }

  /**
   * Start polling while a view is on screen. `board` also polls the org board
   * and devices; settings only needs the status. Returns the release.
   */
  watch(opts: { board?: boolean } = {}): () => void {
    const board = !!opts.board;
    this.#statusWatchers++;
    if (board) this.#boardWatchers++;
    if (!this.#timer) this.#timer = setInterval(() => void this.refresh(), HUB_POLL_MS);
    void this.refresh();
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.#statusWatchers--;
      if (board) this.#boardWatchers--;
      if (this.#statusWatchers <= 0 && this.#timer) {
        clearInterval(this.#timer);
        this.#timer = null;
        this.#statusWatchers = 0;
        this.#boardWatchers = 0;
      }
    };
  }

  /** Forget everything read so far (tests; a fresh store between mounts). */
  reset() {
    this.status = null;
    this.statusError = "";
    this.board = null;
    this.boardError = "";
    this.org = null;
    this.orgError = "";
    this.outdated = false;
    this.loaded = false;
    this.busy = "";
    this.#refreshing = null;
  }

  /** True while any view polls. Exposed for tests. */
  get polling(): boolean {
    return this.#timer !== null;
  }

  /** Read status (and, while a board view watches, the board and devices). */
  refresh(opts: { fresh?: boolean } = {}): Promise<void> {
    if (this.#refreshing && !opts.fresh) return this.#refreshing;
    const run = this.#read(!!opts.fresh).finally(() => {
      if (this.#refreshing === run) this.#refreshing = null;
    });
    this.#refreshing = run;
    return run;
  }

  async #read(fresh: boolean) {
    const wantBoard = this.#boardWatchers > 0 && (this.status?.joined ?? true);
    const [st, bd, og] = await Promise.allSettled([
      HubService.Status(),
      wantBoard ? HubService.Board({ state: this.boardState, limit: 0, fresh }) : Promise.resolve(null),
      wantBoard ? HubService.Org() : Promise.resolve(null),
    ]);
    this.loaded = true;
    if (st.status === "fulfilled") {
      this.status = st.value;
      this.statusError = "";
      this.outdated = false;
    } else {
      const msg = message(st.reason);
      this.outdated = HUB_PREDATES_RE.test(msg);
      this.statusError = this.outdated ? OUTDATED_MESSAGE : msg;
    }
    if (bd.status === "fulfilled") {
      if (bd.value) {
        this.board = bd.value;
        this.boardError = "";
      }
    } else {
      const msg = message(bd.reason);
      this.boardError = HUB_PREDATES_RE.test(msg) ? OUTDATED_MESSAGE : NOT_JOINED_RE.test(msg) ? "" : msg;
    }
    if (og.status === "fulfilled") {
      if (og.value) {
        this.org = og.value;
        this.orgError = "";
      }
    } else {
      const msg = message(og.reason);
      this.orgError = HUB_PREDATES_RE.test(msg) ? OUTDATED_MESSAGE : NOT_JOINED_RE.test(msg) ? "" : msg;
    }
  }

  /** Change the board filter and re-read. */
  setBoardState(s: BoardState) {
    this.boardState = s;
    void this.refresh({ fresh: false });
  }

  async loadDefaultName(): Promise<string> {
    if (this.defaultName) return this.defaultName;
    try {
      this.defaultName = (await HubService.DefaultDeviceName()) || "";
    } catch {
      this.defaultName = "";
    }
    return this.defaultName;
  }

  async #act<T>(busy: string, run: () => Promise<T>): Promise<HubResult<T>> {
    if (this.busy) return { ok: false, error: "another hub action is still running" };
    this.busy = busy;
    try {
      const data = await run();
      return { ok: true, data };
    } catch (err) {
      const msg = message(err);
      return { ok: false, error: HUB_PREDATES_RE.test(msg) ? OUTDATED_MESSAGE : msg };
    } finally {
      this.busy = "";
      void this.refresh({ fresh: true });
    }
  }

  /** Enroll this machine. The token is passed through and never stored. */
  join(url: string, token: string, name: string): Promise<HubResult<HubJoinData>> {
    return this.#act("join", () => HubService.Join({ url, token, name }));
  }

  leave(forgetKey: boolean): Promise<HubResult<HubLeaveData>> {
    return this.#act("leave", async () => {
      const d = await HubService.Leave(forgetKey);
      this.board = null;
      this.org = null;
      return d;
    });
  }

  claim(itemId: string, project = ""): Promise<HubResult<HubClaimData>> {
    return this.#act(`claim:${itemId}`, () => HubService.Claim({ itemId, project }));
  }

  release(itemId: string): Promise<HubResult<HubReleaseData>> {
    return this.#act(`release:${itemId}`, () => HubService.Release(itemId));
  }

  sync(): Promise<HubResult<unknown>> {
    return this.#act("sync", () => HubService.Sync());
  }

  /** The board rows, never null. */
  get items(): HubBoardItem[] {
    return this.board?.items ?? [];
  }
}

export const hub = new HubStore();

/** "connected" | "error" | "waiting" for the connection pill. */
export function connectionState(st: HubStatusData | null, now = Date.now()): { tone: "good" | "bad" | "warn"; label: string } {
  if (!st?.joined) return { tone: "warn", label: "Not joined" };
  const hb = st.lastHeartbeat ? Date.parse(String(st.lastHeartbeat)) : NaN;
  const errAt = st.lastErrorAt ? Date.parse(String(st.lastErrorAt)) : NaN;
  if (st.lastError && (isNaN(hb) || (!isNaN(errAt) && errAt > hb))) return { tone: "bad", label: "Error" };
  if (isNaN(hb)) return { tone: "warn", label: "Waiting for first heartbeat" };
  // Three missed poll windows (300s default) read as stale rather than live.
  if (now - hb > 15 * 60_000) return { tone: "warn", label: "Stale" };
  return { tone: "good", label: "Connected" };
}
