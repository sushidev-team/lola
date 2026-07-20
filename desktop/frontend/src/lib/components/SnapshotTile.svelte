<script lang="ts">
  import { ansiToHtml } from "$lib/ansi";
  import { appearance } from "$lib/theme-runtime.svelte";
  // A read-only terminal snapshot rendered as styled DOM — no xterm instance, so
  // dozens can live on screen at once. `text` is a raw `capture-pane -e` snapshot
  // the parent grid refreshes on a timer.
  let { text = "", scale = 0.62 }: { text?: string; scale?: number } = $props();
  // Reading appearance.ansi INSIDE the $derived is what re-renders every tile in
  // the grid on a flavor switch — ansi.ts itself is rune-free and just takes the
  // palette as an argument.
  const html = $derived(ansiToHtml(text, appearance.ansi));
</script>

<!-- bg-panel is the flavor's `base`, i.e. exactly the colour LiveTerminal paints
     as its terminal background, so a snapshot tile and the focused terminal read
     as the same terminal rather than two surfaces at slightly different levels
     (the enclosing Panel is panel mixed 82% toward canvas). -->
<div class="term-snap h-full w-full overflow-hidden bg-panel" style="--snap-scale:{scale}">
  {#if text}
    <!-- No `antialiased` utility here: body sets -webkit-font-smoothing:
         antialiased, which costs ~32% of the glyph ink on DOM text but does NOT
         reach the WebGL glyph atlas (detached canvases). Leaving it on made the
         tiles visibly thinner than the live terminal beside them. app.css opts
         .term-snap back out; dropping the class stops it being re-applied here.
         Same font stack as the live terminal via --font-term. -->
    <pre
      class="m-0 whitespace-pre leading-[1.2] text-ink"
      style="font-family:var(--font-term);font-size:calc(13px * var(--snap-scale))">{@html html}</pre>
  {:else}
    <div class="flex h-full items-center justify-center text-[11px] text-faint">no pane output</div>
  {/if}
</div>
