<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { updates } from "$lib/update.svelte";
  import { nav } from "$lib/nav.svelte";
  import Button from "./Button.svelte";

  // The pinned utility row: everything the old footer carried (daemon liveness,
  // start/restart/stop, version + update badge) plus the vitals bar's health dot
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
    <span aria-hidden="true">{degraded ? "▲" : store.alive ? "●" : "○"}</span>
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
        <svg
          viewBox="0 0 24 24"
          class="h-4 w-4"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M21 12a9 9 0 1 1-2.64-6.36" />
          <path d="M21 3v6h-6" />
        </svg>
      </Button>
      <!-- Asks first: stopping the daemon halts every poll. -->
      <Button variant="danger" icon title="stop daemon" aria-label="Stop daemon" onclick={() => store.askStopDaemon()}>
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="currentColor" aria-hidden="true">
          <rect x="6" y="6" width="12" height="12" rx="2" />
        </svg>
      </Button>
    </span>
  {:else if store.connected}
    <!-- Persistent, not hover-revealed: with the daemon down this is the only
         thing on the screen worth clicking. -->
    <Button variant="secondary" onclick={() => store.startDaemon()}>Start</Button>
  {/if}

  <!-- Padding here is shaved deliberately: at 248px the row's worst case
       (daemon chip + its two hover controls + help + settings + the wider
       "↑ update" chip) is within a few px of the available width, and the
       daemon chip is the only item that can shrink. -->
  <span class="ml-auto flex items-center gap-0.5">
    <Button icon title="keyboard shortcuts (?)" aria-label="Keyboard shortcuts" onclick={() => nav.openOverlay("help")}>
      ?
    </Button>
    <Button icon title="Settings (S)" aria-label="Settings" onclick={() => nav.openOverlay("settings")}>
      <svg
        viewBox="0 0 24 24"
        class="h-4 w-4"
        fill="none"
        stroke="currentColor"
        stroke-width="1.8"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <circle cx="12" cy="12" r="3" />
        <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
      </svg>
    </Button>
    {#if updates.available}
      <Button
        variant="secondary"
        size="xs"
        class="num border-accent! text-accent-ink!"
        title="update available: v{updates.info?.latestVersion}"
        onclick={() => nav.openOverlay("update")}
      >
        <span aria-hidden="true">↑</span> Update
      </Button>
    {:else}
      <Button size="xs" class="num" title="check for updates" onclick={() => nav.openOverlay("update")}>
        v{updates.version}
      </Button>
    {/if}
  </span>
</div>
