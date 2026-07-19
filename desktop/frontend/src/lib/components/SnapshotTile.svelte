<script lang="ts">
  import { ansiToHtml } from "$lib/ansi";
  // A read-only terminal snapshot rendered as styled DOM — no xterm instance, so
  // dozens can live on screen at once. `text` is a raw `capture-pane -e` snapshot
  // the parent grid refreshes on a timer.
  let { text = "", scale = 0.62 }: { text?: string; scale?: number } = $props();
  const html = $derived(ansiToHtml(text));
</script>

<div class="term-snap h-full w-full overflow-hidden bg-canvas" style="--snap-scale:{scale}">
  {#if text}
    <pre
      class="m-0 whitespace-pre font-mono leading-[1.15] text-ink"
      style="font-size:calc(12px * var(--snap-scale))">{@html html}</pre>
  {:else}
    <div class="flex h-full items-center justify-center text-[11px] text-faint">no pane output</div>
  {/if}
</div>
