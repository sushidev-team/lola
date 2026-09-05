<script lang="ts" module>
  // The soft, dot-led status chip — the design's primary carrier of "what is
  // happening" on a hero card and in the session detail's identity row.
  //
  // WHY A CHIP AGAIN, AFTER SessionRow DROPPED ONE. That component's comment
  // argues at length against a pill, and it is still right about the case it
  // describes: a saturated `bg-bad` badge parked at the far right of a COMPACT
  // row put the one word that says what is happening as far from the sentence
  // it modifies as the row is wide. Nothing here reverses that — the compact
  // row keeps its bare word and its LivePulse. This chip is for the two places
  // the design draws one instead: the hero card, where the chip LEADS its own
  // row and the card is the only thing on screen, and the detail header, where
  // it sits beside the issue key it qualifies. The grounds are soft tints
  // (`pill-*-soft`) rather than the desktop's saturated fills for exactly the
  // reason that comment gives: at 393 points a filled badge is the loudest
  // thing on the screen whether or not it is the most important.
  //
  // THE VOCABULARY IS NOT LOCAL. The label is `statusLabel` and the family is
  // `pillKind`, both from `$lib/theme` — the port of Go's internal/state that
  // desktop/state_parity_test.go pins. The only phone-side decision is which of
  // the six pill kinds collapse into which of the three grounds below, and that
  // collapse is stated once, here.

  /**
   * The three grounds, as LITERAL class strings.
   *
   * Rule 4 of the brief, and the Button invariant in CLAUDE.md: Tailwind v4
   * scans source TEXT, so a composed `bg-pill-${kind}-soft` compiles to
   * nothing. The failure is silent — a transparent chip, no error anywhere —
   * which is why the map is spelled out rather than assembled.
   */
  const GROUNDS = {
    /** A human is blocked on this session. The reason the app exists. */
    urgent: "bg-pill-urgent-soft text-pill-urgent-soft-fg",
    /** Something is broken and lola is (or should be) reacting to it. */
    broken: "bg-pill-broken-soft text-pill-broken-soft-fg",
    /** True, and not news: everything else, plus a caller's own text. */
    grey: "bg-pill-grey text-pill-grey-fg",
  } as const;

  export type ChipTone = keyof typeof GROUNDS;
</script>

<script lang="ts">
  import { pillKind, statusLabel } from "$lib/theme";

  let {
    /** The rolled-up status word. Drives both the tone and the label. */
    status = "",
    /**
     * Text to print instead of `statusLabel(status)` — the design's second
     * chip on a hero card ("retry 1/2"), which states a fact about the
     * session that the status vocabulary has no word for. A caller passing
     * this passes `tone` too; there is no status to derive one from.
     */
    label = "",
    /**
     * Force a ground. "auto" derives it from `status`, which is what every
     * status-carrying call site wants.
     */
    tone = "auto",
    class: klass = "",
  }: {
    status?: string;
    label?: string;
    tone?: ChipTone | "auto";
    /**
     * Appended last. Overriding one of the chip's OWN utilities from here
     * needs Tailwind's trailing `!` — a plain `px-2` ties with `pr-2` on
     * specificity and the winner is decided by sheet order, not by the class
     * attribute (the Button invariant in CLAUDE.md).
     */
    class?: string;
  } = $props();

  /**
   * Six pill kinds down to three grounds.
   *
   * `urgent` and `broken` together are EXACTLY theme.ts's `ATTENTION_STATUSES`
   * — needs_input, ci_failed, changes_requested, merge_conflict — so this
   * collapse says the same thing `isAttention` does while keeping the two
   * halves apart, which `isAttention` cannot: being blocked on a human and
   * being broken are different colours in this design. The test pins that
   * equivalence, because it is the property that would silently drift if a
   * future status joined one set and not the other.
   *
   * Everything else — working, review_pending, approved, merged, idle, and any
   * word a newer daemon invents — is grey and dotless. That is the same
   * judgement `statustone.ts` makes for the bare word: a quiet status is true
   * and not news, and a phone that shouts about all seven of them says nothing
   * about the one that matters.
   */
  function toneFor(s: string): ChipTone {
    switch (pillKind(s)) {
      case "urgent":
        return "urgent";
      case "broken":
        return "broken";
      default:
        return "grey";
    }
  }

  const kind = $derived<ChipTone>(tone === "auto" ? toneFor(status) : tone);
  const text = $derived(label || statusLabel(status));
</script>

<!-- UPPERCASE IS CSS, NOT THE STRING. `text-xs` is the design's 10/12 bold
     tracked label step and it deliberately does not carry `text-transform` (see
     app.css), so the transform is added here — and only here. Uppercasing in JS
     would put "NEEDS YOU" in the accessibility tree, where a screen reader
     reads capitals as an initialism and spells some of them out. The DOM keeps
     theme.ts's own spelling; only the pixels are shouted.

     The dot is `bg-current`, so it is the chip's foreground by construction and
     can never drift from it — and it is absent on grey by rule: a dot on a
     quiet chip is a lit indicator saying nothing, which is how six of them in a
     list stop meaning anything. -->
<span
  class="inline-flex shrink-0 items-center gap-[5px] rounded-md py-[3px] pr-2 pl-[7px]
         text-xs uppercase {GROUNDS[kind]} {klass}"
>
  {#if kind !== "grey"}
    <span class="h-[5px] w-[5px] shrink-0 rounded-full bg-current" aria-hidden="true"></span>
  {/if}
  {text}
</span>
