<script lang="ts">
  import StatusPill from "$lib/components/StatusPill.svelte";
  import PrBadge from "$lib/components/PrBadge.svelte";
  import AgentActivity from "$lib/components/AgentActivity.svelte";
  import { reactionIsAlarm, reactionNote } from "$lib/reaction";
  import type { SessionInfo } from "$lib/store.svelte";

  // One session, as a phone row.
  //
  // WHY NOT `$lib/components/SessionsTable.svelte`. It compiles here unchanged
  // behind the service shim — that was checked, and it is a real property of the
  // reuse bet — but it is a seven-column <table> whose narrowest sensible layout
  // is about 700 points. At 390 the columns collapse into each other, the title
  // truncates to two words, and the status pill and the PR badge end up on
  // separate visual lines with no relationship between them. A phone list is not
  // a narrow table; it is a different arrangement of the same facts.
  //
  // So the LAYOUT is new and everything else is borrowed: StatusPill, PrBadge
  // and AgentActivity are the shared components rendered verbatim, and the
  // colours, the labels and the attention rule all come from `$lib/theme`, which
  // is the port of Go's internal/state vocabulary that desktop/state_parity_test.go
  // pins. Re-deriving any of that here would create a third mirror of a list the
  // repository already keeps in exactly two.
  //
  // Hand-rolled as a <button> rather than a <TouchButton>: CLAUDE.md's Button
  // invariant names card-shaped rows as one of the five things that stay
  // hand-rolled on purpose, alongside the kanban cards and the sidebar's nav
  // rows. This is one of those.

  let {
    session,
    projectLabel,
    onopen,
  }: {
    session: SessionInfo;
    projectLabel: string;
    onopen: () => void;
  } = $props();

  const s = $derived(session);
  const note = $derived(reactionNote(s.reacting));
  const alarm = $derived(reactionIsAlarm(note));
  // The one row-level emphasis, and it is deliberately NARROWER than
  // `$lib/theme`'s `isAttention`. That predicate covers the whole attention
  // family (needs_input, ci_failed, changes_requested, merge_conflict), each of
  // which the StatusPill already draws in its own loud colour. `needs_input` is
  // the reason this app exists, so it alone earns a rail on top of that; giving
  // one to the family would make four rows in five look urgent.
  const wanted = $derived(s.status === "needs_input");
</script>

<button
  type="button"
  class="flex w-full touch-manipulation flex-col gap-1 border-b border-edge/40 px-4 py-3
         text-left transition-colors active:bg-sel
         {wanted ? 'border-l-2 border-l-orange pl-[14px]' : ''}"
  onclick={onopen}
>
  <div class="flex w-full items-center gap-2">
    <!-- The row's ONE 500: the issue key is what the row is about. -->
    <span class="min-w-0 truncate font-medium text-ink">
      {s.issue || s.id.slice(0, 12)}
    </span>
    <span class="ml-auto shrink-0">
      <!-- No `onResolve`: this is a read-only surface in M1, and the pill's
           hover-morph into a "resolve" action has no hover on a phone anyway.
           The conflict action is M4's, behind an explicit control. -->
      <StatusPill status={s.status} interpreted={s.interpretedState} />
    </span>
  </div>

  {#if s.title}
    <!-- Two lines rather than one truncated one: at 390 points a single line of
         an issue title is about five words, which is rarely enough to tell two
         tickets apart. -->
    <div class="line-clamp-2 w-full text-ink">{s.title}</div>
  {/if}

  <!-- The agent axis and the interpreter's headline, rendered by the shared
       component: untrusted, display-only, already marked with its own "approx"
       sign. Nothing here feeds any control loop. -->
  <AgentActivity session={s} />

  <div class="flex w-full items-center gap-2 text-sm text-faint">
    <span class="min-w-0 truncate">{projectLabel}</span>
    {#if s.devActive}
      <span class="shrink-0 text-good" aria-label="running this project's dev commands">●</span>
    {/if}
    <span aria-hidden="true">·</span>
    <!-- `num` keeps the age from reflowing its neighbours on every push. -->
    <span class="num shrink-0">{s.age}</span>
    <span class="ml-auto shrink-0">
      <PrBadge session={s} status={s.status} />
    </span>
  </div>

  {#if note}
    <div class="num text-sm {alarm ? 'text-bad' : 'text-faint'}">
      {alarm ? "escalated — lola has spent its CI retries" : note}
    </div>
  {/if}
</button>
