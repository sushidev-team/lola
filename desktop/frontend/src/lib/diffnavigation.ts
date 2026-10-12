// Scrollspy for the diff's own scroll container. Cache section positions on
// layout changes, then binary-search on scroll: intersection ratios would make
// a short file win over the long file whose sticky header is being read.
export function observeDiffNavigation(root: HTMLElement, onActive: (path: string) => void) {
  const sections = Array.from(root.querySelectorAll<HTMLElement>("section[data-file]"));
  let tops: number[] = [];
  let active = "";
  let clickedTop: number | null = null;
  let frame = 0;
  let dirty = true;

  function activate(path: string) {
    if (path === active) return;
    active = path;
    onActive(path);
  }

  function measure() {
    const origin = root.getBoundingClientRect().top + root.clientTop;
    tops = sections.map((section) => section.getBoundingClientRect().top - origin + root.scrollTop);
    dirty = false;
  }

  function sync() {
    frame = 0;
    if (!sections.length || root.clientHeight === 0) return;
    if (dirty) measure();
    const max = Math.max(0, root.scrollHeight - root.clientHeight);
    const top = Math.max(0, Math.min(root.scrollTop, max)); // WebKit elastic scrolling
    // A short file near the end cannot always reach the top. Keep an explicit
    // click selected at its clamped destination until the reader scrolls again.
    if (clickedTop !== null && Math.abs(top - clickedTop) <= 1) return;
    clickedTop = null;
    let lo = 0;
    let hi = tops.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (tops[mid] <= top + 1) lo = mid + 1;
      else hi = mid;
    }
    const index = max > 0 && top >= max - 1 ? sections.length - 1 : Math.max(0, lo - 1);
    activate(sections[index].dataset.file!);
  }

  function schedule() {
    if (!frame) frame = requestAnimationFrame(sync);
  }

  function resized() {
    dirty = true;
    clickedTop = null;
    schedule();
  }

  // Observe every section: comments, split mode, fonts and window resizing all
  // move later files without necessarily resizing the scroll container itself.
  const resize = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(resized);
  resize?.observe(root);
  sections.forEach((section) => resize?.observe(section));
  root.addEventListener("scroll", schedule, { passive: true });
  window.addEventListener("resize", resized);
  schedule();

  return {
    jump(path: string) {
      const index = sections.findIndex((section) => section.dataset.file === path);
      if (index < 0) return;
      measure();
      const top = Math.max(0, Math.min(tops[index], root.scrollHeight - root.clientHeight));
      clickedTop = top;
      // Only move this pane. scrollIntoView also scrolls ancestors, which can
      // move the surrounding session layout. Instant jumps avoid intermediate
      // highlights and also respect reduced-motion without a second code path.
      root.scrollTo({ top, behavior: "instant" });
      activate(path);
    },
    destroy() {
      resize?.disconnect();
      root.removeEventListener("scroll", schedule);
      window.removeEventListener("resize", resized);
      if (frame) cancelAnimationFrame(frame);
    },
  };
}
