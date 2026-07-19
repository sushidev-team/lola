<script lang="ts">
  import { onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import type { TicketsData, TicketRow } from "@bindings/internal/protocol";

  type Scope = "mine" | "team";
  const scopes: Scope[] = ["mine", "team"];

  let scope = $state<Scope>("mine");
  let loading = $state(false);
  let error = $state("");
  let starting = $state("");
  let data = $state<TicketsData | null>(null);

  const issues = $derived(data?.issues ?? []);
  const team = $derived(data?.team ?? "");

  async function load() {
    if (!store.alive) return;
    loading = true;
    error = "";
    try {
      data = await store.tickets(nav.project, scope);
    } catch (e) {
      error = String(e);
      data = null;
    } finally {
      loading = false;
    }
  }

  function pick(s: Scope) {
    if (s === scope) return;
    scope = s;
    void load();
  }

  async function start(t: TicketRow) {
    if (t.alreadyLive) {
      store.setFlash(`${t.identifier} already running`, "warn");
      return;
    }
    if (starting) return;
    starting = t.identifier;
    const r = await store.openTicket({
      project: nav.project,
      identifier: t.identifier,
      uuid: t.uuid,
      branch: t.branch,
      title: t.title,
    });
    starting = "";
    if (r) nav.goCockpit(nav.project);
  }

  function prio(p: number): { label: string; cls: string } {
    switch (p) {
      case 1:
        return { label: "urgent", cls: "text-bad" };
      case 2:
        return { label: "high", cls: "text-warn" };
      case 3:
        return { label: "medium", cls: "text-ink" };
      case 4:
        return { label: "low", cls: "text-faint" };
      default:
        return { label: "—", cls: "text-faint" };
    }
  }

  onMount(() => {
    void load();
  });
</script>

<div class="flex h-full min-h-0 flex-col p-4">
  <div class="mb-3 flex items-center gap-3">
    <button class="text-faint hover:text-accent" onclick={() => nav.goDetail(nav.project)}>‹ back</button>
    <div class="text-sm text-faint">
      lola <span class="text-edge">▸</span>
      <span class="text-ink">{nav.project}</span>
      <span class="text-edge">▸</span>
      <span class="text-ink">tickets</span>
    </div>

    <span class="ml-auto flex items-center gap-0.5 rounded border border-edge p-0.5">
      {#each scopes as s (s)}
        <button
          class="rounded px-2 py-[1px] text-[11px]"
          class:bg-accent={scope === s}
          class:text-canvas={scope === s}
          class:text-faint={scope !== s}
          onclick={() => pick(s)}>{s}</button
        >
      {/each}
    </span>
    <button
      class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30 disabled:opacity-40"
      disabled={loading || !store.alive}
      onclick={() => load()}>↻ refresh</button
    >
  </div>

  <div class="mb-2 text-[11px] text-faint">
    team <span class="text-ink">{team || "—"}</span>
    <span class="text-edge">·</span> scope <span class="text-ink">{scope}</span>
    <span class="text-edge">·</span> <span class="tabular-nums">{issues.length}</span>
  </div>

  <div class="min-h-0 flex-1 overflow-auto rounded-[10px] border border-edge">
    {#if !store.alive}
      <div class="px-3 py-8 text-center text-faint">Daemon offline — start it to browse issues.</div>
    {:else if loading}
      <div class="px-3 py-8 text-center text-faint">Loading issues…</div>
    {:else if error}
      <div class="px-3 py-8 text-center text-bad">{error}</div>
    {:else if issues.length === 0}
      <div class="px-3 py-8 text-center text-faint">No issues in this scope.</div>
    {:else}
      <table class="w-full text-xs">
        <thead class="sticky top-0 bg-panel/95 text-left text-[10px] tracking-wider text-faint uppercase backdrop-blur">
          <tr>
            <th class="px-3 py-2">Issue</th>
            <th class="px-3 py-2">Title</th>
            <th class="px-3 py-2">Priority</th>
            <th class="px-3 py-2"></th>
          </tr>
        </thead>
        <tbody>
          {#each issues as t (t.uuid)}
            {@const p = prio(t.priority)}
            <tr class="group border-t border-edge/30 hover:bg-sel/50" class:opacity-60={t.alreadyLive}>
              <td class="cursor-pointer px-3 py-2 font-mono text-[11px] whitespace-nowrap text-faint" onclick={() => start(t)}>
                {#if t.alreadyLive}<span class="mr-1 text-good" title="already running">●</span>{/if}
                {t.identifier}
              </td>
              <td class="cursor-pointer px-3 py-2" onclick={() => start(t)}>
                <div class="max-w-[52ch] truncate text-ink">{t.title}</div>
              </td>
              <td class="px-3 py-2 {p.cls}">{p.label}</td>
              <td class="px-3 py-2 text-right whitespace-nowrap opacity-0 group-hover:opacity-100">
                <button
                  class="px-1.5 text-faint hover:text-accent disabled:opacity-40"
                  disabled={starting === t.identifier}
                  onclick={() => start(t)}>{t.alreadyLive ? "live" : "start ›"}</button
                >
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>
