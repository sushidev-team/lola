<script lang="ts">
  import { REPEAT_DELAY_MS, REPEAT_INTERVAL_MS } from "@mobile/lib/keybytes";

  // One key on the accessory bar.
  //
  // Three behaviours, and each exists because glass is not a keyboard:
  //
  //  1. IT FIRES ON POINTERDOWN, not on click. A terminal key must feel like a
  //     key, and a click event waits for the finger to lift. It also means the
  //     press and its repeat share one code path.
  //  2. IT REPEATS while held, if the key asked to. 200ms to the first repeat
  //     and 80ms after, which are PLAN.md's numbers and matter most for the
  //     arrows: moving four items down an AskUserQuestion picker should be one
  //     press, not four.
  //  3. IT NEVER TAKES FOCUS. `preventDefault` on pointerdown keeps the
  //     terminal's textarea focused, so the soft keyboard does not dismiss
  //     itself every time the bar is touched — which is what makes the bar
  //     usable at all, since it lives directly above that keyboard.
  //
  // Not a <TouchButton>: a bar key is a dense 40pt cell in a scrolling strip
  // rather than an action in a layout, it must not fade or take focus, and its
  // pressed state is driven by pointer capture rather than by :hover, which does
  // not exist here. Everything else in the app that is an action IS a Button.

  let {
    label,
    aria,
    repeats = false,
    latched = false,
    wide = false,
    onfire,
  }: {
    label: string;
    aria: string;
    repeats?: boolean;
    /** For a modifier key: drawn as held until the next keypress consumes it. */
    latched?: boolean;
    /** A word-width key (esc, tab, home) rather than a glyph-width one. */
    wide?: boolean;
    /** Fire the key. */
    onfire: () => void;
  } = $props();

  let held = $state(false);
  let delay: ReturnType<typeof setTimeout> | undefined;
  let interval: ReturnType<typeof setInterval> | undefined;

  function stop() {
    held = false;
    clearTimeout(delay);
    clearInterval(interval);
    delay = undefined;
    interval = undefined;
  }

  function down(e: PointerEvent) {
    // Keeps the terminal's textarea focused: without it the first bar tap
    // dismisses the soft keyboard, and the bar sits right on top of it.
    e.preventDefault();
    held = true;
    onfire();
    if (!repeats) return;
    delay = setTimeout(() => {
      interval = setInterval(onfire, REPEAT_INTERVAL_MS);
    }, REPEAT_DELAY_MS);
  }
</script>

<button
  type="button"
  aria-label={aria}
  aria-pressed={latched ? true : undefined}
  class="flex h-10 shrink-0 touch-manipulation items-center justify-center rounded-md
         border border-edge/60 font-mono text-base transition-colors select-none
         {wide ? 'min-w-14 px-2.5' : 'min-w-10 px-1.5'}
         {latched ? 'border-accent bg-accent-fill text-accent-ink' : 'bg-panel text-ink'}
         {held ? 'bg-sel' : ''}"
  onpointerdown={down}
  onpointerup={stop}
  onpointercancel={stop}
  onpointerleave={stop}
  oncontextmenu={(e) => e.preventDefault()}
>
  {label}
</button>
