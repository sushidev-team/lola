<script lang="ts">
  import { store, type ProjectInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";

  let filter = $state("");
  const rows = $derived(
    store.projects.filter(
      (p) => !filter || p.name.toLowerCase().includes(filter.toLowerCase()) || p.path.toLowerCase().includes(filter.toLowerCase()),
    ),
  );

  function compactPath(p: string): string {
    if (!p) return "(no path)";
    const home = "/Users/";
    let s = p;
    if (s.startsWith(home)) s = "~/" + s.split("/").slice(3).join("/");
    const parts = s.split("/");
    return parts.length > 3 ? "…/" + parts.slice(-2).join("/") : s;
  }

  function pollCell(p: ProjectInfo): { text: string; cls: string } {
    if (!p.pathOk) return { text: "⚠ missing", cls: "text-warn" };
    if (p.lastError) return { text: "⚠ err", cls: "text-warn" };
    if (p.pollCount === 0) return { text: "no polls", cls: "text-faint" };
    if (p.pollsEnabled > 0) return { text: `● ${p.pollsEnabled} on`, cls: "text-good" };
    return { text: "○ paused", cls: "text-faint" };
  }
</script>

<div class="flex h-full min-h-0 flex-col p-4">
  <div class="mb-3 flex items-center gap-3">
    <div class="text-sm text-faint">lola <span class="text-edge">▸</span> <span class="text-ink">projects</span></div>
    <input
      class="ml-auto w-56 rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent"
      placeholder="filter projects…"
      bind:value={filter}
    />
    <button
      class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30"
      onclick={() => nav.openOverlay("project", "")}>+ add project</button
    >
  </div>

  <div class="min-h-0 flex-1 overflow-auto rounded-[10px] border border-edge">
    <table class="w-full text-xs">
      <thead class="sticky top-0 bg-panel/95 text-left text-[10px] tracking-wider text-faint uppercase backdrop-blur">
        <tr>
          <th class="px-3 py-2">Project</th>
          <th class="px-3 py-2">Path</th>
          <th class="px-3 py-2">Poll</th>
          <th class="px-3 py-2 text-right">Live</th>
          <th class="px-3 py-2">Attention</th>
          <th class="px-3 py-2 text-right">Last</th>
          <th class="px-3 py-2"></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as p (p.name)}
          {@const poll = pollCell(p)}
          <tr class="group border-t border-edge/30 hover:bg-sel/50">
            <td class="cursor-pointer px-3 py-2 font-medium" onclick={() => nav.goDetail(p.name)}>
              {p.name}
              {#if !p.agentOk}<span class="ml-1 text-bad" title={p.agentErr}>✗</span>{/if}
            </td>
            <td class="px-3 py-2 font-mono text-[11px] text-faint">{compactPath(p.path)}</td>
            <td class="px-3 py-2 {poll.cls}">{poll.text}</td>
            <td class="px-3 py-2 text-right tabular-nums">{store.alive ? p.liveCounted : "—"}</td>
            <td class="px-3 py-2">
              {#if p.needsYou > 0}<span class="text-orange">{p.needsYou} need</span>{/if}
              {#if p.ciRed > 0}<span class="ml-1 text-bad">{p.ciRed} ci</span>{/if}
              {#if p.needsYou === 0 && p.ciRed === 0}<span class="text-faint">—</span>{/if}
            </td>
            <td class="px-3 py-2 text-right text-faint tabular-nums">{p.openPrs > 0 ? `${p.openPrs} PR` : ""}</td>
            <td class="px-3 py-2 text-right whitespace-nowrap opacity-0 group-hover:opacity-100">
              <button class="px-1.5 text-faint hover:text-accent" onclick={() => nav.goCockpit(p.name)}>sessions</button>
              <button class="px-1.5 text-faint hover:text-accent" onclick={() => nav.openOverlay("project", p.name)}>edit</button>
              <button class="px-1.5 text-faint hover:text-accent" onclick={() => nav.goDetail(p.name)}>open ›</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    {#if rows.length === 0}
      <div class="px-3 py-8 text-center text-faint">No projects yet. Add your first repo.</div>
    {/if}
  </div>
</div>
