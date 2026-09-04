<script lang="ts">
  import LivePulse from "$lib/components/LivePulse.svelte";
  import PrBadge from "$lib/components/PrBadge.svelte";
  import { reactionIsAlarm, reactionNote } from "$lib/reaction";
  import { kanbanColumn, statusLabel } from "$lib/theme";
  import { statusTone } from "@mobile/lib/statustone";
  import type { SessionInfo } from "$lib/store.svelte";

  // One session, as the list's COMPACT row — the shape used for a session
  // nobody is blocked on. Its geometry is the "Compact row" block of the
  // redesign brief (Figma 41:31 / 41:71): px-5 py-[11px], a 13px medium title
  // over an 11px facts line, separated by an `edge-soft` hairline.
  //
  // THIS ROW IS NOW HALF OF A PAIR. A session that needs a human renders as a
  // <SessionCard> instead — bordered, shadowed, carrying a status chip and a
  // coloured rail — so everything that used to make THIS component shout has
  // moved there. That is why the orange left rail is gone: it was this row's
  // one emphasis, drawn for `needs_input` alone, and a row that is now only
  // ever used for the quiet population has nothing to emphasise. Keeping it
  // would mean two different treatments of the same state on one screen.
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
  // WHY LivePulse AND NOT `@mobile/lib/components/StatusDot.svelte`. StatusDot
  // exists because a filter chip's dot and a card's chip dot vary in colour and
  // size and never pulse, none of which LivePulse can express. Here the
  // hardcoding IS the requirement: the design's compact-row dot is 6 points,
  // appears "only while the agent is live", and lives on the axis LivePulse
  // owns. Passing StatusDot a `pulse` flag would mean re-deriving
  // `agentState === "working" || "starting"` at this call site — a second copy
  // of the one rule that decides whether a runner is alive.
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

  // The Done column's statuses are TERMINAL — nothing is waiting on them — so
  // the row is dimmed rather than dropped. The design does this in two places
  // at once: the title steps from `ink` down to `faint`, and the status word
  // keeps its own colour at reduced weight. The word's half is the older
  // decision and the reason for it still holds: `dead` in particular used to
  // arrive from pillClasses as a saturated `bg-bad` fill, which at 393pt with
  // two rows on screen made the row that needed nothing the loudest thing in
  // the list. The colour is still theme.ts's — statusTone only steps the
  // UNNAMED family down off the heading's ink, see statustone.ts — and only its
  // WEIGHT changes here.
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

<!-- `border-edge-soft`, not `border-edge/40`. Every list hairline in the
     redesign is the soft token; `edge` survives only where a PANEL is drawn,
     which on this screen means the hero card's border. The old value was an
     opacity of the panel border approximating the same thing by hand, and it
     drifted per flavor because the alpha composited against whatever the row
     sat on. -->
<button
  type="button"
  class="flex w-full touch-manipulation flex-col gap-[3px] border-b border-edge-soft px-5 py-[11px]
         text-left transition-colors active:bg-sel"
  onclick={onopen}
>
  <!-- Two lines rather than one truncated one: at 390 points a single line of
       an issue title is about five words, which is rarely enough to tell two
       tickets apart. The design draws one line because its sample titles are
       short; it says nothing about the overflow case, and clamping is the
       existing answer to it.

       `text-base font-medium` is the design's row title (13/17 at 500). It is
       also the ladder's DEFAULT size, spelled out rather than inherited so the
       row states its own tier — the brief's rule is to reach for the class, and
       a title that silently tracked a future change to `body` would be wrong. -->
  <div class="line-clamp-2 w-full text-base font-medium {settled ? 'text-faint' : 'text-ink'}">
    {heading}
  </div>

  <!-- THE STATUS IS PART OF THE ROW, not a chip parked on top of it. It used to
       be a <StatusPill> pushed to the far right of the first line, which put the
       one word that says what is happening as far from the sentence it modifies
       as the row is wide, and drew a filled badge around it to compensate. Here
       it simply leads the facts line in its own colour, which is the same colour
       the pill was filled with — statusLabel is the exact function StatusPill
       calls and statusTone defers to theme.ts's statusText for every status it
       names, so the vocabulary the three surfaces share is untouched. The
       redesign keeps that: <StatusChip> is drawn on the hero card and in the
       detail header, where a chip leads its own row, and never in here.

       THE LINE WRAPS, AND WHAT MAY TRUNCATE IS CHOSEN RATHER THAN INHERITED.
       At an accessibility text size this line is far wider than the screen, and
       it used to be the ISSUE KEY that gave way — it and the project were the
       only `min-w-0 truncate` items while the age and the PR badge were
       `shrink-0` — so the row read `needs you · NO… · No… · 3h27m #341`. That
       is precisely backwards: the key is the citation handle the title
       deliberately made room for, and the age is the least useful fact on the
       line. So the key and the age are both `shrink-0` (they are short and
       fixed-width), the PROJECT is the single item allowed to truncate, and
       `flex-wrap` lets the rest fall to a second line rather than crushing
       anything. At the default size nothing wraps and the row is the design's.

       The design's gap is 6px, which is `gap-x-1.5`; the vertical gap is this
       file's own, because the frame has no wrapped state to transcribe and 6px
       between two halves of one sentence reads as two lines rather than one
       that ran out of room. -->
  <div class="flex w-full flex-wrap items-center gap-x-1.5 gap-y-0.5 text-sm text-faint">
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
    <!-- The design puts a `flex-1` spacer here and the age hard against the
         right edge, which is why there is no "·" before it: the gap IS the
         separator. `ml-auto` is that spacer without a DOM node for it, and it
         behaves better in the case the design has no drawing for — when the
         line wraps, an auto margin keeps the age right-aligned on whichever
         line it lands on, where a spacer element would leave it stranded at the
         left of the second one.

         `num` keeps the age from reflowing its neighbours on every push. -->
    <span class="num ml-auto shrink-0">{s.age}</span>
    <!-- Only when there IS a PR. PrBadge renders a bare em-dash otherwise,
         which is unambiguous in a desktop table under a column header and is
         just a mark in a phone row that has none. An absent PR is clearer as
         absence.

         Still PrBadge and not the redesign's <MetaPill>. The design draws no PR
         badge in a compact row at all, so there is nothing to transcribe; and
         the two components do different jobs — MetaPill draws a filled chip and
         leaves the wording to its caller, while PrBadge DECIDES what to say
         (the number, the checks and review glyphs, and which of those the
         status word on this same line already implies). Swapping it would drop
         facts, not restyle them. -->
    {#if s.prNumber > 0}
      <span class="shrink-0 pl-1">
        <PrBadge session={s} status={s.status} />
      </span>
    {/if}
  </div>

  {#if activity}
    <!-- `text-body` is the brief's prose step (12/16) — an activity sentence is
         read, not scanned, and it is the one thing in this row that is a
         sentence.

         It stays `faint` rather than taking the new `subtext` token, which the
         design spends on the HERO CARD's activity line. There the sentence is a
         primary fact with a glyph of its own; here it is a trailing note under
         a title that already said what the session is, and lifting it a step
         would put it above the facts line it hangs from.

         The interpreter's headline reads as a live-progress note, not a
         warning, so it carries the pulse dot's `info` blue and the "≈" that
         marks it as an approximation. The agent's own notification is plain
         faint prose. Both are AgentActivity's rules, applied to the half of it
         this row still draws. -->
    <div class="w-full truncate text-body {s.headline ? 'text-info' : 'text-faint'}">
      {s.headline ? `≈ ${activity}` : activity}
    </div>
  {/if}

  {#if note}
    <!-- `font-medium` on a `num` run at 11px is the brief's §1 rule and not
         decoration: tabular figures at regular weight beside a 13px medium
         title read as fine print, and this line is either a countdown of lola's
         remaining CI retries or the announcement that they are spent. -->
    <div class="num text-sm font-medium {alarm ? 'text-bad' : 'text-faint'}">
      {alarm ? "escalated — lola has spent its CI retries" : note}
    </div>
  {/if}
</button>
