<script lang="ts">
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { devToggleTitle } from "$lib/devtoggle";
  import { devUrlGroups, devUrlLabel } from "$lib/devurl";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import { preview } from "$lib/preview.svelte";
  let { session }: { session: SessionInfo } = $props();
  const groups = $derived(session.devActive ? devUrlGroups(session.devUrls ?? [], session.apps ?? []) : []);
</script>

{#if session.devCommands?.length}
  <section data-side-dev>
    <div class="flex items-center justify-between gap-2">
      <h3 class="cv-overview-heading">Dev</h3>
      <Button size="xs" selected={session.devActive} loading={store.devPending[session.id]} title={devToggleTitle(session)} onclick={() => store.dev(session.id, !session.devActive)}>
        {#if !store.devPending[session.id]}<Icon name={session.devActive ? 'square' : 'play'} size={14} />{/if}
        {session.devActive ? 'Stop dev' : 'Start dev'}
      </Button>
    </div>
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
