<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { eventPhrase, statusText } from "$lib/theme";

  // The ONLY reader of store.activity anywhere in the app. store.setActivity
  // defers its write to a macrotask so it lands in a separate flush from
  // sessions; an activity read sitting next to a session read is the same-flush
  // corruption that once froze the sessions list. Keep this read here.
</script>

{#if store.activity.length === 0}
  <div class="text-sm text-faint">no activity yet</div>
{:else}
  <ul class="flex flex-col gap-1 text-sm">
    <!-- 30, not 40: the sidebar's activity track is shorter than the old rail
         panel, and rows nobody can scroll to are just retained memory. -->
    {#each store.activity.slice(0, 30) as ev (ev.id + ev.to + ev.ago)}
      <li class="flex items-baseline gap-1.5">
        <span class="font-medium text-ink">{ev.issue || ev.id.slice(0, 6)}</span>
        <span class={statusText(ev.to)}>{eventPhrase(ev.from, ev.to)}</span>
        <span class="num ml-auto text-faint">{ev.ago}</span>
      </li>
    {/each}
  </ul>
{/if}
