<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sortRank, eventPhrase, statusText } from "$lib/theme";
  import Panel from "./Panel.svelte";
  import Meter from "./Meter.svelte";

  const total = $derived(store.sessions.length);
  const fixing = $derived(store.sessions.filter((s) => sortRank(s.status) === 1).length);
  const working = $derived(store.sessions.filter((s) => sortRank(s.status) === 2).length);
  const ready = $derived(store.sessions.filter((s) => sortRank(s.status) === 3).length);
  const withPr = $derived(store.sessions.filter((s) => s.prNumber > 0).length);

  function pollDot(name: string): { glyph: string; cls: string; faint: boolean } {
    const ps = (store.status?.polls ?? []).find((p) => p.name === name);
    if (!ps) return { glyph: "·", cls: "text-faint", faint: true };
    if (ps.lastError) return { glyph: "●", cls: "text-bad", faint: false };
    if (ps.enabled) return { glyph: "●", cls: "text-good", faint: false };
    return { glyph: "○", cls: "text-faint", faint: true };
  }
</script>

<div class="flex h-full min-h-0 flex-col gap-2">
  <!-- Triage — sizes to content -->
  <Panel title="Triage">
    {#if store.needsYou > 0}
      <div class="mb-2 text-sm font-bold text-orange">{store.needsYou}&nbsp; NEED YOU</div>
    {:else}
      <div class="mb-2 text-sm font-bold text-good">0 <span class="font-normal text-faint">all clear</span></div>
    {/if}
    {#if total === 0}
      <div class="text-xs text-faint">no active sessions</div>
    {:else}
      <div class="flex flex-col gap-1.5">
        <Meter label="working" value={working} {total} color="var(--color-info)" />
        <Meter label="ready" value={ready} {total} color="var(--color-good)" />
        <Meter label="fixing" value={fixing} {total} color="var(--color-bad)" />
      </div>
      <div class="mt-2 text-[11px] text-faint">{total} total · {withPr} with PR</div>
    {/if}
  </Panel>

  <!-- Activity — grows to fill the rail -->
  <Panel title="Activity" note="newest first" fill>
    {#if store.activity.length === 0}
      <div class="text-xs text-faint">no activity yet</div>
    {:else}
      <ul class="flex flex-col gap-1 text-xs">
        {#each store.activity.slice(0, 40) as ev (ev.id + ev.to + ev.ago)}
          <li class="flex items-baseline gap-1.5">
            <span class="font-medium text-ink">{ev.issue || ev.id.slice(0, 6)}</span>
            <span class={statusText(ev.to)}>{eventPhrase(ev.from, ev.to)}</span>
            <span class="ml-auto text-faint tabular-nums">{ev.ago}</span>
          </li>
        {/each}
      </ul>
    {/if}
  </Panel>

  <!-- Projects switcher -->
  <Panel title="Projects" count={store.projects.length} focused={false}>
    {#if store.projects.length === 0}
      <div class="text-xs text-faint">no projects — press p to add one</div>
    {:else}
      <ul class="flex flex-col text-xs">
        {#each store.projects as p (p.name)}
          {@const d = pollDot(p.name)}
          {@const active = nav.scoped && nav.project === p.name}
          <li class="group flex items-center rounded hover:bg-sel/60" class:bg-sel={active}>
            <button
              class="flex min-w-0 flex-1 items-center gap-2 px-1.5 py-1 text-left"
              title="scope the cockpit to this project"
              onclick={() => nav.goCockpit(p.name)}
            >
              <span class={d.cls}>{d.glyph}</span>
              <span class="truncate" class:text-faint={d.faint}>{p.name}</span>
              {#if p.needsYou > 0}<span class="text-orange">{p.needsYou}!</span>{/if}
              {#if p.ciRed > 0}<span class="text-bad">{p.ciRed}✕</span>{/if}
            </button>
            <button
              class="px-1.5 text-faint opacity-0 group-hover:opacity-100 hover:text-accent-ink"
              title="open project hub"
              onclick={() => nav.goDetail(p.name)}>›</button
            >
          </li>
        {/each}
      </ul>
    {/if}
  </Panel>
</div>
