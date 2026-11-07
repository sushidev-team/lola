<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  import { boardStale, phaseDot, progressText } from "$lib/board";
  import Icon from "./Icon.svelte";
  import LocalChecks from "./LocalChecks.svelte";
  import BoardMarker from "./BoardMarker.svelte";
  import TimeAgo from "./TimeAgo.svelte";

  // The sidebar's board and the story line's Report overlay (StoryLine): the agent's whole
  // self-report — phase, progress, blocker, plan, checks, note — beside its live
  // terminal. The tab is always there; before the first report it says so.
  //
  // Everything here is the agent's own CLAIM ("Agent report" says so in the
  // header), and it fades once stale. The facts — the status pill and the PR
  // badge — stay on the row this session was selected from.
  let { session, embedded = false, showChecks = true, compactWhenDone = false }: { session: SessionInfo; embedded?: boolean; showChecks?: boolean; compactWhenDone?: boolean } = $props();

  const b = $derived(session.board);
  const stale = $derived(boardStale(b));
  const todos = $derived(b?.todos ?? []);
  const checks = $derived(b?.checks ?? []);
  const passing = $derived(checks.filter((c) => c.state === "pass").length);
  const failing = $derived(checks.some((c) => c.state === "fail"));
  const complete = $derived(!!b && !b.blocked &&
    (todos.length ? todos.every(t => t.state === "done") && (!b.hasProgress || b.percent >= 100) : b.hasProgress ? b.percent >= 100 : b.phase === "done"));
  let expanded = $state(false);
  $effect(() => { if (!complete) expanded = false; });
  const collapsible = $derived(compactWhenDone && complete);
  const collapsed = $derived(collapsible && !expanded);


  // Motion means "this is happening now", so it runs only while BOTH are true:
  // the agent axis says the runner is mid-turn (a FACT, from hooks and the pane)
  // and the report is fresh. An idle agent's plan, or a stale one, sits still —
  // a breathing marker on work nobody is doing would be the board lying.
  // prefers-reduced-motion is honoured globally in app.css.
  const live = $derived(!stale && (session.agentState === "working" || session.agentState === "starting"));
</script>

{#snippet heading(text: string, aside: string = "", asideCls: string = "text-faint")}
  <div class="mb-2 flex items-baseline gap-2">
    <span class="label text-faint">{text}</span>
    {#if aside}<span class="num ml-auto text-sm {asideCls}">{aside}</span>{/if}
  </div>
{/snippet}

{#if b}
  <!-- The story line's Report overlay; StoryLine owns its width and scroll. -->
  <section class="flex flex-col" aria-label="Agent report">
    {#if collapsible}
      <button type="button" class="flex w-full items-center gap-2 rounded-sm text-left focus-visible:outline-2 focus-visible:outline-accent" aria-expanded={expanded} onclick={() => expanded = !expanded}>
        <Icon name="circle-check" size={16} class="text-good" />
        <span class="font-medium text-ink">Plan complete</span>
        {#if todos.length}<span class="num text-xs text-faint">{todos.length}/{todos.length}</span>{/if}
        <Icon name={expanded ? "chevron-down" : "chevron-right"} size={14} class="ml-auto text-faint" />
      </button>
    {:else}
    <div class="flex items-center gap-2 {embedded ? 'pb-3' : 'border-b border-edge/60 px-3 py-2'}">
      {#if !embedded}<span class="label text-faint">Agent report</span>{/if}
      {#if b.phase}
        <!-- A dot of the phase's own colour beside the word, so the stage reads
             at a glance before the word does. -->
        <span class="inline-flex items-center gap-1.5 text-sm text-ink-2">
          <span class="h-1.5 w-1.5 rounded-full {phaseDot(b.phase)}" class:breathe-dot={live} aria-hidden="true"></span>
          {b.phase}
        </span>
      {/if}
      <TimeAgo
        at={b.updatedAt}
        class="ml-auto text-sm {stale ? 'text-warn' : 'text-faint'}"
        hint={stale ? "This report is old — the agent may have moved on" : "When the agent last reported"}
      />
    </div>

    {/if}

    {#if !collapsed}
    <div class:mt-4={collapsible} class="flex flex-col gap-5 transition-opacity duration-500 {embedded ? '' : 'px-3 py-3.5'}" class:opacity-60={stale}>
      {#if b.blocked}
        <!-- Orange is the app's "you are needed" colour (the needs-you pill). It
             is still the agent's claim, so it is worded as one — and it says how
             long, which is the part that tells a human how much it matters. -->
        <div class="flex gap-2.5" role="status">
          <BoardMarker kind="blocked" />
          <div class="min-w-0">
            <div class="flex items-baseline gap-1.5">
              <span class="font-medium text-orange">Blocked</span>
              <TimeAgo at={b.blockedAt} ago={false} prefix="· " hint="Blocked since" class="text-sm text-orange/70" />
            </div>
            <div class="selectable mt-0.5 break-words text-ink">{b.blocked}</div>
          </div>
        </div>
      {/if}

      {#if showChecks && b.mismatches?.length}
        <!-- lola's OWN words, not the agent's: the claim audit found a claim
             below that the evidence does not back (no matching command in the
             transcript, a failing last run, failing CI). Warn-yellow, not the
             blocker's orange: it is a doubt to check, not a request to act. -->
        <div class="flex flex-col gap-1" role="status">
          <span class="font-medium text-warn">Claim not backed by evidence</span>
          {#each b.mismatches as m, i (i)}
            <div class="selectable text-sm break-words text-ink">{m}</div>
          {/each}
        </div>
      {/if}

      {#if b.hasProgress}
        <div>
          <!-- "Progress", never "Plan": the plan list below already carries that
               heading, and two "Plan"s stacked read as a rendering glitch. -->
          <div class="mb-1.5 flex items-baseline gap-2 text-sm">
            <span class="truncate text-faint">{b.progressLabel || "Progress"}</span>
            <span class="num ml-auto font-medium text-ink">{progressText(b)}</span>
          </div>
          <div
            class="h-2 overflow-hidden rounded-full bg-edge/60"
            role="progressbar"
            aria-label="reported progress"
            aria-valuemin="0"
            aria-valuemax="100"
            aria-valuenow={b.percent}
          >
            <div
              class="relative h-full overflow-hidden rounded-full bg-good transition-[width] duration-700 ease-out"
              style="width:{b.percent}%"
            >
              {#if live}<span class="shimmer" aria-hidden="true"></span>{/if}
            </div>
          </div>
        </div>
      {/if}

      {#if todos.length}
        <div>
          {@render heading("Plan")}
          <!-- A timeline: one marker per item on a hairline rail, which turns
               green behind each finished step. -->
          <ol class="flex flex-col">
            {#each todos as t, i (i)}
              {@const last = i === todos.length - 1}
              <li class="relative flex items-start gap-2.5 pb-3 last:pb-0">
                {#if !last}
                  <span
                    class="absolute top-[19px] bottom-[3px] left-[7.5px] w-px transition-colors duration-500 {t.state === 'done'
                      ? 'bg-good/50'
                      : 'bg-edge/60'}"
                    aria-hidden="true"
                  ></span>
                {/if}
                <span class="mt-[1px]">
                  <BoardMarker kind={t.state === "done" ? "done" : t.state === "active" ? "active" : "pending"} {live} />
                </span>
                <span
                  class="selectable min-w-0 break-words transition-colors duration-500 {t.state === 'done'
                    ? 'text-faint line-through decoration-edge'
                    : t.state === 'active'
                      ? 'font-medium text-ink'
                      : 'text-ink-2'}">{t.text}</span
                >
                <span class="sr-only">({t.state})</span>
              </li>
            {/each}
          </ol>
        </div>
      {/if}

      {#if showChecks && checks.length}
        <div>
          <!-- The tally names the worst news first: one red check outweighs any
               number of green ones. -->
          {@render heading(
            "Local checks",
            failing ? "failing" : `${passing}/${checks.length} passed`,
            failing ? "text-bad" : passing === checks.length ? "text-good" : "text-faint",
          )}
          <!-- Each check is a row of its own with the summary on a SECOND line
               that wraps: a summary like "demo suites 118/118, vitest 178/178" is
               the evidence, and truncating it to "vitest 178/…" hid exactly the
               numbers that make the claim checkable. Titled "local" because it is
               what the agent says it ran — not CI, which is a gh fact. -->
          <LocalChecks {checks} {live} />
        </div>
      {/if}

      {#if b.note}
        <div>
          {@render heading("Note")}
          <!-- A quote rail rather than bare text: a note is the agent speaking in
               its own words, and the rail sets it apart from the labelled facts
               above without the weight of a box. -->
          <div class="selectable border-l-2 border-accent/50 pl-2.5 break-words text-ink">{b.note}</div>
        </div>
      {/if}
    </div>
    {/if}
  </section>
{:else}
  <!-- The sidebar is always available now, so a session that has not reported
       yet (or whose hooks cannot reach the daemon) says so instead of leaving an
       empty column that reads as broken. -->
  <section class="flex flex-col gap-1.5 px-3 py-3.5 text-sm text-faint" aria-label="Agent report">
    <span class="label">Agent report</span>
    <p>Nothing reported yet. The agent publishes its plan and progress here with <code>lola report</code>.</p>
  </section>
{/if}

<style>
  /* Motion here is slow and low-amplitude on purpose: it sits beside a live
     terminal that is already moving, and must read as a heartbeat, not as
     something asking to be clicked. The markers' own motion lives in
     BoardMarker. */
  .breathe-dot {
    animation: breathe-dot 2.4s ease-in-out infinite;
  }
  @keyframes breathe-dot {
    0%,
    100% {
      opacity: 0.55;
    }
    50% {
      opacity: 1;
    }
  }

  /* A soft highlight drifting across the FILLED part of the bar. It lives inside
     the fill (overflow hidden there), so an empty bar never shimmers. */
  .shimmer {
    position: absolute;
    inset: 0;
    background: linear-gradient(90deg, transparent 0%, color-mix(in srgb, white 35%, transparent) 50%, transparent 100%);
    transform: translateX(-100%);
    animation: shimmer 2.8s ease-in-out infinite;
  }
  @keyframes shimmer {
    0% {
      transform: translateX(-100%);
    }
    60%,
    100% {
      transform: translateX(100%);
    }
  }
</style>
