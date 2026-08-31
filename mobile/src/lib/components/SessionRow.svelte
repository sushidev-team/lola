<script lang="ts">
  import LivePulse from "$lib/components/LivePulse.svelte";
  import PrBadge from "$lib/components/PrBadge.svelte";
  import { reactionIsAlarm, reactionNote } from "$lib/reaction";
  import { kanbanColumn, statusLabel } from "$lib/theme";
  import { statusTone } from "@mobile/lib/statustone";
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
  // So the LAYOUT is new and everything else is borrowed: PrBadge and LivePulse
  // are the shared components rendered verbatim, and the colours, the labels and
  // the attention rule all come from `$lib/theme`, which is the port of Go's
  // internal/state vocabulary that desktop/state_parity_test.go pins.
  // Re-deriving any of that here would create a third mirror of a list the
  // repository already keeps in exactly two.
  //
  // WHY NOT `<AgentActivity>`, which this row used to render. That shared
  // component is `{#if live || text}` — pulse first, prose after — so a working
  // agent with no headline and no notification produced a line containing
  // nothing but a six-point dot, hanging on its own between the title and the
  // facts. It read as a rendering artefact rather than as liveness, and it is
  // the commonest state in the list: most sessions are mid-turn with nothing to
  // say about it. The two halves are therefore split across the row's own
  // structure — the PULSE leads the facts line, beside the status word it
  // qualifies, and the PROSE keeps a line of its own only when there is prose.
  // Nothing changed on the desktop: AgentActivity is untouched and still
  // rendered by the surfaces built around it.
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
  // which the status word already carries in its own loud colour. `needs_input`
  // is the reason this app exists, so it alone earns a rail on top of that;
  // giving one to the family would make four rows in five look urgent.
  const wanted = $derived(s.status === "needs_input");

  // The Done column's statuses are TERMINAL — nothing is waiting on them — so
  // their word is dimmed rather than dropped. This is the second half of a
  // weight cut that started with the pill: `dead` in particular used to arrive
  // from pillClasses as a saturated `bg-bad` fill, which at 393pt with two rows
  // on screen made the row that needed nothing the loudest thing in the list.
  // The colour is still theme.ts's — statusTone only steps the UNNAMED family
  // down off the heading's ink, see statustone.ts — and only its WEIGHT changes
  // here.
  const settled = $derived(kanbanColumn(s.status) === "Done");

  // WHAT LEADS THE ROW. The issue key used to, at full weight, and it was the
  // wrong emphasis: a list of NOR-401 / NOR-329 / NOR-311 is a list of three
  // things you cannot tell apart. The title says what the session is; the key is
  // how you cite it afterwards. So the title leads and the key drops into the
  // meta line — unless there is no title, in which case the key is all the row
  // has and it leads instead (and is not then repeated below).
  const heading = $derived(s.title || s.issue || s.id.slice(0, 12));
  const metaIssue = $derived(s.title ? s.issue : "");

  // The interpreter's headline, or — failing that — the agent's own last
  // notification. Same source and same precedence as AgentActivity's, which is
  // the component this row no longer mounts; the daemon clears
  // lastNotification on any transition off waiting_input, so it is never a
  // stale sentence about a turn that ended. UNTRUSTED and display-only.
  const activity = $derived(s.headline || s.lastNotification || "");
</script>

<button
  type="button"
  class="flex w-full touch-manipulation flex-col gap-1 border-b border-edge/40 px-4 py-3
         text-left transition-colors active:bg-sel
         {wanted ? 'border-l-2 border-l-orange pl-[14px]' : ''}"
  onclick={onopen}
>
  <!-- Two lines rather than one truncated one: at 390 points a single line of
       an issue title is about five words, which is rarely enough to tell two
       tickets apart. -->
  <div class="line-clamp-2 w-full font-medium text-ink">{heading}</div>

  <!-- THE STATUS IS PART OF THE ROW, not a chip parked on top of it. It used to
       be a <StatusPill> pushed to the far right of the first line, which put the
       one word that says what is happening as far from the sentence it modifies
       as the row is wide, and drew a filled badge around it to compensate. Here
       it simply leads the facts line in its own colour, which is the same colour
       the pill was filled with — statusLabel is the exact function StatusPill
       calls and statusTone defers to theme.ts's statusText for every status it
       names, so the vocabulary the three surfaces share is untouched.

       THE LINE WRAPS, AND WHAT MAY TRUNCATE IS CHOSEN RATHER THAN INHERITED.
       At an accessibility text size this line is far wider than the screen, and
       it used to be the ISSUE KEY that gave way — it and the project were the
       only `min-w-0 truncate` items while the age and the PR badge were
       `shrink-0` — so the row read `needs you · NO… · No… · 3h27m #341`. That
       is precisely backwards: the key is the citation handle item 2 kept
       deliberately, and the age is the least useful fact on the line. So the
       key and the age are both `shrink-0` (they are short and fixed-width), the
       PROJECT is the single item allowed to truncate, and `flex-wrap` lets the
       rest fall to a second line rather than crushing anything. At the default
       size nothing wraps and the row is unchanged. -->
  <div class="flex w-full flex-wrap items-center gap-x-2 gap-y-0.5 text-sm text-faint">
    <!-- The agent axis, as motion rather than a word. It leads the line it
         qualifies: post-PR the status word is the DELIVERY word and says
         nothing about whether the runner is alive, so this dot is the only
         thing on the row that does. LivePulse renders nothing at all unless the
         agent is working or starting, so a settled row costs no space — which
         is the bug that came of giving it a line of its own. -->
    <LivePulse agentState={s.agentState} />
    <span class="shrink-0 {statusTone(s.status)} {settled ? 'opacity-55' : ''}">
      {statusLabel(s.status)}
    </span>
    {#if s.interpretedState}
      <!-- The [statusagent] interpreter DISAGREES with the deterministic axis:
           an untrusted approximation, "≈"-marked (statusPillFor in the TUI). -->
      <span class="shrink-0 text-orange">≈{statusLabel(s.interpretedState)}</span>
    {/if}
    {#if metaIssue}
      <span aria-hidden="true">·</span>
      <!-- `shrink-0`: an issue key is seven characters and is the row's
           citation handle. "NO…" is not a citation. -->
      <span class="num shrink-0">{metaIssue}</span>
    {/if}
    <span aria-hidden="true">·</span>
    <!-- The ONE item that may truncate: a project label is free text and is the
         only thing here that can be long. -->
    <span class="min-w-0 truncate">{projectLabel}</span>
    {#if s.devActive}
      <span class="shrink-0 text-good" aria-label="running this project's dev commands">●</span>
    {/if}
    <span aria-hidden="true">·</span>
    <!-- `num` keeps the age from reflowing its neighbours on every push. -->
    <span class="num shrink-0">{s.age}</span>
    <!-- Only when there IS a PR. PrBadge renders a bare em-dash otherwise,
         which is unambiguous in a desktop table under a column header and is
         just a mark in a phone row that has none. An absent PR is clearer as
         absence. -->
    {#if s.prNumber > 0}
      <span class="ml-auto shrink-0 pl-1">
        <PrBadge session={s} status={s.status} />
      </span>
    {/if}
  </div>

  {#if activity}
    <!-- The interpreter's headline reads as a live-progress note, not a
         warning, so it carries the pulse dot's `info` blue and the "≈" that
         marks it as an approximation. The agent's own notification is plain
         faint prose. Both are AgentActivity's rules, applied to the half of it
         this row still draws. -->
    <div class="w-full truncate text-sm {s.headline ? 'text-info' : 'text-faint'}">
      {s.headline ? `≈ ${activity}` : activity}
    </div>
  {/if}

  {#if note}
    <div class="num text-sm {alarm ? 'text-bad' : 'text-faint'}">
      {alarm ? "escalated — lola has spent its CI retries" : note}
    </div>
  {/if}
</button>
