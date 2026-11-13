// A local, in-process replacement for the Wails event bus.
//
// The desktop's backend pushes named events into the WebView every two seconds
// and the frontend subscribes once, in `store.svelte.ts`'s `start()`. That
// subscription shape is the thing the mobile app most wants to keep: 26 of the
// 32 Wails-coupled components never touch a binding directly, they read
// `store.sessions`, and the store is fed entirely by these events. So the shim
// keeps the bus and changes only who fills it — here it is filled by
// `bridge.ts`, from frames the daemon answered, rather than by a Go push loop.
//
// The API mirrors @wailsio/runtime's Events namespace exactly, including the
// two details every call site depends on: `On` RETURNS an unsubscribe function
// (which every caller assigns and later invokes), and the callback receives an
// EVENT OBJECT whose `data` field carries the payload — not the payload itself.
// Getting either wrong produces components that look subscribed and never
// update.

/** The event object a Wails callback receives. */
export type WailsEvent<T = any> = {
  name: string;
  data: T;
  sender?: string;
};

export type WailsEventCallback<T = any> = (e: WailsEvent<T>) => void;

type Entry = { cb: WailsEventCallback<any>; remaining: number };

const listeners = new Map<string, Set<Entry>>();

/**
 * Register a callback for at most `maxCallbacks` deliveries of `name`.
 * Returns the unsubscribe function.
 */
export function OnMultiple<T = any>(
  name: string,
  cb: WailsEventCallback<T>,
  maxCallbacks: number,
): () => void {
  const entry: Entry = { cb, remaining: maxCallbacks };
  let set = listeners.get(name);
  if (!set) {
    set = new Set();
    listeners.set(name, set);
  }
  set.add(entry);
  return () => {
    const s = listeners.get(name);
    if (!s) return;
    s.delete(entry);
    if (s.size === 0) listeners.delete(name);
  };
}

/** Register a callback for every occurrence of `name`. Returns an unsubscribe. */
export function On<T = any>(name: string, cb: WailsEventCallback<T>): () => void {
  return OnMultiple(name, cb, -1);
}

/** Register a callback for the first occurrence of `name` only. */
export function Once<T = any>(name: string, cb: WailsEventCallback<T>): () => void {
  return OnMultiple(name, cb, 1);
}

/** Drop every listener registered for each named event. */
export function Off(...names: string[]): void {
  for (const n of names) listeners.delete(n);
}

/** Drop every listener for every event. */
export function OffAll(): void {
  listeners.clear();
}

/**
 * Fan an event out to its listeners, synchronously.
 *
 * This is the shim's own entry point — `bridge.ts` calls it when a poll answers
 * or a pane frame arrives. It is separate from `Emit` because `Emit` has to
 * keep Wails' `Promise<boolean>` signature, and an internal caller should not
 * have to think about a promise it never awaits.
 *
 * A throwing listener is contained. One component's render error must not stop
 * the other subscribers of the same event from being told — on the desktop the
 * Go side's fan-out gives that property for free, and losing it here would mean
 * a single bad row could freeze the whole session list.
 */
export function emit<T = any>(name: string, data: T): void {
  const set = listeners.get(name);
  if (!set || set.size === 0) return;
  // Snapshot so a listener registered DURING this delivery does not receive the
  // event it was registered by, and re-check membership so one that
  // UNSUBSCRIBED during it does not receive it either — a component that
  // unsubscribes on unmount must not then be called.
  for (const entry of [...set]) {
    if (entry.remaining === 0) continue;
    if (!set.has(entry)) continue;
    if (entry.remaining > 0) {
      entry.remaining--;
      if (entry.remaining === 0) {
        set.delete(entry);
        if (set.size === 0) listeners.delete(name);
      }
    }
    try {
      entry.cb({ name, data });
    } catch (err) {
      console.error(`wailsshim: listener for "${name}" threw`, err);
    }
  }
}

/**
 * Wails' public emit. Kept for signature compatibility — nothing in the shared
 * frontend calls it, and on mobile it is purely local (there is no Go side to
 * broadcast to). Resolves false, matching "the event was not cancelled".
 */
export function Emit<T = any>(name: string, data?: T): Promise<boolean> {
  emit(name, data as T);
  return Promise.resolve(false);
}

/** Test helper: how many listeners are registered for `name`. */
export function listenerCount(name: string): number {
  return listeners.get(name)?.size ?? 0;
}
