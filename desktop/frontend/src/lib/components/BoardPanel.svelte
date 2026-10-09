<script lang="ts">
  import { scale } from "svelte/transition";
  import { backOut } from "svelte/easing";
  import type { SessionInfo } from "$lib/store.svelte";
  import { boardStale, phaseChip, progressText, CHECK_GLYPH, CHECK_TEXT } from "$lib/board";

  // The session view's sidebar: the agent's whole self-report — phase, progress,
  // blocker, plan, checks, note — beside its live terminal. SessionEmbed renders
  // it only when the agent has reported something, so a session that never does
  // keeps the full-width terminal.
  //
  // Everything here is the agent's own CLAIM ("Agent report" says so in the
  // header), and it fades once stale. The facts — the status pill and the PR
  // badge — stay on the row this session was selected from.
  let { session }: { session: SessionInfo } = $props();

  const b = $derived(session.board);
  const stale = $derived(boardStale(b));
  const todos = $derived(b?.todos ?? []);
  const checks = $derived(b?.checks ?? []);

  // Motion means "this is happening now", so it runs only while BOTH are true:
  // the agent axis says the runner is mid-turn (a FACT, from hooks and the pane)
  // and the report is fresh. An idle agent's plan, or a stale one, sits still —
  // a breathing marker on work nobody is doing would be the board lying.
  // prefers-reduced-motion is honoured globally in app.css.
  const live = $derived(!stale && (session.agentState === "working" || session.agentState === "starting"));
</script>

{#if b}
  <aside
    class="flex h-full min-h-0 w-72 flex-col overflow-y-auto border-l border-edge/60 bg-canvas"
    aria-label="Agent report"
  >
    <div class="flex items-center gap-2 border-b border-edge/60 px-3 py-2">
      <span class="label text-faint">Agent report</span>
      {#if b.phase}
        <span class="rounded px-1.5 py-[1px] text-sm {phaseChip(b.phase)}">{b.phase}</span>
      {/if}
      {#if b.updatedAgo}
        <span
          class="ml-auto text-sm text-faint"
          class:text-warn={stale}
          title={stale ? "this report is old — the agent may have moved on" : ""}>{b.updatedAgo} ago</span
        >
      {/if}
    </div>

    <div class="flex flex-col gap-4 px-3 py-3 transition-opacity duration-500" class:opacity-60={stale}>
      {#if b.blocked}
        <!-- Orange is the app's "you are needed" colour (the needs-you pill). It
             is still the agent's claim, so it is worded as one. -->
        <div class="rounded-md border border-orange/50 bg-orange/10 px-2.5 py-2">
          <div class="label mb-0.5 text-orange">Blocked</div>
          <div class="selectable text-ink">{b.blocked}</div>
        </div>
      {/if}

      {#if b.hasProgress}
        <div>
          <!-- "Progress", never "Plan": the plan list below already carries that
               heading, and two "Plan"s stacked read as a rendering glitch. -->
          <div class="mb-1.5 flex items-baseline gap-2 text-sm">
            <span class="truncate text-faint">{b.progressLabel || "Progress"}</span>
            <span class="num ml-auto text-ink">{progressText(b)}</span>
          </div>
          <div
            class="h-1.5 overflow-hidden rounded-full bg-edge/70"
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
          <div class="label mb-2 text-faint">Plan</div>
          <!-- A timeline: one 16px marker per item on a hairline rail. The marker
               is DRAWN at a fixed size, not typed — the old ✓ ▸ · glyphs came from
               three different fonts at three different sizes, which is why the
               pending dots read as specks next to the check. -->
          <ol class="flex flex-col">
            {#each todos as t, i (i)}
              {@const last = i === todos.length - 1}
              <li class="relative flex items-start gap-2.5 pb-2.5 last:pb-0">
                {#if !last}
                  <span
                    class="absolute top-[18px] bottom-0 left-[7.5px] w-px transition-colors duration-500 {t.state ===
                    'done'
                      ? 'bg-good/50'
                      : 'bg-edge/60'}"
                    aria-hidden="true"
                  ></span>
                {/if}
                <span class="relative mt-[1px] flex h-4 w-4 shrink-0 items-center justify-center" aria-hidden="true">
                  {#if t.state === "done"}
                    <span
                      class="flex h-4 w-4 items-center justify-center rounded-full bg-good/20 text-good"
                      in:scale={{ start: 0.4, duration: 260, easing: backOut }}
                    >
                      <svg
                        viewBox="0 0 16 16"
                        class="h-2.5 w-2.5"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2.4"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                      >
                        <path d="M3.5 8.5l3 3 6-7" />
                      </svg>
                    </span>
                  {:else if t.state === "active"}
                    {#if live}<span class="ripple absolute inset-0 rounded-full border border-accent"></span>{/if}
                    <span class="flex h-4 w-4 items-center justify-center rounded-full border-[1.5px] border-accent">
                      <span class="h-1.5 w-1.5 rounded-full bg-accent" class:breathe={live}></span>
                    </span>
                  {:else}
                    <span class="h-3 w-3 rounded-full border-[1.5px] border-edge"></span>
                  {/if}
                </span>
                <span
                  class="selectable min-w-0 transition-colors duration-500 {t.state === 'done'
                    ? 'text-faint line-through decoration-edge'
                    : t.state === 'active'
                      ? 'font-medium text-ink'
                      : 'text-ink/80'}">{t.text}</span
                >
                <span class="sr-only">({t.state})</span>
              </li>
            {/each}
          </ol>
        </div>
      {/if}

      {#if checks.length}
        <div>
          <div class="label mb-1.5 text-faint" title="checks the agent says it ran locally — not CI">Local checks</div>
          <ul class="flex flex-col gap-1">
            {#each checks as c (c.name)}
              <li class="flex items-baseline gap-2">
                <span
                  class="w-4 shrink-0 text-center {CHECK_TEXT[c.state] ?? 'text-faint'}"
                  class:breathe-text={c.state === "running" && live}
                  aria-hidden="true">{CHECK_GLYPH[c.state] ?? "·"}</span
                >
                <span class="text-ink">{c.name}</span>
                {#if c.summary}<span class="selectable num ml-auto truncate text-sm text-faint">{c.summary}</span>{/if}
                <span class="sr-only">({c.state})</span>
              </li>
            {/each}
          </ul>
        </div>
      {/if}

      {#if b.note}
        <div>
          <div class="label mb-0.5 text-faint">Note</div>
          <div class="selectable text-sm text-ink">{b.note}</div>
        </div>
      {/if}
    </div>
  </aside>
{/if}

<style>
  /* All motion here is slow and low-amplitude on purpose: it sits beside a live
     terminal that is already moving, and must read as a heartbeat, not as
     something asking to be clicked. Under prefers-reduced-motion app.css stops it
     and every marker keeps its static shape, so no meaning rides on motion. */

  /* The active item's core breathes... */
  .breathe {
    animation: breathe 2.4s ease-in-out infinite;
  }
  @keyframes breathe {
    0%,
    100% {
      transform: scale(0.7);
      opacity: 0.65;
    }
    50% {
      transform: scale(1);
      opacity: 1;
    }
  }

  /* ...and a ring leaves it on the same beat, fading as it grows. */
  .ripple {
    animation: ripple 2.4s ease-out infinite;
  }
  @keyframes ripple {
    0% {
      transform: scale(1);
      opacity: 0.45;
    }
    70%,
    100% {
      transform: scale(1.9);
      opacity: 0;
    }
  }

  /* A "running" check glyph. */
  .breathe-text {
    animation: fade 1.8s ease-in-out infinite;
  }
  @keyframes fade {
    0%,
    100% {
      opacity: 0.4;
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
    background: linear-gradient(
      90deg,
      transparent 0%,
      color-mix(in srgb, white 35%, transparent) 50%,
      transparent 100%
    );
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
