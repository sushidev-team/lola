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
    /**
     * Rows actually on screen, the mirror of `shown`.
     *
     * Together the pair is what this phone can SHOW, which is a different thing
     * from `cols`/`rows` — the grid the Mac is sending. Only the pair is worth
     * naming in the size-pin caption below, because it is the size the Mac's own
     * window would be squeezed to.
     */
    shownRows: number;
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

  /**
   * Is the grid WIDER THAN THE SCREEN — i.e. do lines continue past the right
   * edge?
   *
   * Exported because the fact is now needed in two places that must not
   * disagree: this sheet's readout, and the header button that opens it. See
   * `viewClippingNotice`.
   */
  export function viewIsClipped(geom: ViewGeometry): boolean {
    return geom.cols > 0 && geom.panning;
  }

  /** The visible column range in words: "44–86 of 211". */
  export function viewColumnRange(geom: ViewGeometry): string {
    const last = Math.min(geom.cols, geom.first + Math.max(0, geom.shown - 1));
    return `${geom.first}–${last} of ${geom.cols}`;
  }

  /**
   * The clipping, as a sentence for an accessible name — or "" when nothing is
   * clipped.
   *
   * THIS IS THE LOAD-BEARING EXPORT, and the reason this component gave up its
   * own trigger rather than its guarantee. A phone shows roughly 55 of a
   * developer's 200 columns, and a pane clipped at column 55 looks EXACTLY like
   * an agent that stopped writing mid-line. The old trigger carried this in its
   * name and wore a dot, so a clipped pane was announced and marked without the
   * sheet being opened; the header now has ONE button where it had two, and that
   * button carries both. A caller that opens this sheet without spending the
   * sentence has silently dropped the guarantee.
   */
  export function viewClippingNotice(geom: ViewGeometry): string {
    return viewIsClipped(geom) ? `Showing columns ${viewColumnRange(geom)}` : "";
  }
</script>

<script lang="ts">
  import TouchButton from "./TouchButton.svelte";
  import { FONT_MAX, FONT_MIN, stepFont } from "@mobile/lib/viewport";

  // The terminal screen's view settings: what is on screen, the text size, and
  // the one control that changes the Mac.
  //
  // A SHEET BODY, not a sheet and not a button. See the note above the markup
  // for why the trigger and the <Sheet> wrapper were both removed, and what
  // carries the clipping guarantee in their place.
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
  // ESCAPE, THE BACKDROP AND THE HEIGHT CAP ARE THE PARENT SHEET'S. This file
  // used to mount its own <Sheet> and, before that, its own `svelte:window`
  // Escape listener; both are gone, in that order, and for the same reason each
  // time — one copy of the modal behaviour, in Sheet.svelte, mounted by whoever
  // owns the modal. That owner is now the terminal screen's session sheet.

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
     * Whether the size pin is on: while the phone is looking at this pane, the
     * pane's window ON THE MAC is held at the phone's size.
     *
     * OFF BY DEFAULT and never inferred. This is the one control in the app that
     * changes what somebody else sees, so it is a preference a human turns on
     * rather than a convenience the app decides for them. The screen owns the
     * value and the lifecycle; this component only draws the switch.
     */
    pinned = false,
    /** Turn the size pin on or off. */
    onpin = (_on: boolean) => {},
    /**
     * Fades and refuses every control. NOT wired to `exited` by the terminal
     * screen, and that is deliberate: a dead pane still has its last frame on
     * screen, and being able to enlarge that frame — or read how much of it is
     * off to the right — is the one thing left worth doing with it.
     */
    disabled = false,
    /**
     * Dismiss the sheet these sections are in.
     *
     * Used by exactly one control — "Fit the width", which changes what is on
     * screen behind the sheet and so has nothing left to show once it has run.
     * Every other control here is adjusted and re-adjusted with the sheet open.
     */
    ondone = () => {},
  }: {
    font: number;
    geom: ViewGeometry;
    onfont: (size: number) => void;
    onfit: () => void;
    pinned?: boolean;
    onpin?: (on: boolean) => void;
    disabled?: boolean;
    ondone?: () => void;
  } = $props();

  // Both from the module block, so the readout below and the header button that
  // opens it can never disagree about whether the pane is clipped.
  const clipped = $derived(viewIsClipped(geom));
  const range = $derived(viewColumnRange(geom));

  /**
   * The size the Mac's window would be squeezed to, in words, or "" before the
   * pane has been measured.
   *
   * Stated rather than left to be discovered. "Resized to fit this phone" is an
   * abstraction; "about 50 by 20" is the number a person can weigh against the
   * 211-column grid named four lines above it, which is the whole point of
   * putting this control in the same sheet as the column readout.
   */
  const pinSize = $derived(
    geom.shown > 0 && geom.shownRows > 0 ? ` — about ${geom.shown} by ${geom.shownRows}` : "",
  );

  const atFloor = $derived(font <= FONT_MIN);
  const atCeiling = $derived(font >= FONT_MAX);
  /** Whether there is a fit action to offer at all. */
  const hasFit = $derived(geom.canFit || geom.fitActive);

</script>

<!-- SECTIONS, NOT A SHEET, AND NO TRIGGER OF ITS OWN. Both were removed in the
     same change and for one reason: the terminal header carried a settings glyph
     and a menu glyph side by side, 88 points of chrome on the screen with the
     least of it to spare, and the issue key — the one thing that says WHICH
     session this is — was the item giving way to fit them. There is now one
     button, it opens the session sheet, and these sections are the first thing
     in it.

     What that costs, and how it is paid: the old trigger was more than a way in.
     It wore a dot and carried the live column range in its name whenever the
     pane was clipped, which is the only always-visible sign that a line stopped
     at the screen edge rather than because the agent stopped writing. Burying
     that would have defeated the reason this component exists. So the facts are
     exported from the module block above (`viewIsClipped`,
     `viewClippingNotice`) and the single header button wears both. -->
    <!-- NO "VISIBLE" SECTION. It reported the column range, the row count and a
         sentence explaining that a phone pans over a grid it cannot shrink —
         three lines of readout in a sheet whose other two sections are controls.
         The FACT it carried has not gone anywhere: the header button still wears
         the warn dot and still states the live range in its accessible name (see
         `viewClippingNotice`), and the pane draws its own position bar along its
         bottom edge. Those are both always visible, which the readout never was.

         The FIT survived it, and moved here. It is a zoom — it changes the text
         size and nothing else — so it belongs beside A− and A+ rather than under
         a heading about what is on screen, and putting it there is what let the
         readout go without taking a control with it. -->
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

      {#if geom.cols > 0 && hasFit}
        <!-- `ondone` because this one changes what is on screen BEHIND the
             sheet, so leaving the sheet up hides the thing just asked for. The
             two buttons above are pressed repeatedly and must not. -->
        <TouchButton
          wide
          variant="secondary"
          {disabled}
          onclick={() => {
            onfit();
            ondone();
          }}
        >
          {geom.fitActive ? "Back to the reading size" : `Fit the width (${geom.fitSize} pt)`}
        </TouchButton>
      {:else if clipped && atFloor}
        <!-- At the 8-point floor with a grid still wider than the screen there
             is no fit to offer, and a disabled button offering one anyway is
             worse than a sentence. See viewport.ts's FitWidth.complete.
             GATED ON THE FLOOR, not merely on the absence of a fit: `canFit`
             is also false for a moment before the pane has been measured, and
             telling a reader at 12 points that they are already at the
             smallest text is a plain lie about a control they can still
             use. -->
        <span class="copy text-sm text-faint">Already at the smallest text.</span>
      {/if}
    </section>

    <div class="h-px bg-edge/60" aria-hidden="true"></div>

    <!-- THE ONE CONTROL IN THIS APP THAT CHANGES SOMEBODY ELSE'S SCREEN, and the
         only one in this sheet that still keeps a caption.
         
         Everything else here is a zoom on this phone and now says so by being
         obvious rather than by being explained — the paragraphs under the fit
         and the column readout are gone. This one is the exact opposite of a
         local zoom and cannot be inferred from its own label, so the difference
         stays spelled out: the section is titled for the MAC, the switch is
         labelled for what happens THERE, and one line names the cost.

         SHORTENED, NOT DROPPED. It used to run four lines. What it may not lose
         is that somebody else's window narrows and that it lets go on its own —
         the first is the cost and the second is what stops a reader thinking
         they have broken something permanently. A control whose cost is one tap
         away from being read is a control whose cost is not read, which is why
         this is not a tooltip and not a help icon. -->
    <section class="flex flex-col gap-2">
      <span class="label text-faint">Pane size on the Mac</span>
      <!-- `role="switch"` with `aria-checked` rather than the shared Button's
           `selected`, which reports `aria-pressed`: a toggle that stays on until
           it is turned off is a switch, and a switch that also claimed to be a
           pressed button would be two conflicting states on one element.
           `w-full!`/`justify-between!` carry the trailing `!` for the reason
           CLAUDE.md gives — a plain utility ties with the size map's own and the
           winner would be decided by the compiled sheet's order. The default
           (non-icon) size sets no justify of its own, so nothing here is
           fighting a `justify-center`. -->
      <TouchButton
        variant="secondary"
        class="w-full! justify-between!"
        role="switch"
        aria-checked={pinned}
        aria-label="Resize the Mac's pane to fit this phone"
        {disabled}
        onclick={() => onpin(!pinned)}
      >
        <span class="min-w-0 flex-1 truncate text-left">Resize the Mac's pane to fit</span>
        <!-- The state is already on `aria-checked`; the word is for eyes only,
             and announcing it twice makes the switch read as "off off". -->
        <span class="num shrink-0 text-sm text-faint" aria-hidden="true">
          {pinned ? "On" : "Off"}
        </span>
        <span
          class="relative h-6 w-11 shrink-0 rounded-full transition-colors {pinned
            ? 'bg-accent-fill'
            : 'bg-edge'}"
          aria-hidden="true"
        >
          <span
            class="absolute top-0.5 h-5 w-5 rounded-full bg-ink transition-all {pinned
              ? 'left-[1.375rem]'
              : 'left-0.5'}"
          ></span>
        </span>
      </TouchButton>
      <span class="copy text-sm text-faint">
        Narrows the window on the Mac{pinSize} while you are on this pane.
        Released when you leave.
      </span>
    </section>

    <!-- NO "Done" HERE. The sheet these sections sit in owns its own dismiss, and
         a second one two rows above it would be two ways to close one sheet. -->
