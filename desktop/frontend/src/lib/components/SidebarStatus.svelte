<script lang="ts">
  import GlyphIcon from "$lib/components/GlyphIcon.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { hub } from "$lib/hub.svelte";
  import { onDestroy, onMount } from "svelte";
  import Button from "./Button.svelte";

  // The Team icon carries the Needs-you count, so a failed or taken chore is
  // seen without opening the Team view. This row is always on screen, so it
  // holds the hub store's LIGHT watch (a status read a minute, the board every
  // few minutes) and routes the store's off-screen notices to the toast.
  let releaseHub: (() => void) | null = null;
  onMount(() => {
    hub.onNotice = (n) => store.setFlash(n.text, n.kind);
    releaseHub = hub.watchLight();
  });
  onDestroy(() => {
    releaseHub?.();
    hub.onNotice = null;
  });
  const needs = $derived(hub.needsCount);

  // The pinned utility row: everything the old footer carried (daemon liveness,
  // start/restart/stop) plus the vitals bar's health dot
  // and settings gear, in one 44px strip at the foot of the sidebar. The flash
  // moved out to <Toast> — a transient message in a permanent row made the row
  // twitch, and it was the one thing there that had no home of its own.
  //
  // Reads the store directly (leaf component) — see Sidebar.svelte.

  const health = $derived(
    store.status ? `runtime ${store.status.runtimeOk ? "✓" : "✗"} · linear ${store.status.linearOk ? "✓" : "✗"}` : "daemon health unknown",
  );
  // The chip merges the vitals dot with the footer's liveness pill, so the one
  // place that says "is lola alive" is also the way into the doctor report.
  //
  // It reports LIVENESS *and* HEALTH, not liveness alone. The deleted VitalsBar
  // drove a persistent red ▲ off `runtimeOk && linearOk`; folding that into a
  // tooltip meant a dead Linear key — the exact silent failure this app exists to
  // surface — rendered a green "● running" and nothing else. A degraded daemon is
  // therefore warn-coloured and carries the ▲ glyph, so the alert survives at a
  // glance and the tooltip only says WHICH half is down.
  const healthOk = $derived(!!store.status && store.status.runtimeOk && store.status.linearOk);
  const degraded = $derived(store.alive && !healthOk);
  // Trailing `!`: this colour must beat the <Button> variant's own `text-faint`,
  // and two equal-specificity utilities are resolved by Tailwind's order in the
  // compiled sheet rather than by the order in the class attribute.
  const daemonCls = $derived(
    !store.connected ? "text-faint!" : !store.alive ? "text-bad!" : healthOk ? "text-good!" : "text-warn!",
  );
  const daemonText = $derived(
    store.alive ? (healthOk ? "Running" : "Degraded") : store.connected ? "Down" : "Connecting…",
  );
</script>

<div class="group/status flex h-11 items-center gap-1 border-t border-edge px-3 text-sm">
  <Button class="min-w-0 shrink! {daemonCls}" title="{health} · open doctor (d)" onclick={() => nav.openOverlay("doctor")}>
    <span aria-hidden="true"><GlyphIcon glyph={degraded ? "▲" : store.alive ? "●" : "○"} /></span>
    <span class="truncate">{daemonText}</span>
  </Button>

  {#if store.alive}
    <!-- Revealed on row hover: two low-frequency, one-way controls that should
         not sit permanently under the cursor's path. -->
    <!-- Drawn as SVG, not the ⟳ / ■ glyphs they used to be: a text glyph is sized
         by the 12px font of this row, so both controls rendered visibly smaller
         than the 16px gear sitting a few pixels away. An explicit h-4 w-4 puts all
         three icons on the same optical size regardless of the type scale. -->
    <!-- COLLAPSED to zero width at rest, not merely transparent: an invisible
         control that still occupies its slot stole the room the daemon line
         needs, so a status as ordinary as "Connecting…" arrived truncated. The
         row now gives that line everything until the pointer arrives, and only
         then makes space — which is also why the status button may shrink
         (shrink! above) rather than being pushed past the sidebar's edge. -->
    <span
      class="flex max-w-0 items-center overflow-hidden opacity-0 transition-all duration-150 group-hover/status:max-w-16 group-hover/status:opacity-100 focus-within:max-w-16 focus-within:opacity-100"
    >
      <Button icon title="restart daemon" aria-label="Restart daemon" onclick={() => store.restartDaemon()}>
        <Icon name="refresh-cw" class="h-4 w-4" />
      </Button>
      <!-- Asks first: stopping the daemon halts every poll. -->
      <Button variant="danger" icon title="stop daemon" aria-label="Stop daemon" onclick={() => store.askStopDaemon()}>
        <Icon name="square" class="h-4 w-4" />
      </Button>
    </span>
  {:else if store.connected}
    <!-- Persistent, not hover-revealed: with the daemon down this is the only
         thing on the screen worth clicking. -->
    <Button variant="secondary" onclick={() => store.startDaemon()}>Start</Button>
  {/if}

  <!-- Help + settings, pinned to the right edge: two overlays that belong to the
       app rather than to the daemon, so they sit apart from the status line and
       land in the same corner every time. The daemon chip is still the row's
       only shrinkable item if the worst case (a long status + the wider
       "↑ Update" chip + revealed hover controls) runs out of room. -->
  <span class="ml-auto flex shrink-0 items-center gap-0.5">
    <Button icon class="relative" title={needs ? `Team (T) · ${needs} ${needs === 1 ? "chore needs" : "chores need"} you` : "Team (T)"}
      aria-label={needs ? `Team, ${needs} ${needs === 1 ? "chore needs" : "chores need"} you` : "Team"} onclick={() => nav.goTeam()}>
      <Icon name="users" size={14} />
      {#if needs}<span class="num absolute -right-1 -top-1 min-w-4 rounded-full bg-bad px-1 text-center text-[10px] font-semibold leading-4 text-on-bad" aria-hidden="true" data-team-badge>{needs > 9 ? "9+" : needs}</span>{/if}
    </Button>
    <Button icon title="keyboard shortcuts (?)" aria-label="Keyboard shortcuts" onclick={() => nav.openOverlay("help")}>
      <Icon name="circle-help" size={14} /></Button>
    <Button icon title="Settings" aria-label="Settings" onclick={() => nav.openOverlay("settings")}>
      <Icon name="settings" class="h-4 w-4" />
    </Button>
  </span>
</div>
