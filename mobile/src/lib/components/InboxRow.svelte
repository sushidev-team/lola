<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { ageLabel, metaLine, rowAriaLabel, type RowModel } from "@mobile/lib/inbox.svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { mergeQueueChip } from "$lib/crosssession";
  import MetaPill from "./MetaPill.svelte";
  import { pillTone } from "./SessionChips.svelte";
  import { nav } from "$lib/nav.svelte";
  import { stateGlyph } from "$lib/sessionview";
  import StateGlyph from "$lib/components/StateGlyph.svelte";

  // The model object is the whole input, so an unchanged session is skipped.

  let {
    model,
    projectLabel,
    onopen,
  }: {
    model: RowModel;
    projectLabel: string;
    onopen: () => void;
  } = $props();

  // A row in the review bucket opens the Review segment; every other row is the host's `onopen`.
  function openRow(): void {
    if (model.bucket === "review") router.push({ name: "session", id: model.id, seg: "review" });
    else onopen();
  }

  const settled = $derived(model.bucket === "done");
  const age = $derived(ageLabel(model.since, model.ageFallback));
  const meta = $derived(metaLine(model, projectLabel));
  // The queue place only: a row is dense, and the overlap warning lives on the session.
  const queue = $derived(mergeQueueChip(model.session.mergeQueue));
  const agent = $derived(stateGlyph(model.session, nav.seenAt(model.id)));
  const mark = $derived(model.session.delivery === "merged" ? { glyph: "done", label: "Merged" }
    : model.bucket === "review" && (agent.label === "Resting" || agent.label === "Finished a turn")
      ? { glyph: "review", label: "Awaiting review" } : agent);
  const pr = $derived(model.session.prNumber);
  const spoken = $derived([rowAriaLabel(model, projectLabel), pr ? `PR #${pr}` : "", queue?.label].filter(Boolean).join(", "));
</script>

<button
  type="button"
  class="flex min-h-16 w-full touch-manipulation items-center gap-3 px-4 text-left active:bg-sel"
  aria-label={spoken}
  onclick={openRow}
>
  <span class="grid size-9 shrink-0 place-items-center rounded-lg border border-edge-soft bg-panel" aria-hidden="true">
    <StateGlyph glyph={mark.glyph} label={mark.label} />
  </span>
  <span class="flex min-h-16 min-w-0 flex-1 items-center gap-2 border-b border-edge-soft py-2.5" aria-hidden="true">
    <span class="min-w-0 flex-1">
      <span class="line-clamp-2 break-words text-body font-semibold {settled ? 'text-subtext' : 'text-ink'}">{model.title}</span>
      <span class="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-footnote text-faint">
        {#if meta}<span class="min-w-0 truncate">{meta}</span>{/if}
        {#if pr}<span class="num inline-flex items-center gap-1 text-magenta"><Icon name="git-pull-request" size={12} />#{pr}</span>{/if}
        {#if queue}<MetaPill tone={pillTone(queue.tone)} ground="grey">Queue #{model.session.mergeQueue?.position}</MetaPill>{/if}
      </span>
    </span>
    {#if age}<span class="num shrink-0 text-footnote text-faint">{age}</span>{/if}
    <Icon name="chevron-right" size={15} class="shrink-0 text-faint" />
  </span>
</button>
