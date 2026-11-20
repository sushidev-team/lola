// Team session sharing's client state (docs/team-sessions.md): the teammates a
// person can pin, the sessions teammates offer this Mac, and the changes in
// flight. Every daemon call goes through HubService (one socket round-trip per
// method); components read and call.
//
// Rules this store keeps:
//   - It POLLS only while a view shows it (`watch()`); the share control on a
//     session reads once when it opens.
//   - The daemon is the authority. A pin is re-checked there against the key the
//     hub names at that moment, so a refusal comes back in the daemon's words and
//     is shown as is; nothing here decides who may attach.
//   - The fingerprint a person compared is an argument of trust() and is not
//     kept. Nothing here derives or compares it to decide anything.
//   - Busy is per teammate or session, so one change never greys out another.

import { HubService } from "@bindings/desktop";
import type { ShareMemberInfo, ShareOffer, ShareSessionArgs, ShareSessionData } from "@bindings/internal/protocol";

/** Cadence while a view that shows teammates or offers is on screen. */
export const SHARE_POLL_MS = 8_000;

function message(err: unknown): string {
  const s = err instanceof Error ? err.message : String(err);
  return s.replace(/^Error:\s*/, "");
}

/** An older daemon predates sharing: one sentence, not a raw "unknown cmd". */
export const SHARE_OUTDATED_MESSAGE = "Update the lola daemon to share sessions";
const OUTDATED_RE = /unknown cmd/i;

/** Call fn as a promise: a throw rejects instead of escaping. */
function attempt<T>(fn: () => Promise<T>): Promise<T> {
  return Promise.resolve().then(fn);
}

export class ShareStore {
  members = $state<ShareMemberInfo[]>([]);
  /** The hub could not be asked; members then holds only the pinned ones. */
  membersOffline = $state(false);
  offers = $state<ShareOffer[]>([]);
  offersOffline = $state(false);
  loaded = $state(false);
  error = $state("");
  /** Keys with a change in flight: a teammate's device id or a session id. */
  busy = $state<Record<string, boolean>>({});
  /** The share id of the teammate's session shown in the terminal pane ("" = none). */
  attachedId = $state("");

  #seq = 0;
  #applied = 0;
  /** Reads started at or before this were begun before a successful pin change and must not land. */
  #floor = 0;
  #watchers = 0;
  #timer: ReturnType<typeof setInterval> | null = null;

  #fail(e: unknown) {
    const m = message(e);
    this.error = OUTDATED_RE.test(m) ? SHARE_OUTDATED_MESSAGE : m;
  }

  /** Reads teammates and offers together; one failing read never blanks the other. */
  async refresh() {
    const seq = ++this.#seq;
    // A binding missing in an older build rejects here instead of escaping.
    const [m, o] = await Promise.allSettled([attempt(() => HubService.ShareMembers()), attempt(() => HubService.ShareOffered())]);
    if (seq < this.#applied || seq <= this.#floor) return;
    this.#applied = seq;
    this.error = "";
    if (m.status === "fulfilled") {
      this.members = m.value.members ?? [];
      this.membersOffline = !!m.value.offline;
    } else this.#fail(m.reason);
    if (o.status === "fulfilled") {
      this.offers = o.value.offers ?? [];
      this.offersOffline = !!o.value.offline;
    } else if (!this.error) this.#fail(o.reason);
    this.loaded = true;
  }

  /** Show a teammate's session in the terminal pane. */
  attach(shareId: string) {
    this.attachedId = shareId;
  }

  /** Leave the terminal pane. The terminal detaches itself as it unmounts. */
  detach() {
    this.attachedId = "";
  }

  /** Poll while a view shows sharing. Returns the release. */
  watch(): () => void {
    this.#watchers++;
    if (!this.#timer) this.#timer = setInterval(() => void this.refresh(), SHARE_POLL_MS);
    void this.refresh();
    let released = false;
    return () => {
      if (released) return;
      released = true;
      if (--this.#watchers <= 0 && this.#timer) {
        clearInterval(this.#timer);
        this.#timer = null;
        this.#watchers = 0;
      }
    };
  }

  get polling(): boolean {
    return this.#timer !== null;
  }

  async #change<T>(key: string, run: () => Promise<T>): Promise<T | undefined> {
    if (this.busy[key]) return undefined;
    this.busy[key] = true;
    this.error = "";
    try {
      return await run();
    } catch (e) {
      this.#fail(e);
      return undefined;
    } finally {
      delete this.busy[key];
    }
  }

  /** Turn sharing of one session on or off. Resolves to the daemon's answer, or undefined on a refusal (see `error`). */
  setShared(args: ShareSessionArgs): Promise<ShareSessionData | undefined> {
    return this.#change(args.session, () => HubService.SetShared(args));
  }

  /** Pin a teammate in the directions chosen, after the person compared `fingerprint`. */
  async trust(deviceId: string, fingerprint: string, dir: { allow?: boolean; trust?: boolean }): Promise<boolean> {
    const r = await this.#change(deviceId, () => HubService.ShareTrust({ deviceId, fingerprint, allow: !!dir.allow, trust: !!dir.trust }));
    if (!r) return false;
    this.#floor = this.#seq;
    this.members = r.members ?? [];
    this.membersOffline = !!r.offline;
    void this.refresh();
    return true;
  }

  /** Remove a teammate in the directions named (neither: both). */
  async untrust(deviceId: string, dir: { allow?: boolean; trust?: boolean } = {}): Promise<boolean> {
    const r = await this.#change(deviceId, () => HubService.ShareUntrust({ deviceId, allow: !!dir.allow, trust: !!dir.trust }));
    if (!r) return false;
    this.#floor = this.#seq;
    this.members = r.members ?? [];
    this.membersOffline = !!r.offline;
    void this.refresh();
    return true;
  }
}

export const share = new ShareStore();
