<script lang="ts" module>
  /**
   * Everything this popover reports about the grid, in the exact shape the
   * terminal screen already assembles from MobileTerminal's `onstate`. It is
   * structural on purpose: the screen keeps one `geom` object and hands it
   * over whole, rather than spreading eight props at the call site.
   */
  export interface ViewGeometry {
    /** Columns and rows in the grid the daemon is sending. */
    cols: number;
    rows: number;
    /** Columns actually on screen. Equal to `cols` when nothing is clipped. */
    shown: number;
    /** The leftmost visible column, 1-based. */
    first: number;
    /**
     * Whether the grid is WIDER THAN THE SCREEN. The field is named for the
     * gesture it enables rather than for the fact it states — see
     * viewport.ts's isPanning — but the fact is the one that matters here, and
     * it is the whole reason this popover exists.
     */
    panning: boolean;
    /** Whether a smaller text size would show more of the grid. */
    canFit: boolean;
    /** Whether a fit-width zoom is currently in effect. */
    fitActive: boolean;
    /** The size a fit would land on, for the control's label. */
    fitSize: number;
  }
</script>

<script lang="ts">
  import Sheet from "./Sheet.svelte";
  import TouchButton from "./TouchButton.svelte";
  import { FONT_MAX, FONT_MIN, stepFont } from "@mobile/lib/viewport";

  // The terminal screen's view settings, behind one button.
  //
  // WHAT THIS REPLACED, AND WHY IT IS NOT A DELETION. The terminal header used
  // to carry four controls — a fit-width toggle, A− and A+, and a subtitle
  // reading `44–86 of 211×44` — which is most of a phone header spent on
  // settings, above a screen whose entire subject is the pane below it. They
  // are all still here, unchanged in behaviour; only their address moved.
  //
  // THE COLUMN COUNT IS THE LOAD-BEARING ONE. A phone shows roughly 55 of a
  // developer's 200 columns, and a pane clipped at column 55 looks EXACTLY like
  // an agent that stopped writing mid-line. That is the failure this number
  // prevents, so it cannot simply be dropped for tidiness, and it cannot be
  // shown only inside a closed popover either. Two things keep it honest:
  //
  //   * The TRIGGER states the clipping. Its accessible name carries the live
  //     column range whenever the grid is clipped, and it wears a visible dot,
  //     so a clipped pane is announced and marked without the popover being
  //     opened at all. This is the same move item 3's filter button makes: a
  //     filtered list must never read as a short one, and a clipped pane must
  //     never read as a silent agent.
  //   * The PANE keeps its own two-point position bar along its bottom edge
  //     (MobileTerminal), which is the shape of the same fact and tracks a
  //     finger while the number cannot.
  //
  // ESCAPE IS NOT HANDLED HERE. It used to be, as a `svelte:window` listener
  // guarded on `open` — but that made this the only sheet in the app a hardware
  // keyboard could dismiss. It moved into Sheet.svelte, which every sheet
  // mounts and which is only in the tree while one is open, so the guard went
  // with it.
  //
  // A BOTTOM SHEET, NOT AN ANCHORED POPOVER. The trigger sits in the header's
  // top-right corner, which is the one part of a phone screen a thumb cannot
  // reach; a menu hanging off it would be unusable one-handed. The sheet is the
  // shape this app already uses for the disconnect confirmation, so it is a
  // reuse of an established pattern rather than a second modal idiom.

  let {
    /** The terminal's current font size, in points. */
    font,
    geom,
    /**
     * A new ABSOLUTE font size, already clamped by stepFont. Absolute rather
     * than a delta because the clamp has to happen against the size the
     * terminal is actually rendering: the caller forwards this straight to
     * MobileTerminal.setFont, which is also where the debounced persistence
     * lives, so a size chosen here is remembered by exactly the same path a
     * pinch is. Nothing about the clamp or the persistence is re-implemented
     * in this component.
     */
    onfont,
    /** Toggle the fit-width zoom. Forwarded to MobileTerminal.toggleFit. */
    onfit,
    /**
     * Fades and refuses every control. NOT wired to `exited` by the terminal
     * screen, and that is deliberate: a dead pane still has its last frame on
     * screen, and being able to enlarge that frame — or read how much of it is
     * off to the right — is the one thing left worth doing with it.
     */
    disabled = false,
    /**
     * Whether the sheet is up.
     *
     * BINDABLE, with an internal default, so the component works both ways: on
     * its own it opens and closes itself, and the terminal screen binds it to
     * `nav.sheet` so the sheet has a NAME and a development link can land with
     * it open. That matters more than it sounds — this popover holds the column
     * readout, and until it was addressable no screenshot of that readout could
     * be taken at all, since the Simulator offers no way to tap the trigger.
     */
    open = $bindable(false),
  }: {
    font: number;
    geom: ViewGeometry;
    onfont: (size: number) => void;
    onfit: () => void;
    disabled?: boolean;
    open?: boolean;
  } = $props();

  /** The grid is wider than the screen. */
  const clipped = $derived(geom.cols > 0 && geom.panning);
  /** The rightmost visible column, 1-based and inside the grid. */
  const last = $derived(Math.min(geom.cols, geom.first + Math.max(0, geom.shown - 1)));
  /** The range in words, reused by the trigger's name and the sheet's readout. */
  const range = $derived(`${geom.first}–${last} of ${geom.cols}`);

  // THE TRIGGER SAYS WHAT IS WRONG, not what the button is. "View settings" is
  // the affordance; the clipping is the news, and a name that carries it means
  // a VoiceOver user learns the pane is clipped without opening anything.
  const triggerLabel = $derived(
    clipped ? `View settings. Showing columns ${range}` : "View settings",
  );

  const atFloor = $derived(font <= FONT_MIN);
  const atCeiling = $derived(font >= FONT_MAX);
  /** Whether there is a fit action to offer at all. */
  const hasFit = $derived(geom.canFit || geom.fitActive);

  function close() {
    open = false;
  }
</script>

<!-- `relative` so the clipped dot can be positioned inside the button. It is
     appended after the shared Button's own classes, which set no positioning,
     so no `!` is needed here — unlike the geometry overrides in TouchButton. -->
<TouchButton
  icon
  class="relative"
  aria-label={triggerLabel}
  aria-haspopup="dialog"
  aria-expanded={open}
  {disabled}
  onclick={() => (open = true)}
>
  <svg
    viewBox="0 0 16 16"
    width="18"
    height="18"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
    stroke-linecap="round"
    aria-hidden="true"
  >
    <path d="M2 4.5h3M8 4.5h6M2 11.5h6M11 11.5h3" />
    <circle cx="6.5" cy="4.5" r="1.6" />
    <circle cx="9.5" cy="11.5" r="1.6" />
  </svg>
  {#if clipped}
    <!-- The always-on clipping mark. `warn` rather than `bad` because a clipped
         pane is not an error — it is the normal state of a 200-column grid on a
         phone — but it is the state a reader has to know about before believing
         the right-hand edge of what they can see. -->
    <span
      class="pointer-events-none absolute top-1.5 right-1.5 h-2 w-2 rounded-full bg-warn ring-2 ring-canvas"
      aria-hidden="true"
    ></span>
  {/if}
</TouchButton>

{#if open}
  <!-- The app's one modal shape, shared with the settings and filter sheets. It
       is worth taking rather than keeping the inline copy this component was
       written with: Sheet caps its own height and scrolls, which is what keeps
       the Done button reachable at the largest Dynamic Type size — a sheet this
       long overflows the viewport there and hides its own dismiss control. -->
  <Sheet label="View settings" dismissLabel="Close view settings" onclose={close}>
    <!-- What is on screen, and the action that changes it. -->
    <section class="flex flex-col gap-2">
      <span class="label text-faint">Visible</span>
      {#if geom.cols > 0}
        <span class="num text-ink">
          {#if clipped}
            Columns {range}
          {:else}
            All {geom.cols} columns
          {/if}
        </span>
        <span class="num text-sm text-faint">{geom.rows} rows</span>
        {#if clipped}
          <!-- The sentence the number is shorthand for. Without it the reader
               has to already know that a phone pans over a grid it cannot
               shrink, which is exactly the thing a person who has just seen a
               line stop halfway does not know. -->
          <span class="copy text-sm text-faint">
            The pane is wider than the screen, so lines continue past the right
            edge. Drag sideways to follow them.
          </span>
        {/if}
      {:else}
        <span class="text-faint">No grid yet.</span>
      {/if}

      {#if geom.cols > 0 && hasFit}
        <TouchButton
          wide
          variant="secondary"
          {disabled}
          onclick={() => {
            onfit();
            close();
          }}
        >
          {geom.fitActive ? "Back to the reading size" : `Fit the width (${geom.fitSize} pt)`}
        </TouchButton>
        <span class="copy text-sm text-faint">
          A zoom on this phone only. The pane keeps its size on the Mac, and no
          other client sees a change.
        </span>
      {:else if clipped && atFloor}
        <!-- At the 8-point floor with a grid still wider than the screen there
             is no fit to offer, and a disabled button offering one anyway is
             worse than a sentence. See viewport.ts's FitWidth.complete.
             GATED ON THE FLOOR, not merely on the absence of a fit: `canFit`
             is also false for a moment before the pane has been measured, and
             telling a reader at 12 points that they are already at the
             smallest text is a plain lie about a control they can still
             use. -->
        <span class="copy text-sm text-faint">
          Already at the smallest text, and the grid is still wider than the
          screen.
        </span>
      {/if}
    </section>

    <div class="h-px bg-edge/60" aria-hidden="true"></div>

    <section class="flex flex-col gap-2">
      <span class="label text-faint">Text size</span>
      <!-- `secondary`, not the header's `ghost`. In a header a quiet glyph
           beside other quiet glyphs still reads as a control; in a sheet, sat
           under an outlined "Fit the width" button, a bare A− read as a
           caption. The outline is the only thing changed — same size, same
           behaviour — which is the two-of-three rule the variant map states.
           Spread apart rather than bunched left, because both ends of this
           row have to be reachable by one thumb. -->
      <div class="flex items-center gap-3">
        <TouchButton
          icon
          variant="secondary"
          aria-label="Smaller text"
          disabled={disabled || atFloor}
          onclick={() => onfont(stepFont(font, -1))}>A−</TouchButton
        >
        <!-- A live region, so pressing A− announces the size it landed on: the
             buttons keep their own names, and without this the only feedback
             is a redraw a screen-reader user cannot see. -->
        <span class="num flex-1 text-center text-ink" role="status" aria-live="polite">
          {font} pt
        </span>
        <TouchButton
          icon
          variant="secondary"
          aria-label="Larger text"
          disabled={disabled || atCeiling}
          onclick={() => onfont(stepFont(font, 1))}>A+</TouchButton
        >
      </div>
    </section>

    <TouchButton wide onclick={close}>Done</TouchButton>
  </Sheet>
{/if}
