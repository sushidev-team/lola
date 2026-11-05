<script lang="ts">
  // The glance. Derived from timeline items: the agent's standing account, then one
  // fact row. "Account" is deliberate: it is theirs. An old summary fades, because a
  // confident stale summary is worse than none.
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { deriveBrief, nowRow, planBar } from "$lib/ssh";
  import Button from "../Button.svelte";
  import LivePulse from "../LivePulse.svelte";
  import TimeAgo from "../TimeAgo.svelte";
  import BoardMarker from "../BoardMarker.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const brief = $derived(deriveBrief(view.tabItems, sshClock.now));
  const approvalWaiting = $derived(view.tabItems.some((i) => i.block?.status === "pending_approval"));
  const row = $derived(nowRow(view.tabItems, session ?? {}, sshClock.now));
  const hasAccount = $derived(
    !!brief.summary || !!brief.plan || brief.findings.length > 0 || brief.theories.open + brief.theories.confirmed + brief.theories.refuted > 0,
  );
  const collapsed = $derived(view.briefCollapsed);
  const theoryParts = $derived(
    [
      brief.theories.confirmed ? `${brief.theories.confirmed} confirmed` : "",
      brief.theories.open ? `${brief.theories.open} open` : "",
      brief.theories.refuted ? `${brief.theories.refuted} refuted` : "",
    ].filter(Boolean),
  );

  function jumpTo(id: string) {
    view.cursor = id;
    view.jump = { block: `item:${id}`, n: (view.jump?.n ?? 0) + 1 };
  }
</script>

<!-- A waiting approval has its own card right below, so the line would only repeat it. -->
{#if hasAccount || !approvalWaiting}
<section class="max-h-[33cqh] overflow-y-auto border-b border-edge/60 px-3 py-1.5 text-sm" aria-label="Brief">
  {#if hasAccount}
   <div class="ssh-brief-account">
    <div class="ssh-brief-label flex items-center">
      <span class="label text-faint">Agent's account</span>
      <Button
        size="xs"
        icon
        class="ml-auto"
        aria-expanded={!collapsed}
        aria-label={collapsed ? "Expand the brief" : "Collapse the brief"}
        onclick={() => (view.briefCollapsed = !collapsed)}>{collapsed ? "▸" : "▾"}</Button
      >
    </div>
    <div class="mt-1 grid gap-1">
      {#if brief.summary}
        <div class="grid grid-cols-[14px_minmax(0,1fr)_auto] items-baseline gap-1.5" class:opacity-60={brief.summary.stale}>
          <span class="inline-block h-2 w-2 translate-y-px justify-self-center rounded-full border border-ink" aria-hidden="true"></span>
          <span class="ssh-brief-summary copy selectable min-w-0 text-ink" class:truncate={collapsed}>{brief.summary.title}</span>
          <TimeAgo at={brief.summary.at} ago={false} class="text-sm {brief.summary.stale ? 'text-warn' : 'text-faint'}" />
        </div>
      {/if}
      {#if !collapsed}
        {#if brief.plan}
          <button
            type="button"
            class="ssh-brief-more grid grid-cols-[14px_auto_auto_minmax(0,1fr)] items-baseline gap-1.5 text-left hover:text-ink"
            onclick={() => jumpTo(brief.plan?.id ?? "")}
          >
            <span class="inline-block h-2 w-2 translate-y-px justify-self-center rounded-full border border-faint" aria-hidden="true"></span>
            <span class="num text-sm text-faint">Plan {brief.plan.done}/{brief.plan.total}</span>
            <span class="text-sm tracking-tight text-faint" aria-hidden="true">{planBar(brief.plan.done, brief.plan.total)}</span>
            <span class="truncate text-ink/85">{brief.plan.current}</span>
          </button>
        {/if}
        {#if theoryParts.length || brief.findings.length}
          <div class="ssh-brief-more grid grid-cols-[14px_minmax(0,1fr)] items-baseline gap-1.5">
            <span class="inline-block h-2 w-2 translate-y-px justify-self-center rounded-full border border-faint" aria-hidden="true"></span>
            <span class="flex flex-wrap items-baseline gap-x-3 text-sm">
              {#if theoryParts.length}
                <button type="button" class="text-faint hover:text-ink" onclick={() => (view.filter = "findings")}>Theories {theoryParts.join(" · ")}</button>
              {/if}
              {#if brief.findings.length}
                <button type="button" class="inline-flex flex-wrap items-center gap-x-2 text-faint hover:text-ink" onclick={() => (view.filter = "findings")}>
                  Findings
                  {#each brief.findings as f (f.severity)}
                    <span class="inline-flex items-center gap-1"><span class="inline-block h-2 w-2 rounded-full {f.cls}" aria-hidden="true"></span><span class="num">{f.count} {f.word}</span></span>
                  {/each}
                </button>
              {/if}
            </span>
          </div>
        {/if}
      {/if}
    </div>
    <div class="my-1.5 h-px bg-edge/40" aria-hidden="true"></div>
   </div>
  {/if}
  <!-- The Now row: the only fact here, in Lola's voice, with a solid marker. -->
  {#if !approvalWaiting}
  <div class="grid grid-cols-[14px_minmax(0,1fr)_auto] items-center gap-1.5" data-now-row>
    <span class="inline-flex justify-center">
      {#if row.block}<BoardMarker kind="running" live size={12} />{:else}<span class="inline-block h-1.5 w-1.5 rounded-[1px] {row.cls === 'text-orange' ? 'bg-orange' : 'bg-faint'}" aria-hidden="true"></span>{/if}
    </span>
    <span class="flex min-w-0 items-center gap-1.5 {row.cls}">
      {row.text}
      {#if row.command}<span class="selectable truncate font-mono text-sm text-ink">{row.command}</span>{/if}
      {#if row.tail}<span class="num shrink-0 text-sm text-faint">{row.tail}</span>{/if}
      {#if row.pulse}<LivePulse agentState="working" />{/if}
    </span>
    <span class="flex items-center">
      {#if row.action === "stop" && row.block}
        <Button variant="danger" size="xs" onclick={() => row.block && ssh.stop(sessionId, row.block)}>Stop</Button>
      {:else if row.action === "show"}
        <Button size="xs" class="text-orange!" onclick={() => ssh.showAgentPane(sessionId)}>Show</Button>
      {:else if row.action === "revive"}
        <Button variant="accent" size="xs" onclick={() => store.revive(sessionId)}>Revive</Button>
      {/if}
    </span>
  </div>
  {/if}
</section>
{/if}
