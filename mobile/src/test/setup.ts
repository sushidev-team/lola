// Vitest global setup for the mobile app.
//
// Deliberately SHORTER than desktop/frontend/src/test/setup.ts: that file spends
// most of its length cancelling a `window.setInterval` @wailsio/runtime's
// drag.js starts at import time, and this project never loads the real Wails
// runtime — vite.config.ts aliases @wailsio/runtime at src/wailsshim/runtime.ts,
// which starts no timers. If that alias is ever removed, port the interval
// wrapper across with it.

import "@testing-library/jest-dom/vitest";

// jsdom has no canvas or WebGL surface, and xterm.js probes for one while it is
// being constructed. Stub just enough that a component importing xterm can
// mount. Real terminal rendering is verified on a device, never here.
if (!(HTMLCanvasElement.prototype as unknown as { getContext?: unknown }).getContext) {
  (HTMLCanvasElement.prototype as unknown as { getContext: () => null }).getContext = () => null;
}

// Referenced by the shared theme and breakpoint helpers.
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
  })) as unknown as typeof window.matchMedia;
}

// jsdom has no Web Animations API, which Svelte's transitions run on. A stub
// that finishes on the next task lets a transition complete and its element
// leave; what the motion looks like is verified on a device.
if (!(Element.prototype as unknown as { animate?: unknown }).animate) {
  (Element.prototype as unknown as { animate: () => Animation }).animate = function () {
    const animation = {
      playState: "running",
      currentTime: 0,
      effect: null as unknown,
      onfinish: null as null | (() => void),
      cancel() {
        animation.playState = "idle";
      },
      play() {},
      pause() {},
      finish() {
        animation.playState = "finished";
        animation.onfinish?.();
      },
    };
    setTimeout(() => {
      if (animation.playState === "running") animation.finish();
    }, 0);
    return animation as unknown as Animation;
  };
}
