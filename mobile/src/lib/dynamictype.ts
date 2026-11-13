// iOS Dynamic Type, honoured by the parts of the app that can afford to move.
//
// THE PROBLEM. `app.css` raises the desktop's five-step type scale for a phone
// and explains at length why 13px reads as a shrunken Mac window. It then pins
// every step to an ABSOLUTE pixel value — so a user who has raised the system
// text size gets nothing at all. On a phone that is the single most-used
// accessibility setting, and a capture at `accessibility-extra-large` was
// pixel-identical to one at the default size: not one glyph grew.
//
// THE SHAPE OF THE FIX. The scale becomes rem, the root font size is derived
// from the system's own body font, and `--spacing` is pinned to px so the
// LAYOUT does not scale with it. That split is deliberate: growing the type in
// the session list and the connect form is the accessibility win, while growing
// every gap, inset and icon box with it would reflow a screen that is already
// tight and would fight the terminal, which measures its own grid in px and
// must not move at all.
//
// WHY `-apple-system-body` RATHER THAN A MEDIA QUERY. There is no CSS media
// feature for Dynamic Type. WebKit exposes it exactly once, through the
// `font: -apple-system-body` shorthand, whose resolved size tracks the user's
// setting. So the value is MEASURED: one off-screen element, one
// `getComputedStyle`, one custom property. Everything downstream is ordinary
// CSS.
//
// IT ONLY EVER GROWS. The floor is the app's own designed base, because the
// phone scale was raised on purpose and letting a small Dynamic Type step undo
// that would re-introduce the failure `app.css` describes. The ceiling is a
// layout decision rather than a preference: past it the two-line session row
// and the connect form's field captions stop fitting, and clipped text serves
// nobody. A user who needs more than the ceiling is better served by system
// zoom, which scales everything including the terminal.

/** The size `--text-base` resolves to at the system's default setting. */
export const ROOT_BASE = 16;

/** Never smaller than the designed phone scale. */
export const ROOT_MIN = 16;

/** Past this the session row and the form captions stop fitting. */
export const ROOT_MAX = 23;

/**
 * `-apple-system-body` at the system's default Dynamic Type step, in px.
 *
 * UIKit's `.body` text style is 17pt there, and WebKit reports it as 17px in a
 * WebView at the default zoom. It is the denominator rather than a threshold:
 * the ratio is what carries the user's setting, so a platform that resolves the
 * shorthand to something else still scales proportionally.
 */
export const BODY_DEFAULT = 17;

/**
 * The root font size for a measured system body size.
 *
 * Returns ROOT_BASE for anything unmeasurable — a zero, a NaN, a browser with
 * no support for the shorthand — because "we could not tell" and "the user
 * wants the default" have the same right answer and an unmeasured value must
 * never shrink the app.
 */
export function rootSizeFor(bodyPx: number): number {
  if (!Number.isFinite(bodyPx) || bodyPx <= 0) return ROOT_BASE;
  const scaled = (ROOT_BASE * bodyPx) / BODY_DEFAULT;
  return Math.min(ROOT_MAX, Math.max(ROOT_MIN, Math.round(scaled * 100) / 100));
}

/** Measure the system body font, in px. 0 when it cannot be measured. */
export function measureBodyPx(doc: Document = document): number {
  const probe = doc.createElement("span");
  // `font:` shorthand, not `font-size:` — the system keyword is only valid as a
  // whole font, and a `font-size: -apple-system-body` is silently dropped.
  probe.style.cssText =
    "font: -apple-system-body; position:absolute; visibility:hidden; pointer-events:none;";
  probe.textContent = "0";
  const host = doc.body ?? doc.documentElement;
  if (!host) return 0;
  host.appendChild(probe);
  const size = parseFloat(getComputedStyle(probe).fontSize);
  probe.remove();
  return Number.isFinite(size) ? size : 0;
}

/** Write the root size the stylesheet reads. Exported for the test. */
export function applyRootSize(px: number, doc: Document = document): void {
  doc.documentElement.style.setProperty("--lola-root-size", `${px}px`);
}

/**
 * Track the setting for the life of the app. Returns a teardown.
 *
 * Re-measured on resize and on returning to the foreground, which are the two
 * moments iOS applies a Dynamic Type change to a running app — the setting is
 * changed in Settings, so the app is always backgrounded while it happens.
 */
export function installDynamicType(doc: Document = document): () => void {
  const apply = () => applyRootSize(rootSizeFor(measureBodyPx(doc)), doc);
  apply();

  const view = doc.defaultView;
  if (!view) return () => {};
  const onVisible = () => {
    if (doc.visibilityState === "visible") apply();
  };
  view.addEventListener("resize", apply);
  doc.addEventListener("visibilitychange", onVisible);
  return () => {
    view.removeEventListener("resize", apply);
    doc.removeEventListener("visibilitychange", onVisible);
  };
}
