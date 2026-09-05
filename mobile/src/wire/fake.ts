// An in-memory FrameChannel, for tests and for developing the app with no
// daemon in reach.
//
// It is shipped rather than kept inside a test file because three different
// pieces need one: this package's own correlator tests, the wailsshim's event
// synthesis (there is no server push in M1, so `daemon:sessions` has to be
// produced by polling `cmd=sessions`, and testing that needs a fake daemon), and
// any view that wants to be developed against a scripted pane.
//
// It deliberately does NOT emulate the daemon's policy. It will happily carry a
// denied command or a frame travelling the wrong way, because a test that wants
// to see the client refuse one has to be able to send it.

import type { Frame } from "./protocol";
import type { FrameChannel, Unsubscribe } from "./transport";

/** A recorded exchange: what the client sent, in order. */
export class FakeChannel implements FrameChannel {
  /** Every frame the client has sent, in order. Assert against this. */
  readonly sent: Frame[] = [];

  private frameListeners = new Set<(f: Frame) => void>();
  private closeListeners = new Set<(err?: Error) => void>();
  private closed = false;

  /**
   * Called for every outbound frame, after it is recorded. The usual shape is a
   * scripted daemon: look at the frame, call `deliver` with the reply.
   */
  onSend?: (f: Frame, ch: FakeChannel) => void;

  /** A rejection here surfaces as a failed request rather than a thrown send. */
  sendError?: Error;

  send(frame: Frame): void {
    if (this.closed) throw new Error("fake channel: send after close");
    if (this.sendError) throw this.sendError;
    // Recorded by value, so a caller mutating the frame afterwards cannot
    // rewrite history — the daemon has the bytes by then.
    this.sent.push(structuredCloneish(frame));
    this.onSend?.(frame, this);
  }

  /** Push one inbound frame at the client. */
  deliver(frame: Frame): void {
    for (const l of [...this.frameListeners]) l(frame);
  }

  onFrame(listener: (f: Frame) => void): Unsubscribe {
    this.frameListeners.add(listener);
    return () => this.frameListeners.delete(listener);
  }

  onClose(listener: (err?: Error) => void): Unsubscribe {
    this.closeListeners.add(listener);
    return () => this.closeListeners.delete(listener);
  }

  close(reason?: string): void {
    if (this.closed) return;
    this.closed = true;
    const err = reason ? new Error(reason) : undefined;
    for (const l of [...this.closeListeners]) l(err);
  }

  /** The last frame sent, which is what most assertions want. */
  get last(): Frame | undefined {
    return this.sent[this.sent.length - 1];
  }
}

/**
 * A structured clone that works in jsdom, in Node and in a WebView without
 * depending on which of them has globalThis.structuredClone. The frames here are
 * plain JSON, so a JSON round trip is a faithful copy.
 */
function structuredCloneish<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}
