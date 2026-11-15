<script lang="ts">
  import type { ProjectInfo } from "@bindings/internal/protocol";
  import { store } from "$lib/store.svelte";
  import { buildRows } from "$lib/sidebarlayout";
  import { displayName } from "$lib/slug";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
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
      <ListGroup header={block.header} count={block.count}>
        {#each block.projects as p (p.name)}
          <ListRow
            label={store.displayNameFor(p.name)}
            detail={fill(copy.projects.count, { n: p.sessions }) +
              (p.pollsEnabled > 0 ? "" : ` · ${copy.projects.paused}`)}
            chevron
            onclick={() => open(p)}
          />
        {/each}
      </ListGroup>
    {:else}
      <p class="px-8 py-12 text-center text-callout text-faint">
        {store.connected ? copy.projects.empty.body : copy.projects.connecting.status}
      </p>
    {/each}
  </div>
</div>
