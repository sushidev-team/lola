// Reports an element's width as it changes, for the bands that fold below 900px of main column.
// A container query cannot do this, because what changes is which controls exist and not only
// how they are drawn. Width 0 means not measured, and every band reads that as the wide layout,
// so nothing narrows on a guess and a test environment with no layout stays wide.
export function widthOf(report: (w: number) => void) {
  return (el: HTMLElement) => {
    if (typeof ResizeObserver === "undefined") return;
    report(el.clientWidth);
    const ro = new ResizeObserver(() => report(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  };
}
