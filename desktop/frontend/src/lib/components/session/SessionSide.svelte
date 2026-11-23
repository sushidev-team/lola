<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { hypothesisView, severityView, stepView } from "$lib/ssh";
  import { inScope } from "$lib/turns";
  import { deriveSide } from "$lib/sessionside";
  import { reviewWorkspace } from "$lib/reviewworkspace.svelte";
  import { feed } from "$lib/feed.svelte";
  import { changes } from "$lib/changes.svelte";
  import { changesOffered } from "$lib/sessionview";
  import { sessionActivity } from "$lib/sessionactivity";
  import Button from "../Button.svelte";
  import BeforeMerge from "./BeforeMerge.svelte";
  import OverviewChecks from "./OverviewChecks.svelte";
  import SessionTasks from "./SessionTasks.svelte";
  import SessionBrief from "./SessionBrief.svelte";
  import BoardPanel from "../BoardPanel.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const board = $derived(session?.board);
  const side = $derived(deriveSide(inScope(view.items, view.scope), board, sshClock.now));
  const f = $derived(feed.view(sessionId));
  const activity = $derived(sessionActivity({ feed: f.items, items: view.items, scope: view.scope, alive: ['working', 'starting', 'waiting_input'].includes(session?.agentState ?? '') }));
  const tests = $derived(activity.filter(e => e.kind === 'tests'));
  const diff = $derived(changes.of(sessionId));
  const hasProgressReport = $derived(board?.blocked || board?.hasProgress || board?.todos?.length || board?.note);
  const hasPR = $derived(session?.stack ? session.apps?.some(app => app.number) : !!session?.prNumber);
  const diffable = $derived(!!session && changesOffered(session));
  const hasChecks = $derived(tests.length || board?.checks?.length || board?.mismatches?.length);
  const failedCheck = $derived(board?.mismatches?.length || board?.checks?.some(c => c.state === 'fail') || tests.at(-1)?.status === 'error');
  const plan = $derived(side.plan?.element);
  const steps = $derived(plan?.steps ?? []);
  const done = $derived(steps.filter((s) => s.state === "done").length);
  const planComplete = $derived(steps.length > 0 && done === steps.length);
  let planExpanded = $state(false);
  $effect(() => { if (!planComplete) planExpanded = false; });
  function jumpTo(id: string) {
    view.filter = "everything";
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
    <h3 class="cv-overview-heading">{text}</h3>
    {#if aside}<span class="num ml-auto text-sm text-faint">{aside}</span>{/if}
  </div>
{/snippet}

{#snippet tasks()}<SessionTasks {sessionId} />{/snippet}
{#snippet checks()}{#if hasChecks}<OverviewChecks {session} {tests} onJump={jumpTo} />{/if}{/snippet}
{#snippet progress()}
  {#if session && hasProgressReport}<BoardPanel {session} embedded showChecks={false} compactWhenDone />{/if}
  {#if plan}
    <section data-side-plan>
      {#if planComplete}
        <button type="button" class="flex w-full items-center gap-2 text-left" aria-expanded={planExpanded} onclick={() => planExpanded = !planExpanded}><Icon name="circle-check" size={16} class="text-good" /><span class="font-medium">Plan complete</span><span class="num text-xs text-faint">{done}/{steps.length}</span><Icon name={planExpanded ? "chevron-down" : "chevron-right"} size={14} class="ml-auto text-faint" /></button>
      {/if}
      {#if !planComplete || planExpanded}
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
      {/if}
    </section>
  {/if}
{/snippet}

{#snippet review()}
  {#if session && (diffable || hasPR)}
    <section aria-label="Session review" data-side-review>
      <div class="flex items-center justify-between gap-2">
        <h3 class="cv-overview-heading">Review</h3>
        <Button size="xs" variant="secondary" onclick={() => reviewWorkspace.open(sessionId)}>Open review <Icon name="arrow-up-right" size={13} /></Button>
      </div>
      {#if diffable}
        <button type="button" class="changes-link" aria-label="View changes in Review" onclick={() => session && ssh.openChanges(session)}>
          <span>Changes</span>
          <span class="changes-count">{diff ? `${diff.files} ${diff.files === 1 ? 'file' : 'files'}` : 'Not loaded'}</span>
          {#if diff?.files}<span class="num text-good">+{diff.add}</span><span class="num text-bad">−{diff.del}</span>{/if}
          <Icon name="chevron-right" size={14} />
        </button>
      {/if}
      {#if hasPR}<div class="pr-details"><BeforeMerge {session} /></div>{/if}
    </section>
  {/if}
{/snippet}

<aside class="cv-session-side grid min-h-0 content-start overflow-y-auto text-sm" aria-label="Session overview" data-session-side>
  <SessionBrief {sessionId} />
  <!-- A keyed move preserves task drafts, disclosure state and PR reads as priorities change. -->
  {#each [...new Set([
    ...(board?.blocked ? [progress] : []),
    ...(session?.tasks?.some(t => t.state === 'checking') ? [tasks] : []),
    ...(failedCheck ? [checks] : []),
    ...(hasPR || (diff?.files && session?.agentState === 'idle') ? [review] : []),
    ...(session?.agentState === 'working' && (plan || hasProgressReport) ? [progress] : []),
    review, tasks, progress, checks,
  ])] as section (section)}
    {@render section()}
  {/each}

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

<style>
  .changes-link {
    display: flex;
    width: 100%;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-top: 8px;
    padding: 5px 0;
    text-align: left;
    color: var(--color-accent-ink);
  }
  .changes-link:hover > span:first-child { text-decoration: underline; }
  .changes-link:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; }
  .changes-count { margin-left: auto; color: var(--color-faint); font-variant-numeric: tabular-nums; }
  .pr-details { margin-top: 10px; padding-top: 10px; border-top: 1px solid var(--color-edge); }
</style>
