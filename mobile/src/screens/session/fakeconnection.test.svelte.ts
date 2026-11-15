// A `connection` whose `ready` is a real rune, so a test can flip it and the
// terminal segment's effects (pin release on disconnect, reassert on return)
// actually run. Named `.test.svelte.ts` so the Svelte plugin compiles the rune
// while vitest's include glob never collects it as a suite.

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
