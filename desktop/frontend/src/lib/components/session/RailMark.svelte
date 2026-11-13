<script lang="ts">
  // A row's mark on the turn's rail. Decoration: it says what kind of row this is, never what state
  // anything is in, so it is neutral, static and hidden from a reader. The one colour is the tick of
  // a failure nothing has answered yet. Every class is a literal, for Tailwind. See railKind.
  import type { RailKind } from "$lib/turns";

  let { kind, lead = false }: { kind: RailKind | "folded"; /** The row opens on the large lead line, so its mark sits on that. */ lead?: boolean } = $props();

  const MARK: Record<Exclude<RailKind, "none"> | "folded", string> = {
    you: "top-[22px] size-[11px] rounded-full border border-ink-2 bg-canvas",
    folded: "top-[13px] size-[11px] rounded-full border border-ink-2 bg-canvas",
    terminal: "top-[9px] size-[11px] rounded-full border border-faint bg-canvas",
    narration: "top-[13px] size-[5px] rounded-full bg-ink-2",
    answer: "top-[13px] size-[9px] rounded-full bg-ink",
    plate: "top-[17px] size-[9px] rounded-[2px] border border-ink-2 bg-canvas",
    "plate-why": "top-[8px] size-[9px] rounded-[2px] border border-ink-2 bg-canvas",
    finding: "top-[11px] size-2 rotate-45 border border-ink-2 bg-canvas",
    element: "top-[11px] size-[5px] rounded-full bg-ink-2",
    running: "top-3 size-[9px] rounded-full border border-ink bg-canvas",
    receipt: "top-3 h-px w-[9px] bg-ink-2",
    tick: "",
    "tick-bad": "",
  };
</script>

{#if kind === "tick" || kind === "tick-bad"}
  <!-- A branch off the rail, starting on it, with no node: this line is detail, not a beat. -->
  <span class="absolute top-[11px] -left-4 h-px w-[13px] {kind === 'tick-bad' ? 'bg-bad' : 'bg-edge'}" aria-hidden="true" data-rail-mark={kind}></span>
{:else if kind !== "none"}
  <span class="absolute -left-4 -translate-x-1/2 -translate-y-1/2 {MARK[kind]} {lead ? 'top-[15px]!' : ''}" aria-hidden="true" data-rail-mark={kind}></span>
  {#if kind === "running"}
    <!-- Static, and the only thing a row's running adds to the rail. The footer has the one pulse. -->
    <span class="absolute top-[17px] -left-4 h-4 border-l border-dashed border-edge" aria-hidden="true"></span>
  {/if}
{/if}
