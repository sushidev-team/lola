<script lang="ts">
  import { onDestroy, untrack } from "svelte";

  let { text = "", running = false }: { text?: string; running?: boolean } = $props();
  // Use the bounded transcript excerpt; opening details is still the only output fetch.
  const fragment = $derived(text.trim().slice(0, 1000));
  let shown = $state(untrack(() => fragment));
  let pending = "";
  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    pending = fragment;
    if (!running) {
      clearTimeout(timer);
      timer = undefined;
      shown = pending;
    } else if (!timer) {
      timer = setTimeout(() => { shown = pending; timer = undefined; }, 800);
    }
  });
  onDestroy(() => clearTimeout(timer));
</script>

<div class="cv-tool-preview selectable font-mono" data-preview><span>{shown || (running ? "Waiting for output…" : "No output recorded.")}</span></div>
