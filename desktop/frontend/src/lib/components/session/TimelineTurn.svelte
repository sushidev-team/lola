<script lang="ts">
  // One turn on its rail. Older ones fold to one row, the ask and the answer's first words, and mount
  // no rows, and an off-screen one becomes a placeholder of its measured height. What a person opened
  // lives on the feed and the view, not in here, so nothing is lost when this stops rendering its rows.
  import { store } from "$lib/store.svelte";
  import { agentName } from "$lib/ssh";
  import { feed } from "$lib/feed.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { answerRowId, foldLabel, railKind, turnAsk, turnFiles, turnParts, workLabel, workSegments, type StoryRow as StoryRowData, type Turn } from "$lib/turns";
  import { answerBlocks, plainInline } from "$lib/answerblocks";
  import { clockTime } from "$lib/ssh";
  import Chevron from "../Chevron.svelte";
  import DigLine from "./DigLine.svelte";
  import RailMark from "./RailMark.svelte";
  import StoryRow from "./StoryRow.svelte";
  import CatchUp from "./CatchUp.svelte";

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
  const parts = $derived(turnParts(turn.rows));
  const answerId = $derived(answerRowId(turn, live));
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
  const agentParts = $derived(parts.filter((p) => !(p.kind === "row" && (p.row === person || p.row === receipt))));
  const files = $derived(receipt ? turnFiles(turn.rows) : undefined);
  const personClock = $derived(person && !Number.isNaN(person.at) ? clockTime(new Date(person.at).toISOString()) : "");
  const leads = (row: StoryRowData) => row.kind === "prose" && answerBlocks(row.text)[0]?.kind === "lead";
  // A cursor or a jump landing inside a fold opens it for good, or the row it names is not there.
  $effect(() => {
    if (!cursor) return;
    for (const p of parts) {
      if (p.kind === "work" && p.rows.some((r) => r.id === cursor || (r.kind === "run" && r.items.some((m) => m.id === cursor)))) f.expanded.add(p.id);
    }
  });
</script>

{#snippet rowBody(row: StoryRowData, nested = false)}
    <div
      data-row={row.id}
      data-kind={row.kind}
      class="-mx-2 scroll-mt-2 rounded-sm px-2 {fresh?.(row.id) ? 'ssh-fade-in' : ''} {cursor === row.id ? 'bg-sel/30' : ''}"
    >
      <StoryRow {sessionId} {row} {chars} {showHost} {nested} role={row.id === answerId ? "answer" : "narration"} {latest} files={row.kind === "receipt" ? files : undefined} />
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
    <div class="cv-turn relative">
      {#if person}
        <RailMark kind={railKind(person, answerId, view.filter)} />
        <div class="cv-person">
          <div class="cv-speaker"><strong>{person.kind === "terminal" ? "You · terminal" : "You"}</strong>{#if personClock}<time>{personClock}</time>{/if}</div>
          {@render rowBody(person)}
        </div>
      {/if}
      <div class="cv-agent">
        {#if agentParts.length}
          <div class="cv-speaker" data-agent-speaker><svg class="size-4 text-faint" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m8 8-4 4 4 4m8-8 4 4-4 4m-3-10-2 20" /></svg><strong>{agentName(session?.agent)}</strong></div>
        {/if}
        {#each agentParts as part, i (part.kind === "row" ? part.row.id : part.id)}
          {#if part.kind === "row"}
            {@render rowView(part.row, false, i === 0 ? 0 : part.row.gap)}
          {:else}
            {@const open = f.expanded.has(part.id)}
            <div class="relative" style="margin-top:{i === 0 ? 0 : part.rows[0].gap}px" data-work={part.id}>
              <RailMark kind="tick" />
              <DigLine
                segments={workSegments(part.rows)}
                control
                chevron
                {open}
                word={part.failed > 0 ? `${part.failed} failed` : ""}
                title={part.label}
                onclick={() => (open ? f.expanded.delete(part.id) : f.expanded.add(part.id))}
                data-work-fold=""
              />
              {#if open}
                <div class="mt-0.5 pl-4">
                  {#each part.rows as row, j (row.id)}
                    {@render rowView(j === 0 ? { ...row, gap: 0 } : row, true)}
                  {/each}
                </div>
              {/if}
            </div>
          {/if}
        {/each}
      </div>
      {#if receipt}<div class="cv-turn-end">{@render rowView(receipt)}</div>{/if}
    </div>
  {/if}
</div>
