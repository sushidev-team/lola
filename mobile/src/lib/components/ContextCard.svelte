<script lang="ts">
  import MetaPill from "./MetaPill.svelte";
  import AiGlyph from "@mobile/lib/icons/AiGlyph.svelte";
  import { inputReasonLabel } from "$lib/theme";
  import type { SessionInfo } from "$lib/store.svelte";

  // The strip between the pane tabs and the terminal: what the agent is doing,
  // and the two or three facts about this session that a person would otherwise
  // have to go back to the list to find.
  //
  // WHY THE DETAIL SCREEN NEEDS ONE AT ALL. Everything below this card is a
  // terminal — a grid of somebody else's text, roughly fifty columns of a
  // two-hundred-column pane, scrolling. It is the truth and it is unreadable at
  // a glance, which is precisely the reason the list has a status chip and an
  // activity sentence. Tapping into a session used to throw all of that away:
  // the header kept the status word and nothing else, so "why is this one
  // waiting" was answerable only by reading the pane. This card carries the
  // list's own derivations one screen further in.
  //
  // IT RENDERS NOTHING — not an empty card, not a placeholder — WHEN THERE IS
  // NOTHING TO SAY. A card is a box with a border and a shadow's worth of
  // presence, and an empty one directly above a terminal is worse than the ~40
  // points it costs: the pane is the subject of this screen and the chrome above
  // it has to earn every row it takes. That is the commonest state, too — a
  // session with no PR, no interpreter judgement and no notification is most
  // sessions most of the time.
  //
  // NOTHING HERE IS A CONTROL. Every fact is a <MetaPill> with no `onclick`, so
  // it stays a <span>: the actions that correspond to these facts (open the PR,
  // start the dev commands) live behind the header's overflow button, where they
  // can be labelled properly and confirmed. A chip that is sometimes tappable
  // and sometimes not is a worse affordance than one that never is.
  //
  // THE VOCABULARY IS NOT LOCAL, in the sense rule 2 of the brief means: the
  // input reason is `$lib/theme`'s `inputReasonLabel` — the port of Go's
  // state.InputReason that desktop/state_parity_test.go pins — and the check
  // glyphs below are the ones `deliveryGlyph` already spends on the same three
  // outcomes. The only judgement made here is which of the record's fields are
  // worth a chip on a phone.

  let {
    /**
     * The session record, or undefined while the list has not caught up with
     * the pane the screen is attached to. Undefined draws nothing, like every
     * other empty case.
     */
    session,
  }: { session: SessionInfo | undefined } = $props();

  const s = $derived(session);

  /**
   * The agent's own line: the interpreter's headline first, its last
   * notification second.
   *
   * The same source and the same precedence as <SessionCard>'s and the compact
   * row's, deliberately — this is the sentence the user just tapped through
   * from, and a detail screen that paraphrases the list is a detail screen
   * people learn to distrust. The daemon clears `lastNotification` on any
   * transition off waiting_input, so it is never a stale sentence about a turn
   * that has ended.
   *
   * BOTH HALVES ARE UNTRUSTED. They are derived from pane text, which an issue
   * description, a dependency's README or a CI log can write into. They are
   * rendered as TEXT and nowhere else — never as HTML, never as something a
   * link is built from (rule 6 of the brief).
   */
  const activity = $derived(s?.headline || s?.lastNotification || "");

  /**
   * The judgement's age, and ONLY the judgement's.
   *
   * `headlineAgo` is the freshness of the interpreter's sentence, so it is
   * printed only beside that sentence. `SessionInfo.age` is a different fact
   * entirely — how long the session has existed — and putting it here under a
   * "2m ago" shape would read as "this is what the agent said two minutes ago"
   * about a session that has simply been running for two minutes.
   */
  const stamp = $derived(s?.headline && s.headlineAgo ? `${s.headlineAgo} ago` : "");

  /**
   * One fact chip: its text, its foreground and its ground.
   *
   * The tones are <MetaPill>'s own names rather than class strings, so rule 4
   * (Tailwind scans source text; a composed class compiles to nothing) is that
   * component's problem and not restated here.
   */
  type Fact = {
    text: string;
    tone: "good" | "bad" | "grey";
    ground: "sel" | "grey";
  };

  /**
   * The checks rollup, when there is a PR to have one.
   *
   * The glyphs are `deliveryGlyph`'s for the same three outcomes — ✓ for
   * approved, ✕ for ci_failed, ⧗ for ci_pending — rather than new ones. They
   * are all BMP characters a system UI font carries, which is theme.ts's own
   * rule: anything outside it falls back to Apple Color Emoji, which paints its
   * own multi-colour art at 11px and ignores the chip's foreground entirely.
   *
   * `none` and "" draw nothing. A PR with no checks configured is not a fact
   * about this session, and "no checks" reads as a failure of the app to find
   * them.
   */
  const checks = $derived.by<Fact | undefined>(() => {
    switch (s?.checks) {
      case "pass":
        return { text: "✓ CI pass", tone: "good", ground: "sel" };
      case "fail":
        return { text: "✕ CI fail", tone: "bad", ground: "sel" };
      case "pending":
        return { text: "⧗ CI running", tone: "grey", ground: "grey" };
      default:
        return undefined;
    }
  });

  /**
   * WHY the agent is blocked, when it is.
   *
   * This is the one fact on the screen that turns a status into an
   * instruction — "needs you" says a person is required, "permission prompt"
   * says what pressing into the pane will actually ask. `inputReasonLabel`
   * answers "" for everything outside the four answerable reasons, including
   * the historical `idle_notification` that a pre-split snapshot can still
   * carry, so a record from that era shows no chip rather than an explanation
   * that is no longer true.
   */
  const reason = $derived(inputReasonLabel(s?.inputReason));

  /**
   * The facts, in the order they are drawn: what the PR is doing, why the agent
   * is stopped, and whether this session holds its project's dev servers.
   *
   * The dev chip is the one whose ABSENCE is the interesting case. Only one
   * session per project may run the dev commands, so "this is the active one"
   * is a fact about a shared resource rather than about this session alone —
   * and it is the fact behind the toggle in the overflow menu, which is
   * otherwise the only place on this screen that says which state it is in.
   */
  const facts = $derived.by<Fact[]>(() => {
    const out: Fact[] = [];
    if (checks) out.push(checks);
    if (reason) out.push({ text: reason, tone: "grey", ground: "grey" });
    if (s?.devActive) out.push({ text: "dev running", tone: "good", ground: "sel" });
    return out;
  });

  /** Nothing to say. See the header: an empty card is worse than no card. */
  const empty = $derived(activity === "" && facts.length === 0);
</script>

{#if !empty}
  <!-- `shrink-0` in the screen's flex column, so the card's height comes off
       the terminal's `flex-1` rather than pushing the accessory bar off the
       bottom of the screen — the same rule the tab strip above it follows. -->
  <div class="shrink-0 px-3 pt-2.5 pb-2" data-context-card="true">
    <!-- `data-context-card` is a TEST HOOK and is the only one in this file.
         The contract this component is built around — nothing at all rather
         than an empty card — cannot be asserted by looking for text, because
         an empty card has no text either; the two states are only
         distinguishable by whether the element exists. It carries no styling
         and nothing in the app reads it. -->
    <div
      class="flex flex-col gap-2 rounded-[10px] border border-edge-soft bg-panel px-3 py-2.5"
    >
      {#if activity}
        <!-- `items-start` and not `items-center`: the sentence wraps and the
             glyph has to stay level with its first line.

             TWO LINES AT MOST. This is a note above a terminal, not the card
             the list draws to be read — an interpreter headline that ran to
             five lines would push the pane down by a third of the screen, and
             the pane is the reason the screen exists. The full sentence is in
             the list, one tap back.

             The interpreter's headline is an APPROXIMATION and stays marked as
             one — the same "≈" the TUI's statusPillFor uses and both list
             components repeat. Like <SessionCard>, and unlike the compact row,
             it is not ALSO given a colour: the design spends one prose tone
             (`subtext`) on this line and puts the accent on the glyph, which
             already says that an agent is talking. -->
        <div class="flex items-start gap-2">
          <span class="mt-px shrink-0 text-accent"><AiGlyph /></span>
          <span class="line-clamp-2 text-body text-subtext">
            {s?.headline ? `≈ ${activity}` : activity}
          </span>
        </div>
      {/if}

      {#if facts.length > 0 || stamp}
        <!-- `flex-wrap` is this file's own, not the design's: the frame draws
             two chips on one line and says nothing about three at an
             accessibility text size, where "permission prompt" alone is wider
             than the screen. Wrapping is the only degradation that keeps every
             fact; truncating a chip would leave "permission pro…", which is
             not a fact.

             `ml-auto` rather than a `flex-1` spacer element, for the reason the
             compact row gives: on a wrapped line an auto margin keeps the stamp
             right-aligned on whichever line it lands on, where a spacer would
             strand it at the left of the second one. -->
        <div class="flex flex-wrap items-center gap-1.5">
          {#each facts as f (f.text)}
            <MetaPill tone={f.tone} ground={f.ground}>{f.text}</MetaPill>
          {/each}
          {#if stamp}
            <!-- `num` for the same reason every age in this app carries it: the
                 stamp is rewritten on every observer push, and a proportional
                 "1" would nudge the chips beside it each time one arrived. -->
            <span class="num ml-auto shrink-0 text-sm text-faint">{stamp}</span>
          {/if}
        </div>
      {/if}
    </div>
  </div>
{/if}
