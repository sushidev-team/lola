<script lang="ts">
  // The top bar's subscription limits at a glance: each agent's name beside its
  // limit windows as bare bars, stacked when an agent has more than one (Claude's
  // 5-hour over its weekly). No labels or numbers — the hover card (SpendCard)
  // carries those; the bar's LENGTH is the usage and its COLOUR the pace
  // (windowPace): accent while on track, warn once it would run out before its
  // reset (or passes 80%), bad from 95%.
  import type { QuotaInfo } from "@bindings/internal/protocol";
  import { AGENT_NAMES, windowName, windowPace } from "$lib/usage";

  let { quotas }: { quotas: QuotaInfo[] } = $props();
  const fill = { ok: "bg-accent", warn: "bg-warn", bad: "bg-bad" } as const;
</script>

<span class="inline-flex items-center gap-3">
  {#each quotas as q (q.agent)}
    {@const name = AGENT_NAMES[q.agent] ?? q.agent}
    <span class="inline-flex items-center gap-1.5">
      <span class="text-faint">{name}</span>
      <span class="flex w-12 flex-col justify-center gap-[3px]">
        {#each q.windows ?? [] as w (w.label)}
          <span
            class="block h-1 overflow-hidden rounded-full bg-sel"
            role="meter"
            aria-label="{name} {windowName(w.label)} limit"
            aria-valuemin="0"
            aria-valuemax="100"
            aria-valuenow={Math.round(w.usedPercent)}
          >
            <span
              class="block h-full rounded-full {fill[windowPace(w).tone]}"
              style="width: {Math.min(100, Math.max(4, w.usedPercent))}%"
            ></span>
          </span>
        {/each}
      </span>
    </span>
  {/each}
</span>
