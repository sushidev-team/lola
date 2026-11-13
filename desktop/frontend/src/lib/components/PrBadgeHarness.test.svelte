<script lang="ts">
  // Test harness: the badge's whole reason for stopping propagation is the
  // clickable row it sits in (a table row that selects the session, a grid tile
  // that opens a terminal), which a bare render() of the badge cannot express.
  import PrBadge from "./PrBadge.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  let {
    session,
    onOpen = () => {},
    rowClick = () => {},
  }: {
    session: Pick<SessionInfo, "prNumber" | "prUrl" | "checks" | "review">;
    onOpen?: () => void;
    rowClick?: () => void;
  } = $props();
</script>

<div role="presentation" onclick={rowClick}>
  <PrBadge {session} delivery="draft" {onOpen} />
</div>
