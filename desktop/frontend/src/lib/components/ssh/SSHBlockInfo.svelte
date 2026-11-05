<script lang="ts">
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
      <svg viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
        {#if ok}<path d="m5 12.5 4.5 4.5L19 7.5" />
        {:else if bad}<path d="M7 7l10 10M17 7 7 17" />
        {:else if live}<circle cx="12" cy="12" r="7" stroke-dasharray="3 4" />
        {:else}<path d="M6 12h12" />{/if}
      </svg>
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
      <svg viewBox="0 0 24 24" class="mt-0.5 size-4 text-faint" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3.5" y="4.5" width="17" height="6" rx="1.5" /><rect x="3.5" y="13.5" width="17" height="6" rx="1.5" /><path d="M7 7.5h.01M7 16.5h.01" /></svg>
      <div class="min-w-0">
        <div class="flex min-w-0 items-center gap-1.5">
          <span class="truncate font-medium text-ink">{host}</span>
          <SSHTierTag tier={block.tier} />
        </div>
        {#if who && who !== host}<div class="selectable font-mono text-sm [overflow-wrap:anywhere] text-faint">{who}</div>{/if}
      </div>
    </div>
    <div class="grid grid-cols-[16px_minmax(0,1fr)] items-start gap-2">
      <svg viewBox="0 0 24 24" class="mt-0.5 size-4 text-faint" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3.5 18.5v-13a1.5 1.5 0 0 1 1.5-1.5h4.5l2.5 3H19a1.5 1.5 0 0 1 1.5 1.5v10a1.5 1.5 0 0 1-1.5 1.5H5a1.5 1.5 0 0 1-1.5-1.5Z" /></svg>
      {#if block.cwd}<span class="selectable font-mono text-sm [overflow-wrap:anywhere] text-ink">{block.cwd}</span>{:else}<span class="text-sm text-faint">Login directory</span>{/if}
    </div>
    <!-- Who: one step for your own command, asked then allowed for the agent's. -->
    <div class="grid grid-cols-[16px_minmax(0,1fr)] items-start gap-2" data-info-who>
      <svg viewBox="0 0 24 24" class="mt-0.5 size-4 text-faint" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="8" r="3.5" /><path d="M5 20c.7-3.6 3.5-5.5 7-5.5s6.3 1.9 7 5.5" /></svg>
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
