<script lang="ts">
  // Test harness: the conflict pill's whole reason for stopping propagation is
  // the clickable row it sits in (a table row that selects the session), which a
  // bare render() of the pill cannot express.
  import StatusPill from "./StatusPill.svelte";

  let {
    status = "merge_conflict",
    onResolve = () => {},
    rowClick = () => {},
  }: { status?: string; onResolve?: () => void; rowClick?: () => void } = $props();
</script>

<div role="presentation" onclick={rowClick}>
  <StatusPill {status} {onResolve} resolveBranch="develop" />
</div>
