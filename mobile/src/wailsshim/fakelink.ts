// A hand-driven long-lived channel for tests: the shape the native plugin's channel has.

import { FakeChannel, type Unsubscribe } from "../wire";
import type { LinkChannel, LinkUp } from "./channeltransport";

/** A channel that outlives an epoch; the test raises up, down and migrating by hand. */
export class FakeLink extends FakeChannel implements LinkChannel {
  up: LinkUp = { epoch: 1 };
  adopted: number[] = [];
  private ups = new Set<(u: LinkUp) => void>();
  private downs = new Set<(e: Error) => void>();
  private migs = new Set<(u: LinkUp) => void>();
  onUp(l: (u: LinkUp) => void): Unsubscribe {
    this.ups.add(l);
    return () => this.ups.delete(l);
  }
  onDown(l: (e: Error) => void): Unsubscribe {
    this.downs.add(l);
    return () => this.downs.delete(l);
  }
  onMigrating(l: (u: LinkUp) => void): Unsubscribe {
    this.migs.add(l);
    return () => this.migs.delete(l);
  }
  async adopt(epoch: number): Promise<void> {
    this.adopted.push(epoch);
  }
  raiseUp(u: LinkUp) {
    this.up = u;
    for (const l of this.ups) l(u);
  }
  raiseDown(e: Error) {
    for (const l of this.downs) l(e);
  }
  raiseMigrating(u: LinkUp) {
    for (const l of this.migs) l(u);
  }
}

