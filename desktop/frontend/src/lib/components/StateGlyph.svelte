<script lang="ts">
  // One cell of the state vocabulary: roster, footer, Glance and the list. A finished turn nobody
  // has seen is a small filled dot, because a full-size ● in the top ink outweighed ◉ Needs you, and
  // an old finish must never read louder than a blocked agent. Filled keeps it apart from ○.
  let { glyph, cls = "", mono = false, class: extra = "" }: { glyph: string; cls?: string; mono?: boolean; class?: string } = $props();
</script>

{#if glyph === "●"}
  <span class="mx-auto block size-1.5 self-center rounded-full bg-ink-2 {extra}" aria-hidden="true" data-state-glyph="●"></span>
{:else}
  <span class="text-center {cls} {mono ? 'font-mono' : ''} {extra}" aria-hidden="true" data-state-glyph={glyph}>{glyph}</span>
{/if}
