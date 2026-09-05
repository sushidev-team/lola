<script lang="ts">
  import StatusChip from "./StatusChip.svelte";
  import MetaPill from "./MetaPill.svelte";
  import AiGlyph from "@mobile/lib/icons/AiGlyph.svelte";
  import BranchIcon from "@mobile/lib/icons/BranchIcon.svelte";
  import { reactionIsAlarm, reactionNote } from "$lib/reaction";
  import { pillKind, statusLabel } from "$lib/theme";
  import type { SessionInfo } from "$lib/store.svelte";

  // The HERO card: one session that a human is being asked to look at.
  //
  // IT IS THE SAME FACTS AS <SessionRow>, ARRANGED WITH MORE ROOM. Nothing here
  // is a new fact and nothing here is a second vocabulary — the status word and
  // its colour still come from `$lib/theme` through <StatusChip>, the reaction
  // note still comes from `$lib/reaction`, and the heading still falls back
  // through title → issue → id exactly as the compact row's does. What changes
  // is the budget: the design gives the attention buckets a card and everything
  // else a 42-point row, so the card can afford two lines of title, two lines of
  // the agent's own prose, and a rule between the session and its citation
  // details. The row cannot, which is why it truncates all three.
  //
  // So the two components are deliberately NOT one component with a `dense`
  // prop. Every line of the row's layout comment is an argument about what to
  // give up at 390 points, and a card that gives nothing up shares none of those
  // decisions — only the derivations, which is what the shared modules are for.
  //
  // HAND-ROLLED AS A <button> RATHER THAN A <TouchButton>. CLAUDE.md's Button
  // invariant names card-shaped rows as one of the five things that stay
  // hand-rolled on purpose, alongside the kanban cards and the sidebar's nav
  // rows; the whole card is one tap target and the ladder has no variant that
  // is a bordered, shadowed, four-block panel. The consequence is stated again
  // at the PR badge below: nothing inside may be a <button> of its own.

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

  // THE RAIL IS FOR ONE STATUS, AND THE ASYMMETRY IS THE POINT. The Figma draws
  // it on the "waiting for you" card and leaves the CI-failed card without one,
  // which is the same narrowing <SessionRow> argues for: `$lib/theme`'s
  // `isAttention` covers the whole family (needs_input, ci_failed,
  // changes_requested, merge_conflict) and each of those already arrives in its
  // own loud chip. `needs_input` is the reason this app exists — it is the one
  // state where nothing at all happens until a person acts — so it alone earns a
  // mark on top of the chip. Giving one to the family would put a rail on four
  // cards in five and the rail would stop meaning anything.
  //
  // ASKED OF `pillKind` RATHER THAN COMPARED TO A STRING. The `urgent` kind is
  // exactly `needs_input` and nothing else, so the meaning is unchanged — but
  // the status vocabulary belongs to `$lib/theme`, which is the port of Go's
  // internal/state that desktop/state_parity_test.go pins across the three
  // surfaces. A literal here would have been the only place in this app outside
  // statustone.ts holding an opinion about a status name, and the one that would
  // silently stop matching if the daemon ever renamed the state.
  const wanted = $derived(pillKind(s.status) === "urgent");

  // Only the two notes that say something the status does not: how much CI
  // retry budget is left, or that it is spent. See `$lib/reaction` — the other
  // four outcomes are a relabelling of the status word the chip beside this one
  // already prints.
  const note = $derived(reactionNote(s.reacting));
  const alarm = $derived(reactionIsAlarm(note));

  // WHAT LEADS THE CARD, and it is the row's rule unchanged: the title says
  // what the session IS, the issue key is how you cite it afterwards, and a list
  // of NOR-401 / NOR-329 / NOR-311 is a list of three things you cannot tell
  // apart. So the title leads, the key drops to the meta line — and when there
  // is no title the key leads instead and is NOT repeated below, or the card
  // would print it twice with a rule between the two copies.
  const heading = $derived(s.title || s.issue || s.id.slice(0, 12));
  const metaIssue = $derived(s.title ? s.issue : "");

  // The interpreter's headline, or — failing that — the agent's own last
  // notification. Same source and same precedence as <AgentActivity>'s and the
  // compact row's; the daemon clears lastNotification on any transition off
  // waiting_input, so it is never a stale sentence about a turn that ended.
  //
  // BOTH ARE UNTRUSTED. They are derived from pane text, which an issue
  // description or a dependency's README can write into. They are rendered as
  // TEXT here and nowhere else — never as HTML, never as something a link is
  // built from (rule 6 of the brief).
  const activity = $derived(s.headline || s.lastNotification || "");
</script>

<!-- The outer element is the tap target and carries only the row's own gutter;
     the card's border, ground and shadow live on the div inside it, so the
     pressed state has to cross that boundary — hence `group` here and
     `group-active:` there. Putting `active:bg-sel` on the button would tint the
     gutter instead of the card. -->
<button
  type="button"
  class="group w-full touch-manipulation px-4 py-0.5 text-left"
  onclick={onopen}
>
  <!-- `overflow-hidden` IS WHAT MAKES THE RAIL SURVIVE THE CORNER RADIUS, and
       it is the one place this card departs from the transcribed geometry. The
       Figma rail is inset -1px on three sides so it covers the card's own
       border; at a 12px radius a straight 3px bar drawn from -1px pokes visibly
       out of both left corners, because the card's edge has already curved away
       by then. Clipping the card instead puts the rail against the inside of the
       left border and bends it with the corner, which is what the mock looks
       like. The cost is the 1px of `edge-soft` still showing to its left.

       `relative` is the rail's containing block; the shadow is the design's
       literal value (an arbitrary Tailwind value, so it must stay spelled out —
       rule 4). -->
  <div
    class="relative flex flex-col gap-[9px] overflow-hidden rounded-xl border border-edge-soft
           bg-panel px-3.5 py-3 shadow-[0_1px_8px_rgba(0,0,0,0.22)]
           transition-colors group-active:bg-sel"
  >
    {#if wanted}
      <!-- `bg-orange` as a literal rather than through a lookup: this mark
           exists for exactly one status, so a table keyed by status would have
           one entry and would only hide that fact. It IS the shared colour —
           theme.ts answers `text-orange` for needs_input — and it is the same
           one the compact row spends on its left border. -->
      <span class="absolute inset-y-0 left-0 w-[3px] bg-orange" aria-hidden="true"></span>
    {/if}

    <!-- STATUS ROW. The chip leads because on a card it is not parked at the far
         right of a wide row — the objection <SessionRow> raises against pills
         does not apply to a shape that is only as wide as its own text and sits
         at the start of the line it heads. -->
    <div class="flex items-center gap-2">
      <StatusChip status={s.status} />
      {#if note}
        <!-- THE SECOND CHIP IS THE ONE FACT LOLA ITSELF CONTRIBUTES: it is
             retrying CI, and this much budget is left. The design draws it grey
             and dotless, which is right for "ci retry 1/2" — progress, not news.
             `escalated` is news, so it keeps the grey ground (two loud chips
             side by side just cancel out; the status beside it is already
             ci_failed and already broken-toned) and spends the foreground
             instead. The `!` is not optional — a plain `text-bad` ties with the
             chip's own `text-pill-grey-fg` and the winner would be decided by
             sheet order rather than by the class attribute (the Button
             invariant in CLAUDE.md).

             The word is the daemon's own, not the compact row's expansion of it
             ("escalated — lola has spent its CI retries"). That sentence needs a
             line, and this is a chip. -->
        <StatusChip label={note} tone="grey" class={alarm ? "text-bad!" : ""} />
      {/if}
      {#if s.interpretedState}
        <!-- THE INTERPRETER DISAGREEING WITH THE DETERMINISTIC AXIS, and the
             card has to show it for the same reason the compact row does: this
             is the [statusagent] overlay, an untrusted approximation of what the
             agent is ACTUALLY doing, and the chip to its left is what the hooks
             and the pane say. When the two differ that difference is the most
             interesting thing on the card.

             "≈"-marked, exactly as the TUI's statusPillFor marks it, and drawn
             as bare text rather than a third chip — two filled chips reading as
             equals is precisely the claim this must not make. DISPLAY ONLY: it
             reaches no gate, no count and no reaction (CLAUDE.md). -->
        <span class="shrink-0 text-sm text-orange">≈{statusLabel(s.interpretedState)}</span>
      {/if}
      <span class="flex-1"></span>
      <!-- `num` is tabular figures: the age is rewritten on every observer push
           and a proportional "1" would nudge the chips beside it each time. -->
      <span class="num shrink-0 text-sm font-medium text-faint">{s.age}</span>
    </div>

    <!-- TWO LINES, NOT ONE TRUNCATED ONE. At 390 points a single line of an
         issue title is about five words, which is rarely enough to tell two
         tickets apart — and this is the card the user is being asked to act on,
         so it is the worst place in the app to make them open something to find
         out what it is. -->
    <div class="line-clamp-2 text-lg text-ink">{heading}</div>

    {#if activity}
      <!-- Only when there IS prose. <AgentActivity> is `{#if live || text}` —
           pulse first, prose after — so a mid-turn agent with nothing to say
           produced a block containing nothing but a dot; here the same shape
           would leave a lone sparkle floating under the title. The glyph belongs
           to the sentence, so it comes and goes with it.

           `items-start` and not `items-center`: the sentence wraps to two lines
           and the glyph must stay level with the first of them.

           The interpreter's headline is an APPROXIMATION and stays marked as one
           — the same "≈" the TUI's statusPillFor uses and the compact row
           repeats. It is not marked with a colour here, unlike the row: the
           design gives this line one prose tone (`subtext`) and spends the
           accent on the glyph, which already says an agent is talking. -->
      <div class="flex items-start gap-2">
        <span class="mt-px shrink-0 text-accent"><AiGlyph /></span>
        <span class="line-clamp-2 text-body text-subtext">
          {s.headline ? `≈ ${activity}` : activity}
        </span>
      </div>
    {/if}

    <!-- A rule rather than a gap, because what follows is a different KIND of
         fact: everything above is what is happening now, everything below is how
         to find this session again. `h-px` + `bg-edge-soft` rather than a
         border, so it is a flex item that takes the card's inner width. -->
    <div class="h-px w-full bg-edge-soft" aria-hidden="true"></div>

    <div class="flex items-center gap-2">
      <!-- The citation handles. `min-w-0` on the wrapper and `truncate` on the
           PROJECT only: the issue key is seven characters and is the whole point
           of printing it ("NO…" is not a citation), while a project label is
           free text and is the one thing on this line that can be long. The
           separator is drawn in `edge` rather than `faint` so it reads as
           punctuation between two facts rather than as a third one. -->
      <div class="flex min-w-0 items-center gap-[7px]">
        {#if metaIssue}
          <span class="num shrink-0 text-sm font-medium text-faint">{metaIssue}</span>
          <span class="shrink-0 text-sm text-edge" aria-hidden="true">·</span>
        {/if}
        <span class="truncate text-sm text-faint">{projectLabel}</span>
        {#if s.devActive}
          <!-- This session is the one holding the project's dev servers. Only
               one may (they bind ports — see the ACTIVE-session invariant in
               CLAUDE.md), so it is a fact about the session and not about the
               project, and the compact row prints it here too. A mark rather
               than a word: the card has no room for "dev" and the dot is the
               same one the terminal's own toggle wears. -->
          <span class="shrink-0 text-good" aria-label="running this project's dev commands">●</span>
        {/if}
      </div>
      <span class="flex-1"></span>
      {#if s.prNumber > 0}
        <!-- ONLY WHEN THERE IS A PR. <PrBadge> renders a bare em-dash otherwise,
             which is unambiguous under a desktop table's column header and is
             just a mark on a card that has none — an absent PR is clearer as
             absence.

             NO `onclick`, so <MetaPill> stays a <span>. The card is itself a
             <button> and a nested button does not parse: the parser closes the
             outer one and takes the card's own tap with it. Opening the PR is
             the detail screen's job, one tap further in.

             The colour comes from the CHECKS, not from the delivery word — a red
             number is the fastest way to see that the PR that exists is not the
             PR you wanted. -->
        <MetaPill tone={s.checks === "fail" ? "bad" : "magenta"}>
          {#snippet leading()}<BranchIcon />{/snippet}
          #{s.prNumber}
        </MetaPill>
      {/if}
    </div>
  </div>
</button>
