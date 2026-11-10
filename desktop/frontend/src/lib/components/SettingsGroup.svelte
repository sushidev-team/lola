<script lang="ts">
  import type { Snippet } from "svelte";

  let { title, description = "", children }: {
    title: string;
    description?: string;
    children: Snippet;
  } = $props();
  const id = $props.id();
</script>

<section class="settings-group" aria-labelledby={id}>
  <header class="mb-4">
    <h4 id={id} class="text-lg text-ink">{title}</h4>
    {#if description}<p class="mt-1 text-sm text-faint">{description}</p>{/if}
  </header>
  <div class="grid min-w-0 gap-4">{@render children()}</div>
</section>

<style>
  :global(.settings-group) + .settings-group {
    margin-top: 24px;
    border-top: 1px solid var(--color-edge);
    padding-top: 24px;
  }
</style>
