<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sortRank, eventPhrase, statusText } from "$lib/theme";
  import { displayName } from "$lib/slug";
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
    {#snippet actions()}
      <button
        class="rounded border border-edge px-1.5 py-[1px] text-[11px] font-normal text-faint hover:border-accent hover:text-accent-ink"
        title="add a project"
        onclick={() => nav.openOverlay("project", "")}>+ add</button
      >
    {/snippet}
    {#if store.projects.length === 0}
      <button
        class="w-full rounded border border-dashed border-edge px-2 py-3 text-center text-xs text-faint hover:border-accent hover:text-accent-ink"
        onclick={() => nav.openOverlay("project", "")}>no projects — add one</button
      >
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
              <span class="truncate" class:text-faint={d.faint}>{displayName(p)}</span>
              {#if p.needsYou > 0}<span class="text-orange">{p.needsYou}!</span>{/if}
              {#if p.ciRed > 0}<span class="text-bad">{p.ciRed}✕</span>{/if}
            </button>
            <!-- Settings is always visible (faint), so it's obvious a project
                 can be configured; the hub arrow reveals on hover. -->
            <button
              class="shrink-0 px-1 text-faint/70 hover:text-accent-ink"
              title="project settings"
              aria-label="{displayName(p)} settings"
              onclick={() => nav.openOverlay("project", p.name)}
            >
              <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
              </svg>
            </button>
            <button
              class="shrink-0 px-1.5 text-faint opacity-0 group-hover:opacity-100 hover:text-accent-ink"
              title="open project hub"
              aria-label="{displayName(p)} hub"
              onclick={() => nav.goDetail(p.name)}>›</button
            >
          </li>
        {/each}
      </ul>
    {/if}
  </Panel>
</div>
