<script lang="ts">
  import { fly } from "svelte/transition";
  import { cubicOut } from "svelte/easing";
  import Button from "$lib/components/Button.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import type { IconName } from "$lib/icons";
  import { store } from "$lib/store.svelte";

  // The transient action result ("Killed eng-123", "Config reloaded", an error).
  // It used to live in the footer, where a message appearing and vanishing made
  // a permanent row twitch and pushed the hint text around. As a corner toast it
  // costs no layout at all.
  //
  // No timer here: store.setFlash clears itself (4s, 8s for an error), hovering
  // holds it, and a second timer would only be a second thing to get out of sync.
  //
  // z-45 sits above the z-40 modal backdrop — an action fired from inside a
  // guarded config form still reports — but below SessionMenu's z-50 popup.

  const ICON: Record<"good" | "warn" | "bad", IconName> = {
    good: "circle-check",
    warn: "circle-alert",
    bad: "circle-x",
  };
  const TONE = {
    good: "text-good",
    warn: "text-warn",
    bad: "text-bad",
  } as const;
  const ACCENT = {
    good: "bg-good",
    warn: "bg-warn",
    bad: "bg-bad",
  } as const;

  const reduced = typeof window !== "undefined" && !!window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches;
</script>

<!-- The live region is ALWAYS in the DOM and only its contents change. A
     role="status" created together with its text is commonly missed by screen
     readers — the region has to exist before the announcement is written into
     it. The wrapper is inert and invisible when there is no flash. -->
<div
  class="pointer-events-none fixed right-4 bottom-4 z-[45] w-[min(24rem,calc(100vw-2rem))] empty:hidden"
  role="status"
  aria-live="polite"
>
  {#if store.flash}
    {@const flash = store.flash}
    {#key flash}
      <div
        class="toast pointer-events-auto relative grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-2.5 overflow-hidden rounded-lg border border-edge bg-panel py-2.5 pr-2 pl-4 text-sm text-ink"
        role="presentation"
        onmouseenter={() => store.holdFlash(true)}
        onmouseleave={() => store.holdFlash(false)}
        in:fly={{ y: 12, duration: reduced ? 0 : 180, easing: cubicOut }}
        out:fly={{ y: 8, duration: reduced ? 0 : 140 }}
      >
        <span class="absolute inset-y-0 left-0 w-1 {ACCENT[flash.kind]}" aria-hidden="true"></span>
        <span class="mt-px {TONE[flash.kind]}"><Icon name={ICON[flash.kind]} size={16} /></span>
        <span class="selectable min-w-0 py-px break-words">{flash.text}</span>
        <Button size="xs" icon aria-label="Dismiss" onclick={() => store.dismissFlash()}>
          <Icon name="x" size={14} />
        </Button>
      </div>
    {/key}
  {/if}
</div>

<style>
  .toast {
    /* Depth so the toast reads as floating above the panes. */
    box-shadow: 0 8px 24px -8px rgb(0 0 0 / 0.45), 0 2px 6px -2px rgb(0 0 0 / 0.25);
  }
</style>
