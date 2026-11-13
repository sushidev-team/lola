<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // One 28px line under the roster. The rows above already carry each session's ask, so there is
  // no second list here. Down says unknown and never all clear, because the list is the last
  // thing the daemon said and nobody can say whether something is waiting now.
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { waitingQueue } from "$lib/queue";
  import { displayFor } from "$lib/theme";
  import Button from "./Button.svelte";

  const count = $derived(waitingQueue(store.rosterSessions).length);
  const running = $derived(store.rosterSessions.filter((s) => s.agentState && displayFor(s.agentState) === "working").length);

  // The moment the last wait clears gets a beat: the line holds the top ink, then settles. Never for
  // a count that started at 0, and never across a daemon outage, when nobody cleared anything.
  const CLEAR_HOLD_MS = 1200;
  let last = -1;
  let cleared = $state(false);
  $effect(() => {
    const n = count;
    if (!store.alive) {
      last = -1;
      cleared = false;
      return;
    }
    if (last > 0 && n === 0) {
      cleared = true;
      const t = setTimeout(() => (cleared = false), CLEAR_HOLD_MS);
      last = n;
      return () => clearTimeout(t);
    }
    last = n;
  });
</script>

<div class="flex h-7 min-w-0 items-center px-3 text-sm" data-sidebar-waiting>
  {#if !store.alive}
    <span class="truncate px-1.5 text-faint">State unknown</span>
  {:else if count > 0}
    <Button size="xs" class="text-orange!" title="Next session waiting on you" onclick={() => ssh.nextWaiting()}>
      <span aria-hidden="true"><Icon name="circle-alert" size={14} /></span> Waiting <span class="num">{count}</span> · <span class="text-orange/70">⌘J</span>
    </Button>
  {:else}
    <span class="truncate px-1.5 transition-colors duration-200 motion-reduce:transition-none {cleared ? 'text-ink' : 'text-faint'}">All clear · <span class="num">{running}</span> running</span>
  {/if}
</div>
