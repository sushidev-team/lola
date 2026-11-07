<script lang="ts" module>
  /** What the composer's footer says about the agent, for the rail to end in. */
  export type FootState = { glyph: string; glyphCls: string; text: string; textCls: string; clock: string };
</script>

<script lang="ts">
  import GlyphIcon from "$lib/components/GlyphIcon.svelte";
  // Where the story's rail ends: the agent's state, on the line, at the bottom left. State glyphs may
  // be here because this is the footer's place and not the timeline. No pulse, because nothing here
  // loops. The composer keeps deciding what it says, and keeps its own copy for the single column,
  // where this row is hidden. See .cv-foot in app.css.
  let { state: foot }: { state?: FootState } = $props();
</script>

<div class="cv-rail-line cv-rail-foot grid items-end">
  {#if foot}
    <div class="relative flex min-h-7 min-w-0 items-center text-sm font-medium" data-rail-foot>
      {#if foot.glyph}<span class="absolute -left-4 grid w-4 -translate-x-1/2 place-items-center bg-canvas {foot.glyphCls}" aria-hidden="true" data-rail-foot-glyph><GlyphIcon glyph={foot.glyph} /></span>{/if}
      <span class="min-w-0 truncate {foot.textCls}">{foot.text}{#if foot.clock}{" "}<span class="num inline-block min-w-[6ch]">{foot.clock}</span>{/if}</span>
    </div>
  {/if}
</div>
