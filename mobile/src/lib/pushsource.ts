// The phone's `PushSource`: the desktop stores' push seam implemented over the
// watch registry.
//
// Invariants: a session topic is watched only while a store follows it, so only
// while a component that needs it is mounted. A refused topic reaches the store's
// `onError` and the store falls back to its own polling. A timeline reset marked
// `older` is completed by paging `sessionTimeline` up to its lastSeq.

import type { SSHTimelineData, TalkStateData } from "@bindings/internal/protocol";
import type { FeedPush, PushError, PushSource } from "$lib/pushsource";
import { bridge } from "../wailsshim/bridge";
import { watch, type Topic, type WatchCode } from "../wailsshim/watch";
import type { DiffEvent, FeedEvent, TimelineEvent } from "../wire";

function refused(onError: (e: PushError) => void): (code: WatchCode) => void {
  return (code) => onError(code);
}

export const mobilePush: PushSource = {
  talk(session, on, onError) {
    return watch(`talk:${session}` as Topic, {
      on: (p) => on(p as TalkStateData),
      onError: refused(onError),
    });
  },

  feed(session, on, onError) {
    return watch(`feed:${session}` as Topic, {
      on: (p, reset) => {
        const e = p as FeedEvent;
        const push: FeedPush = {
          reset,
          older: e.older === true,
          epoch: e.epoch,
          lastSeq: e.lastSeq,
          items: e.items,
          scanning: e.scanning === true,
          health: e.health,
        };
        on(push);
      },
      onError: refused(onError),
    });
  },

  timeline(session, on, onError) {
    // Bumped by every reset and by the unsubscribe, so a page walk for an older snapshot stops.
    let generation = 0;
    const stop = watch(`timeline:${session}` as Topic, {
      on: (p, reset) => {
        const e = p as TimelineEvent;
        if (reset) generation += 1;
        // null means unchanged: the view keeps its set, and nothing that reads it re-runs.
        on({ session: e.session, items: e.items, lastSeq: e.lastSeq, connections: e.connections ?? null }, reset);
        if (reset && e.older) void pageOlder(session, e.lastSeq, generation);
      },
      onError: refused(onError),
    });

    // A reset with `older` is a newest window: page `sessionTimeline` from 0 up to its lastSeq as deltas.
    async function pageOlder(id: string, upTo: number, gen: number): Promise<void> {
      let after = 0;
      while (gen === generation && after < upTo) {
        let page: SSHTimelineData;
        try {
          page = await bridge.request<SSHTimelineData>("SSHService.Timeline", "sessionTimeline", {
            args: { session: id, after },
          });
        } catch {
          return; // the next reset pages again
        }
        if (gen !== generation) return;
        on({ session: id, items: page.items ?? [], lastSeq: page.lastSeq, connections: null }, false);
        if (!page.more || page.lastSeq <= after) return;
        after = page.lastSeq;
      }
    }

    return () => {
      generation += 1;
      stop();
    };
  },

  diff(session, on, onError) {
    return watch(`diff:${session}` as Topic, {
      on: (p) => on((p as DiffEvent).rev),
      onError: refused(onError),
    });
  },
};
