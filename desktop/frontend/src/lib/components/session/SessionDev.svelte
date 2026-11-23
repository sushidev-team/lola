<script lang="ts">
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { devToggleTitle } from "$lib/devtoggle";
  import { devUrlGroups, devUrlLabel } from "$lib/devurl";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import { preview } from "$lib/preview.svelte";
  import { nav } from "$lib/nav.svelte";
  let { session }: { session: SessionInfo } = $props();
  // The agent's request is dismissed per view; applying the setup removes it daemon-side.
  let dismissed = $state("");
  const ask = $derived(session.portSetupAsk && session.portSetupAsk !== dismissed ? session.portSetupAsk : "");
  const startAsk = $derived(!session.devActive ? (session.devStartAsk ?? "") : "");
  const groups = $derived(session.devActive ? devUrlGroups(session.devUrls ?? [], session.apps ?? []) : []);
</script>

{#if session.devCommands?.length}
  <section data-side-dev aria-label="Dev">
    <div class="flex items-center justify-between gap-2">
      <h3 class="cv-overview-heading">Dev</h3>
      <div class="flex flex-wrap justify-end gap-1">
        {#if preview.sessionId === session.id}
          <Button size="xs" variant="secondary" onclick={() => preview.visible = true}>Open preview</Button>
        {/if}
        <Button size="xs" selected={session.devActive} loading={store.devPending[session.id]} title={devToggleTitle(session)} onclick={() => store.dev(session.id, !session.devActive)}>
          {#if !store.devPending[session.id]}<Icon name={session.devActive ? 'square' : 'play'} size={14} />{/if}
          {session.devActive ? 'Stop dev' : 'Start dev'}
        </Button>
      </div>
    </div>
    {#if startAsk}
      <div class="mt-2 grid gap-1.5 rounded-lg border border-edge bg-canvas/50 p-2" role="status" data-dev-start-ask>
        <p class="text-sm text-ink">The agent asks you to start the dev server</p>
        <p class="text-sm wrap-anywhere text-faint">{startAsk}</p>
        <div class="flex gap-1">
          <Button size="xs" loading={store.devPending[session.id]} onclick={() => store.dev(session.id, true)}>Start dev</Button>
        </div>
      </div>
    {/if}
    {#if ask}
      <div class="mt-2 grid gap-1.5 rounded-lg border border-edge bg-canvas/50 p-2" role="status" data-port-ask>
        <p class="text-sm text-ink">The agent asks for dynamic ports</p>
        <p class="text-sm wrap-anywhere text-faint">{ask}</p>
        <div class="flex gap-1">
          <Button size="xs" variant="secondary" onclick={() => nav.openOverlay("project", session.project, "worktree")}>Review port setup</Button>
          <Button size="xs" onclick={() => (dismissed = ask)}>Dismiss</Button>
        </div>
      </div>
    {/if}
    {#each groups as group (group.name)}
      {#if group.urls.length}
        <div class="mt-1 flex min-w-0 flex-wrap items-start gap-x-2">
          {#if group.label}
            <h4 class="max-w-full py-1 text-sm font-medium leading-4 wrap-anywhere text-ink">{group.label}</h4>
          {/if}
          <ul class="flex min-w-0 flex-[1_1_9rem] flex-wrap gap-x-1" aria-label={group.label || undefined}>
            {#each group.urls as url (url)}
              <li class="min-w-0 max-w-full"><Button size="xs" class="max-w-full font-mono!" title="Preview {url}" onclick={() => preview.open(session.id, url)}><span class="min-w-0 truncate">{devUrlLabel(url)}</span></Button></li>
            {/each}
          </ul>
        </div>
      {/if}
    {/each}
  </section>
{/if}

<style>
  [data-side-dev] {
    padding: 10px 15px;
    border-top: 1px solid color-mix(in srgb, var(--color-edge) 65%, transparent);
    background: color-mix(in srgb, var(--color-accent) 5%, var(--color-panel));
  }
</style>
