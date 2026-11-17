<script lang="ts">
  // The smaller text between the prose: one line naming what some work touched, for whoever wants to
  // dig. Sans at rest and never a box: a resting wall of mono is what makes a conversation read like
  // the shell tab. The chevron sits right after the words, so it reads as part of the sentence.
  //
  // The row is the control, as the design's other card-shaped rows are, because a Button's padding and
  // hover chip would break this line's rhythm. It opens at once, and the caller keeps what is open.
  import { tooltip } from "$lib/tooltip";
  import type { WorkSegment } from "$lib/turns";
  import Chevron from "../Chevron.svelte";

  let {
    segments,
    control = false,
    chevron = false,
    open = false,
    word = "",
    bad = false,
    tail = "",
    title,
    onclick,
    ...rest
  }: {
    segments: WorkSegment[];
    /** Whether a press does anything. A row that cannot open is plain text. */
    control?: boolean;
    chevron?: boolean;
    open?: boolean;
    /** A status worth saying: `failed`, `stopped`. Goes last, and only an unsettled failure is red. */
    word?: string;
    bad?: boolean;
    tail?: string;
    title?: string;
    onclick?: () => void;
    [key: `data-${string}`]: unknown;
  } = $props();
</script>

{#snippet cells()}
  <span class="flex min-w-0 items-center gap-1.5">
    <span class="min-w-0 truncate">
      {#each segments as s, i (i)}{#if i > 0}{" · "}{/if}{s.verb}{#if s.names.length}{" "}<span class="text-ink-2">{s.names.join(", ")}</span>{/if}{#if s.rest}{" "}<span class="num">+{s.rest}</span>{/if}{#if s.stat}{" "}<span class="num">{s.stat}</span>{/if}{/each}
    </span>
    {#if chevron}<span class="grid shrink-0 place-items-center group-hover:text-ink"><Chevron {open} /></span>{/if}
  </span>
  <span class="num flex items-baseline gap-2 whitespace-nowrap">
    {#if word}<span class={bad ? "text-bad" : "text-ink-2"}>{word}</span>{/if}
    {#if tail}<span>{tail}</span>{/if}
  </span>
{/snippet}

{#if control}
  <button
    type="button"
    class="group -mx-2 grid min-h-[22px] w-[calc(100%+16px)] grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2 rounded-sm px-2 text-left text-sm text-faint hover:bg-sel/30 focus-visible:-outline-offset-1"
    aria-expanded={chevron ? open : undefined}
    use:tooltip={title || ""}
    {onclick}
    {...rest}
  >
    {@render cells()}
  </button>
{:else}
  <div class="grid min-h-[22px] grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2 text-sm text-faint" use:tooltip={title || ""} {...rest}>
    {@render cells()}
  </div>
{/if}
