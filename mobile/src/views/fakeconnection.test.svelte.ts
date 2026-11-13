// A `connection` whose `ready` is a real rune.
//
// It exists because the terminal screen releases its size pin on the socket
// going away, and that is an EFFECT reading `connection.ready`. A plain getter
// over a `let` — which is what the other terminal tests mock the module with —
// is not reactive, so flipping it would change nothing and the disconnect path
// would silently go untested: the one release path whose failure leaves a
// developer's window squashed with no app on screen to explain it.
//
// Named `.test.svelte.ts` on purpose. The `.svelte.ts` half is what makes the
// Svelte plugin compile the runes in it; the `.test.` half keeps it out of the
// app the way `*.test.svelte` harnesses are kept out, and vitest's own include
// glob (`*.{test,spec}.{ts,js}`) does not match it either, so it is never
// collected as a suite.

import type { PaneEvent, PaneSubscription, Viewport } from "@mobile/wire";

/** The little of a subscription MobileTerminal actually touches. */
export class FakeSubscription {
  readonly pane: string;
  readonly id = "sub-1";
  /** The first resync, delivered as the acknowledgement. See MobileTerminal. */
  screen: unknown;
  lastSeq = 0;
  exited = false;
  closed = false;

  #events: ((e: PaneEvent) => void)[] = [];

  constructor(pane: string, screen: unknown) {
    this.pane = pane;
    this.screen = screen;
  }

  async write(): Promise<void> {}
  async resize(): Promise<void> {}
  async scroll(): Promise<void> {}
  async close(): Promise<void> {
    this.closed = true;
  }
  onEvent(cb: (e: PaneEvent) => void): () => void {
    this.#events.push(cb);
    return () => {
      this.#events = this.#events.filter((f) => f !== cb);
    };
  }
  onError(): () => void {
    return () => {};
  }
  /** Push an event at the terminal, the way the daemon's bus would. */
  emit(e: PaneEvent): void {
    for (const cb of [...this.#events]) cb(e);
  }
}

class FakeConnection {
  /** The rune the screen's pin effect watches. */
  ready = $state(true);
  /** Every subscription handed out, newest last. */
  subs: FakeSubscription[] = [];
  /** The screen every new subscription acknowledges with. */
  screen: unknown = null;

  subscribe(pane: string, _viewport?: Viewport): Promise<PaneSubscription> {
    const s = new FakeSubscription(pane, this.screen);
    this.subs.push(s);
    return Promise.resolve(s as unknown as PaneSubscription);
  }

  /** The subscription for a pane, or the last one handed out. */
  sub(pane?: string): FakeSubscription | undefined {
    if (!pane) return this.subs[this.subs.length - 1];
    return [...this.subs].reverse().find((s) => s.pane === pane);
  }

  reset(): void {
    this.ready = true;
    this.subs = [];
    this.screen = null;
  }
}

export const connection = new FakeConnection();
