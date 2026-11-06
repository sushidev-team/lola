<script lang="ts">
  // The agent's standing account beside its story: plan, progress, checks, findings, notes and its
  // widgets. It exists only once the agent has used one of them, so a session that never did keeps
  // a plain conversation. Everything here is a claim, which is why the heading says whose it is.
  // The audit's doubt, inside BoardPanel, is lola's own words.
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { hypothesisView, severityView, stepView } from "$lib/ssh";
  import { inScope } from "$lib/turns";
  import { boardHasContent, deriveSide } from "$lib/sessionside";
  import Button from "../Button.svelte";
  import BoardPanel from "../BoardPanel.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const board = $derived(session?.board);
  const side = $derived(deriveSide(inScope(view.items, view.scope), board, sshClock.now));
  const plan = $derived(side.plan?.element);
  const steps = $derived(plan?.steps ?? []);
  const done = $derived(steps.filter((s) => s.state === "done").length);
  function jumpTo(id: string) {
    view.cursor = id;
    view.jump = { block: `item:${id}`, n: (view.jump?.n ?? 0) + 1 };
  }
  function openWidget(id: string) {
    view.widgetsAllowed = true;
    view.openWidget(id);
  }
</script>

{#snippet heading(text: string, aside: string = "")}
  <div class="mb-2 flex items-baseline gap-2">
    <span class="label text-faint">{text}</span>
    {#if aside}<span class="num ml-auto text-sm text-faint">{aside}</span>{/if}
  </div>
{/snippet}

<aside class="cv-session-side grid min-h-0 content-start gap-5 overflow-y-auto px-4 pt-2 pb-4 text-sm" aria-label="Session overview" data-session-side>


  {#if side.brief.summary}
    <p class="selectable copy text-prose text-ink {side.brief.summary.stale ? 'opacity-60' : ''}" data-side-summary>{side.brief.summary.title}</p>
  {/if}

  {#if session && boardHasContent(board)}<BoardPanel {session} embedded />{/if}

  {#if !boardHasContent(board) && !plan}
    <section data-side-progress-empty>
      {@render heading("Task progress")}
      <p class="text-faint">No plan reported yet.</p>
    </section>
  {/if}

  {#if plan}
    <section data-side-plan>
      {@render heading(plan.title || "Plan", steps.length ? `${done}/${steps.length}` : "")}
      {#if steps.length}
        <div class="mb-3 h-1.5 overflow-hidden rounded-full bg-edge/60" role="progressbar" aria-label="Reported plan" aria-valuemin="0" aria-valuemax={steps.length} aria-valuenow={done}>
          <div class="h-full rounded-full bg-accent transition-[width] duration-500" style="width:{(done / steps.length) * 100}%"></div>
        </div>
      {/if}
      <ol class="grid gap-1.5">
        {#each steps as s, i (i)}
          {@const v = stepView(s.state)}
          <li class="grid grid-cols-[12px_minmax(0,1fr)] items-baseline gap-2">
            <span class="inline-block size-2 translate-y-px rounded-full border {v.ring}" aria-hidden="true"></span>
            <span class="selectable min-w-0 break-words {v.text}">{s.text}{v.suffix}</span>
          </li>
        {/each}
      </ol>
    </section>
  {/if}

  {#if side.findings.length}
    <section data-side-findings>
      {@render heading("Findings", String(side.findings.length))}
      <ul class="grid gap-1">
        {#each side.findings as f (f.id)}
          {@const v = severityView(f.element.severity)}
          <li>
            <button type="button" class="-mx-1.5 grid w-[calc(100%+12px)] grid-cols-[52px_minmax(0,1fr)] items-baseline gap-2 rounded-sm px-1.5 py-0.5 text-left hover:bg-sel/30" onclick={() => jumpTo(f.id)}>
              <span class={v.text}>{v.word}</span>
              <span class="min-w-0 break-words text-ink-2">{f.element.title}</span>
            </button>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  {#if side.theories.length}
    <section data-side-theories>
      {@render heading("Theories")}
      <ul class="grid gap-1.5">
        {#each side.theories as t (t.id)}
          {@const v = hypothesisView(t.element.state)}
          <li>
            <button type="button" class="-mx-1.5 grid w-[calc(100%+12px)] gap-0.5 rounded-sm px-1.5 py-0.5 text-left hover:bg-sel/30" onclick={() => jumpTo(t.id)}>
              <span class="min-w-0 break-words {v.titleCls}">{t.element.title}</span>
              <span class={v.wordCls}>{v.word}</span>
            </button>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  {#if side.metrics}
    <section data-side-metrics>
      {@render heading(side.metrics.element.title || "Numbers")}
      <dl class="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-1">
        {#each side.metrics.element.metrics ?? [] as m (m.name)}
          <dt class="min-w-0 truncate text-faint">{m.name}</dt>
          <dd class="num text-ink">{m.value}{#if m.unit}<span class="text-faint"> {m.unit}</span>{/if}</dd>
        {/each}
      </dl>
    </section>
  {/if}

  {#if side.notes.length}
    <section data-side-notes>
      {@render heading("Notes")}
      <ul class="grid gap-2">
        {#each side.notes as n (n.id)}
          <li class="selectable border-l-2 border-edge pl-2.5 break-words text-ink-2">{n.element.title || n.element.body}</li>
        {/each}
      </ul>
    </section>
  {/if}

  {#if side.widgets.length}
    <section data-side-widgets>
      {@render heading("Widgets")}
      <ul class="grid gap-1">
        {#each side.widgets as w (w.id)}
          <li class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2">
            <span class="min-w-0 truncate text-ink-2">{w.element.title}</span>
            <Button size="xs" variant="secondary" title="Run this page in a sandbox, and show it" onclick={() => openWidget(w.element.id)}>Open</Button>
          </li>
        {/each}
      </ul>
    </section>
  {/if}
</aside>
