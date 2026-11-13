<script lang="ts" module>
  import type { Component } from "svelte";
  import ActivityIcon from "@mobile/lib/icons/ActivityIcon.svelte";
  import ProjectsIcon from "@mobile/lib/icons/ProjectsIcon.svelte";
  import SessionsIcon from "@mobile/lib/icons/SessionsIcon.svelte";
  import SettingsIcon from "@mobile/lib/icons/SettingsIcon.svelte";
  import type { Tab } from "@mobile/lib/nav.svelte";

  // The bottom bar: four destinations, drawn in the order the design draws
  // them (Figma node 42:2).
  //
  // The table is a module constant rather than markup repeated four times, but
  // NOTE what is not in it: no class strings. The active/resting colours differ
  // per state, not per tab, so they belong in the template where Tailwind can
  // see them as literals — a `text-${tone}` composed out of a table compiles to
  // nothing (CLAUDE.md's Button rule, and app.css's @source note for why the
  // failure is silent here in particular).
  //
  // The order is `TABS`' order and must stay in step with it: `TABS` is what a
  // link validates against and what a future keyboard shortcut would index
  // into, and a bar drawn in a different order than the vocabulary reads is a
  // bug nobody sees until they count.
  const ITEMS: { tab: Tab; label: string; icon: Component }[] = [
    { tab: "sessions", label: "Sessions", icon: SessionsIcon },
    { tab: "activity", label: "Activity", icon: ActivityIcon },
    { tab: "projects", label: "Projects", icon: ProjectsIcon },
    { tab: "settings", label: "Settings", icon: SettingsIcon },
  ];
</script>

<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { attentionCount } from "$lib/theme";
  import { nav } from "@mobile/lib/nav.svelte";

  // WHY IT READS THE SINGLETONS RATHER THAN TAKING PROPS. This is chrome: there
  // is exactly one of it, it is mounted by App.svelte and by nothing else, and
  // every fact it draws (which tab, how many sessions need a human) is already
  // module state that its one parent would only have to forward. The mobile
  // tests drive those singletons directly for the same reason Sessions.test.ts
  // does — a mocked store would let this component pass while the real one has
  // renamed a field underneath it.
  //
  // `attentionCount` is the SHARED count, over the legacy status word, and it
  // is deliberately not recomputed here: theme.ts is a port of Go's
  // internal/state pinned by desktop/state_parity_test.go, and a second
  // definition of "needs a human" on the phone is exactly the drift that
  // vocabulary exists to prevent. The Sessions header counts the same way, so
  // the badge and the subtitle can never disagree.
  const needsYou = $derived(attentionCount(store.sessions));
</script>

<!-- A <nav> of buttons, NOT role="tablist".
     ARIA tabs describe panels inside one document that are shown and hidden;
     these are four destinations, and a screen reader that announces "tab 2 of 4"
     for a whole section of the app is describing the wrong thing. `aria-current
     ="page"` is the pattern for "this is the destination you are on", and it is
     what iOS's own bar maps to.

     The bar PAYS THE BOTTOM INSET ITSELF (`pb-safe-b`), so the screens above it
     do not: a screen that also paid it would leave a band of canvas between its
     last row and a bar that is already clear of the home indicator. The ground
     and the border sit on the <nav> rather than on the row, so the inset strip
     under the tabs is crust too — an inset drawn in canvas reads as the bar
     floating.

     The row is exactly the design's 82pt box (`h-[82px] pt-2 pb-[22px]`), which
     leaves 52pt of target height: over Apple's 44 minimum, but `tap` states the
     guarantee rather than leaving it to be re-derived from three paddings. -->
<!-- THE BOTTOM INSET IS PAID ONCE, AND THE DESIGN ALREADY PAYS MOST OF IT. The
     Figma frame is 844 points tall, its tab bar starts at 762 and is 82 tall, so
     the bar runs to the very bottom edge and its own `pb-[22px]` IS the home
     indicator allowance — the frame draws the indicator inside that band. Adding
     `pb-safe-b` on top of it therefore counted the same space twice and came to
     about 116 points of chrome on a notched phone against Apple's own 83.

     `max()` is what pays it exactly once: the drawn 22 points on a device with
     no inset, and the real inset (34 on the notched phones) where there is one.
     The 59-point band above it — `pt-2` plus a 51-point row — is the design's
     and does not move, so the glyph and its label sit where the frame puts them
     on every device. -->
<nav
  class="shrink-0 border-t border-edge-soft bg-crust"
  style="padding-bottom: max(22px, env(safe-area-inset-bottom, 0px))"
  aria-label="Sections"
>
  <div class="grid h-[60px] grid-cols-4 pt-2">
    {#each ITEMS as item (item.tab)}
      {@const active = nav.tab === item.tab}
      {@const Icon = item.icon}
      <!-- The badge is the ONE thing on this bar a sighted user gets and a
           VoiceOver user would not, so the count goes into the button's name
           when it is showing — the same trade the sessions header's filter
           button makes. When there is nothing to say, the visible label IS the
           name and no aria-label is set: an aria-label duplicating the text it
           covers is one more string to keep in step for no gain. -->
      <button
        type="button"
        class="tap flex touch-manipulation flex-col items-center justify-center gap-1
               active:opacity-70 {active ? 'text-accent' : 'text-faint'}"
        aria-current={active ? "page" : undefined}
        aria-label={item.tab === "sessions" && needsYou > 0
          ? `Sessions — ${needsYou} need you`
          : undefined}
        onclick={() => nav.toTab(item.tab)}
      >
        <span class="relative flex">
          <Icon />
          {#if item.tab === "sessions" && needsYou > 0}
            <!-- Decoration: the state it marks is in the button's accessible
                 name above and in the Sessions header's subtitle.

                 THE RING IS WHAT MAKES IT A BADGE. A 9px dot laid straight over
                 a 20x22 glyph of the same weight reads as part of the glyph —
                 a smudge on the top rule of the list mark. A 2px ring in the
                 bar's own ground knocks it out of whatever it overlaps, which
                 is the same trick the Settings glyph's slider handles use (see
                 the icons README), and it works on every flavor because
                 `crust` is repainted with the theme by mobiletokens.ts.

                 `orange` rather than `bad`: this is the "waiting for you"
                 colour the whole design uses for attention, and `bad` is the
                 broken family — a badge in red would claim something failed. -->
            <span
              class="absolute -top-0.5 -right-1.5 size-[9px] rounded-full bg-orange ring-2 ring-crust"
              aria-hidden="true"
            ></span>
          {/if}
        </span>
        <span class="text-sm">{item.label}</span>
      </button>
    {/each}
  </div>
</nav>
