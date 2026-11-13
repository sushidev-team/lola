<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // What happened to one command, read top down: how it ended, when, where, on whose say.
  // Every value is a fact the daemon recorded. The why stays out of this panel, because
  // that is the agent's claim.
  import { sshClock } from "$lib/ssh.svelte";
  import {
    allowedLine,
    blockElapsedMs,
    blockView,
    clockSeconds,
    formatBytes,
    formatMs,
    formatSeconds,
    hostName,
    identity,
    isTerminal,
    type SSHBlock,
    type SSHConnInfo,
  } from "$lib/ssh";
  import SSHTierTag from "./SSHTierTag.svelte";

  let { block, conn, agent }: { block: SSHBlock; conn: SSHConnInfo | undefined; agent: string } = $props();

  const live = $derived(!isTerminal(block.status));
  const now = $derived(live ? sshClock.now : Date.now());
  const v = $derived(blockView(block, now));
  const ok = $derived(block.status === "succeeded");
  const bad = $derived(typeof block.exitCode === "number" && block.exitCode !== 0);
  const headline = $derived(typeof block.exitCode === "number" ? `Exit ${block.exitCode}` : v.result);
  const elapsed = $derived(block.startedAt ? formatMs(blockElapsedMs(block, now)) : "");
  const host = $derived(conn ? hostName(conn) : block.host);
  const who = $derived(conn ? identity(conn) : "");
  const size = $derived((block.stdoutBytes ?? 0) + (block.stderrBytes ?? 0));
  const human = $derived(block.actor === "human");
  const proposed = $derived(!!block.proposalElement);
</script>

<div class="grid gap-3 font-sans" data-block-info>
  <!-- How it ended is the headline, and the mark carries it without the colour. -->
  <div class="flex items-center gap-2.5" data-info-result>
    <span
      class="grid size-7 shrink-0 place-items-center rounded-full {ok ? 'bg-good/15 text-good' : bad ? 'bg-bad/15 text-bad' : live ? 'bg-info/15 text-info' : 'bg-sel text-faint'}"
      aria-hidden="true"
    >
      <Icon name={ok ? 'check' : bad ? 'x' : live ? 'loader-circle' : 'minus'} class="size-4" />
    </span>
    <span class="min-w-0 flex-1">
      <span class="num block font-medium {ok ? 'text-good' : bad ? 'text-bad' : 'text-ink'}">{headline}</span>
      {#if size > 0}<span class="num block text-sm text-faint">{formatBytes(size)} of output</span>{/if}
    </span>
    {#if block.elevated}<span class="rounded-sm border border-warn/60 px-1 text-sm text-warn" data-elevated>sudo</span>{/if}
    <span class="selectable num font-mono text-sm text-faint" title="This block's id">{block.id}</span>
  </div>

  <!-- From, until, and how long, as a span rather than two timestamps to subtract. -->
  {#if block.startedAt}
    <div data-info-span>
      <div class="flex items-center gap-1.5">
        <span class="size-2 shrink-0 rounded-full bg-accent" aria-hidden="true"></span>
        <span class="relative h-px flex-1 {live ? 'bg-edge' : 'bg-accent/60'}">
          <span class="num absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 rounded bg-panel px-1.5 text-sm text-ink" data-info-elapsed>{elapsed}</span>
        </span>
        <span class="size-2 shrink-0 rounded-full {live ? 'ssh-pulse border border-accent' : 'bg-accent'}" aria-hidden="true"></span>
      </div>
      <div class="num mt-1 flex justify-between text-sm text-faint">
        <span>{clockSeconds(block.startedAt)}</span>
        <span>{live ? (block.timeoutSeconds ? `Stops after ${formatSeconds(block.timeoutSeconds)}` : "Running") : clockSeconds(block.endedAt)}</span>
      </div>
    </div>
  {/if}

  <div class="grid gap-2 border-t border-edge/60 pt-2.5">
    <!-- Where -->
    <div class="grid grid-cols-[16px_minmax(0,1fr)] items-start gap-2" data-info-where>
      <Icon name="server" class="mt-0.5 size-4 text-faint" />
      <div class="min-w-0">
        <div class="flex min-w-0 items-center gap-1.5">
          <span class="truncate font-medium text-ink">{host}</span>
          <SSHTierTag tier={block.tier} />
        </div>
        {#if who && who !== host}<div class="selectable font-mono text-sm [overflow-wrap:anywhere] text-faint">{who}</div>{/if}
      </div>
    </div>
    <div class="grid grid-cols-[16px_minmax(0,1fr)] items-start gap-2">
      <Icon name="folder" class="mt-0.5 size-4 text-faint" />
      {#if block.cwd}<span class="selectable font-mono text-sm [overflow-wrap:anywhere] text-ink">{block.cwd}</span>{:else}<span class="text-sm text-faint">Login directory</span>{/if}
    </div>
    <!-- Who: one step for your own command, asked then allowed for the agent's. -->
    <div class="grid grid-cols-[16px_minmax(0,1fr)] items-start gap-2" data-info-who>
      <Icon name="user" class="mt-0.5 size-4 text-faint" />
      <div class="flex min-w-0 flex-wrap items-center gap-1.5 text-sm">
        {#if human && !proposed}
          <span class="text-accent-ink">{block.origin === "terminal" ? "You ran this in your shell" : "You ran this"}</span>
        {:else if human}
          <span class="text-magenta">{agent} proposed</span>
          <span class="text-faint" aria-hidden="true">→</span>
          <span class="text-accent-ink">You ran it</span>
        {:else}
          <span class="text-magenta">{agent} asked</span>
          <span class="text-faint" aria-hidden="true">→</span>
          <span class={block.approval?.mode === "grant" ? "text-warn" : "text-ink"}>{block.approval?.mode === "grant" ? "Allowed by your grant" : allowedLine(block, conn)}</span>
        {/if}
      </div>
    </div>
  </div>
</div>
