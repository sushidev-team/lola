<script lang="ts">
  // A sentence of lola's: something happened to the work. One quiet line, because the story is the
  // agent's and the human's. A failure word takes the one colour; nothing else here has any.
  //
  // A chosen label ends up here too, deliberately never the you fill: it was read off the pane,
  // and the you fill is the one thing a process must not be able to put words into.
  import type { Snippet } from "svelte";

  let { text, children }: { text: string; children?: Snippet } = $props();

  const FAILURE = /\b(failed|failing|failure)\b/i;
  const parts = $derived.by(() => {
    const m = FAILURE.exec(text);
    if (!m) return { before: text, word: "", after: "" };
    return { before: text.slice(0, m.index), word: m[0], after: text.slice(m.index + m[0].length) };
  });
</script>

<div class="group flex min-h-4 min-w-0 items-center gap-2 text-sm text-faint" data-voice="lola">
  <span class="selectable min-w-0"><span class="sr-only">Lola recorded </span>{parts.before}{#if parts.word}<span class="text-bad">{parts.word}</span>{/if}{parts.after}</span>
  {#if children}<span class="flex shrink-0 items-center">{@render children()}</span>{/if}
</div>
