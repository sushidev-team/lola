<script lang="ts">
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
    wide = false,
    disabled = false,
    onfire,
  }: {
    label: string;
    aria: string;
    repeats?: boolean;
    /** For a modifier key: drawn as held until the next keypress consumes it. */
    latched?: boolean;
    /** A word-width key (esc, tab, home) rather than a glyph-width one. */
    wide?: boolean;
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
</script>

<!-- THE GROUND MOVED FROM `panel` TO `sel`, AND THE BORDER IS GONE WITH IT.
     The key used to be a panel-coloured cell with a hairline around it, drawn
     against a panel-coloured bar — so the border was the only thing separating
     one key from the next, and at arm's length a row of them read as one ruled
     strip rather than as nine targets. The design fills the key instead and
     drops the bar a step to `crust`, which is the same trick a physical
     keyboard uses: the keycap is lighter than the deck it sits in. Two
     consequences to keep:

       * the PRESSED state had to move up rather than down. It was `bg-sel` over
         `bg-panel`, which is now the resting state, so a press would have been
         invisible; `bg-edge` with `text-ink` is the next step in the same
         surface ramp and reads as a key going in.
       * the latched state keeps `bg-accent-fill` and loses its `border-accent`.
         The fill is what says "held" and the border was reinforcing it against
         a ground it now no longer needs to fight.

     `text-subtext` rather than `text-ink`: the legends are the quietest text in
     the app on purpose — they name keys a person already knows how to use, and
     nine of them at full contrast under a live pane compete with the pane. -->
<button
  type="button"
  {disabled}
  aria-label={aria}
  aria-pressed={latched ? true : undefined}
  class="relative flex h-10 shrink-0 touch-manipulation items-center justify-center
         rounded-[7px] px-[9px] font-mono text-sm font-medium transition-colors select-none
         before:absolute before:-inset-0.5 before:content-['']
         disabled:opacity-40
         {wide ? 'min-w-14' : 'min-w-10'}
         {latched ? 'bg-accent-fill text-accent-ink' : 'bg-sel text-subtext'}
         {held ? 'bg-edge text-ink' : ''}"
  onpointerdown={down}
  onpointermove={move}
  onpointerup={up}
  onpointercancel={abort}
  onpointerleave={abort}
  oncontextmenu={(e) => e.preventDefault()}
>
  {label}
</button>
