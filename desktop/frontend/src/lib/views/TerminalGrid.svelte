<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { triaged } from "$lib/filters";
  import { statusLabel } from "$lib/theme";
  import { TermService } from "@bindings/desktop";
  import SnapshotTile from "$lib/components/SnapshotTile.svelte";
  import SessionsEmpty from "$lib/components/SessionsEmpty.svelte";
  import StatusPill from "$lib/components/StatusPill.svelte";
  import LivePulse from "$lib/components/LivePulse.svelte";
  import Button from "$lib/components/Button.svelte";

  // Reads the store directly (leaf component) — the Cockpit view can't pass live
  // rows in the production WKWebView. See WKWEBVIEW_REACTIVITY in Cockpit.svelte.
  const rows = $derived(triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage));

  // The tmux-backed sessions we can actually render terminals for.
  const tiles = $derived(rows.filter((s) => s.tmuxName));

  // Opening a tile switches to the list lens (whose detail panel is what expands to
  // the fullscreen terminal) and focuses the session. Focus is a CSS state on that
  // detail terminal, not a separate mount — see SessionsColumn.svelte.
  function openTile(id: string) {
    nav.setLens("list");
    nav.select(id);
    nav.focusedTerm = id;
  }

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
  <SessionsEmpty>
    {#snippet idle()}
      <div class="flex h-full items-center justify-center text-faint">
        no live terminals — start a session to see it here
      </div>
    {/snippet}
  </SessionsEmpty>
{:else}
  <div
    class="grid h-full min-h-0 auto-rows-[minmax(150px,1fr)] content-start gap-2 overflow-auto p-2"
    style="grid-template-columns:repeat(auto-fill,minmax(280px,1fr))"
  >
    {#each tiles as s (s.id)}
      {@const sel = nav.selectedId === s.id}
      <!-- A dead / ended session keeps its last captured frame (the grid holds
           the frame on a capture failure by design), which looks live. Mark it
           so the stale snapshot reads as gone, and offer to revive it. -->
      {@const dead = s.status === "dead" || s.status === "session_ended"}
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
        onclick={() => openTile(s.id)}
        oncontextmenu={(e) => {
          nav.select(s.id);
          sessionMenu.open(s.id, e);
        }}
        onkeydown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            openTile(s.id);
          }
        }}
      >
        <div class="flex items-center gap-1.5 border-b border-edge/50 bg-panel/70 px-2 py-1 text-sm">
          <span class="truncate font-medium" class:text-accent-ink={sel}>{s.issue || s.id.slice(0, 8)}</span>
          <span class="truncate text-faint">{store.displayNameFor(s.project)}</span>
          <span class="ml-auto flex shrink-0 items-center gap-1.5">
            <LivePulse agentState={s.agentState} />
            <StatusPill status={s.status} />
          </span>
        </div>
        <div class="relative min-h-0 flex-1">
          <!-- Dim the frozen frame so it no longer passes for a live terminal. -->
          <div class="pointer-events-none h-full" class:opacity-40={dead}>
            <SnapshotTile text={snaps[s.tmuxName] ?? ""} />
          </div>
          {#if dead}
            <div class="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-canvas/50">
              <!-- Colour already separates this from the tile chrome; a weight
                   bump on top would be spending two tokens for one job. -->
              <span class="text-sm text-faint">{statusLabel(s.status)}</span>
              <!-- stopPropagation: reviving must not also open the (dead) terminal. -->
              <Button
                variant="secondary"
                onclick={(e) => {
                  e.stopPropagation();
                  store.revive(s.id);
                }}>Revive</Button
              >
            </div>
          {/if}
        </div>
        <div
          class="pointer-events-none flex items-center justify-end border-t border-edge/40 px-2 py-0.5 text-sm text-faint opacity-0 transition-opacity group-hover:opacity-100"
        >
          ⛶ open
        </div>
      </div>
    {/each}
  </div>
{/if}
