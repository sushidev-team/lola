<script lang="ts">
  let { text, indices = [] }: { text: string; indices?: number[] } = $props();
  const parts = $derived.by(() => {
    const hits = new Set(indices);
    const result: { text: string; matched: boolean }[] = [];
    Array.from(text).forEach((char, i) => {
      const matched = hits.has(i);
      const last = result[result.length - 1];
      if (last && last.matched === matched) last.text += char;
      else result.push({ text: char, matched });
    });
    return result;
  });
</script>

{#each parts as part}{#if part.matched}<span class="font-medium text-accent-ink underline decoration-accent/40 underline-offset-2">{part.text}</span>{:else}{part.text}{/if}{/each}
