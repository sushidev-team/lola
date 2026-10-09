<script lang="ts">
  import { displayFor, displayLabel, inputReasonLabel, statusLabel } from "$lib/theme";

  // The AGENT axis as one 16px icon — the list's replacement for the status
  // pill's word. Only `working` moves (a slowly turning arc), so motion on the
  // list means exactly one thing: this runner is mid-turn. Every other state is
  // a distinct STATIC shape, so the six still read apart under
  // prefers-reduced-motion (app.css stills the arc) and without colour:
  //
  //   working   turning arc on a faint ring        info
  //   idle      small solid dot                     faint ink — alive, resting
  //   needs_you filled disc with "!"                orange — the loud one
  //   gone      hollow ring                         faint — nothing running
  //   shell     prompt chevron                      magenta — agentless checkout
  //   orphaned  ring with a slash                   red — adoption anomaly
  //
  // The word, the input reason and the interpreter's disagreement go to the
  // tooltip and to a visually-hidden label, so nothing the pill used to say is
  // lost — it just stops taking a column.
  let {
    agentState = "",
    inputReason = "",
    status = "",
    interpreted = "",
    size = 16,
  }: {
    agentState?: string;
    inputReason?: string;
    /** Legacy rolled-up status: the label for a pre-axis daemon's session. */
    status?: string;
    interpreted?: string;
    size?: number;
  } = $props();

  // A daemon predating the axis split sends no agentState, and displayFor("")
  // answers "working" — which would set a parked PR spinning. Such a session
  // gets a static dot and its rollup word instead.
  const axes = $derived(agentState !== "");
  const d = $derived(displayFor(agentState));
  const reason = $derived(d === "needs_you" ? inputReasonLabel(inputReason) : "");
  const word = $derived(axes ? (d === "working" ? "agent running" : displayLabel(d)) : statusLabel(status));
  const tip = $derived(
    [word, reason, interpreted ? `≈ ${statusLabel(interpreted)} (interpreted)` : ""].filter(Boolean).join(" · "),
  );
</script>

<span
  class="relative inline-flex shrink-0 items-center justify-center align-middle"
  style="width:{size}px;height:{size}px"
  title={tip}
>
  <span class="sr-only">{tip}</span>
  {#if !axes}
    <span class="h-[40%] w-[40%] rounded-full bg-faint/70" aria-hidden="true"></span>
  {:else if d === "working"}
    <svg viewBox="0 0 16 16" class="spin h-[88%] w-[88%] text-info" fill="none" stroke-width="2" aria-hidden="true">
      <circle cx="8" cy="8" r="6" stroke="currentColor" opacity="0.22" />
      <path d="M8 2a6 6 0 0 1 6 6" stroke="currentColor" stroke-linecap="round" />
    </svg>
  {:else if d === "idle"}
    <span class="h-[40%] w-[40%] rounded-full bg-ink/45" aria-hidden="true"></span>
  {:else if d === "needs_you"}
    <span class="flex h-[88%] w-[88%] items-center justify-center rounded-full bg-orange text-canvas" aria-hidden="true">
      <svg viewBox="0 0 16 16" class="h-[70%] w-[70%]" fill="currentColor">
        <rect x="6.9" y="3" width="2.2" height="6.5" rx="1.1" />
        <circle cx="8" cy="12.2" r="1.3" />
      </svg>
    </span>
  {:else if d === "shell"}
    <svg viewBox="0 0 16 16" class="h-[88%] w-[88%] text-magenta" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M3.5 4.5l3.5 3.5-3.5 3.5M8.5 12h4" />
    </svg>
  {:else if d === "orphaned"}
    <svg viewBox="0 0 16 16" class="h-[80%] w-[80%] text-bad" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true">
      <circle cx="8" cy="8" r="6" />
      <path d="M4 12l8-8" />
    </svg>
  {:else}
    <span class="h-[50%] w-[50%] rounded-full border-[1.5px] border-faint/60" aria-hidden="true"></span>
  {/if}
</span>

<style>
  .spin {
    animation: spin 1.6s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
