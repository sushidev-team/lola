<script lang="ts">
  import { untrack } from "svelte";
  import { nav } from "$lib/nav.svelte";
  import SessionsTable from "./SessionsTable.svelte";
  import SessionsKanban from "./SessionsKanban.svelte";
  import SessionEmbed from "./SessionEmbed.svelte";
  import TerminalGrid from "$lib/views/TerminalGrid.svelte";
  import { reflowGridRows } from "$lib/reflow";

  // The cockpit's main column, extracted into its own component so Cockpit mounts
  // it directly (see WKWEBVIEW_REACTIVITY in Cockpit.svelte). Reads the live store
  // itself; the underlying reactivity is sound because the store no longer writes
  // sessions + activity in the same flush (see store.svelte.ts).
  //
  // The panel is deliberately UNTITLED now: <MainTopBar> names the view, counts
  // its rows and owns the lens switcher, so a second title + count + accent
  // border here was two chrome bars saying the same thing.

  // One session view at two heights. In Overview it is the band under the list, for whichever row
  // is selected, and Enter makes the SAME instance fill <main>. Nothing remounts and no other view
  // of a session exists, so opening one only changes its height. Its wrapper is a CSS state, never
  // an {#if}: swapping a branch with a terminal inside froze the enclosing template effect in the
  // production WKWebView (see CockpitLayout.svelte).
  const focused = $derived(!!nav.focusedTerm);
  // A short wait, so holding j does not mount a session per row, or attach to one that opens on
  // its terminal.
  const SETTLE_MS = 120;
  let settled = $state(untrack(() => nav.selectedId));
  $effect(() => {
    const id = nav.selectedId;
    if (!untrack(() => settled)) {
      settled = id;
      return;
    }
    const t = setTimeout(() => (settled = id), SETTLE_MS);
    return () => clearTimeout(t);
  });
  // Leaving an open session keeps it on show, whatever the wait above has not caught up with.
  $effect(() => {
    if (nav.focusedTerm) settled = nav.focusedTerm;
  });
  const shownId = $derived(focused ? nav.focusedTerm : nav.lens === "grid" ? "" : settled);
</script>

<!-- a grid so panels stretch to full width AND height in WebKit; the fr-rows +
     reflowGridRows dance is the layout fix documented in $lib/reflow.

     The list row is `fit-content(45vh)`, NOT a fraction: it sizes to the table and
     scrolls past 45vh, so the session takes every pixel left down to the bottom edge. -->
<div
  class="grid min-h-0 min-w-0"
  style="grid-template-rows:{nav.lens === 'grid' ? 'minmax(0,1fr)' : 'fit-content(45vh) minmax(280px,1fr)'}"
  {@attach reflowGridRows}
>
  <!-- The sessions band. No card: it sits directly on the canvas, under the top
       bar that already names and counts it, and its only chrome is the hairline
       where the terminal band begins. A rounded panel here cost a border, a
       radius and 12px of padding to say something the top bar had already said.

       Never accented either: it used to carry an accent border + ring while the
       keyboard was driving it, which read as an error state on a surface the user
       looks at constantly. The lens switcher and the selected row say that. -->
  <div class="min-h-0 min-w-0 overflow-auto">
    {#if nav.lens === "list"}
      <SessionsTable />
    {:else if nav.lens === "kanban"}
      <SessionsKanban />
    {:else}
      <TerminalGrid />
    {/if}
  </div>

  <!-- `absolute inset-0`, NOT `fixed`: <main> is the nearest positioned ancestor and spans exactly
       the cockpit area, so this lands on the right rectangle by construction, under the top bar and
       under an out-of-date banner. A grid, so its one child fills it in WebKit. The sidebar stays
       visible: losing all navigation on Enter is disorienting. -->
  <div
    class={focused
      ? "absolute inset-0 z-30 grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)] bg-canvas"
      : nav.lens === "grid"
        ? "hidden"
        : "grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)] border-t border-edge bg-canvas shadow-[0_-12px_28px_-18px_rgba(0,0,0,0.85)]"}
    data-session-embed
    data-open={focused ? "" : undefined}
  >
    <SessionEmbed sessionId={shownId} />
</div>
</div>
