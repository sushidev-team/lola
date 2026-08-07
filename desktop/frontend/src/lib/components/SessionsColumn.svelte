<script lang="ts">
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

  // "Focus" (fullscreen) is a CSS state on the SAME detail terminal, NOT a
  // separate `{#if}` branch: mounting/unmounting a LiveTerminal on a toggle freezes
  // the template effect in WKWebView (see CockpitLayout.svelte). So the detail
  // SessionEmbed stays mounted and its wrapper simply becomes a fixed overlay.
  const focused = $derived(!!nav.focusedTerm);
</script>

<!-- a grid so panels stretch to full width AND height in WebKit; the fr-rows +
     reflowGridRows dance is the layout fix documented in $lib/reflow.

     The list row is `fit-content(45vh)`, NOT a fraction: a fixed 2fr/3fr split
     left a half-empty sessions panel whenever there were only a handful of
     sessions, and pinned the terminal to a fraction of the column instead of the
     bottom. fit-content sizes the row to the table's own height and caps it at
     45vh, past which the table scrolls inside its panel — so the terminal always
     takes every remaining pixel down to the bottom edge, and short lists give it
     more of them. The terminal row keeps `minmax(0,1fr)` so it absorbs the rest. -->
<div
  class="grid min-h-0 min-w-0"
  style="grid-template-rows:{nav.lens === 'grid' ? 'minmax(0,1fr)' : 'fit-content(45vh) minmax(0,1fr)'}"
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

  {#if nav.lens !== "grid"}
    <!-- Detail / live terminal. When focused, the wrapper becomes an overlay
         covering the cockpit area; the SessionEmbed instance is unchanged, so the
         terminal resizes without remounting. The sidebar stays visible on purpose
         — losing all navigation on Enter is disorienting, and a terminal running
         under the traffic lights looks broken.

         `absolute inset-0`, NOT `fixed` offset by --app-sidebar-w: <main> is the
         nearest positioned ancestor and already spans exactly the cockpit area,
         so the overlay lands on the right rectangle by construction instead of by
         arithmetic. The fixed version was pinned at `top-11` — the top bar's
         height alone — so whenever PushErrorBanner occupied its row the focused
         terminal covered the "daemon is out of date" alert. -->
    <div class={focused ? "absolute inset-0 z-30 flex min-h-0" : "contents"}>
      <!-- The terminal band is a SHEET the list slides under, and that is the only
           reason the two bands are told apart: a hairline between two tones four
           values apart is not a boundary, it is a table rule, which is exactly how
           the detail header read next to the row it describes.
           Three things do the work, and none of them costs space: the band is the
           terminal's own `base` (the widest step the palette has from the canvas
           the list sits on), the rule above it is full-strength `edge` rather than
           edge/60, and it casts a soft shadow UPWARD over the list. Elevation is
           also the honest description — this band is a live terminal, not another
           row of the table.
           Focused, it covers the cockpit area edge to edge and takes an accent
           hairline: nothing is inset, because a fullscreen terminal floating 12px
           off the window read as a dialog. -->
      <div
        class="flex w-full min-h-0 min-w-0 flex-col overflow-hidden border-t bg-panel shadow-[0_-12px_28px_-18px_rgba(0,0,0,0.85)]"
        class:flex-1={focused}
        class:border={focused}
        class:border-accent={focused}
        class:border-edge={!focused}
      >
        <SessionEmbed sessionId={nav.selectedId} {focused} />
      </div>
    </div>
  {/if}
</div>
