// A tap on the wrist when a session newly needs the person while the app is open.
//
// Invariants:
//  - "Needs you" is the Inbox's own bucket (`bucketOf`), so the buzz and the Needs
//    section never disagree about what counts.
//  - Only a CHANGE buzzes. The first snapshot of a launch is the baseline, a session
//    that already needed the person stays quiet, and the session on screen is
//    skipped: the person is looking at it.
//  - One buzz per push however many sessions changed, and at most one per
//    `ATTENTION_GAP_MS`, on top of the haptic engine's own gap.
//  - Foreground only, because the plugin closes the socket in the background (see
//    appstate.ts); nothing here can notify a locked phone. Display only: it feeds no
//    send, gate or reaction.

import { Events } from "@wailsio/runtime";
import { untrack } from "svelte";
import { store, type SessionInfo } from "$lib/store.svelte";
import { haptic } from "./haptics";
import { bucketOf } from "./inbox.svelte";
import { router } from "./router.svelte";

/** Least time between two attention haptics. */
export const ATTENTION_GAP_MS = 5000;

export class AttentionWatcher {
  /** Whether each known session needed the person at the last push. */
  private needs = new Map<string, boolean>();
  private baselined = false;
  private last = -Infinity;

  /**
   * Takes one full list. `viewing` is the session on screen, if any. Returns true
   * when it buzzed.
   */
  ingest(list: readonly SessionInfo[], viewing: string | null, now: number = Date.now()): boolean {
    const next = new Map<string, boolean>();
    let fresh = false;
    for (const s of list) {
      const needy = bucketOf(s) === "needs";
      next.set(s.id, needy);
      if (needy && this.baselined && s.id !== viewing && this.needs.get(s.id) !== true) fresh = true;
    }
    this.needs = next;
    this.baselined = true;
    if (!fresh || now - this.last < ATTENTION_GAP_MS) return false;
    this.last = now;
    haptic("attention", now);
    return true;
  }
}

/**
 * Feeds a watcher from the pushed `daemon:sessions` event, baselined on what the
 * store already holds. Returns the teardown.
 */
export function attachAttention(watcher: AttentionWatcher = new AttentionWatcher()): () => void {
  untrack(() => {
    if (store.sessions.length > 0) watcher.ingest(store.sessions, null, -Infinity);
  });
  return Events.On("daemon:sessions", (e) => {
    const top = router.top;
    watcher.ingest((e.data?.sessions ?? []) as SessionInfo[], top.name === "session" ? top.id : null);
  });
}
