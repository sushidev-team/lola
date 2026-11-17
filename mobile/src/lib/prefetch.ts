// Chunks fetched ahead of the first tap (spec §8.4): PREFETCH_DELAY_MS after the
// Inbox is interactive, when the WebView is idle, and only in the foreground.
// Once per launch; a failed fetch is retried by the real navigation.

export const PREFETCH_DELAY_MS = 1000;

type Loader = () => Promise<unknown>;

let done = false;

/** Schedules `loaders` once per launch. Returns the cancel for a caller that unmounts first. */
export function prefetchOnce(loaders: readonly Loader[], delay = PREFETCH_DELAY_MS): () => void {
  if (done) return () => {};
  let timer: ReturnType<typeof setTimeout> | undefined;
  let idle: number | undefined;
  let waiting: (() => void) | undefined;

  const fetchAll = (): void => {
    done = true;
    for (const load of loaders) void load().catch(() => {});
  };
  const whenIdle = (): void => {
    if (typeof requestIdleCallback === "function") idle = requestIdleCallback(fetchAll);
    else fetchAll();
  };
  const whenForeground = (): void => {
    if (document.visibilityState === "visible") return whenIdle();
    waiting = () => {
      if (document.visibilityState !== "visible") return;
      stopWaiting();
      whenIdle();
    };
    document.addEventListener("visibilitychange", waiting);
  };
  const stopWaiting = (): void => {
    if (waiting) document.removeEventListener("visibilitychange", waiting);
    waiting = undefined;
  };

  timer = setTimeout(whenForeground, delay);
  return () => {
    clearTimeout(timer);
    stopWaiting();
    if (idle !== undefined) cancelIdleCallback(idle);
  };
}

/** Test seam: forgets that this launch prefetched. */
export function resetPrefetch(): void {
  done = false;
}
