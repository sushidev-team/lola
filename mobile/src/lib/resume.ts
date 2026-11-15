// What happens when a connection becomes ready: the epoch is the unit of resume.
//
// Invariant: nothing survives an epoch. When a connection ends every watch is
// returned to idle; when the next one is ready (first connect, a reconnect the
// plugin made on its own, or an adopted migration) every topic that still has a
// subscriber is watched again in one request and every bound pane re-subscribes
// with its current viewport. Resets replace consumer state; nothing merges
// across epochs. Requests in flight on the ended epoch are the transport's
// concern (read-tier re-sent once, everything else rejected).

import type { ShimTransport } from "../wailsshim/channeltransport";
import { bridge } from "../wailsshim/bridge";
import { watches } from "../wailsshim/watch";
import type { Unsubscribe } from "../wire";

export interface ResumeTargets {
  watches: { reset(): void; resume(): void };
  bridge: { onEpoch(): Promise<void> };
}

/** Follow `t` and resume the watch registry and the pane bindings on every new epoch. */
export function installResume(t: ShimTransport, targets: ResumeTargets = { watches, bridge }): Unsubscribe {
  let epoch = 0;
  const apply = (s: ShimTransport["status"]) => {
    if (s.phase !== "ready") {
      if (epoch !== 0) targets.watches.reset();
      epoch = 0;
      return;
    }
    if (s.epoch === epoch) return;
    epoch = s.epoch;
    targets.watches.resume();
    void targets.bridge.onEpoch();
  };
  apply(t.status);
  return t.onStatus(apply);
}
