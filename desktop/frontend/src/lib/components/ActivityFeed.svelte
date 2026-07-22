<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { eventPhrase, statusText } from "$lib/theme";
</script>

{#if store.activity.length === 0}
  <div class="text-xs text-faint">no activity yet</div>
{:else}
  <ul class="flex flex-col gap-1 text-xs">
    {#each store.activity.slice(0, 40) as ev (ev.id + ev.to + ev.ago)}
      <li class="flex items-baseline gap-1.5">
        <span class="font-medium text-ink">{ev.issue || ev.id.slice(0, 6)}</span>
        <span class={statusText(ev.to)}>{eventPhrase(ev.from, ev.to)}</span>
        <span class="ml-auto text-faint tabular-nums">{ev.ago}</span>
      </li>
    {/each}
  </ul>
{/if}
