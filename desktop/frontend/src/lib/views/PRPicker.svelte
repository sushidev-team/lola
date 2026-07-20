<script lang="ts">
  import { onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import type { PrRow, PrsData } from "@bindings/internal/protocol";

  let data = $state<PrsData | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let filter = $state("");

  const filtered = $derived.by(() => {
    const rows = data?.prs ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter(
      (p) =>
        p.title.toLowerCase().includes(q) ||
        p.author.toLowerCase().includes(q) ||
        p.branch.toLowerCase().includes(q) ||
        String(p.number).includes(q),
    );
  });

  async function load(refresh = false) {
    loading = true;
    error = null;
    try {
      data = await store.prs(nav.project, refresh);
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  // pass|fail|pending|none → glyph + color
  function ciGlyph(checks: string): { glyph: string; cls: string } {
    switch (checks) {
      case "pass":
        return { glyph: "✓", cls: "text-good" };
      case "fail":
        return { glyph: "✕", cls: "text-bad" };
      case "pending":
        return { glyph: "•", cls: "text-warn" };
      default:
        return { glyph: "—", cls: "text-faint" };
    }
  }

  function reviewGlyph(r: string): { text: string; cls: string } {
    switch (r) {
      case "APPROVED":
        return { text: "✓ appr", cls: "text-good" };
      case "CHANGES_REQUESTED":
        return { text: "✗ chg", cls: "text-bad" };
      case "REVIEW_REQUIRED":
        return { text: "○ req", cls: "text-faint" };
      default:
        return { text: "○", cls: "text-faint" };
    }
  }

  async function openShell(p: PrRow) {
    if (p.alreadyOpen) {
      store.setFlash("already open in a session", "warn");
      return;
    }
    const r = await store.open(nav.project, String(p.number));
    if (r) nav.goCockpit(nav.project);
  }

  async function openAgent(p: PrRow) {
    if (p.alreadyOpen) {
      store.setFlash("already open in a session", "warn");
      return;
    }
    if (p.isFork) {
      store.setFlash("can't push back to a fork", "warn");
      return;
    }
    const r = await store.openPr({ project: nav.project, branch: p.branch, number: p.number, isFork: p.isFork });
    if (r) nav.goCockpit(nav.project);
  }

  function openBrowser(p: PrRow) {
    void store.openURL(p.url);
  }
</script>

<div class="flex h-full min-h-0 flex-col p-4">
  <div class="mb-3 flex items-center gap-3">
    <button class="text-faint hover:text-accent-ink" title="back to project" onclick={() => nav.goDetail(nav.project)}>‹</button>
    <div class="text-sm text-faint">
      lola <span class="text-edge">▸</span>
      <span class="text-ink">{nav.project || "project"}</span>
      <span class="text-edge">▸</span> <span class="text-ink">PRs</span>
    </div>
    {#if data}
      <span class="text-[11px] text-faint tabular-nums">
        {data.prs?.length ?? 0} open · {data.ageSeconds}s ago{data.stale ? " · stale" : ""}
      </span>
    {/if}
    <input
      class="ml-auto w-56 rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent placeholder:text-placeholder"
      placeholder="filter PRs…"
      bind:value={filter}
    />
    <button
      class="rounded bg-accent-fill px-3 py-1 text-xs text-accent-ink hover:bg-accent-fill-hover disabled:opacity-50"
      disabled={loading}
      onclick={() => load(true)}>↻ refresh</button
    >
  </div>

  <div class="min-h-0 flex-1 overflow-auto rounded-[10px] border border-edge">
    {#if loading && !data}
      <div class="px-3 py-8 text-center text-faint">Fetching open PRs…</div>
    {:else if !store.alive}
      <div class="px-3 py-8 text-center text-faint">daemon not running</div>
    {:else if error}
      <div class="flex flex-col items-center gap-2 px-3 py-8 text-center">
        <span class="text-bad">couldn't list PRs: {error}</span>
        <button class="rounded bg-accent-fill px-3 py-1 text-xs text-accent-ink hover:bg-accent-fill-hover" onclick={() => load(true)}>retry</button>
      </div>
    {:else if !data || (data.prs?.length ?? 0) === 0}
      <div class="px-3 py-8 text-center text-faint">No open PRs — refresh</div>
    {:else}
      <table class="w-full text-xs">
        <thead class="sticky top-0 bg-panel/95 text-left text-[10px] tracking-wider text-faint uppercase backdrop-blur">
          <tr>
            <th class="px-3 py-2 text-right">#</th>
            <th class="px-3 py-2">Title</th>
            <th class="px-3 py-2">Author</th>
            <th class="px-3 py-2">Branch</th>
            <th class="px-3 py-2 text-center">CI</th>
            <th class="px-3 py-2">Review</th>
            <th class="px-3 py-2"></th>
          </tr>
        </thead>
        <tbody>
          {#each filtered as p (p.number)}
            {@const ci = ciGlyph(p.checks)}
            {@const rv = reviewGlyph(p.review)}
            <tr class="group cursor-pointer border-t border-edge/30 hover:bg-sel/50">
              <td class="px-3 py-2 text-right tabular-nums {p.alreadyOpen ? 'text-faint' : ''}" onclick={() => openShell(p)}>#{p.number}</td>
              <td class="px-3 py-2" onclick={() => openShell(p)}>
                <div class="max-w-[46ch] truncate">
                  {p.title}{#if p.isDraft}<span class="text-faint"> [draft]</span>{/if}{#if p.isFork}<span class="text-faint"> [fork]</span>{/if}
                </div>
              </td>
              <td class="px-3 py-2 text-faint" onclick={() => openShell(p)}>{p.author || "—"}</td>
              <td class="px-3 py-2 font-mono text-[11px] text-faint" onclick={() => openShell(p)}>
                <div class="max-w-[26ch] truncate">{p.branch}</div>
              </td>
              <td class="px-3 py-2 text-center {ci.cls}" title={p.checks} onclick={() => openShell(p)}>{ci.glyph}</td>
              <td class="px-3 py-2 {rv.cls}" title={p.review} onclick={() => openShell(p)}>{rv.text}</td>
              <td class="px-3 py-2 text-right whitespace-nowrap opacity-0 group-hover:opacity-100">
                <button class="px-1.5 text-faint hover:text-accent-ink disabled:opacity-40" disabled={p.alreadyOpen} onclick={() => openShell(p)}>shell</button>
                <button class="px-1.5 text-faint hover:text-accent-ink disabled:opacity-40" disabled={p.alreadyOpen || p.isFork} onclick={() => openAgent(p)}>agent</button>
                <button class="px-1.5 text-faint hover:text-accent-ink" onclick={() => openBrowser(p)}>browser</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if filtered.length === 0}
        <div class="px-3 py-8 text-center text-faint">No matching PRs.</div>
      {/if}
    {/if}
  </div>
</div>
