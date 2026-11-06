<script lang="ts">
  // A tile with no picture to show: a drawn picture or three lines, in currentColor. No word, because
  // a word on a tile that small is what the owner asked to remove, and its title still says it.
  let { kind, class: cls = "" }: { kind: "image" | "text"; class?: string } = $props();
</script>

<svg class="shrink-0 {cls}" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" data-attach-glyph={kind}>
  {#if kind === "image"}
    <rect x="3" y="4" width="14" height="12" rx="1.5" />
    <circle cx="7.5" cy="8.25" r="1.25" />
    <path d="m3.5 14.5 4-3.5 2.75 2.25L13 10.5l3.5 3.5" />
  {:else}
    <path d="M4 6h12M4 10h12M4 14h8" />
  {/if}
</svg>
