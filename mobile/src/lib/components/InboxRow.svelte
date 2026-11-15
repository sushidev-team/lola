<script lang="ts">
  import { cubicOut } from "svelte/easing";
  import Icon from "$lib/components/Icon.svelte";
  import { ageLabel, metaLine, rowAriaLabel, type RowModel } from "@mobile/lib/inbox.svelte";
  import { reducedMotion } from "@mobile/lib/mobiletokens";
  import StatusDot from "./StatusDot.svelte";

  // One dense Inbox row: a leading 6 pt state dot, title over a meta line
  // (spec §5.2, 56 pt). 16 pt inset; the separator starts at the text. The dot is
  // on every row in every view; the state word is never in the meta line (the dot
  // carries it, and the composite aria-label says it). Age and chevron share one
  // trailing column, 12 pt apart, 16 pt from the edge. Rows with no age (the
  // Projects/Activity lists) keep the chevron alone.
  // The model object is the whole input, so an unchanged session is skipped.
  // A row that arrives or leaves fades with an 8 pt slide, only in an
  // unwindowed list (`animate`): a windowed row scrolling in is not new.

  let {
    model,
    projectLabel,
    animate = false,
    onopen,
  }: {
    model: RowModel;
    projectLabel: string;
    animate?: boolean;
    onopen: () => void;
  } = $props();

  const reduced = reducedMotion();

  function enter(_node: Element) {
    return {
      duration: animate && !reduced ? 180 : 0,
      easing: cubicOut,
      css: (t: number) => `opacity: ${t}; transform: translateY(${(1 - t) * 8}px)`,
    };
  }

  const settled = $derived(model.bucket === "done");
  const age = $derived(ageLabel(model.since, model.ageFallback));
  const meta = $derived(metaLine(model, projectLabel));
  const spoken = $derived(rowAriaLabel(model, projectLabel));
</script>

<button
  type="button"
  class="flex min-h-14 w-full touch-manipulation items-center pl-4 text-left active:bg-sel"
  aria-label={spoken}
  onclick={onopen}
  in:enter|global
  out:enter|global
>
  <StatusDot tone={model.stateClass} class="mr-3" />
  <span class="flex min-h-14 w-full min-w-0 items-center gap-3 border-b border-edge-soft py-[7px] pr-4" aria-hidden="true">
    <span class="flex min-w-0 flex-1 flex-col justify-center pr-4">
      <span class="min-w-0 truncate text-body {settled ? 'text-faint' : 'text-ink'}">{model.title}</span>
      <span class="truncate text-footnote text-faint">{meta}</span>
    </span>
    {#if age}
      <span class="num shrink-0 text-footnote text-faint">{age}</span>
    {/if}
    <Icon name="chevron-right" size={16} class="shrink-0 text-faint" />
  </span>
</button>
