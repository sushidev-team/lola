<script lang="ts">
  // A block: one command, as a plate at three depths. The agent's `why` is its title,
  // because that is what you read first, with the command under it. The why is still a
  // CLAIM and everything else a fact the daemon recorded, so it stays visibly the agent's:
  // a sans line behind a hollow ring, never in the mono the facts use, and gone from the
  // Facts view.
  //
  // A non-zero exit is not a verdict: `grep` exits 1 on no match. The row says
  // "exit 1" in red and never the word "failed".
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import {
    SSH_REMOTE_OUTLIVES,
    agentName,
    blockView,
    hostName,
    isTerminal,
    previewLine,
    type SSHBlock,
    type Filter,
  } from "$lib/ssh";
  import Button from "../Button.svelte";
  import SSHOutput from "./SSHOutput.svelte";
  import SSHBlockInfo from "./SSHBlockInfo.svelte";

  let {
    sessionId,
    block,
    filter = "everything",
    showHost = false,
  }: { sessionId: string; block: SSHBlock; filter?: Filter; showHost?: boolean } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const conn = $derived(view.connections.find((c) => c.id === block.conn));
  const open = $derived(view.expanded.has(block.id));
  const yours = $derived(block.actor === "human" && !block.proposalElement);
  const live = $derived(!isTerminal(block.status));
  const titled = $derived(!!block.why && filter !== "facts");
  const now = $derived(live ? sshClock.now : Date.now());
  const v = $derived(blockView(block, now));
  const lit = $derived(view.hoverBlock === block.id || view.flashBlock === block.id);
  // A success is one quiet line: the marker says it worked, and opening it shows the rest.
  const preview = $derived(v.showsPreview && block.status !== "succeeded" ? previewLine(block) : "");
  const line2 = $derived(preview || v.detail);

  function toggle() {
    if (open) view.expanded.delete(block.id);
    else view.openBlock(block.id);
  }
</script>

{#snippet prompt()}<span class="mr-[1ch] {yours ? 'text-accent-ink' : 'text-faint'}" aria-hidden="true">$</span>{/snippet}

{#snippet why(clip: boolean)}
  <span class="grid grid-cols-[12px_minmax(0,1fr)] items-baseline gap-1.5 font-sans" data-claim="why">
    <span class="inline-block h-2 w-2 translate-y-px rounded-full border border-faint" aria-hidden="true"></span>
    <span class="selectable min-w-0 text-ink {clip ? 'truncate' : 'break-words whitespace-pre-wrap'}"><span class="sr-only">{agent} says: </span>{block.why}</span>
  </span>
{/snippet}

<!-- A conversation: yours on the right, the agent's on the left. An open block takes its
     side's full share, because output needs the width. -->
<div data-block={block.id} data-side={yours ? "right" : "left"} class="min-w-0 max-w-[80%] {yours ? 'ml-auto' : ''} {open ? 'w-[80%]' : 'w-fit'}">
  <!-- The plate. Nothing the agent composed reaches it except the command. -->
  <div
    data-plate
    data-actor={yours ? "you" : "agent"}
    class="rounded-md border font-mono text-sm transition-[border-color,opacity] duration-200 {yours ? 'border-l-2 border-l-accent bg-accent-fill/25' : 'bg-panel'} {v.plate} {lit ? 'border-accent!' : ''}"
    class:opacity-70={v.muted}
  >
    {#if !open}
      <!-- A card-shaped row: the whole of it expands, so it is its own button. -->
      <button
        type="button"
        class="grid w-full gap-0.5 rounded-md px-2.5 py-1.5 text-left transition-colors hover:bg-sel/30"
        aria-expanded={false}
        onclick={toggle}
      >
        <span class="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-3">
          <span class="min-w-0">
            {#if titled}
              {@render why(true)}
            {:else}
              <span class="block truncate">{@render prompt()}{#if showHost}<span class="mr-[1ch] text-faint">{conn ? hostName(conn) : block.host}</span>{/if}<span class="text-ink">{block.command}</span></span>
            {/if}
          </span>
          <span class="num flex items-baseline gap-1.5 whitespace-nowrap text-faint">
            {#if block.elevated}<span class="rounded-sm border border-warn/60 px-1 text-warn" title="This command raises its rights." data-elevated>sudo</span>{/if}
            {#if block.status !== "succeeded"}<span class={v.resultCls}>{v.result}</span>{/if}
            {#if block.private}<svg viewBox="0 0 24 24" class="h-3 w-3 self-center" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" role="img" aria-label="Only you can see this"><rect x="5" y="11" width="14" height="9" rx="2" /><path d="M8 11V8a4 4 0 0 1 8 0v3" /></svg>{/if}
            <span class="grid size-5 place-items-center self-center rounded font-sans text-faint" aria-hidden="true">›</span>
          </span>
        </span>
        {#if titled}
          <span class="block truncate text-[12px] text-faint" data-command-line>{@render prompt()}{#if showHost}<span class="mr-[1ch] text-faint">{conn ? hostName(conn) : block.host}</span>{/if}{block.command}</span>
        {/if}
        {#if line2}
          <!-- Only output is clipped. A status sentence is lola's, and it may be the warning. -->
          <span class="text-faint {preview ? 'truncate' : 'font-sans whitespace-normal'}" aria-hidden={!!preview}>
            {#if preview && block.previewStream === "stderr"}<span class="mr-1 text-faint/80">stderr</span>{/if}{line2}
          </span>
        {/if}
      </button>
    {:else}
      <div class="grid gap-2 px-2.5 py-2">
        <SSHOutput {sessionId} {block} onHead={toggle}>
          {#snippet head()}
            {#if titled}
              {@render why(false)}
              <div class="selectable mt-0.5 text-[12px] whitespace-pre-wrap break-all text-faint" data-command-line>{@render prompt()}{block.command}</div>
            {:else}
              <div class="selectable text-[13.5px] font-medium whitespace-pre-wrap break-all text-ink" data-command-line>{@render prompt()}{block.command}</div>
            {/if}
            <!-- Silent when all is well. Only what changes how you read the output shows. -->
            {#if block.elevated || block.status !== "succeeded"}
              <div class="num mt-0.5 flex flex-wrap items-center gap-x-1.5 text-faint">
                {#if block.elevated}<span class="rounded-sm border border-warn/60 px-1 text-warn" data-elevated>sudo</span>{/if}
                {#if block.status !== "succeeded"}<span class={v.resultCls}>{v.result}</span>{/if}
              </div>
            {/if}
          {/snippet}
          {#snippet info()}
            <SSHBlockInfo {block} {conn} {agent} />
          {/snippet}
          {#snippet tail()}
            {#if block.status === "running" && block.origin !== "terminal"}
              <Button variant="danger" size="xs" title={SSH_REMOTE_OUTLIVES} onclick={() => ssh.stop(sessionId, block)}>Stop</Button>
            {/if}
            {#if block.private}
              <Button size="xs" disabled={live} title="Only you can see this until you share it." onclick={() => ssh.shareBlock(sessionId, block.id)}>Share with {agent}</Button>
            {:else}
              <Button size="xs" onclick={() => ssh.askAbout(sessionId, block.id)}>Ask {agent}</Button>
            {/if}
            <Button size="xs" icon aria-expanded={true} aria-label="Collapse {block.id}" onclick={toggle}><span class="rotate-90" aria-hidden="true">›</span></Button>
          {/snippet}
        </SSHOutput>
        {#if v.detail}<p class="font-sans text-faint">{v.detail}</p>{/if}
      </div>
    {/if}
  </div>

  <!-- Your deny reason, in your voice. -->
  {#if block.status === "denied" && block.approval?.reason}
    <div class="mt-1 rounded-md bg-sel/50 px-2.5 py-1.5">
      <span class="text-sm text-accent-ink">You</span>
      <span class="copy selectable ml-1.5 text-ink">{block.approval.reason}</span>
    </div>
  {/if}

</div>
