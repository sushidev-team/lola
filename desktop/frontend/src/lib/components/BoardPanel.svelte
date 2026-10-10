<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  import { boardStale, phaseChip, phaseDot, progressText, checkKind, CHECK_WORD, agoShort, EVIDENCE_TAG } from "$lib/board";
  import BoardMarker from "./BoardMarker.svelte";

  // The session sidebar's Report tab (SidePanel): the agent's whole
  // self-report — phase, progress, blocker, plan, checks, note — beside its live
  // terminal. SidePanel offers the tab only when the agent has reported
  // something.
  //
  // Everything here is the agent's own CLAIM ("Agent report" says so in the
  // header), and it fades once stale. The facts — the status pill and the PR
  // badge — stay on the row this session was selected from.
  let { session }: { session: SessionInfo } = $props();

  const b = $derived(session.board);
  const stale = $derived(boardStale(b));
  const todos = $derived(b?.todos ?? []);
  const checks = $derived(b?.checks ?? []);
  const passing = $derived(checks.filter((c) => c.state === "pass").length);
  const failing = $derived(checks.some((c) => c.state === "fail"));
  const blockedFor = $derived(agoShort(b?.blockedAt));

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
  <!-- The sidebar's Report tab; SidePanel owns the column, its width and scroll. -->
  <section class="flex flex-col" aria-label="Agent report">
    <div class="flex items-center gap-2 border-b border-edge/60 px-3 py-2">
      <span class="label text-faint">Agent report</span>
      {#if b.phase}
        <!-- A dot of the phase's own colour inside the chip, so the stage reads
             at a glance before the word does. -->
        <span class="inline-flex items-center gap-1.5 rounded-full px-2 py-[1px] text-sm {phaseChip(b.phase)}">
          <span class="h-1.5 w-1.5 rounded-full {phaseDot(b.phase)}" class:breathe-dot={live} aria-hidden="true"></span>
          {b.phase}
        </span>
      {/if}
      {#if b.updatedAgo}
        <span
          class="num ml-auto text-sm text-faint"
          class:text-warn={stale}
          title={stale ? "this report is old — the agent may have moved on" : "when the agent last reported"}
          >{b.updatedAgo} ago</span
        >
      {/if}
    </div>

    <div class="flex flex-col gap-5 px-3 py-3.5 transition-opacity duration-500" class:opacity-60={stale}>
      {#if b.blocked}
        <!-- Orange is the app's "you are needed" colour (the needs-you pill). It
             is still the agent's claim, so it is worded as one — and it says how
             long, which is the part that tells a human how much it matters. -->
        <div class="flex gap-2.5 rounded-lg border border-orange/40 bg-orange/10 px-2.5 py-2.5" role="status">
          <BoardMarker kind="blocked" />
          <div class="min-w-0">
            <div class="flex items-baseline gap-1.5">
              <span class="font-medium text-orange">Blocked</span>
              {#if blockedFor}<span class="num text-sm text-orange/70">· {blockedFor === "now" ? "just now" : `${blockedFor}`}</span>{/if}
            </div>
            <div class="selectable mt-0.5 break-words text-ink">{b.blocked}</div>
          </div>
        </div>
      {/if}

      {#if b.mismatches?.length}
        <!-- lola's OWN words, not the agent's: the claim audit found a claim
             below that the evidence does not back (no matching command in the
             transcript, a failing last run, failing CI). Warn-yellow, not the
             blocker's orange: it is a doubt to check, not a request to act. -->
        <div class="flex flex-col gap-1 rounded-lg border border-warn/40 bg-warn/10 px-2.5 py-2.5" role="status">
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
                      : 'text-ink/75'}">{t.text}</span
                >
                <span class="sr-only">({t.state})</span>
              </li>
            {/each}
          </ol>
        </div>
      {/if}

      {#if checks.length}
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
          <ul class="flex flex-col gap-1.5">
            {#each checks as c (c.name)}
              {@const kind = checkKind(c.state)}
              <li
                class="flex items-start gap-2.5 rounded-md border px-2.5 py-2 {kind === 'fail'
                  ? 'border-bad/35 bg-bad/5'
                  : 'border-edge/50 bg-panel/40'}"
              >
                <span class="mt-[1px]"><BoardMarker {kind} {live} /></span>
                <div class="min-w-0 flex-1">
                  <div class="flex items-baseline gap-2">
                    <span class="truncate font-medium text-ink">{c.name}</span>
                    <span
                      class="ml-auto shrink-0 text-sm {kind === 'pass'
                        ? 'text-good'
                        : kind === 'fail'
                          ? 'text-bad'
                          : 'text-info'}">{CHECK_WORD[kind]}</span
                    >
                    {#if c.evidence && EVIDENCE_TAG[c.evidence]}
                      <span
                        class="shrink-0 text-sm {EVIDENCE_TAG[c.evidence].cls}"
                        title={c.evidenceNote ?? ""}
                        >· {EVIDENCE_TAG[c.evidence].word}</span
                      >
                    {/if}
                  </div>
                  {#if c.summary}
                    <div class="selectable num mt-0.5 text-sm break-words text-faint">{c.summary}</div>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
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
