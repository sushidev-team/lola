<script lang="ts">
  import { pillClasses, statusLabel } from "$lib/theme";
  let {
    status,
    interpreted = "",
  }: { status: string; interpreted?: string } = $props();
  // No `dim` opacity. Group opacity composites the pill — fill AND label as one
  // layer — over whatever is behind it, which pulls the two toward a common
  // colour and quietly undoes the AA the pill's measured tokens guarantee. The
  // grid's de-emphasis already comes from the muted tints the non-attention
  // statuses carry (urgent/broken stay solid and loud); fading the whole pill
  // only cost legibility on top of that.
  //
  // The agent-axis badge is GONE from here. It rendered two-letter glyphs
  // ("·wk", "·?!", "·en") ported from the TUI's agentBadge
  // (internal/tui/sessionview.go) — correct there, where every column is
  // precious, and unreadable in an app that has room for words. The axis is now
  // rendered by <AgentActivity> as a live pulse plus plain language on the row's
  // secondary line; this component is back to one job — the delivery state, as
  // one pill.
</script>

<span class="inline-flex items-center align-middle whitespace-nowrap">
  <span
    class="inline-flex items-center whitespace-nowrap rounded px-1.5 py-[1px] text-sm {pillClasses(
      status,
    )}"
  >
    {statusLabel(status)}
  </span>
  {#if interpreted}
    <!-- The [statusagent] interpreter DISAGREES with the deterministic axis:
         an untrusted approximation, "≈"-marked (statusPillFor in the TUI). -->
    <span class="ml-0.5 text-sm text-orange">≈{statusLabel(interpreted)}</span>
  {/if}
</span>
