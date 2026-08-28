<script lang="ts">
  import { sortSessions, store } from "$lib/store.svelte";
  import { triaged } from "$lib/filters";
  import { attentionCount } from "$lib/theme";
  import SessionRow from "@mobile/lib/components/SessionRow.svelte";
  import TriageChips from "@mobile/lib/components/TriageChips.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { nav, paneNameFor } from "@mobile/lib/nav.svelte";
  import { searchSessions } from "@mobile/lib/search";

  // The session list. READ-ONLY in M1: it shows what is happening and opens a
  // terminal. The answer card, the control tiers and every mutating action are
  // M3 and M4, and none of them is stubbed here — a disabled button for a feature
  // that does not exist is worse than its absence.
  //
  // WHERE THE DATA COMES FROM. Straight out of `$lib/store.svelte`, the desktop's
  // own store, unmodified: it subscribes to `daemon:sessions` and friends, and the
  // service shim synthesizes those events from the remote listener. So this list
  // is fed by the same code path the desktop app uses, which is the entire reuse
  // bet in one screen.
  //
  // SORTING AND FILTERING ARE BORROWED, NOT REBUILT. `sortSessions` is
  // attention-first (theme.ts's sortRank, a port of the TUI's), `triaged` is the
  // kanban partition, and both are pinned against Go by
  // desktop/state_parity_test.go. Only the free-text search is new, because the
  // desktop replaces it with a project sidebar that does not fit here.

  const all = $derived(sortSessions(store.sessions));
  const rows = $derived(
    searchSessions(triaged(all, nav.triage), nav.query, (p) => store.displayNameFor(p)),
  );
  const needsYou = $derived(attentionCount(store.sessions));

  let refreshing = $state(false);
  async function refresh() {
    refreshing = true;
    try {
      await store.refresh();
    } finally {
      refreshing = false;
    }
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <header
    class="flex shrink-0 items-center gap-2 border-b border-edge px-3 pb-2"
    style="padding-top: calc(env(safe-area-inset-top, 0px) + 0.5rem)"
  >
    <div class="flex min-w-0 flex-col">
      <span class="truncate text-lg text-ink">Sessions</span>
      <span class="text-sm text-faint">
        {#if needsYou > 0}
          <span class="text-orange">{needsYou} need you</span> ·
        {/if}
        {store.sessions.length} observed
      </span>
    </div>
    <div class="ml-auto flex shrink-0 items-center gap-1">
      <TouchButton icon aria-label="Refresh" loading={refreshing} onclick={refresh}>⟳</TouchButton>
      <TouchButton icon aria-label="Disconnect" onclick={() => {
        void connection.disconnect();
        nav.toConnect();
      }}>⏻</TouchButton>
    </div>
  </header>

  {#if !connection.ready}
    <!-- PLAN.md: an off-network phone must say so in one line naming the actual
         reason, and must never be shown the pairing screen — that is what
         revocation looks like, and the two have to stay distinguishable. -->
    <div class="shrink-0 border-b border-warn/40 bg-warn/10 px-4 py-2" role="status">
      <span class="text-sm text-warn">{connection.diagnosis.title}.</span>
      <span class="text-sm text-faint"> Showing the last snapshot.</span>
    </div>
  {:else if !store.alive}
    <div class="shrink-0 border-b border-bad/40 bg-bad/10 px-4 py-2" role="status">
      <span class="text-sm text-bad">The daemon is not running.</span>
      <span class="text-sm text-faint"> Nothing can be observed until it starts, and a phone
        cannot start it.</span>
    </div>
  {/if}

  <TriageChips bind:value={nav.triage} sessions={all} />

  <div class="shrink-0 px-3 py-2">
    <input
      class="w-full rounded border border-edge bg-panel px-3 py-2.5 text-base text-ink outline-none
             focus:border-accent placeholder:text-placeholder"
      type="search"
      inputmode="search"
      autocapitalize="none"
      autocorrect="off"
      spellcheck="false"
      aria-label="Search sessions"
      placeholder="Filter by issue, title or project"
      bind:value={nav.query}
    />
  </div>

  <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
    {#each rows as s (s.id)}
      <SessionRow
        session={s}
        projectLabel={store.displayNameFor(s.project)}
        onopen={() => nav.toTerminal(s.id, paneNameFor(s))}
      />
    {:else}
      <!-- Three genuinely different empty states. Collapsing them into "no
           sessions" is what hides a dead daemon behind what looks like an idle
           queue — and unlike the desktop, this app has no recovery action to
           offer, so saying which one it is matters more, not less. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        {#if !store.connected}
          <span class="text-faint">Connecting…</span>
        {:else if nav.query || nav.triage}
          <span class="text-faint">Nothing matches that filter.</span>
          <TouchButton
            onclick={() => {
              nav.query = "";
              nav.triage = "";
            }}>Clear filters</TouchButton
          >
        {:else}
          <span class="text-lg text-ink">No sessions</span>
          <span class="copy text-sm text-faint">
            lola spawns one per matching Linear issue. Nothing is waiting on you.
          </span>
        {/if}
      </div>
    {/each}
    <div style="height: env(safe-area-inset-bottom, 0px)"></div>
  </div>
</div>
