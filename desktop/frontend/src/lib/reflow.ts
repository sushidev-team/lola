// A one-shot relayout nudge for WKWebView.
//
// WebKit (the production WKWebView, not Chrome) can size a CSS grid's fractional
// (`fr`) tracks against a container height that has not been resolved yet on the
// very first paint, leaving `minmax(0,Nfr)` rows collapsed to 0. The panels then
// stay blank until *any* later relayout — which is exactly why toggling the
// cockpit lens (it rewrites `grid-template-rows`) makes the sessions list and the
// embedded terminal suddenly appear. See CLAUDE.md's WebKit gotcha.
//
// `reflowGridRows` reproduces that fix deterministically: after the first frame
// has painted (double rAF), it rewrites `grid-template-rows` to a throwaway value
// and straight back, forcing WebKit to re-run track sizing with the now-resolved
// height. It runs as a Svelte attachment, so it also fires whenever the element
// is re-created — e.g. returning from the focused-terminal view to the split
// view, where the same collapse would otherwise recur.
//
// The `void node.offsetHeight` read between the two writes flushes the pending
// style so the intermediate value is genuinely committed; without it the engine
// coalesces both writes into a no-op. Svelte owns the real `style` attribute and
// re-applies it on the next lens change, so restoring the captured value here is
// enough to keep the two in agreement in the meantime.
export function reflowGridRows(node: HTMLElement) {
  let r1 = 0;
  let r2 = 0;
  r1 = requestAnimationFrame(() => {
    r2 = requestAnimationFrame(() => {
      const prev = node.style.gridTemplateRows;
      node.style.gridTemplateRows = "0px";
      void node.offsetHeight; // force WebKit to flush track sizing
      node.style.gridTemplateRows = prev;
    });
  });
  return () => {
    cancelAnimationFrame(r1);
    cancelAnimationFrame(r2);
  };
}
