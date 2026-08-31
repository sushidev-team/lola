// The affordance a horizontal strip needs before it is allowed to hide anything.
//
// WHY THIS EXISTS. Both scrolling strips in this app — the accessory bar's two
// key rows and the sessions screen's triage chips — hold more than a phone is
// wide, and `body` is `overflow: hidden`, so an overflowing strip gets no
// scrollbar and no bounce: it simply ends. The triage chips shipped that way
// once and were reverted to wrapping, because the strip ended mid-word on "In
// Review" with "Done" off-screen and nothing on the screen said so. The key row
// ends on a clean key boundary instead, which is worse — Enter and Shift-Enter
// look like keys this app does not have.
//
// So a strip fades at whichever edge is hiding something. Text that fades out
// continues; text that is chopped is broken, and a strip that fades at neither
// edge is telling the truth about being complete.
//
// IT IS SCROLL-AWARE, and that is the whole reason it is code rather than one
// CSS declaration. A static `mask-image` fades the trailing 24px forever, so
// the last key stays half-dimmed after you have scrolled to it — the same lie
// as the hard cut, pointing the other way. `edgeFadeMask` is pure so that rule
// can be tested without a browser; `overflowFade` is the six lines of DOM that
// feed it.

/** The ramp, in CSS pixels. Wide enough to read as a fade, under a 40pt key. */
export const FADE_PX = 24;

// Sub-pixel slack. A scroller that fits reports scrollWidth a fraction over
// clientWidth often enough that an exact comparison fades a complete strip, and
// `scrollLeft` at the far end lands a fraction short of the maximum.
const EPS = 1;

/** Which edges of a horizontal scroller are hiding something. */
export interface OverflowEdges {
  left: boolean;
  right: boolean;
}

/**
 * The edges that are hiding content, as a pair of booleans.
 *
 * WHY THIS IS SEPARATE FROM THE MASK. A mask can only dim what it covers, so it
 * says "there is more" ONLY when the clip happens to land on ink. A key row
 * ends on clean key boundaries with 4pt gaps, and at rest the clip lands in a
 * key's padding: measured on the device, the last visible key's glyph dimmed
 * from luminance 215 to 179 and its 1px border from 51 to 36 against a 32
 * background — which at arm's length is a complete, crisp key followed by
 * nothing. Enter and Shift-Enter read as keys the app does not have. The chip
 * strip fades correctly only because its clip happens to fall mid-word.
 *
 * So the mask stays (it is right when it lands on text) and this drives a real
 * overlay drawn on the strip's own background, which cannot be defeated by
 * where the boundary falls.
 */
export function overflowEdges(
  scrollLeft: number,
  clientWidth: number,
  scrollWidth: number,
): OverflowEdges {
  const overflow = scrollWidth - clientWidth;
  if (!(overflow > EPS)) return { left: false, right: false };
  return { left: scrollLeft > EPS, right: scrollLeft < overflow - EPS };
}

/**
 * The `mask-image` for a horizontal scroller, or `""` when it needs none.
 *
 * `""` rather than an all-black gradient on purpose: a mask promotes the
 * element to its own layer, and a strip that fits should cost nothing.
 */
export function edgeFadeMask(
  scrollLeft: number,
  clientWidth: number,
  scrollWidth: number,
  px: number = FADE_PX,
): string {
  const overflow = scrollWidth - clientWidth;
  if (!(overflow > EPS)) return ""; // also catches NaN, which is an unlaid-out node

  // Never let the two ramps meet: on a strip narrower than 2×px they would
  // cross and dim the middle, which reads as a disabled control.
  const ramp = Math.max(1, Math.min(px, Math.floor(clientWidth / 3)));

  const left = scrollLeft > EPS;
  const right = scrollLeft < overflow - EPS;
  if (!left && !right) return "";

  const head = left ? `transparent 0px, black ${ramp}px` : "black 0px";
  const tail = right ? `black calc(100% - ${ramp}px), transparent 100%` : "black 100%";
  return `linear-gradient(to right, ${head}, ${tail})`;
}

/**
 * Keep `edgeFadeMask` applied to a scrolling element.
 *
 * Both spellings are written because unprefixed `mask-image` only reaches
 * Safari 15.4 and this bundle targets iOS 15.0 — a single property would fail
 * silently on the floor device, which is the one that cannot be checked.
 */
export interface FadeOptions {
  px?: number;
  /**
   * Called whenever the overflowing edges change, so the caller can draw an
   * overlay the mask cannot express. See `overflowEdges`.
   */
  onedges?: (e: OverflowEdges) => void;
}

export function overflowFade(node: HTMLElement, opts: number | FadeOptions = FADE_PX) {
  let ramp = typeof opts === "number" ? opts : (opts.px ?? FADE_PX);
  let onedges = typeof opts === "number" ? undefined : opts.onedges;
  let last: OverflowEdges = { left: false, right: false };

  function apply() {
    const edges = overflowEdges(node.scrollLeft, node.clientWidth, node.scrollWidth);
    if (edges.left !== last.left || edges.right !== last.right) {
      last = edges;
      onedges?.(edges);
    }

    const mask = edgeFadeMask(node.scrollLeft, node.clientWidth, node.scrollWidth, ramp);
    if (mask === "") {
      node.style.removeProperty("mask-image");
      node.style.removeProperty("-webkit-mask-image");
      return;
    }
    node.style.setProperty("mask-image", mask);
    node.style.setProperty("-webkit-mask-image", mask);
  }

  node.addEventListener("scroll", apply, { passive: true });

  // Rotation and the soft keyboard both resize the strip. A ResizeObserver on
  // the node catches those; it does NOT catch the content growing inside a
  // node of unchanged width, which here is only a chip's count going from one
  // digit to two — a couple of pixels, never the difference between fitting
  // and not.
  const ro = typeof ResizeObserver === "function" ? new ResizeObserver(apply) : undefined;
  ro?.observe(node);

  // The strips are measured in the vendored mono face, which lands after the
  // first paint. Without this the first measurement is of fallback metrics.
  void document.fonts?.ready.then(apply).catch(() => {});

  apply();

  return {
    update(next: number | FadeOptions = FADE_PX) {
      ramp = typeof next === "number" ? next : (next.px ?? FADE_PX);
      if (typeof next !== "number") onedges = next.onedges;
      apply();
    },
    destroy() {
      node.removeEventListener("scroll", apply);
      ro?.disconnect();
    },
  };
}
