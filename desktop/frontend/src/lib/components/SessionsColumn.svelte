<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Panel from "./Panel.svelte";
  import SessionsTable from "./SessionsTable.svelte";
  import SessionsKanban from "./SessionsKanban.svelte";
  import SessionEmbed from "./SessionEmbed.svelte";
  import TerminalGrid from "$lib/views/TerminalGrid.svelte";
  import { reflowGridRows } from "$lib/reflow";

  // The cockpit's main column, extracted into its own component so Cockpit mounts
  // it directly (see WKWEBVIEW_REACTIVITY in Cockpit.svelte). Reads the live store
  // itself; the underlying reactivity is sound because the store no longer writes
  // sessions + activity in the same flush (see store.svelte.ts).
  const lensLabel = $derived(nav.lens === "list" ? "list" : nav.lens === "kanban" ? "kanban" : "grid");
  const count = $derived(store.sessions.length);

  // "Focus" (fullscreen) is a CSS state on the SAME detail terminal, NOT a
  // separate `{#if}` branch: mounting/unmounting a LiveTerminal on a toggle freezes
  // the template effect in WKWebView (see CockpitLayout.svelte). So the detail
  // SessionEmbed stays mounted and its wrapper simply becomes a fixed overlay.
  const focused = $derived(!!nav.focusedTerm);

  const lenses: { id: "list" | "kanban" | "grid"; icon: string; label: string }[] = [
    { id: "list", icon: "≡", label: "list" },
    { id: "kanban", icon: "▤", label: "board" },
    { id: "grid", icon: "▦", label: "terminals" },
  ];
</script>

<!-- a grid so panels stretch to full width AND height in WebKit; the fr-rows +
     reflowGridRows dance is the layout fix documented in $lib/reflow. -->
<div
  class="grid min-w-0 min-h-0 gap-2"
  style="grid-template-rows:{nav.lens === 'grid' ? 'minmax(0,1fr)' : 'minmax(0,2fr) minmax(0,3fr)'}"
  {@attach reflowGridRows}
>
  <Panel
    title={nav.scoped ? `Sessions · ${store.displayNameFor(nav.project)}` : "Sessions"}
    note={lensLabel}
    {count}
    focused
    pad={false}
  >
    {#snippet actions()}
      <span class="flex items-center gap-0.5 rounded border border-edge p-0.5">
        {#each lenses as l (l.id)}
          <button
            class="rounded px-1.5 py-[1px] text-[11px] font-normal"
            class:bg-accent={nav.lens === l.id}
            class:text-on-accent={nav.lens === l.id}
            class:text-faint={nav.lens !== l.id}
            title={l.label}
            onclick={() => (nav.lens = l.id)}>{l.icon}</button
          >
        {/each}
      </span>
    {/snippet}

    {#if nav.lens === "list"}
      <SessionsTable />
    {:else if nav.lens === "kanban"}
      <SessionsKanban />
    {:else}
      <TerminalGrid />
    {/if}
  </Panel>

  {#if nav.lens !== "grid"}
    <!-- Detail / live terminal. When focused, the wrapper becomes a fixed overlay
         covering the cockpit area (between the top bar and footer); the SessionEmbed
         instance is unchanged, so the terminal resizes without remounting. -->
    <div class={focused ? "fixed inset-x-0 top-9 bottom-8 z-30 flex min-h-0 p-2" : "contents"}>
      <Panel focused={focused} fill={focused} pad={false}>
        <SessionEmbed sessionId={nav.selectedId} {focused} />
      </Panel>
    </div>
  {/if}
</div>
