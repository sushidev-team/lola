<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { store } from "$lib/store.svelte";

  // The top strip. Doubles as the frameless window-drag region; the left pad
  // clears the inset traffic lights.
  let clock = $state("");
  let timer: ReturnType<typeof setInterval>;
  function tick() {
    const d = new Date();
    clock = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  }
  onMount(() => {
    tick();
    timer = setInterval(tick, 30_000);
  });
  onDestroy(() => clearInterval(timer));

  const pollsEnabled = $derived((store.status?.polls ?? []).filter((p) => p.enabled).length);
  const pollsTotal = $derived((store.status?.polls ?? []).length);
</script>

<header
  class="drag flex h-9 shrink-0 items-center gap-2.5 border-b border-edge/70 bg-canvas pr-5 pl-[100px] text-xs leading-none select-none"
>
  <span class="font-semibold tracking-wide text-ink">lola</span>

  <span class="text-edge">·</span>
  {#if store.alive}
    <span class="text-good">● running</span>
  {:else}
    <span class="text-bad">○ down</span>
  {/if}

  {#if store.status}
    <span class="text-edge">·</span>
    <span class={store.status.runtimeOk ? "text-faint" : "text-bad"}>
      runtime {store.status.runtimeOk ? "✓" : "✗"}
    </span>
    <span class="text-edge">·</span>
    <span class={store.status.linearOk ? "text-faint" : "text-bad"}>
      linear {store.status.linearOk ? "✓" : "✗"}
    </span>
  {/if}

  {#if store.needsYou > 0}
    <span class="text-edge">·</span>
    <span class="font-medium text-orange">{store.needsYou} need you</span>
  {/if}

  <span class="ml-auto flex items-center gap-3 text-faint">
    <span>sessions {store.sessions.length}</span>
    <span>projects {store.projects.length}</span>
    <span>polls {pollsEnabled}/{pollsTotal}</span>
    <span class="tabular-nums">{clock}</span>
  </span>
</header>
