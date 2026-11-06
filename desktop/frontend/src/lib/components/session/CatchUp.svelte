<script lang="ts">
  // Where you stopped reading. Counted locally, with no model anywhere near it.
  let { words }: { words: string } = $props();
</script>

<div class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-x-3 text-sm text-faint" data-catch-up>
  <span class="h-px bg-edge/60" aria-hidden="true"></span>
  <span class="num">{words}</span>
  <span class="h-px bg-edge/60" aria-hidden="true"></span>
</div>
