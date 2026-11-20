<script lang="ts">
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { devToggleTitle } from "$lib/devtoggle";
  import { devUrlLabel } from "$lib/devurl";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import { preview } from "$lib/preview.svelte";
  let { session }: { session: SessionInfo } = $props();
  const urls = $derived(session.devActive ? session.devUrls ?? [] : []);
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
    {#if urls.length}
      <ul class="mt-2 grid min-w-0 gap-1">
        {#each urls as url (url)}
          <li class="min-w-0"><Button size="xs" class="max-w-full font-mono!" title="Open {url}" onclick={() => store.openURL(url)}><span class="min-w-0 truncate">{devUrlLabel(url)}</span><Icon name="external-link" size={14} /></Button></li>
        {/each}
      </ul>
      <Button size="xs" class="mt-2" title="Preview the app here and pick an element to send to the agent" onclick={() => preview.open(session.id, urls[0])}>Preview</Button>
    {/if}
  </section>
{/if}
