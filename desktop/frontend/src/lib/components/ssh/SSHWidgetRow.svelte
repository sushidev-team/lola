<script lang="ts">
  // What a widget leaves in the timeline: its name and whether it runs. The page itself lives in
  // the dock, so scrolling the timeline never reloads it. A click on an open one points at its
  // tab, and only a closed one needs a button, the way back. The chip's magenta is the label that
  // says the agent made it, so no words are spent on that here.
  import { ssh } from "$lib/ssh.svelte";
  import { groupOf } from "$lib/docklayout";
  import type { SSHElement } from "$lib/ssh";
  import Button from "../Button.svelte";

  let { sessionId, element }: { sessionId: string; element: SSHElement } = $props();

  const view = $derived(ssh.view(sessionId));
  const open = $derived(!!groupOf(view.dock, element.id));
  const live = $derived(view.widgetsAllowed && open);
  const faulted = $derived(view.widgetFaults.has(element.id));
  const dot = $derived(faulted ? "fault" : live ? "live" : "off");
</script>

{#snippet face()}
  <span
    class="inline-block h-1.5 w-1.5 shrink-0 rounded-full {faulted ? 'bg-warn' : live ? 'bg-good' : 'border border-faint'}"
    data-widget-dot={dot}
    aria-hidden="true"
  ></span>
  <span class="min-w-0 truncate font-medium text-ink">{element.title}</span>
{/snippet}

<div class="flex max-w-full min-w-0 items-center gap-2 rounded-lg border border-magenta/30 bg-magenta/8 text-sm {open ? '' : 'py-0.5 pr-1 pl-2.5'}" data-widget-row={element.id}>
  {#if open}
    <Button variant="bare" size="xs" class="min-w-0 gap-2! px-2.5!" title="Go to its tab" onclick={() => view.openWidget(element.id)}>{@render face()}</Button>
  {:else}
    {@render face()}
    <Button size="xs" onclick={() => view.openWidget(element.id)}>Show</Button>
  {/if}
</div>
