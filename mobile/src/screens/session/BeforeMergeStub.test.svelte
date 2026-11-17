<script lang="ts">
  import { onMount } from "svelte";

  // BeforeMerge for the Review segment's tests: hands the readiness answer a test put on
  // `globalThis.__reviewFacts` to `onfacts`, as the real component does after its read.
  let { onfacts }: { onfacts?: (f: unknown) => void } = $props();
  onMount(() => {
    const f = (globalThis as { __reviewFacts?: unknown }).__reviewFacts;
    if (f) onfacts?.(f);
  });
</script>

<div data-stub></div>
