<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { TermService } from "@bindings/desktop";
  import SnapshotTile from "$lib/components/SnapshotTile.svelte";
  import StatusPill from "$lib/components/StatusPill.svelte";

  let { rows }: { rows: SessionInfo[] } = $props();

  // The tmux-backed sessions we can actually render terminals for. Clicking a
  // tile sets nav.focusedTerm; the Cockpit then takes over with the big terminal.
  const tiles = $derived(rows.filter((s) => s.tmuxName));

  // Snapshot cache: session id → last capture-pane text.
  let snaps = $state<Record<string, string>>({});
  let timer: ReturnType<typeof setInterval> | undefined;
  let inflight = false;

  async function poll() {
    if (inflight || nav.focusedTerm) return; // skip while a live terminal is expanded
    const names = tiles.map((s) => s.tmuxName).filter(Boolean);
    if (names.length === 0) return;
    inflight = true;
    try {
      const out = await TermService.CaptureMany(names, 60);
      // out is keyed by tmux name (== session id).
      snaps = { ...snaps, ...(out as Record<string, string>) };
    } catch {
      /* a transient capture failure just leaves the last frame up */
    } finally {
      inflight = false;
    }
  }

  onMount(() => {
    poll();
    timer = setInterval(poll, 1400);
  });
  onDestroy(() => clearInterval(timer));
</script>

{#if tiles.length === 0}
  <div class="flex h-full items-center justify-center text-sm text-faint">
    no live terminals — start a session to see it here
  </div>
{:else}
  <div
    class="grid h-full min-h-0 auto-rows-[minmax(150px,1fr)] content-start gap-2 overflow-auto p-2"
    style="grid-template-columns:repeat(auto-fill,minmax(280px,1fr))"
  >
    {#each tiles as s (s.id)}
      {@const sel = nav.selectedId === s.id}
      <!--
        The whole tile is one click target that opens the live terminal. It must
        be a single click, not a double: the snapshot refreshes on a timer, and a
        re-render landing between the two clicks of a dblclick swallows it. Inner
        content is pointer-events-none so every click hits the stable tile.
      -->
      <div
        class="group relative flex min-h-0 cursor-pointer flex-col overflow-hidden rounded-lg border transition-colors hover:border-accent/70"
        class:border-accent={sel}
        class:border-edge={!sel}
        role="button"
        tabindex="0"
        title="open the live terminal"
        onclick={() => {
          nav.select(s.id);
          nav.focusedTerm = s.id;
        }}
        onkeydown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            nav.select(s.id);
            nav.focusedTerm = s.id;
          }
        }}
      >
        <div class="flex items-center gap-1.5 border-b border-edge/50 bg-panel/70 px-2 py-1 text-[11px]">
          <span class="truncate font-medium" class:text-accent={sel}>{s.issue || s.id.slice(0, 8)}</span>
          <span class="truncate text-faint">{s.project}</span>
          <span class="ml-auto shrink-0"><StatusPill status={s.status} dim /></span>
        </div>
        <div class="pointer-events-none min-h-0 flex-1">
          <SnapshotTile text={snaps[s.tmuxName] ?? ""} />
        </div>
        <div
          class="pointer-events-none flex items-center justify-end border-t border-edge/40 px-2 py-0.5 text-[10px] text-faint opacity-0 transition-opacity group-hover:opacity-100"
        >
          ⛶ open
        </div>
      </div>
    {/each}
  </div>
{/if}
