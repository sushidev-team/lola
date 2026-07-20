<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";

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

    <!-- no-drag: the header is the window drag region, so anything clickable
         inside it has to opt out or the click becomes a window move. -->
    <button
      type="button"
      class="no-drag -mr-1 rounded p-1 text-faint transition-colors hover:bg-edge/40 hover:text-ink"
      aria-label="Settings"
      title="Settings (S)"
      onclick={() => nav.openOverlay("settings")}
    >
      <svg
        viewBox="0 0 24 24"
        class="h-4 w-4"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <circle cx="12" cy="12" r="3" />
        <path
          d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"
        />
      </svg>
    </button>
  </span>
</header>
