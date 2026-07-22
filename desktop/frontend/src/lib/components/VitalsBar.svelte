<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import LolaLogo from "$lib/components/LolaLogo.svelte";

  // The top strip. Doubles as the frameless window-drag region; the left pad
  // clears the inset traffic lights. Kept deliberately sparse — session/project/
  // poll counts live in the rail, and daemon liveness lives in the footer — so
  // this bar carries only identity, the one urgent alert, health-on-hover, the
  // clock and settings.
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

  // One summary of the two health flags: green only when both are OK, red the
  // moment either fails — the detail (which one) is the hover reveal.
  const healthOk = $derived(!!store.status && store.status.runtimeOk && store.status.linearOk);
</script>

<header
  class="drag flex h-9 shrink-0 items-center gap-2.5 border-b border-edge/70 bg-canvas pr-5 pl-[82px] text-xs leading-none select-none"
>
  <LolaLogo class="h-[15px] w-auto shrink-0" />

  <span class="ml-auto flex items-center gap-3 text-faint">
    {#if store.status}
      <!-- Health: a single dot by default; hovering reveals which of runtime /
           linear is which, so the bar stays quiet until something is wrong. -->
      <span class="group flex items-center gap-2" title="daemon health">
        <span class={healthOk ? "text-good" : "text-bad"}>{healthOk ? "●" : "▲"}</span>
        <span
          class="flex max-w-0 items-center gap-2 overflow-hidden opacity-0 transition-all duration-150 group-hover:max-w-[220px] group-hover:opacity-100"
        >
          <span class="whitespace-nowrap {store.status.runtimeOk ? 'text-faint' : 'text-bad'}">
            runtime {store.status.runtimeOk ? "✓" : "✗"}
          </span>
          <span class="text-edge">·</span>
          <span class="whitespace-nowrap {store.status.linearOk ? 'text-faint' : 'text-bad'}">
            linear {store.status.linearOk ? "✓" : "✗"}
          </span>
        </span>
      </span>
    {/if}

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
