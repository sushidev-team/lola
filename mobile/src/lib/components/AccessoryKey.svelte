<script lang="ts">
  import { onDestroy } from "svelte";
  import { REPEAT_DELAY_MS, REPEAT_INTERVAL_MS } from "@mobile/lib/keybytes";
  import {
    KEY_COMMIT_MS,
    beginKey,
    cancelKey,
    commitKey,
    moveKey,
    releaseKey,
    type KeyGesture,
  } from "@mobile/lib/keygesture";

  // One key on the accessory bar.
  //
  // Three behaviours, and each exists because glass is not a keyboard:
  //
  //  1. IT FIRES ON A SETTLED PRESS, not on `pointerdown` and not on `click`.
  //     `click` waits for the finger to lift, which does not feel like a key.
  //     `pointerdown` did feel like one and was a live-fire hazard: both rows
  //     scroll sideways, the rows are wall-to-wall keys, so every swipe of the
  //     bar begins on a key and SENT it — a drag begun on ^Z to reach the
  //     right-hand end of the second row suspended a real Claude Code session.
  //     So the press commits after KEY_COMMIT_MS of a still finger, or on
  //     release if the finger lifts sooner, and never once it has travelled
  //     past the slop. The whole decision lives in keygesture.ts, where it is
  //     pinned by tests; this file only supplies the events and the timers.
  //  2. IT REPEATS while held, if the key asked to. 200ms to the first repeat
  //     and 80ms after, which are PLAN.md's numbers and matter most for the
  //     arrows: moving four items down an AskUserQuestion picker should be one
  //     press, not four. The repeat clock starts at the COMMIT, so a hold is
  //     unchanged apart from the commit window itself.
  //  3. IT NEVER TAKES FOCUS. `preventDefault` on pointerdown keeps the
  //     terminal's textarea focused, so the soft keyboard does not dismiss
  //     itself every time the bar is touched — which is what makes the bar
  //     usable at all, since it lives directly above that keyboard. It does NOT
  //     prevent the strip from scrolling: that is governed by `touch-action`,
  //     which is why the slop gate above is needed at all.
  //
  // Not a <TouchButton>: a bar key is a dense 40pt cell in a scrolling strip
  // rather than an action in a layout, it must not fade or take focus, and its
  // pressed state is driven by pointer capture rather than by :hover, which does
  // not exist here. Everything else in the app that is an action IS a Button.
  //
  // THE KEYCAP IS 40 POINTS AND ITS TARGET IS 44, WHICH ARE NOT THE SAME BOX.
  // (The target is now a real 44pt button around a 40pt keycap; see the markup.)
  // The Figma draws the key as `px-[9px] py-2` around an 11px legend, which
  // comes to 31 points — below Apple's minimum for anything a finger has to
  // hit, and this is the densest row of targets in the app, nine keys wide with
  // 5-point gaps. So the horizontal padding, the radius, the ground and the type
  // are the design's, and the height becomes `h-10` with the vertical padding
  // dropped (a fixed height plus `items-center` centres the legend exactly as
  // the padding did).
  //
  // 40 IS THE DRAWN HEIGHT, NOT THE TAP TARGET. An earlier version stopped at 40
  // and argued that the bar sits on the soft keyboard so every point it takes
  // comes out of the pane — true, and it still left the app's densest control
  // row four points under the floor rule 3 states without qualification. The
  // fix costs no layout at all: `before:` paints an invisible 2-point skirt on
  // every side, so the HIT area is 44x44 while the keycap, the bar height and
  // the pane are untouched. Two points is under the 5-point gap between keys,
  // so no two skirts overlap and no key can steal its neighbour's press.

  let {
    label,
    aria,
    repeats = false,
    latched = false,
    disabled = false,
    onfire,
  }: {
    label: string;
    aria: string;
    repeats?: boolean;
    /** For a modifier key: drawn as held until the next keypress consumes it. */
    latched?: boolean;
    /** A word-width key (esc, tab, home) rather than a glyph-width one. */
    /** Nothing can receive the byte: a dead pane, or no connection. */
    disabled?: boolean;
    /** Fire the key. */
    onfire: () => void;
  } = $props();

  let held = $state(false);
  let gesture: KeyGesture | undefined;
  let commit: ReturnType<typeof setTimeout> | undefined;
  let delay: ReturnType<typeof setTimeout> | undefined;
  let interval: ReturnType<typeof setInterval> | undefined;

  function clearTimers() {
    clearTimeout(commit);
    clearTimeout(delay);
    clearInterval(interval);
    commit = delay = interval = undefined;
  }

  /** Send it, and start the repeat clock if this key asked for one. */
  function fire() {
    onfire();
    if (!repeats) return;
    delay = setTimeout(() => {
      interval = setInterval(onfire, REPEAT_INTERVAL_MS);
    }, REPEAT_DELAY_MS);
  }

  function down(e: PointerEvent) {
    if (disabled) return;
    // Keeps the terminal's textarea focused: without it the first bar tap
    // dismisses the soft keyboard, and the bar sits right on top of it. It does
    // not stop the strip scrolling — `touch-action` governs that.
    e.preventDefault();
    clearTimers();
    // The pressed state is immediate even though the byte is not: the key has
    // to answer the finger, and drawing it pressed costs nothing if the gesture
    // turns out to be a scroll.
    held = true;
    gesture = beginKey(e.clientX, e.clientY);
    commit = setTimeout(() => {
      commit = undefined;
      if (!gesture) return;
      const r = commitKey(gesture);
      gesture = r.gesture;
      if (r.fire) fire();
    }, KEY_COMMIT_MS);
  }

  function move(e: PointerEvent) {
    if (!gesture) return;
    const next = moveKey(gesture, e.clientX, e.clientY);
    if (next === gesture) return;
    gesture = next;
    // It is a scroll. Drop the pressed state and every timer; the key is not
    // sent and there is nothing to undo, which is the entire point of waiting.
    if (next.cancelled) stop();
  }

  function up() {
    if (gesture) {
      const r = releaseKey(gesture);
      gesture = r.gesture;
      // Ordered so the repeat timers `fire` starts are torn down immediately
      // after: a tap this short has no hold to repeat.
      if (r.fire) onfire();
    }
    stop();
  }

  /** The browser took the gesture over for its own scroll. Never a keypress. */
  function abort() {
    if (gesture) gesture = cancelKey(gesture);
    stop();
  }

  function stop() {
    held = false;
    gesture = undefined;
    clearTimers();
  }

  // A disabled button gets no lift, and an unmounted one no events at all: either
  // ends a hold, or the repeat would keep firing into a pane that is gone.
  $effect(() => {
    if (disabled) stop();
  });
  onDestroy(clearTimers);
</script>

<!-- THE BUTTON IS THE 44-POINT TOUCH BOX AND THE SPAN INSIDE IS THE 40-POINT KEYCAP.
     Earlier the 44 was a `before:` skirt, invisible to layout and to anything
     that measures the control; the box is now real: a transparent `h-11` button
     with 2 points either side, so two keys 4 apart are drawn 8 apart and no two
     boxes overlap. The legend is `text-subtext`, the quietest text in the app on
     purpose: nine of them at full contrast under a live pane compete with it.

     THE KEYCAP HAS A SURFACE IN EVERY THEME. It used to take `sel`, which on
     latte is barely a step off the bar's own ground, so the keys vanished into
     the deck. The fill is the panel surface (white on latte), with a half-point
     edge and a 1pt under-shadow, so a key reads as a key in all four flavors.
     Pressed and latched keep their own grounds, which is why the surface is a
     class applied only to the resting state: a scoped rule would otherwise beat
     the utilities on specificity. -->
<button
  type="button"
  {disabled}
  aria-label={aria}
  aria-pressed={latched ? true : undefined}
  class="flex h-11 w-11 shrink-0 touch-manipulation items-center justify-center select-none
         disabled:opacity-40"
  onpointerdown={down}
  onpointermove={move}
  onpointerup={up}
  onpointercancel={abort}
  onpointerleave={abort}
  oncontextmenu={(e) => e.preventDefault()}
>
  <!-- Every key is the same 44 x 40 cap, radius 8, `sel` ground, 15px mono
       legend: no variable widths. The button around it is the 44pt hit box. -->
  <span
    class="flex h-10 w-11 items-center justify-center rounded-lg font-mono text-[15px]
           font-medium transition-colors
           {held ? 'bg-edge text-ink' : latched ? 'bg-accent-fill text-accent-ink' : 'bg-sel text-subtext'}"
  >
    {label}
  </span>
</button>

