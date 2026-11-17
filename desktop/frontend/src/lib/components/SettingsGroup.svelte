<script lang="ts">
  import type { Snippet } from "svelte";
  import HelpText from "./HelpText.svelte";

  let { title, description = "", help = "", aside, children }: {
    title: string;
    description?: string;
    /** Background for the info popover beside the title, instead of a description line. */
    help?: string;
    /** Right-aligned header content: a count, a status. */
    aside?: Snippet;
    children: Snippet;
  } = $props();
  const id = $props.id();
</script>

<section class="settings-group" aria-labelledby={id}>
  <header class="mb-4">
    <div class="flex min-w-0 items-center gap-1.5">
      <h4 id={id} class="text-lg text-ink">{title}</h4>
      {#if help}<HelpText label={title} detail={help} />{/if}
      {#if aside}<span class="ml-auto min-w-0">{@render aside()}</span>{/if}
    </div>
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
