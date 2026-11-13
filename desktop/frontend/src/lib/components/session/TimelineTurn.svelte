<script lang="ts">
  // One turn on its rail. Older ones fold to one row, the ask and the answer's first words, and mount
  // no rows, and an off-screen one becomes a placeholder of its measured height. What a person opened
  // lives on the feed and the view, not in here, so nothing is lost when this stops rendering its rows.
  import { store } from "$lib/store.svelte";
  import { agentName } from "$lib/ssh";
  import { feed } from "$lib/feed.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { answerRowId, foldLabel, railKind, turnAsk, turnFiles, turnParts, workLabel, type StoryRow as StoryRowData, type Turn } from "$lib/turns";
  import { answerBlocks, plainInline } from "$lib/answerblocks";
  import { clockTime } from "$lib/ssh";
  import Chevron from "../Chevron.svelte";
  import RailMark from "./RailMark.svelte";
  import StoryRow from "./StoryRow.svelte";
  import CatchUp from "./CatchUp.svelte";
  import ActivityLog from "./ActivityLog.svelte";
  import { untrack } from "svelte";


  let {
    sessionId,
    turn,
    folded = false,
    afterFolded = false,
    pinned = false,
    live = false,
    latest = false,
    chars = 72,
    showHost = false,
    cursor = "",
    catchUpRow = "",
    catchUpWords = "",
    fresh,
    observe,
  }: {
    sessionId: string;
    turn: Turn;
    folded?: boolean;
    /** The turn above is folded too: a run of these reads as one list, not as turns apart. */
    afterFolded?: boolean;
    /** Keep the rows mounted whatever the scroll position says: a jump is on its way, or it is the tail. */
    pinned?: boolean;
    /** The newest turn of an agent that can still speak, so its last word is not an answer yet. */
    live?: boolean;
    /** The newest turn of the story, whose receipt keeps its actions out. */
    latest?: boolean;
    chars?: number;
    showHost?: boolean;
    cursor?: string;
    catchUpRow?: string;
    catchUpWords?: string;
    fresh?: (id: string) => boolean;
    observe?: (el: HTMLElement, cb: (near: boolean) => void) => () => void;
  } = $props();

  const session = $derived(store.sessionById(sessionId));
  const f = $derived(feed.view(sessionId));
  const view = $derived(ssh.view(sessionId));
  let el = $state<HTMLElement | null>(null);
  let near = $state(true);
  let height = $state(0);

  $effect(() => {
    if (!el || !observe) return;
    const node = el;
    return observe(node, (isNear) => {
      if (isNear) {
        near = true;
        return;
      }
      // A selection is someone's work, and unmounting its rows would drop it.
      if (window.getSelection?.()?.containsNode?.(node, true)) return;
      height = node.getBoundingClientRect().height;
      near = false;
    });
  });

  const mounted = $derived(near || pinned);
  const answerId = $derived(answerRowId(turn, live));
  const parts = $derived(turnParts(turn.rows, 0, !live, f.standing));
  // History may arrive folded; once a row is presented, later events cannot hide it.
  // Keep this on the feed so virtualization and session navigation preserve the decision.
  $effect(() => {
    if (!mounted || folded) return;
    const shown = parts;
    const id = turn.id;
    untrack(() => {
      f.standing.add(`turn-${id}`);
      for (const part of shown) if (part.kind === "row") f.standing.add(part.row.id);
    });
  });
  // buildStory ends a turn at every message, so a person's row can only ever open one. Their words
  // leave the agent's column and sit on their own side, and everything else stays in order.
  const person = $derived.by(() => {
    const first = parts[0];
    return first?.kind === "row" && (first.row.kind === "you" || first.row.kind === "terminal") ? first.row : undefined;
  });
  const receipt = $derived.by(() => {
    const last = parts[parts.length - 1];
    return last?.kind === "row" && last.row.kind === "receipt" ? last.row : undefined;
  });
  // Join only adjacent answer + receipt; intervening events keep their chronology.
  const joinedReceipt = $derived(!!receipt && !!answerId && turn.rows.at(-2)?.id === answerId);
  const agentParts = $derived(parts.filter((p) => !(p.kind === "row" && (p.row === person || p.row === receipt))));
  const opener = $derived(agentParts[0]?.kind === "row" && (agentParts[0].row.kind === "origin" || (agentParts[0].row.kind === "moment" && agentParts[0].row.item.system?.kind === "handoff")) ? agentParts[0].row : undefined);
  const files = $derived(receipt ? turnFiles(turn.rows) : undefined);
  const personClock = $derived(person && !Number.isNaN(person.at) ? clockTime(new Date(person.at).toISOString()) : "");
  // Keep chronology while grouping each progress note with the work that follows it.
  function workSteps(rows: StoryRowData[]) {
    const groups: StoryRowData[][] = [];
    for (const row of rows) {
      if (!groups.length || row.kind === "prose") groups.push([]);
      groups[groups.length - 1].push(row);
    }
    return groups;
  }
  const leads = (row: StoryRowData) => row.kind === "prose" && answerBlocks(row.text)[0]?.kind === "lead";
  // A cursor or a jump landing inside a fold opens it for good, or the row it names is not there.
  $effect(() => {
    if (!cursor) return;
    for (const p of parts) {
      for (const row of p.kind === "work" ? p.rows : [p.row]) {
        if (p.kind === "work" && (row.id === cursor || row.ids.includes(cursor))) f.expanded.add(p.id);
        if (row.kind === "run" && row.items.some(item => item.id === cursor)) f.expanded.add(row.id);
      }
    }
  });
</script>

{#snippet rowBody(row: StoryRowData, nested = false)}
    <div
      data-row={row.id}
      data-kind={row.kind}
      class="-mx-2 scroll-mt-2 rounded-sm px-2 {row.id === answerId ? 'cv-result' : ''} {fresh?.(row.id) ? 'ssh-fade-in' : ''} {cursor === row.id ? 'bg-sel/30' : ''}"
    >
      <StoryRow active={live && row.id === turn.rows.at(-1)?.id} {sessionId} {row} {chars} {showHost} {nested} role={row.id === answerId ? "answer" : "narration"} {latest} files={row.kind === "receipt" ? files : undefined} />
    </div>
{/snippet}

{#snippet rowView(row: StoryRowData, nested = false, gap = row.gap)}
  {#if row.id === catchUpRow}<div class="py-4"><CatchUp words={catchUpWords} /></div>{/if}
  <div class="relative" style="margin-top:{gap}px">
    {#if !nested}<RailMark kind={railKind(row, answerId, view.filter)} lead={leads(row)} />{/if}
    {@render rowBody(row, nested)}
  </div>
{/snippet}

<div
  bind:this={el}
  data-turn={turn.id}
  style="margin-top:{folded && afterFolded ? 4 : turn.gap}px;{mounted ? '' : `height:${height}px`}"
>
  {#if folded}
    {@const ask = turnAsk(turn)}
    {@const label = foldLabel(turn)}
    <!-- Only what a person typed goes on their side. A background task that opened a turn is not them. -->
    {@const theirs = turn.rows[0]?.kind === "you" || turn.rows[0]?.kind === "terminal"}
    {@const did = label && label !== ask ? plainInline(label) : workLabel(turn.rows)}
    <div class="relative">
      <RailMark kind="folded" />
      <!-- The row is the control, as in ToolRow. Its receipt's actions are one click away, in the turn.
           What the agent did on its side and what the person asked on theirs, so position still says who. -->
      <button
        type="button"
        class="cv-fold group -mx-2 min-h-[26px] w-[calc(100%+16px)] items-center rounded-sm px-2 text-left text-sm text-ink-2 hover:bg-sel/30 focus-visible:-outline-offset-1"
        aria-expanded={false}
        title={ask || undefined}
        onclick={() => f.expanded.add(`turn-${turn.id}`)}
        data-turn-fold
      >
        <span class="flex min-w-0 items-center gap-1.5">
          <span class="min-w-0 truncate">{did || (theirs ? "" : ask)}</span>
          <span class="grid shrink-0 place-items-center text-faint group-hover:text-ink"><Chevron /></span>
          {#if turn.meta}<span class="num text-sm whitespace-nowrap text-faint">{turn.meta}</span>{/if}
        </span>
        <span class="cv-fold-ask min-w-0 truncate" data-turn-ask>{theirs ? ask : ""}</span>
      </button>
    </div>
  {:else if mounted}
    {#if person && person.id === catchUpRow}<div class="py-4"><CatchUp words={catchUpWords} /></div>{/if}
    <!-- Person first in the DOM, so reading order stays chronological. Which side and which column is
         app.css's: one DOM for the wide layout, the short one and the single column. -->
    <div class="cv-turn relative" class:cv-result-with-footer={joinedReceipt}>
      {#if person}
        <RailMark kind={railKind(person, answerId, view.filter)} />
        <div class="cv-person">
          <div class="cv-speaker"><strong>You</strong>{#if person.kind === "terminal"}<span class="cv-source-badge">Terminal</span>{/if}{#if personClock}<time>{personClock}</time>{/if}</div>
          {@render rowBody(person)}
        </div>
      {/if}
      <div class="cv-agent">
        {#if opener}<div class="mb-4">{@render rowView(opener, false, 0)}</div>{/if}
        {#if agentParts.length > (opener ? 1 : 0)}
          <span class="sr-only" data-agent-speaker>{agentName(session?.agent)} response</span>
        {/if}
        {#each agentParts as part, i (part.kind === "row" ? part.row.id : part.id)}
          <div class="cv-turn-part">
          {#if part.kind === "row" && part.row === opener}
            <!-- System origin precedes the agent identity. -->
          {:else if part.kind === "row"}
            {@render rowView(part.row, false, i === 0 ? 0 : part.row.gap)}
          {:else}
            <div class="cv-work-flat" style="margin-top:{i === 0 ? 0 : part.rows[0].gap}px">
              {#if !f.expanded.has(part.id) && part.rows.some(row => row.id === catchUpRow)}<div class="py-4"><CatchUp words={catchUpWords} /></div>{/if}
              <ActivityLog {sessionId} id={part.id} rows={part.rows} failed={part.failed}>
              {#each workSteps(part.rows) as step (step[0].id)}
                <section class="cv-work-step" aria-label="Work log entry">
                  {#each step as row (row.id)}
                    {@render rowView(row, true, 0)}
                  {/each}
                </section>
              {/each}
              </ActivityLog>
            </div>
          {/if}
          </div>
        {/each}
      </div>
      {#if receipt}<div class="cv-turn-end" class:cv-result-footer={joinedReceipt}>{@render rowView(receipt, false, joinedReceipt ? 0 : receipt.gap)}</div>{/if}
    </div>
  {/if}
</div>
