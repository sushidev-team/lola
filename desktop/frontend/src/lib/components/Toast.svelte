<script lang="ts">
  import { cubicOut, cubicIn } from "svelte/easing";
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

  // No motion for reduced-motion users, and none where the Web Animations API
  // is missing (jsdom): Svelte drives transitions through element.animate.
  const reduced =
    typeof window === "undefined" ||
    typeof Element.prototype.animate !== "function" ||
    !!window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches;

  // |global on both: the toast's nearest block is the {#key} that lives and
  // dies with the {#if}, and a local transition does not play when its block
  // is created or destroyed by an ancestor — the first toast and its exit
  // would just snap. A new message (key change) cross-fades the same way.
  // Rise + fade + a hint of scale on the way in; a quicker drop on the way out.
  function enter(_node: Element) {
    return {
      duration: reduced ? 0 : 220,
      easing: cubicOut,
      css: (t: number, u: number) =>
        `opacity:${t}; transform: translateY(${u * 14}px) scale(${0.96 + 0.04 * t});`,
    };
  }
  function leave(_node: Element) {
    return {
      duration: reduced ? 0 : 160,
      easing: cubicIn,
      css: (t: number, u: number) =>
        `opacity:${t}; transform: translateY(${u * 8}px) scale(${0.98 + 0.02 * t});`,
    };
  }
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
        onfocusin={() => store.holdFlash(true)}
        onfocusout={() => store.holdFlash(false)}
        in:enter|global
        out:leave|global
      >
        <span class="absolute inset-y-0 left-0 w-1 {ACCENT[flash.kind]}" aria-hidden="true"></span>
        <span class="mt-px {TONE[flash.kind]}"><Icon name={ICON[flash.kind]} size={16} /></span>
        <div class="selectable min-w-0 py-px break-words">
          <span class:font-medium={!!flash.description}>{flash.text}</span>
          {#if flash.description}<p class="mt-1 text-ink-2">{flash.description}</p>{/if}
          {#if flash.action}<Button size="xs" variant="secondary" class="mt-2" onclick={() => {const action=flash.action; store.dismissFlash(); void action?.run();}}>{flash.action.label}</Button>{/if}
        </div>
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
