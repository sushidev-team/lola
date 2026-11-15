<script lang="ts">
  import type { Snippet } from "svelte";
  import Icon from "$lib/components/Icon.svelte";
  import type { IconName } from "$lib/icons";
  import TouchButton from "./TouchButton.svelte";

  // The one empty state: a 56px glyph at 60%, a headline, a quiet body, an optional
  // primary action, and a slot for anything else. Vertically centred in its parent.
  let {
    icon,
    tone = "faint",
    size = "screen",
    title,
    body,
    action,
    children,
  }: {
    icon?: IconName;
    tone?: "faint" | "good";
    /** `screen` fills its parent; `sheet` is a compact block for a fit sheet. */
    size?: "screen" | "sheet";
    title: string;
    body?: string;
    action?: { label: string; onclick: () => void };
    children?: Snippet;
  } = $props();
  const sheet = $derived(size === "sheet");
</script>

<div
  class="grid place-content-center justify-items-center gap-2 px-6 text-center {sheet ? 'py-6' : 'h-full'}"
  data-empty
>
  {#if icon}
    {#if sheet}
      <span class="text-faint" aria-hidden="true"><Icon name={icon} size={28} /></span>
    {:else}
      <span class="mb-2 opacity-60 {tone === 'good' ? 'text-good' : 'text-faint'}" aria-hidden="true">
        <Icon name={icon} size={56} />
      </span>
    {/if}
  {/if}
  <h2 class="text-headline text-ink">{title}</h2>
  {#if body}
    <p class="max-w-[28ch] {sheet ? 'text-callout text-subtext' : 'text-subhead font-normal text-faint'}">{body}</p>
  {/if}
  {#if action}
    <div class="mt-4">
      <TouchButton variant={sheet ? "secondary" : "primary"} onclick={action.onclick}>{action.label}</TouchButton>
    </div>
  {/if}
  {#if children}
    <div class="mt-2">{@render children()}</div>
  {/if}
</div>
