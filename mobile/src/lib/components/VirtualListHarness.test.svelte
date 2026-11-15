<script lang="ts">
  import VirtualList, { type RowKind } from "./VirtualList.svelte";

  // Mounts the list with plain rows so a test can drive it with an items array.
  let {
    items,
    kind,
  }: { items: { id: string; kind?: RowKind }[]; kind?: (it: { id: string; kind?: RowKind }) => RowKind } = $props();
</script>

<VirtualList {items} key={(it) => it.id} kind={kind ?? ((it) => it.kind ?? "row")} label="Rows">
  {#snippet row(it)}
    <div data-row={it.id}>{it.id}</div>
  {/snippet}
</VirtualList>
