<script lang="ts">
  import type { Snippet } from "svelte";
  import Icon from "$lib/components/Icon.svelte";

  // The top of every tab root and pushed screen: the same inset, the same baseline.
  // It pins the top (the status bar, or the banner that already paid for it via
  // `--lola-top-inset`), then an optional 44pt back row, then the 28/34 title with
  // a trailing slot in a 44pt row.
  let {
    title,
    back,
    trailing,
  }: {
    title: string;
    back?: { label: string; onclick: () => void };
    trailing?: Snippet;
  } = $props();
</script>

<header class="pt-[calc(var(--lola-top-inset,env(safe-area-inset-top,0px))+8px)]">
  {#if back}
    <div class="flex h-11 items-center px-gutter">
      <button
        type="button"
        class="-ml-2 flex min-h-11 touch-manipulation items-center gap-0.5 pr-3 pl-1 text-body text-accent active:opacity-60"
        onclick={back.onclick}
      >
        <Icon name="chevron-left" size={22} />
        <span>{back.label}</span>
      </button>
    </div>
  {/if}
  <div class="flex min-h-11 items-center gap-3 px-4">
    <h1 class="min-w-0 flex-1 truncate text-title text-ink">{title}</h1>
    {#if trailing}
      <div class="flex shrink-0 items-center">{@render trailing()}</div>
    {/if}
  </div>
</header>
