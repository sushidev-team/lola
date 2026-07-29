<script lang="ts">
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { KANBAN_COLUMNS, statusBadge, statusText } from "$lib/theme";
  import PrBadge from "./PrBadge.svelte";
  import SessionsEmpty from "./SessionsEmpty.svelte";

  // Reads the store directly (leaf component) — the Cockpit view can't pass live
  // rows in the production WKWebView. See WKWEBVIEW_REACTIVITY in Cockpit.svelte.
  const rows = $derived(scopedSessions(store.sessions, nav.scoped, nav.project));
  const cols = $derived(
    KANBAN_COLUMNS.map((c) => ({
      title: c.title,
      items: rows.filter((s) => c.statuses.includes(s.status)),
    })),
  );
</script>

{#if store.connected && store.alive}
  <div class="flex h-full min-h-0 gap-2 overflow-x-auto p-1">
    {#each cols as col (col.title)}
      <!-- min-h-0 so the card list below can bound itself and scroll rather than
           stretching the column past the panel and clipping its bottom cards. -->
      <div class="flex min-h-0 min-w-[13rem] flex-1 flex-col">
        <div class="mb-1 flex items-center gap-1.5 border-b border-edge/60 pb-1 text-xs font-semibold">
          <span>{col.title}</span><span class="text-faint">{col.items.length}</span>
        </div>
        <!-- min-h-0 flex-1: a tall column scrolls inside itself; without them it
             overflows the panel and the last cards are cut off. -->
        <div class="flex min-h-0 flex-1 flex-col gap-1 overflow-auto">
          {#each col.items as s (s.id)}
            {@const sel = nav.selectedId === s.id}
            <button
              class="rounded border px-2 py-1 text-left text-xs transition-colors hover:border-accent/60"
              class:border-accent={sel}
              class:border-edge={!sel}
              class:bg-sel={sel}
              onclick={() => nav.select(s.id)}
              ondblclick={() => nav.toggleFocusTerm(s.id)}
              oncontextmenu={(e) => {
                nav.select(s.id);
                sessionMenu.open(s.id, e);
              }}
            >
              <div class="flex items-center gap-1.5">
                {#if s.status === "needs_input" && !sel}<span class="text-warn">!</span>{/if}
                <span class="font-medium" class:text-accent-ink={sel}>{s.issue || s.id.slice(0, 8)}</span>
                <span class="ml-auto font-mono text-[10px] {statusText(s.status)}">{statusBadge(s.status)}</span>
              </div>
              {#if s.title}<div class="truncate text-[11px] text-faint">{s.title}</div>{/if}
              <div class="mt-0.5"><PrBadge session={s} /></div>
            </button>
          {/each}
        </div>
      </div>
    {/each}
  </div>
{:else}
  <!-- Empty columns look identical whether the daemon is dead or connecting, so
       hand off to the shared placeholder rather than showing a silent board. -->
  <SessionsEmpty />
{/if}
