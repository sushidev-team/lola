// One clock for every phone-rendered age (spec §8.3). Ticks on 30 s wall-clock
// boundaries, only while something reads it and the page is visible.

import { createSubscriber } from "svelte/reactivity";

export const CLOCK_TICK_MS = 30_000;

const subscribe = createSubscriber((update) => {
  let timer: ReturnType<typeof setTimeout> | undefined;

  const stop = (): void => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
  };
  const schedule = (): void => {
    stop();
    timer = setTimeout(() => {
      update();
      schedule();
    }, CLOCK_TICK_MS - (Date.now() % CLOCK_TICK_MS));
  };
  const onVisibility = (): void => {
    if (document.hidden) {
      stop();
    } else {
      update();
      schedule();
    }
  };

  document.addEventListener("visibilitychange", onVisibility);
  if (!document.hidden) schedule();
  return () => {
    document.removeEventListener("visibilitychange", onVisibility);
    stop();
  };
});

export const minuteClock = {
  get now(): number {
    subscribe();
    return Date.now();
  },
};
