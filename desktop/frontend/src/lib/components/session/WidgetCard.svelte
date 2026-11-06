<script lang="ts">
  // A page an agent posted: a card, because you can act on it, and one you open yourself. Posting
  // creates this and a dot, never a panel, so an agent cannot change the layout under someone.
  // `Open` is the per-session consent and the open in one gesture. Nothing runs before it.
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { agentName, type SSHElement } from "$lib/ssh";
  import { activeTabs } from "$lib/docklayout";
  import Button from "../Button.svelte";

  let { sessionId, element }: { sessionId: string; element: SSHElement } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const faulted = $derived(view.widgetFaults.has(element.id));
  const line = $derived((element.body ?? "").split("\n").find((l) => l.trim()) ?? "");
  // On screen right now, so there is nothing left for this button to do.
  const shown = $derived(
    view.widgetsAllowed &&
      !view.dockHidden &&
      (view.dockCompact ? view.dockDrawer && view.dockDrawerTab === element.id : activeTabs(view.dock).includes(element.id)),
  );

  function open() {
    view.widgetsAllowed = true;
    view.openWidget(element.id);
  }
</script>

<div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 rounded-md border border-edge/60 bg-panel px-3 py-2" data-widget-card={element.id}>
  <div class="min-w-0">
    <div class="flex min-w-0 items-baseline gap-2">
      <span class="min-w-0 truncate font-medium text-ink">{element.title}</span>
      <span class="shrink-0 text-sm text-magenta">by {agent}</span>
    </div>
    {#if faulted}
      <div class="truncate text-sm text-faint">This page reported a problem.</div>
    {:else if line}
      <div class="truncate text-sm text-faint">{line}</div>
    {/if}
  </div>
  <!-- A session that ended has no dock to open it in. -->
  {#if session}<Button variant="secondary" size="xs" disabled={shown} title={shown ? "This page is showing" : "Run this page in a sandbox, and show it"} onclick={open}>{shown ? "Shown" : "Open"}</Button>{/if}
</div>
