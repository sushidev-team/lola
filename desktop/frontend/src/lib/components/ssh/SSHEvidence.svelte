<script lang="ts">
  // Where a claim touches something recorded: one quiet line under the claim. It names the command
  // rather than a block id nobody has seen, and shows its facts. It never says "verified", because
  // the block being real does not make the claim true. No box, since a claim is not a plate and a
  // third boxed kind would blur what a box means. It reads the live block when it can, else the
  // facts the daemon copied into the evidence at post time.
  import { ssh } from "$lib/ssh.svelte";
  import { agentName, blockElapsedMs, formatMs } from "$lib/ssh";
  import { store } from "$lib/store.svelte";
  import Button from "../Button.svelte";

  let {
    sessionId,
    block,
    verified,
    status = "",
    exitCode = undefined,
  }: { sessionId: string; block: string; verified: boolean; status?: string; exitCode?: number | null } = $props();

  const view = $derived(ssh.view(sessionId));
  const live = $derived(view.block(block));
  const shownStatus = $derived(live?.status ?? status);
  const shownExit = $derived(live?.exitCode ?? exitCode);
  const open = $derived(shownStatus === "running" || shownStatus === "pending_approval" || shownStatus === "queued");
  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));
  const known = $derived(verified && (!!live || !!status));
  const bad = $derived(typeof shownExit === "number" && shownExit !== 0);
  const tail = $derived(live && !open ? ` · ${formatMs(blockElapsedMs(live))}` : "");
</script>

{#if !known}
  <span
    class="inline rounded border border-edge/60 bg-panel px-1.5 font-mono text-sm text-warn"
    title="Lola has no record of this block. It may have been removed to stay within the limit.">{block} not on record</span
  >
{:else}
  <Button
    variant="bare"
    size="xs"
    class="h-auto! min-w-0 shrink! gap-[1ch]! px-0! align-baseline font-mono! text-faint! enabled:hover:text-ink!"
    title="{live?.command ? `${live.command}\n` : ''}Lola recorded this command. That does not mean {agent}'s reading of it is correct."
    onmouseenter={() => (view.hoverBlock = block)}
    onmouseleave={() => (view.hoverBlock = "")}
    onclick={(e: MouseEvent) => {
      e.stopPropagation();
      ssh.digTo(sessionId, block);
    }}
  >
    <span class="max-w-[48ch] min-w-0 truncate" data-evidence-command>{live?.command ?? block}</span>
    {#if open}
      <span class="shrink-0 text-info">{shownStatus === "running" ? "running" : "waiting"}</span>
    {:else if typeof shownExit === "number"}
      <span class="num shrink-0 {bad ? 'text-bad' : ''}">exit {shownExit}{tail}</span>
    {:else}
      <span class="shrink-0">{(shownStatus || "").replace("_", " ")}</span>
    {/if}
  </Button>
{/if}
