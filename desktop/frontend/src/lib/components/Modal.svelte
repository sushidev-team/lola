<script lang="ts">
  import type { Snippet } from "svelte";
  let {
    title,
    onClose,
    width = "560px",
    children,
    footer,
  }: {
    title: string;
    onClose: () => void;
    width?: string;
    children: Snippet;
    footer?: Snippet;
  } = $props();
</script>

<div
  class="fixed inset-0 z-40 flex items-center justify-center bg-black/45 backdrop-blur-[2px]"
  onclick={(e) => e.target === e.currentTarget && onClose()}
  onkeydown={(e) => e.key === "Escape" && onClose()}
  role="presentation"
>
  <div
    class="flex max-h-[84vh] w-full flex-col overflow-hidden rounded-xl border border-edge bg-panel shadow-2xl"
    style="max-width:{width}"
    role="dialog"
    aria-modal="true"
    aria-label={title}
  >
    <header class="flex items-center border-b border-edge/70 px-4 py-2.5">
      <h2 class="text-sm font-semibold text-accent-ink">{title}</h2>
      <button class="ml-auto text-faint hover:text-ink" onclick={onClose} aria-label="close">✕</button>
    </header>
    <div class="min-h-0 flex-1 overflow-auto p-4">
      {@render children()}
    </div>
    {#if footer}
      <footer class="border-t border-edge/70 px-4 py-2.5">{@render footer()}</footer>
    {/if}
  </div>
</div>
