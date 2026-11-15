// One attached pane: the routing, replay and gap rules of a pane subscription.
//
// Invariants:
//  - Events that arrive before the first listener are HELD, then replayed in
//    order. The acknowledging resync is the subscription's return value (read
//    off `screen`), so it is the one frame never replayed. Dropping stream bytes
//    in that window blanks a pane that an idle agent will not repaint for
//    minutes.
//  - A resync gap is self-healing (the daemon repairs it with a fresh screen);
//    only a gap on a `pty` frame means torn bytes and calls for a re-subscribe.
//  - Every `emit` goes through the replay buffer; a direct fan-out is the
//    blank-pane bug.

import {
  FRAME_ERR,
  FRAME_PTY,
  FRAME_RESYNC,
  WireError,
  WireRefusalError,
  base64ToBytes,
  bytesToBase64,
  classifyGap,
  ptyResizeFrame,
  ptyScrollFrame,
  ptyWriteFrame,
  unsubFrame,
  type Frame,
  type PaneEvent,
  type PaneSubscription,
  type ResyncPayload,
  type Unsubscribe,
} from "../wire";

export type Listener<T> = (v: T) => void;

/** Call every listener; one throwing listener never stops the rest. */
export function fanout<T>(set: Set<Listener<T>>, v: T, what: string): void {
  for (const l of [...set]) {
    try {
      l(v);
    } catch (err) {
      console.error(`wailsshim: ${what} listener threw`, err);
    }
  }
}

/** What a subscription needs from the transport that owns it. */
export interface PaneOwner {
  retire(pane: string): void;
  sendFrame(f: Frame): Promise<void>;
}

/** Past this many held events the oldest is dropped and the next delivery is marked torn. */
const REPLAY_MAX = 256;

export class ChannelPaneSubscription implements PaneSubscription {
  readonly pane: string;
  readonly id: string;
  screen: ResyncPayload | null = null;
  lastSeq = 0;
  exited = false;

  private readonly events = new Set<Listener<PaneEvent>>();
  private readonly errors = new Set<Listener<WireRefusalError>>();
  private replay: PaneEvent[] | null = [];
  private replayTorn = false;
  private acked = false;

  constructor(
    pane: string,
    id: string,
    private readonly owner: PaneOwner,
  ) {
    this.pane = pane;
    this.id = id;
  }

  onEvent(l: Listener<PaneEvent>): Unsubscribe {
    this.events.add(l);
    this.drainReplay();
    return () => this.events.delete(l);
  }

  /** Hand the first listener what arrived before it existed, once; a second reader gets the live stream only. */
  private drainReplay(): void {
    const held = this.replay;
    if (held === null) return;
    this.replay = null;
    const torn = this.replayTorn;
    this.replayTorn = false;
    for (let i = 0; i < held.length; i++) {
      const e = held[i];
      fanout(this.events, torn && i === 0 && e.kind === "output" ? { ...e, gap: true } : e, "pane");
    }
  }

  private emit(e: PaneEvent): void {
    if (this.replay === null) {
      fanout(this.events, e, "pane");
      return;
    }
    if (this.replay.length >= REPLAY_MAX) {
      this.replay.shift();
      this.replayTorn = true;
    }
    this.replay.push(e);
  }

  onError(l: Listener<WireRefusalError>): Unsubscribe {
    this.errors.add(l);
    return () => this.errors.delete(l);
  }

  /** Route one frame addressed to this pane, the acknowledging resync included. */
  handle(f: Frame): void {
    if (f.type === FRAME_RESYNC) {
      const verdict = classifyGap(this.lastSeq, f);
      const screen = (f.payload ?? {}) as ResyncPayload;
      this.screen = screen;
      if (typeof f.seq === "number" && f.seq > 0) this.lastSeq = f.seq;
      const ack = !this.acked;
      this.acked = true;

      if (screen.exited) {
        // The daemon drops its authorization for the pane before writing the
        // exit, so anything typed afterwards is refused: retire it here too.
        this.exited = true;
        this.owner.retire(this.pane);
        this.emit({ kind: "exit", screen, seq: this.lastSeq });
        return;
      }
      if (ack) return;
      this.emit({ kind: "resync", screen, seq: this.lastSeq, repaired: verdict === "repaired" });
      return;
    }

    if (f.type === FRAME_PTY) {
      const verdict = classifyGap(this.lastSeq, f);
      const payload = (f.payload ?? {}) as { data?: string | null };
      // Go marshals a nil []byte as null, so "" and null are both well formed.
      const data = base64ToBytes(payload.data);
      if (typeof f.seq === "number" && f.seq > 0) this.lastSeq = f.seq;
      this.emit({ kind: "output", data, seq: this.lastSeq, gap: verdict === "torn" });
      return;
    }

    if (f.type === FRAME_ERR) {
      fanout(this.errors, WireRefusalError.fromFrame(f), "pane error");
    }
  }

  /** Report a connection-level failure to this pane's error listeners. */
  fail(err: WireRefusalError): void {
    fanout(this.errors, err, "pane error");
  }

  async write(data: Uint8Array | string): Promise<void> {
    if (this.exited) throw new WireError(`wire: pane ${this.pane} has exited`);
    const bytes = typeof data === "string" ? new TextEncoder().encode(data) : data;
    if (bytes.length === 0) return;
    // No correlation id: a successful write is never acknowledged, and a refusal
    // echoes the pane, which is all the routing it needs.
    await this.owner.sendFrame(ptyWriteFrame(this.pane, bytesToBase64(bytes)));
  }

  async scroll(lines: number): Promise<void> {
    if (this.exited) return;
    if (!Number.isFinite(lines) || Math.trunc(lines) === 0) return;
    await this.owner.sendFrame(ptyScrollFrame(this.pane, Math.trunc(lines)));
  }

  async resize(cols: number, rows: number): Promise<void> {
    if (this.exited) return;
    await this.owner.sendFrame(ptyResizeFrame(this.pane, cols, rows));
  }

  async close(): Promise<void> {
    this.owner.retire(this.pane);
    if (this.exited) return;
    // Unacknowledged by design: there is no reply to wait for.
    await this.owner.sendFrame(unsubFrame(this.pane));
  }
}
