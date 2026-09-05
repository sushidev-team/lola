// The bridge between the native LolaTransport plugin and the wire package's
// FrameChannel seam — the one file in the shim that knows Capacitor exists.
//
// It is deliberately the ONLY one. Everything else here is plain TypeScript
// over interfaces, which is what lets the whole request/response and pane path
// be exercised in Node against `FakeChannel`. Keeping the Capacitor import in a
// single leaf also keeps it out of the shim's barrel: a test that imported
// `./index` would otherwise pull `@capacitor/core` into a jsdom environment
// that has no bridge for it.
//
// DIVISION OF LABOUR with the plugin, which is worth stating because getting it
// wrong produces a connection that authenticates twice:
//
//   the plugin owns  TCP, TLS 1.3, the SPKI pin decision, the length prefix in
//                    both directions, the M1 BEARER HANDSHAKE, and inbound
//                    coalescing on a 16ms tick.
//   this file owns   frame bodies in and out — nothing more.
//   ChannelTransport correlation, the command allowlist, pane subscriptions and
//                    sequence-gap classification.
//
// Because the plugin completes the bearer handshake itself (its `connect` only
// resolves once the daemon has accepted the key), the transport is constructed
// with `handshake: "channel"`. Sending an in-band hello on top of that would
// arrive as an ordinary `req` naming a `remote.` command on an already
// authenticated connection — which the daemon denies, and a denied command is
// fatal there.

import { LolaTransport, type LolaFramesEvent, type LolaStateEvent } from "lola-transport";
import type { PluginListenerHandle } from "@capacitor/core";
import {
  DEFAULT_REMOTE_PORT,
  FrameMalformedError,
  decodeFrameBody,
  encodeFrameJSON,
  type ConnectOptions,
  type Endpoint,
  type Frame,
  type FrameChannel,
  type Unsubscribe,
} from "../wire";
import { ChannelTransport } from "./channeltransport";
import { refusalFromPluginError, stateError } from "./pluginerror";

const encoder = new TextEncoder();

/**
 * A FrameChannel over one plugin connection.
 *
 * Every event the plugin emits carries the `epoch` of the connection it belongs
 * to, and this channel ignores anything from another one. That is not
 * defensive tidiness: suspending an iOS app SIGSTOPs its queues, and on resume
 * a flood of callbacks queued against the previous socket is delivered before
 * anything new happens. Without the epoch filter those stale frames would be
 * routed into a fresh connection's correlator, where their ids either match
 * nothing or — far worse — match a new request by coincidence.
 */
class PluginChannel implements FrameChannel {
  private readonly frameListeners = new Set<(f: Frame) => void>();
  private readonly closeListeners = new Set<(err?: Error) => void>();
  private handles: PluginListenerHandle[] = [];
  private closed = false;

  /**
   * The connection this channel belongs to, or undefined while `connect` is
   * still in flight.
   *
   * The listeners are registered BEFORE connect is called, which is the only
   * ordering that cannot lose an event: Capacitor's `notifyListeners` does not
   * retain anything for a listener that does not exist yet, and the plugin's
   * connection is fully live — read loop armed, `phase = .connected` — the
   * instant before `connect` resolves in JavaScript. A `failed`/`closed` event
   * emitted in that window used to vanish, leaving the transport reporting
   * `ready` over a dead socket while every request rejected on its own.
   *
   * Anything arriving before the epoch is known is HELD, then reconciled against
   * it in `bind`. That is not the same as accepting it blindly: an event from a
   * previous connection is still dropped, just a moment later.
   */
  private epoch: number | undefined;
  private held: Array<LolaFramesEvent | LolaStateEvent> = [];

  /** Set when a terminal state event arrived before the channel was bound. */
  private bornDead: Error | undefined;

  /**
   * How many bridge events may be held while `connect` is in flight. The window
   * is short and the daemon writes nothing unsolicited before the client's first
   * request, so this is a bound rather than a working buffer. Overflowing drops
   * events, and the resulting sequence gap is the pane layer's own re-subscribe
   * trigger — the defined recovery rather than a silent corruption.
   */
  private static readonly maxHeld = 64;

  /**
   * Adopt the connection `connect` returned.
   *
   * Terminal state events are settled HERE, synchronously, because the answer
   * decides whether `openPluginChannel` returns a channel at all. Frame batches
   * are not: nothing has subscribed to them yet, so they wait for `flush` (see
   * `maybeFlush`). The two halves are treated differently on purpose.
   */
  bind(epoch: number): void {
    this.epoch = epoch;
    const held = this.held.filter((e) => e.epoch === epoch);
    this.held = [];
    for (const e of held) {
      if ("frames" in e) {
        this.held.push(e);
        continue;
      }
      if (e.phase === "closed" || e.phase === "failed") {
        this.bornDead = stateError(e);
        this.held = [];
        this.closed = true;
        this.detach();
        return;
      }
      // connecting / handshaking / connected carry nothing this layer acts on.
    }
  }

  /** The failure a bound channel was already in, if it never came up. */
  get failure(): Error | undefined {
    return this.bornDead;
  }

  /** Drop the listeners for a connection that never came up. */
  abandon(): void {
    this.held = [];
    this.closed = true;
    this.detach();
  }

  async wire(): Promise<void> {
    this.handles = await Promise.all([
      LolaTransport.addListener("frames", (e: LolaFramesEvent) => this.receive(e)),
      LolaTransport.addListener("state", (e: LolaStateEvent) => this.receive(e)),
    ]);
  }

  private receive(e: LolaFramesEvent | LolaStateEvent): void {
    if (this.epoch === undefined) {
      if (this.held.length < PluginChannel.maxHeld) this.held.push(e);
      else console.warn("lola: dropped a bridge event queued before the connection was bound");
      return;
    }
    if (e.epoch !== this.epoch) return;
    if ("frames" in e) this.onFrames(e);
    else this.onState(e);
  }

  /**
   * Replay the frames held during the connect window, once the consumer is
   * wired for both frames and closes.
   *
   * The condition is BOTH listeners rather than the first, because
   * `ChannelTransport.doConnect` registers `onFrame` and `onClose` in adjacent
   * statements and a replay between them would deliver a close to nobody — the
   * very failure this whole mechanism exists to remove.
   */
  private maybeFlush(): void {
    if (this.epoch === undefined || this.held.length === 0) return;
    if (this.frameListeners.size === 0 || this.closeListeners.size === 0) return;
    const held = this.held;
    this.held = [];
    for (const e of held) if ("frames" in e) this.onFrames(e);
  }

  private onFrames(e: LolaFramesEvent): void {
    for (const body of e.frames) {
      let frame: Frame;
      try {
        // Decoded through the wire package rather than a bare JSON.parse, so
        // one implementation decides what a well-formed envelope is. The extra
        // encode is trivial beside the bridge crossing that just happened.
        frame = decodeFrameBody(encoder.encode(body));
      } catch (err) {
        // A body that is not an envelope is the daemon's bug or the plugin's,
        // and there is nothing this side can do about it. Dropping one frame is
        // strictly better than tearing down a working connection, and the
        // sequence gap it creates is visible to the pane layer.
        console.error("lola: undecodable frame body", err instanceof FrameMalformedError ? err.message : err);
        continue;
      }
      for (const l of [...this.frameListeners]) {
        // One frame's routing must not cost the rest of the batch. At the
        // plugin's 16ms coalescing window a batch is routinely many frames, and
        // a throw out of the router — an invalid base64 pty payload is the
        // reachable one — would abandon every frame behind it, turning one bad
        // frame into a silently truncated pane.
        try {
          l(frame);
        } catch (err) {
          console.error("lola: frame listener threw", err);
        }
      }
    }
  }

  private onState(e: LolaStateEvent): void {
    if (e.phase !== "closed" && e.phase !== "failed") return;
    this.fail(stateError(e));
  }

  private detach(): void {
    for (const h of this.handles) void h.remove();
    this.handles = [];
  }

  private fail(err?: Error): void {
    if (this.closed) return;
    this.closed = true;
    this.detach();
    for (const l of [...this.closeListeners]) l(err);
  }

  async send(frame: Frame): Promise<void> {
    if (this.closed) throw new Error("lola: send on a closed connection");
    // One frame per call. The plugin accepts a batch and writes it in a single
    // socket write, which is worth using for a burst — but a keystroke is one
    // frame and latency matters more there than syscalls do.
    await LolaTransport.send({ frames: [encodeFrameJSON(frame)] });
  }

  onFrame(listener: (f: Frame) => void): Unsubscribe {
    this.frameListeners.add(listener);
    this.maybeFlush();
    return () => this.frameListeners.delete(listener);
  }

  onClose(listener: (err?: Error) => void): Unsubscribe {
    this.closeListeners.add(listener);
    this.maybeFlush();
    return () => this.closeListeners.delete(listener);
  }

  async close(reason?: string): Promise<void> {
    this.fail(reason ? new Error(reason) : undefined);
    await LolaTransport.disconnect({ reason }).catch(() => {});
  }
}

/** Open a plugin-backed channel for one endpoint. */
export async function openPluginChannel(
  endpoint: Endpoint,
  opts?: ConnectOptions,
): Promise<FrameChannel> {
  // AN EMPTY PIN IS REFUSED HERE, BEFORE A SOCKET EXISTS.
  //
  // The pin is the whole of the server's identity on this transport: the
  // certificate is self-signed, is in no trust store, and carries DNSNames
  // ["lola"], so system evaluation cannot succeed against a LAN address even in
  // principle. Dialling unpinned means writing the bearer key to whatever
  // answers the address — which, on DHCP, can be a machine that inherited it.
  // Accepting that has to be something a caller SAYS, not something that
  // happens when a field is empty.
  if (!endpoint.spkiPin && !opts?.allowUnpinned) {
    throw new Error(
      "lola: no certificate pin for this daemon. Paste the SPKI pin it logged at startup.",
    );
  }

  // Listeners FIRST, connect second. See the note on PluginChannel.epoch: the
  // plugin's socket is live before `connect` resolves here, and Capacitor
  // retains nothing for a listener that does not yet exist, so a refusal or a
  // close landing in that window would simply disappear.
  const channel = new PluginChannel();
  await channel.wire();

  let result: { epoch: number };
  try {
    result = await LolaTransport.connect({
      host: endpoint.host,
      port: endpoint.port ?? DEFAULT_REMOTE_PORT,
      spkiPin: endpoint.spkiPin,
      // NOT DERIVED FROM THE PIN BEING EMPTY, and that is the whole point.
      //
      // The plugin refuses to dial without either a pin or an explicit
      // `allowUnpinned`, and its own comment says why: "an omitted option is
      // indistinguishable from a typo'd one, and a check that vanishes on a
      // typo is not a check." This line used to read
      // `allowUnpinned: endpoint.spkiPin ? undefined : true`, which reinstated
      // exactly the vanishing it was written to prevent — any caller reaching
      // here with a falsy pin got trust-anything TLS and handed the bearer key
      // to whatever answered. `Endpoint.spkiPin` is a plain required `string`,
      // so `""` satisfies the type, and the only thing holding it closed was a
      // validator in another module on one of the paths in.
      //
      // So it is now a decision a caller has to state, and an empty pin is
      // refused below, in the same module as the socket, where a second caller
      // inherits the refusal instead of having to know about the validator.
      allowUnpinned: opts?.allowUnpinned || undefined,
      insecureKey: endpoint.insecureKey || undefined,
      keyRef: endpoint.keyRef || undefined,
      connectTimeoutMs: opts?.timeoutMs,
    });
  } catch (err) {
    channel.abandon(); // never came up; do not leave two listeners behind
    // A refusal the DAEMON spoke — a wrong bearer key, a version skew — is not
    // a transport failure and must not be reported as one. The plugin puts the
    // daemon's own code in the rejection's `data`, so it is readable here,
    // synchronously; the matching `state` event is still a bridge hop behind at
    // this instant and waiting for it would put a timer on every failure. See
    // pluginerror.ts.
    throw refusalFromPluginError(err) ?? err;
  }

  channel.bind(result.epoch);
  // The connection died inside the listener window. `connect` resolved because
  // the plugin's handshake had already succeeded when the socket dropped, so
  // returning the channel here would hand the transport a corpse it would report
  // as `ready`. Failing the connect is the honest answer.
  const dead = channel.failure;
  if (dead) throw dead;
  return channel;
}

/**
 * The app's one Transport.
 *
 * `main.ts` calls this once and hands the SAME instance to the shim (which
 * turns `DaemonService.Sessions()` into a req frame on it) and to the
 * connection store (which drives the connect screen and the pane streams). Two
 * instances would authenticate twice, hold two of the daemon's eight connection
 * slots, and let one show a connected UI over the other's dead pipe.
 */
export function makeTransport(): ChannelTransport {
  return new ChannelTransport({ open: openPluginChannel, handshake: "channel" });
}
