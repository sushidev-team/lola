// The connection as the UI sees it: phase, path, device and one diagnosis.
//
// Invariants:
//  - This module never builds a transport. `main.ts` creates the app's one
//    `ChannelTransport` and hands the SAME instance to the shim and here; two
//    would hold two of the daemon's connection slots and one could show a
//    connected UI over the other's dead pipe.
//  - Nothing that identifies or authenticates a daemon lives here or in
//    localStorage: the pairing record, key and relay credential are native. This
//    store holds a daemon's public id and display name only.
//  - Once connected, reconnecting and path migration are the plugin's; the epoch
//    changes and `lib/resume.ts` repairs the streams. This store only retries a
//    FIRST connect that failed, on a short ladder that ends.
//  - A deliberate `disconnect` is never undone by a foreground or a retry.

import type { DeviceInfo, FailureCode, PairedDaemon, PathInfo } from "lola-transport";
import type { PaneSubscription, Unsubscribe, Viewport } from "@mobile/wire";
import { transport as nativeTransport } from "../native";
import type { LinkPhase, LinkStatus, ShimTransport } from "../wailsshim/channeltransport";
import { diagnose, type Diagnosis } from "./diagnose";
import { tiers } from "./tiers.svelte";

type Refusal = NonNullable<LinkStatus["refusal"]>;

export class Connection {
  phase = $state<LinkPhase>("idle");
  /** Changes exactly when a new connection became ready. */
  epoch = $state(0);
  path = $state<PathInfo | null>(null);
  /** The last path race's duration, in ms; null until a connection reported one. */
  raceMs = $state<number | null>(null);
  device = $state<DeviceInfo | null>(null);
  failure = $state<FailureCode | null>(null);
  refusal = $state<Refusal | null>(null);
  error = $state<Error | null>(null);
  daemons = $state<PairedDaemon[]>([]);
  daemonId = $state("");
  /** True while a connect the app started is in flight. */
  busy = $state(false);
  /** True while an automatic retry of a failed first connect is in flight. */
  reconnecting = $state(false);

  /** The paired Mac's name, else a phrase. */
  label = $derived(this.daemons.find((d) => d.daemonId === this.daemonId)?.name || "your Mac");
  ready = $derived(this.phase === "ready");
  diagnosis = $derived<Diagnosis>(
    diagnose({
      phase: this.phase,
      failure: this.failure,
      refusal: this.refusal,
      label: this.label,
      relay: this.daemons.find((d) => d.daemonId === this.daemonId)?.relay,
    }),
  );

  #transport: ShimTransport | undefined;
  #offStatus: Unsubscribe | undefined;
  #userClosed = false;
  #retryTimer: ReturnType<typeof setTimeout> | undefined;
  #retryStep = 0;
  #arrived!: () => void;
  #transportArrived = new Promise<void>((resolve) => {
    this.#arrived = resolve;
  });

  /** The ladder for a failed first connect, then silence; a foreground or pull starts a new one. */
  static readonly RETRY_DELAYS_MS: readonly number[] = [2000, 5000, 15000];

  get transport(): ShimTransport | undefined {
    return this.#transport;
  }

  /** Adopt the app's one transport; safe to call again with the same instance. */
  useTransport(t: ShimTransport): void {
    this.#offStatus?.();
    this.#transport = t;
    this.#arrived();
    this.#adopt(t.status);
    this.#offStatus = t.onStatus((s) => this.#adopt(s));
  }

  #adopt(s: LinkStatus): void {
    this.phase = s.phase;
    this.epoch = s.epoch;
    this.path = s.path ?? null;
    if (s.phase === "ready" && s.raceMs !== undefined) this.raceMs = s.raceMs;
    this.device = s.device ?? null;
    this.failure = s.failure ?? null;
    this.refusal = s.refusal ?? null;
    this.error = s.error ?? null;
  }

  /** A connect at launch can land before `main.ts`'s dynamic import resolved the transport; wait, boundedly. */
  async #awaitTransport(ms = 4000): Promise<ShimTransport | undefined> {
    if (this.#transport) return this.#transport;
    let timer: ReturnType<typeof setTimeout> | undefined;
    await Promise.race([this.#transportArrived, new Promise<void>((r) => (timer = setTimeout(r, ms)))]);
    clearTimeout(timer);
    return this.#transport;
  }

  /** Read the paired daemons from the plugin and pick the most recently paired. Never throws. */
  async refresh(): Promise<PairedDaemon[]> {
    try {
      this.daemons = (await nativeTransport.daemons()).daemons;
    } catch {
      this.daemons = [];
    }
    const known = this.daemons.some((d) => d.daemonId === this.daemonId);
    if (!known) {
      const latest = [...this.daemons].sort((a, b) => b.pairedAt - a.pairedAt)[0];
      this.daemonId = latest?.daemonId ?? "";
    }
    return this.daemons;
  }

  /** Connect to a paired daemon (the current one by default). True when it is ready. */
  async connect(daemonId = this.daemonId): Promise<boolean> {
    if (daemonId === "") return false;
    this.#userClosed = false;
    this.daemonId = daemonId;
    const t = await this.#awaitTransport();
    if (!t) {
      this.phase = "closed";
      this.error = new Error("No transport is installed in this build.");
      return false;
    }
    if (this.busy) return false;
    this.busy = true;
    try {
      await t.connect({ daemonId });
      const { device, epoch } = t.status;
      if (device) tiers.set(device.tiers, epoch);
      this.#retryStep = 0;
      return true;
    } catch {
      // The status listener has already recorded why.
      return false;
    } finally {
      this.busy = false;
    }
  }

  /** Launch: find the paired daemon and connect. A failed first connect arms the retry ladder. */
  async boot(): Promise<boolean> {
    await this.refresh();
    if (this.daemonId === "") return false;
    const ok = await this.connect();
    if (!ok && this.diagnosis.retryable) this.retryLater();
    return ok;
  }

  /** The app returned to the foreground or the person pulled to refresh. True when connected. */
  async reconnect(): Promise<boolean> {
    this.#cancelRetry();
    this.#retryStep = 0;
    return this.#attempt();
  }

  /** Arm the ladder without dialling now, for a caller that just failed an attempt of its own. */
  retryLater(): void {
    if (this.#userClosed) return;
    this.#cancelRetry();
    this.#retryStep = 0;
    this.#schedule();
  }

  async #attempt(): Promise<boolean> {
    if (this.#userClosed || this.busy || this.reconnecting) return false;
    // Once up the plugin owns reconnecting; only a link that is down needs this.
    if (this.phase === "ready" || this.phase === "connecting") return false;
    if (this.daemonId === "") await this.refresh();
    if (this.daemonId === "") return false;
    this.reconnecting = true;
    let ok = false;
    try {
      ok = await this.connect();
    } finally {
      this.reconnecting = false;
    }
    if (!ok && this.diagnosis.retryable) this.#schedule();
    return ok;
  }

  #schedule(): void {
    const delay = Connection.RETRY_DELAYS_MS[this.#retryStep];
    if (delay === undefined) return;
    this.#retryStep += 1;
    this.#retryTimer = setTimeout(() => {
      this.#retryTimer = undefined;
      void this.#attempt();
    }, delay);
  }

  #cancelRetry(): void {
    clearTimeout(this.#retryTimer);
    this.#retryTimer = undefined;
  }

  /** Close the link. Also what backgrounding does natively; this one is the person's choice and sticks. */
  async disconnect(reason = "user"): Promise<void> {
    this.#userClosed = true;
    this.#cancelRetry();
    try {
      await this.#transport?.disconnect(reason);
    } catch {
      // A disconnect that fails has still stopped being usable.
    }
  }

  /** Unpair: the plugin deletes key, certificate, pairing record and relay credential. */
  async forget(daemonId = this.daemonId): Promise<void> {
    if (daemonId === "") return;
    await this.disconnect("forgotten");
    await nativeTransport.forget({ daemonId });
    tiers.clear();
    await this.refresh();
  }

  /** Rename the daemon on this phone only. */
  async rename(name: string, daemonId = this.daemonId): Promise<void> {
    await nativeTransport.rename({ daemonId, name });
    await this.refresh();
  }

  subscribe(pane: string, viewport?: Viewport): Promise<PaneSubscription> {
    const t = this.#transport;
    return t ? t.subscribe(pane, viewport) : Promise.reject(new Error("not connected"));
  }

  request<T = unknown>(cmd: string, fields?: Record<string, unknown>): Promise<T> {
    const t = this.#transport;
    return t ? t.request<T>(cmd, fields as never) : Promise.reject(new Error("not connected"));
  }
}

export const connection = new Connection();
