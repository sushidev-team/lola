<script lang="ts">
  import type { ProjectInfo } from "@bindings/internal/protocol";
  import { store } from "$lib/store.svelte";
  import { buildRows } from "$lib/sidebarlayout";
  import { displayName } from "$lib/slug";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import StatusChip from "@mobile/lib/components/StatusChip.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";

  // Read-only: a row opens the project's detail. The arrangement is the
  // desktop's own `buildRows`, so an empty folder still appears. A folder is
  // an inset group; consecutive loose projects share one.
  type Block = { key: string; header?: string; count?: number; projects: ProjectInfo[] };
  const blocks = $derived.by((): Block[] => {
    const out: Block[] = [];
    for (const row of buildRows(store.projects, store.groups)) {
      if (row.kind === "group") {
        out.push({ key: `g:${row.group.name}`, header: displayName(row.group), count: row.projects.length, projects: row.projects });
        continue;
      }
      const last = out.at(-1);
      if (last && last.header === undefined) last.projects.push(row.project);
      else out.push({ key: `p:${row.project.name}`, projects: [row.project] });
    }
    return out;
  });

  function open(p: ProjectInfo): void {
    router.replace({ name: "tab", tab: "projects", project: p.name });
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <LargeTitle title={copy.projects.title} />

  <div class="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)] content-start gap-4 overflow-y-auto overscroll-contain pb-4">
    {#each blocks as block (block.key)}
      <section>
        {#if block.header}<SectionHeader title={block.header} count={block.count} />{/if}
        {#each block.projects as p (p.name)}
          <button type="button" class="flex min-h-16 w-full items-center gap-3 px-4 text-left active:bg-sel" onclick={() => open(p)}>
            <span class="grid size-9 shrink-0 place-items-center rounded-lg border border-edge-soft bg-panel text-accent">
              <Icon name="folder" size={18} />
            </span>
            <span class="flex min-w-0 flex-1 items-center gap-2 border-b border-edge-soft py-3">
              <span class="min-w-0 flex-1">
                <span class="block truncate text-headline text-ink">{store.displayNameFor(p.name)}</span>
                <span class="flex min-w-0 flex-wrap items-center gap-x-2 text-footnote text-faint">
                  {#if p.defaultBranch}<span class="flex min-w-0 items-center gap-1"><Icon name="git-branch" size={12} /><span class="truncate font-mono">{p.defaultBranch}</span></span>{/if}
                  <span>{store.alive && p.liveCounted ? fill(copy.projects.chips.running, { n: p.liveCounted }) : fill(copy.projects.count, { n: p.sessions })}{p.pollsEnabled ? "" : ` · ${copy.projects.paused}`}</span>
                </span>
              </span>
              {#if p.needsYou}
                <StatusChip tone="urgent" label={fill(copy.projects.chips.needs, { n: p.needsYou })} />
              {:else if p.ciRed}
                <StatusChip tone="broken" label={fill(copy.projects.chips.ciFailed, { n: p.ciRed })} />
              {/if}
              <Icon name="chevron-right" size={16} class="shrink-0 text-faint" />
            </span>
          </button>
        {/each}
      </section>
    {:else}
      <p class="px-8 py-12 text-center text-callout text-faint">
        {store.connected ? copy.projects.empty.body : copy.projects.connecting.status}
      </p>
    {/each}
  </div>
</div>
