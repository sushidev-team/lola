<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { haptic } from "@mobile/lib/haptics";

  // The phone's surface for `store.flash`: every shared action (`store.act`,
  // `kill`, `dev`, checkpoints, feedback) reports its outcome there, and a
  // refusal is shown as the daemon's sentence.
  //
  // Invariants: the live region is always in the DOM and only its contents
  // change; a refusal plays the refusal haptic once per flash;
  // it sits above sheets, so a sheet that stays open still shows why; the store
  // owns the timeout.

  const TONE = {
    good: "border-edge-soft text-ink",
    // A refusal is information, not an alarm: the daemon's sentence in quiet text.
    warn: "border-edge-soft text-subtext",
    bad: "border-bad/40 text-bad",
  } as const;

  const flash = $derived(store.flash);
  $effect(() => {
    if (flash?.kind === "bad") haptic("refusal");
  });
</script>

<div
  class="pointer-events-none fixed inset-x-4 top-[calc(env(safe-area-inset-top,0px)+8px)] z-40 flex justify-center empty:hidden"
  role="status"
  aria-live="polite"
  data-flash
>
  {#if flash}
    <div
      class="flash pointer-events-auto flex w-full max-w-md items-center gap-3 rounded-card border bg-panel px-4 py-3 text-subhead shadow-lg {TONE[flash.kind]}"
    >
      <button
        type="button"
        class="min-w-0 flex-1 touch-manipulation text-left break-words"
        aria-label="Dismiss: {flash.text}"
        onclick={() => store.dismissFlash()}
      >
        {flash.text}
      </button>
      {#if flash.action}
        <button
          type="button"
          class="tap shrink-0 touch-manipulation px-1 font-semibold active:opacity-70"
          onclick={() => {
            const run = flash.action?.run;
            store.dismissFlash();
            void run?.();
          }}
        >
          {flash.action.label}
        </button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .flash {
    animation: flash-in 200ms ease-out;
  }
  @keyframes flash-in {
    from {
      transform: translateY(-12px);
      opacity: 0;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .flash {
      animation: flash-fade 200ms ease-out;
    }
    @keyframes flash-fade {
      from {
        opacity: 0;
      }
    }
  }
</style>
