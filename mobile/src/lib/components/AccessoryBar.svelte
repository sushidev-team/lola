<script lang="ts">
  import {
    BAR_ROW_PRIMARY,
    BAR_ROW_SECONDARY,
    DEFAULT_MODES,
    barKeyBytes,
    type BarKey,
    type KeyModifiers,
    type TerminalModes,
  } from "@mobile/lib/keybytes";
  import { overflowFade, type OverflowEdges } from "@mobile/lib/edgefade";
  import AccessoryKey from "./AccessoryKey.svelte";

  // The keyboard accessory bar: the feature that decides whether this app is
  // usable at all.
  //
  // An iOS soft keyboard has no Escape, no Tab, no Ctrl, no arrows and no way to
  // express Shift+Enter. Every one of those is on the critical path for
  // answering a parked agent — Escape dismisses the modal the daemon's pane
  // classifier reports as ActivityBlocked, Shift-Tab cycles Claude Code's
  // permission modes, the arrows drive the AskUserQuestion picker, and Ctrl-C is
  // the reason a person reaches for the phone in the first place. Without this
  // bar the terminal screen is a viewer.
  //
  // The layout is Termux's, which is what every serious mobile terminal
  // converges on and is the one whose mechanics are readable in source. Row one
  // is always up; row two collapses, because two permanent rows over a soft
  // keyboard leave almost no pane visible on a 390-point screen. Both rows
  // scroll sideways — see STRIP below.
  //
  // MODIFIERS LATCH. Tap ctrl, then c: the next ordinary keypress consumes the
  // modifier and clears it. A modifier you must hold is unusable one-handed on
  // glass, and holding two is impossible. The latch is consumed by ANY key that
  // produces bytes, including one from the soft keyboard — which is why the
  // parent hands its own typing through `consumeLatch`.
  //
  // The bar never encodes anything itself: every byte comes from
  // keybytes.barKeyBytes, which is the one table, and this component's only job
  // is deciding which key was pressed and what the latch was at that moment.

  let {
    /** The terminal's live modes. The arrows' encoding depends on DECCKM. */
    modes = DEFAULT_MODES,
    /**
     * Nothing can receive a byte: the pane died, or the connection is down.
     *
     * A full-strength row of keys under a pane that cannot take one is the app
     * claiming an ability it does not have. The bar is faded and every key
     * refuses, latches included — a latch that survived a dead pane would be
     * consumed by the first keystroke after a reconnect.
     */
    disabled = false,
    /**
     * The soft keyboard is up, so the home-indicator inset is already paid.
     *
     * The screen pays the keyboard's height as bottom padding, and that height
     * covers the home indicator too — so a bar that goes on adding
     * `safe-area-inset-bottom` on top of it floats about 34 points clear of the
     * keyboard, which reads as a rendering fault rather than as spacing.
     */
    raised = false,
    /** Send bytes to the pane. */
    onsend,
  }: {
    modes?: TerminalModes;
    disabled?: boolean;
    raised?: boolean;
    onsend: (bytes: string) => void;
  } = $props();

  // BOTH ROWS SCROLL, and neither wraps. The primary row is nine keys wide —
  // about 500 points with the disclosure — so on every shipping iPhone its tail
  // ran off the screen with no scrollbar, no fade and nothing to grab: `body`
  // is `overflow: hidden`, so the overflow was simply invisible. That cost the
  // last two keys and, far worse, the chevron, which is the only way to open
  // row two; ctrl, alt and the four control chords were unreachable on a phone.
  //
  // Three of these classes are load-bearing rather than decorative:
  //
  //   min-w-0    a flex item defaults to `min-width: auto`, which refuses to
  //              shrink below its content — without it `overflow-x-auto` never
  //              engages and the strip goes on overflowing its parent.
  //   py-1       `overflow-x: auto` forces `overflow-y` to compute to `auto`,
  //              so a 40pt key in a 40pt box has its pressed state and any
  //              focus ring sliced off top and bottom, and a phantom vertical
  //              scrollbar appears. `overflow-y: visible` is illegal in that
  //              pairing and silently becomes `auto`; padding is the only fix.
  //   overscroll-x-contain
  //              a swipe past the last key must stay in the strip rather than
  //              becoming the WebView's back gesture. The `overscroll-behavior:
  //              none` in app.css governs the document, not this box.
  //
  // Horizontal padding stays on the ROW, never on the strip: WebKit drops the
  // trailing padding of a horizontal scroll container, so `px-2` here would put
  // the first key 8pt in and the last one hard against the clip edge.
  //
  // `use:overflowFade` is the other half and is not decoration. A key row ends
  // on a clean key boundary, so an overflowing strip looks exactly like a strip
  // that has no more keys — Enter and Shift-Enter simply appear not to exist.
  const STRIP =
    "flex min-w-0 flex-1 gap-1 overflow-x-auto overscroll-x-contain py-1 [scrollbar-width:none]";

  // THE MASK IS NOT ENOUGH ON A KEY ROW, and this is the second half.
  //
  // `overflowFade` dims the strip's trailing pixels, which says "there is more"
  // only when the clip lands on ink. A key row ends on clean key boundaries with
  // 4pt gaps, and at rest the clip lands in a key's PADDING: measured on the
  // device, the last visible key's glyph dimmed from 215 to 179 and its 1px
  // border from 51 to 36 against a 32 background — at arm's length, a complete
  // key followed by nothing. Enter and Shift-Enter read as keys this app does
  // not have, which is exactly the failure the fade was added to prevent.
  //
  // So each strip also gets an overlay drawn on the ROW, over the strip's own
  // background, plus a chevron. It cannot be defeated by where the boundary
  // falls, because it does not depend on there being a glyph underneath.
  const NONE: OverflowEdges = { left: false, right: false };
  let primaryEdges = $state<OverflowEdges>(NONE);
  let secondaryEdges = $state<OverflowEdges>(NONE);

  let expanded = $state(false);
  let ctrl = $state(false);
  let alt = $state(false);

  const mods = $derived<KeyModifiers>({ ctrl, alt });

  function clearLatch() {
    ctrl = false;
    alt = false;
  }

  /**
   * The latch as it stands, consumed. Exported so the parent can apply it to
   * text the SOFT keyboard produced: a latch that only worked for bar keys would
   * make "ctrl then a letter" — the single most common chord — impossible, since
   * the letters are on the system keyboard.
   */
  export function consumeLatch(): KeyModifiers {
    const m = { ctrl, alt };
    clearLatch();
    return m;
  }

  /** Whether a modifier is currently latched, for the parent's own encoding. */
  export function latched(): KeyModifiers {
    return { ctrl, alt };
  }

  /**
   * Fire one key.
   *
   * Nothing here refuses a press for being mid-turn — the live terminal is a
   * human's own keyboard and deliberately bypasses lola's AtPrompt gate, which
   * guards lola's OWN automation. The only refusal is a pane that cannot
   * receive a byte at all.
   */
  function press(key: BarKey) {
    if (disabled) return;
    if (key.kind === "latch") {
      // A latch toggles and sends nothing. Tapping it twice cancels, which is
      // the only way to back out of a mis-tap without sending a key.
      if (key.value === "ctrl") ctrl = !ctrl;
      else alt = !alt;
      return;
    }

    const bytes = barKeyBytes(key, modes, mods);
    clearLatch();
    if (bytes !== "") onsend(bytes);
  }
</script>

<!-- The bar sits above the soft keyboard and pays back the home-indicator inset
     itself. env() inline rather than a Tailwind token: this component must not
     depend on the shell's CSS defining a spacing scale it happens not to. -->
<div
  class="shrink-0 border-t border-edge bg-panel"
  style="padding-bottom: {raised ? '0px' : 'env(safe-area-inset-bottom, 0px)'}"
>
  {#if expanded}
    <div class="relative flex items-center px-2 pt-1 pb-1">
      <div
        class={STRIP}
        use:overflowFade={{ onedges: (e) => (secondaryEdges = e) }}
      >
        {#each BAR_ROW_SECONDARY as key (key.id ?? key.value)}
          <AccessoryKey
            label={key.label}
            aria={key.aria}
            repeats={key.repeats}
            wide={key.label.length > 2}
            {disabled}
            latched={key.kind === "latch" && (key.value === "ctrl" ? ctrl : alt)}
            onfire={() => press(key)}
          />
        {/each}
      </div>
      {@render edges(secondaryEdges, false)}
    </div>
  {/if}

  <!-- The row's own vertical padding is 0 at the top: the strip carries `py-1`
       inside itself, and doubling it here would push the pane up for nothing. -->
  <div class="relative flex items-center px-2 pb-1">
    <div class={STRIP} use:overflowFade={{ onedges: (e) => (primaryEdges = e) }}>
      {#each BAR_ROW_PRIMARY as key (key.id)}
        <AccessoryKey
          label={key.label}
          aria={key.aria}
          repeats={key.repeats}
          wide={key.label.length > 2}
          {disabled}
          onfire={() => press(key)}
        />
      {/each}
    </div>
    <!-- Inset on the right by the chevron button's width plus its gap, so the
         overlay marks the STRIP's edge and not the row's. -->
    {@render edges(primaryEdges, true)}
    <!-- OUTSIDE the strip, and that is the whole point of the split. It is the
         only way to reach row two, so a chevron that scrolled away with the keys
         would leave ctrl, alt and the four control chords unreachable on any
         phone narrower than the primary row — which is every phone. `shrink-0`
         and the `ml-1` gap replace the `gap-1` the strip owns. -->
    <button
      type="button"
      {disabled}
      aria-label={expanded ? "Hide the second key row" : "Show the second key row"}
      aria-expanded={expanded}
      class="ml-1 flex h-10 min-w-10 shrink-0 touch-manipulation items-center justify-center
             rounded-md border border-edge/60 bg-panel text-base text-faint select-none
             disabled:opacity-40"
      onpointerdown={(e) => {
        e.preventDefault();
        expanded = !expanded;
      }}
    >
      {expanded ? "▾" : "▴"}
    </button>
  </div>
</div>
{#snippet edges(e: OverflowEdges, inset: boolean)}
  <!-- Painted on the row, above the strip, and never in the way: `inset-y-0`
       with no hit area, so a swipe passes straight through to the scroller.
       The chevron is the part that survives a clip landing on empty padding. -->
  {#if e.left}
    <div
      class="pointer-events-none absolute inset-y-0 left-2 flex w-7 items-center justify-start
             bg-gradient-to-r from-panel via-panel/80 to-transparent text-sm text-faint"
      aria-hidden="true"
    >
      ‹
    </div>
  {/if}
  {#if e.right}
    <div
      class="pointer-events-none absolute inset-y-0 flex w-7 items-center justify-end
             bg-gradient-to-l from-panel via-panel/80 to-transparent text-sm text-faint
             {inset ? 'right-13' : 'right-2'}"
      aria-hidden="true"
    >
      ›
    </div>
  {/if}
{/snippet}

