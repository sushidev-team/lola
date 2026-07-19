import "@testing-library/jest-dom/vitest";

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
