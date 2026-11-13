<script lang="ts">
  // "Sync now" plus what it did. The daemon only QUEUES a pass, so the store
  // keeps re-reading until a heartbeat (or an error) newer than the press shows
  // up; this draws that life: Syncing… → Synced, or the daemon's reason. At
  // rest it shows how old the last heartbeat is, live, so the view reads as
  // current without anyone pressing anything (the store polls while watched
  // and re-reads when the window comes back).
  import Button from "$lib/components/Button.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import TimeAgo from "$lib/components/TimeAgo.svelte";
  import { hub } from "$lib/hub.svelte";
  import { hubReason } from "$lib/hubboard";

  let { size = "md" }: { size?: "sm" | "md" } = $props();

  const phase = $derived(hub.syncPhase);
  const syncing = $derived(phase === "syncing");
  const beat = $derived(hub.status?.lastHeartbeat);
</script>

<span class="flex min-w-0 items-center gap-2.5" data-hub-sync={phase || "idle"}>
  <span role="status" aria-live="polite" class="min-w-0 truncate text-sm">
    {#if phase === "failed"}
      <span class="text-bad" title={hub.syncError}>{hubReason(hub.syncError) || "Sync failed"}</span>
    {:else if phase === "slow"}
      <span class="text-warn">No answer yet. The daemon keeps trying.</span>
    {:else if beat && !syncing}
      <TimeAgo at={beat} prefix="Updated " class="text-faint" />
    {/if}
  </span>
  <Button variant="secondary" {size} disabled={syncing || hub.busy !== ""} aria-busy={syncing} onclick={() => void hub.sync()}>
    {#if phase === "synced"}
      <span class="text-good"><Icon name="check" size={14} /></span> Synced
    {:else}
      <span class="inline-flex {syncing ? 'animate-spin motion-reduce:animate-[spin_1.8s_linear_infinite]' : ''}"><Icon name="refresh-cw" size={14} /></span>
      {syncing ? "Syncing…" : "Sync now"}
    {/if}
  </Button>
</span>
