// The team hub's client state: the daemon's hub status, the org board and the
// org's devices, plus which hub actions are in flight. Every daemon call goes
// through here (HubService, one socket round-trip per method); components read
// and call.
//
// Rules this store keeps:
//   - It POLLS only while somebody looks. A view that shows hub data calls
//     `watch()` on mount and the returned release on destroy. The always-on
//     sidebar takes the LIGHT watch instead (`watchLight()`): a status read a
//     minute and the board every few minutes, enough for the Needs-you badge
//     and the off-screen notices, never the org or the stats. The daemon
//     caches the board (20s) and the org (60s) on its side too, so a poll
//     never floods the hub.
//   - Reads go out together through Promise.allSettled: one failing read (an
//     unreachable hub, an older daemon) never blanks the others.
//   - An older daemon answers `unknown cmd "hubStatus"`. That is not an error to
//     show verbatim: `outdated` turns into one sentence telling the person what
//     to do (OUTDATED_MESSAGE). An older daemon without `hubStats` just has no
//     stats line.
//   - The enrollment token is an ARGUMENT of join() and is never kept here. A
//     form clears its own field after a successful join.
//   - Busy is per action: claiming one chore never greys out another row.
//     Join and leave are exclusive (they change who this Mac is), so they wait
//     for nothing else and nothing else starts while they run.
//   - Nothing here decides policy. The daemon re-checks every claim and release
//     and its refusal comes back in its own words.

import { HubService, type HubStatsDTO } from "@bindings/desktop";
import type {
  HubBoardData,
  HubBoardItem,
  HubClaimData,
  HubCreateTeamData,
  HubInviteData,
  HubRevokeData,
  HubTeamSettingsArgs,
  HubTeamSettingsData,
  HubUpdateScope,
  HubJoinData,
  HubLeaveData,
  HubOrgData,
  HubReleaseData,
  HubStatusData,
} from "@bindings/internal/protocol";
import { hubNotices, needsYou, type HubNotice, type NeedsRow } from "$lib/hubboard";

/** An older daemon predates the hub commands. */
export const HUB_PREDATES_RE = /unknown cmd/i;
/** The daemon's words for a hub that is not set up yet: not a fault to show. */
const NOT_JOINED_RE = /hub: not (enabled|joined)/i;
export const OUTDATED_MESSAGE = "Update the lola daemon to use Team hub";
/** Poll cadence while a hub view is on screen. */
export const HUB_POLL_MS = 15_000;
/** The sidebar's light poll: status this often while nothing hub-shaped is on screen. */
export const HUB_LIGHT_POLL_MS = 60_000;
/** ...and the board at most this often on that light poll. */
export const HUB_LIGHT_BOARD_MS = 5 * 60_000;
/** The stats line is re-read at most this often. */
export const HUB_STATS_MS = 5 * 60_000;
/** After "Sync now": how often to re-read while waiting for the pass, and for how long. */
export const SYNC_POLL_MS = 1_000;
export const SYNC_WAIT_MS = 30_000;
/** How long "Synced" stays before the button reads "Sync now" again. */
export const SYNC_DONE_MS = 2_500;

/** The life of one "Sync now": "" (idle) → syncing → synced | failed | slow. */
export type SyncPhase = "" | "syncing" | "synced" | "failed" | "slow";

export type BoardState = "" | "open" | "claimed" | "in_review" | "done" | "failed" | "escalated";

export type HubResult<T> = { ok: true; data: T } | { ok: false; error: string };

/** Actions that change who this Mac is: nothing else runs beside them. */
const EXCLUSIVE = new Set(["join", "leave", "create"]);

/** Call fn as a promise: a throw (a binding missing in an older build) rejects instead of escaping. */
function attempt<T>(fn: () => Promise<T>): Promise<T> {
  return Promise.resolve().then(fn);
}

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
  /** The org's last seven days (null = not read, or the daemon has no stats). */
  stats = $state<HubStatsDTO | null>(null);
  /** The daemon predates the hub commands. */
  outdated = $state(false);
  /** The first status read has answered (either way). */
  loaded = $state(false);
  /** The board's state filter ("" = every state). */
  boardState = $state<BoardState>("");
  /** Actions in flight, keyed "join" | "leave" | "sync" | "claim:<id>" | "release:<id>". */
  pending = $state<Record<string, true>>({});

  /** Where the last "Sync now" is. `syncError` says why it failed. */
  syncPhase = $state<SyncPhase>("");
  syncError = $state("");

  /** The device name a join uses by default (this Mac's hostname). */
  defaultName = $state("");

  /** The item a chip asked the Team view to show ("" = none). The view clears it. */
  focusItem = $state("");

  /**
   * Where off-screen notices go (a toast). Set by whoever shows them; with no
   * receiver, nothing is said.
   */
  onNotice: ((n: HubNotice) => void) | null = null;

  #statusWatchers = 0;
  #boardWatchers = 0;
  #lightWatchers = 0;
  #timer: ReturnType<typeof setInterval> | null = null;
  #lightTimer: ReturnType<typeof setInterval> | null = null;
  #refreshing: Promise<void> | null = null;
  /** Each #read takes a number; an answer older than one already applied is dropped. */
  #seq = 0;
  #applied = 0;
  /** When the board last answered (ms), for the light poll's cadence. */
  #boardAt = 0;
  #statsAt = 0;
  /** Items a person claimed here: their sessions get no "this Mac took" notice. */
  #claimedHere = new Set<string>();
  /** Bumped by each sync; an older sync's wait loop stops when it changes. */
  #syncRun = 0;
  #syncDoneTimer: ReturnType<typeof setTimeout> | null = null;
  #onVisible = () => {
    if (typeof document !== "undefined" && document.visibilityState === "visible") void this.refresh();
  };

  get joined(): boolean {
    return !!this.status?.joined;
  }

  /**
   * The hub refused this Mac's signature (an admin removed it) and lola
   * dropped the identity: only a new invite brings it back. A daemon that
   * predates `membership` never says so, which reads as plain "not joined".
   */
  get revoked(): boolean {
    return !this.joined && this.status?.membership === "revoked";
  }

  /**
   * The first action in flight, or "". Kept for the settings form, which only
   * asks "is anything running"; rows ask isBusy for their own key.
   */
  get busy(): string {
    return Object.keys(this.pending)[0] ?? "";
  }

  /** Whether the action under `key` is in flight. */
  isBusy(key: string): boolean {
    return !!this.pending[key];
  }

  /** The Needs-you rows from the last board and status read. */
  get needs(): NeedsRow[] {
    return needsYou(this.items, this.status);
  }

  /** How many rows need a person: the sidebar badge. 0 when not joined. */
  get needsCount(): number {
    return this.joined ? this.needs.length : 0;
  }

  /**
   * Start polling while a view is on screen. `board` also polls the org board
   * and devices; settings only needs the status. Returns the release.
   */
  watch(opts: { board?: boolean } = {}): () => void {
    const board = !!opts.board;
    this.#statusWatchers++;
    if (board) this.#boardWatchers++;
    if (!this.#timer) {
      this.#timer = setInterval(() => void this.refresh(), HUB_POLL_MS);
      // Coming back to the window reads at once instead of waiting out the poll.
      if (typeof window !== "undefined") {
        window.addEventListener("focus", this.#onVisible);
        document.addEventListener("visibilitychange", this.#onVisible);
      }
    }
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
        if (typeof window !== "undefined") {
          window.removeEventListener("focus", this.#onVisible);
          document.removeEventListener("visibilitychange", this.#onVisible);
        }
        this.#statusWatchers = 0;
        this.#boardWatchers = 0;
      }
    };
  }

  /**
   * The always-on light poll (the sidebar): a status read every minute and
   * the board every few minutes while joined, so the badge and the notices
   * work off-screen. A full watcher's poll replaces it while one runs.
   */
  watchLight(): () => void {
    this.#lightWatchers++;
    if (!this.#lightTimer) {
      this.#lightTimer = setInterval(() => {
        if (!this.#timer) void this.refresh({ light: true });
      }, HUB_LIGHT_POLL_MS);
    }
    if (!this.#timer) void this.refresh({ light: true });
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.#lightWatchers--;
      if (this.#lightWatchers <= 0 && this.#lightTimer) {
        clearInterval(this.#lightTimer);
        this.#lightTimer = null;
        this.#lightWatchers = 0;
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
    this.stats = null;
    this.outdated = false;
    this.loaded = false;
    this.pending = {};
    this.focusItem = "";
    this.syncPhase = "";
    this.syncError = "";
    this.#boardAt = 0;
    this.#statsAt = 0;
    this.#claimedHere.clear();
    this.#syncRun++;
    if (this.#syncDoneTimer) clearTimeout(this.#syncDoneTimer);
    this.#syncDoneTimer = null;
    this.#refreshing = null;
  }

  /** True while any view polls. Exposed for tests. */
  get polling(): boolean {
    return this.#timer !== null;
  }

  /** True while the light poll runs. Exposed for tests. */
  get lightPolling(): boolean {
    return this.#lightTimer !== null;
  }

  /** Ask the Team view to bring one item into view (a chip's click). */
  focus(itemId: string) {
    this.focusItem = itemId;
  }

  /**
   * Read status (and, while a board view watches, the board and devices). A
   * light read adds the board only when it is a few minutes old.
   */
  refresh(opts: { fresh?: boolean; light?: boolean } = {}): Promise<void> {
    if (this.#refreshing && !opts.fresh) return this.#refreshing;
    const run = this.#read(!!opts.fresh, !!opts.light).finally(() => {
      if (this.#refreshing === run) this.#refreshing = null;
    });
    this.#refreshing = run;
    return run;
  }

  async #read(fresh: boolean, light: boolean) {
    // A fresh read (after a claim) runs beside any poll already in flight. The
    // older answer must not land after the newer one and put the pre-claim
    // board back, so every read is numbered and a stale one is dropped whole.
    const seq = ++this.#seq;
    const full = () => this.#boardWatchers > 0;
    const lightBoard = light && Date.now() - this.#boardAt >= HUB_LIGHT_BOARD_MS;
    const boardWanted = () => full() || lightBoard;
    const wantBoard = boardWanted() && (this.status?.joined ?? true);
    const none = { status: "fulfilled", value: null } as const;
    const readBoard = () =>
      Promise.allSettled([
        attempt(() => HubService.Board({ state: this.boardState, limit: 0, fresh })),
        full() ? attempt(() => HubService.Org()) : Promise.resolve(null),
      ]);
    // Both go out together; allSettled never rejects.
    const boardRead = wantBoard ? readBoard() : null;
    const [st] = await Promise.allSettled([attempt(() => HubService.Status())]);
    let [bd, og]: [PromiseSettledResult<HubBoardData | null>, PromiseSettledResult<HubOrgData | null>] = boardRead
      ? await boardRead
      : [none, none];
    // The board was skipped on the OLD status (not joined) but this status says
    // joined (a join just finished): read it now, not on the next poll.
    if (!boardRead && boardWanted() && st.status === "fulfilled" && st.value?.joined) {
      [bd, og] = await readBoard();
    }
    if (seq < this.#applied) return;
    this.#applied = seq;
    this.loaded = true;
    if (st.status === "fulfilled") {
      const prev = this.status;
      this.status = st.value;
      this.statusError = "";
      this.outdated = false;
      this.#notify(prev, st.value);
    } else {
      const msg = message(st.reason);
      this.outdated = HUB_PREDATES_RE.test(msg);
      this.statusError = this.outdated ? OUTDATED_MESSAGE : msg;
    }
    if (bd.status === "fulfilled") {
      if (bd.value) {
        this.board = bd.value;
        this.boardError = "";
        this.#boardAt = Date.now();
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
    if (full() && this.joined && Date.now() - this.#statsAt >= HUB_STATS_MS) void this.#readStats();
  }

  /** The stats line: best effort. An older daemon or hub simply has none. */
  async #readStats() {
    this.#statsAt = Date.now();
    try {
      this.stats = await HubService.Stats();
    } catch {
      this.stats = null;
    }
  }

  #notify(prev: HubStatusData | null, next: HubStatusData | null) {
    const out = this.onNotice;
    if (!out) return;
    for (const n of hubNotices(prev, next, this.#claimedHere)) out(n);
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

  async #act<T>(key: string, run: () => Promise<T>): Promise<HubResult<T>> {
    const keys = Object.keys(this.pending);
    if (this.pending[key]) return { ok: false, error: "that is still running" };
    if (keys.some((k) => EXCLUSIVE.has(k)) || (EXCLUSIVE.has(key) && keys.length))
      return { ok: false, error: "another Team hub action is still running" };
    this.pending = { ...this.pending, [key]: true };
    try {
      const data = await run();
      return { ok: true, data };
    } catch (err) {
      const msg = message(err);
      return { ok: false, error: HUB_PREDATES_RE.test(msg) ? OUTDATED_MESSAGE : msg };
    } finally {
      const { [key]: _, ...rest } = this.pending;
      this.pending = rest;
      void this.refresh({ fresh: true });
    }
  }

  /** Enroll this machine. The token is passed through and never stored. */
  join(url: string, token: string, name: string): Promise<HubResult<HubJoinData>> {
    return this.#act("join", () => HubService.Join({ url, token, name }));
  }

  /**
   * Create a team with the operator's create code; this Mac becomes its first
   * member and its admin. The code is passed through and never stored.
   */
  createTeam(code: string, team: string, name: string, replaceAdminKey = false): Promise<HubResult<HubCreateTeamData>> {
    return this.#act("create", () => HubService.CreateTeam({ code, team, name, replaceAdminKey }));
  }

  /** This Mac holds the joined team's admin key. The hub still decides. */
  get admin(): boolean {
    return !!this.status?.joined && !!this.status?.admin;
  }

  /** Mint one invite. It holds a one-time token: the caller shows it once. */
  invite(label: string, ttlSeconds: number): Promise<HubResult<HubInviteData>> {
    return this.#act("invite", () => HubService.Invite({ label, ttlSeconds }));
  }

  /** Read the team's settings status, or change them when a field is set. */
  teamSettings(args: HubTeamSettingsArgs, key = "settings"): Promise<HubResult<HubTeamSettingsData>> {
    return this.#act(`team:${key}`, () => HubService.TeamSettings(args));
  }

  /** Read the team's settings status. A read re-reads nothing else. */
  async readTeamSettings(teams = false): Promise<HubResult<HubTeamSettingsData>> {
    try {
      return { ok: true, data: await attempt(() => HubService.TeamSettings({ teams })) };
    } catch (err) {
      const msg = message(err);
      return { ok: false, error: HUB_PREDATES_RE.test(msg) ? OUTDATED_MESSAGE : msg };
    }
  }

  revokeDevice(name: string): Promise<HubResult<HubRevokeData>> {
    return this.#act(`revoke:${name}`, () => HubService.RevokeDevice(name));
  }

  leave(forgetKey: boolean): Promise<HubResult<HubLeaveData>> {
    return this.#act("leave", async () => {
      const d = await HubService.Leave(forgetKey);
      this.board = null;
      this.org = null;
      this.stats = null;
      return d;
    });
  }

  claim(itemId: string, project = ""): Promise<HubResult<HubClaimData>> {
    this.#claimedHere.add(itemId);
    return this.#act(`claim:${itemId}`, () => HubService.Claim({ itemId, project }));
  }

  /** Claims several advisories of one repository for ONE session ("Fix all"). */
  claimMany(key: string, itemIds: string[], project = "", scope: HubUpdateScope | null = null): Promise<HubResult<HubClaimData>> {
    for (const id of itemIds) this.#claimedHere.add(id);
    return this.#act(`claim:${key}`, () => HubService.Claim({ itemId: itemIds[0] ?? "", itemIds, project, ...(scope ? { scope } : {}) }));
  }

  release(itemId: string): Promise<HubResult<HubReleaseData>> {
    return this.#act(`release:${itemId}`, () => HubService.Release(itemId));
  }

  /**
   * "Sync now": ask the daemon for a loop pass, then keep re-reading until the
   * pass shows up — a check-in or an error newer than the press — so the
   * button can say "Synced" (or why not) instead of a bare "queued". The
   * daemon only queues the pass, so the answer is read, never assumed.
   */
  async sync(): Promise<HubResult<unknown>> {
    if (this.syncPhase === "syncing") return { ok: false, error: "a sync is already running" };
    const run = ++this.#syncRun;
    if (this.#syncDoneTimer) clearTimeout(this.#syncDoneTimer);
    const pressed = Date.now();
    const beatBefore = stamp(this.status?.lastHeartbeat);
    const r = await this.#actSync();
    if (run !== this.#syncRun) return r;
    if (!r.ok) {
      this.syncPhase = "failed";
      this.syncError = r.error;
      return r;
    }
    // The request is answered; watching for the pass runs on without the caller.
    void this.#awaitPass(run, pressed, beatBefore);
    return r;
  }

  #actSync(): Promise<HubResult<unknown>> {
    const p = this.#act("sync", () => HubService.Sync());
    // Marked syncing only once the action was let through; a refusal (a join
    // in flight) lands as "failed" with its reason.
    if (this.isBusy("sync")) {
      this.syncPhase = "syncing";
      this.syncError = "";
    }
    return p;
  }

  async #awaitPass(run: number, pressed: number, beatBefore: number) {
    const deadline = pressed + SYNC_WAIT_MS;
    while (run === this.#syncRun && Date.now() < deadline) {
      await this.refresh({ fresh: true });
      if (run !== this.#syncRun) return;
      const st = this.status;
      const beat = stamp(st?.lastHeartbeat);
      const errAt = stamp(st?.lastErrorAt);
      // An error stamped after the press, and newer than any check-in, is this pass failing.
      if (st?.lastError && errAt >= pressed - 1000 && !(beat > errAt)) {
        this.syncPhase = "failed";
        this.syncError = st.lastError;
        return;
      }
      if (beat > beatBefore) {
        this.syncPhase = "synced";
        this.#syncDoneTimer = setTimeout(() => {
          if (run === this.#syncRun) this.syncPhase = "";
        }, SYNC_DONE_MS);
        return;
      }
      await new Promise((res) => setTimeout(res, SYNC_POLL_MS));
    }
    if (run === this.#syncRun) this.syncPhase = "slow";
  }

  /** The board rows, never null. */
  get items(): HubBoardItem[] {
    return this.board?.items ?? [];
  }
}

export const hub = new HubStore();

/** A wire timestamp in epoch ms; 0 when absent or unparseable. */
function stamp(v: unknown): number {
  if (!v) return 0;
  const n = Date.parse(String(v));
  return isNaN(n) ? 0 : n;
}

/** "connected" | "error" | "waiting" for the connection pill, in plain words. */
export function connectionState(st: HubStatusData | null, now = Date.now()): { tone: "good" | "bad" | "warn"; label: string } {
  if (!st?.joined) return st?.membership === "revoked" ? { tone: "bad", label: "Removed by the hub" } : { tone: "warn", label: "Not joined" };
  const hb = st.lastHeartbeat ? Date.parse(String(st.lastHeartbeat)) : NaN;
  const errAt = st.lastErrorAt ? Date.parse(String(st.lastErrorAt)) : NaN;
  if (st.lastError && (isNaN(hb) || (!isNaN(errAt) && errAt > hb))) return { tone: "bad", label: "Sync problem" };
  if (isNaN(hb)) return { tone: "warn", label: "Connecting…" };
  // Three missed poll windows (300s default) read as out of date rather than live.
  if (now - hb > 15 * 60_000) return { tone: "warn", label: "Not heard from lately" };
  return { tone: "good", label: "Connected" };
}
