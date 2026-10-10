<script lang="ts">
  // The top bar's hover card: each agent's subscription limits as meters (with
  // when each resets and how fresh the reading is), then today's tokens, the
  // budget and per-project usage, and why dispatch is held.
  import type { UsageStatus } from "@bindings/internal/protocol";
  import { agoShort } from "$lib/board";
  import { budgetPercent, fmtTokens, fmtUSD, untilShort } from "$lib/usage";

  let { usage }: { usage: UsageStatus } = $props();
  const names: Record<string, string> = { claude: "Claude", codex: "Codex" };
  const meter = (p: number) => (p >= 95 ? "bg-bad" : p >= 80 ? "bg-warn" : "bg-accent");
  const budget = $derived(budgetPercent(usage.weighted, usage.budgetTokens));
  const fresh = (iso: string) => {
    const a = agoShort(iso);
    return a === "now" ? "just now" : a ? `${a} ago` : "";
  };
</script>

<div class="flex w-72 flex-col gap-3">
  {#each usage.quotas ?? [] as q (q.agent)}
    <section>
      <div class="mb-1 flex items-baseline justify-between gap-3">
        <span class="font-medium text-ink">{names[q.agent] ?? q.agent}{#if q.plan}<span class="ml-1.5 text-faint">{q.plan}</span>{/if}</span>
        <span class="text-xs text-faint">{fresh(q.at)}</span>
      </div>
      {#each q.windows ?? [] as w (w.label)}
        <div class="grid grid-cols-[2.5rem_1fr_3rem] items-center gap-2 py-0.5">
          <span class="num text-faint">{w.label}</span>
          <span class="h-1.5 overflow-hidden rounded-full bg-sel">
            <span class="block h-full rounded-full {meter(w.usedPercent)}" style="width: {Math.min(100, w.usedPercent)}%"></span>
          </span>
          <span class="num text-right text-ink">{Math.round(w.usedPercent)}%</span>
        </div>
        {#if untilShort(w.resetsAt)}
          <div class="pl-[3rem] text-xs text-faint">resets {untilShort(w.resetsAt)}</div>
        {/if}
      {/each}
    </section>
  {/each}

  <section class="{usage.quotas?.length ? 'border-t border-edge pt-2.5' : ''}">
    <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
      <dt class="text-faint">Today</dt>
      <dd class="num text-ink">
        {fmtTokens(usage.tokens)} tokens{#if usage.todayUsd > 0}<span class="ml-1 text-faint">· ~{fmtUSD(usage.todayUsd)}</span>{/if}
      </dd>
      <dt class="text-faint">Budget</dt>
      <dd class="num {budget >= 100 ? 'text-bad' : budget >= 80 ? 'text-warn' : 'text-ink'}">
        {#if budget >= 0}{budget}%<span class="ml-1 text-faint">of {fmtTokens(usage.budgetTokens ?? 0)} weighted</span>{:else}<span class="text-faint">no daily limit</span>{/if}
      </dd>
      {#each usage.projects ?? [] as p (p.name)}
        {@const pct = budgetPercent(p.weighted, p.budgetTokens)}
        <dt class="truncate pl-2 text-faint">{p.name}</dt>
        <dd class="num text-ink">{fmtTokens(p.tokens)}{#if pct >= 0}<span class="ml-1 text-faint">· {pct}% of limit</span>{/if}</dd>
      {/each}
    </dl>
  </section>

  {#if usage.load?.busy}
    <p class="rounded-md bg-bad/12 px-2 py-1.5 text-bad">Dispatch held — {usage.load.busy}</p>
  {/if}
</div>
