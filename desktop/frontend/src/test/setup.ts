import "@testing-library/jest-dom/vitest";
import { afterAll } from "vitest";

// @wailsio/runtime's drag.js starts a `window.setInterval` AT IMPORT TIME that
// polls up to 100 times for the Wails environment (dist/drag.js). In jsdom that
// environment never arrives, so the interval is still pending when the test file
// finishes; vitest then tears the environment down, the next tick dereferences a
// `window` that no longer exists, and the run dies with an uncaught
// `ReferenceError: window is not defined` — attributed to whichever file happened
// to be running. It reproduced roughly one run in eight and exits 1, i.e. a red
// CI on a green test suite.
//
// Nothing we own creates the timer and the module offers no teardown, so setup
// records every window interval as it is created and cancels whatever is still
// pending once the file's tests finish. setupFiles run BEFORE the test file's
// imports, so the wrapper is already installed when drag.js registers its poll.
//
// It has to be window.clearInterval specifically: jsdom keeps its own timer
// registry, so the Node global clearInterval does not cancel an id that
// window.setInterval handed out (the stack bottoms out in node:internal/timers
// either way, which makes the two look interchangeable — they are not).
const pendingIntervals = new Set<number>();
const realSetInterval = window.setInterval.bind(window);
const realClearInterval = window.clearInterval.bind(window);

window.setInterval = ((handler: TimerHandler, timeout?: number, ...rest: unknown[]) => {
  const id = realSetInterval(handler as any, timeout as any, ...(rest as any[]));
  pendingIntervals.add(id as unknown as number);
  return id;
}) as typeof window.setInterval;

window.clearInterval = ((id?: number) => {
  if (id !== undefined) pendingIntervals.delete(id);
  return realClearInterval(id as any);
}) as typeof window.clearInterval;

afterAll(() => {
  for (const id of pendingIntervals) realClearInterval(id as any);
  pendingIntervals.clear();
});

// jsdom lacks the canvas + WebGL surfaces xterm.js probes at construction time.
// Stub just enough that components importing xterm can mount under test without
// pulling a real GPU context. Real terminal rendering is exercised in the app,
// not in jsdom.
if (!(HTMLCanvasElement.prototype as any).getContext) {
  (HTMLCanvasElement.prototype as any).getContext = () => null;
}

// matchMedia is referenced by theme/breakpoint helpers.
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as any;
}
