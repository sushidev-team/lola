<script lang="ts">
  // The top bar's subscription limits as compact progress bars: per agent its
  // name, then one labelled bar per window ("5h", "7d") with the percentage.
  // A bar is accent below 80%, warn from 80%, bad from 95% (quotaTone).
  import type { QuotaInfo } from "@bindings/internal/protocol";
  import { AGENT_NAMES, quotaTone } from "$lib/usage";

  let { quotas }: { quotas: QuotaInfo[] } = $props();
  const fill = { ok: "bg-accent", warn: "bg-warn", bad: "bg-bad" } as const;
  const text = { ok: "text-ink", warn: "text-warn", bad: "text-bad" } as const;
</script>

<span class="inline-flex items-center gap-3">
  {#each quotas as q (q.agent)}
    <span class="inline-flex items-center gap-1.5">
      <span class="text-faint">{AGENT_NAMES[q.agent] ?? q.agent}</span>
      {#each q.windows ?? [] as w (w.label)}
        {@const tone = quotaTone(w.usedPercent)}
        <span
          class="inline-flex items-center gap-1"
          role="meter"
          aria-label="{AGENT_NAMES[q.agent] ?? q.agent} {w.label} limit"
          aria-valuemin="0"
          aria-valuemax="100"
          aria-valuenow={Math.round(w.usedPercent)}
        >
          <span class="text-xs text-faint">{w.label}</span>
          <span class="h-1.5 w-9 overflow-hidden rounded-full bg-sel">
            <span class="block h-full rounded-full {fill[tone]}" style="width: {Math.min(100, Math.max(2, w.usedPercent))}%"></span>
          </span>
          <span class="num {text[tone]}">{Math.round(w.usedPercent)}%</span>
        </span>
      {/each}
    </span>
  {/each}
</span>
