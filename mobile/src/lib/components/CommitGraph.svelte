<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { agoShort } from "$lib/board";
  import { branchRemote } from "$lib/gitrefdisplay";
  import { commitGraph, defaultBranchTip, graphColor, graphPath, GRAPH_LANE_WIDTH, GRAPH_NODE_X, type GitCommit, type GitRef, type GitSnapshot } from "$lib/projectgit";
  import { store } from "$lib/store.svelte";
  import type { PrsData } from "@bindings/internal/protocol";
  import { copy } from "@mobile/lib/copy";
  import { shortSha } from "@mobile/lib/gitphone";
  import { middleEllipsis } from "@mobile/lib/projects.svelte";
  import { can } from "@mobile/lib/tiers.svelte";
  import VirtualList from "./VirtualList.svelte";
  import TouchButton from "./TouchButton.svelte";

  let { snapshot, project, historyRef, onfilter, onopen, more, onmore, loading, switchingFilter }: {
    snapshot: GitSnapshot; project: string; historyRef: string; onfilter: (ref: string) => void;
    onopen: (commit: GitCommit) => void; more: boolean; onmore: () => void; loading: boolean; switchingFilter: boolean;
  } = $props();

  const rows = $derived(commitGraph(snapshot.commits, defaultBranchTip(snapshot.refs, store.defaultBranchFor(project), snapshot.commits)));
  const laneCount = $derived(Math.max(1, ...rows.map(row => row.width)));
  const compactLanes = 4;
  let expanded = $state(false);
  let picking = $state(false);
  let search = $state("");
  const visibleLanes = $derived(expanded ? laneCount : Math.min(laneCount, compactLanes));
  const width = $derived(visibleLanes * 15 + 8);
  const filterLabel = $derived(snapshot.refs.find(ref => ref.fullName === historyRef)?.name ?? copy.git.allBranches);
  const matches = $derived(snapshot.refs.filter(ref => ref.name.toLowerCase().includes(search.trim().toLowerCase())));
  let prs = $state<PrsData | null>(null);
  $effect(() => {
    const name = project;
    let current = true;
    prs = null;
    if (can("prs") && store.projectByName(name)?.repoConfigured) {
      void store.prs(name).then(data => { if (current) prs = data; }).catch(() => {});
    }
    return () => { current = false; };
  });

  const tips = $derived.by(() => {
    const byCommit = new Map<string, GitRef[]>();
    for (const ref of snapshot.refs) {
      const group = byCommit.get(ref.sha) ?? [];
      group.push(ref);
      byCommit.set(ref.sha, group);
    }
    return byCommit;
  });

  // The phone's PR listing identifies the head repository only for same-repo PRs.
  // Match the tracking ref against that repository before attaching its number.
  const prNumbers = $derived.by(() => {
    const result = new Map<string, Set<number>>();
    if (!prs?.repo) return result;
    const numbersByBranch = new Map<string, number[]>();
    for (const pr of prs.prs ?? []) {
      if (pr.isFork) continue;
      const numbers = numbersByBranch.get(pr.branch) ?? [];
      numbers.push(pr.number);
      numbersByBranch.set(pr.branch, numbers);
    }
    for (const ref of snapshot.refs) {
      const remote = branchRemote(ref, snapshot.remotes ?? []);
      if (remote?.host !== "github.com" || remote.repo.toLowerCase() !== prs.repo.toLowerCase()) continue;
      const branch = ref.name.slice(remote.name.length + 1);
      const matches = numbersByBranch.get(branch);
      if (!matches) continue;
      const numbers = result.get(ref.sha) ?? new Set<number>();
      for (const n of matches) numbers.add(n);
      result.set(ref.sha, numbers);
    }
    return result;
  });

  function choose(ref: string) {
    picking = false;
    search = "";
    if (ref !== historyRef) onfilter(ref);
  }
</script>

<div class="flex min-h-0 min-w-0 flex-1 flex-col">
  <div class="flex min-h-11 shrink-0 items-center justify-between gap-2 border-y border-edge-soft bg-panel px-4">
    <button type="button" class="flex min-h-11 min-w-0 items-center gap-2 text-subhead text-ink" aria-expanded={picking}
      aria-label={copy.git.filterBranches + ", " + filterLabel} onclick={() => { picking = !picking; search = ""; }}>
      <Icon name="git-branch" size={15} class="text-accent" /><span class="truncate">{filterLabel}</span><Icon name="chevron-down" size={14} />
    </button>
    {#if laneCount > compactLanes}
      <button type="button" class="min-h-11 shrink-0 text-footnote text-accent" aria-pressed={expanded} onclick={() => expanded = !expanded}>
        {expanded ? copy.git.collapse : copy.git.expand}
      </button>
    {/if}
  </div>
  {#if picking}
    <div class="min-h-0 max-h-[40%] shrink-0 overflow-auto border-b border-edge-soft bg-panel px-4">
      <input class="my-1 min-h-11 w-full rounded-lg bg-canvas px-3 text-body" type="search"
        aria-label={copy.git.branchSearch} placeholder={copy.git.branchSearch} bind:value={search} />
      <button class="block min-h-11 w-full text-left text-body text-accent" onclick={() => choose("")}>{copy.git.allBranches}</button>
      {#each matches as ref (ref.fullName)}
        <button class="flex min-h-11 w-full items-center gap-2 text-left text-footnote text-ink" onclick={() => choose(ref.fullName)}>
          <Icon name={ref.remote ? "cloud" : "git-branch"} size={14} /><span class="break-all font-mono">{ref.name}</span>
          {#if historyRef === ref.fullName}<Icon name="check" size={15} />{/if}
        </button>
      {:else}
        <p class="py-3 text-footnote text-faint">{copy.git.noBranches.body}</p>
      {/each}
    </div>
  {/if}
  {#if switchingFilter}
    <p class="px-8 py-6 text-center text-callout text-faint" role="status">{copy.git.loading.status}</p>
  {:else if !rows.length}
    <p class="px-8 py-6 text-center text-callout text-faint">{copy.git.noCommits.body}</p>
  {:else}
    <div class="min-h-0 flex-1" aria-busy={loading}>
      {#key historyRef}
        <VirtualList items={rows} key={row => row.sha} label={copy.git.graph}>
          {#snippet row(row, i)}
            {@const commit = snapshot.commits[i]}
            {@const refs = tips.get(commit.sha) ?? []}
            {@const merge = commit.parents.length > 1}
            <button type="button" class="commit-row" style:--rail={width + 'px'} class:expanded
              onclick={() => onopen(commit)}>
              <span class="rail" aria-hidden="true">
                <svg width={width} viewBox={'0 0 ' + (width / 15 * GRAPH_LANE_WIDTH) + ' 64'} preserveAspectRatio="none">
                  {#each row.edges as edge}
                    <path d={graphPath(edge, 64)} stroke={graphColor(edge.color)} fill="none" stroke-width="1.6" vector-effect="non-scaling-stroke" />
                  {/each}
                </svg>
                {#if row.lane < visibleLanes}
                  <span class="node" class:merge style:left={(GRAPH_NODE_X / GRAPH_LANE_WIDTH + row.lane) * 15 + 'px'} style:--lane={graphColor(row.color)}></span>
                {:else}
                  <span class="outside" style:color={graphColor(row.color)}>›</span>
                {/if}
              </span>
              <span class="min-w-0 flex-1 border-b border-edge-soft py-2.5 pr-4">
                {#if refs.length || commit.refs.some(r => r.startsWith("tag: ")) || prNumbers.has(commit.sha)}
                  <span class="mb-1 flex flex-wrap gap-1">
                    {#each refs as ref (ref.fullName)}
                      <span class="ref" class:current={ref.current} title={ref.name}>
                        <Icon name={ref.remote ? "cloud" : "git-branch"} size={12} />
                        <span class="truncate"><span class="sr-only">{ref.current ? "Checked out, " : ""}{ref.name}</span><span aria-hidden="true">{middleEllipsis(ref.name)}</span></span>
                        {#if ref.ahead}<span aria-hidden="true">↑{ref.ahead}</span><span class="sr-only">{ref.ahead} ahead</span>{/if}{#if ref.behind}<span aria-hidden="true">↓{ref.behind}</span><span class="sr-only">{ref.behind} behind</span>{/if}
                      </span>
                    {/each}
                    {#each commit.refs.filter(r => r.startsWith("tag: ")) as tag}
                      <span class="ref truncate">{tag}</span>
                    {/each}
                    {#each [...(prNumbers.get(commit.sha) ?? [])] as n}
                      <span class="ref text-magenta!">#{n}</span>
                    {/each}
                  </span>
                {/if}
                <span class="line-clamp-2 break-words text-body text-ink">{commit.subject}</span>
                <span class="sr-only">{merge ? `Merge commit, ${commit.parents.length} parents` : "Commit"}</span>
                <span class="mt-0.5 flex items-baseline gap-2 text-footnote text-faint">
                  <span class="num">{shortSha(commit.sha)}</span><span class="truncate">{commit.author}</span>
                  <span class="ml-auto shrink-0">{agoShort(commit.date)}</span>
                </span>
              </span>
            </button>
          {/snippet}
        </VirtualList>
      {/key}
    </div>
    {#if more}
      <div class="border-t border-edge-soft px-4"><TouchButton wide disabled={loading} onclick={onmore}>{copy.git.loadMore}</TouchButton></div>
    {/if}
  {/if}
</div>

<style>
  .commit-row { display: flex; align-items: stretch; gap: 8px; padding-left: 8px; width: 100%; min-height: 48px; text-align: left; }
  .commit-row:active { background: var(--color-sel); }
  .commit-row.expanded { min-width: calc(var(--rail) + 15rem); }
  .rail { position: relative; flex: 0 0 var(--rail); overflow: hidden; }
  .rail svg { position: absolute; inset: 0; height: 100%; }
  .node, .outside { position: absolute; top: 50%; transform: translate(-50%, -50%); }
  .node { width: 8px; height: 8px; border-radius: 50%; background: var(--lane); }
  .node.merge { width: 11px; height: 11px; border: 2px solid var(--lane); background: var(--color-canvas); }
  .outside { right: 0; font-weight: 700; }
  .ref { display: inline-flex; align-items: center; gap: 4px; max-width: 100%; padding: 1px 5px; border: 1px solid var(--color-edge); border-radius: 4px; background: var(--color-panel); color: var(--color-subtext); font: var(--text-footnote) / 1.35 var(--font-mono); }
  .ref.current { color: var(--color-info); border-color: color-mix(in srgb, currentColor 48%, transparent); }
</style>
