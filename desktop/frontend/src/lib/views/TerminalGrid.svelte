<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { triaged } from "$lib/filters";
  import { statusLabel } from "$lib/theme";
  import { terms, AGENT } from "$lib/terms.svelte";
  import { TermService } from "@bindings/desktop";
  import SnapshotTile from "$lib/components/SnapshotTile.svelte";
  import SessionsEmpty from "$lib/components/SessionsEmpty.svelte";
  import StatusPill from "$lib/components/StatusPill.svelte";
  import PrBadge from "$lib/components/PrBadge.svelte";
  import LivePulse from "$lib/components/LivePulse.svelte";
  import Button from "$lib/components/Button.svelte";

  // Reads the store directly (leaf component) — the Cockpit view can't pass live
  // rows in the production WKWebView. See WKWEBVIEW_REACTIVITY in Cockpit.svelte.
  const rows = $derived(triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage));

  // The tmux-backed sessions we can actually render terminals for.
  const tiles = $derived(rows.filter((s) => s.tmuxName));

  // Which pane a tile previews: the session's ACTIVE tab, exactly as the detail
  // panel resolves it (SessionEmbed does the same three lines). That is also why
  // the tab state lives in $lib/terms and not here — switching a tile to "Shell 2"
  // is the same fact as switching the detail panel to it, so opening the terminal
  // lands on the pane you were watching, and the choice survives the lens switch,
  // the selection moving away and back, and the tile being re-rendered.
  function paneName(s: { id: string; tmuxName: string }): string {
    const tab = terms.activeTab(s.id);
    return tab === AGENT ? s.tmuxName : tab;
  }

  // Opening a tile focuses the session's live terminal. `toggleFocusTerm` is what
  // leaves the grid for the list lens (only that lens mounts a terminal) AND what
  // records the way back, so minimizing returns to the grid — see nav.returnLens.
  function openTile(id: string) {
    nav.select(id);
    nav.toggleFocusTerm(id);
  }

  // Snapshot cache: tmux pane name → last capture-pane text.
  let snaps = $state<Record<string, string>>({});
  let timer: ReturnType<typeof setInterval> | undefined;
  let shellTimer: ReturnType<typeof setInterval> | undefined;
  let inflight = false;

  async function poll() {
    if (inflight || nav.focusedTerm) return; // skip while a live terminal is expanded
    const names = tiles.map(paneName).filter(Boolean);
    if (names.length === 0) return;
    inflight = true;
    try {
      const out = await TermService.CaptureMany(names, 60);
      // out is keyed by tmux name — the agent pane's, or the shell/review tab's.
      snaps = { ...snaps, ...(out as Record<string, string>) };
    } catch {
      /* a transient capture failure just leaves the last frame up */
    } finally {
      inflight = false;
    }
  }

  // Tabs are DISCOVERED from the tmux server, so a shell opened in the TUI, in the
  // detail panel, or by a review pass appears on the tile within a few seconds.
  // Same 4s cadence the detail panel uses, and the same idempotent call — but here
  // it runs for every tile, which is why it is not folded into the 1.4s capture
  // loop: one `tmux ls` per session per 4s, not per 1.4s.
  function refreshShells() {
    if (nav.focusedTerm) return;
    for (const s of tiles) void terms.refresh(s.id);
  }

  onMount(() => {
    poll();
    refreshShells();
    timer = setInterval(poll, 1400);
    shellTimer = setInterval(refreshShells, 4000);
  });
  onDestroy(() => {
    clearInterval(timer);
    clearInterval(shellTimer);
  });
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
  <!-- A TERMINAL is the unit here, so the tile is shaped like one: wide enough for
       an 80-column line and only as tall as the tail worth reading. Three numbers
       do that, and each replaced one that fought it:

       auto-FIT, not auto-fill: auto-fill kept laying down empty 280px tracks, so
       three sessions on a 1500px window sat in a narrow left-hand ribbon with
       half the band blank. auto-fit collapses the empty tracks and lets the real
       tiles take the width.

       minmax(min(100%,32rem),1fr): 32rem is a whole terminal line at the tile
       font (see SnapshotTile's scale) instead of the old 280px half-line that cut
       every sentence mid-word. The min() keeps it from overflowing a window
       narrower than one tile.

       A FIXED row height, not `minmax(150px,1fr)`: a 1fr row stretched the only
       row to the full band, which is what made the tiles tall columns of mostly
       stale scrollback. Capped at 26rem they stay landscape while showing a real
       stretch of the conversation, and the snapshot inside them is bottom-anchored
       so the height is spent on the NEWEST output.

       Full-bleed and gapless: tiles are told apart by a hairline, like the kanban
       columns and the cockpit's bands — no gutters, no radius, no card. -->
  <div
    class="grid h-full min-h-0 content-start overflow-auto"
    style="grid-template-columns:repeat(auto-fit,minmax(min(100%,32rem),1fr));grid-auto-rows:clamp(16rem,42vh,26rem)"
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
      {@const tabs = terms.shellsFor(s.id)}
      {@const tab = terms.activeTab(s.id)}
      <!-- The tile is a rectangle of terminal (`panel`) on the canvas, divided
           from its neighbours by a hairline on two sides — not a bordered,
           rounded card. It carries NO selection ring: in a wall of live terminals
           an accent frame around one of them reads as an alarm on that session,
           and the grid is a monitor, not a list you are stepping through. The
           selected tile still tints its issue key, which is all the marker a
           surface you open by clicking needs. -->
      <div
        class="group relative flex min-h-0 cursor-pointer flex-col overflow-hidden border-r border-b border-edge/40 bg-panel"
        role="button"
        tabindex="0"
        title="open the live terminal"
        onclick={() => openTile(s.id)}
        oncontextmenu={(e) => {
          nav.select(s.id);
          sessionMenu.open(s.id, e);
        }}
        onkeydown={(e) => {
          // Only the tile's OWN keypresses open it. The header carries real
          // controls now (the tab strip, the PR link), and a bubbled Enter from
          // one of those would activate the control AND open the terminal.
          if (e.target !== e.currentTarget) return;
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            openTile(s.id);
          }
        }}
      >
        <!-- The header carries the row's identity at the app's base size now that
             a tile is wide enough for it: key, then TITLE (what the work is —
             the tiles used to name a project four times over and the work not at
             once), then the metadata tier on the right. It tints on hover, which
             is also where the ⛶ affordance appears — the old always-present
             hover footer spent a whole row of tile height on it. -->
        <div class="flex items-center gap-2 border-b border-edge/50 px-2.5 py-1.5 transition-colors group-hover:bg-sel/60">
          <span class="shrink-0 font-medium" class:text-accent-ink={sel}>{s.issue || s.id.slice(0, 8)}</span>
          {#if s.title}<span class="min-w-0 flex-1 truncate text-faint">{s.title}</span>{/if}
          <span class="ml-auto flex shrink-0 items-center gap-2">
            <span class="truncate text-sm text-faint">{store.displayNameFor(s.project)}</span>
            <LivePulse agentState={s.agentState} />
            <StatusPill
              agentState={s.agentState}
              inputReason={s.inputReason}
              delivery={s.delivery}
              status={s.status}
              interpreted={s.interpretedState}
            />
            <!-- The tiles had no PR surface at all: the one lens you sit and
                 watch a build from was the one that never told you a PR existed,
                 let alone that its CI had gone red. The tile is a click-to-open
                 <div role="button">, not a <button>, so the badge may render the
                 real control (PrBadge stops the click from reaching the tile). -->
            {#if s.prNumber > 0}
              <PrBadge session={s} delivery={s.delivery} onOpen={() => store.openURL(s.prUrl)} />
            {/if}
            <span class="text-sm text-faint opacity-0 transition-opacity group-hover:opacity-100" aria-hidden="true"
              >⛶</span
            >
          </span>
        </div>
        {#if tabs.length}
          <!-- A session's OTHER panes — the shells it has open and the review pass's
               pane — are previewable here too, so the grid can be pointed at the
               thing you actually want to watch (a test run in Shell 2, the review
               that just opened) instead of only ever at the agent.
               The strip is the detail panel's, at tile scale: same recessed band,
               same filled active cell, same labels from terms.labelFor. It is NOT
               a tab editor — no rename, no drag, no ×, no "+ Shell". Those belong
               to the panel that owns the terminal; here a tab is a channel switch.
               Hidden entirely when the agent pane is the only one, so an ordinary
               session's tile stays chrome-free.
               stopPropagation on each button, not on a wrapper: the tile itself is
               the click target that opens the terminal, and a <button> already
               satisfies the interaction a11y rules a clickable <div> would not. -->
          <div
            class="relative z-10 flex items-stretch overflow-x-auto border-b border-edge/40 bg-[color-mix(in_srgb,var(--color-panel)_88%,var(--color-canvas))] select-none"
          >
            <button
              type="button"
              aria-pressed={tab === AGENT}
              class="h-6 shrink-0 border-r border-edge/40 px-2.5 text-sm transition-colors {tab === AGENT
                ? 'bg-sel font-medium text-ink'
                : 'text-faint hover:bg-sel/50 hover:text-ink'}"
              onclick={(e) => {
                e.stopPropagation();
                terms.select(s.id, AGENT);
              }}
            >
              Agent
            </button>
            {#each tabs as t (t)}
              <button
                type="button"
                aria-pressed={tab === t}
                class="h-6 shrink-0 border-r border-edge/40 px-2.5 text-sm transition-colors {tab === t
                  ? 'bg-sel font-medium text-ink'
                  : 'text-faint hover:bg-sel/50 hover:text-ink'}"
                onclick={(e) => {
                  e.stopPropagation();
                  terms.select(s.id, t);
                }}
              >
                {terms.labelFor(s.id, t)}
              </button>
            {/each}
          </div>
        {/if}
        <div class="relative min-h-0 flex-1">
          <!-- Dim the frozen frame so it no longer passes for a live terminal. -->
          <div class="pointer-events-none h-full" class:opacity-40={dead}>
            <SnapshotTile text={snaps[paneName(s)] ?? ""} />
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
      </div>
    {/each}
  </div>
{/if}
