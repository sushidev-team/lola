<script lang="ts">
  import { sortSessions, store } from "$lib/store.svelte";
  import { triaged } from "$lib/filters";
  import { attentionCount } from "$lib/theme";
  import SessionRow from "@mobile/lib/components/SessionRow.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import FilterSheet from "@mobile/lib/components/FilterSheet.svelte";
  import ConnectionSheet from "@mobile/lib/components/ConnectionSheet.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { nav, paneNameFor } from "@mobile/lib/nav.svelte";
  import { searchSessions } from "@mobile/lib/search";
  import { installKeyboardInset } from "@mobile/lib/keyboardinset";
  import { onDestroy, onMount } from "svelte";

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

  const filtered = $derived(nav.triage !== "" || nav.query !== "");

  /**
   * The filter button's accessible name, and it changes with the filter.
   *
   * A dot is enough for a sighted user to know something is on; it says nothing
   * about WHAT, and to VoiceOver it says nothing at all — `aria-hidden` decoration
   * on a button whose name never moves. So the name carries the state. It is the
   * one guarantee this control owes the list: a filtered list must never be
   * mistakable for a short one, on either surface.
   */
  const filterLabel = $derived.by(() => {
    if (!filtered) return "Filters";
    const parts: string[] = [];
    if (nav.triage) parts.push(`showing ${nav.triage}`);
    if (nav.query) parts.push(`searching ${nav.query}`);
    return `Filters active — ${parts.join(", ")}`;
  });

  let keyboardInset = $state(0);
  let offKeyboard: (() => void) | undefined;
  onMount(() => {
    offKeyboard = installKeyboardInset((px) => (keyboardInset = px));
  });
  onDestroy(() => {
    offKeyboard?.();
  });

  // WHICH SHEET IS OPEN LIVES IN `nav`, not here. It was two locals; naming the
  // state is what lets a development link land on an open sheet, which is the
  // only way those overlays can be photographed at all — the Simulator has no
  // gesture API, so a screen reachable solely by a tap is a screen a reviewer
  // must judge from unit tests. See lib/sheets.ts.
  const settingsOpen = $derived(nav.sheet === "connection");
  const filterOpen = $derived(nav.sheet === "filter");

  async function leave(forget: boolean) {
    nav.closeSheet();
    if (forget) await connection.forget();
    await connection.disconnect();
    nav.toConnect();
  }
</script>

<!-- The list pays back the soft keyboard's height, exactly as the terminal
     screen does. `Keyboard.resize: KeyboardResize.None` means nothing else
     will: with the filter field focused the keyboard covers the bottom of the
     list, and a list with no content below the covered row cannot be scrolled
     clear of it. -->
<div class="flex h-full min-h-0 flex-col bg-canvas" style="padding-bottom: {keyboardInset}px">
  <header
    class="flex shrink-0 items-center gap-2 border-b border-edge px-3 pb-2"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 0.5rem)"
  >
    <div class="flex min-w-0 flex-col">
      <!-- text-2xl is the phone's large-title step. -->
      <span class="truncate text-2xl font-medium text-ink">Sessions</span>
      <!-- ONE LINE, CLIPPED. Everything after the count is user-supplied — a
           triage name and, in quotes, whatever was typed into the search field
           — and this span had no truncation while the title above it did. A
           long search term wrapped and GREW the header, pushing the list down,
           which is the opposite of what moving the filters behind a button was
           for. The button's accessible name carries the same state in full, so
           nothing is lost by clipping the visible copy. -->
      <span class="truncate text-sm text-faint">
        {#if needsYou > 0}
          <span class="text-orange">{needsYou} need you</span> ·
        {/if}
        <!-- "observed" is the daemon's observer-loop word and nothing in this
             app teaches it; the noun it counts was missing as well.

             THE FILTERED COUNT NAMES BOTH NUMBERS. The chips and the search
             field now live behind a button, so the only thing on screen saying
             the list is cut is this line and the button's dot. "2 sessions"
             over two rows is indistinguishable from a quiet morning; "2 of 7
             sessions" is not, and it is the same promise the sheet's own tally
             makes from the other side. -->
        {#if filtered}
          {rows.length} of {store.sessions.length}
          {store.sessions.length === 1 ? "session" : "sessions"}
        {:else}
          {store.sessions.length}
          {store.sessions.length === 1 ? "session" : "sessions"}
        {/if}
        <!-- THE ACTIVE FILTER IS NAMED HERE as well as on the button, because
             the button can only carry a dot at this size. -->
        {#if nav.triage}
          · <span class="text-ink">{nav.triage}</span>
        {/if}
        {#if nav.query}
          · <span class="text-ink">“{nav.query}”</span>
        {/if}
      </span>
    </div>

    <!-- TWO ICONS, EACH WITH ONE SUBJECT. What was here before was a Refresh
         mark and the word "Disconnect", and neither named what it acted on, so
         both read as daemon controls — which this app has none of and must never
         grow (PLAN.md: "a phone that stops the daemon severs the only link it
         has back"). Refresh is gone outright: the list polls, and a manual
         button beside a live list is an invitation to distrust the polling.
         Disconnect moved into the settings sheet, where it can afford to name
         the Mac it leaves. -->
    <div class="ml-auto flex shrink-0 items-center gap-1">
      <TouchButton icon aria-label={filterLabel} onclick={() => nav.openSheet("filter")}>
        <span class="relative inline-flex">
          <svg
            viewBox="0 0 24 24"
            class="size-5"
            fill="none"
            stroke="currentColor"
            stroke-width="1.8"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M21 4H3l7.2 8.5V19l3.6 1.8v-8.3z" />
          </svg>
          {#if filtered}
            <!-- Decoration only: the state it marks is already in the button's
                 accessible name and in the subtitle above it. -->
            <span
              class="absolute -right-1.5 -top-1 size-2.5 rounded-full border border-canvas bg-accent"
              aria-hidden="true"
            ></span>
          {/if}
        </span>
      </TouchButton>

      <!-- A MAC, NOT A GEAR, and the name says which one. The sheet behind this
           button is about the link to one machine — it says "Connected to
           <host>" and its action is "Disconnect from <host>" — but a gear
           beside a funnel on a screen titled "Sessions" reads as list or
           display settings, so the control's subject only appeared after it was
           opened. The glyph now states the subject (a computer) and the
           accessible name states the instance, which is the half a sighted
           user gets from the sheet and a VoiceOver user got from nowhere.
           `connection.label` is the host, or "the daemon" before one is
           known. -->
      <TouchButton
        icon
        aria-label="Connection settings — connected to {connection.label}"
        onclick={() => nav.openSheet("connection")}
      >
        <svg
          viewBox="0 0 24 24"
          class="size-5"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <rect x="2.5" y="4" width="19" height="12.5" rx="1.8" />
          <path d="M9 20.5h6M12 16.5v4" />
        </svg>
      </TouchButton>
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
        {:else if filtered}
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

{#if filterOpen}
  <FilterSheet
    bind:triage={nav.triage}
    bind:query={nav.query}
    sessions={all}
    matched={rows.length}
    onclose={() => nav.closeSheet()}
  />
{/if}

{#if settingsOpen}
  <ConnectionSheet onleave={(forget) => void leave(forget)} onclose={() => nav.closeSheet()} />
{/if}
