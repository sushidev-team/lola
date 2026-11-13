// When a touch on an accessory-bar key is a KEYPRESS and when it is the start
// of a scroll.
//
// WHY THIS EXISTS. Both key rows scroll sideways — they hold more keys than a
// phone is wide, and the second row is the only way to reach y, n, pgup and
// pgdn. The rows are wall-to-wall keys with 4pt gaps, so a horizontal swipe of
// the strip NECESSARILY begins on a key. The keys fired on `pointerdown`, so
// every scroll sent whatever key it started on, into a live coding agent.
//
// That is not a theoretical hazard. A drag begun on the ^Z key to reach the
// right-hand end of the second row suspended a real Claude Code session; the
// row that has to be scrolled is the row holding ^C, ^Z and ^D.
//
// THE RULE. A press is committed either when the finger has stayed put for a
// few frames, or when it lifts having never moved. A finger that travels
// further than the slop before either of those happens is a scroll, and the key
// is never sent. Nothing here can un-send a key, which is why the decision is
// made before the byte rather than after it.
//
// WHY NOT JUST `pointercancel`. The browser does fire it when it takes a
// gesture over for scrolling, and the key components listen for it — but it
// arrives only once the scroll has actually started, which is tens of
// milliseconds and several pixels of movement after the press already went out.
// It is the backstop, not the gate.
//
// Pure, so the arithmetic is pinned by a test rather than by a finger.

/**
 * How far a finger may travel and still be a press, in CSS pixels.
 *
 * The same 8px `viewport.AXIS_LOCK_SLOP` uses for the terminal's own pan, and
 * for the same reason: it is comfortably above the jitter of a still finger on
 * glass and comfortably below a deliberate swipe.
 */
export const KEY_SLOP_PX = 8;

/**
 * How long a still finger waits before the key is sent, in milliseconds.
 *
 * Short enough to stay under the threshold at which a control feels laggy, long
 * enough that a swipe has visibly begun. The cost is real and worth stating: a
 * key now arrives this much later than it used to, and a repeating key reaches
 * its first repeat this much later still. The alternative was a bar that could
 * not be scrolled without sending a control character.
 */
export const KEY_COMMIT_MS = 50;

/** One touch on a key, from the moment it lands. */
export interface KeyGesture {
  readonly x: number;
  readonly y: number;
  /** The key has been sent; further movement is a hold, not a scroll. */
  readonly fired: boolean;
  /** The finger travelled: this gesture is a scroll and will never fire. */
  readonly cancelled: boolean;
}

/** A touch has landed. Nothing is sent yet. */
export function beginKey(x: number, y: number): KeyGesture {
  return { x, y, fired: false, cancelled: false };
}

/** Whether a point is further from the gesture's origin than the slop. */
export function beyondSlop(g: KeyGesture, x: number, y: number, slop = KEY_SLOP_PX): boolean {
  const dx = x - g.x;
  const dy = y - g.y;
  return Math.hypot(dx, dy) > slop;
}

/**
 * The finger moved.
 *
 * Movement AFTER the key has been sent is not a cancellation: at that point the
 * gesture is a hold, the repeat is running, and sliding a fingertip a few
 * millimetres while holding an arrow key must not stop it.
 */
export function moveKey(g: KeyGesture, x: number, y: number, slop = KEY_SLOP_PX): KeyGesture {
  if (g.fired || g.cancelled) return g;
  return beyondSlop(g, x, y, slop) ? { ...g, cancelled: true } : g;
}

/** The still-finger timer elapsed: send it, unless this turned into a scroll. */
export function commitKey(g: KeyGesture): { gesture: KeyGesture; fire: boolean } {
  if (g.fired || g.cancelled) return { gesture: g, fire: false };
  return { gesture: { ...g, fired: true }, fire: true };
}

/**
 * The finger lifted.
 *
 * A tap shorter than the commit window still fires, on release — otherwise the
 * quickest taps, which are the most obviously deliberate ones, would be the only
 * presses the bar dropped.
 */
export function releaseKey(g: KeyGesture): { gesture: KeyGesture; fire: boolean } {
  return commitKey(g);
}

/** The browser took the gesture over for scrolling. It is a scroll, full stop. */
export function cancelKey(g: KeyGesture): KeyGesture {
  return g.cancelled ? g : { ...g, cancelled: true };
}

/**
 * How long a still finger holds before a tab strip opens its menu, in ms.
 *
 * REUSED HERE rather than given its own module because the hard part of a long
 * press is the part this file already solved: a press that becomes a drag must
 * scroll the strip and open nothing. The tab strip has exactly the accessory
 * bar's geometry -- `overflow-x-auto` with wall-to-wall targets -- so a swipe
 * necessarily begins on a tab, and `beginKey`/`moveKey`/`cancelKey` are the
 * gate. Only the timer differs, and it is the whole difference between the two
 * gestures: `KEY_COMMIT_MS` decides how soon a press COUNTS, this decides how
 * long a press must be held to mean something else.
 *
 * 500ms matches iOS's own long press. It has to sit comfortably above
 * KEY_COMMIT_MS or a tap and a hold would be telling the same story at the same
 * moment, and comfortably below the point at which a person concludes nothing is
 * going to happen and lifts.
 */
export const LONG_PRESS_MS = 500;
