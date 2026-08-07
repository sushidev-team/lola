<script lang="ts">
  import { ansiToHtml, trimTrailingBlankLines, paneColumns } from "$lib/ansi";
  import { appearance } from "$lib/theme-runtime.svelte";
  // A read-only terminal snapshot rendered as styled DOM — no xterm instance, so
  // dozens can live on screen at once. `text` is a raw `capture-pane -e` snapshot
  // the parent grid refreshes on a timer.
  //
  // The type size is FITTED to the tile, not a fixed fraction of the app's base
  // size: a pane is as wide as tmux made it (80 columns for one agent, 200 for
  // another), so any fixed size cuts some tiles off mid-word on the right — the
  // one thing a preview must not do, since the cut end of a line is usually the
  // part that says what happened. So: measure the box, count the pane's widest
  // line, and pick the size that lands that line inside the box.
  // `min` is a LEGIBILITY floor, not a fit floor: a 200-column pane in a
  // half-window tile would fit only at ~5px, which is a grey texture rather than
  // text. Below the floor the type stops shrinking and the lines WRAP instead —
  // see the pre's white-space below. Wrapping is the guarantee that a preview
  // never silently amputates the right-hand end of a line, which is usually the
  // half that says what happened.
  // `max` is the live terminal's own 13px: a tile is a preview OF that terminal,
  // so at full size it should be the same text, not a smaller typeface that has
  // to be leaned into. The tiles are tall enough now to pay for it in lines.
  let {
    text = "",
    min = 9,
    max = 13,
    pad = 10,
  }: { text?: string; min?: number; max?: number; pad?: number } = $props();

  // Advance width of the terminal font as a fraction of the em. Hack (and every
  // fallback in --font-term) is 0.6 — the value is a constant rather than a
  // measurement because measuring per tile would cost a layout flush per frame,
  // and being a hair conservative only leaves a sliver of margin on the right.
  const CHAR_EM = 0.6;

  // Reading appearance.ansi INSIDE the $derived is what re-renders every tile in
  // the grid on a flavor switch — ansi.ts itself is rune-free and just takes the
  // palette as an argument.
  //
  // Trailing blank lines are dropped BEFORE render: `capture-pane -S -60` ends at
  // the bottom of the visible pane, so a pane whose cursor sits mid-screen comes
  // back padded with empty rows. Harmless while the tile was taller than the
  // capture; now that the snapshot is anchored to its bottom edge, that padding
  // would push the newest output up out of sight.
  const body = $derived(trimTrailingBlankLines(text));
  const html = $derived(ansiToHtml(body, appearance.ansi));

  // The measured inner width of the text box (padding excluded — this is bound on
  // the element INSIDE the padded frame, so the fit already accounts for it).
  let boxWidth = $state(0);
  const cols = $derived(paneColumns(body));
  const fontPx = $derived(
    boxWidth <= 0 || cols <= 0 ? max : Math.min(max, Math.max(min, boxWidth / (cols * CHAR_EM))),
  );
</script>

<!-- bg-panel is the flavor's `base`, i.e. exactly the colour LiveTerminal paints
     as its terminal background, so a snapshot tile and the focused terminal read
     as the same terminal rather than two surfaces at slightly different levels
     (the band around it is the canvas, a step below `base`).

     flex-col + justify-end anchors the snapshot to the BOTTOM of the tile, the way
     a terminal reads: the newest line sits on the floor and older ones scroll off
     the top edge (clipped by overflow-hidden, never scrolled — this is a preview).
     Top-anchored, a tile shorter than its 60-line capture showed the OLDEST lines
     and cut off everything the agent had just done.

     The padding is inline rather than a utility because the SAME number has to
     reach the fit maths above: the text box is measured inside it, so a change
     here re-fits the type instead of silently eating a column. A terminal pressed
     against its own frame reads as clipped even when it isn't. -->
<div
  class="term-snap flex h-full w-full flex-col justify-end overflow-hidden bg-panel"
  style="padding:{pad}px"
>
  {#if text}
    <!-- bind:clientWidth here, not on the padded frame: clientWidth INCLUDES
         padding, so measuring the frame would over-report the room by 2*pad and
         put the right-hand columns back outside the tile. -->
    <div class="min-w-0" bind:clientWidth={boxWidth}>
      <!-- No `antialiased` utility here: body sets -webkit-font-smoothing:
           antialiased, which costs ~32% of the glyph ink on DOM text but does NOT
           reach the WebGL glyph atlas (detached canvases). Leaving it on made the
           tiles visibly thinner than the live terminal beside them. app.css opts
           .term-snap back out; dropping the class stops it being re-applied here.
           Same font stack as the live terminal via --font-term. -->
      <!-- pre-WRAP + break-all, not `whitespace-pre`: once the pane is wider than
           the fitted type can cover, a line folds onto the next row exactly as a
           narrow terminal would fold it, instead of being clipped at the frame.
           Nothing the agent printed can leave the tile by the right edge. -->
      <pre
        class="m-0 leading-[1.25] break-all whitespace-pre-wrap text-ink"
        style="font-family:var(--font-term);font-size:{fontPx}px">{@html html}</pre>
    </div>
  {:else}
    <div class="flex h-full items-center justify-center text-sm text-faint">no pane output</div>
  {/if}
</div>
