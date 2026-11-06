<script lang="ts">
  // The agent's own account of where it is: one row, only when there is an account. Report opens
  // over the story and never pushes it. Everything here is a claim, including the blocker, which
  // is orange text with no Attention, count or tray effect. The audit's doubt is lola's own words.
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { deriveBrief } from "$lib/ssh";
  import { hasChip, hasMismatch, progressText } from "$lib/board";
  import { inScope } from "$lib/turns";
  import { hasReport } from "$lib/sessionview";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import BoardPanel from "../BoardPanel.svelte";

  let { sessionId, onside }: { sessionId: string; onside?: () => void } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const board = $derived(session?.board);
  const brief = $derived(deriveBrief(inScope(view.items, view.scope), sshClock.now));
  const hasBrief = $derived(hasReport(false, brief));
  const shown = $derived(hasChip(board) || hasBrief);
  const blocked = $derived(board?.blocked ?? "");
  const theories = $derived(
    [
      brief.theories.confirmed ? `${brief.theories.confirmed} confirmed` : "",
      brief.theories.open ? `${brief.theories.open} open` : "",
      brief.theories.refuted ? `${brief.theories.refuted} refuted` : "",
    ].filter(Boolean),
  );
  const line = $derived.by(() => {
    if (brief.plan) return { lead: `Plan ${brief.plan.done}/${brief.plan.total}`, rest: brief.plan.current };
    if (board?.hasProgress) return { lead: board.progressDerived ? `Plan ${progressText(board)}` : progressText(board), rest: board.current || board.progressLabel || board.phase || "" };
    if (board?.phase) return { lead: board.phase[0].toUpperCase() + board.phase.slice(1), rest: board.current ?? "" };
    if (brief.summary) return { lead: "", rest: brief.summary.title };
    if (brief.findings.length) return { lead: "Findings", rest: brief.findings.map((f) => `${f.count} ${f.word}`).join(" · ") };
    return { lead: "", rest: theories.length ? `Theories ${theories.join(" · ")}` : "" };
  });
  // On the view, because the session menu opens this from a terminal.
  const open = $derived(view.reportOpen);
  let root = $state<HTMLElement | null>(null);

  function keydown(e: KeyboardEvent) {
    if (open && e.key === "Escape") {
      view.reportOpen = false;
      e.preventDefault();
      e.stopPropagation();
    }
  }
  function pointer(e: PointerEvent) {
    if (open && root && !root.contains(e.target as Node)) view.reportOpen = false;
  }
  function jumpTo(id: string) {
    view.reportOpen = false;
    view.cursor = id;
    view.jump = { block: `item:${id}`, n: (view.jump?.n ?? 0) + 1 };
  }
</script>

<svelte:window onkeydowncapture={keydown} onpointerdowncapture={pointer} />

{#if shown}
  <div bind:this={root} class="cv-band" data-story-line>
    <div class="grid h-6 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 text-sm text-faint">
      <span class="min-w-0 truncate">
        {#if blocked}
          <span class="text-orange">Says blocked: {blocked}</span>
        {:else}
          {#if line.lead}<span class="num">{line.lead}</span>{/if}{#if line.lead && line.rest}{" · "}{/if}{line.rest}
        {/if}
      </span>
      <span class="flex items-center gap-2">
        {#if hasMismatch(board)}<span class="text-warn" title={board.mismatches?.join("\n")}>Unverified claim</span>{/if}
        {#if onside}<Button size="xs" title="Show the agent's account beside the story" onclick={onside}>Sidebar</Button>{/if}
        <Button size="xs" aria-expanded={open} aria-haspopup="dialog" onclick={() => (view.reportOpen = !open)}>Report <Chevron open={!open} class={open ? "-rotate-90" : ""} /></Button>
      </span>
    </div>
    {#if open}
      <div class="absolute inset-x-6 z-20 mx-auto max-h-[60%] max-w-[712px] overflow-y-auto rounded-[10px] border border-edge bg-panel shadow-xl" role="dialog" aria-label="Report" data-report>
        {#if hasBrief}
          <section class="grid gap-2 border-b border-edge/60 px-3 py-3 text-sm" aria-label="What the agent says">
            {#if brief.summary}<p class="selectable text-ink">{brief.summary.title}</p>{/if}
            {#if brief.plan}
              <button type="button" class="flex min-w-0 items-baseline gap-2 text-left hover:text-ink" onclick={() => jumpTo(brief.plan?.id ?? "")}>
                <span class="num text-faint">Plan {brief.plan.done}/{brief.plan.total}</span>
                <span class="truncate text-ink-2">{brief.plan.current}</span>
              </button>
            {/if}
            {#if theories.length}<p class="text-faint">Theories {theories.join(" · ")}</p>{/if}
            {#if brief.findings.length}
              <p class="flex flex-wrap items-baseline gap-x-2 text-faint">
                Findings
                {#each brief.findings as f (f.severity)}<span class="num">{f.count} {f.word}</span>{/each}
              </p>
            {/if}
          </section>
        {/if}
        {#if session && board}<BoardPanel {session} />{/if}
      </div>
    {/if}
  </div>
{/if}
