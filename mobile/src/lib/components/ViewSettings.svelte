<script lang="ts" module>
  import type { GridGeometry } from "@mobile/lib/viewport";
  /**
   * Everything this popover reports about the grid, in the exact shape the
   * terminal screen already assembles from MobileTerminal's `onstate`. It is
   * structural on purpose: the screen keeps one `geom` object and hands it
   * over whole, rather than spreading eight props at the call site.
   */
  export type ViewGeometry = GridGeometry;

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

  /** Whether the text-size stepper is the sheet's only control (no fit row, no pin). */
  export function viewTextOnly(geom: ViewGeometry, canPin: boolean): boolean {
    return !canPin && !(geom.cols > 0 && (geom.canFit || geom.fitActive));
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
  import ListGroup from "./ListGroup.svelte";
  import ListRow from "./ListRow.svelte";
  import Switch from "./Switch.svelte";
  import { copy } from "@mobile/lib/copy";
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
    /** Whether this phone may resize the Mac's pane (the `shell` tier). */
    canPin = true,
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
    canPin?: boolean;
    onpin?: (on: boolean) => void;
    disabled?: boolean;
    ondone?: () => void;
  } = $props();

  // Both from the module block, so the readout below and the header button that
  // opens it can never disagree about whether the pane is clipped.
  const clipped = $derived(viewIsClipped(geom));
  const range = $derived(viewColumnRange(geom));

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
<!-- Inset grouped rows, the shared sheet grammar: one group for the reading
     size (and the fit that is a zoom of it), one for the only control that
     changes the Mac. The group footer says why a fit is not offered. -->
{#snippet stepper()}
  <!-- The value is the row's trailing label; the capsule is one 32pt control with
       two 44pt halves split by a hairline. A live region, so a press announces
       the size it landed on. -->
  <span class="num text-callout text-subtext" role="status" aria-live="polite">{font} pt</span>
  <div class="flex h-8 items-center rounded-full bg-edge">
    <button
      type="button"
      class="flex h-11 w-11 touch-manipulation items-center justify-center text-body text-ink select-none
             disabled:opacity-40 enabled:active:opacity-60"
      aria-label="Smaller text"
      disabled={disabled || atFloor}
      onclick={() => onfont(stepFont(font, -1))}>−</button
    >
    <span class="h-4 w-[0.5px] bg-subtext/50" aria-hidden="true"></span>
    <button
      type="button"
      class="flex h-11 w-11 touch-manipulation items-center justify-center text-body text-ink select-none
             disabled:opacity-40 enabled:active:opacity-60"
      aria-label="Larger text"
      disabled={disabled || atCeiling}
      onclick={() => onfont(stepFont(font, 1))}>+</button
    >
  </div>
{/snippet}

{#snippet pinSwitch()}
  <Switch checked={pinned} onchange={(on: boolean) => onpin(on)} label={copy.viewSettings.fit} {disabled} />
{/snippet}

<div class="pt-1">
<ListGroup footer={geom.cols > 0 && !hasFit && clipped && atFloor ? "Already at the smallest text." : undefined}>
  <ListRow label={viewTextOnly(geom, canPin) ? "" : copy.viewSettings.textSize} trailing={stepper} />
  {#if geom.cols > 0 && hasFit}
    <!-- `ondone` because this one changes what is on screen BEHIND the sheet. -->
    <ListRow
      tone="accent"
      {disabled}
      label={geom.fitActive ? "Back to the reading size" : `Fit the width (${geom.fitSize} pt)`}
      onclick={() => {
        onfit();
        ondone();
      }}
    />
  {/if}
</ListGroup>
</div>

{#if canPin}
  <!-- THE ONE CONTROL IN THIS APP THAT CHANGES SOMEBODY ELSE'S SCREEN. Its label
       names the Mac; the switch already says on or off. -->
  <div class="pt-4">
    <ListGroup>
      <ListRow label={copy.viewSettings.fit} trailing={pinSwitch} />
    </ListGroup>
  </div>
{/if}
