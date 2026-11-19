<script lang="ts" module>
  import type { Component } from "svelte";
  import { kanbanKey } from "$lib/theme";
  import ProjectsIcon from "@mobile/lib/icons/ProjectsIcon.svelte";
  import SessionsIcon from "@mobile/lib/icons/SessionsIcon.svelte";
  import SettingsIcon from "@mobile/lib/icons/SettingsIcon.svelte";
  import { copy } from "@mobile/lib/copy";
  import { TABS, type Tab } from "@mobile/lib/router.svelte";

  const ICONS: Record<Tab, Component> = {
    inbox: SessionsIcon,
    projects: ProjectsIcon,
    settings: SettingsIcon,
  };
  const LABELS: Record<Tab, string> = {
    inbox: copy.inbox.title,
    projects: copy.projects.title,
    settings: copy.settings.title,
  };

  /** Sessions the Inbox shows as "Needs you" cards (spec §5.2): a blocked or regressed one, or an approved PR. */
  export function needsYouCount(
    sessions: readonly { agentState?: string; delivery?: string }[],
  ): number {
    let n = 0;
    for (const s of sessions) {
      const k = kanbanKey(s.agentState, s.delivery);
      if (k === "needs" || k === "fixing" || s.delivery === "approved") n++;
    }
    return n;
  }
</script>

<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { haptic } from "@mobile/lib/haptics";
  import { router } from "@mobile/lib/router.svelte";

  // The bottom bar: three destinations in `TABS` order, drawn on the tab shell
  // only (never on a session, never on Connect).
  //
  // Invariants:
  //  - A <nav> of buttons with `aria-current="page"`, not role="tablist": these
  //    are destinations, not panels of one document.
  //  - The bar is 49pt plus the bottom inset (the home indicator's allowance and
  //    nothing else), so the screens above it do not pay it. It is translucent
  //    canvas over a blur; the selected tab draws its glyph filled.
  //  - The badge is a count capsule on the Inbox glyph: the Inbox's "Needs you"
  //    cards, the one number that says something is waiting; it is part of the
  //    button's name when showing.
  //  - Changing tab replaces the route: each tab keeps its own depth only while
  //    it is the top of the stack.

  const needs = $derived(needsYouCount(store.sessions));
  const current = $derived(router.top.name === "tab" ? router.top.tab : null);

  function go(tab: Tab): void {
    if (tab === current) return;
    haptic("select");
    router.replace({ name: "tab", tab });
  }
</script>

<nav
  class="shrink-0 border-t-[0.5px] border-edge-soft"
  style="padding-bottom: env(safe-area-inset-bottom, 0px); background: color-mix(in srgb, var(--color-canvas) 80%, transparent); backdrop-filter: blur(20px) saturate(180%); -webkit-backdrop-filter: blur(20px) saturate(180%)"
  aria-label="Sections"
>
  <div class="grid h-[49px] grid-cols-3">
    {#each TABS as tab (tab)}
      {@const active = current === tab}
      {@const Icon = ICONS[tab]}
      <button
        type="button"
        class="tap flex touch-manipulation flex-col items-center justify-center gap-0.5 active:opacity-70 {active
          ? 'text-accent'
          : 'text-tab-idle'}"
        aria-current={active ? "page" : undefined}
        aria-label={tab === "inbox" && needs > 0 ? `${LABELS.inbox}, ${needs} need you` : undefined}
        onclick={() => go(tab)}
      >
        <span class="relative flex">
          <!-- The inbox tray stays the outline glyph when selected, matching the other tabs. -->
          <Icon filled={active && tab !== "inbox"} size={26} />
          {#if tab === "inbox" && needs > 0}
            <!-- An 18px count circle with a 2px crust ring, its centre 10px right of and its top 6px above the
                 glyph's top-right, so it never covers more than a corner. `on-bad` is the measured
                 label for the `bad` fill (white on latte, dark on the dark flavors). -->
            <span
              class="absolute flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-bad px-1 text-[11px] leading-none font-bold text-on-bad tabular-nums ring-2 ring-crust"
              style="top: -4px; left: calc(100% - 8px)"
              data-badge
              aria-hidden="true"
            >
              {needs > 99 ? "99+" : needs}
            </span>
          {/if}
        </span>
        <span class="text-[10px] leading-[12px] font-medium">{LABELS[tab]}</span>
      </button>
    {/each}
  </div>
</nav>
