<script lang="ts">
  import { copy } from "@mobile/lib/copy";
  import { BUCKETS, type Bucket } from "@mobile/lib/inbox.svelte";
  import type { InboxGroup } from "@mobile/lib/prefs";
  import type { InboxFilter } from "@mobile/lib/router.svelte";
  import ListGroup from "./ListGroup.svelte";
  import ListRow from "./ListRow.svelte";
  import Sheet from "./Sheet.svelte";

  // The Inbox filter: one choice, applied at once. Counts come from the caller,
  // so the sheet reads no store. The selected row shows a check INSTEAD of its
  // count; the others show theirs in faint tabular figures. "Group by" is a
  // second choice of the same kind: a preference, so it never touches the route.

  let {
    value,
    counts,
    group = "status",
    onselect,
    ongroup = () => {},
    onclose,
  }: {
    value: InboxFilter;
    counts: Readonly<Record<Bucket, number>>;
    group?: InboxGroup;
    onselect: (v: InboxFilter) => void;
    ongroup?: (g: InboxGroup) => void;
    onclose: () => void;
  } = $props();

  const options = $derived([
    { value: "" as InboxFilter, label: "All", count: BUCKETS.reduce((n, b) => n + counts[b], 0) },
    ...BUCKETS.map((b) => ({ value: b as InboxFilter, label: copy.inbox.sections[b], count: counts[b] })),
  ]);

  const groupOptions: { value: InboxGroup; label: string }[] = [
    { value: "status", label: copy.inbox.groupBy.status },
    { value: "project", label: copy.inbox.groupBy.project },
  ];
</script>

<Sheet title="Filter" detent="fit" {onclose}>
  <div role="radiogroup" aria-label="Filter">
    <ListGroup>
      {#each options as o (o.value)}
        <ListRow label={o.label} role="radio" checked={value === o.value} onclick={() => onselect(o.value)}>
          {#snippet trailing()}
            {#if value !== o.value}<span class="num text-callout text-faint tabular-nums">{o.count}</span>{/if}
          {/snippet}
        </ListRow>
      {/each}
    </ListGroup>
  </div>
  <div role="radiogroup" aria-label={copy.inbox.groupBy.title}>
    <ListGroup header={copy.inbox.groupBy.title}>
      {#each groupOptions as o (o.value)}
        <ListRow label={o.label} role="radio" checked={group === o.value} onclick={() => ongroup(o.value)} />
      {/each}
    </ListGroup>
  </div>
</Sheet>
