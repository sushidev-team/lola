<script lang="ts">
  // A block: one command, as a plate at three depths. A plate means lola witnessed this, so every
  // word in it is a fact the daemon recorded: the command, the exit code, the time. The agent's
  // `why` is a claim and sits above the plate on the ground, unboxed, and is gone from Facts.
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
  import Chevron from "../Chevron.svelte";
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
  // `exit 0` is the one word here that may be green, and only on a plate. The rest stays quiet.
  const resultParts = $derived.by(() => {
    const m = /^(exit \S+)(.*)$/.exec(v.result);
    return m ? { word: m[1], tail: m[2] } : { word: v.result, tail: "" };
  });
  const wordCls = $derived(block.status === "succeeded" ? "text-good" : v.resultCls);

  function toggle() {
    if (open) view.expanded.delete(block.id);
    else view.openBlock(block.id);
  }
</script>

{#snippet prompt()}<span class="mr-[1ch] text-faint" aria-hidden="true">$</span>{/snippet}

{#snippet hostMark()}{#if showHost}<span class="mr-[1ch] text-faint">{conn ? hostName(conn) : block.host}</span>{/if}{/snippet}

{#snippet whose()}{#if yours}<span class="mr-[1ch] font-sans text-faint">You</span>{/if}{/snippet}

{#snippet result()}<span class={wordCls}>{resultParts.word}</span>{#if resultParts.tail}<span class="text-faint">{resultParts.tail}</span>{/if}{/snippet}

{#snippet sudo()}<span class="rounded-sm border border-edge px-1 text-ink-2" title="This command raises its rights." data-elevated>sudo</span>{/snippet}

<div data-block={block.id} class="min-w-0">
  {#if titled}
    <p class="selectable mb-1 text-sm break-words text-ink-2" data-claim="why"><span class="sr-only">{agent} says: </span>{block.why}</p>
  {/if}
  <!-- The plate. Nothing the agent composed reaches it except the command. -->
  <div
    data-plate
    data-actor={yours ? "you" : "agent"}
    class="rounded-md border bg-panel font-mono text-sm transition-[border-color,opacity] duration-150 {lit ? 'border-accent' : 'border-edge/60'}"
    class:opacity-70={v.muted}
  >
    {#if !open}
      <!-- A card-shaped row: the whole of it expands, so it is its own button. -->
      <button
        type="button"
        class="grid w-full gap-1 rounded-md px-3 py-2 text-left transition-colors hover:bg-sel/30"
        aria-expanded={false}
        onclick={toggle}
      >
        <span class="grid grid-cols-[minmax(0,1fr)_auto_16px] items-baseline gap-x-2">
          <span class="block min-w-0 truncate" data-command-line>{@render whose()}{@render hostMark()}{@render prompt()}<span class="text-ink">{block.command}</span></span>
          <span class="num flex items-baseline gap-2 whitespace-nowrap">
            {#if block.elevated}{@render sudo()}{/if}
            <span data-result>{@render result()}</span>
          </span>
          <span class="grid place-items-center self-center text-faint"><Chevron /></span>
        </span>
        {#if line2}
          <!-- Only output is clipped. A status sentence is lola's, and it may be the warning. -->
          <span class="text-ink-2 {preview ? 'truncate' : 'font-sans whitespace-normal'}" aria-hidden={!!preview}>
            {#if preview && block.previewStream === "stderr"}<span class="mr-1">stderr</span>{/if}{line2}
          </span>
        {/if}
      </button>
      {#if block.private}
        <div class="flex items-center gap-1 px-3 pb-1.5 font-sans text-faint" data-private>
          <span title="Only you can see this until you share it.">Private</span>
          <span aria-hidden="true">·</span>
          <Button size="xs" disabled={live || !session} title="Only you can see this until you share it." onclick={() => ssh.shareBlock(sessionId, block.id)}>Share</Button>
        </div>
      {/if}
    {:else}
      <div class="grid gap-2 px-3 py-2">
        <SSHOutput {sessionId} {block} onHead={toggle}>
          {#snippet head()}
            <div class="selectable font-medium break-all whitespace-pre-wrap text-ink" data-command-line>{@render whose()}{@render hostMark()}{@render prompt()}{block.command}</div>
            <!-- Silent when all is well. Only what changes how you read the output shows. -->
            {#if block.elevated || block.status !== "succeeded"}
              <div class="num mt-0.5 flex flex-wrap items-center gap-x-2">
                {#if block.elevated}{@render sudo()}{/if}
                {#if block.status !== "succeeded"}<span data-result>{@render result()}</span>{/if}
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
              <Button size="xs" disabled={live || !session} title="Only you can see this until you share it." onclick={() => ssh.shareBlock(sessionId, block.id)}>Share with {agent}</Button>
            {:else}
              <Button size="xs" onclick={() => ssh.askAbout(sessionId, block.id)}>Ask {agent}</Button>
            {/if}
            <Button size="xs" icon aria-expanded={true} aria-label="Collapse {block.id}" onclick={toggle}><span class="rotate-90" aria-hidden="true">›</span></Button>
          {/snippet}
        </SSHOutput>
        {#if v.detail}<p class="font-sans text-ink-2">{v.detail}</p>{/if}
      </div>
    {/if}
  </div>

  <!-- Your deny reason, in your voice. -->
  {#if block.status === "denied" && block.approval?.reason}
    <div class="mt-1 w-fit max-w-full rounded-md bg-sel/35 px-3 py-2 text-prose">
      <span class="sr-only">You said</span>
      <span class="copy selectable text-ink-2">{block.approval.reason}</span>
    </div>
  {/if}
</div>
