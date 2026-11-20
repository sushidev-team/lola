<script lang="ts">
  // Team session sharing on the Team view: the sessions teammates offer this Mac.
  // (Which teammates may attach, and whom to trust, is decided on each Mac's row in the
  // Macs list: TeammateActions and PinConfirm.) Compact on purpose (icons carry the state, words
  // only where they decide something). The daemon applies every rule; a refusal is shown
  // in its words.
  import { onMount } from "svelte";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import { share } from "$lib/share.svelte";
  import { LEVELS, attachBlock, groupOffers, levelOf } from "$lib/sharing";
  import { tooltip } from "$lib/tooltip";

  onMount(() => share.watch());

  const groups = $derived(groupOffers(share.offers));

  function levelIcon(l: string) {
    return LEVELS.find((x) => x.id === levelOf(l)) ?? LEVELS[0];
  }
</script>

<section class="min-w-0 rounded-xl border border-edge" aria-labelledby="team-shares" data-team-shares>
  <header class="flex items-center gap-3 border-b border-edge px-4 py-3">
    <h2 id="team-shares" class="text-base font-medium text-ink">Shared sessions <span class="num ml-1 text-faint">{share.offers.length}</span></h2>
  </header>

  {#if share.error}<p role="alert" class="px-4 py-2 text-sm text-warn" data-share-error>{share.error}</p>{/if}
  {#if share.offersOffline}<p class="px-4 py-2 text-sm text-faint">The hub could not be reached.</p>{/if}

  {#if groups.length === 0}
    <p class="px-4 py-3 text-sm text-faint" data-shares-empty>Nothing is shared with you yet.</p>
  {:else}
    <div class="grid divide-y divide-edge/40">
      {#each groups as g (g.owner)}
        <div class="grid min-w-0 gap-1 px-4 py-2.5" data-share-owner={g.owner}>
          <span class="flex min-w-0 items-center gap-2">
            <span class="h-2 w-2 shrink-0 rounded-full {g.online ? 'bg-good' : 'border border-faint'}" role="img" aria-label={g.online ? "Online" : "Offline"}></span>
            <span class="min-w-0 truncate text-ink">{g.ownerName}</span>
          </span>
          <ul class="grid gap-0.5 pl-4">
            {#each g.offers as o (o.id)}
              {@const block = attachBlock(o)}
              {@const lv = levelIcon(o.level)}
              <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2" data-offer={o.id}>
                <span class="min-w-0 truncate text-ink-2">{o.title}</span>
                <span class="text-faint" use:tooltip={{ text: lv.hint }} role="img" aria-label={lv.hint}><Icon name={lv.icon} size={13} /></span>
                <Button
                  icon
                  size="xs"
                  disabled={!!block}
                  aria-label={block ? `${o.title}: ${block}` : `Attach to ${o.title}`}
                  title={block || `Attach to ${o.title}`}
                  onclick={() => share.attach(o.id)}
                  data-attach
                ><Icon name="plug" size={14} /></Button>
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </div>
  {/if}
</section>
