<script lang="ts">
  // The top bar's hover card, in two sections:
  //
  //   LIMITS — per agent: name + plan, and when the reading was taken; then per
  //   window its name and share used, a full-width bar with a tick where usage
  //   WOULD be if it were spread evenly over the window (the pace mark), and
  //   below it when it resets and where the current pace leads (windowPace).
  //
  //   TODAY — the day's tokens as the headline, the budget, and each project's
  //   share as a bar against the day's total; a dispatch hold last.
  import type { UsageStatus } from "@bindings/internal/protocol";
  import { agoShort } from "$lib/board";
  import { AGENT_NAMES, budgetPercent, fmtTokens, fmtUSD, paceNote, untilShort, windowName, windowPace } from "$lib/usage";

  let { usage }: { usage: UsageStatus } = $props();
  const fill = { ok: "bg-accent", warn: "bg-warn", bad: "bg-bad" } as const;
  const ink = { ok: "text-faint", warn: "text-warn", bad: "text-bad" } as const;
  const budget = $derived(budgetPercent(usage.weighted, usage.budgetTokens));
  const projects = $derived([...(usage.projects ?? [])].sort((a, b) => b.tokens - a.tokens));
  const fresh = (iso: string) => {
    const a = agoShort(iso);
    return a === "now" ? "updated just now" : a ? `updated ${a} ago` : "";
  };
</script>

<div class="flex w-80 flex-col">
  {#if usage.quotas?.length}
    <section class="flex flex-col gap-3 pb-3">
      <h3 class="label text-faint">Limits</h3>
      {#each usage.quotas as q (q.agent)}
        <div class="flex flex-col gap-2">
          <div class="flex items-baseline justify-between gap-3">
            <span class="font-medium text-ink">
              {AGENT_NAMES[q.agent] ?? q.agent}
              {#if q.plan}<span class="ml-1 rounded bg-sel px-1.5 py-px text-sm font-normal text-faint">{q.plan}</span>{/if}
            </span>
            <span class="text-sm text-faint">{fresh(q.at)}</span>
          </div>
          {#each q.windows ?? [] as w (w.label)}
            {@const p = windowPace(w)}
            <div class="flex flex-col gap-1">
              <div class="flex items-baseline justify-between">
                <span class="text-ink">{windowName(w.label)}</span>
                <span><span class="num font-medium text-ink">{Math.round(w.usedPercent)}%</span><span class="ml-1 text-sm text-faint">used</span></span>
              </div>
              <div class="relative h-1.5 rounded-full bg-sel">
                <div class="h-full rounded-full {fill[p.tone]}" style="width: {Math.min(100, Math.max(2, w.usedPercent))}%"></div>
                {#if p.elapsed !== null}
                  <!-- The pace mark: where usage would be if spread evenly. -->
                  <div class="absolute -top-0.5 h-2.5 w-px bg-faint" style="left: {p.elapsed * 100}%"></div>
                {/if}
              </div>
              <div class="flex justify-between gap-3 text-sm">
                <span class="text-faint">{#if untilShort(w.resetsAt)}resets {untilShort(w.resetsAt)}{/if}</span>
                <span class={ink[p.tone]}>{paceNote(p)}</span>
              </div>
            </div>
          {/each}
        </div>
      {/each}
    </section>
  {/if}

  <section class="flex flex-col gap-2 {usage.quotas?.length ? 'border-t border-edge pt-3' : ''}">
    <h3 class="label text-faint">Today</h3>
    <div class="flex items-baseline justify-between">
      <span><span class="num text-lg text-ink">{fmtTokens(usage.tokens)}</span><span class="ml-1 text-faint">tokens</span></span>
      {#if usage.todayUsd > 0}<span class="num text-sm text-faint">~{fmtUSD(usage.todayUsd)} est.</span>{/if}
    </div>
    <div class="flex justify-between text-sm">
      <span class="text-faint">Budget</span>
      {#if budget >= 0}
        <span class="num {budget >= 100 ? 'text-bad' : budget >= 80 ? 'text-warn' : 'text-ink'}">{budget}% of {fmtTokens(usage.budgetTokens ?? 0)}</span>
      {:else}
        <span class="text-faint">no daily limit</span>
      {/if}
    </div>
    {#if projects.length}
      <ul class="flex flex-col gap-1.5 pt-1">
        {#each projects as p (p.name)}
          {@const pct = budgetPercent(p.weighted, p.budgetTokens)}
          <li class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1">
            <span class="truncate text-ink">{p.name}</span>
            <span class="num text-sm text-faint">{fmtTokens(p.tokens)}{#if pct >= 0} · {pct}% of limit{/if}</span>
            <span class="col-span-2 h-1 rounded-full bg-sel">
              <span class="block h-full rounded-full bg-faint" style="width: {usage.tokens ? Math.max(2, (100 * p.tokens) / usage.tokens) : 0}%"></span>
            </span>
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  {#if usage.load?.busy}
    <p class="mt-3 rounded-md bg-bad/12 px-2 py-1.5 text-sm text-bad">Dispatch held — {usage.load.busy}</p>
  {/if}
</div>
