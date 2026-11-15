// The phones tab's client state: the paired-device list, and one PairSession
// per open pairing dialog. Every daemon call goes through PhonesService.
//
// Rules this file keeps:
//   - A PairSession owns its pairing id for its whole life. Confirm and Reject
//     send THAT id, never one read from shared state, so a second Begin
//     (another dialog, a "New code") cannot redirect a confirmation to a
//     pairing the human never looked at. The daemon refuses a stale id.
//   - The confirmation code is an argument of confirm() and is never kept.
//     Nothing here derives it: the human types what the phone shows.
//   - The QR payload lives on the session and nowhere else; it is never logged.
//   - The device list polls only while the tab is on screen (`watch()`).

import { PhonesService } from "@bindings/desktop";
import type { DeviceInfo, PairStatusData } from "@bindings/internal/protocol";

/** Cadence of the device list while the Phones tab is visible. */
export const PHONE_POLL_MS = 5_000;
/** One pairing long-poll; the daemon clamps it. */
export const PAIR_WAIT_MS = 25_000;
/** Pause after a failed status read, so a down daemon is not hammered. */
const PAIR_RETRY_MS = 1_000;

/** Tier rows in display order. Read is the floor and cannot be removed. */
export const TIER_ROWS: readonly { id: string; label: string; locked?: boolean }[] = [
  { id: "read", label: "Read", locked: true },
  { id: "answer", label: "Answer" },
  { id: "control", label: "Control" },
  { id: "destructive", label: "Remove sessions" },
  { id: "shell", label: "Terminal input" },
];

/** What a phone gets when the human changes nothing in the dialog. */
export const DEFAULT_PAIR_TIERS: readonly string[] = TIER_ROWS.map((t) => t.id);

/** `tiers` with `id` switched on or off; read is always kept, order is canonical. */
export function withTier(tiers: readonly string[], id: string, on: boolean): string[] {
  const set = new Set(tiers);
  if (on) set.add(id);
  else set.delete(id);
  set.add("read");
  return TIER_ROWS.map((t) => t.id).filter((t) => set.has(t));
}

const TERMINAL = new Set(["paired", "rejected", "expired", "failed", "cancelled"]);

/** Whether a pairing state ends the pairing. */
export function pairEnded(state: string): boolean {
  return TERMINAL.has(state);
}

function message(err: unknown): string {
  const s = err instanceof Error ? err.message : String(err);
  return s.replace(/^Error:\s*/, "");
}

export class PhonesStore {
  devices = $state<DeviceInfo[]>([]);
  /** The daemon's words for a registry it cannot read. */
  problem = $state("");
  error = $state("");
  loaded = $state(false);
  /** Device ids with a change in flight. */
  busy = $state<Record<string, boolean>>({});

  #seq = 0;
  #applied = 0;
  #watchers = 0;
  #timer: ReturnType<typeof setInterval> | null = null;

  async refresh() {
    const seq = ++this.#seq;
    try {
      const d = await PhonesService.Devices();
      if (seq < this.#applied) return;
      this.#applied = seq;
      this.devices = d.devices ?? [];
      this.problem = d.problem ?? "";
      this.error = "";
    } catch (e) {
      if (seq < this.#applied) return;
      this.#applied = seq;
      this.error = message(e);
    } finally {
      this.loaded = true;
    }
  }

  /** Poll while a view shows the list. Returns the release. */
  watch(): () => void {
    this.#watchers++;
    if (!this.#timer) this.#timer = setInterval(() => void this.refresh(), PHONE_POLL_MS);
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

  async setTiers(id: string, tiers: string[]) {
    await this.#change(id, async () => {
      const next = await PhonesService.SetTiers(id, tiers);
      this.devices = this.devices.map((d) => (d.id === id ? next : d));
    });
  }

  async revoke(id: string) {
    await this.#change(id, async () => {
      await PhonesService.Revoke(id);
      this.devices = this.devices.filter((d) => d.id !== id);
    });
  }

  async #change(id: string, run: () => Promise<void>) {
    if (this.busy[id]) return;
    this.busy[id] = true;
    this.error = "";
    try {
      await run();
    } catch (e) {
      this.error = message(e);
    } finally {
      delete this.busy[id];
      void this.refresh();
    }
  }
}

export const phones = new PhonesStore();

/** One pairing, from Begin to a terminal state. A dialog makes one per open. */
export class PairSession {
  /** The pairing id this session began with; never reassigned. */
  pairing = $state("");
  payload = $state("");
  expiresAt = $state("");
  /** The daemon's reason for not opening a window (listener off, nothing bound). */
  problem = $state("");
  status = $state<PairStatusData | null>(null);
  /** The last refused confirm or failed read, in the daemon's words. */
  error = $state("");
  busy = $state(false);
  started = $state(false);

  #closed = false;

  /** waiting until the daemon says otherwise. */
  get state(): string {
    return this.status?.state ?? "waiting";
  }

  get ended(): boolean {
    return pairEnded(this.state);
  }

  async begin() {
    try {
      const d = await PhonesService.Begin();
      if (this.#closed) return;
      if (d.problem) {
        this.problem = d.problem;
      } else {
        this.pairing = d.pairing;
        this.payload = d.payload;
        this.expiresAt = d.expiresAt ?? "";
      }
    } catch (e) {
      if (this.#closed) return;
      this.problem = message(e);
    }
    this.started = true;
    if (this.pairing) void this.#poll();
  }

  async #poll() {
    let since = "waiting";
    while (!this.#closed) {
      try {
        const s = await PhonesService.Status({ pairing: this.pairing, since, waitMs: PAIR_WAIT_MS });
        if (this.#closed) return;
        this.status = s;
        since = s.state;
        if (pairEnded(s.state)) return;
      } catch (e) {
        if (this.#closed) return;
        this.error = message(e);
        await new Promise((r) => setTimeout(r, PAIR_RETRY_MS));
      }
    }
  }

  /** Accept the waiting phone with the digits the human typed from it. */
  async confirm(code: string, tiers: string[]) {
    if (this.busy || !this.pairing) return;
    this.busy = true;
    this.error = "";
    try {
      this.status = await PhonesService.Confirm({ pairing: this.pairing, code, tiers });
    } catch (e) {
      this.error = message(e);
      // A wrong code leaves the pairing in confirming with fewer tries; read it.
      try {
        const s = await PhonesService.Status({ pairing: this.pairing });
        if (!this.#closed) this.status = s;
      } catch {
        /* the refusal above is the message */
      }
    } finally {
      this.busy = false;
    }
  }

  /** Stop watching; refuse the phone when the pairing is still open. */
  async close() {
    const open = this.pairing && !this.#closed && !this.ended;
    this.#closed = true;
    if (!open) return;
    try {
      await PhonesService.Reject(this.pairing);
    } catch {
      /* the window expires on its own */
    }
  }
}
