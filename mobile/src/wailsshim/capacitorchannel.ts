// The native plugin as a long-lived `LinkChannel`: the one file in the shim that
// reaches the plugin.
//
// Invariants:
//  - The plugin owns TLS, pinning, the client identity, the relay and every
//    retry. This file moves frame bodies and reports connection state, nothing
//    more; no secret ever crosses it (the plugin's own surface carries none).
//  - Every `frames` and `state` event carries the `epoch` of one connection and
//    only the current epoch is delivered. A suspended app resumes with callbacks
//    queued against the previous socket, and routing them into a fresh
//    correlator could answer a new request by coincidence.
//  - Listeners are registered before `connect` is called, because Capacitor keeps
//    nothing for a listener that does not exist yet. Events of an epoch that is
//    not yet current are held (bounded) and replayed when it becomes current.
//  - A retryable drop is `onDown`, then `onUp` with the next epoch; only a
//    terminal failure is `onClose`. While down, the plugin's own reconnect races
//    carry newer epochs: their failures still reach the channel, a retryable
//    one as another `onDown` naming its code, a terminal one as `onClose`.
//  - An epoch becomes current only on the plugin's `ready` for it. `adopt` asks
//    for a migration; the migrated socket may die before it is adopted, and then
//    the old epoch stays current.

import type { PluginListenerHandle } from "@capacitor/core";
import type { DeviceInfo, FramesEvent, PathInfo, StateEvent } from "lola-transport";
import { FrameMalformedError, decodeFrameBody, encodeFrameJSON, type Frame, type Unsubscribe } from "../wire";
import { transport } from "../native";
import { ChannelTransport, type LinkChannel, type LinkTarget, type LinkUp } from "./channeltransport";
import type { Listener } from "./panesubs";
import { LinkError, pluginFailure, stateError } from "./pluginerror";

const encoder = new TextEncoder();

/** Codes the plugin retries on its own; every other failure ends the link. */
const RETRYABLE = new Set([
  "offline",
  "unreachable",
  "timeout",
  "relay_unavailable",
  "peer_closed",
  "backgrounded",
  "key_changed",
]);

/** Frame batches held for an epoch that is not current yet. */
const MAX_HELD_BATCHES = 64;

type Plugin = typeof transport;

class PluginChannel implements LinkChannel {
  private readonly frameListeners = new Set<Listener<Frame>>();
  private readonly closeListeners = new Set<Listener<Error | undefined>>();
  private readonly upListeners = new Set<Listener<LinkUp>>();
  private readonly downListeners = new Set<Listener<Error>>();
  private readonly migratingListeners = new Set<Listener<LinkUp>>();
  private readonly deviceListeners = new Set<Listener<DeviceInfo>>();
  private handles: PluginListenerHandle[] = [];

  private epoch: number | undefined;
  private live = false;
  private closed = false;
  private device: DeviceInfo | undefined;
  private readonly held: FramesEvent[] = [];
  private readonly deviceOf = new Map<number, DeviceInfo>();
  private readonly heldStates: StateEvent[] = [];
  private bornDead: Error | undefined;
  up: LinkUp | undefined;

  constructor(private readonly plugin: Plugin) {}

  async wire(): Promise<void> {
    this.handles = await Promise.all([
      this.plugin.addListener("frames", (e) => this.onFrames(e)),
      this.plugin.addListener("state", (e) => this.onState(e)),
      this.plugin.addListener("device", (e) => this.learnDevice(e.epoch, e.device)),
    ]);
  }

  /** Adopt the connection `connect` returned; a terminal state it raced with fails it here. */
  bind(r: { epoch: number; path: PathInfo; device: DeviceInfo; raceMs?: number }): void {
    this.epoch = r.epoch;
    this.live = true;
    this.device = r.device;
    this.up = { epoch: r.epoch, path: r.path, device: r.device, raceMs: r.raceMs };
    for (const e of this.heldStates.splice(0)) {
      if (e.epoch === r.epoch && (e.phase === "failed" || e.phase === "closed")) this.bornDead = stateError(e);
    }
    this.flushHeld();
  }

  get failure(): Error | undefined {
    return this.bornDead;
  }

  abandon(): void {
    this.closed = true;
    this.held.length = 0;
    this.detach();
  }

  // --- inbound -------------------------------------------------------------

  private onFrames(e: FramesEvent): void {
    if (this.closed) return;
    const unheard = this.epoch !== undefined && e.epoch === this.epoch && this.frameListeners.size === 0;
    if (this.epoch === undefined || e.epoch > this.epoch || unheard) {
      if (this.held.length < MAX_HELD_BATCHES) this.held.push(e);
      else console.warn("lola: dropped frames held for an epoch nobody listened to");
      return;
    }
    if (e.epoch !== this.epoch) return;
    for (const body of e.frames) this.deliver(body);
  }

  private deliver(body: string): void {
    let frame: Frame;
    try {
      frame = decodeFrameBody(encoder.encode(body));
    } catch (err) {
      // One undecodable body costs one frame, never the connection; the pane layer sees the sequence gap.
      console.error("lola: undecodable frame body", err instanceof FrameMalformedError ? err.message : err);
      return;
    }
    for (const l of [...this.frameListeners]) {
      try {
        l(frame);
      } catch (err) {
        console.error("lola: frame listener threw", err);
      }
    }
  }

  /** Deliver held batches of the current epoch once someone listens; keep later epochs, drop earlier ones. */
  private flushHeld(): void {
    const current = this.epoch;
    if (current === undefined) return;
    const batches = this.held.splice(0).filter((b) => b.epoch >= current);
    const ready = this.frameListeners.size > 0;
    for (const b of batches) {
      if (b.epoch === current && ready) for (const body of b.frames) this.deliver(body);
      else this.held.push(b);
    }
  }

  private learnDevice(epoch: number, device: DeviceInfo): void {
    this.deviceOf.set(epoch, device);
    if (epoch !== this.epoch) return;
    this.device = device;
    for (const l of [...this.deviceListeners]) l(device);
  }

  private onState(e: StateEvent): void {
    if (this.closed) return;
    if (this.epoch === undefined) {
      // Before `connect` resolved: `bind` reconciles these against the epoch it was given.
      if (this.heldStates.length < MAX_HELD_BATCHES) this.heldStates.push(e);
      return;
    }
    switch (e.phase) {
      case "ready":
        if (e.epoch > this.epoch) this.promote(e.epoch, e.path, e.raceMs);
        return;
      case "migrating":
        if (e.epoch <= this.epoch) return;
        for (const l of [...this.migratingListeners]) l({ epoch: e.epoch, path: e.path, device: this.deviceOf.get(e.epoch) });
        return;
      case "racing":
        // A race while live means the drop itself was not seen.
        if (this.live) this.lose(new LinkError("peer_closed", "peer_closed: the connection dropped"));
        return;
      case "closed":
      case "failed":
        // The current epoch's end, or the end of a reconnect race after it. A
        // newer epoch ending while this one is live is a migration that died
        // before it was adopted.
        if (e.epoch === this.epoch || (!this.live && e.epoch > this.epoch)) this.end(e);
        return;
      default:
        return;
    }
  }

  private promote(epoch: number, path: PathInfo | undefined, raceMs?: number): void {
    this.epoch = epoch;
    this.live = true;
    this.device = this.deviceOf.get(epoch) ?? this.device;
    this.up = { epoch, path, device: this.device, raceMs };
    this.flushHeld();
    for (const l of [...this.upListeners]) l(this.up);
  }

  private end(e: StateEvent): void {
    const err = stateError(e);
    if (e.code && RETRYABLE.has(e.code) && !e.daemonCode) {
      this.lose(err);
      return;
    }
    this.fail(err);
  }

  /** A retryable drop, or a failed reconnect race: the plugin keeps racing. */
  private lose(err: Error): void {
    this.live = false;
    for (const l of [...this.downListeners]) l(err);
  }

  private fail(err?: Error): void {
    if (this.closed) return;
    this.closed = true;
    this.live = false;
    this.detach();
    for (const l of [...this.closeListeners]) l(err);
  }

  private detach(): void {
    for (const h of this.handles) void h.remove();
    this.handles = [];
  }

  // --- outbound ------------------------------------------------------------

  async send(frame: Frame): Promise<void> {
    if (this.closed || !this.live || this.epoch === undefined) throw new Error("lola: send on a closed connection");
    await this.plugin.send({ epoch: this.epoch, frames: [encodeFrameJSON(frame)] });
  }

  async adopt(epoch: number): Promise<void> {
    await this.plugin.adopt({ epoch });
  }

  async close(reason?: string): Promise<void> {
    this.fail(reason ? new Error(reason) : undefined);
    await this.plugin.disconnect({ reason }).catch(() => {});
  }

  // --- subscriptions -------------------------------------------------------

  onFrame(l: Listener<Frame>): Unsubscribe {
    this.frameListeners.add(l);
    if (this.epoch !== undefined) this.flushHeld();
    return () => this.frameListeners.delete(l);
  }

  onClose(l: Listener<Error | undefined>): Unsubscribe {
    this.closeListeners.add(l);
    return () => this.closeListeners.delete(l);
  }

  onUp(l: Listener<LinkUp>): Unsubscribe {
    this.upListeners.add(l);
    return () => this.upListeners.delete(l);
  }

  onDown(l: Listener<Error>): Unsubscribe {
    this.downListeners.add(l);
    return () => this.downListeners.delete(l);
  }

  onMigrating(l: Listener<LinkUp>): Unsubscribe {
    this.migratingListeners.add(l);
    return () => this.migratingListeners.delete(l);
  }

  onDevice(l: Listener<DeviceInfo>): Unsubscribe {
    this.deviceListeners.add(l);
    return () => this.deviceListeners.delete(l);
  }
}

/** Open the plugin's connection to one paired daemon; resolves when a path sent `ready`. */
export async function openPluginChannel(target: LinkTarget): Promise<LinkChannel> {
  const channel = new PluginChannel(transport);
  await channel.wire();
  try {
    channel.bind(await transport.connect({ daemonId: target.daemonId }));
  } catch (err) {
    channel.abandon();
    throw pluginFailure(err);
  }
  const dead = channel.failure;
  if (dead) {
    channel.abandon();
    throw dead;
  }
  return channel;
}

/**
 * The app's one transport. `main.ts` builds it once and hands the SAME instance
 * to the shim bridge and the connection store; two would each hold a daemon
 * connection slot and one could show a connected UI over the other's dead pipe.
 */
export function makeTransport(): ChannelTransport {
  return new ChannelTransport({ open: openPluginChannel });
}
