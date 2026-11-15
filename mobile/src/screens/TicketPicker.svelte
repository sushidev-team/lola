<script lang="ts">
  import { untrack } from "svelte";
  import type { TicketRow } from "@bindings/internal/protocol";
  import Icon from "$lib/components/Icon.svelte";
  import { store } from "$lib/store.svelte";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import SegmentBar from "@mobile/lib/components/SegmentBar.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import VirtualList from "@mobile/lib/components/VirtualList.svelte";
  import { copy } from "@mobile/lib/copy";
  import { TicketPickerModel, type TicketScope } from "@mobile/lib/projects.svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { ticketTone } from "@mobile/lib/statustone";

  const project = $derived(router.top.name === "tab" ? (router.top.project ?? "") : "");
  const m = new TicketPickerModel();
  const SCOPES: { id: TicketScope; label: string }[] = [
    { id: "mine", label: copy.projects.tickets.mine },
    { id: "team", label: copy.projects.tickets.team },
  ];

  // Re-fetches on a project change only; the scope fetches itself.
  $effect(() => {
    const p = project;
    untrack(() => void m.load(p));
  });
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <LargeTitle
    title={copy.projects.tickets.title}
    back={{ label: store.displayNameFor(project), onclick: () => router.back() }}
  />

  <div class="shrink-0 px-4 pb-2">
    <SegmentBar
      segments={SCOPES}
      value={m.scope}
      label="Scope"
      onchange={(id: TicketScope) => m.setScope(project, id)}
    />
  </div>

  {#if m.refusal}
    <div class="mx-4 mb-2 flex shrink-0 items-center gap-2 rounded-xl bg-sel pl-3 text-subhead text-bad" role="status">
      <span class="min-w-0 flex-1 py-2">{m.refusal}</span>
      <TouchButton icon aria-label="Dismiss" class="text-bad!" onclick={() => (m.refusal = "")}>
        <Icon name="x" size={14} />
      </TouchButton>
    </div>
  {/if}

  <div class="min-h-0 flex-1">
    {#if m.loading}
      <p class="px-8 py-12 text-center text-callout text-faint">{copy.projects.tickets.loading.status}</p>
    {:else if m.error}
      <div class="flex flex-col items-center gap-3 px-8 py-12 text-center">
        <span class="text-headline text-ink">{copy.projects.tickets.failed.body}</span>
        <span class="text-callout text-bad">{m.error}</span>
        <TouchButton variant="secondary" onclick={() => m.load(project)}>{copy.projects.retry}</TouchButton>
      </div>
    {:else if m.rows.length === 0}
      <div class="flex flex-col items-center gap-3 px-8 py-12 text-center">
        <span class="text-headline text-ink">{copy.projects.tickets.none.body}</span>
        {#if m.scope === "mine"}
          <TouchButton variant="secondary" onclick={() => m.setScope(project, "team")}>
            {copy.projects.tickets.showTeam}
          </TouchButton>
        {/if}
      </div>
    {:else}
      <VirtualList items={m.rows} key={(r: TicketRow) => r.uuid} label={copy.projects.tickets.title}>
        {#snippet row(r: TicketRow)}
          <button
            type="button"
            class="flex w-full touch-manipulation pl-4 text-left active:bg-sel disabled:opacity-40"
            disabled={m.starting !== "" && m.starting !== r.identifier}
            onclick={() => void m.start(project, r)}
          >
            <span class="flex min-w-0 flex-1 items-center gap-2 border-b border-edge-soft py-2.5 pr-4">
              <span class="flex min-w-0 flex-1 flex-col gap-0.5">
                <!-- A Linear title is untrusted text: a text node, never markup. -->
                <span class="line-clamp-2 text-body text-ink">{r.title}</span>
                <span class="flex min-w-0 items-center gap-1.5 text-subhead font-normal">
                  <span class="num shrink-0 text-faint">{r.identifier}</span>
                  {#if r.alreadyLive || r.state}
                    <span aria-hidden="true" class="shrink-0 text-faint">·</span>
                    <span class="min-w-0 truncate {ticketTone(r.stateType, r.alreadyLive)}">
                      {r.alreadyLive ? copy.projects.tickets.running : r.state}
                    </span>
                  {/if}
                  {#if m.scope === "team" && r.assignee}
                    <span aria-hidden="true" class="shrink-0 text-faint">·</span>
                    <span class="min-w-0 truncate text-subtext">{r.assignee}</span>
                  {/if}
                  <span class="flex-1"></span>
                  {#if r.updated}<span class="num shrink-0 text-faint">{r.updated}</span>{/if}
                </span>
              </span>
              <Icon name="chevron-right" size={16} class="shrink-0 text-faint" />
            </span>
          </button>
        {/snippet}
      </VirtualList>
    {/if}
  </div>
</div>
