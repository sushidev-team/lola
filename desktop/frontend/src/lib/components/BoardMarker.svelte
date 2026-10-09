<script lang="ts">
  import { scale } from "svelte/transition";
  import { backOut } from "svelte/easing";

  // The ONE marker of the agent report: plan items, local checks and the
  // compact list chip all draw their state with it, so every state is the same
  // 16px drawn shape rather than a glyph from whichever font carries it (the old
  // ✓ ▸ · came from three fonts at three sizes — the dots read as specks).
  //
  // `live` turns motion on (breathing core + ripple for active, a turning arc
  // for running). The caller decides it — motion means "happening now", which
  // only the agent axis plus a fresh report can say. app.css stills all of it
  // under prefers-reduced-motion; every state keeps a distinct static shape.
  type Kind = "done" | "active" | "pending" | "pass" | "fail" | "running" | "blocked";
  let { kind, live = false, size = 16 }: { kind: Kind; live?: boolean; size?: number } = $props();
</script>

<span
  class="relative inline-flex shrink-0 items-center justify-center"
  style="width:{size}px;height:{size}px"
  aria-hidden="true"
>
  {#if kind === "done" || kind === "pass"}
    <span
      class="flex h-full w-full items-center justify-center rounded-full bg-good/20 text-good"
      in:scale={{ start: 0.4, duration: 260, easing: backOut }}
    >
      <svg viewBox="0 0 16 16" class="h-[62%] w-[62%]" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
        <path d="M3.5 8.5l3 3 6-7" />
      </svg>
    </span>
  {:else if kind === "fail"}
    <span
      class="flex h-full w-full items-center justify-center rounded-full bg-bad/20 text-bad"
      in:scale={{ start: 0.4, duration: 260, easing: backOut }}
    >
      <svg viewBox="0 0 16 16" class="h-[56%] w-[56%]" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round">
        <path d="M4.5 4.5l7 7M11.5 4.5l-7 7" />
      </svg>
    </span>
  {:else if kind === "blocked"}
    <span class="flex h-full w-full items-center justify-center rounded-full bg-orange/20 text-orange">
      <svg viewBox="0 0 16 16" class="h-[56%] w-[56%]" fill="currentColor">
        <rect x="4" y="3.5" width="2.6" height="9" rx="1" />
        <rect x="9.4" y="3.5" width="2.6" height="9" rx="1" />
      </svg>
    </span>
  {:else if kind === "active"}
    {#if live}<span class="ripple absolute inset-0 rounded-full border border-accent"></span>{/if}
    <span class="flex h-full w-full items-center justify-center rounded-full border-[1.5px] border-accent">
      <span class="h-[38%] w-[38%] rounded-full bg-accent" class:breathe={live}></span>
    </span>
  {:else if kind === "running"}
    <!-- A quarter arc on a faint track. It turns only while live; still, it
         reads as "in progress" by shape alone. -->
    <svg viewBox="0 0 16 16" class="h-full w-full text-info" class:spin={live} fill="none" stroke-width="1.75">
      <circle cx="8" cy="8" r="6.5" stroke="currentColor" opacity="0.25" />
      <path d="M8 1.5a6.5 6.5 0 0 1 6.5 6.5" stroke="currentColor" stroke-linecap="round" />
    </svg>
  {:else}
    <span class="h-[75%] w-[75%] rounded-full border-[1.5px] border-edge"></span>
  {/if}
</span>

<style>
  .breathe {
    animation: breathe 2.4s ease-in-out infinite;
  }
  @keyframes breathe {
    0%,
    100% {
      transform: scale(0.7);
      opacity: 0.65;
    }
    50% {
      transform: scale(1);
      opacity: 1;
    }
  }
  .ripple {
    animation: ripple 2.4s ease-out infinite;
  }
  @keyframes ripple {
    0% {
      transform: scale(1);
      opacity: 0.45;
    }
    70%,
    100% {
      transform: scale(1.9);
      opacity: 0;
    }
  }
  .spin {
    animation: spin 1.4s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
